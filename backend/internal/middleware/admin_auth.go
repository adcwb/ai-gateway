package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/go-kratos/kratos/v2/log"
)

// AdminAuth protects the management API (/ai/gateway/*) with a static bearer
// token (docs/design/04-multi-tenancy-and-auth.md, P0 bootstrap principal).
// When no token is configured the middleware passes traffic through and a
// startup warning is emitted — acceptable only behind a trusted reverse proxy.
type AdminAuth struct {
	token  string
	logger *log.Helper
}

func NewAdminAuth(token string, logger log.Logger) *AdminAuth {
	helper := log.NewHelper(logger)
	if strings.TrimSpace(token) == "" {
		helper.Warn("system.admin_token 未配置：管理 API 处于无认证状态，仅可在受信反向代理之后运行（生产环境请务必设置 AIGW_ADMIN_TOKEN）")
	}
	return &AdminAuth{token: strings.TrimSpace(token), logger: helper}
}

// Middleware enforces `Authorization: Bearer <admin_token>` when a token is set.
func (a *AdminAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := extractBearerToken(r)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"code":"ADMIN_UNAUTHORIZED","msg":"missing or invalid admin token"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
