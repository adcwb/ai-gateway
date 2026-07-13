package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/adcwb/ai-gateway/internal/data/model"
)

const (
	auditSessionGapTTL = 10 * time.Minute
	auditOpeningKeyFmt = "ai:gw:osess:%s"
)

type sessionNativeCtxKey struct{}

func withSessionNative(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionNativeCtxKey{}, id)
}

func sessionNativeFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(sessionNativeCtxKey{}).(string); ok {
		return v
	}
	return ""
}

var sessionHeaderNames = []string{
	"X-Session-ID",
	"X-Session-Affinity",
	"X-Claude-Session-ID",
	"Conversation-Id",
	"Session-Id",
	"session_id",
	"X-Litellm-Session-Id",
	"Helicone-Session-Id",
}

func extractNativeSessionID(key *model.AIVirtualKey, r *http.Request, body []byte) string {
	if r != nil {
		for _, name := range sessionHeaderNames {
			if v := strings.TrimSpace(r.Header.Get(name)); v != "" {
				return hashSessionKey(fmt.Sprintf("h:%d:%s", key.ID, v))
			}
		}
	}
	if len(body) > 0 {
		if v := extractBodySessionField(body); v != "" {
			return hashSessionKey(fmt.Sprintf("b:%d:%s", key.ID, v))
		}
	}
	return ""
}

func extractBodySessionField(body []byte) string {
	var req struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
		PromptCacheKey string          `json:"prompt_cache_key"`
		ConversationID string          `json:"conversation_id"`
		Conversation   json.RawMessage `json:"conversation"`
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	if s := strings.TrimSpace(req.Metadata.UserID); s != "" {
		return "auid:" + s
	}
	if s := strings.TrimSpace(req.PromptCacheKey); s != "" {
		return "pck:" + s
	}
	if s := strings.TrimSpace(req.ConversationID); s != "" {
		return "cid:" + s
	}
	if id := extractConversationID(req.Conversation); id != "" {
		return "conv:" + id
	}
	return ""
}

func extractConversationID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	var obj struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &obj) == nil && strings.TrimSpace(obj.ID) != "" {
		return strings.TrimSpace(obj.ID)
	}
	return ""
}

func resolveGatewaySessionID(ctx context.Context, rdb *redis.Client, key *model.AIVirtualKey, reqBody []byte, clientIP string) string {
	if id := sessionNativeFromCtx(ctx); id != "" {
		return id
	}
	if rdb == nil || len(reqBody) == 0 {
		return ""
	}
	msgs := canonicalMessages(reqBody)
	if len(msgs) == 0 {
		return ""
	}
	agent := clientAgentFromCtx(ctx)
	k := fmt.Sprintf(auditOpeningKeyFmt, openingSignature(key.ID, agent, clientIP, msgs))
	if sid, err := rdb.Get(ctx, k).Result(); err == nil && sid != "" {
		rdb.Expire(ctx, k, auditSessionGapTTL)
		return sid
	}
	sid := mintSessionID(key.ID, agent)
	rdb.Set(ctx, k, sid, auditSessionGapTTL)
	return sid
}

func canonicalMessages(body []byte) [][]byte {
	var req struct {
		Messages []json.RawMessage `json:"messages"`
		Input    json.RawMessage   `json:"input"`
	}
	if json.Unmarshal(body, &req) != nil {
		return nil
	}
	raws := req.Messages
	if len(raws) == 0 && len(req.Input) > 0 {
		var arr []json.RawMessage
		if json.Unmarshal(req.Input, &arr) == nil && len(arr) > 0 {
			raws = arr
		} else {
			raws = []json.RawMessage{req.Input}
		}
	}
	if len(raws) == 0 {
		return nil
	}
	out := make([][]byte, 0, len(raws))
	for _, m := range raws {
		out = append(out, canonicalJSON(m))
	}
	return out
}

func canonicalJSON(raw []byte) []byte {
	var v interface{}
	if json.Unmarshal(raw, &v) != nil {
		return append([]byte(nil), raw...)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return append([]byte(nil), raw...)
	}
	return b
}

func openingSignature(keyID uint, agent, clientIP string, msgs [][]byte) string {
	n := 2
	if len(msgs) < n {
		n = len(msgs)
	}
	h := sha256.New()
	fmt.Fprintf(h, "%d\x1f%s\x1f%s\x1f", keyID, agent, clientIP)
	for i := 0; i < n; i++ {
		h.Write(msgs[i])
		h.Write([]byte{0x1e})
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func mintSessionID(keyID uint, agent string) string {
	return hashSessionKey(fmt.Sprintf("new:%d:%s:%d", keyID, agent, time.Now().UnixNano()))
}

func hashSessionKey(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}
