package conf

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Bootstrap struct {
	Server        *Server        `yaml:"server"`
	Database      *Database      `yaml:"database"`
	Redis         *Redis         `yaml:"redis"`
	AI            *AI            `yaml:"ai"`
	System        *System        `yaml:"system"`
	Observability *Observability `yaml:"observability"`
	Auth          *Auth          `yaml:"auth"`
	Audit         *Audit         `yaml:"audit"`
	Extensions    *Extensions    `yaml:"extensions"`
}

type Server struct {
	HTTP    *HTTPServer    `yaml:"http"`
	Metrics *MetricsServer `yaml:"metrics"`
}

type HTTPServer struct {
	Addr    string        `yaml:"addr"`
	Timeout time.Duration `yaml:"timeout"`
}

// MetricsServer configures the Prometheus metrics listener.
// It is a separate listener from the proxy port so metrics are never
// exposed to untrusted proxy clients. Empty addr disables the listener.
type MetricsServer struct {
	Addr string `yaml:"addr"`
}

type Database struct {
	// Driver selects the GORM dialect: "mysql" (default), "postgres", "sqlite".
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

type Redis struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type AI struct {
	ProxyTimeoutSec int `yaml:"proxy_timeout_sec"`
	AgentTimeoutSec int `yaml:"agent_timeout_sec"`
}

type System struct {
	EncryptionKey string `yaml:"encryption_key"`
	// AdminToken protects the management API (/ai/gateway/*).
	// When empty the management plane is OPEN — only acceptable behind a
	// trusted reverse proxy; a startup warning is emitted.
	AdminToken string `yaml:"admin_token"`
	// AlertWebhook, when set, receives billing alerts (budget watermark,
	// grace entry, suspension) as JSON POSTs.
	AlertWebhook string `yaml:"alert_webhook"`
}

// Observability configures OpenTelemetry tracing (docs/design/05-observability.md).
// An empty OTLPEndpoint disables tracing entirely: SetupTracing constructs no
// exporter/processor and the global OTel no-op TracerProvider stays in place,
// so instrumentation call sites cost a few no-op function calls.
type Observability struct {
	OTLPEndpoint string  `yaml:"otlp_endpoint"`
	Insecure     bool    `yaml:"otlp_insecure"`
	SampleRatio  float64 `yaml:"trace_sample_ratio"`
}

// Auth configures the console management-plane principals beyond the
// bootstrap admin token (docs/design/04-multi-tenancy-and-auth.md): OIDC/SSO
// login with JIT user provisioning, and JWT session cookies. Empty OIDCIssuer
// disables SSO entirely — the bootstrap token remains the only principal,
// exactly today's behavior.
type Auth struct {
	OIDCIssuer       string `yaml:"oidc_issuer"`
	OIDCClientID     string `yaml:"oidc_client_id"`
	OIDCClientSecret string `yaml:"oidc_client_secret"`
	OIDCRedirectURL  string `yaml:"oidc_redirect_url"`
	// OIDCRoleClaim names the ID-token claim (e.g. "groups", "roles") consulted
	// for JIT role assignment into the tenant named OIDCDefaultTenant; absent
	// or unmapped claim values fall back to OIDCDefaultRole.
	OIDCRoleClaim     string `yaml:"oidc_role_claim"`
	OIDCDefaultRole   string `yaml:"oidc_default_role"`
	OIDCDefaultTenant string `yaml:"oidc_default_tenant"`
	// SessionSecret signs the console's JWT session cookie (HMAC). Falls back
	// to system.encryption_key when unset so a single secret suffices for the
	// common case; set independently to rotate sessions without re-encrypting data.
	SessionSecret   string `yaml:"session_secret"`
	SessionTTLHours int    `yaml:"session_ttl_hours"`
}

// Audit configures the gateway traffic audit trail (docs/design/06-security-
// and-guardrails.md P1 "Audit body encryption"). EncryptBodies AES-GCM
// encrypts request/response bodies at rest using system.encryption_key —
// opt-in because encrypted bodies are excluded from Elasticsearch full-text
// indexing (a deployment chooses searchability or at-rest encryption).
type Audit struct {
	EncryptBodies bool `yaml:"encrypt_bodies"`
}

// Extensions configures the event bus's infra-level sinks (docs/design/09-
// extensibility.md "Event bus"). Everything else extensibility-related
// (ai_extensions rows: webhook/WASM hooks, their timeouts/fail-mode) is
// DB/admin-API-driven, like ai_mcp_servers — these are the two settings that
// genuinely can't live in a DB row because they're needed to reach outside
// the process at all.
type Extensions struct {
	// KafkaBrokers, if non-empty, enables the Kafka sink (topic per event
	// type: "audit"/"billing"). Empty = Kafka sink disabled, zero overhead.
	KafkaBrokers []string `yaml:"kafka_brokers"`
	// EventWebhookURL/Secret, if set, enables a webhook sink for on_audit/
	// on_billing events (batched, HMAC-signed). Empty URL = disabled.
	EventWebhookURL    string `yaml:"event_webhook_url"`
	EventWebhookSecret string `yaml:"event_webhook_secret"`
}

// ApplyEnvOverrides maps AIGW_* environment variables onto config fields so
// secrets never need to live in the YAML file (compose / k8s ergonomics).
func (bc *Bootstrap) ApplyEnvOverrides() {
	if v := os.Getenv("AIGW_HTTP_ADDR"); v != "" {
		bc.ensureServer().HTTP.Addr = v
	}
	if v := os.Getenv("AIGW_METRICS_ADDR"); v != "" {
		bc.ensureServer().Metrics.Addr = v
	}
	if v := os.Getenv("AIGW_DB_DRIVER"); v != "" {
		bc.ensureDatabase().Driver = v
	}
	if v := os.Getenv("AIGW_DB_DSN"); v != "" {
		bc.ensureDatabase().DSN = v
	}
	if v := os.Getenv("AIGW_REDIS_ADDR"); v != "" {
		bc.ensureRedis().Addr = v
	}
	if v := os.Getenv("AIGW_REDIS_PASSWORD"); v != "" {
		bc.ensureRedis().Password = v
	}
	if v := os.Getenv("AIGW_ENCRYPTION_KEY"); v != "" {
		bc.ensureSystem().EncryptionKey = v
	}
	if v := os.Getenv("AIGW_ADMIN_TOKEN"); v != "" {
		bc.ensureSystem().AdminToken = v
	}
	if v := os.Getenv("AIGW_ALERT_WEBHOOK"); v != "" {
		bc.ensureSystem().AlertWebhook = v
	}
	if v := os.Getenv("AIGW_OTLP_ENDPOINT"); v != "" {
		bc.ensureObservability().OTLPEndpoint = v
	}
	if v := os.Getenv("AIGW_OTLP_INSECURE"); v != "" {
		bc.ensureObservability().Insecure = v == "true" || v == "1"
	}
	if v := os.Getenv("AIGW_TRACE_SAMPLE_RATIO"); v != "" {
		if ratio, err := strconv.ParseFloat(v, 64); err == nil {
			bc.ensureObservability().SampleRatio = ratio
		}
	}
	if v := os.Getenv("AIGW_OIDC_ISSUER"); v != "" {
		bc.ensureAuth().OIDCIssuer = v
	}
	if v := os.Getenv("AIGW_OIDC_CLIENT_ID"); v != "" {
		bc.ensureAuth().OIDCClientID = v
	}
	if v := os.Getenv("AIGW_OIDC_CLIENT_SECRET"); v != "" {
		bc.ensureAuth().OIDCClientSecret = v
	}
	if v := os.Getenv("AIGW_OIDC_REDIRECT_URL"); v != "" {
		bc.ensureAuth().OIDCRedirectURL = v
	}
	if v := os.Getenv("AIGW_SESSION_SECRET"); v != "" {
		bc.ensureAuth().SessionSecret = v
	}
	if v := os.Getenv("AIGW_AUDIT_ENCRYPT_BODIES"); v != "" {
		bc.ensureAudit().EncryptBodies = v == "true" || v == "1"
	}
	if v := os.Getenv("AIGW_KAFKA_BROKERS"); v != "" {
		bc.ensureExtensions().KafkaBrokers = strings.Split(v, ",")
	}
	if v := os.Getenv("AIGW_EVENT_WEBHOOK_URL"); v != "" {
		bc.ensureExtensions().EventWebhookURL = v
	}
	if v := os.Getenv("AIGW_EVENT_WEBHOOK_SECRET"); v != "" {
		bc.ensureExtensions().EventWebhookSecret = v
	}
}

func (bc *Bootstrap) ensureServer() *Server {
	if bc.Server == nil {
		bc.Server = &Server{}
	}
	if bc.Server.HTTP == nil {
		bc.Server.HTTP = &HTTPServer{}
	}
	if bc.Server.Metrics == nil {
		bc.Server.Metrics = &MetricsServer{}
	}
	return bc.Server
}

func (bc *Bootstrap) ensureDatabase() *Database {
	if bc.Database == nil {
		bc.Database = &Database{}
	}
	return bc.Database
}

func (bc *Bootstrap) ensureRedis() *Redis {
	if bc.Redis == nil {
		bc.Redis = &Redis{}
	}
	return bc.Redis
}

func (bc *Bootstrap) ensureSystem() *System {
	if bc.System == nil {
		bc.System = &System{}
	}
	return bc.System
}

func (bc *Bootstrap) ensureObservability() *Observability {
	if bc.Observability == nil {
		bc.Observability = &Observability{}
	}
	return bc.Observability
}

func (bc *Bootstrap) ensureAuth() *Auth {
	if bc.Auth == nil {
		bc.Auth = &Auth{}
	}
	return bc.Auth
}

func (bc *Bootstrap) ensureAudit() *Audit {
	if bc.Audit == nil {
		bc.Audit = &Audit{}
	}
	return bc.Audit
}

func (bc *Bootstrap) ensureExtensions() *Extensions {
	if bc.Extensions == nil {
		bc.Extensions = &Extensions{}
	}
	return bc.Extensions
}
