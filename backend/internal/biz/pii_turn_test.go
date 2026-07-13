package biz

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opscenter/ai-gateway/internal/biz/guardrail"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

// -----------------------------------------------------------------------------
// currentTurnText
// -----------------------------------------------------------------------------

func TestCurrentTurnText_PicksLastUserMessage(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"call me at 13066914025"},
		{"role":"assistant","content":"ok"},
		{"role":"user","content":"ok, new topic"}
	]}`)
	text, ok := currentTurnText(body)
	if !ok || text != "ok, new topic" {
		t.Fatalf("got text=%q ok=%v", text, ok)
	}
}

func TestCurrentTurnText_MultimodalBlocksJoined(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	text, ok := currentTurnText(body)
	if !ok || text != "hello" {
		t.Fatalf("got text=%q ok=%v", text, ok)
	}
}

func TestCurrentTurnText_MultiBlockReturnsEmpty(t *testing.T) {
	// Cherry-Studio-style resend: a leftover flagged fragment bundled with
	// the user's actual new input in the same message.
	body := []byte(`{"messages":[{"role":"user","content":[
		{"type":"text","text":"13066914025"},
		{"type":"text","text":"ok, new topic"}
	]}]}`)
	text, ok := currentTurnText(body)
	if !ok || text != "" {
		t.Fatalf("expected empty text (unsplittable multi-block) with ok=true, got text=%q ok=%v", text, ok)
	}
}

func TestCurrentTurnText_NoMessagesArrayFallsBack(t *testing.T) {
	if _, ok := currentTurnText([]byte(`my card is 4111111111111111`)); ok {
		t.Fatal("expected ok=false for a non-chat body")
	}
	if _, ok := currentTurnText([]byte(`{"input":"embed this"}`)); ok {
		t.Fatal("expected ok=false for an /embeddings-style body with no messages array")
	}
}

func TestCurrentTurnText_NoUserMessageFallsBack(t *testing.T) {
	body := []byte(`{"messages":[{"role":"system","content":"you are a bot"}]}`)
	if _, ok := currentTurnText(body); ok {
		t.Fatal("expected ok=false when no user message exists")
	}
}

// -----------------------------------------------------------------------------
// applyPIIPolicy legacy engine: history must never permanently block a
// conversation (docs/design/06 ADR addendum).
// -----------------------------------------------------------------------------

func TestApplyPIIPolicy_LegacyBlock_HistoryOnlyMatchDoesNotBlock(t *testing.T) {
	uc, db := newTestGatewayForGuardrail(t)
	isDefault := true
	policy := &model.AIPIIPolicy{Name: "default", Enabled: true, Action: model.PIIActionBlock, IsDefault: &isDefault}
	if err := db.Create(policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	key := &model.AIVirtualKey{Name: "k"}
	body := []byte(`{"messages":[
		{"role":"user","content":"call me at 13066914025"},
		{"role":"assistant","content":"got it"},
		{"role":"user","content":"ok, let's talk about something else"}
	]}`)
	ctx, out := uc.applyPIIPolicy(context.Background(), key, body)
	if out.Blocked {
		t.Fatal("a match found only in resent history must never block")
	}
	if strings.Contains(string(out.NewBody), "13066914025") {
		t.Fatal("historical PII must still be masked before forwarding upstream")
	}
	if info := piiAuditFromCtx(ctx); info == nil || info.action != model.PIIActionLog {
		t.Fatalf("expected a downgraded log-only audit entry, got %+v", info)
	}
}

func TestApplyPIIPolicy_LegacyBlock_CurrentTurnMatchStillBlocks(t *testing.T) {
	uc, db := newTestGatewayForGuardrail(t)
	isDefault := true
	policy := &model.AIPIIPolicy{Name: "default", Enabled: true, Action: model.PIIActionBlock, IsDefault: &isDefault}
	if err := db.Create(policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	key := &model.AIVirtualKey{Name: "k"}
	body := []byte(`{"messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":"hello"},
		{"role":"user","content":"my number is 13066914025"}
	]}`)
	ctx, out := uc.applyPIIPolicy(context.Background(), key, body)
	if !out.Blocked {
		t.Fatal("PII in the caller's own latest turn must still block")
	}
	if info := piiAuditFromCtx(ctx); info == nil || info.action != model.PIIActionBlock {
		t.Fatalf("expected a block audit entry, got %+v", info)
	}
}

func TestApplyPIIPolicy_LegacyBlock_NonChatBodyKeepsOriginalBehavior(t *testing.T) {
	uc, db := newTestGatewayForGuardrail(t)
	isDefault := true
	policy := &model.AIPIIPolicy{Name: "default", Enabled: true, Action: model.PIIActionBlock, IsDefault: &isDefault}
	if err := db.Create(policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	key := &model.AIVirtualKey{Name: "k"}
	// No "messages" array at all (e.g. a body shape this helper doesn't
	// recognize) — currentTurnText is ok=false, so the original unscoped
	// hard-block behavior must be preserved exactly.
	_, out := uc.applyPIIPolicy(context.Background(), key, []byte(`my card is 4111111111111111`))
	if !out.Blocked {
		t.Fatal("expected the original hard-block behavior when no messages array is recognized")
	}
}

func TestApplyPIIPolicy_LegacyBlock_MultiBlockCurrentMessageDoesNotBlock(t *testing.T) {
	uc, db := newTestGatewayForGuardrail(t)
	isDefault := true
	policy := &model.AIPIIPolicy{Name: "default", Enabled: true, Action: model.PIIActionBlock, IsDefault: &isDefault}
	if err := db.Create(policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	key := &model.AIVirtualKey{Name: "k"}
	// Cherry-Studio-style resend: the client bundled the leftover flagged
	// fragment from a prior rejection together with the user's actual new
	// input into one multi-block message.
	body := []byte(`{"messages":[{"role":"user","content":[
		{"type":"text","text":"13066914025"},
		{"type":"text","text":"ok, new topic"}
	]}]}`)
	_, out := uc.applyPIIPolicy(context.Background(), key, body)
	if out.Blocked {
		t.Fatal("a multi-block current message must not permanently block the user's new input")
	}
}

// -----------------------------------------------------------------------------
// runInboundChain (pluggable chain path): same fix, same guarantees.
// -----------------------------------------------------------------------------

func TestApplyPIIPolicy_ChainBlock_HistoryOnlyMatchDoesNotBlock(t *testing.T) {
	uc, db := newTestGatewayForGuardrail(t)
	isDefault := true
	chainJSON, _ := json.Marshal([]checkerConfig{{Name: "pii_rules"}})
	policy := &model.AIPIIPolicy{Name: "default", Enabled: true, Action: model.PIIActionBlock, IsDefault: &isDefault, CheckerChain: chainJSON}
	if err := db.Create(policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	key := &model.AIVirtualKey{Name: "k"}
	body := []byte(`{"messages":[
		{"role":"user","content":"my card is 4111111111111111"},
		{"role":"assistant","content":"noted"},
		{"role":"user","content":"let's change topic"}
	]}`)
	ctx, out := uc.applyPIIPolicy(context.Background(), key, body)
	if out.Blocked {
		t.Fatal("a chain match found only in resent history must never block")
	}
	if info := piiAuditFromCtx(ctx); info == nil || info.action != model.PIIActionLog {
		t.Fatalf("expected a downgraded log-only audit entry, got %+v", info)
	}
}

func TestApplyPIIPolicy_ChainBlock_CurrentTurnMatchStillBlocks(t *testing.T) {
	uc, db := newTestGatewayForGuardrail(t)
	isDefault := true
	chainJSON, _ := json.Marshal([]checkerConfig{{Name: "pii_rules"}})
	policy := &model.AIPIIPolicy{Name: "default", Enabled: true, Action: model.PIIActionBlock, IsDefault: &isDefault, CheckerChain: chainJSON}
	if err := db.Create(policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	key := &model.AIVirtualKey{Name: "k"}
	body := []byte(`{"messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":"hello"},
		{"role":"user","content":"my card is 4111111111111111"}
	]}`)
	_, out := uc.applyPIIPolicy(context.Background(), key, body)
	if !out.Blocked {
		t.Fatal("PII in the caller's own latest turn must still block via the chain path too")
	}
}

// guardrail.Chain sanity: block-fixedAction checkers never populate
// finalText via Chain.Run's own redact step (only ActionRedact findings do),
// which is exactly why runInboundChain's downgrade path pulls the masked
// text from the finding itself rather than trusting finalText.
func TestChainRun_BlockActionNeverRewritesFinalText(t *testing.T) {
	c := newPIIRulesChecker(nil, false, guardrail.ActionBlock)
	chain := guardrail.NewChain([]guardrail.Checker{c}, guardrail.ChainOption{}, nil)
	finalText, action, findings := chain.Run(context.Background(), "call 13800001234 now", guardrail.DirectionInbound, nil)
	if action != guardrail.ActionBlock {
		t.Fatalf("expected block, got %q", action)
	}
	if finalText != "call 13800001234 now" {
		t.Fatalf("expected finalText to stay unredacted for a block action, got %q", finalText)
	}
	if len(findings) != 1 || findings[0].Redacted == "call 13800001234 now" {
		t.Fatalf("expected the finding itself to still carry a masked rewrite, got %+v", findings)
	}
}
