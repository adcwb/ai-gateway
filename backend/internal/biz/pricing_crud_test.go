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

func newTestGatewayForPricing(t *testing.T) *GatewayUseCase {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.AIModelItem{}, &model.AIPriceTable{}, &model.AIPriceTableItem{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewGatewayUseCase(db, nil, nil, nil, nil, nil, nil,
		&conf.AI{}, &conf.System{EncryptionKey: testEncryptionKey[:32]}, log.NewStdLogger(testWriter{t}))
}

func TestModelItemCRUD(t *testing.T) {
	uc := newTestGatewayForPricing(t)
	ctx := context.Background()

	created, err := uc.CreateModelItem(ctx, &dto.CreateModelItemReq{
		ProviderID: 1, Name: "gpt-4o", InputPricePerMillion: 5, OutputPricePerMillion: 15,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Source != "manual" || !created.IsEnabled {
		t.Fatalf("unexpected defaults: %+v", created)
	}

	if _, err := uc.CreateModelItem(ctx, &dto.CreateModelItemReq{ProviderID: 1, Name: "gpt-4o"}); err == nil {
		t.Fatal("expected duplicate (provider,name) to fail")
	}

	list, err := uc.ListModelItems(ctx, 1)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v, len=%d", err, len(list))
	}

	newInput := 6.0
	updated, err := uc.UpdateModelItem(ctx, &dto.UpdateModelItemReq{ID: created.ID, InputPricePerMillion: &newInput})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.InputPricePerMillion != 6.0 {
		t.Fatalf("input price not updated: %+v", updated)
	}

	if err := uc.DeleteModelItem(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := uc.DeleteModelItem(ctx, created.ID); err == nil {
		t.Fatal("expected not-found deleting twice")
	}
}

func TestPriceTableAndItemCRUD(t *testing.T) {
	uc := newTestGatewayForPricing(t)
	ctx := context.Background()

	table, err := uc.CreatePriceTable(ctx, &dto.CreatePriceTableReq{Name: "resale-standard"})
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	if table.Currency != "CNY" {
		t.Fatalf("expected default currency CNY, got %s", table.Currency)
	}
	if _, err := uc.CreatePriceTable(ctx, &dto.CreatePriceTableReq{Name: "resale-standard"}); err == nil {
		t.Fatal("expected duplicate name to fail")
	}

	item, err := uc.CreatePriceTableItem(ctx, &dto.CreatePriceTableItemReq{
		PriceTableID: table.ID, ModelPattern: "gpt-4o.*", InputPricePerMillion: 10, OutputPricePerMillion: 30,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	tables, err := uc.ListPriceTables(ctx)
	if err != nil || len(tables) != 1 || len(tables[0].Items) != 1 {
		t.Fatalf("list tables: %v, %+v", err, tables)
	}

	// resolving through the regex item populates and then the cache must be
	// invalidated on update so a subsequent resolve sees the new price.
	entry := getSellPriceEntry(ctx, uc.db, uc.logger, &table.ID, 0, "gpt-4o-mini")
	if entry == nil || entry.inputPrice != 10 {
		t.Fatalf("expected regex match pricing, got %+v", entry)
	}
	newInput := 20.0
	if _, err := uc.UpdatePriceTableItem(ctx, &dto.UpdatePriceTableItemReq{ID: item.ID, InputPricePerMillion: &newInput}); err != nil {
		t.Fatalf("update item: %v", err)
	}
	entry = getSellPriceEntry(ctx, uc.db, uc.logger, &table.ID, 0, "gpt-4o-mini")
	if entry == nil || entry.inputPrice != 20 {
		t.Fatalf("expected cache invalidation to pick up new price, got %+v", entry)
	}

	if err := uc.DeletePriceTableItem(ctx, item.ID); err != nil {
		t.Fatalf("delete item: %v", err)
	}
	if err := uc.DeletePriceTable(ctx, table.ID); err != nil {
		t.Fatalf("delete table: %v", err)
	}
}

func TestPatternTester(t *testing.T) {
	models := []string{"gpt-4o", "gpt-4o-mini", "claude-3-5-sonnet"}

	exact := TestPattern(&dto.PatternTestReq{Pattern: "gpt-4o", Models: models})
	if len(exact.Matched) != 1 || exact.Matched[0] != "gpt-4o" || exact.IsRegex {
		t.Fatalf("exact match wrong: %+v", exact)
	}

	regex := TestPattern(&dto.PatternTestReq{Pattern: "gpt-4o.*", Models: models})
	if len(regex.Matched) != 2 || !regex.IsRegex {
		t.Fatalf("regex match wrong: %+v", regex)
	}

	none := TestPattern(&dto.PatternTestReq{Pattern: "nonexistent", Models: models})
	if len(none.Matched) != 0 {
		t.Fatalf("expected no matches, got %+v", none)
	}
}
