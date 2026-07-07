package biz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

func TestPollAndSettleBatchJob_CompletesAndMarksSettled(t *testing.T) {
	uc, db := newTestGatewayForBatch(t)
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/batches/batch-9":
			w.Write([]byte(`{"id":"batch-9","status":"completed","output_file_id":"file-out","request_counts":{"total":2,"completed":2,"failed":0}}`))
		case r.URL.Path == "/v1/files/file-out/content":
			w.Write([]byte(
				`{"response":{"body":{"model":"gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":2}}}}}` + "\n" +
					`{"response":{"body":{"model":"gpt-4o","usage":{"prompt_tokens":8,"completion_tokens":4}}}}` + "\n",
			))
		default:
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	provider := seedBatchProvider(t, db, sysCfg, upstream.URL)

	key := &model.AIVirtualKey{Name: "k1"}
	db.Create(key)
	job := model.AIBatchJob{ID: "batch-9", VirtualKeyID: key.ID, ProviderID: provider.ID, Status: "in_progress"}
	db.Create(&job)

	uc.pollAndSettleBatchJob(context.Background(), job)

	var updated model.AIBatchJob
	if err := db.First(&updated, "id = ?", "batch-9").Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if updated.Status != "completed" {
		t.Fatalf("status = %q, want completed", updated.Status)
	}
	if updated.OutputFileID != "file-out" {
		t.Fatalf("output_file_id = %q", updated.OutputFileID)
	}
	if updated.SettledAt == nil {
		t.Fatal("expected settled_at to be set after successful settlement")
	}
	if updated.CompletedAt == nil {
		t.Fatal("expected completed_at to be set on terminal status")
	}
}

func TestPollAndSettleBatchJob_NonTerminalStatusLeftUnsettled(t *testing.T) {
	uc, db := newTestGatewayForBatch(t)
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"batch-10","status":"in_progress"}`))
	}))
	defer upstream.Close()
	provider := seedBatchProvider(t, db, sysCfg, upstream.URL)

	key := &model.AIVirtualKey{Name: "k1"}
	db.Create(key)
	job := model.AIBatchJob{ID: "batch-10", VirtualKeyID: key.ID, ProviderID: provider.ID, Status: "validating"}
	db.Create(&job)

	uc.pollAndSettleBatchJob(context.Background(), job)

	var updated model.AIBatchJob
	db.First(&updated, "id = ?", "batch-10")
	if updated.Status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", updated.Status)
	}
	if updated.SettledAt != nil {
		t.Fatal("non-terminal status must not be marked settled")
	}
}

func TestPollAndSettleBatchJob_FailedStatusMarkedSettledWithoutBilling(t *testing.T) {
	uc, db := newTestGatewayForBatch(t)
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"batch-11","status":"failed"}`))
	}))
	defer upstream.Close()
	provider := seedBatchProvider(t, db, sysCfg, upstream.URL)

	key := &model.AIVirtualKey{Name: "k1"}
	db.Create(key)
	job := model.AIBatchJob{ID: "batch-11", VirtualKeyID: key.ID, ProviderID: provider.ID, Status: "in_progress"}
	db.Create(&job)

	uc.pollAndSettleBatchJob(context.Background(), job)

	var updated model.AIBatchJob
	db.First(&updated, "id = ?", "batch-11")
	if updated.Status != "failed" {
		t.Fatalf("status = %q, want failed", updated.Status)
	}
	if updated.SettledAt == nil {
		t.Fatal("expected a terminal failed status to be marked settled (nothing to bill)")
	}
}

func TestSweepBatchJobs_SkipsAlreadySettledJobs(t *testing.T) {
	uc, db := newTestGatewayForBatch(t)
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer upstream.Close()
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	provider := seedBatchProvider(t, db, sysCfg, upstream.URL)
	key := &model.AIVirtualKey{Name: "k1"}
	db.Create(key)

	now := "2024-01-01 00:00:00"
	db.Exec("INSERT INTO ai_batch_jobs (id, virtual_key_id, provider_id, status, created_at, settled_at) VALUES (?, ?, ?, ?, ?, ?)",
		"batch-done", key.ID, provider.ID, "completed", now, now)

	uc.sweepBatchJobs(context.Background())
	if called {
		t.Fatal("expected sweepBatchJobs to skip a job that already has settled_at set")
	}
}
