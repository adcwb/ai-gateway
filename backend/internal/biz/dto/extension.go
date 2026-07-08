package dto

import "encoding/json"

// CreateExtensionReq registers a webhook- or WASM-backed pre_request/
// post_response hook (docs/design/09-extensibility.md "Delivery mechanisms").
// HMACSecret (webhook) is encrypted at rest and never returned.
type CreateExtensionReq struct {
	Name       string          `json:"name"`
	Kind       string          `json:"kind"` // webhook | wasm
	Hooks      json.RawMessage `json:"hooks"`
	URL        string          `json:"url"`        // webhook only
	HMACSecret string          `json:"hmacSecret"` // webhook only
	WasmPath   string          `json:"wasmPath"`   // wasm only
	FailMode   string          `json:"failMode"`   // open | closed, default open
	TenantID   uint            `json:"tenantId"`   // 0 = all tenants
	TimeoutMs  int             `json:"timeoutMs"`  // 0 = Dispatcher default
}

// UpdateExtensionReq updates an extension; nil fields are left unchanged.
// HMACSecret, when non-empty, replaces (and re-encrypts) the stored secret.
type UpdateExtensionReq struct {
	ID         uint            `json:"id"`
	Name       *string         `json:"name"`
	Hooks      json.RawMessage `json:"hooks"`
	URL        *string         `json:"url"`
	HMACSecret string          `json:"hmacSecret"`
	WasmPath   *string         `json:"wasmPath"`
	FailMode   *string         `json:"failMode"`
	TenantID   *uint           `json:"tenantId"`
	TimeoutMs  *int            `json:"timeoutMs"`
	IsEnabled  *bool           `json:"isEnabled"`
}
