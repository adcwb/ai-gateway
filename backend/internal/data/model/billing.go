package model

import (
	"time"

	"gorm.io/gorm"
)

// Billing account modes / statuses (docs/design/03-billing-and-monetization.md).
const (
	BillingModePrepaid  = "prepaid"
	BillingModePostpaid = "postpaid"

	BillingStatusActive    = "active"
	BillingStatusGrace     = "grace"
	BillingStatusSuspended = "suspended"
)

// Ledger entry types. The ledger is append-only; balance is always
// reconstructible as the sum of an account's entries.
const (
	LedgerEntryRecharge = "recharge"
	LedgerEntryDeduct   = "deduct"
	LedgerEntryRefund   = "refund"
	LedgerEntryAdjust   = "adjust"
)

// MicroCreditScale converts credits to the integer micro-credits stored in
// balances and ledger amounts (matches credits.go microCreditScale).
const MicroCreditScale = 1_000_000

// AIBillingAccount is the per-tenant balance account. Billing is opt-in:
// a missing or disabled account means the gateway behaves exactly as before.
type AIBillingAccount struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	TenantID          uint       `gorm:"not null;uniqueIndex" json:"tenantId"`
	IsEnabled         bool       `gorm:"default:false" json:"isEnabled"`
	Mode              string     `gorm:"type:varchar(16);default:prepaid" json:"mode"`
	Currency          string     `gorm:"type:varchar(8);default:CNY" json:"currency"`
	BalanceMicro      int64      `gorm:"default:0" json:"balanceMicro"`
	CreditLimitMicro  int64      `gorm:"default:0" json:"creditLimitMicro"`  // postpaid ceiling / prepaid overdraft allowance
	LowWatermarkMicro int64      `gorm:"default:0" json:"lowWatermarkMicro"` // budget-alert threshold; 0 = disabled
	PriceTableID      *uint      `gorm:"index" json:"priceTableId"`
	Status            string     `gorm:"type:varchar(16);default:active;index" json:"status"`
	GraceHours        int        `gorm:"default:24" json:"graceHours"`
	GraceUntil        *time.Time `json:"graceUntil"`
}

func (AIBillingAccount) TableName() string { return "ai_billing_accounts" }

// AIBillingLedger is the append-only double-entry record of every value
// movement. IdempotencyKey makes settlement and recharge replay-safe.
type AIBillingLedger struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime;index" json:"createdAt"`

	AccountID         uint   `gorm:"not null;index" json:"accountId"`
	EntryType         string `gorm:"type:varchar(16);not null" json:"entryType"`
	AmountMicro       int64  `gorm:"not null" json:"amountMicro"` // signed: credits in (+), debits out (−)
	BalanceAfterMicro int64  `gorm:"not null" json:"balanceAfterMicro"`
	IdempotencyKey    string `gorm:"type:varchar(64);not null;uniqueIndex" json:"idempotencyKey"`
	RefType           string `gorm:"type:varchar(16)" json:"refType"` // audit_log / manual / payment_order
	RefID             string `gorm:"type:varchar(64)" json:"refId"`
	Remark            string `gorm:"type:varchar(256)" json:"remark"`
}

func (AIBillingLedger) TableName() string { return "ai_billing_ledgers" }

// AIUsageDaily is the pre-aggregated attribution table the console and
// reports read, so analytics never scan the audit table
// (docs/design/03-billing-and-monetization.md).
type AIUsageDaily struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`

	Day        string `gorm:"type:varchar(10);not null;uniqueIndex:idx_usage_dim;index" json:"day"` // YYYY-MM-DD
	TenantID   uint   `gorm:"not null;uniqueIndex:idx_usage_dim;index" json:"tenantId"`
	KeyID      uint   `gorm:"not null;uniqueIndex:idx_usage_dim" json:"keyId"`
	ProviderID uint   `gorm:"not null;uniqueIndex:idx_usage_dim" json:"providerId"`
	Model      string `gorm:"type:varchar(128);not null;uniqueIndex:idx_usage_dim" json:"model"`

	Requests         int64 `gorm:"default:0" json:"requests"`
	PromptTokens     int64 `gorm:"default:0" json:"promptTokens"`
	CompletionTokens int64 `gorm:"default:0" json:"completionTokens"`
	CacheReadTokens  int64 `gorm:"default:0" json:"cacheReadTokens"`
	CostMicro        int64 `gorm:"default:0" json:"costMicro"`  // upstream cost
	PriceMicro       int64 `gorm:"default:0" json:"priceMicro"` // sell-side price
	CacheHits        int64 `gorm:"default:0" json:"cacheHits"`
}

func (AIUsageDaily) TableName() string { return "ai_usage_dailies" }

// AIPriceTable decouples sell-side prices from upstream cost; a tenant's
// account references one table, absent = fall back to model cost.
type AIPriceTable struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	Name     string `gorm:"type:varchar(64);not null;uniqueIndex" json:"name"`
	Currency string `gorm:"type:varchar(8);default:CNY" json:"currency"`

	Items []AIPriceTableItem `gorm:"-" json:"items,omitempty"`
}

func (AIPriceTable) TableName() string { return "ai_price_tables" }

// AIPriceTableItem holds sell-side per-million prices for a model pattern
// (exact name first, then regex — same semantics as model mappings).
type AIPriceTableItem struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	PriceTableID          uint    `gorm:"not null;index" json:"priceTableId"`
	ModelPattern          string  `gorm:"type:varchar(128);not null" json:"modelPattern"`
	InputPricePerMillion  float64 `gorm:"type:decimal(18,6);default:0" json:"inputPricePerMillion"`
	OutputPricePerMillion float64 `gorm:"type:decimal(18,6);default:0" json:"outputPricePerMillion"`
	CacheReadPerMillion   float64 `gorm:"type:decimal(18,6);default:0" json:"cacheReadPerMillion"`
}

func (AIPriceTableItem) TableName() string { return "ai_price_table_items" }
