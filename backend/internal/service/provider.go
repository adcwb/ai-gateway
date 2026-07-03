package service

import (
	"net/http"
	"strconv"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
)

// Provider management handlers (admin-authenticated).

func (s *GatewayService) CreateProvider(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateProviderReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p, err := s.uc.CreateProvider(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, p)
}

func (s *GatewayService) ListProviders(w http.ResponseWriter, r *http.Request) {
	list, err := s.uc.ListProviders(r.Context())
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, list)
}

func (s *GatewayService) UpdateProvider(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateProviderReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p, err := s.uc.UpdateProvider(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, p)
}

func (s *GatewayService) DeleteProvider(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		failWith(w, http.StatusBadRequest, "missing or invalid id")
		return
	}
	if err := s.uc.DeleteProvider(r.Context(), uint(id)); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"deleted": id})
}

func (s *GatewayService) ProviderHealth(w http.ResponseWriter, r *http.Request) {
	items, err := s.uc.ProviderHealth(r.Context())
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, items)
}
