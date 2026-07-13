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

// IsRequestSecure reports whether the request reached this handler over TLS
// — directly (r.TLS != nil), or as reported by a trusted-position reverse
// proxy terminating TLS in front of the Go process (X-Forwarded-Proto:
// https, or X-Forwarded-Ssl: on, both standard nginx/Caddy/ALB conventions).
// Used to gate the Secure flag on the console session cookie: hardcoding
// Secure=true would break every deployment that isn't already behind TLS
// (local dev, a bare compose stack without a TLS-terminating proxy), while
// never setting it leaks the session cookie over any plaintext hop in a
// deployment that IS behind HTTPS.
func IsRequestSecure(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return strings.EqualFold(strings.TrimSpace(strings.SplitN(proto, ",", 2)[0]), "https")
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Ssl"), "on")
}
