package vectorindex

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

// TestEncodeVectorRoundTrip verifies the FLOAT32 little-endian blob encoding
// RediSearch expects for vector fields/query params.
func TestEncodeVectorRoundTrip(t *testing.T) {
	v := []float32{1.0, -2.5, 0, 3.25, -0.001}
	buf := encodeVector(v)
	if len(buf) != len(v)*4 {
		t.Fatalf("expected %d bytes, got %d", len(v)*4, len(buf))
	}
	decoded := decodeVectorForTest(buf)
	for i := range v {
		if decoded[i] != v[i] {
			t.Fatalf("index %d: expected %v, got %v", i, v[i], decoded[i])
		}
	}
}

func decodeVectorForTest(buf []byte) []float32 {
	out := make([]float32, len(buf)/4)
	for i := range out {
		bits := uint32(buf[i*4]) | uint32(buf[i*4+1])<<8 | uint32(buf[i*4+2])<<16 | uint32(buf[i*4+3])<<24
		out[i] = math.Float32frombits(bits)
	}
	return out
}

// TestRedisIndex_AvailableFalseOnPlainRedis exercises the real
// capability-detection / auto-degrade path: miniredis has no RediSearch
// module, so FT._LIST must fail and Available() must report false rather
// than erroring — this is the documented "old Redis keeps exact-cache-only"
// behavior, verified against a real (mini)redis instance, not a mock.
func TestRedisIndex_AvailableFalseOnPlainRedis(t *testing.T) {
	rdb := newTestRedis(t)
	idx := NewRedisIndex(rdb, 4)
	if idx.Available(context.Background()) {
		t.Fatal("expected Available()==false against a RediSearch-less Redis")
	}
}

func TestRedisIndex_AvailableCachesResult(t *testing.T) {
	rdb := newTestRedis(t)
	idx := NewRedisIndex(rdb, 4)
	ctx := context.Background()
	if idx.Available(ctx) {
		t.Fatal("expected false")
	}
	// Flip the cached flag directly and confirm Available() serves the cache
	// rather than re-probing within the TTL window.
	idx.availMu.Lock()
	idx.availCached = true
	idx.availUntil = time.Now().Add(time.Minute)
	idx.availMu.Unlock()
	if !idx.Available(ctx) {
		t.Fatal("expected cached true to be served without re-probing")
	}
}

func TestRedisIndex_UpsertSearchUnavailableIndexReturnsError(t *testing.T) {
	rdb := newTestRedis(t)
	idx := NewRedisIndex(rdb, 3)
	err := idx.Upsert(context.Background(), "scope1", "id1", []float32{1, 2, 3}, []byte("meta"), 60)
	if err == nil {
		t.Fatal("expected Upsert to fail without RediSearch support")
	}
}

func TestRedisIndex_DimMismatchRejected(t *testing.T) {
	rdb := newTestRedis(t)
	idx := NewRedisIndex(rdb, 3)
	if err := idx.Upsert(context.Background(), "s", "id", []float32{1, 2}, nil, 60); err == nil {
		t.Fatal("expected dim mismatch error")
	}
	if _, err := idx.Search(context.Background(), "s", []float32{1, 2}, 5); err == nil {
		t.Fatal("expected dim mismatch error")
	}
}

func TestParseFTSearchReply(t *testing.T) {
	raw := []interface{}{
		int64(1),
		"doc1",
		[]interface{}{"meta", `{"body":"hi"}`, "score", "0.02"},
	}
	matches, err := parseFTSearchReply(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].ID != "doc1" {
		t.Fatalf("unexpected id %q", matches[0].ID)
	}
	if got := matches[0].Score; got < 0.97 || got > 0.99 {
		t.Fatalf("expected similarity ~0.98 (1-distance), got %v", got)
	}
	if string(matches[0].Metadata) != `{"body":"hi"}` {
		t.Fatalf("unexpected metadata %q", matches[0].Metadata)
	}
}

func TestParseFTSearchReply_Empty(t *testing.T) {
	matches, err := parseFTSearchReply([]interface{}{int64(0)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no matches, got %d", len(matches))
	}
}
