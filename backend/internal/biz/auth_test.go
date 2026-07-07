package biz

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	josejwk "github.com/go-jose/go-jose/v4"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

// mockOIDC is a minimal, self-contained OIDC provider (discovery doc + JWKS +
// token endpoint) so the login flow can be exercised end-to-end offline —
// matching the project's "no external services" test ethos. Real IdPs
// (Okta/Auth0/Keycloak/etc.) all implement the same discovery/JWKS/token
// contract, so this is a faithful stand-in, not a shortcut around the logic
// under test.
type mockOIDC struct {
	srv      *httptest.Server
	priv     *rsa.PrivateKey
	clientID string
	subject  string
	email    string
	groups   []string
}

func newMockOIDC(t *testing.T) *mockOIDC {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	m := &mockOIDC{priv: priv, clientID: "test-client", subject: "user-sub-123", email: "alice@example.com", groups: []string{"admin"}}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"issuer":                                m.srv.URL,
			"authorization_endpoint":                m.srv.URL + "/authorize",
			"token_endpoint":                        m.srv.URL + "/token",
			"jwks_uri":                              m.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		set := josejwk.JSONWebKeySet{Keys: []josejwk.JSONWebKey{{
			Key: &m.priv.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig",
		}}}
		json.NewEncoder(w).Encode(set)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		idToken := m.signIDToken(t)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"id_token":     idToken,
		})
	})
	m.srv = httptest.NewServer(mux)
	return m
}

func (m *mockOIDC) signIDToken(t *testing.T) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": m.srv.URL, "sub": m.subject, "aud": m.clientID,
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
		"email": m.email, "groups": m.groups,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-key"
	signed, err := token.SignedString(m.priv)
	if err != nil {
		t.Fatalf("sign id token: %v", err)
	}
	return signed
}

func newTestDBForAuth(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.AITenant{}, &model.AIUser{}, &model.AIUserTenantRole{}, &model.AIAdminKey{}, &model.AIAdminAuditLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&model.AITenant{Name: model.DefaultTenantName, DisplayName: "Default", Status: "active"}).Error; err != nil {
		t.Fatalf("seed default tenant: %v", err)
	}
	return db
}

func TestOIDCLoginFlow(t *testing.T) {
	mock := newMockOIDC(t)
	defer mock.srv.Close()

	db := newTestDBForAuth(t)
	authCfg := &conf.Auth{OIDCIssuer: mock.srv.URL, OIDCClientID: mock.clientID, OIDCRedirectURL: "http://localhost/callback"}
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	uc := NewAuthUseCase(db, authCfg, sysCfg, log.NewStdLogger(testWriter{t}))

	if !uc.OIDCEnabled() {
		t.Fatal("expected OIDC to be enabled when OIDCIssuer is set")
	}

	ctx := context.Background()
	authURL, err := uc.BeginLogin(ctx, "test-state")
	if err != nil {
		t.Fatalf("begin login: %v", err)
	}
	if !strings.Contains(authURL, mock.srv.URL) || !strings.Contains(authURL, "state=test-state") {
		t.Fatalf("unexpected auth URL: %s", authURL)
	}

	user, token, err := uc.CompleteLogin(ctx, "any-code")
	if err != nil {
		t.Fatalf("complete login: %v", err)
	}
	if user.Email != mock.email || user.OIDCSubject != mock.subject {
		t.Fatalf("unexpected user: %+v", user)
	}
	if !user.IsEnabled {
		t.Fatal("JIT-provisioned user should be enabled by default")
	}

	var role model.AIUserTenantRole
	if err := db.Where("user_id = ?", user.ID).First(&role).Error; err != nil {
		t.Fatalf("expected a tenant role row: %v", err)
	}
	if role.Role != "admin" {
		t.Fatalf("expected role 'admin' from the groups claim, got %q", role.Role)
	}

	principal, err := uc.ResolvePrincipalFromSession(ctx, token)
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if principal.Name != mock.email || principal.IsPlatformAdmin {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	if !principal.HasRole(role.TenantID, "admin") || principal.HasRole(role.TenantID, "owner") {
		t.Fatalf("principal role scoping wrong: %+v", principal)
	}

	// Logging in again (same subject) must update, not duplicate, the user row.
	user2, _, err := uc.CompleteLogin(ctx, "any-code")
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if user2.ID != user.ID {
		t.Fatalf("expected the same user on repeat login, got a new one: %d vs %d", user2.ID, user.ID)
	}
	var userCount int64
	db.Model(&model.AIUser{}).Count(&userCount)
	if userCount != 1 {
		t.Fatalf("expected exactly one user row, got %d", userCount)
	}
}

func TestOIDCDisabledWhenIssuerUnset(t *testing.T) {
	db := newTestDBForAuth(t)
	uc := NewAuthUseCase(db, &conf.Auth{}, &conf.System{EncryptionKey: testEncryptionKey[:32]}, log.NewStdLogger(testWriter{t}))
	if uc.OIDCEnabled() {
		t.Fatal("expected OIDC disabled with no issuer configured")
	}
	if _, err := uc.BeginLogin(context.Background(), "s"); err == nil {
		t.Fatal("expected BeginLogin to fail when OIDC is not configured")
	}
}

func TestAdminKeyLifecycle(t *testing.T) {
	db := newTestDBForAuth(t)
	uc := NewAuthUseCase(db, &conf.Auth{}, &conf.System{EncryptionKey: testEncryptionKey[:32]}, log.NewStdLogger(testWriter{t}))
	ctx := context.Background()

	created, err := uc.CreateAdminKey(ctx, &dto.CreateAdminKeyReq{Name: "ci-bot", TenantID: 1, Role: "admin"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(created.PlainKey, "aik-") {
		t.Fatalf("expected aik- prefix, got %s", created.PlainKey)
	}

	principal, err := uc.ResolvePrincipalFromAdminKey(ctx, created.PlainKey)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if principal.AdminKeyTenantID != 1 || principal.AdminKeyRole != "admin" {
		t.Fatalf("unexpected principal: %+v", principal)
	}

	disabled := false
	if err := uc.UpdateAdminKey(ctx, &dto.UpdateAdminKeyReq{ID: created.ID, IsEnabled: &disabled}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := uc.ResolvePrincipalFromAdminKey(ctx, created.PlainKey); err == nil {
		t.Fatal("expected a disabled admin key to fail resolution")
	}

	if err := uc.DeleteAdminKey(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := uc.DeleteAdminKey(ctx, created.ID); err == nil {
		t.Fatal("expected not-found deleting twice")
	}
}

func TestUserTenantRoleManagement(t *testing.T) {
	db := newTestDBForAuth(t)
	uc := NewAuthUseCase(db, &conf.Auth{}, &conf.System{EncryptionKey: testEncryptionKey[:32]}, log.NewStdLogger(testWriter{t}))
	ctx := context.Background()

	user := &model.AIUser{Email: "bob@example.com", OIDCSubject: "sub-bob", IsEnabled: true}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if err := uc.UpdateUserTenantRole(ctx, &dto.UpdateUserTenantRoleReq{UserID: user.ID, TenantID: 1, Role: "viewer"}); err != nil {
		t.Fatalf("set role: %v", err)
	}
	items, err := uc.ListUsers(ctx, 1)
	if err != nil || len(items) != 1 || items[0].Role != "viewer" {
		t.Fatalf("list users: %v, %+v", err, items)
	}

	if err := uc.UpdateUserTenantRole(ctx, &dto.UpdateUserTenantRoleReq{UserID: user.ID, TenantID: 1, Role: "invalid-role"}); err == nil {
		t.Fatal("expected invalid role to be rejected")
	}

	// Removing membership (empty role) drops the user from that tenant's list.
	if err := uc.UpdateUserTenantRole(ctx, &dto.UpdateUserTenantRoleReq{UserID: user.ID, TenantID: 1, Role: ""}); err != nil {
		t.Fatalf("remove role: %v", err)
	}
	items, _ = uc.ListUsers(ctx, 1)
	if len(items) != 0 {
		t.Fatalf("expected no members after role removal, got %+v", items)
	}

	if err := uc.SetUserEnabled(ctx, user.ID, false); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	var reloaded model.AIUser
	db.First(&reloaded, user.ID)
	if reloaded.IsEnabled {
		t.Fatal("expected user to be disabled")
	}
}
