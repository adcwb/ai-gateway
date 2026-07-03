package biz

import (
	"context"
	"net/http"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

type virtualKeyCtxKey struct{}

// WithVirtualKey stores the resolved virtual key in the context.
func WithVirtualKey(ctx context.Context, key *model.AIVirtualKey) context.Context {
	return context.WithValue(ctx, virtualKeyCtxKey{}, key)
}

// VirtualKeyFromCtx retrieves the virtual key from the context.
func VirtualKeyFromCtx(ctx context.Context) *model.AIVirtualKey {
	if v, ok := ctx.Value(virtualKeyCtxKey{}).(*model.AIVirtualKey); ok {
		return v
	}
	return nil
}

// VirtualKeyFromRequest retrieves the virtual key stored in the request context.
func VirtualKeyFromRequest(r *http.Request) *model.AIVirtualKey {
	return VirtualKeyFromCtx(r.Context())
}
