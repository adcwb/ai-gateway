package biz

import (
	"context"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/adcwb/ai-gateway/internal/biz/dto"
	"github.com/adcwb/ai-gateway/internal/conf"
	"github.com/adcwb/ai-gateway/internal/data/model"
)

func newTestGatewayForModelMapping(t *testing.T) *GatewayUseCase {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.AIModelItem{}, &model.AIModelMapping{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewGatewayUseCase(db, nil, nil, nil, nil, nil, nil,
		&conf.AI{}, &conf.System{EncryptionKey: testEncryptionKey[:32]}, log.NewStdLogger(testWriter{t}))
}

func TestModelMappingCRUD(t *testing.T) {
	uc := newTestGatewayForModelMapping(t)
	ctx := context.Background()

	if err := uc.db.Create(&model.AIModelItem{ProviderID: 1, Name: "gpt-4o"}).Error; err != nil {
		t.Fatalf("seed model item: %v", err)
	}

	created, err := uc.CreateModelMapping(ctx, &dto.CreateModelMappingReq{
		VirtualKeyID: 1, VirtualModel: "gpt-4", RealModelID: 1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created.IsEnabled {
		t.Fatalf("expected new mapping enabled by default: %+v", created)
	}

	if _, err := uc.CreateModelMapping(ctx, &dto.CreateModelMappingReq{
		VirtualKeyID: 1, VirtualModel: "gpt-4", RealModelID: 1,
	}); err == nil {
		t.Fatal("expected duplicate (virtualKeyId, virtualModel) to fail")
	}

	list, err := uc.ListModelMappings(ctx, 1)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v, len=%d", err, len(list))
	}
	if list[0].RealModel == nil || list[0].RealModel.Name != "gpt-4o" {
		t.Fatalf("expected RealModel preloaded: %+v", list[0])
	}

	disabled := false
	updated, err := uc.UpdateModelMapping(ctx, &dto.UpdateModelMappingReq{ID: created.ID, IsEnabled: &disabled})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.IsEnabled {
		t.Fatalf("expected mapping disabled: %+v", updated)
	}

	if err := uc.DeleteModelMapping(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := uc.DeleteModelMapping(ctx, created.ID); err == nil {
		t.Fatal("expected not-found deleting twice")
	}
}

// -----------------------------------------------------------------------------
// Modality-consistency validation, phase 3 (docs/superpowers/specs/2026-07-09-
// model-mapping-modality-validation-phase3-design.md). llm mappings are
// exempt — this is a behavior-preservation regression test, not just a
// happy-path check.
// -----------------------------------------------------------------------------

func TestModelMapping_LLMFallbackChainUncatalogedEntryStillAllowed(t *testing.T) {
	uc := newTestGatewayForModelMapping(t)
	ctx := context.Background()

	if err := uc.db.Create(&model.AIModelItem{ProviderID: 1, Name: "gpt-4o"}).Error; err != nil {
		t.Fatalf("seed model item: %v", err)
	}
	chainJSON := []byte(`[{"providerId":2,"model":"some-uncataloged-chat-model"}]`)
	if _, err := uc.CreateModelMapping(ctx, &dto.CreateModelMappingReq{
		VirtualKeyID: 1, VirtualModel: "gpt-4", RealModelID: 1, FallbackChain: chainJSON,
	}); err != nil {
		t.Fatalf("expected an llm mapping's fallback chain to skip catalog validation, got: %v", err)
	}
}

func TestModelMapping_ImageFallbackChainMismatchedModalityRejected(t *testing.T) {
	uc := newTestGatewayForModelMapping(t)
	ctx := context.Background()

	if err := uc.db.Create(&model.AIModelItem{ID: 1, ProviderID: 1, Name: "dall-e-3", ModelType: model.ModelTypeImage, IsEnabled: true}).Error; err != nil {
		t.Fatalf("seed image model: %v", err)
	}
	if err := uc.db.Create(&model.AIModelItem{ID: 2, ProviderID: 1, Name: "tts-1", ModelType: model.ModelTypeTTS, IsEnabled: true}).Error; err != nil {
		t.Fatalf("seed tts model: %v", err)
	}
	chainJSON := []byte(`[{"providerId":1,"model":"tts-1"}]`)
	_, err := uc.CreateModelMapping(ctx, &dto.CreateModelMappingReq{
		VirtualKeyID: 1, VirtualModel: "premium-image", RealModelID: 1, FallbackChain: chainJSON,
	})
	if err == nil {
		t.Fatal("expected a mismatched-modality fallback entry to be rejected")
	}
}

func TestModelMapping_ImageFallbackChainSameModalityAllowed(t *testing.T) {
	uc := newTestGatewayForModelMapping(t)
	ctx := context.Background()

	if err := uc.db.Create(&model.AIModelItem{ID: 1, ProviderID: 1, Name: "dall-e-3", ModelType: model.ModelTypeImage, IsEnabled: true}).Error; err != nil {
		t.Fatalf("seed image model: %v", err)
	}
	if err := uc.db.Create(&model.AIModelItem{ID: 2, ProviderID: 2, Name: "sdxl", ModelType: model.ModelTypeImage, IsEnabled: true}).Error; err != nil {
		t.Fatalf("seed second image model: %v", err)
	}
	chainJSON := []byte(`[{"providerId":2,"model":"sdxl"}]`)
	if _, err := uc.CreateModelMapping(ctx, &dto.CreateModelMappingReq{
		VirtualKeyID: 1, VirtualModel: "premium-image", RealModelID: 1, FallbackChain: chainJSON,
	}); err != nil {
		t.Fatalf("expected a same-modality fallback entry to be allowed, got: %v", err)
	}
}

func TestModelMapping_RealModelIDNotFoundRejected(t *testing.T) {
	uc := newTestGatewayForModelMapping(t)
	ctx := context.Background()

	_, err := uc.CreateModelMapping(ctx, &dto.CreateModelMappingReq{
		VirtualKeyID: 1, VirtualModel: "gpt-4", RealModelID: 999,
	})
	if err == nil {
		t.Fatal("expected a nonexistent RealModelID to be rejected")
	}
}

func TestModelMapping_UpdateFallbackChainToMismatchedModalityRejected(t *testing.T) {
	uc := newTestGatewayForModelMapping(t)
	ctx := context.Background()

	if err := uc.db.Create(&model.AIModelItem{ID: 1, ProviderID: 1, Name: "dall-e-3", ModelType: model.ModelTypeImage, IsEnabled: true}).Error; err != nil {
		t.Fatalf("seed image model: %v", err)
	}
	if err := uc.db.Create(&model.AIModelItem{ID: 2, ProviderID: 1, Name: "tts-1", ModelType: model.ModelTypeTTS, IsEnabled: true}).Error; err != nil {
		t.Fatalf("seed tts model: %v", err)
	}
	created, err := uc.CreateModelMapping(ctx, &dto.CreateModelMappingReq{
		VirtualKeyID: 1, VirtualModel: "premium-image", RealModelID: 1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mismatchedChain := []byte(`[{"providerId":1,"model":"tts-1"}]`)
	if _, err := uc.UpdateModelMapping(ctx, &dto.UpdateModelMappingReq{ID: created.ID, FallbackChain: mismatchedChain}); err == nil {
		t.Fatal("expected updating to a mismatched-modality fallback chain to be rejected")
	}
}

func TestModelMapping_UpdateUnrelatedFieldSkipsValidation(t *testing.T) {
	uc := newTestGatewayForModelMapping(t)
	ctx := context.Background()

	if err := uc.db.Create(&model.AIModelItem{ID: 1, ProviderID: 1, Name: "dall-e-3", ModelType: model.ModelTypeImage, IsEnabled: true}).Error; err != nil {
		t.Fatalf("seed image model: %v", err)
	}
	created, err := uc.CreateModelMapping(ctx, &dto.CreateModelMappingReq{
		VirtualKeyID: 1, VirtualModel: "premium-image", RealModelID: 1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	desc := "updated description only"
	if _, err := uc.UpdateModelMapping(ctx, &dto.UpdateModelMappingReq{ID: created.ID, Description: &desc}); err != nil {
		t.Fatalf("expected an update touching neither RealModelID nor FallbackChain to skip validation, got: %v", err)
	}
}
