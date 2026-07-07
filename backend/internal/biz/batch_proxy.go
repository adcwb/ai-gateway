package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gorm.io/datatypes"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

// Batch + Files API proxy (docs/design/09-extensibility.md "Future protocol
// posture": async-job passthrough with job-ID mapping and deferred usage
// settlement on batch completion). Scope for this round: openai_compatible
// providers only — the only dialect with a well-specified, widely-copied
// Batch/Files shape; Anthropic's separate Message Batches API is not
// translated. This is a pure passthrough: the gateway never stores file
// bytes, only enough shadow bookkeeping (AIProxyFile/AIBatchJob) to route a
// later GET/DELETE-by-id back to the provider that holds it, and for
// BatchSettlementPoller (batch_settlement.go) to find jobs needing a status
// check or final settlement.

const proxyProviderHeader = "X-AIGW-Provider"

// resolveProviderByName finds an enabled provider by its admin-assigned name
// — Files/Batches requests carry no "model" field, so they can't go through
// the usual model-mapping provider resolution.
func (uc *GatewayUseCase) resolveProviderByName(ctx context.Context, name string) (*model.AIProvider, error) {
	if name == "" {
		return nil, fmt.Errorf("%s header is required for this endpoint", proxyProviderHeader)
	}
	var p model.AIProvider
	if err := uc.db.WithContext(ctx).Where("name = ? AND is_enabled = ?", name, true).First(&p).Error; err != nil {
		return nil, ErrProviderNotFound
	}
	return &p, nil
}

func batchAPIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": message}})
	w.Write(b)
}

// forwardRaw proxies method+path to the resolved provider with its
// Bearer-auth header, streaming body through without buffering (multipart
// file uploads can be large).
func (uc *GatewayUseCase) forwardRaw(ctx context.Context, entry *providerEntry, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(entry.provider.BaseURL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+entry.apiKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return newProxyClient().Do(req)
}

func (uc *GatewayUseCase) auditBatchProxyCall(ctx context.Context, key *model.AIVirtualKey, providerID uint, endpoint string, statusCode int, r *http.Request) {
	uc.writeAuditLog(ctx, key, providerID, endpoint, nil, nil, 0, 0, 0, 0, 0,
		statusCode, "", false, ClientIPFromRequest(r), "openai-batch", 0, 0, "")
}

// -----------------------------------------------------------------------------
// Files API
// -----------------------------------------------------------------------------

// ProxyFilesUpload handles POST /ai/v1/files (multipart passthrough).
func (uc *GatewayUseCase) ProxyFilesUpload(ctx context.Context, key *model.AIVirtualKey, w http.ResponseWriter, r *http.Request) {
	provider, err := uc.resolveProviderByName(ctx, r.Header.Get(proxyProviderHeader))
	if err != nil {
		batchAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, err := uc.loadProviderDirect(ctx, provider.ID)
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "failed to load provider credentials")
		return
	}

	resp, err := uc.forwardRaw(ctx, entry, http.MethodPost, "/v1/files", r.Body, r.Header.Get("Content-Type"))
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 300 {
		var parsed struct {
			ID       string `json:"id"`
			Filename string `json:"filename"`
			Purpose  string `json:"purpose"`
			Bytes    int64  `json:"bytes"`
		}
		if json.Unmarshal(raw, &parsed) == nil && parsed.ID != "" {
			uc.db.WithContext(ctx).Save(&model.AIProxyFile{
				ID: parsed.ID, VirtualKeyID: key.ID, ProviderID: provider.ID,
				Purpose: parsed.Purpose, Filename: parsed.Filename, Bytes: parsed.Bytes,
				RawUpstream: datatypes.JSON(raw), CreatedAt: time.Now(),
			})
		}
	}
	uc.auditBatchProxyCall(ctx, key, provider.ID, "files.upload", resp.StatusCode, r)
	copyUpstreamJSON(w, resp, raw)
}

// ProxyFilesGet handles GET /ai/v1/files/{id}.
func (uc *GatewayUseCase) ProxyFilesGet(ctx context.Context, key *model.AIVirtualKey, fileID string, w http.ResponseWriter, r *http.Request) {
	var f model.AIProxyFile
	if err := uc.db.WithContext(ctx).First(&f, "id = ?", fileID).Error; err != nil {
		batchAPIError(w, http.StatusNotFound, "file not found")
		return
	}
	entry, err := uc.loadProviderDirect(ctx, f.ProviderID)
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "failed to load provider credentials")
		return
	}
	resp, err := uc.forwardRaw(ctx, entry, http.MethodGet, "/v1/files/"+fileID, nil, "")
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	uc.auditBatchProxyCall(ctx, key, f.ProviderID, "files.get", resp.StatusCode, r)
	copyUpstreamJSON(w, resp, raw)
}

// ProxyFilesContent handles GET /ai/v1/files/{id}/content.
func (uc *GatewayUseCase) ProxyFilesContent(ctx context.Context, key *model.AIVirtualKey, fileID string, w http.ResponseWriter, r *http.Request) {
	var f model.AIProxyFile
	if err := uc.db.WithContext(ctx).First(&f, "id = ?", fileID).Error; err != nil {
		batchAPIError(w, http.StatusNotFound, "file not found")
		return
	}
	entry, err := uc.loadProviderDirect(ctx, f.ProviderID)
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "failed to load provider credentials")
		return
	}
	resp, err := uc.forwardRaw(ctx, entry, http.MethodGet, "/v1/files/"+fileID+"/content", nil, "")
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	uc.auditBatchProxyCall(ctx, key, f.ProviderID, "files.content", resp.StatusCode, r)
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ProxyFilesDelete handles DELETE /ai/v1/files/{id}.
func (uc *GatewayUseCase) ProxyFilesDelete(ctx context.Context, key *model.AIVirtualKey, fileID string, w http.ResponseWriter, r *http.Request) {
	var f model.AIProxyFile
	if err := uc.db.WithContext(ctx).First(&f, "id = ?", fileID).Error; err != nil {
		batchAPIError(w, http.StatusNotFound, "file not found")
		return
	}
	entry, err := uc.loadProviderDirect(ctx, f.ProviderID)
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "failed to load provider credentials")
		return
	}
	resp, err := uc.forwardRaw(ctx, entry, http.MethodDelete, "/v1/files/"+fileID, nil, "")
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 300 {
		uc.db.WithContext(ctx).Delete(&model.AIProxyFile{}, "id = ?", fileID)
	}
	uc.auditBatchProxyCall(ctx, key, f.ProviderID, "files.delete", resp.StatusCode, r)
	copyUpstreamJSON(w, resp, raw)
}

// -----------------------------------------------------------------------------
// Batches API
// -----------------------------------------------------------------------------

// ProxyFilesList handles GET /ai/v1/files — like uploads, list requires an
// explicit provider selector since files are provider-scoped resources.
func (uc *GatewayUseCase) ProxyFilesList(ctx context.Context, key *model.AIVirtualKey, w http.ResponseWriter, r *http.Request) {
	provider, err := uc.resolveProviderByName(ctx, r.Header.Get(proxyProviderHeader))
	if err != nil {
		batchAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, err := uc.loadProviderDirect(ctx, provider.ID)
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "failed to load provider credentials")
		return
	}
	path := "/v1/files"
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	resp, err := uc.forwardRaw(ctx, entry, http.MethodGet, path, nil, "")
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	uc.auditBatchProxyCall(ctx, key, provider.ID, "files.list", resp.StatusCode, r)
	copyUpstreamJSON(w, resp, raw)
}

// ProxyBatchesList handles GET /ai/v1/batches.
func (uc *GatewayUseCase) ProxyBatchesList(ctx context.Context, key *model.AIVirtualKey, w http.ResponseWriter, r *http.Request) {
	provider, err := uc.resolveProviderByName(ctx, r.Header.Get(proxyProviderHeader))
	if err != nil {
		batchAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, err := uc.loadProviderDirect(ctx, provider.ID)
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "failed to load provider credentials")
		return
	}
	path := "/v1/batches"
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	resp, err := uc.forwardRaw(ctx, entry, http.MethodGet, path, nil, "")
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	uc.auditBatchProxyCall(ctx, key, provider.ID, "batches.list", resp.StatusCode, r)
	copyUpstreamJSON(w, resp, raw)
}

// ProxyBatchesCreate handles POST /ai/v1/batches.
func (uc *GatewayUseCase) ProxyBatchesCreate(ctx context.Context, key *model.AIVirtualKey, body []byte, w http.ResponseWriter, r *http.Request) {
	provider, err := uc.resolveProviderByName(ctx, r.Header.Get(proxyProviderHeader))
	if err != nil {
		batchAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, err := uc.loadProviderDirect(ctx, provider.ID)
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "failed to load provider credentials")
		return
	}

	resp, err := uc.forwardRaw(ctx, entry, http.MethodPost, "/v1/batches", strings.NewReader(string(body)), "application/json")
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 300 {
		var parsed struct {
			ID               string          `json:"id"`
			Endpoint         string          `json:"endpoint"`
			InputFileID      string          `json:"input_file_id"`
			CompletionWindow string          `json:"completion_window"`
			Status           string          `json:"status"`
			RequestCounts    json.RawMessage `json:"request_counts"`
		}
		if json.Unmarshal(raw, &parsed) == nil && parsed.ID != "" {
			uc.db.WithContext(ctx).Save(&model.AIBatchJob{
				ID: parsed.ID, VirtualKeyID: key.ID, ProviderID: provider.ID,
				InputFileID: parsed.InputFileID, Endpoint: parsed.Endpoint,
				CompletionWindow: parsed.CompletionWindow, Status: parsed.Status,
				RequestCounts: datatypes.JSON(parsed.RequestCounts), RawUpstream: datatypes.JSON(raw),
				CreatedAt: time.Now(),
			})
		}
	}
	uc.auditBatchProxyCall(ctx, key, provider.ID, "batches.create", resp.StatusCode, r)
	copyUpstreamJSON(w, resp, raw)
}

// ProxyBatchesGet handles GET /ai/v1/batches/{id}. It replays the local
// shadow row rather than always calling upstream — BatchSettlementPoller
// keeps it fresh in the background, so a client polling status doesn't cost
// an upstream call per poll.
func (uc *GatewayUseCase) ProxyBatchesGet(ctx context.Context, key *model.AIVirtualKey, batchID string, w http.ResponseWriter, r *http.Request) {
	var job model.AIBatchJob
	if err := uc.db.WithContext(ctx).First(&job, "id = ?", batchID).Error; err != nil {
		batchAPIError(w, http.StatusNotFound, "batch not found")
		return
	}
	uc.auditBatchProxyCall(ctx, key, job.ProviderID, "batches.get", http.StatusOK, r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(job.RawUpstream)
}

// ProxyBatchesCancel handles POST /ai/v1/batches/{id}/cancel.
func (uc *GatewayUseCase) ProxyBatchesCancel(ctx context.Context, key *model.AIVirtualKey, batchID string, w http.ResponseWriter, r *http.Request) {
	var job model.AIBatchJob
	if err := uc.db.WithContext(ctx).First(&job, "id = ?", batchID).Error; err != nil {
		batchAPIError(w, http.StatusNotFound, "batch not found")
		return
	}
	entry, err := uc.loadProviderDirect(ctx, job.ProviderID)
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "failed to load provider credentials")
		return
	}
	resp, err := uc.forwardRaw(ctx, entry, http.MethodPost, "/v1/batches/"+batchID+"/cancel", nil, "")
	if err != nil {
		batchAPIError(w, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 300 {
		uc.db.WithContext(ctx).Model(&model.AIBatchJob{}).Where("id = ?", batchID).
			Updates(map[string]interface{}{"status": "cancelling", "raw_upstream_json": datatypes.JSON(raw)})
	}
	uc.auditBatchProxyCall(ctx, key, job.ProviderID, "batches.cancel", resp.StatusCode, r)
	copyUpstreamJSON(w, resp, raw)
}

func copyUpstreamJSON(w http.ResponseWriter, resp *http.Response, raw []byte) {
	for k, vv := range resp.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(raw)
}
