package dto

// ProviderModelItem is one model offered by a provider.
type ProviderModelItem struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

// CreateProviderReq creates an upstream provider. The API key is encrypted
// at rest (AES-256-GCM) and never returned by any endpoint.
type CreateProviderReq struct {
	Name         string              `json:"name"`
	BaseURL      string              `json:"baseUrl"`
	ProviderType string              `json:"providerType"`
	APIKey       string              `json:"apiKey"`
	Models       []ProviderModelItem `json:"models"`
	Weight       int                 `json:"weight"`
	Priority     int                 `json:"priority"`
	Description  string              `json:"description"`
	// ActiveProbeEnabled/IntervalSec configure active health probing
	// (docs/design/01-routing-and-lb.md) — off by default.
	ActiveProbeEnabled     bool `json:"activeProbeEnabled"`
	ActiveProbeIntervalSec int  `json:"activeProbeIntervalSec"`
}

// UpdateProviderReq updates a provider; nil fields are left unchanged.
// APIKey, when non-empty, replaces (and re-encrypts) the stored key.
type UpdateProviderReq struct {
	ID                     uint                 `json:"id"`
	Name                   *string              `json:"name"`
	BaseURL                *string              `json:"baseUrl"`
	ProviderType           *string              `json:"providerType"`
	APIKey                 string               `json:"apiKey"`
	Models                 *[]ProviderModelItem `json:"models"`
	Weight                 *int                 `json:"weight"`
	Priority               *int                 `json:"priority"`
	Description            *string              `json:"description"`
	IsEnabled              *bool                `json:"isEnabled"`
	ActiveProbeEnabled     *bool                `json:"activeProbeEnabled"`
	ActiveProbeIntervalSec *int                 `json:"activeProbeIntervalSec"`
}

// ProviderHealthItem is one row of the live provider-health view
// (breaker state comes from Redis via RouterManager).
type ProviderHealthItem struct {
	ProviderID         uint   `json:"providerId"`
	Name               string `json:"name"`
	State              string `json:"state"` // closed / half_open / open
	IsEnabled          bool   `json:"isEnabled"`
	Weight             int    `json:"weight"`
	Priority           int    `json:"priority"`
	ActiveProbeEnabled bool   `json:"activeProbeEnabled"`
}
