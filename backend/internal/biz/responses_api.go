package biz

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

// ProxyResponses is the /ai/v1/responses entrance (D02): decode Responses API
// request → OpenAI chat-completions body, run the existing ProxyRequest
// pipeline unchanged, re-encode the result via responsesResponseWriter. Same
// pattern as ProxyAnthropicMessages (anthropic_messages.go) — see that file's
// doc comment for why this reuses ProxyRequest rather than reimplementing the
// whole proxy pipeline per inbound codec.
func (uc *GatewayUseCase) ProxyResponses(ctx context.Context, key *model.AIVirtualKey, body []byte, w http.ResponseWriter, r *http.Request) {
	oaBody, _, err := responsesToOpenAIChatRequest(body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(responsesErrorBody(err.Error(), "INVALID_REQUEST"))
		return
	}
	requestedModel := extractModel(oaBody)

	rr := r.Clone(ctx)
	rr.URL.Path = "/ai/v1/chat/completions"

	rw := newResponsesResponseWriter(w, requestedModel)
	uc.ProxyRequest(ctx, key, oaBody, rw, rr)
	rw.Close()
}

// responsesResponseWriter is the Responses-API twin of
// anthropicResponseWriter (anthropic_messages.go) — same buffered/streaming
// split, different translation functions. See that type's doc comment for
// the full rationale.
type responsesResponseWriter struct {
	real           http.ResponseWriter
	requestedModel string

	header      http.Header
	wroteHeader bool
	statusCode  int
	streaming   bool

	buf bytes.Buffer

	pw   *io.PipeWriter
	done chan struct{}
}

func newResponsesResponseWriter(real http.ResponseWriter, requestedModel string) *responsesResponseWriter {
	return &responsesResponseWriter{real: real, requestedModel: requestedModel, header: http.Header{}}
}

func (a *responsesResponseWriter) Header() http.Header { return a.header }

func (a *responsesResponseWriter) WriteHeader(status int) {
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
			openAIStreamToResponsesSSE(pr, a.real, a.requestedModel)
		}()
	}
}

func (a *responsesResponseWriter) Write(p []byte) (int, error) {
	if !a.wroteHeader {
		a.WriteHeader(http.StatusOK)
	}
	if a.streaming {
		return a.pw.Write(p)
	}
	return a.buf.Write(p)
}

func (a *responsesResponseWriter) Flush() {
	if f, ok := a.real.(http.Flusher); ok {
		f.Flush()
	}
}

func (a *responsesResponseWriter) Close() {
	if !a.wroteHeader {
		return
	}
	if a.streaming {
		a.pw.Close()
		<-a.done
		return
	}

	translated := openAIChatToResponses(a.buf.Bytes(), a.requestedModel)
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
