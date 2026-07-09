package service

import (
	"net/http"

	"github.com/opscenter/ai-gateway/internal/biz"
)

// Video generation, phase 2 (docs/superpowers/specs/2026-07-09-video-
// generation-phase2-design.md): thin HTTP wrappers mirroring media.go's
// shape — extract the authenticated virtual key from context and hand off
// to biz for resolution/governance/forwarding.

func (s *GatewayService) VideosCreate(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromCtx(r.Context())
	if key == nil {
		failWith(w, http.StatusUnauthorized, "missing virtual key")
		return
	}
	s.uc.HandleVideosCreate(r.Context(), key, w, r)
}

func (s *GatewayService) VideosList(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromCtx(r.Context())
	if key == nil {
		failWith(w, http.StatusUnauthorized, "missing virtual key")
		return
	}
	s.uc.HandleVideosList(r.Context(), key, w, r)
}

func (s *GatewayService) VideosGet(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromCtx(r.Context())
	if key == nil {
		failWith(w, http.StatusUnauthorized, "missing virtual key")
		return
	}
	s.uc.HandleVideosGet(r.Context(), key, r.PathValue("id"), w, r)
}

func (s *GatewayService) VideosContent(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromCtx(r.Context())
	if key == nil {
		failWith(w, http.StatusUnauthorized, "missing virtual key")
		return
	}
	s.uc.HandleVideosContent(r.Context(), key, r.PathValue("id"), w, r)
}

func (s *GatewayService) VideosDelete(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromCtx(r.Context())
	if key == nil {
		failWith(w, http.StatusUnauthorized, "missing virtual key")
		return
	}
	s.uc.HandleVideosDelete(r.Context(), key, r.PathValue("id"), w, r)
}
