package dto

// CreateModelItemReq registers a model's upstream cost pricing under a
// provider (docs/design/03-billing-and-monetization.md Layer 1).
type CreateModelItemReq struct {
	ProviderID                uint    `json:"providerId"`
	Name                      string  `json:"name"`
	ModelType                 string  `json:"modelType"`
	ContextWindow             int     `json:"contextWindow"`
	IsDefault                 bool    `json:"isDefault"`
	Description               string  `json:"description"`
	InputPricePerMillion      float64 `json:"inputPricePerMillion"`
	OutputPricePerMillion     float64 `json:"outputPricePerMillion"`
	CacheReadPricePerMillion  float64 `json:"cacheReadPricePerMillion"`
	CacheWritePricePerMillion float64 `json:"cacheWritePricePerMillion"`
}

// UpdateModelItemReq partially updates a model item; nil fields are unchanged.
type UpdateModelItemReq struct {
	ID                        uint     `json:"id"`
	ModelType                 *string  `json:"modelType"`
	ContextWindow             *int     `json:"contextWindow"`
	IsDefault                 *bool    `json:"isDefault"`
	IsEnabled                 *bool    `json:"isEnabled"`
	Description               *string  `json:"description"`
	InputPricePerMillion      *float64 `json:"inputPricePerMillion"`
	OutputPricePerMillion     *float64 `json:"outputPricePerMillion"`
	CacheReadPricePerMillion  *float64 `json:"cacheReadPricePerMillion"`
	CacheWritePricePerMillion *float64 `json:"cacheWritePricePerMillion"`
}

// CreatePriceTableReq creates a named sell-side price table.
type CreatePriceTableReq struct {
	Name     string `json:"name"`
	Currency string `json:"currency"`
}

// UpdatePriceTableReq partially updates a price table.
type UpdatePriceTableReq struct {
	ID       uint    `json:"id"`
	Name     *string `json:"name"`
	Currency *string `json:"currency"`
}

// CreatePriceTableItemReq adds a model-pattern price row to a table.
type CreatePriceTableItemReq struct {
	PriceTableID          uint    `json:"priceTableId"`
	ModelPattern          string  `json:"modelPattern"`
	InputPricePerMillion  float64 `json:"inputPricePerMillion"`
	OutputPricePerMillion float64 `json:"outputPricePerMillion"`
	CacheReadPerMillion   float64 `json:"cacheReadPerMillion"`
}

// UpdatePriceTableItemReq partially updates a price table item.
type UpdatePriceTableItemReq struct {
	ID                    uint     `json:"id"`
	ModelPattern          *string  `json:"modelPattern"`
	InputPricePerMillion  *float64 `json:"inputPricePerMillion"`
	OutputPricePerMillion *float64 `json:"outputPricePerMillion"`
	CacheReadPerMillion   *float64 `json:"cacheReadPerMillion"`
}

// PatternTestReq/Resp power the "which known models match" tester shared by
// price-table items and model mappings (same matcher semantics).
type PatternTestReq struct {
	Pattern string   `json:"pattern"`
	Models  []string `json:"models"`
}

type PatternTestResp struct {
	Matched []string `json:"matched"`
	IsRegex bool     `json:"isRegex"`
}
