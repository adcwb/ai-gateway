package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/bulk"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

const (
	auditBatchWorkerCount = 4
	auditQueueSize        = 8192
	auditBatchSize        = 100
	auditBatchTimeout     = 200 * time.Millisecond
	auditEnqueueBlockWait = 200 * time.Millisecond
	retryQueueSize        = 2048
	esAuditIndexName      = "ai_audit_log_bodies_v2"
	esMaxRetryAttempts    = 3
	esBodyIndexMaxBytes   = 256 << 10

	auditResyncInterval  = 60 * time.Second
	auditResyncBatchSize = 200

	auditSpillKey        = "ai:gw:audit:spill"
	auditSpillMySQLRetry = 3
	auditDrainBatchSize  = 200
	auditDrainIdleSleep  = 2 * time.Second
)

type esAuditBody struct {
	AuditLogID   uint      `json:"audit_log_id"`
	VirtualKeyID uint      `json:"virtual_key_id"`
	ProviderID   uint      `json:"provider_id"`
	Model        string    `json:"model"`
	RequestBody  string    `json:"request_body"`
	ResponseBody string    `json:"response_body"`
	CreatedAt    time.Time `json:"created_at"`
}

type esBulkItem struct {
	id           uint
	virtualKeyID uint
	providerID   uint
	model        string
	requestBody  string
	responseBody string
	createdAt    time.Time
}

type esRetryTask struct {
	items   []esBulkItem
	attempt int
}

// AuditWorker batches audit logs and writes them to MySQL + ES.
type AuditWorker struct {
	db          *gorm.DB
	rdb         *redis.Client
	es          *elasticsearch.TypedClient
	logger      *log.Helper
	auditQueue  chan model.AIGatewayAuditLog
	esRetryQ    chan esRetryTask
}

// NewAuditWorker creates an AuditWorker wired with DI dependencies.
func NewAuditWorker(db *gorm.DB, rdb *redis.Client, esClient *elasticsearch.TypedClient, logger log.Logger) *AuditWorker {
	return &AuditWorker{
		db:         db,
		rdb:        rdb,
		es:         esClient,
		logger:     log.NewHelper(logger),
		auditQueue: make(chan model.AIGatewayAuditLog, auditQueueSize),
		esRetryQ:   make(chan esRetryTask, retryQueueSize),
	}
}

// Start launches all background goroutines. Must be called once after construction.
func (w *AuditWorker) Start(ctx context.Context) {
	for i := 0; i < auditBatchWorkerCount; i++ {
		go w.auditBatchWorker(ctx)
	}
	go w.esRetryWorker(ctx)
	go w.auditSpillDrainWorker(ctx)
	go w.esResyncWorker(ctx)
}

// Enqueue submits an audit log for async batch writing.
func (w *AuditWorker) Enqueue(entry model.AIGatewayAuditLog) {
	select {
	case w.auditQueue <- entry:
		return
	default:
	}

	t := time.NewTimer(auditEnqueueBlockWait)
	defer t.Stop()
	select {
	case w.auditQueue <- entry:
		return
	case <-t.C:
	}

	w.logger.Warnf("AI 审计日志内存队列已满，溢出到 Redis 持久化队列 virtualKeyID=%d model=%s",
		entry.VirtualKeyID, entry.Model)
	w.spillAuditLogs([]model.AIGatewayAuditLog{entry})
}

func (w *AuditWorker) auditBatchWorker(ctx context.Context) {
	ticker := time.NewTicker(auditBatchTimeout)
	defer ticker.Stop()
	batch := make([]model.AIGatewayAuditLog, 0, auditBatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		toProcess := make([]model.AIGatewayAuditLog, len(batch))
		copy(toProcess, batch)
		batch = batch[:0]
		w.processBatch(ctx, toProcess)
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case entry := <-w.auditQueue:
			batch = append(batch, entry)
			if len(batch) >= auditBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (w *AuditWorker) processBatch(ctx context.Context, logs []model.AIGatewayAuditLog) {
	requestBodies := make([]string, len(logs))
	responseBodies := make([]string, len(logs))
	for i, l := range logs {
		requestBodies[i] = l.RequestBody
		responseBodies[i] = l.ResponseBody
	}

	var writeErr error
	for attempt := 0; attempt < auditSpillMySQLRetry; attempt++ {
		writeErr = w.insertAuditLogsWithBodies(ctx, logs)
		if writeErr == nil {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	if writeErr != nil {
		w.logger.Warnf("事务写入 AI 审计日志失败，溢出到 Redis 持久化队列 err=%v count=%d", writeErr, len(logs))
		w.spillAuditLogs(logs)
		return
	}

	items := make([]esBulkItem, 0, len(logs))
	for i, l := range logs {
		if l.ID == 0 {
			continue
		}
		items = append(items, esBulkItem{
			id:           l.ID,
			virtualKeyID: l.VirtualKeyID,
			providerID:   l.ProviderID,
			model:        l.Model,
			requestBody:  truncateForESIndex(requestBodies[i]),
			responseBody: truncateForESIndex(responseBodies[i]),
			createdAt:    l.CreatedAt,
		})
	}

	if len(items) == 0 {
		return
	}
	if err := w.esBulkIndex(ctx, items); err != nil {
		w.logger.Warnf("ES 批量写入失败，进入重试队列 err=%v count=%d", err, len(items))
		select {
		case w.esRetryQ <- esRetryTask{items: items, attempt: 1}:
		default:
			w.logger.Warnf("ES 重试队列已满，留待补偿 worker 同步 count=%d", len(items))
			w.markESFailed(items)
		}
	} else {
		w.markESSynced(ctx, items)
	}
}

func (w *AuditWorker) insertAuditLogsWithBodies(ctx context.Context, logs []model.AIGatewayAuditLog) error {
	if len(logs) == 0 {
		return nil
	}
	reqs := make([]string, len(logs))
	resps := make([]string, len(logs))
	for i := range logs {
		reqs[i] = logs[i].RequestBody
		resps[i] = logs[i].ResponseBody
		logs[i].ID = 0
	}
	return w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.CreateInBatches(&logs, auditBatchSize).Error; err != nil {
			return err
		}
		bodies := make([]model.AIGatewayAuditLogBody, 0, len(logs))
		for i, l := range logs {
			if l.ID == 0 {
				continue
			}
			bodies = append(bodies, model.AIGatewayAuditLogBody{
				AuditLogID:   l.ID,
				VirtualKeyID: l.VirtualKeyID,
				ProviderID:   l.ProviderID,
				Model:        l.Model,
				RequestBody:  reqs[i],
				ResponseBody: resps[i],
				ESSynced:     false,
				CreatedAt:    l.CreatedAt,
			})
		}
		if len(bodies) == 0 {
			return nil
		}
		return tx.Omit("AuditLog").CreateInBatches(&bodies, auditBatchSize).Error
	})
}

func (w *AuditWorker) esBulkIndex(ctx context.Context, items []esBulkItem) error {
	if w.es == nil {
		return fmt.Errorf("ES 客户端未初始化")
	}

	ops := make(bulk.Request, 0, len(items)*2)
	for _, item := range items {
		docID := strconv.FormatUint(uint64(item.id), 10)
		ops = append(ops, map[string]any{
			"index": map[string]any{"_id": docID},
		})
		ops = append(ops, esAuditBody{
			AuditLogID:   item.id,
			VirtualKeyID: item.virtualKeyID,
			ProviderID:   item.providerID,
			Model:        item.model,
			RequestBody:  item.requestBody,
			ResponseBody: item.responseBody,
			CreatedAt:    item.createdAt,
		})
	}

	res, err := w.es.Bulk().Index(esAuditIndexName).Request(&ops).Do(ctx)
	if err != nil {
		return fmt.Errorf("ES bulk 请求失败: %w", err)
	}

	if res.Errors {
		failCount := 0
		for _, it := range res.Items {
			for _, op := range it {
				if op.Error != nil {
					failCount++
				}
			}
		}
		if failCount > 0 {
			return fmt.Errorf("ES bulk 部分失败: %d/%d 条写入错误", failCount, len(items))
		}
	}
	return nil
}

func (w *AuditWorker) esRetryWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task, ok := <-w.esRetryQ:
			if !ok {
				return
			}
			wait := time.Duration(1<<uint(task.attempt-1)) * time.Second
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}

			if err := w.esBulkIndex(ctx, task.items); err != nil {
				if task.attempt < esMaxRetryAttempts {
					next := esRetryTask{items: task.items, attempt: task.attempt + 1}
					select {
					case w.esRetryQ <- next:
					default:
						w.markESFailed(task.items)
					}
				} else {
					w.logger.Errorf("ES 写入达到最大重试次数，留待补偿 worker 同步 count=%d err=%v",
						len(task.items), err)
					w.markESFailed(task.items)
				}
			} else {
				w.markESSynced(ctx, task.items)
			}
		}
	}
}

func (w *AuditWorker) markESSynced(ctx context.Context, items []esBulkItem) {
	if len(items) == 0 {
		return
	}
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.id)
	}
	if err := w.db.WithContext(ctx).Model(&model.AIGatewayAuditLogBody{}).
		Where("audit_log_id IN ?", ids).
		Update("es_synced", true).Error; err != nil {
		w.logger.Errorf("更新 body 表 es_synced 失败: %v", err)
	}
}

func (w *AuditWorker) markESFailed(items []esBulkItem) {
	w.logger.Warnf("ES body 同步失败，保持 es_synced=false 待补偿同步 count=%d", len(items))
}

func (w *AuditWorker) spillAuditLogs(logs []model.AIGatewayAuditLog) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payloads := make([]interface{}, 0, len(logs))
	for i := range logs {
		data, err := json.Marshal(logs[i])
		if err != nil {
			w.logger.Errorf("审计日志序列化失败，转同步直写 err=%v", err)
			w.directWriteAuditLog(ctx, logs[i])
			continue
		}
		payloads = append(payloads, data)
	}
	if len(payloads) == 0 {
		return
	}
	if w.rdb == nil {
		for i := range logs {
			w.directWriteAuditLog(ctx, logs[i])
		}
		return
	}
	if err := w.rdb.RPush(ctx, auditSpillKey, payloads...).Err(); err != nil {
		w.logger.Errorf("审计日志溢出 Redis 失败，转同步直写 MySQL err=%v", err)
		for i := range logs {
			w.directWriteAuditLog(ctx, logs[i])
		}
	}
}

func (w *AuditWorker) directWriteAuditLog(ctx context.Context, entry model.AIGatewayAuditLog) {
	logs := []model.AIGatewayAuditLog{entry}
	for attempt := 0; attempt < auditSpillMySQLRetry; attempt++ {
		if err := w.insertAuditLogsWithBodies(ctx, logs); err == nil {
			return
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	w.logger.Errorf("审计日志最终兜底直写 MySQL 仍失败 virtualKeyID=%d", entry.VirtualKeyID)
}

func (w *AuditWorker) auditSpillDrainWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if w.rdb == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(auditDrainIdleSleep):
				continue
			}
		}

		raw, err := w.rdb.LPopCount(ctx, auditSpillKey, auditDrainBatchSize).Result()
		if err != nil || len(raw) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(auditDrainIdleSleep):
			}
			continue
		}

		logs := make([]model.AIGatewayAuditLog, 0, len(raw))
		for _, s := range raw {
			var l model.AIGatewayAuditLog
			if jerr := json.Unmarshal([]byte(s), &l); jerr != nil {
				w.logger.Errorf("溢出审计日志反序列化失败，丢弃该条 err=%v", jerr)
				continue
			}
			l.ID = 0
			logs = append(logs, l)
		}
		if len(logs) == 0 {
			continue
		}

		writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		werr := w.insertAuditLogsWithBodies(writeCtx, logs)
		cancel()
		if werr != nil {
			w.logger.Warnf("回灌溢出审计日志失败，退回队列重试 err=%v count=%d", werr, len(raw))
			reload := make([]interface{}, 0, len(raw))
			for _, s := range raw {
				reload = append(reload, s)
			}
			if perr := w.rdb.LPush(ctx, auditSpillKey, reload...).Err(); perr != nil {
				w.logger.Errorf("退回溢出审计日志也失败，转同步直写兜底 err=%v", perr)
				for i := range logs {
					w.directWriteAuditLog(ctx, logs[i])
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(auditDrainIdleSleep):
			}
		}
	}
}

func (w *AuditWorker) esResyncWorker(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}

	ticker := time.NewTicker(auditResyncInterval)
	defer ticker.Stop()
	for {
		w.resyncPendingBodies(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *AuditWorker) resyncPendingBodies(ctx context.Context) {
	if w.es == nil {
		return
	}
	for {
		var bodies []model.AIGatewayAuditLogBody
		if err := w.db.WithContext(ctx).
			Where("es_synced = ?", false).
			Order("audit_log_id asc").
			Limit(auditResyncBatchSize).
			Find(&bodies).Error; err != nil {
			w.logger.Warnf("扫描待同步 ES 的审计 body 失败 err=%v", err)
			return
		}
		if len(bodies) == 0 {
			return
		}
		items := make([]esBulkItem, 0, len(bodies))
		for _, b := range bodies {
			items = append(items, esBulkItem{
				id:           b.AuditLogID,
				virtualKeyID: b.VirtualKeyID,
				providerID:   b.ProviderID,
				model:        b.Model,
				requestBody:  truncateForESIndex(b.RequestBody),
				responseBody: truncateForESIndex(b.ResponseBody),
				createdAt:    b.CreatedAt,
			})
		}
		if err := w.esBulkIndex(ctx, items); err != nil {
			w.logger.Warnf("补偿同步 ES body 失败，下一周期重试 count=%d err=%v", len(items), err)
			return
		}
		w.markESSynced(ctx, items)
		w.logger.Infof("补偿同步 ES body 成功 count=%d", len(items))
		if len(bodies) < auditResyncBatchSize {
			return
		}
	}
}

func truncateForESIndex(s string) string {
	if len(s) <= esBodyIndexMaxBytes {
		return s
	}
	return strings.ToValidUTF8(s[:esBodyIndexMaxBytes], "")
}

// EnsureAuditFileObjectInDB ensures an audit file object record exists in the DB using ON CONFLICT DO NOTHING.
func EnsureAuditFileObjectInDB(ctx context.Context, db *gorm.DB, hash string, f model.ExtractedFile) error {
	var cnt int64
	if err := db.WithContext(ctx).Model(&model.AIGatewayAuditFileObject{}).
		Where("hash = ?", hash).Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return nil
	}
	obj := model.AIGatewayAuditFileObject{
		Hash:      hash,
		OSSKey:    "ai-audit/" + hash,
		MimeType:  f.MimeType,
		SizeBytes: f.SizeBytes,
	}
	return db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).Create(&obj).Error
}
