package biz

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"gorm.io/datatypes"

	"github.com/adcwb/ai-gateway/internal/data/model"
)

const batchSettlementSweepInterval = 60 * time.Second

// batchDiscount is OpenAI's published Batch API price: half the synchronous
// per-token rate, applied uniformly at settlement time regardless of the
// per-model price table (docs/design/09-extensibility.md "Future protocol
// posture" recipe: deferred usage settlement on batch completion).
const batchDiscount = 0.5

// StartBatchSettlementPoller ticks batchSettlementSweepInterval and, for
// every AIBatchJob not yet settled, refreshes its status from upstream and —
// once that status is "completed" — fetches the output file exactly once,
// sums usage across every JSONL result line, and settles one aggregate
// charge per model via BillingManager.Settle (there is no pre-existing
// freeze to reconcile against, since Admit was never called at batch-submit
// time — usage is unknowable until the batch actually runs — so this
// constructs a zero-estimate FreezeHandle and lets Settle's
// estimate-vs-actual delta do a pure debit).
func (uc *GatewayUseCase) StartBatchSettlementPoller(ctx context.Context) {
	if uc.db == nil {
		return
	}
	ticker := time.NewTicker(batchSettlementSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			uc.sweepBatchJobs(ctx)
		}
	}
}

func (uc *GatewayUseCase) sweepBatchJobs(ctx context.Context) {
	var jobs []model.AIBatchJob
	if err := uc.db.WithContext(ctx).Where("settled_at IS NULL").Find(&jobs).Error; err != nil {
		uc.logger.Warnf("批次结算：查询待处理批次失败 err=%v", err)
		return
	}
	for _, job := range jobs {
		uc.pollAndSettleBatchJob(ctx, job)
	}
}

func (uc *GatewayUseCase) pollAndSettleBatchJob(ctx context.Context, job model.AIBatchJob) {
	entry, err := uc.loadProviderDirect(ctx, job.ProviderID)
	if err != nil {
		uc.logger.Warnf("批次结算：加载提供方失败 jobID=%s err=%v", job.ID, err)
		return
	}
	resp, err := uc.forwardRaw(ctx, entry, http.MethodGet, "/v1/batches/"+job.ID, nil, "")
	if err != nil {
		uc.logger.Warnf("批次结算：轮询上游状态失败 jobID=%s err=%v", job.ID, err)
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		uc.logger.Warnf("批次结算：轮询状态返回非 2xx jobID=%s status=%d", job.ID, resp.StatusCode)
		return
	}

	var parsed struct {
		Status        string          `json:"status"`
		OutputFileID  string          `json:"output_file_id"`
		ErrorFileID   string          `json:"error_file_id"`
		RequestCounts json.RawMessage `json:"request_counts"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return
	}

	updates := map[string]interface{}{
		"status": parsed.Status, "output_file_id": parsed.OutputFileID, "error_file_id": parsed.ErrorFileID,
		"raw_upstream_json": datatypes.JSON(raw),
	}
	if len(parsed.RequestCounts) > 0 {
		updates["request_counts"] = datatypes.JSON(parsed.RequestCounts)
	}
	if model.BatchTerminalStatuses[parsed.Status] {
		now := time.Now()
		updates["completed_at"] = &now
	}
	uc.db.WithContext(ctx).Model(&model.AIBatchJob{}).Where("id = ?", job.ID).Updates(updates)

	if parsed.Status != "completed" {
		if model.BatchTerminalStatuses[parsed.Status] {
			// failed/expired/cancelled: nothing to bill, stop polling this job.
			now := time.Now()
			uc.db.WithContext(ctx).Model(&model.AIBatchJob{}).Where("id = ?", job.ID).Update("settled_at", now)
		}
		return
	}
	uc.settleCompletedBatch(ctx, job, entry, parsed.OutputFileID)
}

type batchUsageAgg struct {
	prompt, completion, cached int
}

// settleCompletedBatch fetches the batch's output file once and sums usage
// per model (a batch's lines can in principle target different models, even
// though the common case is one). If the fetch fails, the job is left
// unsettled and retried on the next sweep — fail-open on economics, per the
// project's standing rule, rather than blocking forever or double-charging.
func (uc *GatewayUseCase) settleCompletedBatch(ctx context.Context, job model.AIBatchJob, entry *providerEntry, outputFileID string) {
	if outputFileID == "" {
		uc.logger.Warnf("批次结算：已完成批次缺少 output_file_id jobID=%s", job.ID)
		return
	}
	resp, err := uc.forwardRaw(ctx, entry, http.MethodGet, "/v1/files/"+outputFileID+"/content", nil, "")
	if err != nil {
		uc.logger.Warnf("批次结算：拉取输出文件失败 jobID=%s err=%v", job.ID, err)
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		uc.logger.Warnf("批次结算：输出文件返回非 2xx jobID=%s status=%d", job.ID, resp.StatusCode)
		return
	}

	usageByModel := map[string]*batchUsageAgg{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row struct {
			Response *struct {
				Body struct {
					Model string `json:"model"`
					Usage struct {
						PromptTokens        int `json:"prompt_tokens"`
						CompletionTokens    int `json:"completion_tokens"`
						PromptTokensDetails *struct {
							CachedTokens int `json:"cached_tokens"`
						} `json:"prompt_tokens_details"`
					} `json:"usage"`
				} `json:"body"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(line), &row) != nil || row.Response == nil || row.Response.Body.Model == "" {
			continue
		}
		agg := usageByModel[row.Response.Body.Model]
		if agg == nil {
			agg = &batchUsageAgg{}
			usageByModel[row.Response.Body.Model] = agg
		}
		agg.prompt += row.Response.Body.Usage.PromptTokens
		agg.completion += row.Response.Body.Usage.CompletionTokens
		if row.Response.Body.Usage.PromptTokensDetails != nil {
			agg.cached += row.Response.Body.Usage.PromptTokensDetails.CachedTokens
		}
	}

	var key model.AIVirtualKey
	if err := uc.db.WithContext(ctx).First(&key, job.VirtualKeyID).Error; err != nil {
		uc.logger.Warnf("批次结算：虚拟 Key 查询失败 jobID=%s err=%v", job.ID, err)
		return
	}
	tenantID := uc.tenantIDForKey(ctx, &key)

	if uc.billing != nil {
		acct := uc.billing.AccountForTenant(ctx, tenantID)
		for modelName, agg := range usageByModel {
			costMicro := uc.billing.CostMicro(ctx, "CNY", job.ProviderID, modelName, agg.prompt, agg.completion, agg.cached, 0)
			priceMicro := costMicro
			if acct != nil {
				priceMicro = uc.billing.PriceMicro(ctx, acct, job.ProviderID, modelName, agg.prompt, agg.completion, agg.cached, 0)
				if priceMicro == 0 {
					priceMicro = costMicro
				}
			}
			billedMicro := int64(float64(priceMicro) * batchDiscount)
			if acct != nil && billedMicro > 0 {
				uc.billing.Settle(ctx, &FreezeHandle{Account: acct, EstMicro: 0}, "batch-"+job.ID+"-"+modelName, billedMicro, job.ID, "batch settlement model="+modelName)
			}
			uc.billing.RecordUsage(tenantID, key.ID, job.ProviderID, modelName, agg.prompt, agg.completion, agg.cached, costMicro, billedMicro, false)
		}
	}

	now := time.Now()
	uc.db.WithContext(ctx).Model(&model.AIBatchJob{}).Where("id = ?", job.ID).Update("settled_at", now)
	uc.writeAuditLog(ctx, &key, job.ProviderID, "batch:"+job.ID, nil, nil, 0, 0, 0, 0, 0,
		http.StatusOK, "", false, "", "openai-batch", 0, 0, "")
}
