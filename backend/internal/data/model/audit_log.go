package model

import (
	"time"

	"gorm.io/datatypes"
)

type AIGatewayAuditLog struct {
	ID        uint      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime;index" json:"createdAt"`

	VirtualKeyID uint   `gorm:"not null;index" json:"virtualKeyId"`
	KeyPrefix    string `gorm:"type:varchar(20)" json:"keyPrefix"`
	KeyName      string `gorm:"column:key_name;type:varchar(128)" json:"keyName"`
	SessionID    string `gorm:"column:session_id;type:varchar(64);index" json:"sessionId"`

	ProviderID     uint   `gorm:"not null;index" json:"providerId"`
	Model          string `gorm:"type:varchar(64);index" json:"model"`
	RequestedModel string `gorm:"type:varchar(128);index" json:"requestedModel"`

	RequestBody  string `gorm:"-" json:"requestBody"`
	ResponseBody string `gorm:"-" json:"responseBody"`

	ExtractedFiles []ExtractedFile         `gorm:"-" json:"extractedFiles,omitempty"`
	Files          []AIGatewayAuditLogFile `gorm:"-" json:"files,omitempty"`

	PromptTokens        int `json:"promptTokens"`
	CompletionTokens    int `json:"completionTokens"`
	TotalTokens         int `json:"totalTokens"`
	CacheReadTokens     int `gorm:"default:0" json:"cacheReadTokens"`
	CacheCreationTokens int `gorm:"default:0" json:"cacheCreationTokens"`
	// ReasoningTokens is informational only (docs/design/02-protocol-adapters.md):
	// OpenAI/Responses-API reasoning tokens are already a subset of
	// CompletionTokens for pricing purposes, so this column is never fed into
	// calcCredits — it exists purely so the audit UI can show the breakdown.
	ReasoningTokens int `gorm:"default:0" json:"reasoningTokens"`

	Protocol string `gorm:"type:varchar(16);default:'openai'" json:"protocol"`

	// Failover trail (docs/design/01-routing-and-lb.md): total upstream
	// attempts and per-attempt provider/status/error/latency records.
	AttemptsTotal    int            `gorm:"column:attempts_total;default:0" json:"attemptsTotal"`
	ProviderAttempts datatypes.JSON `gorm:"column:provider_attempts;type:json" json:"providerAttempts,omitempty"`

	LatencyMs    int64  `json:"latencyMs"`
	StatusCode   int    `json:"statusCode"`
	ErrorMessage string `gorm:"type:varchar(512)" json:"errorMessage"`

	PIIBlocked bool   `gorm:"default:false" json:"piiBlocked"`
	PIIAction  string `gorm:"type:varchar(16);default:''" json:"piiAction"`
	PIITypes   string `gorm:"type:varchar(256);default:''" json:"piiTypes"`

	ClientIP    string `gorm:"type:varchar(64)" json:"clientIp"`
	ClientAgent string `gorm:"type:varchar(128);default:''" json:"clientAgent"`

	ESStatus string `gorm:"type:varchar(16);not null;default:''" json:"esStatus"`

	PointsConsumed float64 `gorm:"column:points_consumed;type:decimal(18,6);default:0" json:"pointsConsumed"`
	PriceConsumed  float64 `gorm:"column:price_consumed;type:decimal(18,6);default:0" json:"priceConsumed"`

	UpstreamRequestID string  `gorm:"column:upstream_request_id;type:varchar(128)" json:"upstreamRequestId"`
	ProjectID         *string `gorm:"column:project_id;type:varchar(64);index" json:"projectId"`
	ProjectName       *string `gorm:"column:project_name;type:varchar(128)" json:"projectName"`
	EnvID             *string `gorm:"column:env_id;type:varchar(64);index" json:"envId"`

	// TraceID/SpanID correlate this row with an OTel trace (docs/design/05-observability.md).
	// Empty when tracing is disabled or the request's span was not sampled.
	TraceID string `gorm:"column:trace_id;type:varchar(32);index" json:"traceId"`
	SpanID  string `gorm:"column:span_id;type:varchar(16)" json:"spanId"`

	// HookLabels are annotate-only key/value pairs contributed by pre_request/
	// post_response extensions (docs/design/09-extensibility.md "Hook points").
	// Empty when no hook ran or none set a label.
	HookLabels datatypes.JSON `gorm:"column:hook_labels;type:json" json:"hookLabels"`
}

func (AIGatewayAuditLog) TableName() string { return "ai_gateway_audit_logs" }

type AIGatewayAuditLogBody struct {
	AuditLogID uint `gorm:"column:audit_log_id;primaryKey;autoIncrement:false;index:idx_es_synced_aid,priority:2" json:"auditLogId"`

	VirtualKeyID uint   `gorm:"column:virtual_key_id;index" json:"virtualKeyId"`
	ProviderID   uint   `gorm:"column:provider_id;index" json:"providerId"`
	Model        string `gorm:"column:model;type:varchar(64);index" json:"model"`

	RequestBody  string `gorm:"type:longtext" json:"requestBody"`
	ResponseBody string `gorm:"type:longtext" json:"responseBody"`

	ESSynced  bool      `gorm:"column:es_synced;not null;default:false;index:idx_es_synced_aid,priority:1" json:"esSynced"`
	CreatedAt time.Time `gorm:"column:created_at;index" json:"createdAt"`

	AuditLog AIGatewayAuditLog `gorm:"foreignKey:AuditLogID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (AIGatewayAuditLogBody) TableName() string { return "ai_gateway_audit_log_bodies" }

type AIGatewayAuditFileObject struct {
	Hash      string    `gorm:"column:hash;type:varchar(64);primaryKey" json:"hash"`
	OSSKey    string    `gorm:"column:oss_key;type:varchar(512)" json:"-"`
	MimeType  string    `gorm:"column:mime_type;type:varchar(128)" json:"mimeType"`
	SizeBytes int64     `gorm:"column:size_bytes" json:"sizeBytes"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime;index" json:"createdAt"`
}

func (AIGatewayAuditFileObject) TableName() string { return "ai_gateway_audit_file_objects" }

type AIGatewayAuditLogFile struct {
	ID          uint      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	AuditLogID  uint      `gorm:"column:audit_log_id;not null;index" json:"auditLogId"`
	Source      string    `gorm:"column:source;type:varchar(16);default:'request'" json:"source"`
	Kind        string    `gorm:"column:kind;type:varchar(16)" json:"kind"`
	Filename    string    `gorm:"column:filename;type:varchar(256)" json:"filename"`
	MimeType    string    `gorm:"column:mime_type;type:varchar(128)" json:"mimeType"`
	SizeBytes   int64     `gorm:"column:size_bytes" json:"sizeBytes"`
	PartIndex   int       `gorm:"column:part_index" json:"partIndex"`
	ContentHash string    `gorm:"column:content_hash;type:varchar(64);index" json:"contentHash"`
	CreatedAt   time.Time `gorm:"column:created_at;index" json:"createdAt"`

	AuditLog AIGatewayAuditLog `gorm:"foreignKey:AuditLogID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (AIGatewayAuditLogFile) TableName() string { return "ai_gateway_audit_log_files" }

type ExtractedFile struct {
	Source    string `json:"source"`
	Kind      string `json:"kind"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
	PartIndex int    `json:"partIndex"`
	Data      []byte `json:"data"`
}
