package service

import (
	"math"
	"net/http"

	"github.com/adcwb/ai-gateway/internal/biz/dto"
	"github.com/adcwb/ai-gateway/internal/data/model"
	"github.com/adcwb/ai-gateway/internal/middleware"
)

// Tenancy + billing + stats handlers (admin-authenticated).

func (s *GatewayService) CreateTenant(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateTenantReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	t, err := s.uc.CreateTenant(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, t)
}

func (s *GatewayService) ListTenants(w http.ResponseWriter, r *http.Request) {
	items, err := s.uc.ListTenants(r.Context())
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, items)
}

func (s *GatewayService) CreateProject(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateProjectReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p, err := s.uc.CreateProject(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, p)
}

func (s *GatewayService) ListProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.uc.ListProjects(r.Context(), uintQuery(r, "tenantId"))
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, list)
}

// ---------------------------------------------------------------------------
// Billing
// ---------------------------------------------------------------------------

func (s *GatewayService) Recharge(w http.ResponseWriter, r *http.Request) {
	var req dto.RechargeReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// RBAC (docs/design/04): recharge is Owner/Admin of that tenant.
	if !middleware.RequireRole(w, r, req.TenantID, "admin") {
		return
	}
	acct, err := s.bm.Recharge(r.Context(), req.TenantID, req.Credits, req.IdempotencyKey, "manual", req.Remark)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, acct)
}

func (s *GatewayService) UpdateBillingAccount(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateBillingAccountReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !middleware.RequireRole(w, r, req.TenantID, "admin") {
		return
	}
	updates := map[string]interface{}{}
	if req.IsEnabled != nil {
		updates["is_enabled"] = *req.IsEnabled
	}
	if req.Mode != nil {
		updates["mode"] = *req.Mode
	}
	if req.Currency != nil {
		updates["currency"] = *req.Currency
	}
	if req.CreditLimit != nil {
		updates["credit_limit_micro"] = int64(math.Round(*req.CreditLimit * model.MicroCreditScale))
	}
	if req.LowWatermark != nil {
		updates["low_watermark_micro"] = int64(math.Round(*req.LowWatermark * model.MicroCreditScale))
	}
	if req.GraceHours != nil {
		updates["grace_hours"] = *req.GraceHours
	}
	if req.ClearPriceTableID {
		updates["price_table_id"] = nil
	} else if req.PriceTableID != nil {
		updates["price_table_id"] = *req.PriceTableID
	}
	acct, err := s.bm.UpdateAccount(r.Context(), req.TenantID, updates)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, acct)
}

func (s *GatewayService) ListLedger(w http.ResponseWriter, r *http.Request) {
	rows, total, err := s.bm.ListLedger(r.Context(), uintQuery(r, "tenantId"), intQuery(r, "page", 1), intQuery(r, "pageSize", 50))
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]interface{}{"list": rows, "total": total})
}

// ---------------------------------------------------------------------------
// Usage stats
// ---------------------------------------------------------------------------

func (s *GatewayService) UsageOverview(w http.ResponseWriter, r *http.Request) {
	out, err := s.bm.UsageOverview(r.Context(), uintQuery(r, "tenantId"), intQuery(r, "days", 7))
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, out)
}

func (s *GatewayService) UsageTimeseries(w http.ResponseWriter, r *http.Request) {
	out, err := s.bm.UsageTimeseries(r.Context(), uintQuery(r, "tenantId"), intQuery(r, "days", 7))
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, out)
}
