package biz

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

// ProxyAnthropicMessages is the /anthropic/v1/messages entrance (D02): decode
// Anthropic Messages → OpenAI-shape body, run the existing ProxyRequest
// pipeline completely unchanged (PII → model resolution → quota → billing →
// cache → routing/failover → settlement), then re-encode whatever
// OpenAI-shape response ProxyRequest wrote back into Anthropic Messages shape
// via anthropicResponseWriter. This is how one new inbound codec reaches
// every existing outbound dialect for free.
func (uc *GatewayUseCase) ProxyAnthropicMessages(ctx context.Context, key *model.AIVirtualKey, body []byte, w http.ResponseWriter, r *http.Request) {
	oaBody, _, err := anthropicMessagesToOpenAIRequest(body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(openAIErrorToAnthropicError("invalid request: "+err.Error(), "invalid_request_error"))
		return
	}
	requestedModel := extractModel(oaBody)

	// ProxyRequest strips a literal "/ai/v1" prefix off r.URL.Path to build the
	// upstream path for openai_compatible/azure_openai providers; this route
	// lives under /anthropic/v1 instead, so a shallow clone stands in for it.
	rr := r.Clone(ctx)
	rr.URL.Path = "/ai/v1/chat/completions"

	aw := newAnthropicResponseWriter(w, requestedModel)
	uc.ProxyRequest(ctx, key, oaBody, aw, rr)
	aw.Close()
}

// anthropicResponseWriter lets ProxyRequest (which only ever speaks the
// OpenAI wire shape) be reused byte-for-byte unchanged for an Anthropic
// Messages client. Non-streaming bodies are buffered whole and translated on
// Close(); streaming bodies are piped into openAIStreamToAnthropicSSE running
// in a background goroutine — mirroring how ProxyRequest itself consumes an
// upstream body via bufio.Scanner, just with the roles of reader/writer
// inverted.
type anthropicResponseWriter struct {
	real           http.ResponseWriter
	requestedModel string

	header      http.Header
	wroteHeader bool
	statusCode  int
	streaming   bool

	buf bytes.Buffer // non-streaming mode only

	pw   *io.PipeWriter
	done chan struct{}
}

func newAnthropicResponseWriter(real http.ResponseWriter, requestedModel string) *anthropicResponseWriter {
	return &anthropicResponseWriter{real: real, requestedModel: requestedModel, header: http.Header{}}
}

func (a *anthropicResponseWriter) Header() http.Header { return a.header }

func (a *anthropicResponseWriter) WriteHeader(status int) {
	if a.wroteHeader {
		return
	}
	a.wroteHeader = true
	a.statusCode = status

	if strings.Contains(a.header.Get("Content-Type"), "text/event-stream") {
		a.streaming = true
		a.real.Header().Set("Content-Type", "text/event-stream")
		a.real.Header().Set("Cache-Control", "no-cache")
		a.real.WriteHeader(status)

		pr, pw := io.Pipe()
		a.pw = pw
		a.done = make(chan struct{})
		go func() {
			defer close(a.done)
			openAIStreamToAnthropicSSE(pr, a.real, a.requestedModel)
		}()
	}
}

func (a *anthropicResponseWriter) Write(p []byte) (int, error) {
	if !a.wroteHeader {
		a.WriteHeader(http.StatusOK)
	}
	if a.streaming {
		return a.pw.Write(p)
	}
	return a.buf.Write(p)
}

// Flush satisfies http.Flusher — translateAnthropicStream/streamProxy/
// writeCachedResponse's streaming branch all type-assert their writer to
// http.Flusher and call it after every chunk.
func (a *anthropicResponseWriter) Flush() {
	if f, ok := a.real.(http.Flusher); ok {
		f.Flush()
	}
}

// Close finalizes the response. Must be called exactly once after
// ProxyRequest returns: for streaming mode it closes the pipe and waits for
// the translation goroutine to drain (so the HTTP handler doesn't return
// before the last bytes reach the client); for buffered mode it translates
// the whole accumulated OpenAI-shape body (success or the gateway's own
// {"error":...} shape — openAIResponseToAnthropicMessage dispatches both) and
// writes it to the real ResponseWriter.
func (a *anthropicResponseWriter) Close() {
	if !a.wroteHeader {
		return // ProxyRequest returned without writing anything; nothing to finalize.
	}
	if a.streaming {
		a.pw.Close()
		<-a.done
		return
	}

	translated := openAIResponseToAnthropicMessage(a.buf.Bytes(), a.requestedModel)
	for k, vv := range a.header {
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Content-Type") {
			continue
		}
		for _, v := range vv {
			a.real.Header().Add(k, v)
		}
	}
	a.real.Header().Set("Content-Type", "application/json")
	a.real.WriteHeader(a.statusCode)
	a.real.Write(translated)
}
