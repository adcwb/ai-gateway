package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/tetratelabs/wazero"
)

// WasmHook runs an in-process WASM guest module for pre_request/
// post_response (docs/design/09-extensibility.md "Delivery mechanisms" (c) —
// built despite the design doc's own "evaluate behind real demand, not a
// promise" hedge, per explicit request). The ABI is deliberately the
// smallest thing that works — a single JSON-in/JSON-out call — documented
// here since an ABI, once shipped, is a compatibility surface:
//
//   - Guest exports "alloc(size u32) -> ptr u32": the host calls this once
//     per Handle to get a scratch buffer, then writes the Event JSON into
//     guest memory at that offset.
//   - Guest exports "handle(ptr u32, len u32) -> packed u64": packed is
//     (resultPtr<<32 | resultLen); the host reads the Result JSON back from
//     guest memory at that offset/length.
//   - Guest declares a "memory" export (the standard implicit export every
//     wazero guest with a memory section has).
//
// No host-imported callback surface exists — a guest cannot call back into
// the gateway mid-execution. Revisit if real plugins need it.
type WasmHook struct {
	HookName string
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	seq      atomic.Uint64
}

// NewWasmHook compiles the module at wasmPath once. WithCloseOnContextDone
// makes the deadline the Dispatcher applies via context.WithTimeout actually
// interrupt a runaway guest, not just log it late.
func NewWasmHook(ctx context.Context, name, wasmPath string) (*WasmHook, error) {
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("extension %s: read wasm module: %w", name, err)
	}
	rc := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	rt := wazero.NewRuntimeWithConfig(ctx, rc)
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("extension %s: compile wasm module: %w", name, err)
	}
	return &WasmHook{HookName: name, runtime: rt, compiled: compiled}, nil
}

func (h *WasmHook) Name() string { return h.HookName }

// Close releases the runtime (and every compiled module in it). Call once
// when an extension is removed/reloaded.
func (h *WasmHook) Close(ctx context.Context) error {
	return h.runtime.Close(ctx)
}

// Handle instantiates a fresh, isolated module per call (wazero modules are
// not safe for concurrent Call from multiple goroutines) and closes it
// afterward — the isolation cost buys "one bad request can't corrupt guest
// state for the next one."
func (h *WasmHook) Handle(ctx context.Context, ev Event) (Result, error) {
	body, err := json.Marshal(webhookRequestBody{
		Point: ev.Point, TenantID: ev.TenantID, RequestID: ev.RequestID, IR: ev.IR, Labels: ev.Labels,
	})
	if err != nil {
		return Result{}, fmt.Errorf("extension %s: encode event: %w", h.HookName, err)
	}

	modName := fmt.Sprintf("%s-%d", h.HookName, h.seq.Add(1))
	mod, err := h.runtime.InstantiateModule(ctx, h.compiled, wazero.NewModuleConfig().WithName(modName))
	if err != nil {
		return Result{}, fmt.Errorf("extension %s: instantiate module: %w", h.HookName, err)
	}
	defer mod.Close(ctx)

	allocFn := mod.ExportedFunction("alloc")
	handleFn := mod.ExportedFunction("handle")
	if allocFn == nil || handleFn == nil {
		return Result{}, fmt.Errorf("extension %s: wasm module must export alloc and handle", h.HookName)
	}

	allocRes, err := allocFn.Call(ctx, uint64(len(body)))
	if err != nil {
		return Result{}, fmt.Errorf("extension %s: alloc failed: %w", h.HookName, err)
	}
	if len(allocRes) != 1 {
		return Result{}, fmt.Errorf("extension %s: alloc must return exactly one value", h.HookName)
	}
	ptr := uint32(allocRes[0])

	if !mod.Memory().Write(ptr, body) {
		return Result{}, fmt.Errorf("extension %s: failed writing event into guest memory", h.HookName)
	}

	packedRes, err := handleFn.Call(ctx, uint64(ptr), uint64(len(body)))
	if err != nil {
		return Result{}, fmt.Errorf("extension %s: handle failed: %w", h.HookName, err)
	}
	if len(packedRes) != 1 {
		return Result{}, fmt.Errorf("extension %s: handle must return exactly one value", h.HookName)
	}
	packed := packedRes[0]
	resultPtr := uint32(packed >> 32)
	resultLen := uint32(packed)

	resultBytes, ok := mod.Memory().Read(resultPtr, resultLen)
	if !ok {
		return Result{}, fmt.Errorf("extension %s: failed reading result from guest memory", h.HookName)
	}

	var parsed webhookResponseBody
	if err := json.Unmarshal(resultBytes, &parsed); err != nil {
		return Result{}, fmt.Errorf("extension %s: invalid result JSON: %w", h.HookName, err)
	}
	return Result{Action: parsed.Action, Patch: parsed.Patch, Reason: parsed.Reason, Labels: parsed.Labels}, nil
}
