package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/go-kratos/kratos/v2/log"

	"github.com/opscenter/ai-gateway/internal/biz"
)

// SessionCookieName is the console's session cookie (docs/design/04-multi-
// tenancy-and-auth.md, JWT session issued by AuthUseCase.CompleteLogin).
const SessionCookieName = "aigw_session"

// AdminAuth protects the management API (/ai/gateway/*) and resolves one of
// three principal kinds, in order: the bootstrap admin token (unchanged fast
// path — existing deployments and every prior test keep working exactly as
// before), an admin API key (`aik-*` bearer), or an OIDC session cookie. All
// three populate the same biz.Principal so handlers never branch on how the
// caller authenticated (see rbac.go).
type AdminAuth struct {
	token  string
	authUC *biz.AuthUseCase
	logger *log.Helper
}

func NewAdminAuth(token string, authUC *biz.AuthUseCase, logger log.Logger) *AdminAuth {
	helper := log.NewHelper(logger)
	if strings.TrimSpace(token) == "" {
		helper.Warn("system.admin_token 未配置：管理 API 处于无认证状态，仅可在受信反向代理之后运行（生产环境请务必设置 AIGW_ADMIN_TOKEN）")
	}
	return &AdminAuth{token: strings.TrimSpace(token), authUC: authUC, logger: helper}
}

// Middleware resolves a principal and stores it in the request context. When
// no admin_token is configured the plane stays open (today's behavior),
// mapped to the synthetic super-admin principal so downstream role checks
// never see a nil principal.
func (a *AdminAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if a.token == "" {
			next.ServeHTTP(w, r.WithContext(biz.WithPrincipal(ctx, biz.BootstrapPrincipal())))
			return
		}

		got := extractBearerToken(r)
		if got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) == 1 {
			next.ServeHTTP(w, r.WithContext(biz.WithPrincipal(ctx, biz.BootstrapPrincipal())))
			return
		}

		if a.authUC != nil {
			if strings.HasPrefix(got, "aik-") {
				if p, err := a.authUC.ResolvePrincipalFromAdminKey(ctx, got); err == nil {
					next.ServeHTTP(w, r.WithContext(biz.WithPrincipal(ctx, p)))
					return
				}
			}
			if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
				if p, err := a.authUC.ResolvePrincipalFromSession(ctx, cookie.Value); err == nil {
					next.ServeHTTP(w, r.WithContext(biz.WithPrincipal(ctx, p)))
					return
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":"ADMIN_UNAUTHORIZED","msg":"missing or invalid admin token"}`))
	})
}

// RequireRole is a handler-level guard for endpoints named in the RBAC table
// (docs/design/04): call it first thing in a handler, after decoding the
// tenantID the action applies to. Writes 403 and returns false when denied.
func RequireRole(w http.ResponseWriter, r *http.Request, tenantID uint, minRole string) bool {
	p := biz.PrincipalFromRequest(r)
	if p.HasRole(tenantID, minRole) {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(`{"code":"FORBIDDEN","msg":"insufficient role for this action"}`))
	return false
}

// RequirePlatformAdmin wraps a route that manages global objects (providers,
// price tables, model catalog, settings — docs/design/04: "the one deliberate
// sharing point") so only a platform-admin principal may call it, without
// touching every handler body individually.
func RequirePlatformAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !RequireRole(w, r, 0, "owner") {
			return
		}
		next(w, r)
	}
}
