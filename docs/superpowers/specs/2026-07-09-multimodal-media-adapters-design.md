# Multimodal media adapters (image/audio) + model-mapping modality routing — design

## Context

Today the gateway's protocol adapter layer (`biz/protocol.go` + `biz/protocol_bedrock*.go` + inbound codecs for Anthropic Messages/Responses API) only covers **text-generation** shapes — chat/completions-style request/response, translated per outbound `provider_type` dialect. There is no inbound surface for image generation, audio (TTS/ASR), or video generation, and `AIModelMapping` routes purely by virtual model *name*, with no concept of modality/capability.

The product goal: let an operator combine best-of-breed vendors per modality behind one gateway — e.g. vendor A's LLM for text, vendor B's LVM for image/video, vendor C's LIM for audio — instead of being locked into one vendor's whole stack. This was evaluated against introducing an A2A (Agent2Agent) protocol; A2A solves autonomous cross-agent delegation, which is not this problem — this is single-request modality-based routing, solved by extending the existing protocol-adapter + model-mapping architecture instead.

This is a three-phase project (see root `CLAUDE.md`'s "Feature status" table for where each existing piece lives):

1. **Phase 1 (this spec, full detail):** image generation + audio (TTS/ASR) inbound endpoints, `openai_compatible`-only outbound (identity passthrough), no billing.
2. **Phase 2 (separate spec later):** video generation — async job/poll model, reusing the `batch_proxy.go`/`batch_settlement.go` shadow-row + poller pattern rather than the synchronous request/response pattern phase 1 uses.
3. **Phase 3 (separate spec later):** extend `AIModelMapping`/routing to be modality-aware so a single virtual key transparently fans text/image/audio/video traffic out to different vendors.

Only phase 1 is designed in full below. Phases 2 and 3 get their own brainstorm → spec → plan cycle once phase 1 ships.

## Goal (phase 1)

Add `POST /ai/v1/images/generations`, `POST /ai/v1/audio/speech`, `POST /ai/v1/audio/transcriptions` to the gateway, authenticated and quota/audit/guardrail-governed the same way model traffic is, forwarded identity-passthrough to `openai_compatible` providers only.

## Non-goals (phase 1)

- No non-`openai_compatible` outbound dialect translation for these three endpoints (no azure_openai/anthropic/bedrock/gemini image or audio adapter code).
- No billing/pricing for image or audio calls — request-count-shaped quota and audit only, no ledger/balance impact.
- No video generation (phase 2).
- No `AIModelMapping` modality-routing changes (phase 3) — phase 1 mapping resolution just filters candidate models by the new `ModelType` values.
- No Keys-page console UI for the two new quota fields (API-only by design, consistent with existing API-only gaps like bedrock credentials/`adapter_config` — see root `CLAUDE.md`'s Feature status table).
- No console Playground/test panel.

## Backend design

### Routes

Registered in `internal/server/http.go`, before the `/ai/v1/` catch-all (same placement pattern as `/ai/v1/models` and `/ai/v1/responses`):

```go
mux.Handle("POST /ai/v1/images/generations", tracing.Middleware("openai-images", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.ImagesGenerations))))
mux.Handle("POST /ai/v1/audio/speech", tracing.Middleware("openai-audio", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.AudioSpeech))))
mux.Handle("POST /ai/v1/audio/transcriptions", tracing.Middleware("openai-audio", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.AudioTranscriptions))))
```

All three go through the existing `middleware.VirtualKeyAuth.ProxyMiddleware` — same `sk-vk-*` key, same top-level request-count quota reservation every route already gets.

### New file: `biz/media_proxy.go`

A sibling to `mcp_proxy.go`, not a modification of `gateway.go`'s `ProxyRequest` — these endpoints have a materially different request shape (JSON with a `prompt`/`input` field, or `multipart/form-data` for transcriptions) and don't go through the OpenAI chat-completions IR at all, so they don't belong in the chat proxy path. They do reuse the same lower-level building blocks:

```go
func (uc *GatewayUseCase) ImagesGenerations(w http.ResponseWriter, r *http.Request)
func (uc *GatewayUseCase) AudioSpeech(w http.ResponseWriter, r *http.Request)
func (uc *GatewayUseCase) AudioTranscriptions(w http.ResponseWriter, r *http.Request)
```

Shared internal flow (`handleMediaRequest(ctx, key, modelType, openAIPath, extractPromptText, body) `):

1. **Extract `model` field.**
   - `images/generations` and `audio/speech`: JSON body, straightforward `json.Unmarshal` into a small struct with just `Model`/`Prompt`/`Input`.
   - `audio/transcriptions`: `multipart/form-data`. Parse just far enough to read the `model` field (`r.ParseMultipartForm` with a bounded memory limit) while preserving the original body bytes (buffer the request body first via `io.ReadAll` capped at a max-size constant, then re-wrap for both the form parse and the eventual upstream forward — mirrors how `mcp_proxy.go`'s `mcpMaxBody` ceiling works).
2. **Resolve target model.** Reuse `resolveModelMapping(ctx, keyID, keyProviderID, requestedModel)` and `router.Candidates(ctx, realModel, primaryProviderID, strategy)` exactly as the chat path does — these are already keyed by model name string, not by chat-specific body shape (`semantic_cache.go` already reuses `buildUpstreamRequest` the same way for `/embeddings`). Candidate `AIModelItem` rows are filtered to `ModelType == "image"` (for images/generations) or `ModelType == "tts"`/`"asr"` (for audio/speech, audio/transcriptions respectively) — a mapping to a model of the wrong modality is treated as unmapped.
3. **Provider-type guard.** Any candidate whose `AIProvider.ProviderType != ProviderTypeOpenAICompatible` is skipped with a warn log (dialect-unaware in phase 1); if that empties the candidate list, return `ErrMediaProviderUnsupported`.
4. **Quota.** New dimensions `QuotaDimImageCall`/`QuotaDimAudioCall` reserved via `QuotaManager.CheckAndReserveToolCall`-shaped Lua script (literally the same sliding-window script `QuotaDimToolCall` uses, parameterized by dimension) against `AIVirtualKey.HourlyImageCallQuota`/`HourlyAudioCallQuota`. Exhausted quota → `ErrImageCallQuotaExceeded`/`ErrAudioCallQuotaExceeded`, upstream never called — same "reject before forwarding" shape as MCP tool-call quota.
5. **Guardrail.** Only where the payload is actually text:
   - `images/generations`: `prompt` field → `resolvePIIPolicy` → `buildChainForPolicy` → `guardrail.Chain.Run`. Block/terminate short-circuits before the upstream call; redact rewrites `prompt` in the outbound JSON.
   - `audio/speech`: `input` field, same treatment.
   - `audio/transcriptions`: no inbound guardrail (audio bytes aren't scannable); after the upstream call, the response JSON's `text` field runs through the same chain (block ⇒ error out the response instead of returning the transcript; redact ⇒ rewrite `text` in the JSON before it reaches the client). Mirrors `applyOutboundGuardrail`'s post-hoc pattern, scoped to this one field instead of a chat message.
6. **Forward.** `buildUpstreamRequest(ctx, entry, http.MethodPost, openAIPath, body, false)` where `openAIPath` is `/images/generations`, `/audio/speech`, or `/audio/transcriptions` — identity passthrough (phase 1 has no dialect translation branch for these paths, so `buildUpstreamRequest`'s `openai_compatible` case is the only one exercised; hitting a non-openai_compatible branch here would misbehave, which is exactly why step 3 filters it out ahead of time). `audio/speech`'s response is binary audio — piped straight through (`io.Copy`) with the upstream's `Content-Type` preserved, no buffering.
7. **Audit.** Reuse `ai_gateway_audit_logs` (no new table): `Protocol` = `"image"` or `"audio"`, `Model` = the resolved real model name. No billing/ledger call — this phase's audit row is request/response-shape-only, matching what MCP tool calls already do minus the settlement step.

### Data model changes (all additive)

`internal/data/model/model_item.go` — no column change; extend the accepted `ModelType` values (validated in `biz/model_item.go`'s create/update) to include `image`, `tts`, `asr` alongside the existing `llm` default.

`internal/data/model/virtual_key.go` — two new columns:
```go
HourlyImageCallQuota uint `gorm:"column:hourly_image_call_quota;default:0"` // 0 = unlimited, same convention as HourlyToolCallQuota
HourlyAudioCallQuota uint `gorm:"column:hourly_audio_call_quota;default:0"`
```
Registered in `data.autoMigrate`. Per the GORM zero-value trap (root `CLAUDE.md`), any seed/test data relying on these being explicitly `0` must use `Update`, not rely on `Create`'s zero-value default.

`biz/errors.go` — new sentinels: `ErrMediaModelNotFound`, `ErrMediaProviderUnsupported`, `ErrImageCallQuotaExceeded`, `ErrAudioCallQuotaExceeded`.

`biz/dto/gateway.go` (or a new `dto/media.go`) — request/response passthrough DTOs only where needed for the thin JSON field extraction described in step 1 above (`Model`, `Prompt`, `Input`, and the transcription response's `Text`); the bulk of each body is forwarded as raw bytes, not fully modeled.

## Frontend design (phase 1 scope)

Only `frontend/src/pages/Models.tsx` (Models & Pricing page):

- Model create/edit form's `modelType` `<select>` gains three new options: `image` (图像生成), `tts` (语音合成), `asr` (语音识别), alongside the existing `llm`.
- List view's model-type badge/tag rendering extends to the three new values (same badge style already used for `llm`).
- `frontend/src/i18n.ts`: en+zh label strings for the three new `modelType` values, landed together per the bilingual-parity rule.
- `frontend/src/api/client.ts`: no new endpoint calls needed — `modelType` is already a plain string field on the existing model create/update request types; just widen any TS union/enum type if one exists.

Keys page, provider admin page, and a testing/playground UI are explicitly out of scope for phase 1 (see Non-goals).

## Error handling

New `kerrors` sentinels (`biz/errors.go`, matching existing `Reason` naming convention — `SCREAMING_SNAKE_CASE` of the var name sans `Err`):

```go
ErrMediaModelNotFound       = kerrors.NotFound("MEDIA_MODEL_NOT_FOUND", "no matching image/audio model for this key")
ErrMediaProviderUnsupported = kerrors.New(400, "MEDIA_PROVIDER_UNSUPPORTED", "resolved provider does not support this media endpoint in this gateway version")
ErrImageCallQuotaExceeded   = kerrors.New(429, "IMAGE_CALL_QUOTA_EXCEEDED", "hourly image call quota exceeded")
ErrAudioCallQuotaExceeded   = kerrors.New(429, "AUDIO_CALL_QUOTA_EXCEEDED", "hourly audio call quota exceeded")
```

Guardrail block/terminate reuses `guardrail.Chain`'s existing verdict shape (same as MCP tool-call governance) — rejected before any upstream call for inbound text, or the response is replaced with an error for the one outbound case (transcription text).

## Testing

All offline (miniredis + in-memory SQLite), following existing repo conventions:

- `biz/media_proxy_test.go`, table-driven:
  - Modality-filtered model resolution (a `tts` mapping is invisible to `images/generations` and vice versa).
  - Non-`openai_compatible` candidate is skipped; empty candidate list after filtering returns `ErrMediaProviderUnsupported`.
  - Quota reserve/exhaust for both new dimensions, independently (exhausting image quota doesn't block audio calls and vice versa).
  - Guardrail: block/redact on `prompt` (images), `input` (TTS), and post-hoc on transcription `text`.
  - `httptest.Server` stand-in upstream verifying passthrough fidelity (request bytes in, response bytes/content-type out unchanged) for all three endpoints, including the transcription multipart body arriving upstream byte-identical to what the client sent.
  - Multipart edge cases: missing `model` field, oversized body (over the max-size ceiling), malformed multipart.
  - Audit row assertions: `protocol`/`model` populated correctly, no ledger/billing calls made.
- Frontend: extend whatever existing Models.tsx test coverage exists to confirm the three new `modelType` options are selectable and persist through create/edit.

## Phase 2 / 3 preview (not designed yet)

- **Phase 2 (video):** async job submission/polling, new `AIProxyVideoJob`-shaped shadow table (mirrors `AIBatchJob`), a poller in `StartBackgroundWorkers`, its own quota dimension (`QuotaDimVideoCall`) and `ModelType` value (`video`). Gets its own spec before implementation.
- **Phase 3 (modality-aware model mapping):** `AIModelMapping` gains a way to resolve by (virtual key, modality, virtual name) rather than name alone, so one key can transparently mix vendors per modality; console `ModelMappings.tsx` gains a modality selector. Gets its own spec before implementation.
