package dto

// AuthConfigResp tells the console whether SSO is available — a public,
// unauthenticated endpoint (docs/design/04-multi-tenancy-and-auth.md console
// login). The console navigates to GET /ai/gateway/auth/login directly (a
// server-side redirect to the provider), so no separate login URL is needed.
type AuthConfigResp struct {
	OIDCEnabled bool `json:"oidcEnabled"`
}

// SessionResp is returned after a successful OIDC callback and by "who am I".
type SessionResp struct {
	UserID          uint   `json:"userId"`
	Email           string `json:"email"`
	DisplayName     string `json:"displayName"`
	IsPlatformAdmin bool   `json:"isPlatformAdmin"`
}

// CreateAdminKeyReq mints a machine principal (docs/design/04 "Admin API keys").
type CreateAdminKeyReq struct {
	Name     string `json:"name"`
	TenantID uint   `json:"tenantId"` // 0 = platform-wide
	Role     string `json:"role"`
}

// CreateAdminKeyResp shows the plaintext key exactly once.
type CreateAdminKeyResp struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	KeyPrefix string `json:"keyPrefix"`
	PlainKey  string `json:"plainKey"`
}

// UpdateAdminKeyReq partially updates an admin key; nil fields unchanged.
type UpdateAdminKeyReq struct {
	ID        uint    `json:"id"`
	Role      *string `json:"role"`
	IsEnabled *bool   `json:"isEnabled"`
}

// UserItem is one row of the console's Users page: a user plus their role in
// the tenant currently being viewed (empty when the user has no membership there).
type UserItem struct {
	ID              uint   `json:"id"`
	Email           string `json:"email"`
	DisplayName     string `json:"displayName"`
	IsPlatformAdmin bool   `json:"isPlatformAdmin"`
	IsEnabled       bool   `json:"isEnabled"`
	Role            string `json:"role"`
}

// UpdateUserTenantRoleReq upserts (or, with Role="", removes) a user's role
// within one tenant.
type UpdateUserTenantRoleReq struct {
	UserID   uint   `json:"userId"`
	TenantID uint   `json:"tenantId"`
	Role     string `json:"role"`
}
