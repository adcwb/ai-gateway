package biz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	oidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-kratos/kratos/v2/log"
	"golang.org/x/oauth2"
	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data/model"
	"github.com/opscenter/ai-gateway/internal/pkg"
)

const defaultSessionTTLHours = 24

// AuthUseCase owns OIDC/SSO login, session issuance, admin API keys, and user
// management (docs/design/04-multi-tenancy-and-auth.md). OIDC discovery is
// lazy: an unreachable/misconfigured issuer must never block server startup
// (the bootstrap admin token keeps working regardless).
type AuthUseCase struct {
	db      *gorm.DB
	authCfg *conf.Auth
	sysCfg  *conf.System
	logger  *log.Helper

	initOnce  sync.Once
	initErr   error
	provider  *oidc.Provider
	verifier  *oidc.IDTokenVerifier
	oauth2Cfg oauth2.Config
}

func NewAuthUseCase(db *gorm.DB, authCfg *conf.Auth, sysCfg *conf.System, logger log.Logger) *AuthUseCase {
	return &AuthUseCase{db: db, authCfg: authCfg, sysCfg: sysCfg, logger: log.NewHelper(logger)}
}

func (uc *AuthUseCase) OIDCEnabled() bool {
	return uc.authCfg != nil && strings.TrimSpace(uc.authCfg.OIDCIssuer) != ""
}

func (uc *AuthUseCase) sessionSecret() []byte {
	if uc.authCfg != nil && uc.authCfg.SessionSecret != "" {
		return []byte(uc.authCfg.SessionSecret)
	}
	if uc.sysCfg != nil {
		return []byte(uc.sysCfg.EncryptionKey)
	}
	return []byte("")
}

func (uc *AuthUseCase) sessionTTL() time.Duration {
	hours := defaultSessionTTLHours
	if uc.authCfg != nil && uc.authCfg.SessionTTLHours > 0 {
		hours = uc.authCfg.SessionTTLHours
	}
	return time.Duration(hours) * time.Hour
}

// ensureProvider performs OIDC discovery once, lazily, on first use.
func (uc *AuthUseCase) ensureProvider(ctx context.Context) error {
	if !uc.OIDCEnabled() {
		return ErrOIDCNotConfigured
	}
	uc.initOnce.Do(func() {
		provider, err := oidc.NewProvider(ctx, uc.authCfg.OIDCIssuer)
		if err != nil {
			uc.initErr = err
			uc.logger.Warnf("auth: OIDC discovery 失败，SSO 登录暂不可用 issuer=%s err=%v", uc.authCfg.OIDCIssuer, err)
			return
		}
		uc.provider = provider
		uc.verifier = provider.Verifier(&oidc.Config{ClientID: uc.authCfg.OIDCClientID})
		uc.oauth2Cfg = oauth2.Config{
			ClientID:     uc.authCfg.OIDCClientID,
			ClientSecret: uc.authCfg.OIDCClientSecret,
			RedirectURL:  uc.authCfg.OIDCRedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		}
	})
	return uc.initErr
}

// BeginLogin returns the provider's authorization URL for the given opaque
// state (the caller is responsible for round-tripping state via a short-lived
// cookie or query echo — CSRF protection for the OAuth2 code flow).
func (uc *AuthUseCase) BeginLogin(ctx context.Context, state string) (string, error) {
	if err := uc.ensureProvider(ctx); err != nil {
		return "", ErrOIDCNotConfigured
	}
	return uc.oauth2Cfg.AuthCodeURL(state), nil
}

// CompleteLogin exchanges the authorization code, verifies the ID token, JIT-
// provisions/updates the AIUser + tenant role, and issues a session JWT.
func (uc *AuthUseCase) CompleteLogin(ctx context.Context, code string) (*model.AIUser, string, error) {
	if err := uc.ensureProvider(ctx); err != nil {
		return nil, "", ErrOIDCNotConfigured
	}
	oauth2Token, err := uc.oauth2Cfg.Exchange(ctx, code)
	if err != nil {
		return nil, "", ErrOIDCLoginFailed.WithMetadata(map[string]string{"err": err.Error()})
	}
	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, "", ErrOIDCLoginFailed.WithMetadata(map[string]string{"err": "no id_token in response"})
	}
	idToken, err := uc.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, "", ErrOIDCLoginFailed.WithMetadata(map[string]string{"err": err.Error()})
	}

	var claims struct {
		Subject string   `json:"sub"`
		Email   string   `json:"email"`
		Name    string   `json:"name"`
		Groups  []string `json:"groups"`
		Roles   []string `json:"roles"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, "", ErrOIDCLoginFailed.WithMetadata(map[string]string{"err": "claims decode: " + err.Error()})
	}
	if claims.Subject == "" || claims.Email == "" {
		return nil, "", ErrOIDCLoginFailed.WithMetadata(map[string]string{"err": "id token missing sub/email"})
	}

	user, err := uc.upsertUser(ctx, claims.Subject, claims.Email, claims.Name)
	if err != nil {
		return nil, "", err
	}

	role := uc.resolveRole(claims.Groups, claims.Roles)
	tenantID, terr := uc.resolveDefaultTenantID(ctx)
	if terr == nil && tenantID != 0 && !user.IsPlatformAdmin {
		uc.upsertUserRole(ctx, user.ID, tenantID, role)
	}

	token, err := pkg.IssueSessionToken(uc.sessionSecret(), user.ID, user.Email, user.IsPlatformAdmin, uc.sessionTTL())
	if err != nil {
		return nil, "", ErrOIDCLoginFailed.WithMetadata(map[string]string{"err": "session issuance: " + err.Error()})
	}
	recordAdminAudit(ctx, uc.db, uc.logger, &Principal{Kind: PrincipalKindSession, ID: user.ID, Name: user.Email}, tenantID, "login", "user", user.Email, "")
	return user, token, nil
}

func (uc *AuthUseCase) upsertUser(ctx context.Context, subject, email, name string) (*model.AIUser, error) {
	var user model.AIUser
	err := uc.db.WithContext(ctx).Where("oidc_subject = ?", subject).First(&user).Error
	now := time.Now()
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		// First login for this subject: link to an existing row by email
		// (pre-provisioned platform admin) or create a fresh JIT user.
		if ferr := uc.db.WithContext(ctx).Where("email = ?", email).First(&user).Error; ferr == nil {
			user.OIDCSubject = subject
			user.LastLoginAt = &now
			uc.db.WithContext(ctx).Model(&user).Updates(map[string]interface{}{"oidc_subject": subject, "last_login_at": now})
			return &user, nil
		}
		user = model.AIUser{Email: email, DisplayName: name, OIDCSubject: subject, IsEnabled: true, LastLoginAt: &now}
		if cerr := uc.db.WithContext(ctx).Create(&user).Error; cerr != nil {
			return nil, ErrOIDCLoginFailed.WithMetadata(map[string]string{"err": "user provisioning: " + cerr.Error()})
		}
		return &user, nil
	case err != nil:
		return nil, ErrOIDCLoginFailed.WithMetadata(map[string]string{"err": err.Error()})
	default:
		uc.db.WithContext(ctx).Model(&user).Update("last_login_at", now)
		if !user.IsEnabled {
			return nil, ErrOIDCLoginFailed.WithMetadata(map[string]string{"err": "user account disabled"})
		}
		return &user, nil
	}
}

// resolveRole maps OIDC group/role claims onto one of the four fixed roles;
// falls back to the configured default (viewer if unset).
func (uc *AuthUseCase) resolveRole(groups, roles []string) string {
	def := model.RoleViewer
	if uc.authCfg != nil && uc.authCfg.OIDCDefaultRole != "" {
		def = uc.authCfg.OIDCDefaultRole
	}
	candidates := append(append([]string{}, groups...), roles...)
	best := ""
	for _, c := range candidates {
		c = strings.ToLower(strings.TrimSpace(c))
		if model.RoleRank(c) > model.RoleRank(best) {
			best = c
		}
	}
	if best == "" {
		return def
	}
	return best
}

func (uc *AuthUseCase) resolveDefaultTenantID(ctx context.Context) (uint, error) {
	name := model.DefaultTenantName
	if uc.authCfg != nil && uc.authCfg.OIDCDefaultTenant != "" {
		name = uc.authCfg.OIDCDefaultTenant
	}
	var t model.AITenant
	if err := uc.db.WithContext(ctx).Where("name = ?", name).First(&t).Error; err != nil {
		return 0, err
	}
	return t.ID, nil
}

func (uc *AuthUseCase) upsertUserRole(ctx context.Context, userID, tenantID uint, role string) {
	err := uc.db.WithContext(ctx).
		Where("user_id = ? AND tenant_id = ?", userID, tenantID).
		Assign(map[string]interface{}{"user_id": userID, "tenant_id": tenantID, "role": role}).
		FirstOrCreate(&model.AIUserTenantRole{}).Error
	if err != nil {
		uc.logger.Warnf("auth: 写入用户租户角色失败 userID=%d tenantID=%d err=%v", userID, tenantID, err)
	}
}

// ResolvePrincipalFromSession verifies a session JWT and loads the current
// tenant-role membership map for RBAC checks.
func (uc *AuthUseCase) ResolvePrincipalFromSession(ctx context.Context, token string) (*Principal, error) {
	claims, err := pkg.ParseSessionToken(uc.sessionSecret(), token)
	if err != nil {
		return nil, ErrSessionInvalid
	}
	var user model.AIUser
	if err := uc.db.WithContext(ctx).First(&user, claims.UserID).Error; err != nil || !user.IsEnabled {
		return nil, ErrSessionInvalid
	}
	var roles []model.AIUserTenantRole
	uc.db.WithContext(ctx).Where("user_id = ?", user.ID).Find(&roles)
	roleMap := make(map[uint]string, len(roles))
	for _, r := range roles {
		roleMap[r.TenantID] = r.Role
	}
	return &Principal{
		Kind: PrincipalKindSession, ID: user.ID, Name: user.Email,
		IsPlatformAdmin: user.IsPlatformAdmin, TenantRoles: roleMap,
	}, nil
}

// ResolvePrincipalFromAdminKey looks up a plaintext admin key by its SHA-256 hash.
func (uc *AuthUseCase) ResolvePrincipalFromAdminKey(ctx context.Context, plainKey string) (*Principal, error) {
	sum := sha256.Sum256([]byte(plainKey))
	hash := hex.EncodeToString(sum[:])
	var key model.AIAdminKey
	if err := uc.db.WithContext(ctx).Where("key_hash = ? AND is_enabled = ?", hash, true).First(&key).Error; err != nil {
		return nil, ErrSessionInvalid
	}
	go func() {
		now := time.Now()
		uc.db.WithContext(context.Background()).Model(&model.AIAdminKey{}).Where("id = ?", key.ID).Update("last_used_at", now)
	}()
	return &Principal{
		Kind: PrincipalKindAdminKey, ID: key.ID, Name: key.Name,
		AdminKeyTenantID: key.TenantID, AdminKeyRole: key.Role,
	}, nil
}

// -----------------------------------------------------------------------------
// Admin API keys (docs/design/04 "Admin API keys" — machine principals)
// -----------------------------------------------------------------------------

func (uc *AuthUseCase) CreateAdminKey(ctx context.Context, req *dto.CreateAdminKeyReq) (*dto.CreateAdminKeyResp, error) {
	if strings.TrimSpace(req.Name) == "" || model.RoleRank(req.Role) == 0 {
		return nil, ErrAdminKeyInvalid
	}
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return nil, ErrKeyGenerationFailed
	}
	plainKey := "aik-" + hex.EncodeToString(rawBytes)
	sum := sha256.Sum256([]byte(plainKey))
	keyHash := hex.EncodeToString(sum[:])
	keyPrefix := plainKey[:16]

	plainKeyEncrypted, err := pkg.EncryptAES(plainKey, []byte(uc.sysCfg.EncryptionKey))
	if err != nil {
		return nil, ErrEncryptionFailed
	}
	k := &model.AIAdminKey{
		Name: strings.TrimSpace(req.Name), KeyHash: keyHash, KeyPrefix: keyPrefix,
		PlainKeyEncrypted: plainKeyEncrypted, TenantID: req.TenantID, Role: req.Role, IsEnabled: true,
	}
	if err := uc.db.WithContext(ctx).Create(k).Error; err != nil {
		return nil, err
	}
	return &dto.CreateAdminKeyResp{ID: k.ID, Name: k.Name, KeyPrefix: k.KeyPrefix, PlainKey: plainKey}, nil
}

func (uc *AuthUseCase) ListAdminKeys(ctx context.Context, tenantID uint) ([]model.AIAdminKey, error) {
	q := uc.db.WithContext(ctx).Order("id asc")
	if tenantID > 0 {
		q = q.Where("tenant_id = ?", tenantID)
	}
	var list []model.AIAdminKey
	if err := q.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (uc *AuthUseCase) UpdateAdminKey(ctx context.Context, req *dto.UpdateAdminKeyReq) error {
	updates := map[string]interface{}{}
	if req.Role != nil {
		if model.RoleRank(*req.Role) == 0 {
			return ErrRoleInvalid
		}
		updates["role"] = *req.Role
	}
	if req.IsEnabled != nil {
		updates["is_enabled"] = *req.IsEnabled
	}
	if len(updates) == 0 {
		return nil
	}
	res := uc.db.WithContext(ctx).Model(&model.AIAdminKey{}).Where("id = ?", req.ID).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrAdminKeyNotFound
	}
	return nil
}

func (uc *AuthUseCase) DeleteAdminKey(ctx context.Context, id uint) error {
	res := uc.db.WithContext(ctx).Delete(&model.AIAdminKey{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrAdminKeyNotFound
	}
	return nil
}

// -----------------------------------------------------------------------------
// User management (JIT-provisioned users only — no local password accounts)
// -----------------------------------------------------------------------------

func (uc *AuthUseCase) ListUsers(ctx context.Context, tenantID uint) ([]dto.UserItem, error) {
	var users []model.AIUser
	if err := uc.db.WithContext(ctx).Order("id asc").Find(&users).Error; err != nil {
		return nil, err
	}
	roleByUser := map[uint]string{}
	if tenantID > 0 {
		var roles []model.AIUserTenantRole
		uc.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Find(&roles)
		for _, r := range roles {
			roleByUser[r.UserID] = r.Role
		}
	}
	items := make([]dto.UserItem, 0, len(users))
	for _, u := range users {
		role := roleByUser[u.ID]
		if tenantID > 0 && role == "" && !u.IsPlatformAdmin {
			continue // not a member of the tenant being viewed
		}
		items = append(items, dto.UserItem{
			ID: u.ID, Email: u.Email, DisplayName: u.DisplayName,
			IsPlatformAdmin: u.IsPlatformAdmin, IsEnabled: u.IsEnabled, Role: role,
		})
	}
	return items, nil
}

// UpdateUserTenantRole upserts a role, or removes membership when role is empty.
func (uc *AuthUseCase) UpdateUserTenantRole(ctx context.Context, req *dto.UpdateUserTenantRoleReq) error {
	if req.UserID == 0 || req.TenantID == 0 {
		return ErrUserNotFound
	}
	if req.Role == "" {
		return uc.db.WithContext(ctx).
			Where("user_id = ? AND tenant_id = ?", req.UserID, req.TenantID).
			Delete(&model.AIUserTenantRole{}).Error
	}
	if model.RoleRank(req.Role) == 0 {
		return ErrRoleInvalid
	}
	uc.upsertUserRole(ctx, req.UserID, req.TenantID, req.Role)
	return nil
}

func (uc *AuthUseCase) SetUserEnabled(ctx context.Context, userID uint, enabled bool) error {
	res := uc.db.WithContext(ctx).Model(&model.AIUser{}).Where("id = ?", userID).Update("is_enabled", enabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}
