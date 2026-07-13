package service

import (
	"net/http"
	"strconv"

	"github.com/adcwb/ai-gateway/internal/biz/dto"
)

// PII/guardrail policy registry (admin API,
// docs/design/06-security-and-guardrails.md), mirroring the MCP server/
// extension management handlers.

func (s *GatewayService) CreatePIIPolicy(w http.ResponseWriter, r *http.Request) {
	var req dto.CreatePIIPolicyReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p, err := s.uc.CreatePIIPolicy(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, p)
}

func (s *GatewayService) ListPIIPolicies(w http.ResponseWriter, r *http.Request) {
	list, err := s.uc.ListPIIPolicies(r.Context())
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, list)
}

func (s *GatewayService) UpdatePIIPolicy(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdatePIIPolicyReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p, err := s.uc.UpdatePIIPolicy(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, p)
}

func (s *GatewayService) DeletePIIPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		failWith(w, http.StatusBadRequest, "missing or invalid id")
		return
	}
	if err := s.uc.DeletePIIPolicy(r.Context(), uint(id)); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"deleted": id})
}
