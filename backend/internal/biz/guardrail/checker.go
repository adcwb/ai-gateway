// Package guardrail generalizes PII detection, prompt-injection heuristics,
// and future checks into one pipeline (docs/design/06-security-and-
// guardrails.md), replacing the idea of N parallel bespoke hooks. It is
// deliberately dependency-free with respect to package biz (biz depends on
// guardrail, never the reverse) so built-in checkers that need biz-level
// state (e.g. the rule-based PII detectors in internal/biz/pii_engine.go)
// live in biz as thin adapters implementing the Checker interface defined
// here, while checkers with no such dependency (the external gRPC adapter)
// live directly in this package.
package guardrail

import "context"

// Direction is which leg of the request a checker inspects.
type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
)

// Action is what a checker (or the chain as a whole) decided to do.
// Ordered least → most severe; Chain.Run reports the most severe action any
// checker produced.
type Action string

const (
	ActionNone      Action = "none"
	ActionLog       Action = "log"
	ActionRedact    Action = "redact"
	ActionBlock     Action = "block"
	ActionTerminate Action = "terminate" // outbound streaming only: stop the stream
)

// Rank orders actions for "most severe wins" aggregation across a chain.
func (a Action) Rank() int {
	switch a {
	case ActionBlock, ActionTerminate:
		return 3
	case ActionRedact:
		return 2
	case ActionLog:
		return 1
	default:
		return 0
	}
}

// Mode declares whether a checker may block/redact synchronously (bounded by
// the chain deadline) or only observes asynchronously (never touches latency;
// docs/design/06 "Sync vs async").
type Mode string

const (
	ModeSync  Mode = "sync"
	ModeAsync Mode = "async"
)

// Content is the text a checker inspects and may rewrite — message *text
// parts* from the IR, never raw JSON, so redaction can never corrupt request/
// response structure (docs/design/06 "Built-in checkers" rationale).
type Content struct {
	Text string
}

// Finding is one checker's verdict for one piece of content.
type Finding struct {
	Action  Action
	Types   []string // detector/category names, for audit
	Details string
	// Redacted is the rewritten text when Action == ActionRedact.
	Redacted string
}

// Checker inspects (and may rewrite) content in one direction.
type Checker interface {
	Name() string
	Mode() Mode
	Check(ctx context.Context, c *Content, dir Direction) (Finding, error)
}
