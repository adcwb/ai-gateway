package dto

// SettingsResp reports the currently effective console-editable settings.
type SettingsResp struct {
	AlertWebhook string `json:"alertWebhook"`
	// AlertWebhookIsOverride is true when the value comes from the console
	// (ai_settings table) rather than the static system.alert_webhook config.
	AlertWebhookIsOverride bool `json:"alertWebhookIsOverride"`

	// Semantic cache embedding config (docs/design/07-caching-strategies.md
	// "Vector backend (ADR)"): which provider+model generates embeddings for
	// the semantic cache. Empty providerId/model = semantic cache disabled
	// gateway-wide, regardless of any per-key semanticEnabled flag.
	CacheEmbeddingProviderID uint   `json:"cacheEmbeddingProviderId"`
	CacheEmbeddingModel      string `json:"cacheEmbeddingModel"`
	CacheEmbeddingDim        int    `json:"cacheEmbeddingDim"`
}

// UpdateSettingsReq partially updates console-editable settings; nil fields
// are left unchanged. An empty (non-nil) AlertWebhook clears the override,
// falling back to the static config.
type UpdateSettingsReq struct {
	AlertWebhook             *string `json:"alertWebhook"`
	CacheEmbeddingProviderID *uint   `json:"cacheEmbeddingProviderId"`
	CacheEmbeddingModel      *string `json:"cacheEmbeddingModel"`
	CacheEmbeddingDim        *int    `json:"cacheEmbeddingDim"`
}

// CreateCreditsRateReq registers a currency's CNY-equivalent credit rate.
type CreateCreditsRateReq struct {
	Currency      string  `json:"currency"`
	RatePerCredit float64 `json:"ratePerCredit"`
	Description   string  `json:"description"`
}

// UpdateCreditsRateReq partially updates a credits rate.
type UpdateCreditsRateReq struct {
	ID            uint     `json:"id"`
	RatePerCredit *float64 `json:"ratePerCredit"`
	IsEnabled     *bool    `json:"isEnabled"`
	Description   *string  `json:"description"`
}
