package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	kerrors "github.com/go-kratos/kratos/v2/errors"

	"github.com/opscenter/ai-gateway/internal/biz"
	"github.com/opscenter/ai-gateway/internal/biz/dto"
	"github.com/opscenter/ai-gateway/internal/middleware"
)

// GatewayService wraps GatewayUseCase and provides HTTP handler methods.
type GatewayService struct {
	uc *biz.GatewayUseCase
	bm *biz.BillingManager
}

func NewGatewayService(uc *biz.GatewayUseCase, bm *biz.BillingManager) *GatewayService {
	return &GatewayService{uc: uc, bm: bm}
}

// =============================================================================
// JSON response helpers
// =============================================================================

func okWith(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "data": data, "msg": "ok"})
}

func failWith(w http.ResponseWriter, statusCode int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{"code": statusCode, "msg": msg})
}

// failWithErr maps a Kratos-typed error to the appropriate HTTP status code.
// If err is a plain error (not a Kratos error), it defaults to 500.
func failWithErr(w http.ResponseWriter, err error) {
	se := kerrors.FromError(err)
	code := int(se.Code)
	if code < 100 || code > 599 {
		code = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{"code": se.Reason, "msg": se.Message})
}

func decodeJSON(r *http.Request, v interface{}) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func uintQuery(r *http.Request, key string) uint {
	v, _ := strconv.ParseUint(r.URL.Query().Get(key), 10, 64)
	return uint(v)
}

func intQuery(r *http.Request, key string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(key)); err == nil {
		return v
	}
	return def
}

func boolQueryPtr(r *http.Request, key string) *bool {
	s := r.URL.Query().Get(key)
	if s == "" {
		return nil
	}
	v := s == "true" || s == "1"
	return &v
}

func stringQueryPtr(r *http.Request, key string) *string {
	s := r.URL.Query().Get(key)
	if s == "" {
		return nil
	}
	return &s
}

// =============================================================================
// Virtual Key handlers
// =============================================================================

func (s *GatewayService) CreateVirtualKey(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateVirtualKeyReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	// TODO: extract creator ID from auth context
	var creatorID uint
	resp, err := s.uc.CreateVirtualKey(r.Context(), req, creatorID)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, resp)
}

func (s *GatewayService) RevealVirtualKey(w http.ResponseWriter, r *http.Request) {
	id := uintQuery(r, "id")
	// RBAC (docs/design/04): reveal is Owner/Admin only, checked against the
	// key's own tenant so a tenant admin can't reveal another tenant's key.
	if !middleware.RequireRole(w, r, s.uc.KeyTenantID(r.Context(), id), "admin") {
		return
	}
	resp, err := s.uc.RevealVirtualKey(r.Context(), id)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, resp)
}

func (s *GatewayService) ListVirtualKeys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := dto.ListVirtualKeysReq{
		PageInfo:   dto.PageInfo{Page: intQuery(r, "page", 1), PageSize: intQuery(r, "pageSize", 10)},
		ProviderID: uintQuery(r, "providerId"),
		IsEnabled:  boolQueryPtr(r, "isEnabled"),
		Keyword:    q.Get("keyword"),
		ProjectID:  stringQueryPtr(r, "projectId"),
	}
	list, total, err := s.uc.ListVirtualKeys(r.Context(), req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]interface{}{"list": list, "total": total})
}

func (s *GatewayService) VirtualKeyStats(w http.ResponseWriter, r *http.Request) {
	resp, err := s.uc.VirtualKeyStats(r.Context())
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, resp)
}

func (s *GatewayService) UpdateVirtualKey(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateVirtualKeyReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.uc.UpdateVirtualKey(r.Context(), req); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, nil)
}

func (s *GatewayService) UpdateVirtualKeyStatus(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateVirtualKeyStatusReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.uc.UpdateVirtualKeyStatus(r.Context(), req); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, nil)
}

func (s *GatewayService) RevokeVirtualKey(w http.ResponseWriter, r *http.Request) {
	id := uintQuery(r, "id")
	if err := s.uc.RevokeVirtualKey(r.Context(), id); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, nil)
}

// =============================================================================
// Quota handlers
// =============================================================================

func (s *GatewayService) GetQuotaConfig(w http.ResponseWriter, r *http.Request) {
	keyID := uintQuery(r, "keyId")
	resp, err := s.uc.GetQuotaConfig(r.Context(), keyID)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, resp)
}

func (s *GatewayService) UpdateQuotaConfig(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateQuotaConfigReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.uc.UpdateQuotaConfig(r.Context(), req); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, nil)
}

func (s *GatewayService) GetKeyQuotaUsage(w http.ResponseWriter, r *http.Request) {
	keyID := uintQuery(r, "keyId")
	resp, err := s.uc.GetKeyQuotaUsage(r.Context(), keyID)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, resp)
}

// =============================================================================
// Audit log handlers
// =============================================================================

func (s *GatewayService) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := dto.ListAuditLogsReq{
		PageInfo: dto.PageInfo{Page: intQuery(r, "page", 1), PageSize: intQuery(r, "pageSize", 10)},
		AuditLogFilter: dto.AuditLogFilter{
			VirtualKeyID: uintQuery(r, "virtualKeyId"),
			ProviderID:   uintQuery(r, "providerId"),
			Model:        q.Get("model"),
			Protocol:     q.Get("protocol"),
			PIIAction:    q.Get("piiAction"),
			Status:       q.Get("status"),
			ClientAgent:  q.Get("clientAgent"),
			PIIBlocked:   boolQueryPtr(r, "piiBlocked"),
			StartTime:    q.Get("startTime"),
			EndTime:      q.Get("endTime"),
		},
		SessionID: q.Get("sessionId"),
	}
	list, total, err := s.uc.ListAuditLogs(r.Context(), req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]interface{}{"list": list, "total": total})
}

func (s *GatewayService) ListAuditSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := dto.ListAuditSessionsReq{
		PageInfo: dto.PageInfo{Page: intQuery(r, "page", 1), PageSize: intQuery(r, "pageSize", 10)},
		AuditLogFilter: dto.AuditLogFilter{
			VirtualKeyID: uintQuery(r, "virtualKeyId"),
			ProviderID:   uintQuery(r, "providerId"),
			Model:        q.Get("model"),
			Protocol:     q.Get("protocol"),
			PIIAction:    q.Get("piiAction"),
			Status:       q.Get("status"),
			ClientAgent:  q.Get("clientAgent"),
			PIIBlocked:   boolQueryPtr(r, "piiBlocked"),
			StartTime:    q.Get("startTime"),
			EndTime:      q.Get("endTime"),
		},
	}
	list, total, err := s.uc.ListAuditSessions(r.Context(), req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]interface{}{"list": list, "total": total})
}

func (s *GatewayService) SecurityOverview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := dto.SecurityOverviewReq{
		AuditLogFilter: dto.AuditLogFilter{
			VirtualKeyID: uintQuery(r, "virtualKeyId"),
			ProviderID:   uintQuery(r, "providerId"),
			Model:        q.Get("model"),
			Protocol:     q.Get("protocol"),
			PIIAction:    q.Get("piiAction"),
			Status:       q.Get("status"),
			StartTime:    q.Get("startTime"),
			EndTime:      q.Get("endTime"),
		},
		TopN: intQuery(r, "topN", 5),
	}
	resp, err := s.uc.SecurityOverview(r.Context(), req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, resp)
}

// =============================================================================
// Models list endpoint (for OpenAI-compatible /models)
// =============================================================================

// ListModels handles GET /ai/v1/models — returns model list for the authenticated key.
func (s *GatewayService) ListModels(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromRequest(r)
	if key == nil {
		failWith(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	names, err := s.uc.ListGatewayModels(r.Context(), key)
	if err != nil {
		failWithErr(w, err)
		return
	}
	items := make([]map[string]interface{}, 0, len(names))
	for _, n := range names {
		items = append(items, map[string]interface{}{"id": n, "object": "model"})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"object": "list", "data": items})
}

// ProxyRequest handles the OpenAI-compatible proxy route.
func (s *GatewayService) ProxyRequest(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromRequest(r)
	if key == nil {
		failWith(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failWith(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	s.uc.ProxyRequest(r.Context(), key, body, w, r)
}
