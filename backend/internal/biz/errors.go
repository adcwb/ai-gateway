package biz

import kerrors "github.com/go-kratos/kratos/v2/errors"

// Gateway-specific typed errors. Callers can use errors.Is() or kerrors.FromError()
// to inspect these at the service layer and map them to correct HTTP status codes.
var (
	ErrVirtualKeyNotFound     = kerrors.NotFound("VIRTUAL_KEY_NOT_FOUND", "virtual key not found")
	ErrKeyPlaintextNotStored  = kerrors.NotFound("KEY_PLAINTEXT_NOT_STORED", "plain key was not stored (legacy key, created before encryption was enabled)")
	ErrKeyGenerationFailed    = kerrors.InternalServer("KEY_GENERATION_FAILED", "failed to generate random key material")
	ErrEncryptionFailed       = kerrors.InternalServer("ENCRYPTION_FAILED", "key encryption failed")
	ErrDecryptionFailed       = kerrors.InternalServer("DECRYPTION_FAILED", "key decryption failed")
	ErrInvalidIPWhitelist     = kerrors.BadRequest("INVALID_IP_WHITELIST", "IP whitelist must be a JSON array of strings")
	ErrInvalidIPEntry         = kerrors.BadRequest("INVALID_IP_ENTRY", "invalid IP address or CIDR notation")
	ErrIPWhitelistEmpty       = kerrors.BadRequest("IP_WHITELIST_EMPTY", "at least one IP or CIDR is required when IP whitelist is enabled")
	ErrProviderNotFound       = kerrors.NotFound("PROVIDER_NOT_FOUND", "provider not found")
	ErrProviderInvalid        = kerrors.BadRequest("PROVIDER_INVALID", "provider name, baseUrl and apiKey are required")
	ErrProviderNameExists     = kerrors.BadRequest("PROVIDER_NAME_EXISTS", "a provider with this name already exists")
	ErrTenantNotFound         = kerrors.NotFound("TENANT_NOT_FOUND", "tenant not found")
	ErrTenantInvalid          = kerrors.BadRequest("TENANT_INVALID", "tenant/project name is required")
	ErrTenantNameExists       = kerrors.BadRequest("TENANT_NAME_EXISTS", "a tenant/project with this name already exists")
	ErrBillingAccountNotFound = kerrors.NotFound("BILLING_ACCOUNT_NOT_FOUND", "billing account not found")
	ErrBillingInvalidAmount   = kerrors.BadRequest("BILLING_INVALID_AMOUNT", "recharge amount must be positive")
	ErrBillingSuspended       = kerrors.New(402, "BILLING_SUSPENDED", "account suspended: insufficient balance")
	ErrProviderSyncUnsupported = kerrors.BadRequest("PROVIDER_SYNC_UNSUPPORTED", "model sync is only supported for openai_compatible providers")
	ErrProviderSyncFailed      = kerrors.New(502, "PROVIDER_SYNC_FAILED", "failed to fetch model list from the upstream provider")
	ErrInsufficientBalance    = kerrors.New(402, "INSUFFICIENT_BALANCE", "insufficient balance for this request")

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
)
