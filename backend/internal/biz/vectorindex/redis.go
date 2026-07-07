package vectorindex

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisIndex implements Index on top of RediSearch (Redis Stack / Redis >= 8's
// bundled search module) vector fields. It is the shipped default per the
// design's vector-backend ADR: it rides the project's existing Redis
// dependency instead of adding a new one, at the cost of requiring a
// search-capable Redis — deployments on plain Redis keep exact-cache-only,
// detected automatically (never a startup failure).
//
// One FT index is created lazily per distinct scope (not per tenant): this
// keeps the implementation free of any tenant-shaped assumptions (Index is a
// generic scope->vectors store) at the cost of more, smaller indices than the
// design doc's illustrative "one index per tenant" key convention — acceptable
// since the index count is bounded by (tenant x model x distinct param
// combos), not by request volume.
type RedisIndex struct {
	rdb *redis.Client
	dim int

	availMu     sync.Mutex
	availUntil  time.Time
	availCached bool

	knownIdx sync.Map // scope -> struct{}, indices already FT.CREATE'd this process
}

const (
	availabilityProbeTTL = 60 * time.Second
	indexNamePrefix      = "ai:gw:cache:vidx:"
	docKeyPrefixFmt      = "ai:gw:cache:vdoc:%s:" // %s = scope
)

// NewRedisIndex constructs a RedisIndex. dim is the fixed embedding
// dimensionality (must match the configured embedding model) — every vector
// passed to Upsert/Search must have exactly this many components.
func NewRedisIndex(rdb *redis.Client, dim int) *RedisIndex {
	return &RedisIndex{rdb: rdb, dim: dim}
}

// Available probes RediSearch support via FT._LIST — a read-only, no-op-if-
// absent command — and caches the result briefly so capability detection
// doesn't cost a Redis round trip on every request. Re-probing periodically
// (rather than once at startup) means a live Redis upgrade to a search-capable
// version is picked up without a gateway restart.
func (r *RedisIndex) Available(ctx context.Context) bool {
	if r == nil || r.rdb == nil || r.dim <= 0 {
		return false
	}
	r.availMu.Lock()
	if time.Now().Before(r.availUntil) {
		cached := r.availCached
		r.availMu.Unlock()
		return cached
	}
	r.availMu.Unlock()

	pctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	_, err := r.rdb.Do(pctx, "FT._LIST").Result()
	ok := err == nil

	r.availMu.Lock()
	r.availCached = ok
	r.availUntil = time.Now().Add(availabilityProbeTTL)
	r.availMu.Unlock()
	return ok
}

func indexName(scope string) string { return indexNamePrefix + scope }
func docKeyPrefix(scope string) string { return fmt.Sprintf(docKeyPrefixFmt, scope) }
func docKey(scope, id string) string   { return docKeyPrefix(scope) + id }

// ensureIndex lazily FT.CREATEs the per-scope index on first use. Errors are
// swallowed into a false return (treated as unavailable by the caller) except
// "Index already exists", which is the expected steady state.
func (r *RedisIndex) ensureIndex(ctx context.Context, scope string) bool {
	if _, ok := r.knownIdx.Load(scope); ok {
		return true
	}
	name := indexName(scope)
	_, err := r.rdb.Do(ctx, "FT.CREATE", name,
		"ON", "HASH",
		"PREFIX", "1", docKeyPrefix(scope),
		"SCHEMA",
		"vec", "VECTOR", "HNSW", "6",
		"TYPE", "FLOAT32",
		"DIM", strconv.Itoa(r.dim),
		"DISTANCE_METRIC", "COSINE",
		"meta", "TEXT", "NOINDEX",
	).Result()
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return false
	}
	r.knownIdx.Store(scope, struct{}{})
	return true
}

func encodeVector(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// Upsert stores one vector as a Redis hash document (vec + meta fields) under
// the scope's index prefix, then applies the TTL directly on the key.
func (r *RedisIndex) Upsert(ctx context.Context, scope, id string, vector []float32, metadata []byte, ttlSeconds int) error {
	if r == nil || r.rdb == nil {
		return fmt.Errorf("vectorindex: nil backend")
	}
	if len(vector) != r.dim {
		return fmt.Errorf("vectorindex: vector dim %d != configured dim %d", len(vector), r.dim)
	}
	if !r.ensureIndex(ctx, scope) {
		return fmt.Errorf("vectorindex: index unavailable for scope %s", scope)
	}
	key := docKey(scope, id)
	if err := r.rdb.HSet(ctx, key, map[string]interface{}{
		"vec":  encodeVector(vector),
		"meta": metadata,
	}).Err(); err != nil {
		return err
	}
	if ttlSeconds > 0 {
		r.rdb.Expire(ctx, key, time.Duration(ttlSeconds)*time.Second)
	}
	return nil
}

// ftSearchResult is the shape of a raw FT.SEARCH reply parsed via the generic
// interface{} decoding go-redis's Do() returns for RESP2 array replies.
func (r *RedisIndex) Search(ctx context.Context, scope string, vector []float32, topK int) ([]Match, error) {
	if r == nil || r.rdb == nil {
		return nil, fmt.Errorf("vectorindex: nil backend")
	}
	if len(vector) != r.dim {
		return nil, fmt.Errorf("vectorindex: vector dim %d != configured dim %d", len(vector), r.dim)
	}
	if _, ok := r.knownIdx.Load(scope); !ok {
		// Nothing has ever been upserted for this scope in this process —
		// the index may not exist yet; a fresh Redis also 404s FT.SEARCH
		// against a missing index, so short-circuit to a clean miss.
		return nil, nil
	}
	name := indexName(scope)
	query := fmt.Sprintf("*=>[KNN %d @vec $BLOB AS score]", topK)
	raw, err := r.rdb.Do(ctx, "FT.SEARCH", name, query,
		"PARAMS", "2", "BLOB", encodeVector(vector),
		"SORTBY", "score",
		"RETURN", "1", "meta",
		"DIALECT", "2",
	).Result()
	if err != nil {
		return nil, err
	}
	return parseFTSearchReply(raw)
}

// parseFTSearchReply decodes FT.SEARCH's flat reply shape:
// [total, docID1, [field, value, ...], docID2, [field, value, ...], ...]
func parseFTSearchReply(raw interface{}) ([]Match, error) {
	items, ok := raw.([]interface{})
	if !ok || len(items) < 1 {
		return nil, nil
	}
	var matches []Match
	for i := 1; i+1 < len(items); i += 2 {
		docID, _ := items[i].(string)
		fields, _ := items[i+1].([]interface{})
		var meta []byte
		var score float32
		for j := 0; j+1 < len(fields); j += 2 {
			fname, _ := fields[j].(string)
			switch fname {
			case "meta":
				if s, ok := fields[j+1].(string); ok {
					meta = []byte(s)
				}
			case "score":
				if s, ok := fields[j+1].(string); ok {
					if f, err := strconv.ParseFloat(s, 32); err == nil {
						// COSINE distance metric returns distance (0 = identical);
						// convert to similarity for the Index interface's contract.
						score = float32(1 - f)
					}
				}
			}
		}
		matches = append(matches, Match{ID: docID, Score: score, Metadata: meta})
	}
	return matches, nil
}

// Flush removes every document under scope's prefix via SCAN (bounded,
// non-blocking) — this is an infrequent admin operation, not hot-path.
func (r *RedisIndex) Flush(ctx context.Context, scope string) error {
	if r == nil || r.rdb == nil {
		return nil
	}
	prefix := docKeyPrefix(scope)
	iter := r.rdb.Scan(ctx, 0, prefix+"*", 200).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	return r.rdb.Del(ctx, keys...).Err()
}
