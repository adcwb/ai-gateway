package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

const (
	stickySessionTTL = time.Hour
	stickyKeyFmt     = "ai:gw:sticky:%s"
)

type stickyRecord struct {
	ProviderID uint   `json:"p"`
	Model      string `json:"m"`
}

func extractSessionHash(key *model.AIVirtualKey, r *http.Request, body []byte) string {
	scope := ""
	if key != nil {
		scope = fmt.Sprintf("k:%d|", key.ID)
	}
	if r != nil {
		if sid := strings.TrimSpace(r.Header.Get("X-Session-ID")); sid != "" {
			return hashSessionKey(scope + "sid:" + sid)
		}
	}
	if len(body) > 0 {
		if pck := extractPromptCacheKey(body); pck != "" {
			return hashSessionKey(scope + "pck:" + pck)
		}
		if prefix := contentPrefixSignature(body); prefix != "" {
			return hashSessionKey(scope + "cpx:" + prefix)
		}
	}
	return ""
}

func extractPromptCacheKey(body []byte) string {
	var req struct {
		PromptCacheKey string `json:"prompt_cache_key"`
	}
	_ = json.Unmarshal(body, &req)
	return strings.TrimSpace(req.PromptCacheKey)
}

func contentPrefixSignature(body []byte) string {
	var req map[string]json.RawMessage
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	var b strings.Builder
	for _, field := range []string{"system", "instructions", "tools", "input"} {
		if v, ok := req[field]; ok {
			b.Write(v)
		}
	}
	if v, ok := req["messages"]; ok {
		var msgs []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(v, &msgs) == nil {
			var firstSystem, firstUser json.RawMessage
			for _, m := range msgs {
				if m.Role == "system" && firstSystem == nil {
					firstSystem = m.Content
				}
				if m.Role == "user" && firstUser == nil {
					firstUser = m.Content
					break
				}
			}
			b.Write(firstSystem)
			b.Write(firstUser)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}

func getStickySession(ctx context.Context, rdb *redis.Client, sessionHash string) stickyRecord {
	if sessionHash == "" || rdb == nil {
		return stickyRecord{}
	}
	val, err := rdb.Get(ctx, fmt.Sprintf(stickyKeyFmt, sessionHash)).Bytes()
	if err != nil {
		return stickyRecord{}
	}
	var rec stickyRecord
	if json.Unmarshal(val, &rec) != nil {
		return stickyRecord{}
	}
	return rec
}

func setStickySession(ctx context.Context, rdb *redis.Client, sessionHash string, providerID uint, model string) {
	if sessionHash == "" || providerID == 0 || rdb == nil {
		return
	}
	data, err := json.Marshal(stickyRecord{ProviderID: providerID, Model: model})
	if err != nil {
		return
	}
	rdb.Set(ctx, fmt.Sprintf(stickyKeyFmt, sessionHash), data, stickySessionTTL)
}

func clearStickySession(ctx context.Context, rdb *redis.Client, sessionHash string) {
	if sessionHash == "" || rdb == nil {
		return
	}
	rdb.Del(ctx, fmt.Sprintf(stickyKeyFmt, sessionHash))
}
