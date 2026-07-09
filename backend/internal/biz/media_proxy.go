package biz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"

	"github.com/opscenter/ai-gateway/internal/biz/guardrail"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

// Multimodal media adapters, phase 1 (docs/superpowers/specs/2026-07-09-
// multimodal-media-adapters-design.md): POST /ai/v1/images/generations,
// /ai/v1/audio/speech, /ai/v1/audio/transcriptions. Identity passthrough to
// openai_compatible providers only — no dialect translation exists yet for
// these three endpoints, so resolveMediaModel rejects any candidate whose
// provider isn't openai_compatible before an upstream call is ever attempted.
// No billing in this phase: request-count-shaped quota (a dedicated
// per-modality dimension, mirroring QuotaDimToolCall) and audit only.

const (
	mediaMaxReqBody  = 20 << 20 // 20 MiB ceiling on one request (audio file uploads are the largest case)
	mediaMaxRespBody = 50 << 20 // 50 MiB ceiling on a buffered (non-streamed) response body
)

type imageGenerationRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type audioSpeechRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type audioTranscriptionResponse struct {
	Text string `json:"text"`
}

func mediaAPIError(w http.ResponseWriter, err error) {
	se := kerrors.FromError(err)
	code := int(se.Code)
	if code < 100 || code > 599 {
		code = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	b, _ := json.Marshal(map[string]any{
		"error": map[string]string{"message": se.Message, "type": "gateway_error", "code": se.Reason},
	})
	w.Write(b)
}

func readBoundedBody(r io.Reader, max int64) (body []byte, tooLarge bool, err error) {
	body, err = io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > max {
		return nil, true, nil
	}
	return body, false, nil
}

// replaceJSONStringField rewrites one top-level string field in a JSON body,
// leaving every other field untouched. Used for guardrail redaction
// (prompt/input) and, together with replaceModelInBody, for per-candidate
// model substitution on a mapping's fallback chain.
func replaceJSONStringField(body []byte, field, value string) []byte {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	req[field] = value
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

// resolveMediaModel resolves requestedModel to a real (model, provider) pair
// via the existing exact-match model-mapping/allowlist resolver, then
// verifies an AIModelItem row exists for that pair with the expected
// modality and that the provider is openai_compatible — phase 1 has no
// dialect translation for these endpoints, so anything else would misbuild
// the upstream request rather than fail cleanly.
func (uc *GatewayUseCase) resolveMediaModel(ctx context.Context, key *model.AIVirtualKey, requestedModel, wantModelType string) (realModel string, providerID uint, mappingActive bool, err error) {
	if strings.TrimSpace(requestedModel) == "" {
		return "", 0, false, ErrMediaModelRequired
	}
	realModel, providerID, mappingActive, rerr := uc.resolveExactTargetModel(ctx, key, requestedModel)
	if rerr != nil {
		return "", 0, false, ErrMediaModelNotFound
	}
	if !uc.mediaCandidateQualifies(ctx, providerID, realModel, wantModelType) {
		return "", 0, false, ErrMediaModelNotFound
	}
	var provider model.AIProvider
	if derr := uc.db.WithContext(ctx).Select("provider_type").First(&provider, providerID).Error; derr != nil {
		return "", 0, false, ErrMediaModelNotFound
	}
	if provider.ProviderType != model.ProviderTypeOpenAICompatible {
		return "", 0, false, ErrMediaProviderUnsupported
	}
	return realModel, providerID, mappingActive, nil
}

// mediaCandidateQualifies checks that (providerID, modelName) is a
// catalogued, enabled AIModelItem of the expected modality. A mapping or
// fallback-chain entry pointing at a model with no matching catalog row (or
// the wrong ModelType) is treated as if it didn't resolve at all.
func (uc *GatewayUseCase) mediaCandidateQualifies(ctx context.Context, providerID uint, modelName, wantModelType string) bool {
	var item model.AIModelItem
	if err := uc.db.WithContext(ctx).
		Where("provider_id = ? AND name = ? AND is_enabled = ?", providerID, modelName, true).
		First(&item).Error; err != nil {
		return false
	}
	return item.ModelType == wantModelType
}

// mediaCandidates builds the ordered failover list exactly like ProxyRequest
// does (docs/design/01-routing-and-lb.md: a mapping is an instruction, not a
// suggestion — only its own explicit fallback chain applies; an unmapped
// request fans out across the key's routing strategy), then drops any
// candidate that isn't an enabled, correctly-typed, openai_compatible model —
// phase 1's dialect-unaware posture.
func (uc *GatewayUseCase) mediaCandidates(ctx context.Context, key *model.AIVirtualKey, requestedModel, realModel string, providerID uint, mappingActive bool, wantModelType string) []RouteCandidate {
	var candidates []RouteCandidate
	if mappingActive || uc.router == nil {
		candidates = []RouteCandidate{{ProviderID: providerID, Model: realModel}}
		if mappingActive && uc.router != nil {
			candidates = append(candidates, uc.mappingFallbackCandidates(ctx, key.ID, key.ProviderID, requestedModel)...)
		}
	} else {
		strategy := key.RoutingStrategy
		if strategy == "" {
			strategy = StrategyWeighted
		}
		candidates = uc.router.Candidates(ctx, realModel, providerID, strategy)
	}
	if len(candidates) > maxUpstreamAttempts {
		candidates = candidates[:maxUpstreamAttempts]
	}
	qualified := candidates[:0]
	for _, c := range candidates {
		var provider model.AIProvider
		if uc.db.WithContext(ctx).Select("provider_type").First(&provider, c.ProviderID).Error != nil {
			continue
		}
		if provider.ProviderType != model.ProviderTypeOpenAICompatible {
			continue
		}
		if !uc.mediaCandidateQualifies(ctx, c.ProviderID, c.Model, wantModelType) {
			continue
		}
		qualified = append(qualified, c)
	}
	return qualified
}

// buildMediaUpstreamRequest is buildUpstreamRequest's openai_compatible
// (identity) branch with one difference: an explicit content-type, needed
// because audio/transcriptions forwards a multipart/form-data body rather
// than JSON. Phase 1 only ever calls this for openai_compatible providers
// (mediaCandidates already filtered out everything else), so the other
// dialect branches buildUpstreamRequest carries are irrelevant here.
func buildMediaUpstreamRequest(ctx context.Context, entry *providerEntry, method, openAIPath string, body []byte, contentType string) (*http.Request, error) {
	upstreamPath := rewriteOpenAIPathForProvider(openAIPath, entry.provider)
	req, err := http.NewRequestWithContext(ctx, method, entry.provider.BaseURL+upstreamPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+entry.apiKey)
	return req, nil
}

// forwardMediaRequest tries each candidate in order (breaker-aware, like
// ProxyRequest's attempt loop) until one returns a non-retryable status or
// the list is exhausted. setModel rebuilds the outbound body/content-type
// for a given candidate's model name — a no-op substitution for the common
// case (candidate model == already-resolved real model), a real rewrite only
// when a fallback-chain candidate names a different model.
func (uc *GatewayUseCase) forwardMediaRequest(ctx context.Context, candidates []RouteCandidate, method, openAIPath string, setModel func(modelName string) ([]byte, string, error)) (*http.Response, uint, error) {
	client := newProxyClient()
	var lastErr error
	for i, cand := range candidates {
		if uc.router != nil && !uc.router.TryPass(ctx, cand.ProviderID) {
			lastErr = fmt.Errorf("provider %d circuit open", cand.ProviderID)
			continue
		}
		entry, perr := uc.loadProviderDirect(ctx, cand.ProviderID)
		if perr != nil {
			lastErr = perr
			continue
		}
		attemptBody, contentType, serr := setModel(cand.Model)
		if serr != nil {
			lastErr = serr
			continue
		}
		upstreamReq, berr := buildMediaUpstreamRequest(ctx, entry, method, openAIPath, attemptBody, contentType)
		if berr != nil {
			lastErr = berr
			continue
		}
		attemptResp, rerr := client.Do(upstreamReq)
		if rerr != nil {
			if uc.router != nil {
				uc.router.ReportResult(ctx, cand.ProviderID, AttemptRetryableError)
			}
			lastErr = rerr
			continue
		}
		if IsRetryableStatus(attemptResp.StatusCode) && i < len(candidates)-1 {
			attemptResp.Body.Close()
			if uc.router != nil {
				uc.router.ReportResult(ctx, cand.ProviderID, AttemptRetryableError)
			}
			lastErr = fmt.Errorf("upstream returned status %d", attemptResp.StatusCode)
			continue
		}
		if uc.router != nil {
			if IsRetryableStatus(attemptResp.StatusCode) {
				uc.router.ReportResult(ctx, cand.ProviderID, AttemptRetryableError)
			} else {
				uc.router.ReportResult(ctx, cand.ProviderID, AttemptSuccess)
			}
		}
		return attemptResp, cand.ProviderID, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no upstream candidate available")
	}
	return nil, 0, lastErr
}

func writeMediaUpstreamFailure(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	w.Write([]byte(`{"error":{"message":"all upstream providers failed","type":"gateway_error","code":"MEDIA_UPSTREAM_FAILED"}}`))
}

// copyUpstreamStream relays a binary response (audio/speech) to the client
// without buffering — matching the streaming commit rule everywhere else in
// this package: headers are already correct, so no failover is possible past
// this point regardless.
func copyUpstreamStream(w http.ResponseWriter, resp *http.Response) {
	for k, vv := range resp.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// -----------------------------------------------------------------------------
// POST /ai/v1/images/generations
// -----------------------------------------------------------------------------

func (uc *GatewayUseCase) HandleImagesGenerations(ctx context.Context, key *model.AIVirtualKey, w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	clientIP := ClientIPFromRequest(r)

	body, tooLarge, rerr := readBoundedBody(r.Body, mediaMaxReqBody)
	if rerr != nil || tooLarge {
		mediaAPIError(w, kerrors.BadRequest("MEDIA_BODY_INVALID", "failed to read request body or body too large"))
		return
	}
	var req imageGenerationRequest
	if json.Unmarshal(body, &req) != nil {
		mediaAPIError(w, kerrors.BadRequest("MEDIA_BODY_INVALID", "invalid JSON body"))
		return
	}

	realModel, providerID, mappingActive, rerr := uc.resolveMediaModel(ctx, key, req.Model, model.ModelTypeImage)
	if rerr != nil {
		uc.writeAuditLog(ctx, key, key.ProviderID, req.Model, body, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), int(kerrors.FromError(rerr).Code), rerr.Error(), false, clientIP, "image", 0, 0, "")
		mediaAPIError(w, rerr)
		return
	}

	if qerr := uc.quota.CheckAndReserveImageCall(ctx, key); qerr != nil {
		uc.writeAuditLog(ctx, key, providerID, realModel, body, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusTooManyRequests, qerr.Error(), false, clientIP, "image", 0, 0, "", req.Model)
		mediaAPIError(w, kerrors.New(http.StatusTooManyRequests, "IMAGE_CALL_QUOTA_EXCEEDED", qerr.Error()))
		return
	}

	sendBody := replaceModelInBody(body, realModel)
	finalPrompt, blocked, findingTypes := uc.guardrailScanText(ctx, key, guardrail.DirectionInbound, req.Prompt)
	if blocked {
		msg := "prompt blocked by guardrail policy types=" + findingTypes
		uc.writeAuditLog(ctx, key, providerID, realModel, body, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusForbidden, msg, true, clientIP, "image", 0, 0, "", req.Model)
		mediaAPIError(w, kerrors.Forbidden("GUARDRAIL_BLOCKED", "prompt blocked by guardrail policy"))
		return
	}
	if finalPrompt != req.Prompt {
		sendBody = replaceJSONStringField(sendBody, "prompt", finalPrompt)
	}

	candidates := uc.mediaCandidates(ctx, key, req.Model, realModel, providerID, mappingActive, model.ModelTypeImage)
	if len(candidates) == 0 {
		uc.writeAuditLog(ctx, key, providerID, realModel, body, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusBadRequest, "no qualifying candidate provider", false, clientIP, "image", 0, 0, "", req.Model)
		mediaAPIError(w, ErrMediaProviderUnsupported)
		return
	}

	resp, actualProviderID, ferr := uc.forwardMediaRequest(ctx, candidates, http.MethodPost, "/images/generations",
		func(modelName string) ([]byte, string, error) {
			if modelName == realModel {
				return sendBody, "application/json", nil
			}
			return replaceModelInBody(sendBody, modelName), "application/json", nil
		})
	if ferr != nil {
		uc.writeAuditLog(ctx, key, providerID, realModel, body, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusBadGateway, ferr.Error(), false, clientIP, "image", 0, 0, "", req.Model)
		writeMediaUpstreamFailure(w)
		return
	}
	defer resp.Body.Close()
	respBody, _, _ := readBoundedBody(resp.Body, mediaMaxRespBody)

	uc.writeAuditLog(ctx, key, actualProviderID, realModel, body, respBody, 0, 0, 0, 0,
		time.Since(startTime).Milliseconds(), resp.StatusCode, "", false, clientIP, "image", 0, 0, "", req.Model)
	copyUpstreamJSON(w, resp, respBody)
}

// -----------------------------------------------------------------------------
// POST /ai/v1/audio/speech (TTS)
// -----------------------------------------------------------------------------

func (uc *GatewayUseCase) HandleAudioSpeech(ctx context.Context, key *model.AIVirtualKey, w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	clientIP := ClientIPFromRequest(r)

	body, tooLarge, rerr := readBoundedBody(r.Body, mediaMaxReqBody)
	if rerr != nil || tooLarge {
		mediaAPIError(w, kerrors.BadRequest("MEDIA_BODY_INVALID", "failed to read request body or body too large"))
		return
	}
	var req audioSpeechRequest
	if json.Unmarshal(body, &req) != nil {
		mediaAPIError(w, kerrors.BadRequest("MEDIA_BODY_INVALID", "invalid JSON body"))
		return
	}

	realModel, providerID, mappingActive, rerr := uc.resolveMediaModel(ctx, key, req.Model, model.ModelTypeTTS)
	if rerr != nil {
		uc.writeAuditLog(ctx, key, key.ProviderID, req.Model, body, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), int(kerrors.FromError(rerr).Code), rerr.Error(), false, clientIP, "audio", 0, 0, "")
		mediaAPIError(w, rerr)
		return
	}

	if qerr := uc.quota.CheckAndReserveAudioCall(ctx, key); qerr != nil {
		uc.writeAuditLog(ctx, key, providerID, realModel, body, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusTooManyRequests, qerr.Error(), false, clientIP, "audio", 0, 0, "", req.Model)
		mediaAPIError(w, kerrors.New(http.StatusTooManyRequests, "AUDIO_CALL_QUOTA_EXCEEDED", qerr.Error()))
		return
	}

	sendBody := replaceModelInBody(body, realModel)
	finalInput, blocked, findingTypes := uc.guardrailScanText(ctx, key, guardrail.DirectionInbound, req.Input)
	if blocked {
		msg := "input text blocked by guardrail policy types=" + findingTypes
		uc.writeAuditLog(ctx, key, providerID, realModel, body, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusForbidden, msg, true, clientIP, "audio", 0, 0, "", req.Model)
		mediaAPIError(w, kerrors.Forbidden("GUARDRAIL_BLOCKED", "input text blocked by guardrail policy"))
		return
	}
	if finalInput != req.Input {
		sendBody = replaceJSONStringField(sendBody, "input", finalInput)
	}

	candidates := uc.mediaCandidates(ctx, key, req.Model, realModel, providerID, mappingActive, model.ModelTypeTTS)
	if len(candidates) == 0 {
		uc.writeAuditLog(ctx, key, providerID, realModel, body, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusBadRequest, "no qualifying candidate provider", false, clientIP, "audio", 0, 0, "", req.Model)
		mediaAPIError(w, ErrMediaProviderUnsupported)
		return
	}

	resp, actualProviderID, ferr := uc.forwardMediaRequest(ctx, candidates, http.MethodPost, "/audio/speech",
		func(modelName string) ([]byte, string, error) {
			if modelName == realModel {
				return sendBody, "application/json", nil
			}
			return replaceModelInBody(sendBody, modelName), "application/json", nil
		})
	if ferr != nil {
		uc.writeAuditLog(ctx, key, providerID, realModel, body, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusBadGateway, ferr.Error(), false, clientIP, "audio", 0, 0, "", req.Model)
		writeMediaUpstreamFailure(w)
		return
	}
	defer resp.Body.Close()

	// Binary audio: not logged (bodies are meant for text; see design doc's
	// "not scanned, not logged" call for binary media bytes), streamed
	// straight through per the streaming commit rule.
	uc.writeAuditLog(ctx, key, actualProviderID, realModel, body, nil, 0, 0, 0, 0,
		time.Since(startTime).Milliseconds(), resp.StatusCode, "", false, clientIP, "audio", 0, 0, "", req.Model)
	copyUpstreamStream(w, resp)
}

// -----------------------------------------------------------------------------
// POST /ai/v1/audio/transcriptions (ASR)
// -----------------------------------------------------------------------------

func extractMultipartModelField(body []byte, contentType string) (string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", err
	}
	boundary := params["boundary"]
	if boundary == "" {
		return "", fmt.Errorf("missing multipart boundary")
	}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			return "", nil
		}
		if perr != nil {
			return "", perr
		}
		if part.FormName() == "model" {
			val, _ := io.ReadAll(io.LimitReader(part, 256))
			return string(val), nil
		}
	}
}

// rewriteMultipartModelField re-encodes the multipart body with a new
// "model" field value, streaming every other field/file through unchanged.
// Only called when a fallback-chain candidate names a different real model
// than the one the client requested — the common case never pays this cost.
func rewriteMultipartModelField(body []byte, contentType, newModel string) ([]byte, string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, "", err
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			return nil, "", perr
		}
		if part.FormName() == "model" {
			io.Copy(io.Discard, part) //nolint:errcheck
			if werr := mw.WriteField("model", newModel); werr != nil {
				return nil, "", werr
			}
			continue
		}
		if part.FileName() != "" {
			fw, cerr := mw.CreateFormFile(part.FormName(), part.FileName())
			if cerr != nil {
				return nil, "", cerr
			}
			if _, cerr := io.Copy(fw, part); cerr != nil {
				return nil, "", cerr
			}
		} else {
			val, _ := io.ReadAll(part)
			if werr := mw.WriteField(part.FormName(), string(val)); werr != nil {
				return nil, "", werr
			}
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

func (uc *GatewayUseCase) HandleAudioTranscriptions(ctx context.Context, key *model.AIVirtualKey, w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	clientIP := ClientIPFromRequest(r)
	contentType := r.Header.Get("Content-Type")

	body, tooLarge, rerr := readBoundedBody(r.Body, mediaMaxReqBody)
	if rerr != nil || tooLarge {
		mediaAPIError(w, kerrors.BadRequest("MEDIA_BODY_INVALID", "failed to read request body or body too large"))
		return
	}
	requestedModel, merr := extractMultipartModelField(body, contentType)
	if merr != nil {
		mediaAPIError(w, kerrors.BadRequest("MEDIA_BODY_INVALID", "invalid multipart/form-data body: "+merr.Error()))
		return
	}

	realModel, providerID, mappingActive, rerr := uc.resolveMediaModel(ctx, key, requestedModel, model.ModelTypeASR)
	if rerr != nil {
		uc.writeAuditLog(ctx, key, key.ProviderID, requestedModel, nil, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), int(kerrors.FromError(rerr).Code), rerr.Error(), false, clientIP, "audio", 0, 0, "")
		mediaAPIError(w, rerr)
		return
	}

	if qerr := uc.quota.CheckAndReserveAudioCall(ctx, key); qerr != nil {
		uc.writeAuditLog(ctx, key, providerID, realModel, nil, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusTooManyRequests, qerr.Error(), false, clientIP, "audio", 0, 0, "", requestedModel)
		mediaAPIError(w, kerrors.New(http.StatusTooManyRequests, "AUDIO_CALL_QUOTA_EXCEEDED", qerr.Error()))
		return
	}

	// No inbound guardrail: the payload is an audio file, not scannable text.
	candidates := uc.mediaCandidates(ctx, key, requestedModel, realModel, providerID, mappingActive, model.ModelTypeASR)
	if len(candidates) == 0 {
		uc.writeAuditLog(ctx, key, providerID, realModel, nil, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusBadRequest, "no qualifying candidate provider", false, clientIP, "audio", 0, 0, "", requestedModel)
		mediaAPIError(w, ErrMediaProviderUnsupported)
		return
	}

	resp, actualProviderID, ferr := uc.forwardMediaRequest(ctx, candidates, http.MethodPost, "/audio/transcriptions",
		func(modelName string) ([]byte, string, error) {
			if modelName == realModel {
				return body, contentType, nil
			}
			return rewriteMultipartModelField(body, contentType, modelName)
		})
	if ferr != nil {
		uc.writeAuditLog(ctx, key, providerID, realModel, nil, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusBadGateway, ferr.Error(), false, clientIP, "audio", 0, 0, "", requestedModel)
		writeMediaUpstreamFailure(w)
		return
	}
	defer resp.Body.Close()
	respBody, _, _ := readBoundedBody(resp.Body, mediaMaxRespBody)

	// Outbound guardrail on the transcript text only (docs/superpowers/specs/
	// 2026-07-09-multimodal-media-adapters-design.md) — a block replaces the
	// response instead of returning the transcript; a redact rewrites "text"
	// in place. Only applies to a JSON, successful response.
	finalRespBody := respBody
	if resp.StatusCode < 300 && strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		var parsed audioTranscriptionResponse
		if json.Unmarshal(respBody, &parsed) == nil && parsed.Text != "" {
			finalText, blocked, findingTypes := uc.guardrailScanText(ctx, key, guardrail.DirectionOutbound, parsed.Text)
			if blocked {
				uc.writeAuditLog(ctx, key, actualProviderID, realModel, nil, respBody, 0, 0, 0, 0,
					time.Since(startTime).Milliseconds(), http.StatusForbidden, "transcript blocked by guardrail policy types="+findingTypes, true, clientIP, "audio", 0, 0, "", requestedModel)
				mediaAPIError(w, kerrors.Forbidden("GUARDRAIL_BLOCKED", "transcript blocked by guardrail policy"))
				return
			}
			if finalText != parsed.Text {
				finalRespBody = replaceJSONStringField(respBody, "text", finalText)
			}
		}
	}

	uc.writeAuditLog(ctx, key, actualProviderID, realModel, nil, finalRespBody, 0, 0, 0, 0,
		time.Since(startTime).Milliseconds(), resp.StatusCode, "", false, clientIP, "audio", 0, 0, "", requestedModel)
	copyUpstreamJSON(w, resp, finalRespBody)
}
