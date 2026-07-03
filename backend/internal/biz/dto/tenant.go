package dto

import "github.com/opscenter/ai-gateway/internal/data/model"

// CreateTenantReq registers a tenant (a disabled billing-account shell is
// created alongside).
type CreateTenantReq struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// TenantItem is a tenant row with its billing summary for the console.
type TenantItem struct {
	model.AITenant
	Account  *model.AIBillingAccount `json:"account,omitempty"`
	KeyCount int64                   `json:"keyCount"`
}

// CreateProjectReq adds a project under a tenant.
type CreateProjectReq struct {
	TenantID    uint   `json:"tenantId"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// RechargeReq credits a tenant's billing account (manual/admin recharge).
type RechargeReq struct {
	TenantID       uint    `json:"tenantId"`
	Credits        float64 `json:"credits"`
	IdempotencyKey string  `json:"idempotencyKey"`
	Remark         string  `json:"remark"`
}

// UpdateBillingAccountReq applies operator changes; nil fields unchanged.
type UpdateBillingAccountReq struct {
	TenantID          uint     `json:"tenantId"`
	IsEnabled         *bool    `json:"isEnabled"`
	Mode              *string  `json:"mode"`
	Currency          *string  `json:"currency"`
	CreditLimit       *float64 `json:"creditLimit"`  // credits
	LowWatermark      *float64 `json:"lowWatermark"` // credits
	GraceHours        *int     `json:"graceHours"`
	PriceTableID      *uint    `json:"priceTableId"`
	ClearPriceTableID bool     `json:"clearPriceTableId"`
}
