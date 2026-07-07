package conf

import (
	"os"
	"strconv"
	"time"
)

type Bootstrap struct {
	Server        *Server        `yaml:"server"`
	Database      *Database      `yaml:"database"`
	Redis         *Redis         `yaml:"redis"`
	AI            *AI            `yaml:"ai"`
	System        *System        `yaml:"system"`
	Observability *Observability `yaml:"observability"`
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
