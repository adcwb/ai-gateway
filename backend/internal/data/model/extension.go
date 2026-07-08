package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Hook kinds an AIExtension can be (docs/design/09-extensibility.md
// "Delivery mechanisms"): (a) compile-time hooks never appear as a row here
// — they're registered directly via internal/biz/extension.Register — this
// table is only the two rebuild-free mechanisms, webhook and WASM.
const (
	ExtensionKindWebhook = "webhook"
	ExtensionKindWasm    = "wasm"
)

const (
	ExtensionFailModeOpen   = "open"
	ExtensionFailModeClosed = "closed"
)

// AIExtension registers a webhook- or WASM-backed pre_request/post_response
// hook (docs/design/09-extensibility.md "MCP gateway"... "Delivery
// mechanisms"). Mirrors AIMCPServer's shape deliberately: global object,
// platform-admin managed, secret encrypted at rest.
type AIExtension struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	Name string `gorm:"type:varchar(64);not null;uniqueIndex" json:"name"`
	Kind string `gorm:"type:varchar(16);not null" json:"kind"` // webhook | wasm

	// Hooks is a JSON array of hook point names ("pre_request","post_response").
	Hooks datatypes.JSON `gorm:"column:hooks;type:json" json:"hooks"`

	URL        string `gorm:"column:url;type:varchar(512)" json:"url"`            // webhook only
	HMACSecret string `gorm:"column:hmac_secret;type:varchar(512)" json:"-"`      // webhook only, AES-256-GCM at rest
	WasmPath   string `gorm:"column:wasm_path;type:varchar(512)" json:"wasmPath"` // wasm only, local file path

	FailMode  string `gorm:"column:fail_mode;type:varchar(8);default:'open'" json:"failMode"`
	TenantID  uint   `gorm:"column:tenant_id;index;default:0" json:"tenantId"` // 0 = all tenants
	TimeoutMs int    `gorm:"column:timeout_ms;default:0" json:"timeoutMs"`     // 0 = Dispatcher default (100ms)
	IsEnabled bool   `gorm:"column:is_enabled;default:true" json:"isEnabled"`
}

func (AIExtension) TableName() string { return "ai_extensions" }

// AIEventLogEntry is the event bus's durable log (docs/design/09-
// extensibility.md "Event bus"): on_audit/on_billing publishes land here
// first (batched insert, mirrors AuditWorker), then per-sink pollers deliver
// from it using AIEventCursor to track resume position. The autoincrement ID
// doubles as the cursor position — durable and already monotonic, unlike a
// pure in-memory channel which loses everything on a crash.
type AIEventLogEntry struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	EventID   string         `gorm:"column:event_id;type:varchar(26);index" json:"eventId"` // ULID, consumer-side idempotency
	EventType string         `gorm:"column:event_type;type:varchar(16);index" json:"eventType"`
	TenantID  uint           `gorm:"column:tenant_id;index" json:"tenantId"`
	Payload   datatypes.JSON `gorm:"column:payload;type:json" json:"payload"`
	V         int            `gorm:"column:v;default:1" json:"v"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime;index" json:"createdAt"`
}

func (AIEventLogEntry) TableName() string { return "ai_event_log" }

// AIEventCursor is one sink's delivery position into AIEventLogEntry.
type AIEventCursor struct {
	SinkName    string    `gorm:"column:sink_name;primaryKey;type:varchar(64)" json:"sinkName"`
	LastEventID uint      `gorm:"column:last_event_id;default:0" json:"lastEventId"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
}

func (AIEventCursor) TableName() string { return "ai_event_cursors" }
