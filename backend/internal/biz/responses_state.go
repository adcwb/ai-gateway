package biz

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

// Responses API server-side conversation state (docs/design/02-protocol-
// adapters.md previous_response_id/store). Storage is plaintext JSON — a
// documented simplification, consistent with how most JSON config columns in
// this schema already work; audit-body encryption (a separate, narrower
// feature) is not extended to this table.

const defaultResponsesStateTTLHours = 24
const responsesStateSweepInterval = 10 * time.Minute

// loadResponseState resolves a previous_response_id, scoped to the
// requesting virtual key — a stored conversation can only be continued by
// the same key that created it. Not found/expired/wrong-key all return the
// same error, so a client can't distinguish "wrong key" from "never
// existed" (no enumeration signal).
func (uc *GatewayUseCase) loadResponseState(ctx context.Context, keyID uint, responseID string) (*model.AIResponseState, error) {
	var state model.AIResponseState
	err := uc.db.WithContext(ctx).
		Where("response_id = ? AND virtual_key_id = ? AND expires_at > ?", responseID, keyID, time.Now()).
		First(&state).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrResponseStateNotFound
		}
		return nil, err
	}
	return &state, nil
}

// saveResponseState persists one turn's full message history (the messages
// actually sent upstream, plus the resulting assistant turn) under the
// response ID returned to the client.
func (uc *GatewayUseCase) saveResponseState(ctx context.Context, keyID uint, responseID, modelName string, messages []map[string]interface{}) {
	raw, err := json.Marshal(messages)
	if err != nil {
		uc.logger.Errorf("responses: 序列化会话状态失败 responseID=%s err=%v", responseID, err)
		return
	}
	ttlHours := uc.aiConf.ResponsesStateTTLHours
	if ttlHours <= 0 {
		ttlHours = defaultResponsesStateTTLHours
	}
	state := model.AIResponseState{
		ResponseID:   responseID,
		VirtualKeyID: keyID,
		Model:        modelName,
		Messages:     raw,
		ExpiresAt:    time.Now().Add(time.Duration(ttlHours) * time.Hour),
	}
	if err := uc.db.WithContext(ctx).Create(&state).Error; err != nil {
		uc.logger.Errorf("responses: 保存会话状态失败 responseID=%s err=%v", responseID, err)
	}
}

// StartResponsesStateSweeper deletes expired stored conversations — TTL is
// the only lifecycle control (no admin/console visibility into this table),
// same posture as the response cache's TTL-only invalidation (D07).
func StartResponsesStateSweeper(ctx context.Context, db *gorm.DB, logger log.Logger) {
	helper := log.NewHelper(logger)
	go func() {
		ticker := time.NewTicker(responsesStateSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepExpiredResponseStates(ctx, db, helper)
			}
		}
	}()
}

func sweepExpiredResponseStates(ctx context.Context, db *gorm.DB, helper *log.Helper) {
	defer func() {
		if r := recover(); r != nil {
			helper.Errorf("responses 状态清理器 panic: %v", r)
		}
	}()
	sweepCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.WithContext(sweepCtx).Where("expires_at < ?", time.Now()).Delete(&model.AIResponseState{}).Error; err != nil {
		helper.Errorf("responses: 清理过期会话状态失败 err=%v", err)
	}
}
