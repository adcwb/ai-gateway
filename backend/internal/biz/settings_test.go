package biz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

func newTestGatewayForSettings(t *testing.T, sysCfg *conf.System) *GatewayUseCase {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.AISetting{}, &model.AICreditsRate{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if sysCfg == nil {
		sysCfg = &conf.System{}
	}
	return NewGatewayUseCase(db, nil, nil, nil, nil, nil, nil, &conf.AI{}, sysCfg, log.NewStdLogger(testWriter{t}))
}

func TestSettingsFallsBackToStaticConfigThenOverride(t *testing.T) {
	uc := newTestGatewayForSettings(t, &conf.System{AlertWebhook: "https://static.example/hook"})
	ctx := context.Background()

	got := uc.GetSettings(ctx)
	if got.AlertWebhook != "https://static.example/hook" || got.AlertWebhookIsOverride {
		t.Fatalf("expected static fallback, got %+v", got)
	}

	override := "https://override.example/hook"
	if _, err := uc.UpdateSettings(ctx, &dto.UpdateSettingsReq{AlertWebhook: &override}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got = uc.GetSettings(ctx)
	if got.AlertWebhook != override || !got.AlertWebhookIsOverride {
		t.Fatalf("expected override to take precedence, got %+v", got)
	}

	cleared := ""
	if _, err := uc.UpdateSettings(ctx, &dto.UpdateSettingsReq{AlertWebhook: &cleared}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got = uc.GetSettings(ctx)
	if got.AlertWebhook != "https://static.example/hook" || got.AlertWebhookIsOverride {
		t.Fatalf("expected fallback after clearing override, got %+v", got)
	}
}

func TestTestAlertWebhookDeliversAndReportsFailure(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	uc := newTestGatewayForSettings(t, nil)
	ctx := context.Background()

	if err := uc.TestAlertWebhook(ctx); err == nil {
		t.Fatal("expected error when no webhook is configured")
	}

	url := srv.URL
	if _, err := uc.UpdateSettings(ctx, &dto.UpdateSettingsReq{AlertWebhook: &url}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := uc.TestAlertWebhook(ctx); err != nil {
		t.Fatalf("expected successful delivery, got %v", err)
	}
	if hits != 1 {
		t.Fatalf("expected exactly one delivery, got %d", hits)
	}
}

func TestCreditsRateCRUD(t *testing.T) {
	uc := newTestGatewayForSettings(t, nil)
	ctx := context.Background()

	created, err := uc.CreateCreditsRate(ctx, &dto.CreateCreditsRateReq{Currency: "usd", RatePerCredit: 0.14})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Currency != "USD" {
		t.Fatalf("expected currency normalized to uppercase, got %s", created.Currency)
	}
	if _, err := uc.CreateCreditsRate(ctx, &dto.CreateCreditsRateReq{Currency: "USD", RatePerCredit: 1}); err == nil {
		t.Fatal("expected duplicate currency to fail")
	}
	if _, err := uc.CreateCreditsRate(ctx, &dto.CreateCreditsRateReq{Currency: "EUR", RatePerCredit: 0}); err == nil {
		t.Fatal("expected non-positive rate to fail")
	}

	newRate := 0.2
	updated, err := uc.UpdateCreditsRate(ctx, &dto.UpdateCreditsRateReq{ID: created.ID, RatePerCredit: &newRate})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.RatePerCredit != 0.2 {
		t.Fatalf("rate not updated: %+v", updated)
	}

	list, err := uc.ListCreditsRates(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v, len=%d", err, len(list))
	}

	if err := uc.DeleteCreditsRate(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := uc.DeleteCreditsRate(ctx, created.ID); err == nil {
		t.Fatal("expected not-found deleting twice")
	}
}
