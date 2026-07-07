package guardrail

import (
	"context"
	"time"
)

// ChainOption tunes one chain's execution (docs/design/06 "Sync vs async").
type ChainOption struct {
	// Deadline bounds the sync portion of the chain; default 100ms.
	Deadline time.Duration
	// FailOpen: a checker error, or the deadline expiring, is treated as "no
	// finding" rather than a block. Default true — "one broken regex must not
	// take down the proxy."
	FailOpen bool
}

// Chain runs an ordered list of checkers over one piece of content.
type Chain struct {
	checkers []Checker
	opt      ChainOption
	onError  func(checkerName string, err error)
}

// NewChain builds a chain. onError, if non-nil, is called for every checker
// error or deadline-exceeded event (metrics/logging hook — kept out of this
// package to stay dependency-free per the package doc).
func NewChain(checkers []Checker, opt ChainOption, onError func(checkerName string, err error)) *Chain {
	if opt.Deadline <= 0 {
		opt.Deadline = 100 * time.Millisecond
	}
	return &Chain{checkers: checkers, opt: opt, onError: onError}
}

// Run executes the chain's sync checkers in order over text, short-
// circuiting on the first block/terminate. A redact finding rewrites the
// text seen by subsequent checkers. Async checkers (Mode() == ModeAsync) are
// dispatched via onAsync and never block this call or affect its return
// value — matching the existing async PII side-channel pattern.
func (c *Chain) Run(ctx context.Context, text string, dir Direction, onAsync func(Finding)) (finalText string, action Action, findings []Finding) {
	deadlineCtx, cancel := context.WithTimeout(ctx, c.opt.Deadline)
	defer cancel()

	finalText = text
	action = ActionNone

	for _, checker := range c.checkers {
		if checker.Mode() == ModeAsync {
			c.dispatchAsync(checker, finalText, dir, onAsync)
			continue
		}

		select {
		case <-deadlineCtx.Done():
			if !c.opt.FailOpen {
				action = ActionBlock
				findings = append(findings, Finding{Action: ActionBlock, Types: []string{"chain_deadline_exceeded"}})
			}
			if c.onError != nil {
				c.onError(checker.Name(), deadlineCtx.Err())
			}
			return finalText, action, findings
		default:
		}

		content := Content{Text: finalText}
		f, err := checker.Check(deadlineCtx, &content, dir)
		if err != nil {
			if c.onError != nil {
				c.onError(checker.Name(), err)
			}
			if !c.opt.FailOpen {
				action = ActionBlock
				findings = append(findings, Finding{Action: ActionBlock, Types: []string{"checker_error:" + checker.Name()}})
				return finalText, action, findings
			}
			continue
		}
		if f.Action == ActionNone {
			continue
		}
		findings = append(findings, f)
		if f.Action.Rank() > action.Rank() {
			action = f.Action
		}
		if f.Action == ActionRedact && f.Redacted != "" {
			finalText = f.Redacted
		}
		if f.Action == ActionBlock || f.Action == ActionTerminate {
			return finalText, action, findings
		}
	}
	return finalText, action, findings
}

func (c *Chain) dispatchAsync(checker Checker, text string, dir Direction, onAsync func(Finding)) {
	if onAsync == nil {
		return
	}
	content := Content{Text: text}
	go func() {
		f, err := checker.Check(context.Background(), &content, dir)
		if err != nil {
			if c.onError != nil {
				c.onError(checker.Name(), err)
			}
			return
		}
		if f.Action != ActionNone {
			onAsync(f)
		}
	}()
}
