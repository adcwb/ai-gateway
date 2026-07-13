package eventbus

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/adcwb/ai-gateway/internal/data/model"
)

const (
	logBatchSize   = 100
	logFlushEvery  = 200 * time.Millisecond
	pollInterval   = 500 * time.Millisecond
	pollBatchSize  = 100
	pollBackoffMin = time.Second
	pollBackoffMax = 30 * time.Second
)

type logTask struct {
	eventType string
	tenantID  uint
	payload   []byte
}

// Bus is the durable event log + per-sink delivery loop (docs/design/09-
// extensibility.md "Event bus"). Publish is a non-blocking channel send —
// mirrors BillingManager.Settle's ledgerQ — so a slow or down sink can never
// add latency to the request path; the durable log (ai_event_log) plus a
// per-sink cursor (ai_event_cursors) is what makes delivery resumable after
// a crash, unlike a pure in-memory channel.
type Bus struct {
	db     *gorm.DB
	logger *log.Helper
	logQ   chan logTask
	sinks  []Sink
}

// NewBus builds a Bus. sinks may be empty (events are still durably logged,
// just never delivered anywhere) — matching every other optional integration
// in this codebase (ES, OTel): configuring nothing costs nothing extra.
func NewBus(db *gorm.DB, sinks []Sink, logger log.Logger) *Bus {
	return &Bus{db: db, sinks: sinks, logger: log.NewHelper(logger), logQ: make(chan logTask, 4096)}
}

// Publish enqueues one event for durable logging and eventual sink delivery.
// payload is JSON-marshaled here so a slow marshal never blocks the caller
// past this one call — the queue send itself never blocks (drop + log on a
// full queue, same "fail open on economics" posture as billing's ledgerQ).
func (b *Bus) Publish(eventType string, tenantID uint, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		b.logger.Errorf("eventbus: 序列化事件失败 type=%s err=%v", eventType, err)
		return
	}
	select {
	case b.logQ <- logTask{eventType: eventType, tenantID: tenantID, payload: raw}:
	default:
		b.logger.Errorf("eventbus: 队列已满，丢弃事件 type=%s tenant=%d — 等待对账修复", eventType, tenantID)
	}
}

// Start launches the batch-insert worker and one poller goroutine per sink.
func (b *Bus) Start(ctx context.Context) {
	go b.logWorker(ctx)
	for _, s := range b.sinks {
		go b.sinkPoller(ctx, s)
	}
}

// logWorker drains logQ into ai_event_log in small batches — mirrors
// AuditWorker.processBatch's batch-then-flush shape.
func (b *Bus) logWorker(ctx context.Context) {
	batch := make([]model.AIEventLogEntry, 0, logBatchSize)
	ticker := time.NewTicker(logFlushEvery)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := b.db.WithContext(context.Background()).Create(&batch).Error; err != nil {
			b.logger.Errorf("eventbus: 事件日志落库失败 err=%v", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case task := <-b.logQ:
			batch = append(batch, model.AIEventLogEntry{
				EventID:   generateEventID(),
				EventType: task.eventType,
				TenantID:  task.tenantID,
				Payload:   datatypes.JSON(task.payload),
				V:         1,
			})
			if len(batch) >= logBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// sinkPoller reads ai_event_log rows after sink's cursor, delivers them in
// batches, and advances the cursor only on success — at-least-once, with
// exponential backoff on delivery failure (mirrors audit.go's esRetryWorker).
func (b *Bus) sinkPoller(ctx context.Context, sink Sink) {
	backoff := pollBackoffMin
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		cursor := b.loadCursor(ctx, sink.Name())
		var rows []model.AIEventLogEntry
		if err := b.db.WithContext(ctx).Where("id > ?", cursor).Order("id asc").Limit(pollBatchSize).Find(&rows).Error; err != nil {
			b.logger.Errorf("eventbus: 读取事件日志失败 sink=%s err=%v", sink.Name(), err)
			if !sleepOrDone(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		if len(rows) == 0 {
			if !sleepOrDone(ctx, pollInterval) {
				return
			}
			continue
		}

		events := make([]Event, len(rows))
		for i, r := range rows {
			events[i] = Event{ID: r.ID, EventID: r.EventID, EventType: r.EventType, TenantID: r.TenantID, Payload: json.RawMessage(r.Payload), V: r.V}
		}
		if err := sink.Deliver(ctx, events); err != nil {
			b.logger.Errorf("eventbus: sink 投递失败 sink=%s err=%v，将退避重试", sink.Name(), err)
			if !sleepOrDone(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		backoff = pollBackoffMin
		b.saveCursor(ctx, sink.Name(), rows[len(rows)-1].ID)
	}
}

func nextBackoff(cur time.Duration) time.Duration {
	next := cur * 2
	if next > pollBackoffMax {
		return pollBackoffMax
	}
	return next
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (b *Bus) loadCursor(ctx context.Context, sinkName string) uint {
	var c model.AIEventCursor
	if err := b.db.WithContext(ctx).Where("sink_name = ?", sinkName).First(&c).Error; err != nil {
		return 0
	}
	return c.LastEventID
}

func (b *Bus) saveCursor(ctx context.Context, sinkName string, lastEventID uint) {
	c := model.AIEventCursor{SinkName: sinkName, LastEventID: lastEventID}
	err := b.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "sink_name"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_event_id", "updated_at"}),
	}).Create(&c).Error
	if err != nil {
		b.logger.Errorf("eventbus: 保存 sink 游标失败 sink=%s err=%v", sinkName, err)
	}
}
