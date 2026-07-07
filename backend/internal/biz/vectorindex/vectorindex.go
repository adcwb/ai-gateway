// Package vectorindex is the pluggable ANN backend for the semantic cache
// (docs/design/07-caching-strategies.md "Vector backend (ADR)"). It has no
// dependency on package biz — biz depends on vectorindex, never the reverse,
// the same split used for internal/biz/guardrail.
package vectorindex

import "context"

// Match is one nearest-neighbor result from Search.
type Match struct {
	ID       string
	Score    float32 // cosine similarity in [-1,1]; higher is closer
	Metadata []byte
}

// Index is the pluggable ANN backend. Implementations must fail closed: any
// error from Search/Upsert is treated by the caller as a cache miss, never a
// request failure (docs/design/07-caching-strategies.md "Failure containment").
type Index interface {
	// Available reports whether this backend can currently serve vector
	// search — e.g. false if the connected Redis lacks the search module.
	// Callers must check this before Search/Upsert and silently degrade to
	// exact-cache-only when false.
	Available(ctx context.Context) bool

	// Upsert stores one vector under scope (a tenant+model+params partition)
	// with a TTL in seconds. Scopes keep lookups from crossing tenants,
	// models, or incompatible generation params.
	Upsert(ctx context.Context, scope, id string, vector []float32, metadata []byte, ttlSeconds int) error

	// Search returns up to topK nearest neighbors within scope, ordered by
	// descending similarity.
	Search(ctx context.Context, scope string, vector []float32, topK int) ([]Match, error)

	// Flush removes every entry in scope (the cache-flush admin endpoint).
	Flush(ctx context.Context, scope string) error
}
