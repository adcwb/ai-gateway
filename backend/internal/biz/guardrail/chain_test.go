package guardrail

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeChecker is a scripted Checker for exercising Chain.Run's control flow.
type fakeChecker struct {
	name    string
	mode    Mode
	finding Finding
	err     error
	delay   time.Duration
	calls   *int
}

func (f *fakeChecker) Name() string { return f.name }
func (f *fakeChecker) Mode() Mode   { return f.mode }
func (f *fakeChecker) Check(ctx context.Context, c *Content, dir Direction) (Finding, error) {
	if f.calls != nil {
		*f.calls++
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return Finding{}, ctx.Err()
		}
	}
	return f.finding, f.err
}

func TestChainNoFindingsPassesThrough(t *testing.T) {
	c := NewChain([]Checker{
		&fakeChecker{name: "a", mode: ModeSync, finding: Finding{Action: ActionNone}},
	}, ChainOption{FailOpen: true}, nil)
	text, action, findings := c.Run(context.Background(), "hello", DirectionInbound, nil)
	if text != "hello" || action != ActionNone || len(findings) != 0 {
		t.Fatalf("expected pass-through, got text=%q action=%q findings=%v", text, action, findings)
	}
}

func TestChainBlockShortCircuitsLaterCheckers(t *testing.T) {
	var secondCalls int
	c := NewChain([]Checker{
		&fakeChecker{name: "blocker", mode: ModeSync, finding: Finding{Action: ActionBlock, Types: []string{"x"}}},
		&fakeChecker{name: "never", mode: ModeSync, finding: Finding{Action: ActionLog}, calls: &secondCalls},
	}, ChainOption{FailOpen: true}, nil)
	_, action, findings := c.Run(context.Background(), "hello", DirectionInbound, nil)
	if action != ActionBlock {
		t.Fatalf("expected block, got %q", action)
	}
	if secondCalls != 0 {
		t.Fatal("expected the second checker to never run after a block")
	}
	if len(findings) != 1 || findings[0].Types[0] != "x" {
		t.Fatalf("unexpected findings: %+v", findings)
	}
}

func TestChainRedactRewritesTextForLaterCheckers(t *testing.T) {
	var sawText string
	c := NewChain([]Checker{
		&fakeChecker{name: "redactor", mode: ModeSync, finding: Finding{Action: ActionRedact, Redacted: "***"}},
		&recordingChecker{seen: &sawText},
	}, ChainOption{FailOpen: true}, nil)
	finalText, action, _ := c.Run(context.Background(), "secret", DirectionInbound, nil)
	if action != ActionRedact || finalText != "***" {
		t.Fatalf("expected redacted final text, got action=%q text=%q", action, finalText)
	}
	if sawText != "***" {
		t.Fatalf("expected the second checker to see the redacted text, got %q", sawText)
	}
}

type recordingChecker struct{ seen *string }

func (r *recordingChecker) Name() string { return "recorder" }
func (r *recordingChecker) Mode() Mode   { return ModeSync }
func (r *recordingChecker) Check(_ context.Context, c *Content, _ Direction) (Finding, error) {
	*r.seen = c.Text
	return Finding{Action: ActionNone}, nil
}

func TestChainMostSevereActionWins(t *testing.T) {
	c := NewChain([]Checker{
		&fakeChecker{name: "logger", mode: ModeSync, finding: Finding{Action: ActionLog}},
		&fakeChecker{name: "redactor", mode: ModeSync, finding: Finding{Action: ActionRedact, Redacted: "r"}},
	}, ChainOption{FailOpen: true}, nil)
	_, action, findings := c.Run(context.Background(), "x", DirectionInbound, nil)
	if action != ActionRedact {
		t.Fatalf("expected redact (more severe than log) to win, got %q", action)
	}
	if len(findings) != 2 {
		t.Fatalf("expected both findings recorded, got %v", findings)
	}
}

func TestChainFailOpenSkipsErroringChecker(t *testing.T) {
	c := NewChain([]Checker{
		&fakeChecker{name: "broken", mode: ModeSync, err: errors.New("boom")},
		&fakeChecker{name: "ok", mode: ModeSync, finding: Finding{Action: ActionLog, Types: []string{"y"}}},
	}, ChainOption{FailOpen: true}, nil)
	_, action, findings := c.Run(context.Background(), "x", DirectionInbound, nil)
	if action != ActionLog || len(findings) != 1 {
		t.Fatalf("expected fail-open to skip the broken checker and still see the next one, got action=%q findings=%v", action, findings)
	}
}

func TestChainFailClosedBlocksOnCheckerError(t *testing.T) {
	var errName string
	c := NewChain([]Checker{
		&fakeChecker{name: "broken", mode: ModeSync, err: errors.New("boom")},
	}, ChainOption{FailOpen: false}, func(name string, err error) { errName = name })
	_, action, _ := c.Run(context.Background(), "x", DirectionInbound, nil)
	if action != ActionBlock {
		t.Fatalf("expected fail-closed to block on checker error, got %q", action)
	}
	if errName != "broken" {
		t.Fatalf("expected onError callback with checker name, got %q", errName)
	}
}

func TestChainDeadlineExceededFailOpen(t *testing.T) {
	c := NewChain([]Checker{
		&fakeChecker{name: "slow", mode: ModeSync, delay: 50 * time.Millisecond, finding: Finding{Action: ActionLog}},
	}, ChainOption{FailOpen: true, Deadline: 5 * time.Millisecond}, nil)
	_, action, _ := c.Run(context.Background(), "x", DirectionInbound, nil)
	if action != ActionNone {
		t.Fatalf("expected fail-open on timeout to produce no action, got %q", action)
	}
}

func TestChainAsyncCheckerNeverBlocksOrAffectsResult(t *testing.T) {
	done := make(chan Finding, 1)
	c := NewChain([]Checker{
		&fakeChecker{name: "async", mode: ModeAsync, finding: Finding{Action: ActionBlock, Types: []string{"should-not-affect-sync-result"}}},
	}, ChainOption{FailOpen: true}, nil)
	_, action, findings := c.Run(context.Background(), "x", DirectionInbound, func(f Finding) { done <- f })
	if action != ActionNone || len(findings) != 0 {
		t.Fatalf("expected an async checker to never affect the sync return, got action=%q findings=%v", action, findings)
	}
	select {
	case f := <-done:
		if f.Action != ActionBlock {
			t.Fatalf("expected the async finding to still be delivered, got %+v", f)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the async finding callback")
	}
}
