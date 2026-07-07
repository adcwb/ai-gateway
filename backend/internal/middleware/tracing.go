package middleware

import (
	"crypto/subtle"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/observability"
)

// forceTraceHeader lets an admin force full sampling on one request for
// debugging (docs/design/05-observability.md). It only takes effect when it
// matches system.admin_token — anyone else's header is ignored so tracing
// backends cannot be amplified by arbitrary clients.
const forceTraceHeader = "X-AIGW-Trace-Force"

// Tracing opens the "aigw.request" root span for the proxy routes. It sits
// outside VirtualKeyAuth so the span also covers auth/quota rejections.
type Tracing struct {
	adminToken string
	propagator propagation.TextMapPropagator
}

func NewTracing(sys *conf.System) *Tracing {
	token := ""
	if sys != nil {
		token = sys.AdminToken
	}
	return &Tracing{adminToken: token, propagator: propagation.TraceContext{}}
}

// Middleware wraps next with the root span. routeLabel is a low-cardinality
// tag (e.g. "openai", "anthropic") describing the inbound dialect.
func (t *Tracing) Middleware(routeLabel string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := t.propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		if t.adminToken != "" {
			got := r.Header.Get(forceTraceHeader)
			if got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(t.adminToken)) == 1 {
				ctx = observability.WithForceSample(ctx)
			}
		}

		ctx, span := observability.Tracer.Start(ctx, "aigw.request",
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", r.URL.Path),
				attribute.String("inbound_protocol", routeLabel),
			))
		defer span.End()

		rw := &statusCapturingWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r.WithContext(ctx))

		span.SetAttributes(attribute.Int("http.status_code", rw.status))
		if rw.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(rw.status))
		}
	})
}

// statusCapturingWriter records the status code written by the inner handler
// so the root span can be tagged after ServeHTTP returns.
type statusCapturingWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusCapturingWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusCapturingWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(b)
}

// Flush proxies http.Flusher so streamed (SSE) responses keep working
// through the wrapper.
func (w *statusCapturingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
