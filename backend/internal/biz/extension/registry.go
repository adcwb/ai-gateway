package extension

import "sync"

// Register is the one blessed compile-time touch-point (docs/design/09-
// extensibility.md "Delivery mechanisms" (a)): forks/operators add a hook
// with an import + one call, here or in cmd/server/extensions.go, and
// rebuild. Nothing is registered by default.
var (
	compiledMu sync.Mutex
	compiled   []HookConfig
)

// Register adds a trusted, in-process hook for the given points, running
// with fail-open semantics and the default deadline. Call from an init()
// (see cmd/server/extensions.go) before GatewayUseCase's Dispatcher is built.
func Register(hook Hook, points ...HookPoint) {
	compiledMu.Lock()
	defer compiledMu.Unlock()
	compiled = append(compiled, HookConfig{Hook: hook, Points: points, FailOpen: true})
}

// CompiledHooks returns every hook registered via Register so far — the
// Dispatcher's initial SetHooks call combines this with the ai_extensions
// rows loaded from the database.
func CompiledHooks() []HookConfig {
	compiledMu.Lock()
	defer compiledMu.Unlock()
	out := make([]HookConfig, len(compiled))
	copy(out, compiled)
	return out
}
