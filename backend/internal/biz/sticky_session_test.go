package biz

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adcwb/ai-gateway/internal/data/model"
)

func TestExtractSessionHash_Priority(t *testing.T) {
	key := &model.AIVirtualKey{}
	key.ID = 1

	t.Run("header takes priority over body signals", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("X-Session-ID", "sess-1")
		body := []byte(`{"metadata":{"user_id":"u-1"},"prompt_cache_key":"pck-1"}`)
		gotHeader := extractSessionHash(key, r, body)

		r2 := httptest.NewRequest(http.MethodPost, "/", nil)
		gotNoHeader := extractSessionHash(key, r2, body)
		if gotHeader == "" || gotHeader == gotNoHeader {
			t.Fatalf("expected header-derived hash to differ from body-derived hash, got %q vs %q", gotHeader, gotNoHeader)
		}
	})

	t.Run("metadata.user_id takes priority over prompt_cache_key", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		withUID := []byte(`{"metadata":{"user_id":"u-1"},"prompt_cache_key":"pck-1"}`)
		withoutUID := []byte(`{"prompt_cache_key":"pck-1"}`)

		gotWithUID := extractSessionHash(key, r, withUID)
		gotWithoutUID := extractSessionHash(key, r, withoutUID)
		if gotWithUID == "" || gotWithoutUID == "" {
			t.Fatalf("expected non-empty hashes, got %q / %q", gotWithUID, gotWithoutUID)
		}
		if gotWithUID == gotWithoutUID {
			t.Fatalf("expected metadata.user_id to change the session hash vs prompt_cache_key-only body")
		}

		// Same user_id, different prompt_cache_key: hash should stay stable
		// (metadata.user_id wins, prompt_cache_key is ignored).
		sameUIDDifferentPCK := []byte(`{"metadata":{"user_id":"u-1"},"prompt_cache_key":"pck-2"}`)
		gotSameUID := extractSessionHash(key, r, sameUIDDifferentPCK)
		if gotSameUID != gotWithUID {
			t.Fatalf("expected session hash to stay pinned to metadata.user_id, got %q vs %q", gotSameUID, gotWithUID)
		}
	})

	t.Run("prompt_cache_key takes priority over content prefix", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		body := []byte(`{"prompt_cache_key":"pck-1","messages":[{"role":"user","content":"hello"}]}`)
		bodyDifferentContent := []byte(`{"prompt_cache_key":"pck-1","messages":[{"role":"user","content":"goodbye"}]}`)

		got := extractSessionHash(key, r, body)
		gotDifferentContent := extractSessionHash(key, r, bodyDifferentContent)
		if got == "" || got != gotDifferentContent {
			t.Fatalf("expected prompt_cache_key to pin the hash regardless of content, got %q vs %q", got, gotDifferentContent)
		}
	})

	t.Run("empty body and header yields no hash", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if got := extractSessionHash(key, r, nil); got != "" {
			t.Fatalf("expected empty hash, got %q", got)
		}
	})
}

func TestExtractMetadataUserID(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"present", `{"metadata":{"user_id":"u-1"}}`, "u-1"},
		{"trims whitespace", `{"metadata":{"user_id":"  u-1  "}}`, "u-1"},
		{"missing metadata", `{}`, ""},
		{"missing user_id", `{"metadata":{}}`, ""},
		{"invalid json", `not json`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractMetadataUserID([]byte(tc.body)); got != tc.want {
				t.Errorf("extractMetadataUserID(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}
