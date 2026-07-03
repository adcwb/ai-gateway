package model

import "time"

// Breaker states recorded in router events.
const (
	BreakerStateClosed   = "closed"
	BreakerStateHalfOpen = "half_open"
	BreakerStateOpen     = "open"
)

// AIGatewayRouterEvent records circuit-breaker state transitions for operator
// visibility and console timelines (mirrors the QuotaEvent pattern).
type AIGatewayRouterEvent struct {
	ID         uint      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime;index" json:"createdAt"`
	ProviderID uint      `gorm:"column:provider_id;not null;index" json:"providerId"`
	FromState  string    `gorm:"column:from_state;type:varchar(16)" json:"fromState"`
	ToState    string    `gorm:"column:to_state;type:varchar(16)" json:"toState"`
	Reason     string    `gorm:"column:reason;type:varchar(256)" json:"reason"`
}

func (AIGatewayRouterEvent) TableName() string { return "ai_gateway_router_events" }
