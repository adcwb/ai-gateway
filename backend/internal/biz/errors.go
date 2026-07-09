package biz

import kerrors "github.com/go-kratos/kratos/v2/errors"

// Gateway-specific typed errors. Callers can use errors.Is() or kerrors.FromError()
// to inspect these at the service layer and map them to correct HTTP status codes.
var (
	ErrVirtualKeyNotFound      = kerrors.NotFound("VIRTUAL_KEY_NOT_FOUND", "virtual key not found")
	ErrKeyPlaintextNotStored   = kerrors.NotFound("KEY_PLAINTEXT_NOT_STORED", "plain key was not stored (legacy key, created before encryption was enabled)")
	ErrKeyGenerationFailed     = kerrors.InternalServer("KEY_GENERATION_FAILED", "failed to generate random key material")
	ErrEncryptionFailed        = kerrors.InternalServer("ENCRYPTION_FAILED", "key encryption failed")
	ErrDecryptionFailed        = kerrors.InternalServer("DECRYPTION_FAILED", "key decryption failed")
	ErrInvalidIPWhitelist      = kerrors.BadRequest("INVALID_IP_WHITELIST", "IP whitelist must be a JSON array of strings")
	ErrInvalidIPEntry          = kerrors.BadRequest("INVALID_IP_ENTRY", "invalid IP address or CIDR notation")
	ErrIPWhitelistEmpty        = kerrors.BadRequest("IP_WHITELIST_EMPTY", "at least one IP or CIDR is required when IP whitelist is enabled")
	ErrProviderNotFound        = kerrors.NotFound("PROVIDER_NOT_FOUND", "provider not found")
	ErrProviderInvalid         = kerrors.BadRequest("PROVIDER_INVALID", "provider name, baseUrl and apiKey are required")
	ErrProviderNameExists      = kerrors.BadRequest("PROVIDER_NAME_EXISTS", "a provider with this name already exists")
	ErrTenantNotFound          = kerrors.NotFound("TENANT_NOT_FOUND", "tenant not found")
	ErrTenantInvalid           = kerrors.BadRequest("TENANT_INVALID", "tenant/project name is required")
	ErrTenantNameExists        = kerrors.BadRequest("TENANT_NAME_EXISTS", "a tenant/project with this name already exists")
	ErrBillingAccountNotFound  = kerrors.NotFound("BILLING_ACCOUNT_NOT_FOUND", "billing account not found")
	ErrBillingInvalidAmount    = kerrors.BadRequest("BILLING_INVALID_AMOUNT", "recharge amount must be positive")
	ErrBillingSuspended        = kerrors.New(402, "BILLING_SUSPENDED", "account suspended: insufficient balance")
	ErrProviderSyncUnsupported = kerrors.BadRequest("PROVIDER_SYNC_UNSUPPORTED", "model sync is only supported for openai_compatible providers")
	ErrProviderSyncFailed      = kerrors.New(502, "PROVIDER_SYNC_FAILED", "failed to fetch model list from the upstream provider")
	ErrInsufficientBalance     = kerrors.New(402, "INSUFFICIENT_BALANCE", "insufficient balance for this request")

	ErrModelItemNotFound  = kerrors.NotFound("MODEL_ITEM_NOT_FOUND", "model item not found")
	ErrModelItemInvalid   = kerrors.BadRequest("MODEL_ITEM_INVALID", "providerId and name are required")
	ErrModelItemExists    = kerrors.BadRequest("MODEL_ITEM_EXISTS", "this provider already has a model with this name")
	ErrPriceTableNotFound = kerrors.NotFound("PRICE_TABLE_NOT_FOUND", "price table not found")
	ErrPriceTableInvalid  = kerrors.BadRequest("PRICE_TABLE_INVALID", "price table name is required")
	ErrPriceTableExists   = kerrors.BadRequest("PRICE_TABLE_EXISTS", "a price table with this name already exists")
	ErrPriceItemNotFound  = kerrors.NotFound("PRICE_ITEM_NOT_FOUND", "price table item not found")
	ErrPriceItemInvalid   = kerrors.BadRequest("PRICE_ITEM_INVALID", "priceTableId and modelPattern are required")

	ErrCreditsRateInvalid           = kerrors.BadRequest("CREDITS_RATE_INVALID", "currency and a positive ratePerCredit are required")
	ErrCreditsRateExists            = kerrors.BadRequest("CREDITS_RATE_EXISTS", "a rate for this currency already exists")
	ErrCreditsRateNotFound          = kerrors.NotFound("CREDITS_RATE_NOT_FOUND", "credits rate not found")
	ErrSettingsWebhookNotConfigured = kerrors.BadRequest("SETTINGS_WEBHOOK_NOT_CONFIGURED", "no alert webhook is configured")
	ErrSettingsWebhookTestFailed    = kerrors.New(502, "SETTINGS_WEBHOOK_TEST_FAILED", "test delivery to the alert webhook failed")

	ErrOIDCNotConfigured = kerrors.BadRequest("OIDC_NOT_CONFIGURED", "OIDC/SSO is not configured")
	ErrOIDCLoginFailed   = kerrors.New(401, "OIDC_LOGIN_FAILED", "OIDC login failed")
	ErrSessionInvalid    = kerrors.Unauthorized("SESSION_INVALID", "missing or invalid session")
	ErrForbidden         = kerrors.Forbidden("FORBIDDEN", "insufficient role for this action")
	ErrAdminKeyNotFound  = kerrors.NotFound("ADMIN_KEY_NOT_FOUND", "admin key not found")
	ErrAdminKeyInvalid   = kerrors.BadRequest("ADMIN_KEY_INVALID", "name and role are required")
	ErrUserNotFound      = kerrors.NotFound("USER_NOT_FOUND", "user not found")
	ErrRoleInvalid       = kerrors.BadRequest("ROLE_INVALID", "role must be one of owner/admin/member/viewer")

	// ErrGuardrailBlocked documents the guardrail chain's block outcome
	// (docs/design/06-security-and-guardrails.md); the proxy hot path writes
	// the equivalent JSON directly (matching the pre-existing PII_DETECTED
	// inline pattern) rather than routing through this sentinel, since that
	// path never goes through the service-layer failWithErr indirection.
	ErrGuardrailBlocked = kerrors.BadRequest("GUARDRAIL_BLOCKED", "request or response blocked by guardrail policy")

	// MCP gateway (docs/design/09-extensibility.md).
	ErrMCPServerNotFound   = kerrors.NotFound("MCP_SERVER_NOT_FOUND", "MCP server not found")
	ErrMCPServerInvalid    = kerrors.BadRequest("MCP_SERVER_INVALID", "MCP server name and baseUrl are required")
	ErrMCPServerNameExists = kerrors.BadRequest("MCP_SERVER_NAME_EXISTS", "an MCP server with this name already exists")

	// Extensions / hook dispatcher (docs/design/09-extensibility.md).
	ErrExtensionNotFound   = kerrors.NotFound("EXTENSION_NOT_FOUND", "extension not found")
	ErrExtensionInvalid    = kerrors.BadRequest("EXTENSION_INVALID", "extension name, kind, and hooks are required")
	ErrExtensionNameExists = kerrors.BadRequest("EXTENSION_NAME_EXISTS", "an extension with this name already exists")

	// Responses API server-side state (docs/design/02-protocol-adapters.md).
	ErrResponseStateNotFound = kerrors.BadRequest("PREVIOUS_RESPONSE_NOT_FOUND", "previous_response_id not found or expired")

	// Model mappings (docs/design/01-routing-and-lb.md).
	ErrModelMappingNotFound = kerrors.NotFound("MODEL_MAPPING_NOT_FOUND", "model mapping not found")
	ErrModelMappingInvalid  = kerrors.BadRequest("MODEL_MAPPING_INVALID", "virtualKeyId, virtualModel, and realModelId are required")
	ErrModelMappingExists   = kerrors.BadRequest("MODEL_MAPPING_EXISTS", "this key already has a mapping for that virtual model name")

	// PII/guardrail policies (docs/design/06-security-and-guardrails.md).
	ErrPIIPolicyNotFound = kerrors.NotFound("PII_POLICY_NOT_FOUND", "policy not found")
	ErrPIIPolicyInvalid  = kerrors.BadRequest("PII_POLICY_INVALID", "name and action are required")

	// Multimodal media adapters, phase 1 (docs/superpowers/specs/2026-07-09-
	// multimodal-media-adapters-design.md): image generation + audio TTS/ASR.
	ErrMediaModelRequired       = kerrors.BadRequest("MEDIA_MODEL_REQUIRED", "model is required")
	ErrMediaModelNotFound       = kerrors.NotFound("MEDIA_MODEL_NOT_FOUND", "no matching image/audio model for this key")
	ErrMediaProviderUnsupported = kerrors.BadRequest("MEDIA_PROVIDER_UNSUPPORTED", "resolved provider does not support this media endpoint in this gateway version")
	ErrImageCallQuotaExceeded   = kerrors.New(429, "IMAGE_CALL_QUOTA_EXCEEDED", "hourly image call quota exceeded")
	ErrAudioCallQuotaExceeded   = kerrors.New(429, "AUDIO_CALL_QUOTA_EXCEEDED", "hourly audio call quota exceeded")

	// Video generation, phase 2 (docs/superpowers/specs/2026-07-09-video-
	// generation-phase2-design.md).
	ErrVideoJobNotFound       = kerrors.NotFound("VIDEO_JOB_NOT_FOUND", "video job not found")
	ErrVideoCallQuotaExceeded = kerrors.New(429, "VIDEO_CALL_QUOTA_EXCEEDED", "hourly video call quota exceeded")
)
