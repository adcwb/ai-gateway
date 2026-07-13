package biz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/go-kratos/kratos/v2/log"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/adcwb/ai-gateway/internal/conf"
	"github.com/adcwb/ai-gateway/internal/data/model"
	"github.com/adcwb/ai-gateway/internal/pkg"
)

func newTestGatewayForBatch(t *testing.T) (*GatewayUseCase, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(
		&model.AIProvider{}, &model.AIVirtualKey{}, &model.AITenant{},
		&model.AIGatewayAuditLog{}, &model.AIGatewayAuditLogBody{},
		&model.AIProxyFile{}, &model.AIBatchJob{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	audit := NewAuditWorker(db, nil, nil, &conf.Audit{}, sysCfg, log.NewStdLogger(testWriter{t}))
	audit.Start(context.Background())
	uc := NewGatewayUseCase(db, nil, nil, audit, nil, nil, nil, &conf.AI{AgentTimeoutSec: 5}, sysCfg, log.NewStdLogger(testWriter{t}))
	return uc, db
}

func seedBatchProvider(t *testing.T, db *gorm.DB, sysCfg *conf.System, baseURL string) *model.AIProvider {
	t.Helper()
	encKey, err := pkg.EncryptAES("test-upstream-key", []byte(sysCfg.EncryptionKey))
	if err != nil {
		t.Fatalf("encrypt api key: %v", err)
	}
	p := &model.AIProvider{Name: "batch-provider", BaseURL: baseURL, ProviderType: model.ProviderTypeOpenAICompatible, APIKey: encKey, IsEnabled: true}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	return p
}

func TestProxyFilesUpload_SeedsShadowRowAndPassesThrough(t *testing.T) {
	uc, db := newTestGatewayForBatch(t)
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/files" {
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"file-abc","filename":"batch.jsonl","purpose":"batch","bytes":42}`))
	}))
	defer upstream.Close()
	provider := seedBatchProvider(t, db, sysCfg, upstream.URL)

	key := &model.AIVirtualKey{Name: "k1"}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/ai/v1/files", strings.NewReader("fake multipart body"))
	req.Header.Set("X-AIGW-Provider", provider.Name)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	w := httptest.NewRecorder()
	uc.ProxyFilesUpload(context.Background(), key, w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var row model.AIProxyFile
	if err := db.First(&row, "id = ?", "file-abc").Error; err != nil {
		t.Fatalf("expected shadow row to be created: %v", err)
	}
	if row.ProviderID != provider.ID || row.Filename != "batch.jsonl" || row.Bytes != 42 {
		t.Fatalf("shadow row wrong: %+v", row)
	}
}

func TestProxyFilesUpload_MissingProviderHeaderRejected(t *testing.T) {
	uc, db := newTestGatewayForBatch(t)
	key := &model.AIVirtualKey{Name: "k1"}
	db.Create(key)

	req := httptest.NewRequest(http.MethodPost, "/ai/v1/files", strings.NewReader("x"))
	w := httptest.NewRecorder()
	uc.ProxyFilesUpload(context.Background(), key, w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without X-AIGW-Provider, got %d", w.Code)
	}
}

func TestProxyFilesGetAndDelete_UseShadowRowForProviderRouting(t *testing.T) {
	uc, db := newTestGatewayForBatch(t)
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	var gotDeletePath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{"id":"file-xyz","filename":"x.jsonl"}`))
		case http.MethodDelete:
			gotDeletePath = r.URL.Path
			w.Write([]byte(`{"id":"file-xyz","deleted":true}`))
		}
	}))
	defer upstream.Close()
	provider := seedBatchProvider(t, db, sysCfg, upstream.URL)
	key := &model.AIVirtualKey{Name: "k1"}
	db.Create(key)
	db.Create(&model.AIProxyFile{ID: "file-xyz", VirtualKeyID: key.ID, ProviderID: provider.ID})

	// GET does not need X-AIGW-Provider — it's resolved from the shadow row.
	req := httptest.NewRequest(http.MethodGet, "/ai/v1/files/file-xyz", nil)
	w := httptest.NewRecorder()
	uc.ProxyFilesGet(context.Background(), key, "file-xyz", w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d", w.Code)
	}

	req2 := httptest.NewRequest(http.MethodDelete, "/ai/v1/files/file-xyz", nil)
	w2 := httptest.NewRecorder()
	uc.ProxyFilesDelete(context.Background(), key, "file-xyz", w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d", w2.Code)
	}
	if gotDeletePath != "/v1/files/file-xyz" {
		t.Fatalf("unexpected upstream delete path: %s", gotDeletePath)
	}
	var count int64
	db.Model(&model.AIProxyFile{}).Where("id = ?", "file-xyz").Count(&count)
	if count != 0 {
		t.Fatal("expected shadow row to be removed after successful delete")
	}
}

func TestProxyBatchesCreate_SeedsShadowRow(t *testing.T) {
	uc, db := newTestGatewayForBatch(t)
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"batch-1","endpoint":"/v1/chat/completions","input_file_id":"file-1","status":"validating","completion_window":"24h"}`))
	}))
	defer upstream.Close()
	provider := seedBatchProvider(t, db, sysCfg, upstream.URL)
	key := &model.AIVirtualKey{Name: "k1"}
	db.Create(key)

	req := httptest.NewRequest(http.MethodPost, "/ai/v1/batches", strings.NewReader(`{"input_file_id":"file-1","endpoint":"/v1/chat/completions","completion_window":"24h"}`))
	req.Header.Set("X-AIGW-Provider", provider.Name)
	w := httptest.NewRecorder()
	uc.ProxyBatchesCreate(context.Background(), key, []byte(`{"input_file_id":"file-1","endpoint":"/v1/chat/completions","completion_window":"24h"}`), w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var job model.AIBatchJob
	if err := db.First(&job, "id = ?", "batch-1").Error; err != nil {
		t.Fatalf("expected shadow batch row: %v", err)
	}
	if job.Status != "validating" || job.ProviderID != provider.ID {
		t.Fatalf("shadow batch row wrong: %+v", job)
	}
}

func TestProxyBatchesGet_ReplaysShadowRowWithoutCallingUpstream(t *testing.T) {
	uc, db := newTestGatewayForBatch(t)
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer upstream.Close()
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	provider := seedBatchProvider(t, db, sysCfg, upstream.URL)
	key := &model.AIVirtualKey{Name: "k1"}
	db.Create(key)
	db.Create(&model.AIBatchJob{ID: "batch-2", VirtualKeyID: key.ID, ProviderID: provider.ID, Status: "in_progress", RawUpstream: []byte(`{"id":"batch-2","status":"in_progress"}`)})

	req := httptest.NewRequest(http.MethodGet, "/ai/v1/batches/batch-2", nil)
	w := httptest.NewRecorder()
	uc.ProxyBatchesGet(context.Background(), key, "batch-2", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "in_progress" {
		t.Fatalf("expected replayed shadow row body, got %v", body)
	}
	if called {
		t.Fatal("expected ProxyBatchesGet to answer from the shadow row, not call upstream")
	}
}
