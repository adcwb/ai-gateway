# Video generation (phase 2 of the multimodal media adapters project) — design

## Context

Phase 1 (`docs/superpowers/specs/2026-07-09-multimodal-media-adapters-design.md`, shipped) added synchronous image generation and audio TTS/ASR endpoints: identity passthrough to `openai_compatible` providers only, aligned with OpenAI's own wire shapes, governed by the same virtual-key auth/quota/guardrail/audit machinery as chat traffic. No billing in that phase.

Video generation is structurally different: every real vendor (Sora, Kling, Runway, Veo) is **async** — submit a job, poll its status, download the result once ready. This is architecturally much closer to the existing Batch/Files proxy (`biz/batch_proxy.go` + `biz/batch_settlement.go`, `AIProxyFile`/`AIBatchJob` shadow tables) than to phase 1's synchronous request/response endpoints. That's why phase 1's design explicitly carved video out into its own phase.

**Key simplification versus Batch/Files**: `StartBatchSettlementPoller` exists solely because batch billing has to settle even if the client never polls back for status. Video generation carries no billing in this phase (same non-billing posture as phase 1's image/audio) — so there is **no background poller**. Every video endpoint is a live passthrough to the upstream provider; the client drives its own poll loop, and the gateway just relays each call.

## Goal

Add `POST /ai/v1/videos` (submit), `GET /ai/v1/videos/{id}` (status), `GET /ai/v1/videos/{id}/content` (download result), `DELETE /ai/v1/videos/{id}` (cancel), and `GET /ai/v1/videos` (list this key's own jobs) — aligned with OpenAI's own `/v1/videos` API shape, `openai_compatible` providers only, no dialect translation.

## Non-goals

- No non-`openai_compatible` outbound dialect translation (same restriction as phase 1's image/audio).
- No billing/settlement — request-count-shaped quota and audit only, exactly like phase 1.
- No background poller of any kind — this is a pure live-passthrough proxy; a client that never polls simply never learns its job finished (their problem, not the gateway's — no state to reconcile since nothing is billed).
- No console UI for job visibility — API-only, consistent with Batch/Files being API-only today.
- No changes to `AIModelMapping`/routing beyond adding the `video` `ModelType` value — the modality-aware routing extension is phase 3.

## Backend design

### Routes

Registered before the `/ai/v1/` catch-all, same placement pattern as Files/Batches:

```go
mux.Handle("POST /ai/v1/videos", tracing.Middleware("openai-videos", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.VideosCreate))))
mux.Handle("GET /ai/v1/videos", tracing.Middleware("openai-videos", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.VideosList))))
mux.Handle("GET /ai/v1/videos/{id}", tracing.Middleware("openai-videos", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.VideosGet))))
mux.Handle("GET /ai/v1/videos/{id}/content", tracing.Middleware("openai-videos", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.VideosContent))))
mux.Handle("DELETE /ai/v1/videos/{id}", tracing.Middleware("openai-videos", auth.ProxyMiddleware(http.HandlerFunc(gwSvc.VideosDelete))))
```

### New file: `biz/video_proxy.go`

Sibling to `media_proxy.go` and `batch_proxy.go`, reusing pieces of both:

**`HandleVideosCreate`** (submit): follows `HandleImagesGenerations`'s exact shape —
1. Read + bound body, extract `model`/`prompt`.
2. `resolveMediaModel(ctx, key, req.Model, model.ModelTypeVideo)` — same modality-filtered, `openai_compatible`-only resolution phase 1 already built; reused as-is (no new resolution code).
3. `QuotaManager.CheckAndReserveVideoCall` — new dedicated dimension (`QuotaDimVideoCall`, `HourlyVideoCallQuota`), mirroring `CheckAndReserveImageCall`/`CheckAndReserveAudioCall` exactly. **Submission only** — polling status, downloading content, and deleting a job never touch this quota (they're not the expensive action; mirrors how `ProxyFilesGet`/batch status calls never re-check `CheckAndReserveToolCall`-style budgets either).
4. Guardrail-scan the `prompt` field (inbound only — there is no outbound scan, since the result is a binary video, same as image/audio's binary outputs).
5. Forward via `mediaCandidates`/`forwardMediaRequest` (phase 1's routing/failover machinery, reused unchanged) to `{BaseURL}/videos`.
6. On a successful create, **save a shadow row** (`AIVideoJob`) keyed by the provider's returned job id — required because every follow-up call (`GET`/`DELETE` by id) carries no `model` field, only a path parameter, so the provider has to be remembered exactly the way `AIProxyFile`/`AIBatchJob` already solve this for Files/Batches.
7. Audit row (`protocol="video"`, no billing fields populated).

**`HandleVideosGet`/`HandleVideosContent`/`HandleVideosDelete`** (mirror `ProxyFilesGet`/`ProxyFilesGet`-with-`/content`/a delete variant in `batch_proxy.go`, sharing their `forwardRaw`+shadow-row-lookup pattern):
1. Load the `AIVideoJob` shadow row by `{id}`, **scoped to the requesting virtual key** (`WHERE id = ? AND virtual_key_id = ?`) — a job created by one key must not be pollable/downloadable/deletable by another. Not-found and wrong-key both return the same 404 (mirrors `loadResponseState`'s no-enumeration-signal posture in the Responses API).
2. `loadProviderDirect` on the shadow row's `ProviderID`, then live-passthrough: `/content` streams the binary response like `copyUpstreamStream` (audio/speech's pattern); status/delete responses are small JSON, copied like `copyUpstreamJSON`.
3. Audit row per call (`protocol="video"`), no quota reservation.

**`HandleVideosList`**: lists the requesting key's own `AIVideoJob` rows from the local shadow table — **not** a live upstream forward. This deliberately diverges from `ProxyFilesList`/`ProxyBatchesList`, which forward live to one named provider via the `X-AIGW-Provider` header: that works there because the caller already knows which provider's list they want. Video jobs are created via model-mapping and can land on different providers across calls (fallback chains, multiple video-capable providers), so there's no single upstream to forward a "list" to — a local aggregate over the key's own shadow rows is the only view that makes sense.

### Data model (additive)

```go
// AIVideoJob mirrors AIProxyFile's shape — shadow bookkeeping only, no
// video bytes stored, no SettledAt (no billing to settle in this phase).
type AIVideoJob struct {
	ID           string         `gorm:"column:id;primaryKey;type:varchar(128)"` // provider's job id
	VirtualKeyID uint           `gorm:"not null;index"`
	ProviderID   uint           `gorm:"not null;index"`
	Model        string         `gorm:"type:varchar(128)"`
	RawUpstream  datatypes.JSON `gorm:"type:json"` // the create response, for reference
	CreatedAt    time.Time
}
```

`AIModelItem.ModelType` gains `model.ModelTypeVideo = "video"` (constant added alongside the phase 1 three). `AIVirtualKey` gains `HourlyVideoCallQuota int64` (additive, 0 = unlimited). `QuotaDimVideoCall` constant alongside `QuotaDimImageCall`/`QuotaDimAudioCall`.

## Error handling

New sentinels in `biz/errors.go`, matching the phase 1 convention:

```go
ErrVideoJobNotFound     = kerrors.NotFound("VIDEO_JOB_NOT_FOUND", "video job not found")
ErrVideoCallQuotaExceeded = kerrors.New(429, "VIDEO_CALL_QUOTA_EXCEEDED", "hourly video call quota exceeded")
```

`resolveMediaModel`/`ErrMediaModelNotFound`/`ErrMediaProviderUnsupported` are reused unchanged for the submit path (same modality-filtering, same provider-type guard).

## Frontend

Only the Models & Pricing page's `modelType` selector gains one more option: `video` (视频生成), following the exact pattern phase 1 added `image`/`tts`/`asr` with. No other console changes.

## Testing

All offline, following phase 1's `media_proxy_test.go` conventions:

- `biz/video_proxy_test.go`, table-driven, `httptest.Server` upstream:
  - Submit: modality-filtered resolution reused correctly (a `tts` model is invisible to `/videos`); quota reserve/exhaust on `HourlyVideoCallQuota`, independent of image/audio quotas; guardrail block/redact on `prompt`; shadow row created on success with the correct `provider_id`/`virtual_key_id`.
  - Status/content/delete: correct provider resolved from the shadow row; a job created by key A returns 404 for key B (cross-key access denied); content download streams binary passthrough unbuffered; none of the three consume `HourlyVideoCallQuota`.
  - List: returns only the requesting key's own jobs.
  - Audit: one row per call, `protocol="video"`, no billing fields populated.
