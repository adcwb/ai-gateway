package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandler_ValidSignaturePasses(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"point":"pre_request","tenantId":1,"ir":"e30="}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("X-AIGW-Signature", sig)
	w := httptest.NewRecorder()

	handler(secret)(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != `{"action":"pass"}` {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestHandler_InvalidSignatureRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-AIGW-Signature", "deadbeef")
	w := httptest.NewRecorder()

	handler("test-secret")(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandler_NoSecretConfiguredSkipsCheck(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()

	handler("")(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when no secret is configured, got %d", w.Code)
	}
}

func TestHandler_RejectsNonPOST(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler("")(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}
