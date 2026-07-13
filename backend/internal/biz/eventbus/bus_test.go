package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/go-kratos/kratos/v2/log"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/adcwb/ai-gateway/internal/data/model"
)

type fakeSink struct {
	name string

	mu         sync.Mutex
	failFirstN int
	calls      int
	delivered  []Event
}

func (s *fakeSink) Name() string { return s.name }

func (s *fakeSink) Deliver(_ context.Context, events []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls <= s.failFirstN {
		return fmt.Errorf("simulated failure #%d", s.calls)
	}
	s.delivered = append(s.delivered, events...)
	return nil
}

func (s *fakeSink) deliveredCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.delivered)
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.AIEventLogEntry{}, &model.AIEventCursor{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	waitForWithin(t, 3*time.Second, cond)
}

func waitForWithin(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func TestBus_PublishAndDeliver(t *testing.T) {
	db := newTestDB(t)
	sink := &fakeSink{name: "test-sink"}
	bus := NewBus(db, []Sink{sink}, log.NewStdLogger(testWriter{t}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus.Start(ctx)

	bus.Publish("audit", 1, map[string]string{"k": "v1"})
	bus.Publish("billing", 2, map[string]string{"k": "v2"})

	waitFor(t, func() bool { return sink.deliveredCount() >= 2 })

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.delivered) != 2 {
		t.Fatalf("expected exactly 2 delivered events, got %d", len(sink.delivered))
	}
	var payload map[string]string
	if err := json.Unmarshal(sink.delivered[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["k"] != "v1" {
		t.Fatalf("unexpected first payload: %+v", payload)
	}
	if sink.delivered[0].EventType != "audit" || sink.delivered[0].TenantID != 1 {
		t.Fatalf("unexpected event metadata: %+v", sink.delivered[0])
	}
}

func TestBus_RetriesUntilSinkSucceeds(t *testing.T) {
	db := newTestDB(t)
	sink := &fakeSink{name: "flaky-sink", failFirstN: 2}
	bus := NewBus(db, []Sink{sink}, log.NewStdLogger(testWriter{t}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus.Start(ctx)

	bus.Publish("audit", 1, map[string]string{"k": "v1"})

	// Exponential backoff (pollBackoffMin=1s, doubling) means two failures
	// before the third (successful) attempt take ~1s+2s — give this one
	// longer than the default waitFor budget.
	waitForWithin(t, 8*time.Second, func() bool { return sink.deliveredCount() >= 1 })

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.delivered) != 1 {
		t.Fatalf("expected the event to be delivered exactly once after retries, got %d", len(sink.delivered))
	}
	if sink.calls < 3 {
		t.Fatalf("expected at least 3 delivery attempts (2 failures + 1 success), got %d", sink.calls)
	}
}

func TestBus_CursorSurvivesRestart(t *testing.T) {
	db := newTestDB(t)
	sink1 := &fakeSink{name: "resumable-sink"}
	bus1 := NewBus(db, []Sink{sink1}, log.NewStdLogger(testWriter{t}))

	ctx1, cancel1 := context.WithCancel(context.Background())
	bus1.Start(ctx1)
	bus1.Publish("audit", 1, map[string]string{"k": "first"})
	waitFor(t, func() bool { return sink1.deliveredCount() >= 1 })
	cancel1() // simulate the process stopping

	// A brand new Bus + Sink instance reading the same DB simulates a
	// restart. It must not redeliver the first event, and must pick up
	// where the cursor (ai_event_cursors) left off for anything published
	// afterward.
	sink2 := &fakeSink{name: "resumable-sink"} // same sink name = same cursor row
	bus2 := NewBus(db, []Sink{sink2}, log.NewStdLogger(testWriter{t}))
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	bus2.Start(ctx2)

	bus2.Publish("audit", 1, map[string]string{"k": "second"})
	waitFor(t, func() bool { return sink2.deliveredCount() >= 1 })

	// give it a moment to make sure no extra (duplicate) delivery shows up
	time.Sleep(100 * time.Millisecond)

	sink2.mu.Lock()
	defer sink2.mu.Unlock()
	if len(sink2.delivered) != 1 {
		t.Fatalf("expected only the post-restart event to be delivered to the new sink instance, got %d: %+v", len(sink2.delivered), sink2.delivered)
	}
	var payload map[string]string
	json.Unmarshal(sink2.delivered[0].Payload, &payload)
	if payload["k"] != "second" {
		t.Fatalf("expected the post-restart sink to see only the 'second' event, got %+v", payload)
	}
}
