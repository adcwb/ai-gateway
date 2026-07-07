package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/opscenter/ai-gateway/internal/biz"
	"github.com/opscenter/ai-gateway/internal/observability"
)

// VirtualKeyAuth is an HTTP middleware that authenticates requests via Bearer sk-vk-* token.
// It resolves the virtual key, checks it is enabled and not expired, enforces IP whitelist,
// checks top-level quota (CheckAndReserve), and stores the resolved key in the request context.
type VirtualKeyAuth struct {
	gateway *biz.GatewayUseCase
	quota   *biz.QuotaManager
}

func NewVirtualKeyAuth(gateway *biz.GatewayUseCase, quota *biz.QuotaManager) *VirtualKeyAuth {
	return &VirtualKeyAuth{gateway: gateway, quota: quota}
}

// ProxyMiddleware returns an HTTP handler middleware for the proxy routes.
func (m *VirtualKeyAuth) ProxyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// aigw.auth spans only key resolve + quota gate (docs/design/05-observability.md
		// span topology) — it must end before next.ServeHTTP, not wrap the whole request.
		ctx, authSpan := observability.Tracer.Start(r.Context(), "aigw.auth")

		token := extractVirtualKeyToken(r)
		if token == "" || !strings.HasPrefix(token, "sk-vk-") {
			authSpan.End()
			writeJSONError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}

		sum := sha256.Sum256([]byte(token))
		hash := hex.EncodeToString(sum[:])

		key, err := m.gateway.ResolveKeyByHash(ctx, hash)
		if err != nil {
			authSpan.End()
			writeJSONError(w, http.StatusUnauthorized, "invalid API key")
			return
		}

		if !key.IsEnabled {
			authSpan.End()
			writeJSONError(w, http.StatusForbidden, "key disabled")
			m.gateway.WriteRejectionAuditLog(ctx, key, http.StatusForbidden, "key disabled", biz.ClientIPFromRequest(r), "openai")
			return
		}
		if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
			authSpan.End()
			writeJSONError(w, http.StatusForbidden, "key expired")
			m.gateway.WriteRejectionAuditLog(ctx, key, http.StatusForbidden, "key expired", biz.ClientIPFromRequest(r), "openai")
			return
		}

		clientIP := biz.ClientIPFromRequest(r)
		if !biz.IsClientIPAllowed(key, clientIP) {
			authSpan.End()
			writeJSONError(w, http.StatusForbidden, "IP not in whitelist")
			m.gateway.WriteRejectionAuditLog(ctx, key, http.StatusForbidden, "IP not in whitelist: "+clientIP, clientIP, "openai")
			return
		}

		reqID, quotaErr := m.quota.CheckAndReserve(ctx, key)
		if quotaErr != nil {
			authSpan.End()
			writeJSONError(w, http.StatusTooManyRequests, quotaErr.Error())
			m.gateway.WriteRejectionAuditLog(ctx, key, http.StatusTooManyRequests, quotaErr.Error(), clientIP, "openai")
			return
		}
		defer m.quota.ReleaseSlot(context.Background(), key.ID, reqID)
		authSpan.End()

		ctx = biz.WithVirtualKey(ctx, key)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractVirtualKeyToken accepts either Authorization: Bearer sk-vk-* (the
// gateway's original convention) or x-api-key: sk-vk-* (the Anthropic SDK's
// convention, needed for the /anthropic/v1/messages inbound codec — D02).
func extractVirtualKeyToken(r *http.Request) string {
	if tok := extractBearerToken(r); tok != "" {
		return tok
	}
	if key := r.Header.Get("x-api-key"); key != "" {
		return key
	}
	return ""
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

func writeJSONError(w http.ResponseWriter, statusCode int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	w.Write([]byte(`{"error":{"message":"` + msg + `"}}`))
}
