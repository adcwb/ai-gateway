package service

import (
	"net/http"
	"strconv"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
)

// Extension registry (admin API, docs/design/09-extensibility.md "Delivery
// mechanisms"), mirroring the MCP server management handlers.

func (s *GatewayService) CreateExtension(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateExtensionReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	e, err := s.uc.CreateExtension(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, e)
}

func (s *GatewayService) ListExtensions(w http.ResponseWriter, r *http.Request) {
	list, err := s.uc.ListExtensions(r.Context())
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, list)
}

func (s *GatewayService) UpdateExtension(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateExtensionReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	e, err := s.uc.UpdateExtension(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, e)
}

func (s *GatewayService) DeleteExtension(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		failWith(w, http.StatusBadRequest, "missing or invalid id")
		return
	}
	if err := s.uc.DeleteExtension(r.Context(), uint(id)); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"deleted": id})
}
