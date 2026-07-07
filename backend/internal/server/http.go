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
	"github.com/opscenter/ai-gateway/internal/middleware"
	"github.com/opscenter/ai-gateway/internal/observability"
	"github.com/opscenter/ai-gateway/internal/service"
)

// NewHTTPServer builds and returns the net/http.Server with all routes registered.
func NewHTTPServer(
	c *conf.Server,
	sys *conf.System,
	gwSvc *service.GatewayService,
	gwUc *biz.GatewayUseCase,
	quota *biz.QuotaManager,
	ready *observability.ReadyChecker,
	logger log.Logger,
) *http.Server {
	mux := http.NewServeMux()
	auth := middleware.NewVirtualKeyAuth(gwUc, quota)
	admin := middleware.NewAdminAuth(adminToken(sys), logger)

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
	mgmt.HandleFunc("POST /ai/gateway/billing/recharge", gwSvc.Recharge)
	mgmt.HandleFunc("PUT /ai/gateway/billing/account", gwSvc.UpdateBillingAccount)
	mgmt.HandleFunc("GET /ai/gateway/billing/ledger", gwSvc.ListLedger)
	mgmt.HandleFunc("GET /ai/gateway/stats/overview", gwSvc.UsageOverview)
	mgmt.HandleFunc("GET /ai/gateway/stats/timeseries", gwSvc.UsageTimeseries)

	// P2: model catalog + price tables (docs/design/03,08 module 4)
	mgmt.HandleFunc("POST /ai/gateway/model-items", gwSvc.CreateModelItem)
	mgmt.HandleFunc("GET /ai/gateway/model-items", gwSvc.ListModelItems)
	mgmt.HandleFunc("PUT /ai/gateway/model-items", gwSvc.UpdateModelItem)
	mgmt.HandleFunc("DELETE /ai/gateway/model-items", gwSvc.DeleteModelItem)
	mgmt.HandleFunc("POST /ai/gateway/price-tables", gwSvc.CreatePriceTable)
	mgmt.HandleFunc("GET /ai/gateway/price-tables", gwSvc.ListPriceTables)
	mgmt.HandleFunc("PUT /ai/gateway/price-tables", gwSvc.UpdatePriceTable)
	mgmt.HandleFunc("DELETE /ai/gateway/price-tables", gwSvc.DeletePriceTable)
	mgmt.HandleFunc("POST /ai/gateway/price-tables/items", gwSvc.CreatePriceTableItem)
	mgmt.HandleFunc("PUT /ai/gateway/price-tables/items", gwSvc.UpdatePriceTableItem)
	mgmt.HandleFunc("DELETE /ai/gateway/price-tables/items", gwSvc.DeletePriceTableItem)
	mgmt.HandleFunc("POST /ai/gateway/price-tables/test-pattern", gwSvc.TestPricePattern)

	// P2: settings + credits rates (docs/design/08-web-console.md module 8)
	mgmt.HandleFunc("GET /ai/gateway/settings", gwSvc.GetSettings)
	mgmt.HandleFunc("PUT /ai/gateway/settings", gwSvc.UpdateSettings)
	mgmt.HandleFunc("POST /ai/gateway/settings/test-webhook", gwSvc.TestAlertWebhook)
	mgmt.HandleFunc("POST /ai/gateway/credits-rates", gwSvc.CreateCreditsRate)
	mgmt.HandleFunc("GET /ai/gateway/credits-rates", gwSvc.ListCreditsRates)
	mgmt.HandleFunc("PUT /ai/gateway/credits-rates", gwSvc.UpdateCreditsRate)
	mgmt.HandleFunc("DELETE /ai/gateway/credits-rates", gwSvc.DeleteCreditsRate)

	mux.Handle("/ai/gateway/", admin.Middleware(mgmt))

	// -------------------------------------------------------------------------
	// Embedded web console (single-binary deployment)
	// -------------------------------------------------------------------------

	mux.Handle("/console/", http.StripPrefix("/console/", console.Handler()))
	mux.Handle("/console", http.RedirectHandler("/console/", http.StatusMovedPermanently))

	// -------------------------------------------------------------------------
	// Proxy routes — authenticated via sk-vk-* Bearer token
	// -------------------------------------------------------------------------

	tracing := middleware.NewTracing(sys)

	// /ai/v1/models must be registered before /ai/v1/ catch-all
	mux.Handle("/ai/v1/models", tracing.Middleware("openai", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.ListModels))))
	mux.Handle("/ai/v1/", tracing.Middleware("openai", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.ProxyRequest))))

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
