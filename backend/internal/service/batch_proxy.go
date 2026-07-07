package service

import (
	"io"
	"net/http"

	"github.com/opscenter/ai-gateway/internal/biz"
)

// Batch + Files API proxy handlers (docs/design/09-extensibility.md). Thin
// HTTP decode → biz call, same shape as ProxyRequest/AnthropicMessagesProxy.

func (s *GatewayService) FilesUpload(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromRequest(r)
	if key == nil {
		failWith(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.uc.ProxyFilesUpload(r.Context(), key, w, r)
}

func (s *GatewayService) FilesList(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromRequest(r)
	if key == nil {
		failWith(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.uc.ProxyFilesList(r.Context(), key, w, r)
}

func (s *GatewayService) FilesGet(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromRequest(r)
	if key == nil {
		failWith(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.uc.ProxyFilesGet(r.Context(), key, r.PathValue("id"), w, r)
}

func (s *GatewayService) FilesContent(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromRequest(r)
	if key == nil {
		failWith(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.uc.ProxyFilesContent(r.Context(), key, r.PathValue("id"), w, r)
}

func (s *GatewayService) FilesDelete(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromRequest(r)
	if key == nil {
		failWith(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.uc.ProxyFilesDelete(r.Context(), key, r.PathValue("id"), w, r)
}

func (s *GatewayService) BatchesCreate(w http.ResponseWriter, r *http.Request) {
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
	s.uc.ProxyBatchesCreate(r.Context(), key, body, w, r)
}

func (s *GatewayService) BatchesList(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromRequest(r)
	if key == nil {
		failWith(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.uc.ProxyBatchesList(r.Context(), key, w, r)
}

func (s *GatewayService) BatchesGet(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromRequest(r)
	if key == nil {
		failWith(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.uc.ProxyBatchesGet(r.Context(), key, r.PathValue("id"), w, r)
}

func (s *GatewayService) BatchesCancel(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromRequest(r)
	if key == nil {
		failWith(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.uc.ProxyBatchesCancel(r.Context(), key, r.PathValue("id"), w, r)
}
