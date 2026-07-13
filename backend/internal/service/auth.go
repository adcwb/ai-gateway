package service

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/opscenter/ai-gateway/internal/biz"
	"github.com/opscenter/ai-gateway/internal/biz/dto"
	"github.com/opscenter/ai-gateway/internal/middleware"
)

// AuthService handles OIDC/SSO login and the console session cookie
// (docs/design/04-multi-tenancy-and-auth.md). Its three login-flow handlers
// are registered on the *unauthenticated* mux — they are how a caller gets a
// session in the first place.
type AuthService struct {
	uc *biz.AuthUseCase
}

func NewAuthService(uc *biz.AuthUseCase) *AuthService {
	return &AuthService{uc: uc}
}

const oidcStateCookie = "aigw_oidc_state"

func (s *AuthService) AuthConfig(w http.ResponseWriter, r *http.Request) {
	resp := dto.AuthConfigResp{OIDCEnabled: s.uc.OIDCEnabled()}
	okWith(w, resp)
}

// Login redirects the browser to the OIDC provider, stashing a random state
// value in a short-lived cookie for CSRF protection on the callback.
func (s *AuthService) Login(w http.ResponseWriter, r *http.Request) {
	stateBytes := make([]byte, 16)
	rand.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)

	authURL, err := s.uc.BeginLogin(r.Context(), state)
	if err != nil {
		failWithErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookie, Value: state, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: biz.IsRequestSecure(r), Expires: time.Now().Add(10 * time.Minute),
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// Callback verifies state, completes the OIDC exchange, and sets the session cookie.
func (s *AuthService) Callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	stateCookie, err := r.Cookie(oidcStateCookie)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != q.Get("state") {
		failWith(w, http.StatusBadRequest, "invalid or expired login state")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: "/", MaxAge: -1})

	_, token, err := s.uc.CompleteLogin(r.Context(), q.Get("code"))
	if err != nil {
		failWithErr(w, err)
		return
	}
	// Secure is inferred per-request (direct TLS or a TLS-terminating reverse
	// proxy's X-Forwarded-Proto) rather than hardcoded — see biz.IsRequestSecure.
	// Expires now matches the JWT's own TTL (auth.session_ttl_hours, default
	// 24h) instead of a separately hardcoded 24h that could silently drift
	// from it once that config is changed.
	http.SetCookie(w, &http.Cookie{
		Name: middleware.SessionCookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: biz.IsRequestSecure(r), Expires: time.Now().Add(s.uc.SessionTTL()),
	})
	http.Redirect(w, r, "/console/", http.StatusFound)
}

func (s *AuthService) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: middleware.SessionCookieName, Value: "", Path: "/", MaxAge: -1})
	okWith(w, map[string]any{"loggedOut": true})
}

// Me returns the currently authenticated principal (console "who am I").
func (s *AuthService) Me(w http.ResponseWriter, r *http.Request) {
	p := biz.PrincipalFromRequest(r)
	if p == nil {
		failWith(w, http.StatusUnauthorized, "no active session")
		return
	}
	okWith(w, dto.SessionResp{UserID: p.ID, Email: p.Name, IsPlatformAdmin: p.IsPlatformAdmin})
}

// -----------------------------------------------------------------------------
// Admin API keys
// -----------------------------------------------------------------------------

func (s *AuthService) CreateAdminKey(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateAdminKeyReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !middleware.RequireRole(w, r, req.TenantID, "owner") {
		return
	}
	resp, err := s.uc.CreateAdminKey(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, resp)
}

func (s *AuthService) ListAdminKeys(w http.ResponseWriter, r *http.Request) {
	tenantID := uintQuery(r, "tenantId")
	list, err := s.uc.ListAdminKeys(r.Context(), tenantID)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, list)
}

func (s *AuthService) UpdateAdminKey(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateAdminKeyReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !middleware.RequireRole(w, r, 0, "owner") {
		return
	}
	if err := s.uc.UpdateAdminKey(r.Context(), &req); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"updated": true})
}

func (s *AuthService) DeleteAdminKey(w http.ResponseWriter, r *http.Request) {
	if !middleware.RequireRole(w, r, 0, "owner") {
		return
	}
	id := uintQuery(r, "id")
	if id == 0 {
		failWith(w, http.StatusBadRequest, "missing or invalid id")
		return
	}
	if err := s.uc.DeleteAdminKey(r.Context(), id); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"deleted": id})
}

// -----------------------------------------------------------------------------
// Users (JIT-provisioned via OIDC; this is membership/role management, not
// account creation)
// -----------------------------------------------------------------------------

func (s *AuthService) ListUsers(w http.ResponseWriter, r *http.Request) {
	tenantID := uintQuery(r, "tenantId")
	list, err := s.uc.ListUsers(r.Context(), tenantID)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, list)
}

func (s *AuthService) UpdateUserRole(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateUserTenantRoleReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !middleware.RequireRole(w, r, req.TenantID, "owner") {
		return
	}
	if err := s.uc.UpdateUserTenantRole(r.Context(), &req); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"updated": true})
}

func (s *AuthService) UpdateUserStatus(w http.ResponseWriter, r *http.Request) {
	if !middleware.RequireRole(w, r, 0, "owner") {
		return
	}
	var req struct {
		UserID    uint `json:"userId"`
		IsEnabled bool `json:"isEnabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.uc.SetUserEnabled(r.Context(), req.UserID, req.IsEnabled); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"updated": true})
}
