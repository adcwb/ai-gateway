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

func newTestGatewayForPIIPolicy(t *testing.T) *GatewayUseCase {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.AIPIIPolicy{}, &model.AIVirtualKey{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewGatewayUseCase(db, nil, nil, nil, nil, nil, nil,
		&conf.AI{}, &conf.System{EncryptionKey: testEncryptionKey[:32]}, log.NewStdLogger(testWriter{t}))
}

func TestPIIPolicyCRUD(t *testing.T) {
	uc := newTestGatewayForPIIPolicy(t)
	ctx := context.Background()

	created, err := uc.CreatePIIPolicy(ctx, &dto.CreatePIIPolicyReq{Name: "strict", Action: model.PIIActionBlock})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created.Enabled {
		t.Fatalf("expected enabled by default: %+v", created)
	}

	if _, err := uc.CreatePIIPolicy(ctx, &dto.CreatePIIPolicyReq{Name: "", Action: model.PIIActionBlock}); err == nil {
		t.Fatal("expected empty name to fail")
	}

	updatedName := "strict-v2"
	updated, err := uc.UpdatePIIPolicy(ctx, &dto.UpdatePIIPolicyReq{ID: created.ID, Name: &updatedName})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "strict-v2" {
		t.Fatalf("expected name updated: %+v", updated)
	}

	if err := uc.DeletePIIPolicy(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := uc.DeletePIIPolicy(ctx, created.ID); err == nil {
		t.Fatal("expected not-found deleting twice")
	}
}

func TestPIIPolicySingleDefaultEnforcement(t *testing.T) {
	uc := newTestGatewayForPIIPolicy(t)
	ctx := context.Background()

	first, err := uc.CreatePIIPolicy(ctx, &dto.CreatePIIPolicyReq{Name: "a", Action: model.PIIActionBlock, IsDefault: true})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := uc.CreatePIIPolicy(ctx, &dto.CreatePIIPolicyReq{Name: "b", Action: model.PIIActionBlock, IsDefault: true})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	list, err := uc.ListPIIPolicies(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defaults := 0
	for _, p := range list {
		if p.IsDefault != nil && *p.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("expected exactly 1 default policy, got %d", defaults)
	}

	notDefault := false
	if _, err := uc.UpdatePIIPolicy(ctx, &dto.UpdatePIIPolicyReq{ID: first.ID, IsDefault: &notDefault}); err != nil {
		t.Fatalf("update first: %v", err)
	}
	makeDefault := true
	if _, err := uc.UpdatePIIPolicy(ctx, &dto.UpdatePIIPolicyReq{ID: first.ID, IsDefault: &makeDefault}); err != nil {
		t.Fatalf("re-default first: %v", err)
	}

	list, err = uc.ListPIIPolicies(ctx)
	if err != nil {
		t.Fatalf("list 2: %v", err)
	}
	defaults = 0
	var defaultID uint
	for _, p := range list {
		if p.IsDefault != nil && *p.IsDefault {
			defaults++
			defaultID = p.ID
		}
	}
	if defaults != 1 || defaultID != first.ID {
		t.Fatalf("expected exactly 1 default policy (id=%d), got %d defaults (last=%d)", first.ID, defaults, defaultID)
	}
	_ = second
}

func TestPIIPolicyBoundKeyCount(t *testing.T) {
	uc := newTestGatewayForPIIPolicy(t)
	ctx := context.Background()

	policy, err := uc.CreatePIIPolicy(ctx, &dto.CreatePIIPolicyReq{Name: "bound", Action: model.PIIActionBlock})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := uc.db.Create(&model.AIVirtualKey{
			Name: "k", KeyHash: randHex(t, i), KeyPrefix: "sk-vk", ProviderID: 1, PIIPolicyID: &policy.ID,
		}).Error; err != nil {
			t.Fatalf("seed key: %v", err)
		}
	}

	list, err := uc.ListPIIPolicies(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].BoundKeyCount != 3 {
		t.Fatalf("expected boundKeyCount=3, got %+v", list)
	}
}

func randHex(t *testing.T, seed int) string {
	t.Helper()
	return "hash" + string(rune('a'+seed))
}
