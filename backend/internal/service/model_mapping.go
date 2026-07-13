package service

import (
	"net/http"
	"strconv"

	"github.com/adcwb/ai-gateway/internal/biz/dto"
)

// Model mapping registry (admin API, docs/design/01-routing-and-lb.md),
// mirroring the MCP server/extension management handlers.

func (s *GatewayService) CreateModelMapping(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateModelMappingReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	m, err := s.uc.CreateModelMapping(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, m)
}

func (s *GatewayService) ListModelMappings(w http.ResponseWriter, r *http.Request) {
	keyID, err := strconv.ParseUint(r.URL.Query().Get("virtualKeyId"), 10, 64)
	if err != nil || keyID == 0 {
		failWith(w, http.StatusBadRequest, "missing or invalid virtualKeyId")
		return
	}
	list, err := s.uc.ListModelMappings(r.Context(), uint(keyID))
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, list)
}

func (s *GatewayService) UpdateModelMapping(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateModelMappingReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	m, err := s.uc.UpdateModelMapping(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, m)
}

func (s *GatewayService) DeleteModelMapping(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		failWith(w, http.StatusBadRequest, "missing or invalid id")
		return
	}
	if err := s.uc.DeleteModelMapping(r.Context(), uint(id)); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"deleted": id})
}
