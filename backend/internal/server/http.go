package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/biz"
	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/console"
	"github.com/opscenter/ai-gateway/internal/homepage"
	"github.com/opscenter/ai-gateway/internal/middleware"
	"github.com/opscenter/ai-gateway/internal/observability"
	"github.com/opscenter/ai-gateway/internal/service"
)

// NewHTTPServer builds and returns the net/http.Server with all routes registered.
func NewHTTPServer(
	c *conf.Server,
	sys *conf.System,
	gwSvc *service.GatewayService,
	authSvc *service.AuthService,
	authUC *biz.AuthUseCase,
	gwUc *biz.GatewayUseCase,
	quota *biz.QuotaManager,
	ready *observability.ReadyChecker,
	logger log.Logger,
) *http.Server {
	mux := http.NewServeMux()
	auth := middleware.NewVirtualKeyAuth(gwUc, quota)
	admin := middleware.NewAdminAuth(adminToken(sys), authUC, logger)

	// -------------------------------------------------------------------------
	// SSO/OIDC login flow (docs/design/04-multi-tenancy-and-auth.md) — public
	// by necessity: this is how a caller obtains a session in the first place.
	// -------------------------------------------------------------------------

	mux.HandleFunc("GET /ai/gateway/auth/config", authSvc.AuthConfig)
	mux.HandleFunc("GET /ai/gateway/auth/login", authSvc.Login)
	mux.HandleFunc("GET /ai/gateway/auth/callback", authSvc.Callback)
	mux.HandleFunc("POST /ai/gateway/auth/logout", authSvc.Logout)

	// -------------------------------------------------------------------------
	// Health endpoints (no auth, not audited) — docs/design/05-observability.md
	// -------------------------------------------------------------------------

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		results := ready.Check(r.Context())
		failed := map[string]string{}
		for name, err := range results {
			if err != nil {
				failed[name] = err.Error()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if ready.ShuttingDown || len(failed) > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]any{"ready": false, "failed": failed, "shuttingDown": ready.ShuttingDown})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ready": true})
	})

	// -------------------------------------------------------------------------
	// Management API — protected by the static admin token when configured
	// (docs/design/04-multi-tenancy-and-auth.md, P0 bootstrap principal)
	// -------------------------------------------------------------------------

	mgmt := http.NewServeMux()

	mgmt.HandleFunc("POST /ai/gateway/key", gwSvc.CreateVirtualKey)
	mgmt.HandleFunc("GET /ai/gateway/key/list", gwSvc.ListVirtualKeys)
	mgmt.HandleFunc("GET /ai/gateway/key/stats", gwSvc.VirtualKeyStats)
	mgmt.HandleFunc("PUT /ai/gateway/key", gwSvc.UpdateVirtualKey)
	mgmt.HandleFunc("PUT /ai/gateway/key/status", gwSvc.UpdateVirtualKeyStatus)
	mgmt.HandleFunc("DELETE /ai/gateway/key", gwSvc.RevokeVirtualKey)
	mgmt.HandleFunc("GET /ai/gateway/key/reveal", gwSvc.RevealVirtualKey)

	mgmt.HandleFunc("GET /ai/gateway/key/quota-config", gwSvc.GetQuotaConfig)
	mgmt.HandleFunc("PUT /ai/gateway/key/quota-config", gwSvc.UpdateQuotaConfig)
	mgmt.HandleFunc("GET /ai/gateway/key/quota-usage", gwSvc.GetKeyQuotaUsage)

	mgmt.HandleFunc("GET /ai/gateway/audit/list", gwSvc.ListAuditLogs)
	mgmt.HandleFunc("GET /ai/gateway/audit/sessions", gwSvc.ListAuditSessions)
	mgmt.HandleFunc("GET /ai/gateway/audit/security-overview", gwSvc.SecurityOverview)

	mgmt.HandleFunc("POST /ai/gateway/providers", gwSvc.CreateProvider)
	mgmt.HandleFunc("GET /ai/gateway/providers", gwSvc.ListProviders)
	mgmt.HandleFunc("PUT /ai/gateway/providers", gwSvc.UpdateProvider)
	mgmt.HandleFunc("DELETE /ai/gateway/providers", gwSvc.DeleteProvider)
	mgmt.HandleFunc("GET /ai/gateway/providers/health", gwSvc.ProviderHealth)
	mgmt.HandleFunc("POST /ai/gateway/providers/sync-models", gwSvc.SyncProviderModels)

	// P1: tenancy, billing, usage stats (docs/design/03,04)
	mgmt.HandleFunc("POST /ai/gateway/tenants", gwSvc.CreateTenant)
	mgmt.HandleFunc("GET /ai/gateway/tenants", gwSvc.ListTenants)
	mgmt.HandleFunc("POST /ai/gateway/projects", gwSvc.CreateProject)
	mgmt.HandleFunc("GET /ai/gateway/projects", gwSvc.ListProjects)
	// Recharge/account changes are Owner/Admin of that tenant (docs/design/04
	// RBAC table); tenantID comes from the request body, checked in-handler.
	mgmt.HandleFunc("POST /ai/gateway/billing/recharge", gwSvc.Recharge)
	mgmt.HandleFunc("PUT /ai/gateway/billing/account", gwSvc.UpdateBillingAccount)
	mgmt.HandleFunc("GET /ai/gateway/billing/ledger", gwSvc.ListLedger)
	mgmt.HandleFunc("GET /ai/gateway/stats/overview", gwSvc.UsageOverview)
	mgmt.HandleFunc("GET /ai/gateway/stats/timeseries", gwSvc.UsageTimeseries)

	// P2: model catalog + price tables (docs/design/03,08 module 4) — global
	// objects, mutation is platform-admin only (docs/design/04).
	mgmt.HandleFunc("POST /ai/gateway/model-items", middleware.RequirePlatformAdmin(gwSvc.CreateModelItem))
	mgmt.HandleFunc("GET /ai/gateway/model-items", gwSvc.ListModelItems)
	mgmt.HandleFunc("PUT /ai/gateway/model-items", middleware.RequirePlatformAdmin(gwSvc.UpdateModelItem))
	mgmt.HandleFunc("DELETE /ai/gateway/model-items", middleware.RequirePlatformAdmin(gwSvc.DeleteModelItem))
	mgmt.HandleFunc("POST /ai/gateway/price-tables", middleware.RequirePlatformAdmin(gwSvc.CreatePriceTable))
	mgmt.HandleFunc("GET /ai/gateway/price-tables", gwSvc.ListPriceTables)
	mgmt.HandleFunc("PUT /ai/gateway/price-tables", middleware.RequirePlatformAdmin(gwSvc.UpdatePriceTable))
	mgmt.HandleFunc("DELETE /ai/gateway/price-tables", middleware.RequirePlatformAdmin(gwSvc.DeletePriceTable))
	mgmt.HandleFunc("POST /ai/gateway/price-tables/items", middleware.RequirePlatformAdmin(gwSvc.CreatePriceTableItem))
	mgmt.HandleFunc("PUT /ai/gateway/price-tables/items", middleware.RequirePlatformAdmin(gwSvc.UpdatePriceTableItem))
	mgmt.HandleFunc("DELETE /ai/gateway/price-tables/items", middleware.RequirePlatformAdmin(gwSvc.DeletePriceTableItem))
	mgmt.HandleFunc("POST /ai/gateway/price-tables/test-pattern", gwSvc.TestPricePattern)

	// P2: settings + credits rates (docs/design/08-web-console.md module 8) —
	// global objects, mutation is platform-admin only.
	mgmt.HandleFunc("GET /ai/gateway/settings", gwSvc.GetSettings)
	mgmt.HandleFunc("PUT /ai/gateway/settings", middleware.RequirePlatformAdmin(gwSvc.UpdateSettings))
	mgmt.HandleFunc("POST /ai/gateway/settings/test-webhook", middleware.RequirePlatformAdmin(gwSvc.TestAlertWebhook))
	mgmt.HandleFunc("POST /ai/gateway/credits-rates", middleware.RequirePlatformAdmin(gwSvc.CreateCreditsRate))
	mgmt.HandleFunc("GET /ai/gateway/credits-rates", gwSvc.ListCreditsRates)
	mgmt.HandleFunc("PUT /ai/gateway/credits-rates", middleware.RequirePlatformAdmin(gwSvc.UpdateCreditsRate))
	mgmt.HandleFunc("DELETE /ai/gateway/credits-rates", middleware.RequirePlatformAdmin(gwSvc.DeleteCreditsRate))

	// P3: MCP server registry (docs/design/09-extensibility.md "MCP gateway")
	// — global objects, mutation is platform-admin only (same posture as
	// providers/price-tables).
	mgmt.HandleFunc("POST /ai/gateway/mcp-servers", middleware.RequirePlatformAdmin(gwSvc.CreateMCPServer))
	mgmt.HandleFunc("GET /ai/gateway/mcp-servers", gwSvc.ListMCPServers)
	mgmt.HandleFunc("PUT /ai/gateway/mcp-servers", middleware.RequirePlatformAdmin(gwSvc.UpdateMCPServer))
	mgmt.HandleFunc("DELETE /ai/gateway/mcp-servers", middleware.RequirePlatformAdmin(gwSvc.DeleteMCPServer))

	// P3: extension registry — webhook/WASM pre_request/post_response hooks
	// (docs/design/09-extensibility.md "Delivery mechanisms"). Same posture
	// as MCP servers: global objects, mutation is platform-admin only.
	mgmt.HandleFunc("POST /ai/gateway/extensions", middleware.RequirePlatformAdmin(gwSvc.CreateExtension))
	mgmt.HandleFunc("GET /ai/gateway/extensions", gwSvc.ListExtensions)
	mgmt.HandleFunc("PUT /ai/gateway/extensions", middleware.RequirePlatformAdmin(gwSvc.UpdateExtension))
	mgmt.HandleFunc("DELETE /ai/gateway/extensions", middleware.RequirePlatformAdmin(gwSvc.DeleteExtension))

	// Model mappings — per-key virtual model name -> real model + fallback
	// chain (docs/design/01-routing-and-lb.md "console UI: fallback-chain
	// drag editor"). Same posture as MCP servers/extensions.
	mgmt.HandleFunc("POST /ai/gateway/model-mappings", middleware.RequirePlatformAdmin(gwSvc.CreateModelMapping))
	mgmt.HandleFunc("GET /ai/gateway/model-mappings", gwSvc.ListModelMappings)
	mgmt.HandleFunc("PUT /ai/gateway/model-mappings", middleware.RequirePlatformAdmin(gwSvc.UpdateModelMapping))
	mgmt.HandleFunc("DELETE /ai/gateway/model-mappings", middleware.RequirePlatformAdmin(gwSvc.DeleteModelMapping))

	// PII/guardrail policies — global, bindable per key
	// (docs/design/06-security-and-guardrails.md "console UI: guardrail-chain
	// builder"). Same posture as MCP servers/extensions.
	mgmt.HandleFunc("POST /ai/gateway/pii-policies", middleware.RequirePlatformAdmin(gwSvc.CreatePIIPolicy))
	mgmt.HandleFunc("GET /ai/gateway/pii-policies", gwSvc.ListPIIPolicies)
	mgmt.HandleFunc("PUT /ai/gateway/pii-policies", middleware.RequirePlatformAdmin(gwSvc.UpdatePIIPolicy))
	mgmt.HandleFunc("DELETE /ai/gateway/pii-policies", middleware.RequirePlatformAdmin(gwSvc.DeletePIIPolicy))

	// P1/P2: users, RBAC, admin API keys (docs/design/04-multi-tenancy-and-auth.md)
	mgmt.HandleFunc("GET /ai/gateway/auth/me", authSvc.Me)
	mgmt.HandleFunc("GET /ai/gateway/users", authSvc.ListUsers)
	mgmt.HandleFunc("PUT /ai/gateway/users/role", authSvc.UpdateUserRole)
	mgmt.HandleFunc("PUT /ai/gateway/users/status", authSvc.UpdateUserStatus)
	mgmt.HandleFunc("POST /ai/gateway/admin-keys", authSvc.CreateAdminKey)
	mgmt.HandleFunc("GET /ai/gateway/admin-keys", authSvc.ListAdminKeys)
	mgmt.HandleFunc("PUT /ai/gateway/admin-keys", authSvc.UpdateAdminKey)
	mgmt.HandleFunc("DELETE /ai/gateway/admin-keys", authSvc.DeleteAdminKey)

	mux.Handle("/ai/gateway/", admin.Middleware(mgmt))

	// -------------------------------------------------------------------------
	// Embedded web console (single-binary deployment)
	// -------------------------------------------------------------------------

	mux.Handle("/console/", http.StripPrefix("/console/", console.Handler()))
	mux.Handle("/console", http.RedirectHandler("/console/", http.StatusMovedPermanently))

	// -------------------------------------------------------------------------
	// Public homepage (docs/superpowers/specs/2026-07-10-homepage-and-brand-
	// mark-design.md) — a subtree pattern, so the more specific "/console/"
	// registration above still wins for anything under it.
	// -------------------------------------------------------------------------

	mux.Handle("/", homepage.Handler())

	// -------------------------------------------------------------------------
	// Proxy routes — authenticated via sk-vk-* Bearer token
	// -------------------------------------------------------------------------

	tracing := middleware.NewTracing(sys)

	// /ai/v1/models, /ai/v1/responses and the Batch/Files routes must be
	// registered before the /ai/v1/ catch-all.
	mux.Handle("/ai/v1/models", tracing.Middleware("openai", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.ListModels))))
	mux.Handle("/ai/v1/responses", tracing.Middleware("openai-responses", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.ResponsesProxy))))

	// Batch + Files API proxy (docs/design/09-extensibility.md), openai_compatible
	// providers only — a required X-AIGW-Provider header on the create/upload
	// calls selects which provider owns the (provider-scoped) file/batch.
	mux.Handle("POST /ai/v1/files", tracing.Middleware("openai-batch", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.FilesUpload))))
	mux.Handle("GET /ai/v1/files", tracing.Middleware("openai-batch", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.FilesList))))
	mux.Handle("GET /ai/v1/files/{id}", tracing.Middleware("openai-batch", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.FilesGet))))
	mux.Handle("GET /ai/v1/files/{id}/content", tracing.Middleware("openai-batch", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.FilesContent))))
	mux.Handle("DELETE /ai/v1/files/{id}", tracing.Middleware("openai-batch", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.FilesDelete))))
	mux.Handle("POST /ai/v1/batches", tracing.Middleware("openai-batch", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.BatchesCreate))))
	mux.Handle("GET /ai/v1/batches", tracing.Middleware("openai-batch", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.BatchesList))))
	mux.Handle("GET /ai/v1/batches/{id}", tracing.Middleware("openai-batch", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.BatchesGet))))
	mux.Handle("POST /ai/v1/batches/{id}/cancel", tracing.Middleware("openai-batch", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.BatchesCancel))))

	// Multimodal media adapters, phase 1 (docs/superpowers/specs/2026-07-09-
	// multimodal-media-adapters-design.md): image generation + audio TTS/ASR,
	// openai_compatible providers only. Registered before the /ai/v1/ catch-all.
	mux.Handle("POST /ai/v1/images/generations", tracing.Middleware("openai-images", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.ImagesGenerations))))
	mux.Handle("POST /ai/v1/audio/speech", tracing.Middleware("openai-audio", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.AudioSpeech))))
	mux.Handle("POST /ai/v1/audio/transcriptions", tracing.Middleware("openai-audio", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.AudioTranscriptions))))

	// Video generation, phase 2 (docs/superpowers/specs/2026-07-09-video-
	// generation-phase2-design.md): async submit/poll/download, openai_compatible
	// providers only, no billing/settlement poller (see Files/Batches above for
	// the analogous — but billed and header-selected — pattern).
	mux.Handle("POST /ai/v1/videos", tracing.Middleware("openai-videos", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.VideosCreate))))
	mux.Handle("GET /ai/v1/videos", tracing.Middleware("openai-videos", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.VideosList))))
	mux.Handle("GET /ai/v1/videos/{id}", tracing.Middleware("openai-videos", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.VideosGet))))
	mux.Handle("GET /ai/v1/videos/{id}/content", tracing.Middleware("openai-videos", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.VideosContent))))
	mux.Handle("DELETE /ai/v1/videos/{id}", tracing.Middleware("openai-videos", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.VideosDelete))))

	mux.Handle("/ai/v1/", tracing.Middleware("openai", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.ProxyRequest))))

	// Anthropic Messages API inbound codec (docs/design/02-protocol-adapters.md):
	// accepts x-api-key: sk-vk-* (Anthropic SDK convention) in addition to Bearer.
	mux.Handle("/anthropic/v1/messages", tracing.Middleware("anthropic", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.AnthropicMessagesProxy))))

	// MCP gateway (docs/design/09-extensibility.md): same sk-vk-* credential,
	// same middleware, as model traffic — "one credential system for models
	// and tools." {serverName} selects the registered upstream MCP server.
	mux.Handle("/ai/mcp/{serverName}", tracing.Middleware("mcp", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.MCPProxy))))

	addr := ":8080"
	if c != nil && c.HTTP != nil && c.HTTP.Addr != "" {
		addr = c.HTTP.Addr
	}

	return &http.Server{
		Addr:    addr,
		Handler: mux,
	}
}

func adminToken(sys *conf.System) string {
	if sys == nil {
		return ""
	}
	return sys.AdminToken
}

// NewReadyChecker wires DB and Redis pings into the readiness prober.
func NewReadyChecker(db *gorm.DB, rdb *redis.Client) *observability.ReadyChecker {
	return observability.NewReadyChecker(map[string]observability.Pinger{
		"database": func(ctx context.Context) error {
			sqlDB, err := db.DB()
			if err != nil {
				return err
			}
			return sqlDB.PingContext(ctx)
		},
		"redis": func(ctx context.Context) error {
			return rdb.Ping(ctx).Err()
		},
	})
}

// KratosServer wraps net/http.Server so it satisfies the kratos transport.Server
// interface (Start / Stop), allowing it to be managed by kratos.App lifecycle.
type KratosServer struct {
	httpSrv    *http.Server
	metricsSrv *http.Server // nil when the metrics listener is disabled
	ready      *observability.ReadyChecker
	audit      *biz.AuditWorker
	gw         *biz.GatewayUseCase
	logger     *log.Helper
}

// NewKratosServer constructs the lifecycle-aware server wrapper.
func NewKratosServer(
	c *conf.Server,
	httpSrv *http.Server,
	metrics *observability.Metrics,
	ready *observability.ReadyChecker,
	audit *biz.AuditWorker,
	gw *biz.GatewayUseCase,
	logger log.Logger,
) *KratosServer {
	var metricsSrv *http.Server
	if c != nil && c.Metrics != nil && c.Metrics.Addr != "" {
		mmux := http.NewServeMux()
		mmux.Handle("/metrics", metrics.Handler())
		metricsSrv = &http.Server{Addr: c.Metrics.Addr, Handler: mmux}
	}
	return &KratosServer{
		httpSrv:    httpSrv,
		metricsSrv: metricsSrv,
		ready:      ready,
		audit:      audit,
		gw:         gw,
		logger:     log.NewHelper(logger),
	}
}

// Start is called by kratos.App when the application starts.
func (s *KratosServer) Start(ctx context.Context) error {
	s.audit.Start(ctx)
	s.gw.StartBackgroundWorkers(ctx)

	s.logger.Infof("HTTP server listening on %s", s.httpSrv.Addr)
	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Errorf("HTTP server error: %v", err)
		}
	}()

	if s.metricsSrv != nil {
		s.logger.Infof("metrics server listening on %s", s.metricsSrv.Addr)
		go func() {
			if err := s.metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				s.logger.Errorf("metrics server error: %v", err)
			}
		}()
	}
	return nil
}

// Stop is called by kratos.App during graceful shutdown: readyz flips to 503
// first so load balancers drain, then the listeners shut down.
func (s *KratosServer) Stop(ctx context.Context) error {
	s.logger.Info("HTTP server shutting down")
	if s.ready != nil {
		s.ready.ShuttingDown = true
	}
	if s.metricsSrv != nil {
		_ = s.metricsSrv.Shutdown(ctx)
	}
	return s.httpSrv.Shutdown(ctx)
}
