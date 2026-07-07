package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/opscenter/ai-gateway/internal/biz/vectorindex"
	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data/model"
	"github.com/opscenter/ai-gateway/internal/pkg"
)

// -----------------------------------------------------------------------------
// extractEmbeddingText / semanticScopeDigest — pure functions, no DB/Redis.
// -----------------------------------------------------------------------------

func TestExtractEmbeddingText(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{"chat last user message", `{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"first"},{"role":"assistant","content":"reply"},{"role":"user","content":"second"}]}`, "second", true},
		{"embeddings input", `{"model":"m","input":"embed me"}`, "embed me", true},
		{"empty user content", `{"messages":[{"role":"user","content":""}]}`, "", false},
		{"no user message", `{"messages":[{"role":"assistant","content":"hi"}]}`, "", false},
		{"malformed json", `not json`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractEmbeddingText([]byte(tc.body))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSemanticScopeDigest_PromptIndependentButParamsAndModelSensitive(t *testing.T) {
	bodyHello := []byte(`{"messages":[{"role":"user","content":"hello"}],"temperature":0.2}`)
	bodyBye := []byte(`{"messages":[{"role":"user","content":"goodbye entirely different text"}],"temperature":0.2}`)
	bodyHotterTemp := []byte(`{"messages":[{"role":"user","content":"hello"}],"temperature":0.9}`)

	s1, ok1 := semanticScopeDigest(1, "gpt-x", bodyHello)
	s2, ok2 := semanticScopeDigest(1, "gpt-x", bodyBye)
	if !ok1 || !ok2 {
		t.Fatalf("expected both digests to succeed")
	}
	if s1 != s2 {
		t.Fatalf("expected scope to be prompt-independent: %q != %q", s1, s2)
	}

	s3, ok3 := semanticScopeDigest(1, "gpt-x", bodyHotterTemp)
	if !ok3 || s3 == s1 {
		t.Fatalf("expected different generation params to change scope")
	}

	s4, ok4 := semanticScopeDigest(2, "gpt-x", bodyHello)
	if !ok4 || s4 == s1 {
		t.Fatalf("expected different tenant to change scope")
	}

	s5, ok5 := semanticScopeDigest(1, "gpt-y", bodyHello)
	if !ok5 || s5 == s1 {
		t.Fatalf("expected different resolved model to change scope")
	}
}

// -----------------------------------------------------------------------------
// generateEmbedding — real HTTP round trip against a fake OpenAI-compatible
// embeddings endpoint, through the same buildUpstreamRequest dialect code the
// main proxy path uses.
// -----------------------------------------------------------------------------

func newTestGatewayForSemanticCache(t *testing.T) (*GatewayUseCase, *gorm.DB) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.AIProvider{}, &model.AISetting{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	uc := NewGatewayUseCase(db, rdb, nil, nil, nil, nil, nil,
		&conf.AI{}, &conf.System{EncryptionKey: testEncryptionKey[:32]}, log.NewStdLogger(testWriter{t}))
	return uc, db
}

func seedEmbeddingProvider(t *testing.T, uc *GatewayUseCase, db *gorm.DB, baseURL string) uint {
	t.Helper()
	encKey, err := pkg.EncryptAES("test-embed-key", []byte(uc.sysCfg.EncryptionKey))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	p := &model.AIProvider{
		Name: "embed-provider", BaseURL: baseURL, ProviderType: model.ProviderTypeOpenAICompatible,
		APIKey: encKey, IsEnabled: true, Weight: 100,
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	return p.ID
}

func configureEmbeddingSettings(ctx context.Context, uc *GatewayUseCase, providerID uint, modelName string, dim int) {
	uc.setSetting(ctx, model.SettingKeyCacheEmbeddingProviderID, fmt.Sprintf("%d", providerID))
	uc.setSetting(ctx, model.SettingKeyCacheEmbeddingModel, modelName)
	uc.setSetting(ctx, model.SettingKeyCacheEmbeddingDim, fmt.Sprintf("%d", dim))
}

// fakeEmbeddingServer returns a canned vector per exact "input" string match,
// so tests can construct near-duplicate / orthogonal pairs deterministically.
func fakeEmbeddingServer(t *testing.T, vectors map[string][]float32) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		vec, ok := vectors[req.Input]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resp := map[string]interface{}{
			"data":  []map[string]interface{}{{"embedding": vec}},
			"usage": map[string]int{"prompt_tokens": 3},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestGenerateEmbedding_RoundTrip(t *testing.T) {
	uc, db := newTestGatewayForSemanticCache(t)
	srv, calls := fakeEmbeddingServer(t, map[string][]float32{"hello": {1, 0, 0, 0}})
	providerID := seedEmbeddingProvider(t, uc, db, srv.URL)
	ctx := context.Background()
	configureEmbeddingSettings(ctx, uc, providerID, "test-embed", 4)

	vec, err := uc.generateEmbedding(ctx, 1, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 4 || vec[0] != 1 {
		t.Fatalf("unexpected vector: %v", vec)
	}
	if *calls != 1 {
		t.Fatalf("expected exactly 1 HTTP call, got %d", *calls)
	}
}

func TestGenerateEmbedding_NotConfiguredErrors(t *testing.T) {
	uc, _ := newTestGatewayForSemanticCache(t)
	if _, err := uc.generateEmbedding(context.Background(), 1, "hello"); err == nil {
		t.Fatal("expected error when no embedding provider is configured")
	}
}

func TestGenerateEmbedding_UpstreamErrorStatus(t *testing.T) {
	uc, db := newTestGatewayForSemanticCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	providerID := seedEmbeddingProvider(t, uc, db, srv.URL)
	ctx := context.Background()
	configureEmbeddingSettings(ctx, uc, providerID, "test-embed", 4)
	if _, err := uc.generateEmbedding(ctx, 1, "hello"); err == nil {
		t.Fatal("expected error on non-200 upstream response")
	}
}

// -----------------------------------------------------------------------------
// semanticCacheLookup / semanticCacheStore — using a fake in-memory Index so
// the decision logic (embed -> scope -> search -> threshold) is verified
// without needing a real RediSearch server.
// -----------------------------------------------------------------------------

type fakeVectorDoc struct {
	id   string
	vec  []float32
	meta []byte
}

type fakeVectorIndex struct {
	mu   sync.Mutex
	docs map[string][]fakeVectorDoc
}

func (f *fakeVectorIndex) Available(ctx context.Context) bool { return true }

func (f *fakeVectorIndex) Upsert(ctx context.Context, scope, id string, vector []float32, metadata []byte, ttlSeconds int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.docs == nil {
		f.docs = map[string][]fakeVectorDoc{}
	}
	f.docs[scope] = append(f.docs[scope], fakeVectorDoc{id: id, vec: vector, meta: metadata})
	return nil
}

func (f *fakeVectorIndex) Search(ctx context.Context, scope string, vector []float32, topK int) ([]vectorindex.Match, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var best *fakeVectorDoc
	bestSim := float32(-2)
	for i := range f.docs[scope] {
		d := &f.docs[scope][i]
		sim := cosineSimilarity(d.vec, vector)
		if sim > bestSim {
			bestSim = sim
			best = d
		}
	}
	if best == nil {
		return nil, nil
	}
	return []vectorindex.Match{{ID: best.id, Score: bestSim, Metadata: best.meta}}, nil
}

func (f *fakeVectorIndex) Flush(ctx context.Context, scope string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.docs, scope)
	return nil
}

func cosineSimilarity(a, b []float32) float32 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

func setupSemanticLookupHarness(t *testing.T) (*GatewayUseCase, *fakeVectorIndex, *int32) {
	t.Helper()
	uc, db := newTestGatewayForSemanticCache(t)
	srv, calls := fakeEmbeddingServer(t, map[string][]float32{
		"hello":            {1, 0, 0, 0},
		"hi there":         {0.99, 0.14, 0, 0}, // near-parallel to "hello" -> cosine > 0.95
		"goodbye friend":   {0, 1, 0, 0},       // orthogonal -> cosine 0
	})
	providerID := seedEmbeddingProvider(t, uc, db, srv.URL)
	ctx := context.Background()
	configureEmbeddingSettings(ctx, uc, providerID, "test-embed", 4)

	fake := &fakeVectorIndex{}
	uc.vectorIndex = fake
	uc.vectorIndexDim = 4
	return uc, fake, calls
}

func TestSemanticCacheLookup_DisabledIsNoOp(t *testing.T) {
	uc, _, calls := setupSemanticLookupHarness(t)
	cfg := keyCacheConfig{SemanticEnabled: false, SemanticThreshold: 0.95, SemanticTTLSec: 3600}
	body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	state, hit, _ := uc.semanticCacheLookup(context.Background(), 1, "gpt-x", cfg, body)
	if state != nil || hit != nil {
		t.Fatal("expected no-op when SemanticEnabled is false")
	}
	if *calls != 0 {
		t.Fatalf("expected no embedding HTTP call, got %d", *calls)
	}
}

func TestSemanticCacheLookup_StoreThenParaphraseHits(t *testing.T) {
	uc, _, _ := setupSemanticLookupHarness(t)
	ctx := context.Background()
	cfg := keyCacheConfig{SemanticEnabled: true, SemanticThreshold: 0.95, SemanticTTLSec: 3600}

	body1 := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	state1, hit1, _ := uc.semanticCacheLookup(ctx, 1, "gpt-x", cfg, body1)
	if hit1 != nil {
		t.Fatal("expected a miss against an empty index")
	}
	if state1 == nil || len(state1.embedding) == 0 {
		t.Fatal("expected a computed embedding to carry into store")
	}
	stored := &cachedResponse{Body: json.RawMessage(`{"answer":"hi!"}`), Prompt: 5, Completion: 2, ProviderID: 9, Model: "gpt-x"}
	uc.semanticCacheStore(ctx, state1, stored)

	body2 := []byte(`{"messages":[{"role":"user","content":"hi there"}]}`)
	_, hit2, sim2 := uc.semanticCacheLookup(ctx, 1, "gpt-x", cfg, body2)
	if hit2 == nil {
		t.Fatalf("expected a semantic hit for a near-duplicate prompt (sim=%v)", sim2)
	}
	if string(hit2.Body) != `{"answer":"hi!"}` {
		t.Fatalf("unexpected cached body: %s", hit2.Body)
	}
	if sim2 < 0.95 {
		t.Fatalf("expected similarity >= threshold, got %v", sim2)
	}

	body3 := []byte(`{"messages":[{"role":"user","content":"goodbye friend"}]}`)
	_, hit3, sim3 := uc.semanticCacheLookup(ctx, 1, "gpt-x", cfg, body3)
	if hit3 != nil {
		t.Fatalf("expected orthogonal prompt to miss (sim=%v)", sim3)
	}
}

func TestSemanticCacheLookup_ScopeIsolatesByModel(t *testing.T) {
	uc, _, _ := setupSemanticLookupHarness(t)
	ctx := context.Background()
	cfg := keyCacheConfig{SemanticEnabled: true, SemanticThreshold: 0.95, SemanticTTLSec: 3600}

	body1 := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	state1, _, _ := uc.semanticCacheLookup(ctx, 1, "gpt-x", cfg, body1)
	uc.semanticCacheStore(ctx, state1, &cachedResponse{Body: json.RawMessage(`{"a":1}`)})

	body2 := []byte(`{"messages":[{"role":"user","content":"hi there"}]}`)
	_, hitSameModel, _ := uc.semanticCacheLookup(ctx, 1, "gpt-x", cfg, body2)
	if hitSameModel == nil {
		t.Fatal("expected a hit within the same tenant+model scope")
	}
	_, hitOtherModel, _ := uc.semanticCacheLookup(ctx, 1, "gpt-y", cfg, body2)
	if hitOtherModel != nil {
		t.Fatal("expected a different resolved model to miss (separate scope)")
	}
}
