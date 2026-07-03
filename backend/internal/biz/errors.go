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
)
