package dto

// CreateMCPServerReq registers an upstream MCP server (docs/design/09-
// extensibility.md "MCP gateway"). APIKey (optional — some MCP servers don't
// require upstream auth) is encrypted at rest and never returned.
type CreateMCPServerReq struct {
	Name        string `json:"name"`
	BaseURL     string `json:"baseUrl"`
	APIKey      string `json:"apiKey"`
	Description string `json:"description"`
}

// UpdateMCPServerReq updates an MCP server; nil fields are left unchanged.
// APIKey, when non-empty, replaces (and re-encrypts) the stored credential.
type UpdateMCPServerReq struct {
	ID          uint    `json:"id"`
	Name        *string `json:"name"`
	BaseURL     *string `json:"baseUrl"`
	APIKey      string  `json:"apiKey"`
	Description *string `json:"description"`
	IsEnabled   *bool   `json:"isEnabled"`
}
