package extension

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const defaultDeadline = 100 * time.Millisecond

// HookConfig binds one Hook to the points it runs at, with its own
// deadline/fail-mode/tenant scope — the same per-extension knobs the
// guardrail chain applies per-checker (docs/design/09-extensibility.md
// "Hook points" rules table).
type HookConfig struct {
	Hook     Hook
	Points   []HookPoint
	Deadline time.Duration // <=0 -> defaultDeadline
	// FailOpen: a hook error, panic, or deadline expiring is treated as Pass
	// rather than Reject. Default true, mirroring guardrail.ChainOption.
	FailOpen bool
	// TenantID scopes this hook to one tenant; 0 = every tenant.
	TenantID uint
}

// Dispatcher runs the registered hooks for pre_request/post_response.
// SetHooks does an atomic swap so config reloads (ai_extensions changing) or
// startup registration never race a request goroutine's RunSync.
type Dispatcher struct {
	mu      sync.RWMutex
	byPoint map[HookPoint][]HookConfig
	onError func(hookName string, point HookPoint, err error)
}

// NewDispatcher builds an empty Dispatcher. onError, if non-nil, is called
// for every hook error/timeout/panic (metrics/logging hook, kept out of this
// package to stay dependency-free per the package doc).
func NewDispatcher(onError func(hookName string, point HookPoint, err error)) *Dispatcher {
	return &Dispatcher{byPoint: map[HookPoint][]HookConfig{}, onError: onError}
}

// SetHooks atomically replaces the full set of registered hooks.
func (d *Dispatcher) SetHooks(configs []HookConfig) {
	byPoint := map[HookPoint][]HookConfig{}
	for _, cfg := range configs {
		for _, p := range cfg.Points {
			byPoint[p] = append(byPoint[p], cfg)
		}
	}
	d.mu.Lock()
	d.byPoint = byPoint
	d.mu.Unlock()
}

func (d *Dispatcher) hooksFor(point HookPoint) []HookConfig {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.byPoint[point]
}

// RunSync runs every hook registered for point, in registration order,
// against ev — mirrors guardrail.Chain.Run's semantics exactly: fast-path
// no-op when nothing is registered (hot-path guarantee), each hook bounded
// by its own deadline, a fail-closed hook's error/timeout/panic becomes a
// Reject, a fail-open one is skipped (logged via onError). The first Reject
// short-circuits; a Mutate's Patch becomes the IR the next hook sees, so
// patches compose. Labels from every hook that ran are merged and returned
// regardless of the final Action.
func (d *Dispatcher) RunSync(ctx context.Context, point HookPoint, ev Event) Result {
	hooks := d.hooksFor(point)
	if len(hooks) == 0 {
		return Result{Action: ActionPass}
	}

	mergedLabels := map[string]string{}
	for k, v := range ev.Labels {
		mergedLabels[k] = v
	}
	currentIR := ev.IR

	for _, cfg := range hooks {
		if cfg.TenantID != 0 && cfg.TenantID != ev.TenantID {
			continue
		}
		res, err := d.runOne(ctx, cfg, Event{
			Point: point, TenantID: ev.TenantID, RequestID: ev.RequestID,
			IR: currentIR, Labels: mergedLabels,
		})
		if err != nil {
			if d.onError != nil {
				d.onError(cfg.Hook.Name(), point, err)
			}
			if !cfg.FailOpen {
				return Result{Action: ActionReject, Reason: "extension " + cfg.Hook.Name() + " failed: " + err.Error(), Labels: mergedLabels}
			}
			continue
		}
		for k, v := range res.Labels {
			mergedLabels[k] = v
		}
		switch res.Action {
		case ActionReject:
			return Result{Action: ActionReject, Reason: res.Reason, Labels: mergedLabels}
		case ActionMutate:
			if len(res.Patch) > 0 {
				currentIR = res.Patch
			}
		}
	}

	if string(currentIR) != string(ev.IR) {
		return Result{Action: ActionMutate, Patch: currentIR, Labels: mergedLabels}
	}
	return Result{Action: ActionPass, Labels: mergedLabels}
}

// runOne bounds one hook call by its deadline and contains panics — a
// misbehaving extension must never take down the proxy.
func (d *Dispatcher) runOne(ctx context.Context, cfg HookConfig, ev Event) (res Result, err error) {
	deadline := cfg.Deadline
	if deadline <= 0 {
		deadline = defaultDeadline
	}
	callCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- outcome{err: fmt.Errorf("panic: %v", r)}
			}
		}()
		hres, herr := cfg.Hook.Handle(callCtx, ev)
		done <- outcome{res: hres, err: herr}
	}()

	select {
	case o := <-done:
		return o.res, o.err
	case <-callCtx.Done():
		return Result{}, fmt.Errorf("deadline exceeded (%s)", deadline)
	}
}
