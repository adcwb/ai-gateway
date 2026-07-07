package service

import (
	"net/http"
	"strconv"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
)

// Settings + credits-rate management handlers (docs/design/08-web-console.md
// module 8, admin-authenticated).

func (s *GatewayService) GetSettings(w http.ResponseWriter, r *http.Request) {
	okWith(w, s.uc.GetSettings(r.Context()))
}

func (s *GatewayService) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateSettingsReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	resp, err := s.uc.UpdateSettings(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, resp)
}

func (s *GatewayService) TestAlertWebhook(w http.ResponseWriter, r *http.Request) {
	if err := s.uc.TestAlertWebhook(r.Context()); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"delivered": true})
}

func (s *GatewayService) CreateCreditsRate(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateCreditsRateReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	rate, err := s.uc.CreateCreditsRate(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, rate)
}

func (s *GatewayService) ListCreditsRates(w http.ResponseWriter, r *http.Request) {
	list, err := s.uc.ListCreditsRates(r.Context())
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, list)
}

func (s *GatewayService) UpdateCreditsRate(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateCreditsRateReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	rate, err := s.uc.UpdateCreditsRate(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, rate)
}

func (s *GatewayService) DeleteCreditsRate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		failWith(w, http.StatusBadRequest, "missing or invalid id")
		return
	}
	if err := s.uc.DeleteCreditsRate(r.Context(), uint(id)); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"deleted": id})
}
