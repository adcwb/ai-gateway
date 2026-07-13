package biz

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/adcwb/ai-gateway/internal/data/model"
)

// ProxyResponses is the /ai/v1/responses entrance (D02): decode Responses API
// request → OpenAI chat-completions body, run the existing ProxyRequest
// pipeline unchanged, re-encode the result via responsesResponseWriter. Same
// pattern as ProxyAnthropicMessages (anthropic_messages.go) — see that file's
// doc comment for why this reuses ProxyRequest rather than reimplementing the
// whole proxy pipeline per inbound codec.
//
// previous_response_id/store (docs/design/02-protocol-adapters.md,
// internal/biz/responses_state.go) are resolved/persisted here rather than
// in protocol_responses.go, since that needs DB access and virtual-key
// ownership scoping the codec file doesn't have.
func (uc *GatewayUseCase) ProxyResponses(ctx context.Context, key *model.AIVirtualKey, body []byte, w http.ResponseWriter, r *http.Request) {
	var peek responsesRequest
	if err := json.Unmarshal(body, &peek); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(responsesErrorBody(err.Error(), "INVALID_REQUEST"))
		return
	}

	var priorMessages []map[string]interface{}
	if peek.PreviousResponseID != "" {
		state, err := uc.loadResponseState(ctx, key.ID, peek.PreviousResponseID)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write(responsesErrorBody(err.Error(), "PREVIOUS_RESPONSE_NOT_FOUND"))
			return
		}
		if jerr := json.Unmarshal(state.Messages, &priorMessages); jerr != nil {
			uc.logger.Errorf("responses: 解析存储的会话状态失败 responseID=%s err=%v", peek.PreviousResponseID, jerr)
		}
	}

	oaBody, _, err := responsesToOpenAIChatRequest(body, priorMessages)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(responsesErrorBody(err.Error(), "INVALID_REQUEST"))
		return
	}
	requestedModel := extractModel(oaBody)

	rr := r.Clone(ctx)
	rr.URL.Path = "/ai/v1/chat/completions"

	// Minted once here rather than derived from the upstream chat completion
	// ID (as before) — the ID a client gets back must be unique and stable
	// enough to double as the previous_response_id it resubmits later.
	responseID := "resp_" + generateRequestID()

	var store *responsesStoreConfig
	if peek.Store != nil && *peek.Store {
		var sent struct {
			Messages []map[string]interface{} `json:"messages"`
		}
		json.Unmarshal(oaBody, &sent) //nolint:errcheck // best-effort; empty history is a safe fallback
		store = &responsesStoreConfig{uc: uc, keyID: key.ID, messages: sent.Messages}
	}

	rw := newResponsesResponseWriter(w, requestedModel, responseID, store)
	uc.ProxyRequest(ctx, key, oaBody, rw, rr)
	rw.Close()
}

// responsesStoreConfig carries what Close() needs to persist a turn when the
// request had store=true — the messages actually sent upstream this turn,
// which the resulting assistant turn gets appended to before saving.
type responsesStoreConfig struct {
	uc       *GatewayUseCase
	keyID    uint
	messages []map[string]interface{}
}

// responsesResponseWriter is the Responses-API twin of
// anthropicResponseWriter (anthropic_messages.go) — same buffered/streaming
// split, different translation functions. See that type's doc comment for
// the full rationale.
type responsesResponseWriter struct {
	real           http.ResponseWriter
	requestedModel string
	responseID     string
	store          *responsesStoreConfig

	header      http.Header
	wroteHeader bool
	statusCode  int
	streaming   bool

	buf bytes.Buffer

	pw   *io.PipeWriter
	done chan struct{}
}

func newResponsesResponseWriter(real http.ResponseWriter, requestedModel, responseID string, store *responsesStoreConfig) *responsesResponseWriter {
	return &responsesResponseWriter{real: real, requestedModel: requestedModel, responseID: responseID, store: store, header: http.Header{}}
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
			_, _, _, _, _, assistantMsg := openAIStreamToResponsesSSE(pr, a.real, a.requestedModel, a.responseID)
			a.persist(assistantMsg)
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

	translated, assistantMsg := openAIChatToResponses(a.buf.Bytes(), a.requestedModel, a.responseID)
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
	a.persist(assistantMsg)
}

// persist saves the resulting conversation turn when store was requested —
// a no-op otherwise, or if the turn ended in an error (assistantMsg is nil).
func (a *responsesResponseWriter) persist(assistantMsg map[string]interface{}) {
	if a.store == nil || assistantMsg == nil {
		return
	}
	messages := append(append([]map[string]interface{}{}, a.store.messages...), assistantMsg)
	a.store.uc.saveResponseState(context.Background(), a.store.keyID, a.responseID, a.requestedModel, messages)
}
