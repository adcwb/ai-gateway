// Package eventbus implements the async half of extensibility (docs/design/
// 09-extensibility.md "Event bus"): on_audit and on_billing generalize into
// one internal, durable event stream with pluggable sinks (webhook, Kafka).
// It is dependency-free with respect to package biz — like internal/biz/
// guardrail and internal/biz/mcpgw — but, like internal/biz/vectorindex, it
// does talk directly to gorm/data/model for its own durable log table.
package eventbus

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"sync/atomic"
)

// Event is what a Sink receives — the durable log row, decoded for
// consumption.
type Event struct {
	ID        uint // ai_event_log.id; also the cursor position
	EventID   string
	EventType string // "audit" | "billing"
	TenantID  uint
	Payload   json.RawMessage
	V         int
}

// Sink delivers a batch of events somewhere outside the process. Deliver
// must be idempotent-tolerant — at-least-once delivery means a sink may see
// the same event again after a crash before its cursor was advanced.
type Sink interface {
	Name() string
	Deliver(ctx context.Context, events []Event) error
}

// generateEventID is a process-random-prefix + monotonic-counter ID —
// not a strict ULID, but sortable-enough and unique-enough for the
// consumer-side idempotency the design doc asks for, without adding a
// dedicated ULID dependency (mirrors internal/biz/request_id.go's pattern).
var eventIDPrefix = func() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "e0"
	}
	return "e" + strconv.FormatUint(binary.BigEndian.Uint64(b), 36)
}()

var eventIDCounter atomic.Uint64

func generateEventID() string {
	seq := eventIDCounter.Add(1)
	return eventIDPrefix + "-" + strconv.FormatUint(seq, 36)
}
