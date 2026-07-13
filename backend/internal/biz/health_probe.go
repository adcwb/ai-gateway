package biz

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/adcwb/ai-gateway/internal/data/model"
)

// Active health probing (docs/design/01-routing-and-lb.md, "Optional active
// probing... P1 add-on for idle-period recovery detection, off by default").
//
// Passive breaker recovery only happens when TryPass is called for a given
// provider — i.e. when live traffic is actually routed to it. A provider that
// is open-circuit and ranked behind enough healthy candidates (Candidates()
// pushes open breakers to the end of the list, and the attempt loop stops at
// maxUpstreamAttempts) may never receive another attempt once it fails, so it
// can never re-enter half-open on its own even after the outage clears. The
// active prober closes that gap by periodically calling the exact same
// TryPass/ReportResult pair a real attempt would, using a lightweight
// synthetic request instead of proxied traffic.
const (
	activeProbeSweepInterval  = 10 * time.Second
	activeProbeDefaultIntervalSec = 30
	activeProbeTimeout        = 5 * time.Second
	activeProbeMaxBodyBytes   = 4 << 10
)

type breakerConfig struct {
	ActiveProbeEnabled     bool `json:"activeProbeEnabled"`
	ActiveProbeIntervalSec int  `json:"activeProbeIntervalSec"`
}

func parseBreakerConfig(p *model.AIProvider) breakerConfig {
	cfg := breakerConfig{}
	if len(p.BreakerConfig) > 0 {
		_ = json.Unmarshal(p.BreakerConfig, &cfg)
	}
	if cfg.ActiveProbeIntervalSec <= 0 {
		cfg.ActiveProbeIntervalSec = activeProbeDefaultIntervalSec
	}
	return cfg
}

var healthProbeClient = &http.Client{Timeout: activeProbeTimeout}

// StartActiveHealthProbes launches the sweep loop. Called once from
// StartBackgroundWorkers; a no-op until at least one provider opts in via
// breaker_config.activeProbeEnabled.
func (uc *GatewayUseCase) StartActiveHealthProbes(ctx context.Context) {
	if uc.router == nil || uc.db == nil {
		return
	}
	ticker := time.NewTicker(activeProbeSweepInterval)
	defer ticker.Stop()
	lastProbe := map[uint]time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			uc.sweepActiveProbes(ctx, lastProbe)
		}
	}
}

// sweepActiveProbes scans enabled providers and fires a probe for any
// non-closed provider that opted in and is due (per its configured interval).
// Closed providers are skipped — they already get plenty of signal from live
// traffic; probing them would just add upstream load for no design benefit.
func (uc *GatewayUseCase) sweepActiveProbes(ctx context.Context, lastProbe map[uint]time.Time) {
	var providers []model.AIProvider
	if err := uc.db.WithContext(ctx).Where("is_enabled = ?", true).Find(&providers).Error; err != nil {
		uc.logger.Warnf("健康探测：查询提供方失败 err=%v", err)
		return
	}
	now := time.Now()
	for _, p := range providers {
		cfg := parseBreakerConfig(&p)
		if !cfg.ActiveProbeEnabled {
			continue
		}
		if uc.router.StateOf(ctx, p.ID) == model.BreakerStateClosed {
			continue
		}
		if last, ok := lastProbe[p.ID]; ok && now.Sub(last) < time.Duration(cfg.ActiveProbeIntervalSec)*time.Second {
			continue
		}
		lastProbe[p.ID] = now
		go uc.probeProvider(ctx, p.ID)
	}
}

// probeProvider takes a probe slot exactly like a real attempt would
// (TryPass), issues a lightweight dialect-appropriate request, and feeds the
// outcome back through ReportResult — the shared Lua state machine cannot
// tell this apart from a real attempt, so no breaker changes were needed.
func (uc *GatewayUseCase) probeProvider(parentCtx context.Context, providerID uint) {
	ctx, cancel := context.WithTimeout(parentCtx, activeProbeTimeout)
	defer cancel()

	if !uc.router.TryPass(ctx, providerID) {
		return // still cooling down, or no half-open probe slot free
	}

	entry, err := uc.loadProviderDirect(ctx, providerID)
	if err != nil {
		// Provider config itself is broken (e.g. decryption failure) — this is
		// a real signal, not a transient network blip.
		uc.router.ReportResult(ctx, providerID, AttemptRetryableError)
		return
	}

	req, err := buildHealthProbeRequest(ctx, entry)
	if err != nil {
		// Local construction error (bad BaseURL) — not the upstream's fault;
		// don't let it move the breaker.
		uc.logger.Warnf("健康探测：构建探测请求失败 providerID=%d err=%v", providerID, err)
		return
	}

	resp, err := healthProbeClient.Do(req)
	if err != nil {
		uc.logger.Warnf("健康探测失败 providerID=%d provider=%s err=%v", providerID, entry.provider.Name, err)
		uc.router.ReportResult(ctx, providerID, AttemptRetryableError)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, activeProbeMaxBodyBytes))

	// Any non-5xx response means the network/TLS/HTTP path is alive again —
	// that is the recovery signal this probe exists to detect. 4xx (bad key,
	// wrong path) still proves reachability; it is not what the breaker guards
	// against.
	if resp.StatusCode >= 500 {
		uc.router.ReportResult(ctx, providerID, AttemptRetryableError)
		return
	}
	uc.router.ReportResult(ctx, providerID, AttemptSuccess)
}

// buildHealthProbeRequest builds a cheap read-only request per provider
// dialect: a "list models" call where one exists cheaply, a bare GET on the
// host root otherwise.
func buildHealthProbeRequest(ctx context.Context, entry *providerEntry) (*http.Request, error) {
	p := entry.provider
	switch p.ProviderType {
	case model.ProviderTypeAnthropic:
		cfg := parseAdapterConfig(&p)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL+"/v1/models", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-api-key", entry.apiKey)
		req.Header.Set("anthropic-version", cfg.AnthropicVersion)
		return req, nil

	case model.ProviderTypeGemini:
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL+"/v1beta/models", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-goog-api-key", entry.apiKey)
		return req, nil

	case model.ProviderTypeAzureOpenAI:
		// Azure's BaseURL is deployment-scoped (/openai/deployments/{deployment})
		// with no generic cheap list endpoint; fall back to a bare host-root GET
		// (reachability only — any HTTP response, even 401/404, is recovery signal).
		return healthProbeHostRootRequest(ctx, p.BaseURL)

	default: // openai_compatible
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL+"/models", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+entry.apiKey)
		return req, nil
	}
}

func healthProbeHostRootRequest(ctx context.Context, baseURL string) (*http.Request, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = "/"
	u.RawQuery = ""
	return http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
}
