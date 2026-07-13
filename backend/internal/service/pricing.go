package service

import (
	"net/http"
	"strconv"

	"github.com/adcwb/ai-gateway/internal/biz"
	"github.com/adcwb/ai-gateway/internal/biz/dto"
)

// Model catalog + price table management handlers (docs/design/08-web-console.md
// module 4, admin-authenticated).

func (s *GatewayService) CreateModelItem(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateModelItemReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	m, err := s.uc.CreateModelItem(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, m)
}

func (s *GatewayService) ListModelItems(w http.ResponseWriter, r *http.Request) {
	var providerID uint
	if v := r.URL.Query().Get("providerId"); v != "" {
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			failWith(w, http.StatusBadRequest, "invalid providerId")
			return
		}
		providerID = uint(id)
	}
	list, err := s.uc.ListModelItems(r.Context(), providerID)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, list)
}

func (s *GatewayService) UpdateModelItem(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateModelItemReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	m, err := s.uc.UpdateModelItem(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, m)
}

func (s *GatewayService) DeleteModelItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		failWith(w, http.StatusBadRequest, "missing or invalid id")
		return
	}
	if err := s.uc.DeleteModelItem(r.Context(), uint(id)); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"deleted": id})
}

func (s *GatewayService) CreatePriceTable(w http.ResponseWriter, r *http.Request) {
	var req dto.CreatePriceTableReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	t, err := s.uc.CreatePriceTable(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, t)
}

func (s *GatewayService) ListPriceTables(w http.ResponseWriter, r *http.Request) {
	list, err := s.uc.ListPriceTables(r.Context())
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, list)
}

func (s *GatewayService) UpdatePriceTable(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdatePriceTableReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	t, err := s.uc.UpdatePriceTable(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, t)
}

func (s *GatewayService) DeletePriceTable(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		failWith(w, http.StatusBadRequest, "missing or invalid id")
		return
	}
	if err := s.uc.DeletePriceTable(r.Context(), uint(id)); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"deleted": id})
}

func (s *GatewayService) CreatePriceTableItem(w http.ResponseWriter, r *http.Request) {
	var req dto.CreatePriceTableItemReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := s.uc.CreatePriceTableItem(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, item)
}

func (s *GatewayService) UpdatePriceTableItem(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdatePriceTableItemReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := s.uc.UpdatePriceTableItem(r.Context(), &req)
	if err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, item)
}

func (s *GatewayService) DeletePriceTableItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		failWith(w, http.StatusBadRequest, "missing or invalid id")
		return
	}
	if err := s.uc.DeletePriceTableItem(r.Context(), uint(id)); err != nil {
		failWithErr(w, err)
		return
	}
	okWith(w, map[string]any{"deleted": id})
}

func (s *GatewayService) TestPricePattern(w http.ResponseWriter, r *http.Request) {
	var req dto.PatternTestReq
	if err := decodeJSON(r, &req); err != nil {
		failWith(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	okWith(w, biz.TestPattern(&req))
}
