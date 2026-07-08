package extension

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeHook struct {
	name    string
	result  Result
	err     error
	sleep   time.Duration
	panicOn bool
}

func (h *fakeHook) Name() string { return h.name }
func (h *fakeHook) Handle(ctx context.Context, ev Event) (Result, error) {
	if h.panicOn {
		panic("fakeHook: boom")
	}
	if h.sleep > 0 {
		select {
		case <-time.After(h.sleep):
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	return h.result, h.err
}

func TestDispatcher_NoHooksIsNoOp(t *testing.T) {
	d := NewDispatcher(nil)
	res := d.RunSync(context.Background(), PreRequest, Event{IR: []byte(`{"a":1}`)})
	if res.Action != ActionPass {
		t.Fatalf("expected Pass with zero hooks registered, got %v", res.Action)
	}
}

func TestDispatcher_MutatePatchesCompose(t *testing.T) {
	d := NewDispatcher(nil)
	d.SetHooks([]HookConfig{
		{Hook: &fakeHook{name: "h1", result: Result{Action: ActionMutate, Patch: []byte(`{"a":1}`)}}, Points: []HookPoint{PreRequest}, FailOpen: true},
		{Hook: &fakeHook{name: "h2", result: Result{Action: ActionMutate, Patch: []byte(`{"a":2}`)}}, Points: []HookPoint{PreRequest}, FailOpen: true},
	})
	res := d.RunSync(context.Background(), PreRequest, Event{IR: []byte(`{"a":0}`)})
	if res.Action != ActionMutate || string(res.Patch) != `{"a":2}` {
		t.Fatalf("expected the second hook's patch to win, got action=%v patch=%s", res.Action, res.Patch)
	}
}

type countingHook struct {
	fakeHook
	calls *int
}

func (h *countingHook) Handle(ctx context.Context, ev Event) (Result, error) {
	*h.calls++
	return h.fakeHook.Handle(ctx, ev)
}

func TestDispatcher_FirstRejectShortCircuits(t *testing.T) {
	d := NewDispatcher(nil)
	h2Calls := 0
	d.SetHooks([]HookConfig{
		{Hook: &fakeHook{name: "h1", result: Result{Action: ActionReject, Reason: "nope"}}, Points: []HookPoint{PreRequest}, FailOpen: true},
		{Hook: &countingHook{fakeHook: fakeHook{name: "h2", result: Result{Action: ActionPass}}, calls: &h2Calls}, Points: []HookPoint{PreRequest}, FailOpen: true},
	})

	res := d.RunSync(context.Background(), PreRequest, Event{})
	if res.Action != ActionReject || res.Reason != "nope" {
		t.Fatalf("expected Reject with reason 'nope', got %+v", res)
	}
	if h2Calls != 0 {
		t.Fatalf("expected the second hook to never run after the first rejected, got %d calls", h2Calls)
	}
}

func TestDispatcher_LabelsAccumulateAcrossHooks(t *testing.T) {
	d := NewDispatcher(nil)
	d.SetHooks([]HookConfig{
		{Hook: &fakeHook{name: "h1", result: Result{Action: ActionPass, Labels: map[string]string{"a": "1"}}}, Points: []HookPoint{PostResponse}, FailOpen: true},
		{Hook: &fakeHook{name: "h2", result: Result{Action: ActionPass, Labels: map[string]string{"b": "2"}}}, Points: []HookPoint{PostResponse}, FailOpen: true},
	})
	res := d.RunSync(context.Background(), PostResponse, Event{})
	if res.Labels["a"] != "1" || res.Labels["b"] != "2" {
		t.Fatalf("expected labels from both hooks merged, got %+v", res.Labels)
	}
}

func TestDispatcher_FailOpenSkipsErroringHook(t *testing.T) {
	var loggedErr error
	d := NewDispatcher(func(name string, point HookPoint, err error) { loggedErr = err })
	d.SetHooks([]HookConfig{
		{Hook: &fakeHook{name: "broken", err: errors.New("boom")}, Points: []HookPoint{PreRequest}, FailOpen: true},
	})
	res := d.RunSync(context.Background(), PreRequest, Event{IR: []byte(`x`)})
	if res.Action != ActionPass {
		t.Fatalf("expected a fail-open hook's error to result in Pass, got %v", res.Action)
	}
	if loggedErr == nil {
		t.Fatal("expected onError to be called for the erroring hook")
	}
}

func TestDispatcher_FailClosedRejectsOnError(t *testing.T) {
	d := NewDispatcher(nil)
	d.SetHooks([]HookConfig{
		{Hook: &fakeHook{name: "broken", err: errors.New("boom")}, Points: []HookPoint{PreRequest}, FailOpen: false},
	})
	res := d.RunSync(context.Background(), PreRequest, Event{})
	if res.Action != ActionReject {
		t.Fatalf("expected a fail-closed hook's error to result in Reject, got %v", res.Action)
	}
}

func TestDispatcher_DeadlineExceeded(t *testing.T) {
	d := NewDispatcher(nil)
	d.SetHooks([]HookConfig{
		{Hook: &fakeHook{name: "slow", sleep: 200 * time.Millisecond}, Points: []HookPoint{PreRequest}, FailOpen: false, Deadline: 20 * time.Millisecond},
	})
	start := time.Now()
	res := d.RunSync(context.Background(), PreRequest, Event{})
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("expected the deadline to cut the call short, took %s", elapsed)
	}
	if res.Action != ActionReject {
		t.Fatalf("expected a fail-closed timeout to Reject, got %v", res.Action)
	}
}

func TestDispatcher_PanicIsContained(t *testing.T) {
	d := NewDispatcher(nil)
	d.SetHooks([]HookConfig{
		{Hook: &fakeHook{name: "panics", panicOn: true}, Points: []HookPoint{PreRequest}, FailOpen: true},
	})
	res := d.RunSync(context.Background(), PreRequest, Event{})
	if res.Action != ActionPass {
		t.Fatalf("expected a panicking fail-open hook to result in Pass (not crash the test), got %v", res.Action)
	}
}

func TestDispatcher_TenantScoping(t *testing.T) {
	d := NewDispatcher(nil)
	d.SetHooks([]HookConfig{
		{Hook: &fakeHook{name: "tenant5", result: Result{Action: ActionReject, Reason: "scoped"}}, Points: []HookPoint{PreRequest}, FailOpen: true, TenantID: 5},
	})
	// Tenant 7 should not see tenant 5's hook.
	res := d.RunSync(context.Background(), PreRequest, Event{TenantID: 7})
	if res.Action != ActionPass {
		t.Fatalf("expected tenant-scoped hook to be skipped for a different tenant, got %v", res.Action)
	}
	res = d.RunSync(context.Background(), PreRequest, Event{TenantID: 5})
	if res.Action != ActionReject {
		t.Fatalf("expected tenant-scoped hook to run for its own tenant, got %v", res.Action)
	}
}
