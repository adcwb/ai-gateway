package biz

import (
	"context"
	"testing"

	"github.com/opscenter/ai-gateway/internal/biz/guardrail"
)

func TestTopicFenceChecker_Fires(t *testing.T) {
	c := newTopicFenceChecker([]string{"competitor product X", "internal roadmap"}, guardrail.ActionBlock)
	finding, err := c.Check(context.Background(), &guardrail.Content{Text: "let's talk about our Internal Roadmap for next quarter"}, guardrail.DirectionOutbound)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if finding.Action != guardrail.ActionBlock || len(finding.Types) != 1 || finding.Types[0] != "topic_fence" {
		t.Fatalf("unexpected finding: %+v", finding)
	}
}

func TestTopicFenceChecker_NoFinding(t *testing.T) {
	c := newTopicFenceChecker([]string{"forbidden topic"}, guardrail.ActionBlock)
	finding, err := c.Check(context.Background(), &guardrail.Content{Text: "totally unrelated content"}, guardrail.DirectionOutbound)
	if err != nil || finding.Action != guardrail.ActionNone {
		t.Fatalf("expected no finding, got %+v (err=%v)", finding, err)
	}
}

func TestTopicFenceChecker_EmptyBlocklistIsNoOp(t *testing.T) {
	c := newTopicFenceChecker(nil, guardrail.ActionBlock)
	finding, err := c.Check(context.Background(), &guardrail.Content{Text: "anything at all"}, guardrail.DirectionOutbound)
	if err != nil || finding.Action != guardrail.ActionNone {
		t.Fatalf("expected an empty blocklist to never fire, got %+v (err=%v)", finding, err)
	}
}

func TestTopicFenceChecker_TrimsAndLowercasesConfig(t *testing.T) {
	c := newTopicFenceChecker([]string{"  Forbidden Topic  ", ""}, guardrail.ActionRedact)
	finding, err := c.Check(context.Background(), &guardrail.Content{Text: "this mentions forbidden topic in passing"}, guardrail.DirectionInbound)
	if err != nil || finding.Action != guardrail.ActionRedact {
		t.Fatalf("expected trimmed/lowercased config to still match, got %+v (err=%v)", finding, err)
	}
}

func TestTopicFenceChecker_Name(t *testing.T) {
	c := newTopicFenceChecker(nil, guardrail.ActionBlock)
	if c.Name() != "topic_fence" {
		t.Fatalf("unexpected name: %s", c.Name())
	}
	if c.Mode() != guardrail.ModeSync {
		t.Fatalf("expected sync mode, got %s", c.Mode())
	}
}
