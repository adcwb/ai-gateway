package biz

import (
	"encoding/json"
	"net"
	"strings"

	"github.com/adcwb/ai-gateway/internal/data/model"
)

type ipMatcher struct {
	ipNet *net.IPNet
	ip    net.IP
}

func (m ipMatcher) contains(ip net.IP) bool {
	if m.ipNet != nil {
		return m.ipNet.Contains(ip)
	}
	return m.ip != nil && m.ip.Equal(ip)
}

func parseIPWhitelist(raw []byte) []ipMatcher {
	if len(raw) == 0 {
		return nil
	}
	var entries []string
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	matchers := make([]ipMatcher, 0, len(entries))
	for _, e := range entries {
		if m, ok := parseIPEntry(e); ok {
			matchers = append(matchers, m)
		}
	}
	return matchers
}

func parseIPEntry(entry string) (ipMatcher, bool) {
	s := strings.TrimSpace(entry)
	if s == "" {
		return ipMatcher{}, false
	}
	if strings.Contains(s, "/") {
		if _, ipNet, err := net.ParseCIDR(s); err == nil {
			return ipMatcher{ipNet: ipNet}, true
		}
		return ipMatcher{}, false
	}
	if ip := net.ParseIP(s); ip != nil {
		return ipMatcher{ip: ip}, true
	}
	return ipMatcher{}, false
}

// IsClientIPAllowed checks whether clientIP is permitted by the key's IP whitelist.
// If IPWhitelistEnabled is false, all IPs are allowed.
func IsClientIPAllowed(key *model.AIVirtualKey, clientIP string) bool {
	if key == nil || !key.IPWhitelistEnabled {
		return true
	}
	return isClientIPInWhitelist(key.IPWhitelist, clientIP)
}

// isClientIPInWhitelist checks if clientIP matches the whitelist JSON ([]string).
func isClientIPInWhitelist(raw []byte, clientIP string) bool {
	ip := net.ParseIP(strings.TrimSpace(clientIP))
	if ip == nil {
		return false
	}
	matchers := parseIPWhitelist(raw)
	if len(matchers) == 0 {
		return false
	}
	for _, m := range matchers {
		if m.contains(ip) {
			return true
		}
	}
	return false
}

// NormalizeIPWhitelist validates and normalizes IP whitelist entries, returning canonical JSON.
func NormalizeIPWhitelist(raw []byte, requireNonEmpty bool) (json.RawMessage, error) {
	var entries []string
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, ErrInvalidIPWhitelist
		}
	}
	seen := make(map[string]struct{}, len(entries))
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		s := strings.TrimSpace(e)
		if s == "" {
			continue
		}
		if _, ok := parseIPEntry(s); !ok {
			return nil, ErrInvalidIPEntry.WithMetadata(map[string]string{"entry": s})
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if requireNonEmpty && len(out) == 0 {
		return nil, ErrIPWhitelistEmpty
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return b, nil
}
