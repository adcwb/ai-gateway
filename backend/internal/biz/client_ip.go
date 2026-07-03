package biz

import (
	"net"
	"net/http"
	"strings"
)

// ClientIPFromRequest extracts the real client IP from a request.
func ClientIPFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	candidate := r.RemoteAddr
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0]); first != "" {
			candidate = first
		}
	}
	candidate = strings.TrimSpace(candidate)
	if host, _, err := net.SplitHostPort(candidate); err == nil {
		return host
	}
	return candidate
}
