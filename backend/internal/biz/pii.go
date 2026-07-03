package biz

import (
	"context"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

// piiAuditInfo stores the PII action and matched types for audit logging.
type piiAuditInfo struct {
	action string
	types  string
}

// piiAuditCtxKey is the context key for synchronous PII audit info.
type piiAuditCtxKey struct{}

// piiAsyncLogKey is the context key for an async PII result channel.
type piiAsyncLogKey struct{}

func piiAuditFromCtx(ctx context.Context) *piiAuditInfo {
	if v, ok := ctx.Value(piiAuditCtxKey{}).(*piiAuditInfo); ok {
		return v
	}
	return nil
}

// piiOutput is the result of PII policy application.
type piiOutput struct {
	Blocked bool
	NewBody []byte
	Types   string
}

// applyPIIPolicy applies the PII policy for the given key to the request body.
// This is a stub implementation: full PII detection engine integration is done separately.
// The stub always passes through without blocking.
func (uc *GatewayUseCase) applyPIIPolicy(ctx context.Context, key *model.AIVirtualKey, body []byte) (context.Context, piiOutput) {
	return ctx, piiOutput{Blocked: false, NewBody: body}
}
