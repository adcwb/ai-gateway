package biz

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/opscenter/ai-gateway/internal/conf"
)

// proxyTimeout returns the configured proxy timeout with a 300s fallback.
func proxyTimeout(aiConf *conf.AI) time.Duration {
	if aiConf != nil && aiConf.ProxyTimeoutSec > 0 {
		return time.Duration(aiConf.ProxyTimeoutSec) * time.Second
	}
	if aiConf != nil && aiConf.AgentTimeoutSec > 0 {
		return time.Duration(aiConf.AgentTimeoutSec) * time.Second
	}
	return 300 * time.Second
}

var (
	sharedProxyClientOnce sync.Once
	sharedProxyClient     *http.Client
)

// newProxyClient returns the process-wide shared http.Client for proxying
// upstream LLM requests. Every call site used the same 300s header timeout
// (verified: newProxyClientWithHeaderTimeout is never called with any other
// value), so a single lazily-built client is safe — and required: an
// http.Transport owns its own connection pool, so building a fresh
// *http.Transport per call (the previous behavior) silently defeated
// MaxIdleConns/MaxIdleConnsPerHost, forcing a new TCP+TLS handshake on every
// single upstream request regardless of upstream host.
// Critically: http.Client.Timeout is NOT set — it would cut streaming responses.
// Only Transport.ResponseHeaderTimeout (TTFB) is set.
func newProxyClient() *http.Client {
	sharedProxyClientOnce.Do(func() {
		sharedProxyClient = newProxyClientWithHeaderTimeout(300 * time.Second)
	})
	return sharedProxyClient
}

// newProxyClientWithHeaderTimeout allows customizing the TTFB (response header) timeout.
func newProxyClientWithHeaderTimeout(headerTimeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: headerTimeout,
			TLSHandshakeTimeout:   15 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}

// proxyRequestCtx returns a context for the upstream request:
//   - Streaming: returns original ctx (no deadline — streaming must not be cut off).
//   - Non-streaming: attaches a proxyTimeout deadline to prevent infinite hangs.
func proxyRequestCtx(ctx context.Context, isStream bool, aiConf *conf.AI) (context.Context, context.CancelFunc) {
	if isStream {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, proxyTimeout(aiConf))
}
