package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/opscenter/ai-gateway/internal/biz/guardrail"
	"github.com/opscenter/ai-gateway/internal/biz/mcpgw"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

// MCP gateway proxy (docs/design/09-extensibility.md "MCP gateway", points
// 1-3: proxying, tool-call governance, audit). Virtual keys authenticate the
// same way as model traffic (middleware.VirtualKeyAuth, reused for this
// route) — "one credential system for models and tools."
//
// Scope actually shipped vs. the design: single (non-batched) JSON-RPC
// messages over POST only (no GET/SSE server push, no DELETE session
// cleanup beyond a 204); tool calls consume the key's existing top-level
// request-count quota rather than a dedicated QuotaDimToolCall; agent
// sessions reuse the existing resolveGatewaySessionID heuristic via
// writeAuditLog rather than new session-affinity plumbing. See the ADR
// addendum in the design doc for the full list.

const mcpMaxBody = 4 << 20 // 4 MiB ceiling on one JSON-RPC message

// toolWhitelist mirrors allowedModelList's shape/semantics exactly: empty or
// absent = unrestricted.
func toolWhitelist(key *model.AIVirtualKey) []string {
	if len(key.ToolWhitelist) == 0 || string(key.ToolWhitelist) == "null" {
		return nil
	}
	var allowed []string
	if err := json.Unmarshal(key.ToolWhitelist, &allowed); err != nil {
		return nil
	}
	return allowed
}

func isToolAllowed(key *model.AIVirtualKey, toolName string) bool {
	allowed := toolWhitelist(key)
	if len(allowed) == 0 {
		return true
	}
	return containsString(allowed, toolName)
}

// mcpContentBlock is one entry of an MCP CallToolResult.content array.
type mcpContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func extractToolResultText(content json.RawMessage) string {
	var blocks []mcpContentBlock
	if len(content) == 0 || json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// replaceToolResultText rewrites a single-text-block result in place. Multi-
// block content is left untouched (redaction can't be unambiguously mapped
// back onto multiple original blocks) — the finding is still reported, the
// text just isn't rewritten; documented gap.
func replaceToolResultText(content json.RawMessage, newText string) json.RawMessage {
	var blocks []mcpContentBlock
	if json.Unmarshal(content, &blocks) != nil || len(blocks) != 1 || blocks[0].Type != "text" {
		return content
	}
	blocks[0].Text = newText
	out, err := json.Marshal(blocks)
	if err != nil {
		return content
	}
	return out
}

func blockedToolResultContent(reason string) json.RawMessage {
	out, _ := json.Marshal([]mcpContentBlock{{Type: "text", Text: reason}})
	return out
}

// mcpGuardrailScan runs the key's resolved guardrail chain (the same one
// applyPIIPolicy/applyOutboundGuardrail use for model traffic — docs/design/
// 09 "argument-level guardrail checks (the D06 chain runs on tool arguments/
// results)") against one piece of text. No configured policy/chain is a
// silent pass-through, matching the rest of the guardrail pipeline's
// fail-open-by-absence posture.
func (uc *GatewayUseCase) mcpGuardrailScan(ctx context.Context, key *model.AIVirtualKey, dir guardrail.Direction, text string) (finalText string, blocked bool, types string) {
	if text == "" {
		return text, false, ""
	}
	policy := uc.resolvePIIPolicy(ctx, key)
	if policy == nil || !policy.Enabled {
		return text, false, ""
	}
	chain := uc.buildChainForPolicy(policy, uc.tenantNameForKey(ctx, key))
	if chain == nil {
		return text, false, ""
	}
	out, action, findings := chain.Run(ctx, text, dir, func(f guardrail.Finding) {
		if uc.metrics != nil {
			for _, ty := range f.Types {
				uc.metrics.GuardrailActions.WithLabelValues(ty, string(f.Action)).Inc()
			}
		}
	})
	allTypes := strings.Join(allFindingTypes(findings), ",")
	switch action {
	case guardrail.ActionBlock, guardrail.ActionTerminate:
		return text, true, allTypes
	case guardrail.ActionRedact:
		return out, false, allTypes
	default:
		return text, false, allTypes
	}
}

// filterToolsList removes tools the key isn't whitelisted for from a
// tools/list response — so disallowed tools aren't just rejected on call,
// they're invisible to the agent in the first place.
func filterToolsList(key *model.AIVirtualKey, respBody []byte) []byte {
	allowed := toolWhitelist(key)
	if len(allowed) == 0 {
		return respBody
	}
	var rpcResp mcpgw.Response
	if json.Unmarshal(respBody, &rpcResp) != nil || len(rpcResp.Result) == 0 {
		return respBody
	}
	var result mcpgw.ToolsListResult
	if json.Unmarshal(rpcResp.Result, &result) != nil {
		return respBody
	}
	filtered := result.Tools[:0]
	for _, t := range result.Tools {
		if containsString(allowed, t.Name) {
			filtered = append(filtered, t)
		}
	}
	result.Tools = filtered
	newResult, err := json.Marshal(result)
	if err != nil {
		return respBody
	}
	rpcResp.Result = newResult
	out, err := json.Marshal(rpcResp)
	if err != nil {
		return respBody
	}
	return out
}

func writeMCPError(w http.ResponseWriter, statusCode int, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	w.Write(mcpgw.ErrorResponse(id, code, message))
}

// HandleMCPRequest is the MCP proxy's core: authenticate is already done by
// middleware (VirtualKeyAuth, same as model traffic); this resolves the
// named upstream server, applies tool-call governance, forwards the JSON-RPC
// message, applies outbound governance to the result, and writes one audit
// row per call (protocol="mcp", reusing the existing audit table/console
// rather than a parallel one).
func (uc *GatewayUseCase) HandleMCPRequest(ctx context.Context, key *model.AIVirtualKey, serverName string, w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	clientIP := ClientIPFromRequest(r)

	if r.Method == http.MethodDelete {
		// Stateless proxy: nothing to clean up server-side; 204 satisfies the
		// spec's session-termination contract for clients that send it.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		// GET (server-initiated SSE stream) is optional per the Streamable
		// HTTP transport spec and not implemented here — documented gap.
		writeMCPError(w, http.StatusMethodNotAllowed, nil, mcpgw.ErrCodeInvalidRequest, "this proxy only supports POST")
		return
	}

	server, apiKey, err := uc.loadMCPServerByName(ctx, serverName)
	if err != nil {
		writeMCPError(w, http.StatusNotFound, nil, mcpgw.ErrCodeInvalidRequest, "unknown MCP server: "+serverName)
		uc.WriteRejectionAuditLog(ctx, key, http.StatusNotFound, "unknown MCP server: "+serverName, clientIP, "mcp")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, mcpMaxBody+1))
	if err != nil {
		writeMCPError(w, http.StatusBadRequest, nil, mcpgw.ErrCodeInvalidRequest, "failed to read request body")
		return
	}
	if len(body) > mcpMaxBody {
		writeMCPError(w, http.StatusRequestEntityTooLarge, nil, mcpgw.ErrCodeInvalidRequest, "request body too large")
		return
	}

	req, err := mcpgw.ParseRequest(body)
	if err != nil {
		writeMCPError(w, http.StatusBadRequest, nil, mcpgw.ErrCodeInvalidRequest, err.Error())
		uc.WriteRejectionAuditLog(ctx, key, http.StatusBadRequest, err.Error(), clientIP, "mcp")
		return
	}

	forwardBody := body
	auditReqBody := body
	var toolName string

	if req.Method == mcpgw.MethodToolsCall {
		params, perr := mcpgw.ParseToolCallParams(req)
		if perr != nil {
			writeMCPError(w, http.StatusOK, req.ID, mcpgw.ErrCodeInvalidParams, perr.Error())
			uc.WriteRejectionAuditLog(ctx, key, http.StatusBadRequest, perr.Error(), clientIP, "mcp")
			return
		}
		toolName = params.Name
		auditReqBody = params.Arguments

		if !isToolAllowed(key, toolName) {
			msg := fmt.Sprintf("tool %q is not allowed for this key", toolName)
			writeMCPError(w, http.StatusOK, req.ID, mcpgw.ErrCodeToolNotAllowed, msg)
			uc.writeAuditLog(ctx, key, 0, serverName+"/"+toolName, auditReqBody, nil, 0, 0, 0, 0,
				time.Since(startTime).Milliseconds(), http.StatusForbidden, msg, false, clientIP, "mcp", 0, 0, "")
			return
		}

		finalArgs, blocked, types := uc.mcpGuardrailScan(ctx, key, guardrail.DirectionInbound, string(params.Arguments))
		if blocked {
			msg := "tool call arguments blocked by guardrail policy"
			writeMCPError(w, http.StatusOK, req.ID, mcpgw.ErrCodeGuardrailBlock, msg)
			uc.writeAuditLog(ctx, key, 0, serverName+"/"+toolName, auditReqBody, nil, 0, 0, 0, 0,
				time.Since(startTime).Milliseconds(), http.StatusForbidden, msg+" types="+types, false, clientIP, "mcp", 0, 0, "")
			return
		}
		if finalArgs != string(params.Arguments) {
			params.Arguments = json.RawMessage(finalArgs)
			auditReqBody = params.Arguments
			if paramsJSON, perr := json.Marshal(params); perr == nil {
				req.Params = paramsJSON
				if newBody, berr := json.Marshal(req); berr == nil {
					forwardBody = newBody
				}
			}
		}
	}

	client := &mcpgw.Client{BaseURL: server.BaseURL, APIKey: apiKey, HTTPClient: newProxyClient()}
	timeoutSec := uc.aiConf.AgentTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 600
	}
	fctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	fwd, ferr := client.Forward(fctx, r.Header.Get("Mcp-Session-Id"), forwardBody)
	if ferr != nil {
		writeMCPError(w, http.StatusBadGateway, req.ID, mcpgw.ErrCodeInternal, "upstream MCP server unreachable: "+ferr.Error())
		uc.writeAuditLog(ctx, key, 0, serverName+"/"+toolName, auditReqBody, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusBadGateway, ferr.Error(), false, clientIP, "mcp", 0, 0, "")
		return
	}

	respBody := fwd.Body
	auditRespBody := respBody
	isJSON := strings.Contains(fwd.ContentType, "application/json")

	switch {
	case req.Method == mcpgw.MethodToolsCall && isJSON && fwd.StatusCode == http.StatusOK:
		var rpcResp mcpgw.Response
		if json.Unmarshal(respBody, &rpcResp) == nil && len(rpcResp.Result) > 0 {
			var result mcpgw.ToolCallResult
			if json.Unmarshal(rpcResp.Result, &result) == nil {
				resultText := extractToolResultText(result.Content)
				finalText, blocked, _ := uc.mcpGuardrailScan(ctx, key, guardrail.DirectionOutbound, resultText)
				if blocked {
					result.Content = blockedToolResultContent("tool result blocked by guardrail policy")
					result.IsError = true
				} else if finalText != resultText {
					result.Content = replaceToolResultText(result.Content, finalText)
				}
				if newResult, merr := json.Marshal(result); merr == nil {
					rpcResp.Result = newResult
					if newBody, merr2 := json.Marshal(rpcResp); merr2 == nil {
						respBody = newBody
					}
				}
				auditRespBody, _ = json.Marshal(result)
			}
		}
	case req.Method == mcpgw.MethodToolsList && isJSON && fwd.StatusCode == http.StatusOK:
		respBody = filterToolsList(key, respBody)
		auditRespBody = respBody
	}

	if fwd.SessionID != "" {
		w.Header().Set("Mcp-Session-Id", fwd.SessionID)
	}
	w.Header().Set("Content-Type", fwd.ContentType)
	w.WriteHeader(fwd.StatusCode)
	w.Write(respBody)

	modelField := serverName
	if toolName != "" {
		modelField = serverName + "/" + toolName
	}
	uc.writeAuditLog(ctx, key, 0, modelField, auditReqBody, auditRespBody, 0, 0, 0, 0,
		time.Since(startTime).Milliseconds(), fwd.StatusCode, "", false, clientIP, "mcp", 0, 0, "")
}
