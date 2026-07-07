package dto

import (
	"encoding/json"
	"time"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

// CreateVirtualKeyReq 创建虚拟 Key
type CreateVirtualKeyReq struct {
	Name                string          `json:"name"`
	ProviderID          uint            `json:"providerId"`
	BaseURL             string          `json:"baseUrl"`
	AllowedModels       json.RawMessage `json:"allowedModels"`
	DailyTokenQuota     int64           `json:"dailyTokenQuota"`
	HourlyTokenQuota    int64           `json:"hourlyTokenQuota"`
	HourlyReqQuota      int64           `json:"hourlyReqQuota"`
	MaxConcurrency      int             `json:"maxConcurrency"`
	PIIPolicyID         *uint           `json:"piiPolicyId"`
	IPWhitelistEnabled  bool            `json:"ipWhitelistEnabled"`
	IPWhitelist         json.RawMessage `json:"ipWhitelist"`
	ExpiresAt           *time.Time      `json:"expiresAt"`
	ProjectID           *string         `json:"projectId"`
	ProjectName         *string         `json:"projectName"`
	EnvID               *string         `json:"envId"`
	DailyPointQuota     float64         `json:"dailyPointQuota"`
	HourlyPointQuota    float64         `json:"hourlyPointQuota"`
	Description         string          `json:"description"`
	TenantID            uint            `json:"tenantId"`            // 0 = default tenant
	ProjectRefID        uint            `json:"projectRefId"`        // 0 = default project
	CacheConfig         json.RawMessage `json:"cacheConfig"`         // docs/design/07-caching-strategies.md
	ToolWhitelist       json.RawMessage `json:"toolWhitelist"`       // docs/design/09-extensibility.md; empty = unrestricted
	HourlyToolCallQuota int64           `json:"hourlyToolCallQuota"` // docs/design/09-extensibility.md QuotaDimToolCall; 0 = unlimited
}

type CreateVirtualKeyResp struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	KeyPrefix string `json:"keyPrefix"`
	PlainKey  string `json:"plainKey"`
}

type RevealVirtualKeyResp struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	PlainKey string `json:"plainKey"`
}

// UpdateVirtualKeyReq 更新虚拟 Key 配置
type UpdateVirtualKeyReq struct {
	ID                  uint            `json:"id"`
	Name                string          `json:"name"`
	AllowedModels       json.RawMessage `json:"allowedModels"`
	PIIPolicyID         *uint           `json:"piiPolicyId"`
	IPWhitelistEnabled  bool            `json:"ipWhitelistEnabled"`
	IPWhitelist         json.RawMessage `json:"ipWhitelist"`
	IsEnabled           *bool           `json:"isEnabled"`
	ExpiresAt           *time.Time      `json:"expiresAt"`
	ProjectID           *string         `json:"projectId"`
	ProjectName         *string         `json:"projectName"`
	EnvID               *string         `json:"envId"`
	Description         string          `json:"description"`
	CacheConfig         json.RawMessage `json:"cacheConfig"`
	ToolWhitelist       json.RawMessage `json:"toolWhitelist"`
	HourlyToolCallQuota *int64          `json:"hourlyToolCallQuota"`
}

type UpdateVirtualKeyStatusReq struct {
	ID        uint  `json:"id"`
	IsEnabled *bool `json:"isEnabled"`
}

// ListVirtualKeysReq 虚拟 Key 列表查询
type ListVirtualKeysReq struct {
	PageInfo
	ProviderID uint    `json:"providerId" form:"providerId"`
	IsEnabled  *bool   `json:"isEnabled" form:"isEnabled"`
	Keyword    string  `json:"keyword" form:"keyword"`
	ProjectID  *string `json:"projectId" form:"projectId"`
}

type VirtualKeyStatsResp struct {
	Total    int64 `json:"total"`
	Enabled  int64 `json:"enabled"`
	Expiring int64 `json:"expiring"`
	Inactive int64 `json:"inactive"`
}

// QuotaConfigItem per-model 配额项
type QuotaConfigItem struct {
	ModelName        string  `json:"modelName"`
	DailyTokenQuota  int64   `json:"dailyTokenQuota"`
	HourlyTokenQuota int64   `json:"hourlyTokenQuota"`
	HourlyReqQuota   int64   `json:"hourlyReqQuota"`
	DailyPointQuota  float64 `json:"dailyPointQuota"`
	HourlyPointQuota float64 `json:"hourlyPointQuota"`

	DailyTokenUsed  int64   `json:"dailyTokenUsed"`
	HourlyTokenUsed int64   `json:"hourlyTokenUsed"`
	HourlyReqUsed   int64   `json:"hourlyReqUsed"`
	DailyPointUsed  float64 `json:"dailyPointUsed"`
	HourlyPointUsed float64 `json:"hourlyPointUsed"`
}

type QuotaConfigResp struct {
	KeyID            uint              `json:"keyId"`
	Name             string            `json:"name"`
	KeyPrefix        string            `json:"keyPrefix"`
	ProviderID       uint              `json:"providerId"`
	AllowedModels    json.RawMessage   `json:"allowedModels"`
	DailyTokenQuota  int64             `json:"dailyTokenQuota"`
	HourlyTokenQuota int64             `json:"hourlyTokenQuota"`
	HourlyReqQuota   int64             `json:"hourlyReqQuota"`
	MaxConcurrency   int               `json:"maxConcurrency"`
	DailyPointQuota  float64           `json:"dailyPointQuota"`
	HourlyPointQuota float64           `json:"hourlyPointQuota"`
	ModelQuotas      []QuotaConfigItem `json:"modelQuotas"`
}

type UpdateQuotaConfigReq struct {
	KeyID            uint              `json:"keyId"`
	DailyTokenQuota  int64             `json:"dailyTokenQuota"`
	HourlyTokenQuota int64             `json:"hourlyTokenQuota"`
	HourlyReqQuota   int64             `json:"hourlyReqQuota"`
	MaxConcurrency   int               `json:"maxConcurrency"`
	DailyPointQuota  float64           `json:"dailyPointQuota"`
	HourlyPointQuota float64           `json:"hourlyPointQuota"`
	ModelQuotas      []QuotaConfigItem `json:"modelQuotas"`
}

type KeyQuotaUsageResp struct {
	KeyID              uint    `json:"keyId"`
	DailyTokenQuota    int64   `json:"dailyTokenQuota"`
	DailyTokenUsed     int64   `json:"dailyTokenUsed"`
	HourlyTokenQuota   int64   `json:"hourlyTokenQuota"`
	HourlyTokenUsed    int64   `json:"hourlyTokenUsed"`
	HourlyReqQuota     int64   `json:"hourlyReqQuota"`
	HourlyReqUsed      int64   `json:"hourlyReqUsed"`
	MaxConcurrency     int     `json:"maxConcurrency"`
	CurrentConcurrency int64   `json:"currentConcurrency"`
	DailyPointQuota    float64 `json:"dailyPointQuota"`
	DailyPointUsed     float64 `json:"dailyPointUsed"`
	HourlyPointQuota   float64 `json:"hourlyPointQuota"`
	HourlyPointUsed    float64 `json:"hourlyPointUsed"`
}

// AuditLogFilter 审计日志公共过滤条件
type AuditLogFilter struct {
	VirtualKeyID uint   `json:"virtualKeyId" form:"virtualKeyId"`
	ProviderID   uint   `json:"providerId" form:"providerId"`
	Model        string `json:"model" form:"model"`
	Protocol     string `json:"protocol" form:"protocol"`
	PIIAction    string `json:"piiAction" form:"piiAction"`
	Status       string `json:"status" form:"status"`
	ClientAgent  string `json:"clientAgent" form:"clientAgent"`
	PIIBlocked   *bool  `json:"piiBlocked" form:"piiBlocked"`
	StartTime    string `json:"startTime" form:"startTime"`
	EndTime      string `json:"endTime" form:"endTime"`
}

type ListAuditLogsReq struct {
	PageInfo
	AuditLogFilter
	SessionID string `json:"sessionId" form:"sessionId"`
}

type ListAuditSessionsReq struct {
	PageInfo
	AuditLogFilter
}

type AuditSessionSummary struct {
	SessionID        string    `json:"sessionId"`
	FirstAt          time.Time `json:"firstAt"`
	LastAt           time.Time `json:"lastAt"`
	ReqCount         int64     `json:"reqCount"`
	PromptTokens     int64     `json:"promptTokens"`
	CompletionTokens int64     `json:"completionTokens"`
	TotalTokens      int64     `json:"totalTokens"`
	PointsConsumed   float64   `json:"pointsConsumed"`
	PriceConsumed    float64   `json:"priceConsumed"`
	FinalStatusCode  int       `json:"finalStatusCode"`
	KeyName          string    `json:"keyName"`
	ClientAgent      string    `json:"clientAgent"`
	Protocol         string    `json:"protocol"`
	Model            string    `json:"model"`
}

type SearchAuditLogsReq struct {
	PageInfo
	Keyword      string `json:"keyword" form:"keyword"`
	Scope        string `json:"scope" form:"scope"`
	VirtualKeyID uint   `json:"virtualKeyId" form:"virtualKeyId"`
	ProviderID   uint   `json:"providerId" form:"providerId"`
	Model        string `json:"model" form:"model"`
	StartTime    string `json:"startTime" form:"startTime"`
	EndTime      string `json:"endTime" form:"endTime"`
}

type AuditLogSearchHit struct {
	model.AIGatewayAuditLog
	Highlights []string `json:"highlights"`
}

type SearchAuditLogsResp struct {
	List  []AuditLogSearchHit `json:"list"`
	Total int64               `json:"total"`
}

type SecurityOverviewReq struct {
	AuditLogFilter
	TopN int `json:"topN" form:"topN"`
}

type PIITypeRank struct {
	Type  string `json:"type"`
	Count int64  `json:"count"`
}

type ModelErrorRank struct {
	Model      string `json:"model"`
	ErrorCount int64  `json:"error_count"`
}

type SecurityOverviewResp struct {
	TotalRequests  int64            `json:"totalRequests"`
	BlockCount     int64            `json:"blockCount"`
	RedactCount    int64            `json:"redactCount"`
	ErrorCount     int64            `json:"errorCount"`
	ErrorRate      float64          `json:"errorRate"`
	TopPIITypes    []PIITypeRank    `json:"topPiiTypes"`
	TopErrorModels []ModelErrorRank `json:"topErrorModels"`
}

// GatewayStatsResp 综合统计（仪表盘）
type GatewayStatsResp struct {
	TotalRequests  int64   `json:"totalRequests"`
	TotalTokens    int64   `json:"totalTokens"`
	TotalCredits   float64 `json:"totalCredits"`
	TotalCostCNY   float64 `json:"totalCostCNY"`
	ActiveKeyCount int64   `json:"activeKeyCount"`
	ProviderCount  int64   `json:"providerCount"`
}

// PageInfo shared pagination request
type PageInfo struct {
	Page     int `json:"page" form:"page"`
	PageSize int `json:"pageSize" form:"pageSize"`
}

func (p PageInfo) Offset() int {
	page := p.Page
	if page <= 0 {
		page = 1
	}
	size := p.PageSize
	if size <= 0 {
		size = 10
	}
	return (page - 1) * size
}

func (p PageInfo) Limit() int {
	size := p.PageSize
	if size <= 0 {
		size = 10
	}
	if size > 1000 {
		size = 1000
	}
	return size
}
