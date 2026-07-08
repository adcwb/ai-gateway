// Package extension implements the synchronous half of extensibility
// (docs/design/09-extensibility.md "Hook points" + "Delivery mechanisms"):
// pre_request and post_response hooks, which may mutate the request/response
// IR or reject the call, bounded by a per-hook deadline. It is deliberately
// dependency-free with respect to package biz — the same split used for
// internal/biz/guardrail and internal/biz/mcpgw — so biz wires a Dispatcher
// in and calls RunSync at the two points in the request lifecycle where a
// hook may still change what happens (before routing, before the response
// is written to the client).
//
// on_audit/on_billing are NOT part of this package — the design doc
// generalizes those two into the event bus (internal/biz/eventbus), since
// they are inherently async, read-only, "the async half of extensibility."
package extension

import "context"

// HookPoint is which stage of the request lifecycle a hook attaches to.
type HookPoint string

const (
	// PreRequest runs after auth+guardrails, before routing. May mutate the
	// request IR, reject (with a reason), or annotate (labels flow to
	// audit/billing).
	PreRequest HookPoint = "pre_request"
	// PostResponse runs after the upstream call, before the client encode,
	// for non-streaming responses only — a streaming response gets exactly
	// one terminal, annotate-only invocation instead (mutating already-sent
	// bytes is impossible; see the "Streaming commit rule" in backend/CLAUDE.md).
	PostResponse HookPoint = "post_response"
)

// Action is what a hook decided to do with one Event.
type Action string

const (
	ActionPass   Action = "pass"
	ActionMutate Action = "mutate"
	ActionReject Action = "reject"
)

// Event is the envelope a Hook receives. IR is the request or response body
// (JSON, whatever shape ProxyRequest is currently holding); Labels are
// annotate-only key/value pairs contributed by earlier hooks in the chain.
type Event struct {
	Point     HookPoint
	TenantID  uint
	RequestID string
	IR        []byte
	Labels    map[string]string
}

// Result is one hook's verdict. Patch is the new IR when Action == Mutate;
// Labels (present regardless of Action) merge into the event's label set for
// subsequent hooks and, ultimately, the audit/billing records.
type Result struct {
	Action Action
	Patch  []byte
	Reason string
	Labels map[string]string
}

// Hook is one pre_request/post_response extension — a compile-time
// registration (internal/biz/extension.Register), a WebhookHook, or a
// WasmHook all implement this the same way.
type Hook interface {
	Name() string
	Handle(ctx context.Context, ev Event) (Result, error)
}
