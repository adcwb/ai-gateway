# D02 · Protocol Adapters

> [中文版](../zh-CN/design/02-protocol-adapters.md) · Part of the [ai-gateway documentation suite](../README.md)

| | |
| --- | --- |
| **Phase** | P2 (P3 for Batch/Files APIs) |
| **Depends on** | [D01 Routing](01-routing-and-lb.md) (routing selects the provider; the adapter speaks its dialect) |
| **Depended on by** | [D03 Billing](03-billing-and-monetization.md) and audit (both consume normalized usage), [D07 Caching](07-caching-strategies.md) |

## Context

Today every provider is assumed OpenAI-compatible: `AIProvider.ProviderType` defaults to `openai_compatible` and nothing branches on it except path rewriting (`rewriteOpenAIPathForProvider()` handles the DashScope rerank path). This means:

- Anthropic, Gemini, and Bedrock models are only reachable through third-party compatibility shims — provider choice is constrained by dialect, not quality or price.
- Clients built on the Anthropic SDK (increasingly common: Claude Code, MCP tooling) cannot use the gateway at all.
- Usage accounting silently assumes OpenAI's `usage` shape; cached-token and reasoning-token fields from other dialects would be dropped, corrupting billing.

The adapter layer removes dialect as a constraint in both directions, which is the prerequisite for the P2 exit criterion: *a Claude-SDK client and an OpenAI-SDK client both call the same virtual key and land on the same Gemini provider with correct usage*.

## The protocol matrix

Two independent axes. The gateway's internal representation (IR) sits in the middle, so the work is `N + M` translators, not `N × M`.

```mermaid
flowchart LR
    subgraph Inbound["Inbound (client dialect)"]
        I1["OpenAI Chat Completions<br/>/ai/v1/chat/completions"]
        I2["OpenAI Responses API<br/>/ai/v1/responses"]
        I3["Anthropic Messages<br/>/anthropic/v1/messages"]
    end
    IR["Internal Representation<br/>(canonical request + usage)"]
    subgraph Outbound["Outbound (provider dialect)"]
        O1[openai_compatible]
        O2[anthropic]
        O3[gemini]
        O4[bedrock]
        O5[azure_openai]
    end
    I1 --> IR --> O1
    I2 --> IR --> O2
    IR --> O3
    I3 --> IR
    IR --> O4
    IR --> O5
```

Rollout order (by demand): outbound `anthropic` → `gemini` → `azure_openai` → `bedrock`; inbound `anthropic messages` → `responses`. `openai_compatible` remains the identity adapter and the default, preserving today's behavior exactly.

### Fast path guarantee

When inbound dialect == outbound dialect (OpenAI→openai_compatible — the overwhelming majority of traffic), the adapter layer must **not** round-trip through the IR. The identity adapter passes bodies through with only the existing targeted mutations (`replaceModelInBody()`, `injectStreamUsageOption()`, `injectPromptCacheKey()`, `injectModelExtraParams()`). Full parse/serialize is paid only when translation is actually needed. This protects the hot-path budget (design principle 5).

## Internal representation

Not a lossless union of every API — a **routing-and-accounting** canonical form plus a passthrough extension bag:

```go
// internal/biz/protocol/ir.go
type ChatRequest struct {
    Model       string
    Messages    []Message      // role, content parts (text/image/audio/tool_call/tool_result)
    System      string         // Anthropic separates it; OpenAI embeds it — IR separates it
    Tools       []Tool
    Stream      bool
    MaxTokens   *int
    Temperature *float64
    // ... other first-class common params
    Extensions  map[string]json.RawMessage // dialect-specific params, forwarded when outbound dialect understands them, dropped (and audit-noted) otherwise
}

type Usage struct {
    InputTokens        int
    OutputTokens       int
    CacheReadTokens    int // OpenAI: prompt_tokens_details.cached_tokens · Anthropic: cache_read_input_tokens
    CacheWriteTokens   int // Anthropic: cache_creation_input_tokens · absent elsewhere
    ReasoningTokens    int // OpenAI: completion_tokens_details.reasoning_tokens · Gemini: thoughtsTokenCount
    Raw                json.RawMessage // provider-native usage object, preserved in audit for provenance
}
```

`Usage` is the single shape consumed by `writeAuditLog()`, `QuotaManager.CommitTokens()`, and `calcCredits()` (`internal/biz/credits.go` already prices cache-read tokens separately — the IR finally feeds that field from non-OpenAI dialects too).

## Adapter interfaces

```go
// internal/biz/protocol/adapter.go

// OutboundAdapter speaks one provider dialect. Selected by AIProvider.ProviderType.
type OutboundAdapter interface {
    // BuildRequest maps IR → provider HTTP request (URL from BaseURL + dialect path, auth header style, body).
    BuildRequest(ctx context.Context, p *model.AIProvider, req *ChatRequest) (*http.Request, error)
    // ParseResponse maps a non-streaming provider response → IR response + normalized Usage.
    ParseResponse(resp *http.Response) (*ChatResponse, *Usage, error)
    // StreamDecoder wraps the provider's SSE/chunk stream into a stream of IR events.
    StreamDecoder(body io.Reader) StreamDecoder
}

// InboundCodec speaks one client-facing dialect. Selected by route.
type InboundCodec interface {
    DecodeRequest(r *http.Request) (*ChatRequest, error)
    EncodeResponse(w http.ResponseWriter, resp *ChatResponse) error
    // StreamEncoder writes IR stream events in the client's expected wire format,
    // including dialect-correct event names, role deltas, and terminators ([DONE] vs message_stop).
    StreamEncoder(w http.ResponseWriter) StreamEncoder
}
```

Registration is compile-time (a `map[string]OutboundAdapter` populated in an `init()`-free registry wired through Wire), matching the project's "no runtime magic" stance; community adapters arrive as PRs adding one package + one registry line. `rewriteOpenAIPathForProvider()` folds into the `openai_compatible` adapter's `BuildRequest` and is deleted as a standalone.

### Streaming translation

The hard 20%. Design rules:

1. **Event-level, not token-level, state.** The translator keeps a small state machine per stream (current tool-call index, content block index) — Anthropic's `content_block_start/delta/stop` maps to OpenAI's indexed `tool_calls` deltas and vice versa.
2. **Usage arrives at different points** (OpenAI: final chunk with `stream_options.include_usage`; Anthropic: `message_delta`; Gemini: `usageMetadata` on chunks). The decoder emits a terminal `UsageEvent` regardless of source; audit/billing consume only that.
3. **First-chunk commit rule** from [D01](01-routing-and-lb.md) applies unchanged: once the encoder writes byte one, failover is off.
4. Per-chunk overhead budget: < 5 ms p99 (P2 exit criterion) — translators must not buffer whole responses; they operate chunk-in/events-out.

### Auth & endpoint dialects

| ProviderType | Auth | Path shape | Notes |
| --- | --- | --- | --- |
| `openai_compatible` | `Authorization: Bearer` | `/v1/chat/completions` | today's behavior |
| `anthropic` | `x-api-key` + `anthropic-version` | `/v1/messages` | version header configurable per provider |
| `gemini` | `x-goog-api-key` (or OAuth) | `/v1beta/models/{model}:generateContent` / `:streamGenerateContent` | model is in the *path* — `BuildRequest` owns URL construction |
| `azure_openai` | `api-key` header | `/openai/deployments/{deployment}/chat/completions?api-version=…` | deployment name ≠ model name: stored in `AIModelItem` extra params |
| `bedrock` | SigV4 signing | `/model/{modelId}/invoke(-with-response-stream)` | needs AWS credentials in provider config; the streaming wire format (event-stream) has its own decoder |

Provider-specific settings (anthropic-version, api-version, region, deployment map) live in a new nullable JSON column `adapter_config` on `ai_providers`.

## Inbound entrances

New routes registered in `internal/server/http.go`, guarded by the same `virtual_key_auth` middleware:

- `POST /anthropic/v1/messages` — Anthropic Messages codec. Accepts `x-api-key: sk-vk-*` (Anthropic SDK convention) in addition to Bearer.
- `POST /ai/v1/responses` — OpenAI Responses API codec, mapped onto the IR (stateless subset first; `previous_response_id` chaining is out of scope until demand is proven — noted as an open question).

Model resolution, quotas, routing, audit, and billing are dialect-independent because they run on the IR after decode.

## Data model changes

| Table | Change |
| --- | --- |
| `ai_providers` | `adapter_config json` (nullable) |
| `ai_model_items` | usage of existing extra-params for azure deployment mapping (no schema change) |
| `ai_gateway_audit_logs` | `inbound_protocol varchar(32)`, `cache_write_tokens int`, `reasoning_tokens int` (cache-read already exists in the billing path) |

## Touched code

| Location | Change |
| --- | --- |
| `internal/biz/protocol/` (new package) | IR, adapter/codec interfaces, registry, per-dialect implementations |
| `internal/biz/gateway.go` `ProxyRequest` | Decode via inbound codec → IR pipeline → outbound adapter; fast path preserved |
| `internal/biz/gateway.go` body-mutation helpers | Absorbed into the identity adapter |
| `internal/server/http.go` | New inbound routes |
| `internal/biz/credits.go` `calcCredits` | Accept `Usage` (adds cache-write pricing, already priced in `AIModelItem.CacheWritePricePerMillion`) |

## Testing & verification

- Golden-file tests per adapter: recorded provider fixtures (request/response/stream transcripts) → assert exact IR and exact re-encoded output. Fixtures are the contract; provider API drift shows up as fixture diffs.
- Cross-dialect matrix test: every inbound codec × every outbound adapter over a canonical conversation (text + tool call + streaming), asserting normalized `Usage` equality.
- Load test on the fast path proving zero added allocation/latency for OpenAI→openai_compatible relative to the pre-adapter baseline.

## Implementation notes (ADR addendum)

What shipped covers the whole matrix in this document — inbound Anthropic Messages (`/anthropic/v1/messages`) and OpenAI Responses (`/ai/v1/responses`), outbound bedrock — but takes a different internal shape than proposed above.

- **IR decision**: no new `internal/biz/protocol/` package or dedicated `ChatRequest`/`ChatResponse` structs. `internal/biz/protocol.go`'s own doc comment already asserted "the internal representation is the OpenAI Chat Completions wire format," and every existing outbound dialect (anthropic, gemini, azure_openai) already pivoted through it with full test coverage. Rather than replace that with a parallel struct-based IR, the new inbound codecs translate at the two edges only (request decode, response/stream encode) using the same OpenAI-shape pivot. This makes the fast-path guarantee trivially true — the OpenAI-in→openai_compatible-out path is untouched by any of this work — and keeps new-inbound × existing-outbound at N+M without an N×M rewrite. Cost: the IR is OpenAI-shaped rather than a neutral union; accepted because billing/audit only ever needed the fields `internal/biz/credits.go` already modeled.
- **Response re-encoding**: rather than an `InboundCodec` interface with a registry, each new inbound route wraps the real `http.ResponseWriter` in a small adapter (`anthropicResponseWriter` / `responsesResponseWriter`) that buffers non-streaming bodies whole (translating on `Close()` — success and the gateway's own `{"error":...}` bodies both, dispatched by a structural check, so every existing error path — PII block, quota, billing 402, all-providers-failed 502 — comes out correctly shaped for free) and pipes streaming bodies through a translator goroutine. `ProxyRequest` itself is called completely unmodified.
- **Bedrock scope**: Anthropic Claude models on the native Invoke API only, not the general model-agnostic case sketched in the auth/endpoint table above. Other Bedrock model families have mutually incompatible invoke body shapes; adding them is a bounded follow-up (one more branch + fixture set), not a redesign. SigV4 is hand-implemented (`internal/biz/bedrock/`, dependency-free w.r.t. `biz`) rather than pulling in aws-sdk-go, and cross-checked against an independently-written Python reference implementation in tests rather than only re-deriving the same arithmetic twice in Go.
- **Bedrock credentials**: `AIProvider.APIKey` (the one AES-256-GCM-encrypted string column every dialect already uses) holds a JSON bundle `{"accessKeyId","secretAccessKey","sessionToken"}` for `bedrock`-type providers, rather than new dedicated columns. Region is non-secret and lives in `adapter_config` alongside anthropic's/azure's own dialect settings.
- **Cache-write/reasoning tokens**: `anthropicToOpenAIResponse`/`translateAnthropicStream` now surface `cache_creation_input_tokens` (previously parsed and silently dropped) and `calcCredits` prices it via `AIModelItem.CacheWritePricePerMillion` (a column that existed since this design's first cut but was never read) — this was a real gap, not scope creep, since a live Anthropic Messages client makes it immediately user-visible. `reasoning_tokens` is a new, purely informational `AIGatewayAuditLog` column (reasoning tokens are already a subset of `completion_tokens` for pricing, matching OpenAI's own usage shape).
- **Scoped out** (round 1): Responses API `previous_response_id` chaining and `store:true` are rejected outright (400) rather than faked — the gateway has no server-side Responses-state persistence, resolving this design's own open question as "not supported" instead of leaving it ambiguous. Responses/Anthropic streaming event coverage is the commonly-consumed subset, not full spec parity with every event type either API defines. Console UI for `adapter_config`/bedrock credentials remains API-only.

### Round 2: Bedrock Titan/Llama/Mistral/Nova, Responses `previous_response_id`/`store`

Closes the two gaps round 1 explicitly deferred.

- **Bedrock family dispatch**: `bedrockModelFamily(modelID string)` (substring match — robust to a cross-region inference-profile prefix like `us.`/`eu.` in front of the vendor segment, without needing to parse it out) drives three dispatch points — `buildBedrockRequest` (body construction), `bedrockToOpenAIResponse` (sync response parsing), `translateBedrockStream` (streaming) — each a `switch` over five families. Claude's existing code paths are extracted into `buildClaudeBedrockRequestBody`/`translateClaudeBedrockStream` **unchanged**, so the well-tested Claude behavior is untouched; the four new families each live in their own file (`protocol_bedrock_titan.go`/`_llama.go`/`_mistral.go`/`_nova.go`), matching the one-file-per-dialect convention (`protocol_gemini.go`). A shared `bedrockNextChunk` helper (unwrap the AWS event-stream frame + SDK-level `{"bytes":base64(...)}` envelope, common to all five families) and `newBedrockOpenAIChunkWriter`/`buildOpenAIChatResponse` (the four new families all reduce to "flat text delta + finish reason + token counts," unlike Claude/Gemini's richer event-typed state machines) avoid four-way duplication.
- **Wire shapes were verified against AWS's official Bedrock documentation via Context7** (`docs.aws.amazon.com`), not training-data recall alone, given how easy it is to misremember a field name like `outputText` vs `output_text`. One exception: Nova's *native* `InvokeModelWithResponseStream` chunk event names (`contentBlockDelta`/`messageStop`/`metadata`) are confirmed for the Converse API and the native invoke request/sync-response shapes, but not independently confirmed for native-invoke *streaming* specifically — documented in `protocol_bedrock_nova.go`'s doc comment as a best-effort assumption, since no AWS account is available in this environment to verify live.
- **Prompt flattening for the two raw-completion families**: Llama gets the exact `<|begin_of_text|><|start_header_id|>{role}<|end_header_id|>...` chat template AWS's own Llama 3 InvokeModel example uses; Mistral gets `<s>[INST] u [/INST] a</s>[INST] u2 [/INST]` (system folded into the first user turn — Mistral's raw invoke format has no system role). Titan gets the `User: .../Bot: ...` convention Titan's own docs recommend for conversational use. Nova needs no flattening — its native invoke request is already messages-based, mapping 1:1 from the OpenAI message list.
- **Mistral's text-completion invoke response reports no token usage at all** (unlike Titan/Llama/Nova) — completion tokens are estimated from output character length (same rough heuristic `BillingManager.estimateMicro` already uses elsewhere for pre-freeze pricing); prompt tokens are left at 0 since the original request text isn't available at response-parse time. A documented, honest gap rather than a fabricated total.
- **Scope cuts, four new families**: text-only — no tool/function calling (three of the four have no native tool-calling primitive in their raw invoke API at all; Nova does, via its own `toolConfig`, but supporting it only for Nova would be asymmetric) and no multimodal content, matching the existing Claude adapter's own posture. Scoped to the classic/text-completion invoke shape per family (e.g. Mistral's newer chat-completion invoke mode, seen on some newer Mistral models on Bedrock, is a documented future addition, not this round).
- **Responses API state**: `AIResponseState` (`ai_gateway_responses`, additive) stores the full OpenAI-shape message history for one turn — the messages actually sent upstream plus the resulting assistant turn — keyed by the response ID actually returned to the client. That ID is now minted up front in `ProxyResponses` (`"resp_" + generateRequestID()`) for **both** streaming and non-streaming, replacing the previous `"resp_"+upstreamChatID` (non-stream) / `"resp_"+requestedModel` (streaming — not unique across requests for the same model, a latent bug for any persistence, fixed regardless of whether `store` is set this time). `previous_response_id` resolution is scoped to the requesting virtual key — a stored conversation can only be continued by the same key that created it, closing off cross-tenant enumeration; not-found/expired/wrong-key all return the identical `PREVIOUS_RESPONSE_NOT_FOUND` error so a client can't distinguish them. `internal/biz/responses_state.go`'s `StartResponsesStateSweeper` (ticker-driven, mirrors `StartQuotaReleaseSweeper`'s shape) is the only lifecycle control — TTL-only, `conf.ai.responses_state_ttl_hours` (default 24h), no admin/console visibility, same posture as the response cache's TTL-only invalidation (D07).
- **Scoped out** (round 2): stored Responses state is plaintext JSON — no dedicated encryption (audit-body encryption is a separate, narrower feature not extended to this table). No admin/console visibility into stored conversations.
