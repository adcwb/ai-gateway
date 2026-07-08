package biz

import (
	"context"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data/model"
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
