package service

import (
	"net/http"

	"github.com/adcwb/ai-gateway/internal/biz"
)

// Multimodal media adapters, phase 1 (docs/superpowers/specs/2026-07-09-
// multimodal-media-adapters-design.md): thin HTTP wrappers mirroring
// MCPProxy's shape — extract the authenticated virtual key from context
// (set by middleware.VirtualKeyAuth, the same as model/MCP traffic) and hand
// off to biz for governance/resolution/forwarding.

func (s *GatewayService) ImagesGenerations(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromCtx(r.Context())
	if key == nil {
		failWith(w, http.StatusUnauthorized, "missing virtual key")
		return
	}
	s.uc.HandleImagesGenerations(r.Context(), key, w, r)
}

func (s *GatewayService) AudioSpeech(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromCtx(r.Context())
	if key == nil {
		failWith(w, http.StatusUnauthorized, "missing virtual key")
		return
	}
	s.uc.HandleAudioSpeech(r.Context(), key, w, r)
}

func (s *GatewayService) AudioTranscriptions(w http.ResponseWriter, r *http.Request) {
	key := biz.VirtualKeyFromCtx(r.Context())
	if key == nil {
		failWith(w, http.StatusUnauthorized, "missing virtual key")
		return
	}
	s.uc.HandleAudioTranscriptions(r.Context(), key, w, r)
}
