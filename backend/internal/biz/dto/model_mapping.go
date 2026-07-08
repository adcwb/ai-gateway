package dto

import "encoding/json"

// CreateModelMappingReq maps a virtual model name (whatever a client sends as
// "model") to a real AIModelItem for one virtual key, with an optional
// ordered fallback chain (docs/design/01-routing-and-lb.md): tried in order
// after the mapped target on a retryable upstream failure.
type CreateModelMappingReq struct {
	VirtualKeyID  uint            `json:"virtualKeyId"`
	VirtualModel  string          `json:"virtualModel"`
	RealModelID   uint            `json:"realModelId"`
	Description   string          `json:"description"`
	FallbackChain json.RawMessage `json:"fallbackChain"` // [{"providerId":1,"model":"x"}, ...]
}

// UpdateModelMappingReq updates a mapping; nil fields are left unchanged.
type UpdateModelMappingReq struct {
	ID            uint            `json:"id"`
	VirtualModel  *string         `json:"virtualModel"`
	RealModelID   *uint           `json:"realModelId"`
	IsEnabled     *bool           `json:"isEnabled"`
	Description   *string         `json:"description"`
	FallbackChain json.RawMessage `json:"fallbackChain"`
}
