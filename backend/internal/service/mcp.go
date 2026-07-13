package service

import (
	"net/http"
	"strconv"

	"github.com/adcwb/ai-gateway/internal/biz"
	"github.com/adcwb/ai-gateway/internal/biz/dto"
)

// MCP server registry (admin API, docs/design/09-extensibility.md "MCP
// gateway"), mirroring the provider management handlers.

func (s *GatewayService) CreateMCPServer(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateMCPServerReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	srv, err := s.uc.CreateMCPServer(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, srv)
}

func (s *GatewayService) ListMCPServers(w http.ResponseWriter, r *http.Request) {
	list, err := s.uc.ListMCPServers(r.Context())
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, list)
}

func (s *GatewayService) UpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateMCPServerReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	srv, err := s.uc.UpdateMCPServer(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, srv)
}

func (s *GatewayService) DeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		failWith(w, http.StatusBadRequest, "missing or invalid id")
		return
	}
	if err := s.uc.DeleteMCPServer(r.Context(), uint(id)); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"deleted": id})
}

// MCPProxy is the Streamable HTTP endpoint agents call
// (/ai/mcp/{serverName}), authenticated by the same sk-vk-* virtual-key
// middleware as model traffic.
func (s *GatewayService) MCPProxy(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromCtx(r.Context())
	if key == nil {
		failWith(w, http.StatusUnauthorized, "missing virtual key")
		return
	}
	serverName := r.PathValue("serverName")
	s.uc.HandleMCPRequest(r.Context(), key, serverName, w, r)
}
