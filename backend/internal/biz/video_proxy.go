package biz

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"gorm.io/datatypes"

	"github.com/adcwb/ai-gateway/internal/biz/guardrail"
	"github.com/adcwb/ai-gateway/internal/data/model"
)

// Video generation, phase 2 of the multimodal media adapters project
// (docs/superpowers/specs/2026-07-09-video-generation-phase2-design.md):
// POST /ai/v1/videos (submit), GET /ai/v1/videos/{id} (status),
// GET /ai/v1/videos/{id}/content (download), DELETE /ai/v1/videos/{id},
// GET /ai/v1/videos (list). Aligned with OpenAI's own /v1/videos shape,
// openai_compatible providers only — same posture as phase 1's image/audio.
//
// Unlike Batch/Files, there is no background settlement poller: this phase
// carries no billing, so every call after creation is a live passthrough to
// whichever provider the AIVideoJob shadow row remembers. Submission alone
// reuses phase 1's resolveMediaModel/mediaCandidates/forwardMediaRequest
// (model-mapping resolution + failover); status/content/delete resolve their
// provider from the shadow row instead (that call carries no model field,
// only a path parameter) and forward directly via forwardRaw, matching
// batch_proxy.go's ProxyFilesGet/ProxyFilesContent/ProxyFilesDelete pattern —
// no failover needed since the job is already pinned to one provider.

type videoCreateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// loadVideoJob loads a shadow row scoped to the requesting virtual key — a
// job created by one key must not be pollable/downloadable/deletable by
// another. Not-found and wrong-key both return the same error (no
// enumeration signal), mirroring loadResponseState's posture for the
// Responses API's previous_response_id.
func (uc *GatewayUseCase) loadVideoJob(ctx context.Context, key *model.AIVirtualKey, jobID string) (*model.AIVideoJob, error) {
	var job model.AIVideoJob
	if err := uc.db.WithContext(ctx).Where("id = ? AND virtual_key_id = ?", jobID, key.ID).First(&job).Error; err != nil {
		return nil, ErrVideoJobNotFound
	}
	return &job, nil
}

func (uc *GatewayUseCase) auditVideoProxyCall(ctx context.Context, key *model.AIVirtualKey, providerID uint, endpoint string, reqBody, respBody []byte, statusCode int, errMsg string, r *http.Request) {
	uc.writeAuditLog(ctx, key, providerID, endpoint, reqBody, respBody, 0, 0, 0, 0,
		0, statusCode, errMsg, false, ClientIPFromRequest(r), "video", 0, 0, "")
}

// -----------------------------------------------------------------------------
// POST /ai/v1/videos
// -----------------------------------------------------------------------------

func (uc *GatewayUseCase) HandleVideosCreate(ctx context.Context, key *model.AIVirtualKey, w http.ResponseWriter, r *http.Request) {
	body, tooLarge, rerr := readBoundedBody(r.Body, mediaMaxReqBody)
	if rerr != nil || tooLarge {
		mediaAPIError(w, kerrors.BadRequest("MEDIA_BODY_INVALID", "failed to read request body or body too large"))
		return
	}
	var req videoCreateRequest
	if json.Unmarshal(body, &req) != nil {
		mediaAPIError(w, kerrors.BadRequest("MEDIA_BODY_INVALID", "invalid JSON body"))
		return
	}

	realModel, providerID, mappingActive, rerr := uc.resolveMediaModel(ctx, key, req.Model, model.ModelTypeVideo)
	if rerr != nil {
		uc.auditVideoProxyCall(ctx, key, key.ProviderID, "videos.create", body, nil, int(kerrors.FromError(rerr).Code), rerr.Error(), r)
		mediaAPIError(w, rerr)
		return
	}

	if qerr := uc.quota.CheckAndReserveVideoCall(ctx, key); qerr != nil {
		uc.auditVideoProxyCall(ctx, key, providerID, "videos.create", body, nil, http.StatusTooManyRequests, qerr.Error(), r)
		mediaAPIError(w, kerrors.New(http.StatusTooManyRequests, "VIDEO_CALL_QUOTA_EXCEEDED", qerr.Error()))
		return
	}

	sendBody := replaceModelInBody(body, realModel)
	finalPrompt, blocked, findingTypes := uc.guardrailScanText(ctx, key, guardrail.DirectionInbound, req.Prompt)
	if blocked {
		msg := "prompt blocked by guardrail policy types=" + findingTypes
		uc.auditVideoProxyCall(ctx, key, providerID, "videos.create", body, nil, http.StatusForbidden, msg, r)
		mediaAPIError(w, kerrors.Forbidden("GUARDRAIL_BLOCKED", "prompt blocked by guardrail policy"))
		return
	}
	if finalPrompt != req.Prompt {
		sendBody = replaceJSONStringField(sendBody, "prompt", finalPrompt)
	}

	candidates := uc.mediaCandidates(ctx, key, req.Model, realModel, providerID, mappingActive, model.ModelTypeVideo)
	if len(candidates) == 0 {
		uc.auditVideoProxyCall(ctx, key, providerID, "videos.create", body, nil, http.StatusBadRequest, "no qualifying candidate provider", r)
		mediaAPIError(w, ErrMediaProviderUnsupported)
		return
	}

	resp, actualProviderID, ferr := uc.forwardMediaRequest(ctx, candidates, http.MethodPost, "/videos",
		func(modelName string) ([]byte, string, error) {
			if modelName == realModel {
				return sendBody, "application/json", nil
			}
			return replaceModelInBody(sendBody, modelName), "application/json", nil
		})
	if ferr != nil {
		uc.auditVideoProxyCall(ctx, key, providerID, "videos.create", body, nil, http.StatusBadGateway, ferr.Error(), r)
		writeMediaUpstreamFailure(w)
		return
	}
	defer resp.Body.Close()
	respBody, _, _ := readBoundedBody(resp.Body, mediaMaxRespBody)

	if resp.StatusCode < 300 {
		var parsed struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(respBody, &parsed) == nil && parsed.ID != "" {
			uc.db.WithContext(ctx).Save(&model.AIVideoJob{
				ID: parsed.ID, VirtualKeyID: key.ID, ProviderID: actualProviderID,
				Model: realModel, RawUpstream: datatypes.JSON(respBody), CreatedAt: time.Now(),
			})
		}
	}

	uc.auditVideoProxyCall(ctx, key, actualProviderID, "videos.create", body, respBody, resp.StatusCode, "", r)
	copyUpstreamJSON(w, resp, respBody)
}

// -----------------------------------------------------------------------------
// GET /ai/v1/videos/{id}, GET /ai/v1/videos/{id}/content, DELETE /ai/v1/videos/{id}
// -----------------------------------------------------------------------------

func (uc *GatewayUseCase) HandleVideosGet(ctx context.Context, key *model.AIVirtualKey, jobID string, w http.ResponseWriter, r *http.Request) {
	job, err := uc.loadVideoJob(ctx, key, jobID)
	if err != nil {
		mediaAPIError(w, err)
		return
	}
	entry, perr := uc.loadProviderDirect(ctx, job.ProviderID)
	if perr != nil {
		uc.auditVideoProxyCall(ctx, key, job.ProviderID, "videos.get", nil, nil, http.StatusBadGateway, perr.Error(), r)
		writeMediaUpstreamFailure(w)
		return
	}
	resp, ferr := uc.forwardRaw(ctx, entry, http.MethodGet, "/videos/"+jobID, nil, "")
	if ferr != nil {
		uc.auditVideoProxyCall(ctx, key, job.ProviderID, "videos.get", nil, nil, http.StatusBadGateway, ferr.Error(), r)
		writeMediaUpstreamFailure(w)
		return
	}
	defer resp.Body.Close()
	respBody, _, _ := readBoundedBody(resp.Body, mediaMaxRespBody)
	uc.auditVideoProxyCall(ctx, key, job.ProviderID, "videos.get", nil, respBody, resp.StatusCode, "", r)
	copyUpstreamJSON(w, resp, respBody)
}

func (uc *GatewayUseCase) HandleVideosContent(ctx context.Context, key *model.AIVirtualKey, jobID string, w http.ResponseWriter, r *http.Request) {
	job, err := uc.loadVideoJob(ctx, key, jobID)
	if err != nil {
		mediaAPIError(w, err)
		return
	}
	entry, perr := uc.loadProviderDirect(ctx, job.ProviderID)
	if perr != nil {
		uc.auditVideoProxyCall(ctx, key, job.ProviderID, "videos.content", nil, nil, http.StatusBadGateway, perr.Error(), r)
		writeMediaUpstreamFailure(w)
		return
	}
	resp, ferr := uc.forwardRaw(ctx, entry, http.MethodGet, "/videos/"+jobID+"/content", nil, "")
	if ferr != nil {
		uc.auditVideoProxyCall(ctx, key, job.ProviderID, "videos.content", nil, nil, http.StatusBadGateway, ferr.Error(), r)
		writeMediaUpstreamFailure(w)
		return
	}
	defer resp.Body.Close()
	// Binary video content — not logged (same posture as audio/speech's
	// binary output), streamed straight through, no failover possible past
	// this point regardless (streaming commit rule).
	uc.auditVideoProxyCall(ctx, key, job.ProviderID, "videos.content", nil, nil, resp.StatusCode, "", r)
	copyUpstreamStream(w, resp)
}

func (uc *GatewayUseCase) HandleVideosDelete(ctx context.Context, key *model.AIVirtualKey, jobID string, w http.ResponseWriter, r *http.Request) {
	job, err := uc.loadVideoJob(ctx, key, jobID)
	if err != nil {
		mediaAPIError(w, err)
		return
	}
	entry, perr := uc.loadProviderDirect(ctx, job.ProviderID)
	if perr != nil {
		uc.auditVideoProxyCall(ctx, key, job.ProviderID, "videos.delete", nil, nil, http.StatusBadGateway, perr.Error(), r)
		writeMediaUpstreamFailure(w)
		return
	}
	resp, ferr := uc.forwardRaw(ctx, entry, http.MethodDelete, "/videos/"+jobID, nil, "")
	if ferr != nil {
		uc.auditVideoProxyCall(ctx, key, job.ProviderID, "videos.delete", nil, nil, http.StatusBadGateway, ferr.Error(), r)
		writeMediaUpstreamFailure(w)
		return
	}
	defer resp.Body.Close()
	respBody, _, _ := readBoundedBody(resp.Body, mediaMaxRespBody)
	if resp.StatusCode < 300 {
		uc.db.WithContext(ctx).Delete(&model.AIVideoJob{}, "id = ? AND virtual_key_id = ?", jobID, key.ID)
	}
	uc.auditVideoProxyCall(ctx, key, job.ProviderID, "videos.delete", nil, respBody, resp.StatusCode, "", r)
	copyUpstreamJSON(w, resp, respBody)
}

// -----------------------------------------------------------------------------
// GET /ai/v1/videos (list this key's own jobs, from the local shadow table)
// -----------------------------------------------------------------------------

// videoJobListResp mirrors the shape of AIVideoJob.RawUpstream entries the
// client already expects (each element is the provider's own job object, the
// same JSON the create/get calls returned) rather than inventing a new list
// envelope shape.
type videoJobListResp struct {
	Object string            `json:"object"`
	Data   []json.RawMessage `json:"data"`
}

func (uc *GatewayUseCase) HandleVideosList(ctx context.Context, key *model.AIVirtualKey, w http.ResponseWriter, r *http.Request) {
	var jobs []model.AIVideoJob
	if err := uc.db.WithContext(ctx).Where("virtual_key_id = ?", key.ID).Order("created_at desc").Find(&jobs).Error; err != nil {
		mediaAPIError(w, err)
		return
	}
	out := videoJobListResp{Object: "list", Data: make([]json.RawMessage, 0, len(jobs))}
	for _, j := range jobs {
		if len(j.RawUpstream) > 0 {
			out.Data = append(out.Data, json.RawMessage(j.RawUpstream))
		}
	}
	uc.auditVideoProxyCall(ctx, key, key.ProviderID, "videos.list", nil, nil, http.StatusOK, "", r)
	w.Header().Set("Content-Type", "application/json")
	b, _ := json.Marshal(out)
	w.Write(b)
}
