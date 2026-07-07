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
// Scope: batched ([]) JSON-RPC requests over POST, and a GET/SSE server-push
// passthrough, are both supported (see handleOneMCPMessage/handleMCPStream
// below); DELETE (session termination) returns 204 with no server-side state
// to clean up — this is a stateless proxy, sessions are just an opaque
// Mcp-Session-Id mirrored to/from the upstream server. Tool calls consume a
// dedicated QuotaDimToolCall budget (HourlyToolCallQuota) in addition to the
// key's existing top-level request-count quota that VirtualKeyAuth.ProxyMiddleware
// already reserves for every route.

const (
	mcpMaxBody      = 4 << 20 // 4 MiB ceiling on one JSON-RPC message
	mcpMaxBatchSize = 20      // cap on messages per batched ([]) request
)

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
// named upstream server, then dispatches to the batched-POST handler or the
// GET/SSE stream handler.
func (uc *GatewayUseCase) HandleMCPRequest(ctx context.Context, key *model.AIVirtualKey, serverName string, w http.ResponseWriter, r *http.Request) {
	clientIP := ClientIPFromRequest(r)

	if r.Method == http.MethodDelete {
		// Stateless proxy: nothing to clean up server-side; 204 satisfies the
		// spec's session-termination contract for clients that send it.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	server, apiKey, err := uc.loadMCPServerByName(ctx, serverName)
	if err != nil {
		writeMCPError(w, http.StatusNotFound, nil, mcpgw.ErrCodeInvalidRequest, "unknown MCP server: "+serverName)
		uc.WriteRejectionAuditLog(ctx, key, http.StatusNotFound, "unknown MCP server: "+serverName, clientIP, "mcp")
		return
	}

	if r.Method == http.MethodGet {
		uc.handleMCPStream(ctx, key, server, apiKey, serverName, w, r, clientIP)
		return
	}
	if r.Method != http.MethodPost {
		writeMCPError(w, http.StatusMethodNotAllowed, nil, mcpgw.ErrCodeInvalidRequest, "this proxy only supports GET/POST/DELETE")
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

	reqs, isBatch, err := mcpgw.ParseBatch(body)
	if err != nil {
		writeMCPError(w, http.StatusBadRequest, nil, mcpgw.ErrCodeInvalidRequest, err.Error())
		uc.WriteRejectionAuditLog(ctx, key, http.StatusBadRequest, err.Error(), clientIP, "mcp")
		return
	}
	if len(reqs) > mcpMaxBatchSize {
		msg := fmt.Sprintf("batch too large: max %d messages per request", mcpMaxBatchSize)
		writeMCPError(w, http.StatusBadRequest, nil, mcpgw.ErrCodeInvalidRequest, msg)
		uc.WriteRejectionAuditLog(ctx, key, http.StatusBadRequest, msg, clientIP, "mcp")
		return
	}

	client := &mcpgw.Client{BaseURL: server.BaseURL, APIKey: apiKey, HTTPClient: newProxyClient()}
	timeoutSec := uc.aiConf.AgentTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 600
	}

	sessionID := r.Header.Get("Mcp-Session-Id")
	var responses []json.RawMessage
	lastStatus := http.StatusOK
	lastContentType := "application/json"

	for _, req := range reqs {
		fctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		respBody, newSessionID, statusCode, contentType := uc.handleOneMCPMessage(fctx, key, client, serverName, req, sessionID, clientIP)
		cancel()
		if newSessionID != "" {
			sessionID = newSessionID
		}
		lastStatus = statusCode
		lastContentType = contentType
		if respBody != nil {
			responses = append(responses, respBody)
		}
	}

	if sessionID != "" {
		w.Header().Set("Mcp-Session-Id", sessionID)
	}

	if isBatch {
		// JSON-RPC 2.0 batch response: an array of the response objects for
		// every message that carried an id; a batch of only notifications
		// gets no body (mirrored below for the single-message case too).
		if len(responses) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		out, _ := json.Marshal(responses)
		w.Write(out)
		return
	}

	if len(responses) == 0 {
		// The lone message was a notification (no id) — no response object
		// expected; 202 Accepted per the Streamable HTTP transport spec.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", lastContentType)
	w.WriteHeader(lastStatus)
	w.Write(responses[0])
}

// handleOneMCPMessage runs governance (tool whitelist, dedicated tool-call
// quota, guardrail chain) on and forwards one JSON-RPC message, then writes
// one audit row for it — the unit of work fanned out over a batch, or called
// once for a lone (non-batched) message. respBody is nil for a notification
// (a message with no "id"), which per JSON-RPC 2.0 gets no response object.
func (uc *GatewayUseCase) handleOneMCPMessage(ctx context.Context, key *model.AIVirtualKey, client *mcpgw.Client, serverName string, req *mcpgw.Request, sessionID, clientIP string) (respBody []byte, newSessionID string, statusCode int, contentType string) {
	startTime := time.Now()
	isNotification := len(req.ID) == 0
	wrap := func(body []byte) []byte {
		if isNotification {
			return nil
		}
		return body
	}

	forwardBody, merr := json.Marshal(req)
	if merr != nil {
		return wrap(mcpgw.ErrorResponse(req.ID, mcpgw.ErrCodeInternal, "failed to re-encode message")), "", http.StatusOK, "application/json"
	}
	auditReqBody := forwardBody
	var toolName string

	if req.Method == mcpgw.MethodToolsCall {
		params, perr := mcpgw.ParseToolCallParams(req)
		if perr != nil {
			uc.writeAuditLog(ctx, key, 0, serverName, forwardBody, nil, 0, 0, 0, 0,
				time.Since(startTime).Milliseconds(), http.StatusBadRequest, perr.Error(), false, clientIP, "mcp", 0, 0, "")
			return wrap(mcpgw.ErrorResponse(req.ID, mcpgw.ErrCodeInvalidParams, perr.Error())), "", http.StatusOK, "application/json"
		}
		toolName = params.Name
		auditReqBody = params.Arguments

		if !isToolAllowed(key, toolName) {
			msg := fmt.Sprintf("tool %q is not allowed for this key", toolName)
			uc.writeAuditLog(ctx, key, 0, serverName+"/"+toolName, auditReqBody, nil, 0, 0, 0, 0,
				time.Since(startTime).Milliseconds(), http.StatusForbidden, msg, false, clientIP, "mcp", 0, 0, "")
			return wrap(mcpgw.ErrorResponse(req.ID, mcpgw.ErrCodeToolNotAllowed, msg)), "", http.StatusOK, "application/json"
		}

		if qerr := uc.quota.CheckAndReserveToolCall(ctx, key); qerr != nil {
			uc.writeAuditLog(ctx, key, 0, serverName+"/"+toolName, auditReqBody, nil, 0, 0, 0, 0,
				time.Since(startTime).Milliseconds(), http.StatusTooManyRequests, qerr.Error(), false, clientIP, "mcp", 0, 0, "")
			return wrap(mcpgw.ErrorResponse(req.ID, mcpgw.ErrCodeToolCallQuotaExceeded, qerr.Error())), "", http.StatusOK, "application/json"
		}

		finalArgs, blocked, types := uc.mcpGuardrailScan(ctx, key, guardrail.DirectionInbound, string(params.Arguments))
		if blocked {
			msg := "tool call arguments blocked by guardrail policy"
			uc.writeAuditLog(ctx, key, 0, serverName+"/"+toolName, auditReqBody, nil, 0, 0, 0, 0,
				time.Since(startTime).Milliseconds(), http.StatusForbidden, msg+" types="+types, false, clientIP, "mcp", 0, 0, "")
			return wrap(mcpgw.ErrorResponse(req.ID, mcpgw.ErrCodeGuardrailBlock, msg)), "", http.StatusOK, "application/json"
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

	fwd, ferr := client.Forward(ctx, sessionID, forwardBody)
	if ferr != nil {
		modelField := serverName
		if toolName != "" {
			modelField = serverName + "/" + toolName
		}
		uc.writeAuditLog(ctx, key, 0, modelField, auditReqBody, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusBadGateway, ferr.Error(), false, clientIP, "mcp", 0, 0, "")
		return wrap(mcpgw.ErrorResponse(req.ID, mcpgw.ErrCodeInternal, "upstream MCP server unreachable: "+ferr.Error())), "", http.StatusBadGateway, "application/json"
	}

	respBodyOut := fwd.Body
	auditRespBody := respBodyOut
	isJSON := strings.Contains(fwd.ContentType, "application/json")

	switch {
	case req.Method == mcpgw.MethodToolsCall && isJSON && fwd.StatusCode == http.StatusOK:
		var rpcResp mcpgw.Response
		if json.Unmarshal(respBodyOut, &rpcResp) == nil && len(rpcResp.Result) > 0 {
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
						respBodyOut = newBody
					}
				}
				auditRespBody, _ = json.Marshal(result)
			}
		}
	case req.Method == mcpgw.MethodToolsList && isJSON && fwd.StatusCode == http.StatusOK:
		respBodyOut = filterToolsList(key, respBodyOut)
		auditRespBody = respBodyOut
	}

	modelField := serverName
	if toolName != "" {
		modelField = serverName + "/" + toolName
	}
	uc.writeAuditLog(ctx, key, 0, modelField, auditReqBody, auditRespBody, 0, 0, 0, 0,
		time.Since(startTime).Milliseconds(), fwd.StatusCode, "", false, clientIP, "mcp", 0, 0, "")

	return wrap(respBodyOut), fwd.SessionID, fwd.StatusCode, fwd.ContentType
}

// handleMCPStream proxies the Streamable HTTP transport's optional
// server-initiated GET/SSE push: forward the GET to the upstream server and
// relay its bytes to the client as they arrive, flushing after every chunk.
// This is a raw byte passthrough — no per-message governance (tool
// whitelist/guardrail) runs on server-pushed content, matching the design
// doc's existing documented-gap posture for GET. One audit row is written
// when the stream ends, covering the connection's lifetime rather than a
// single JSON-RPC request/response pair.
func (uc *GatewayUseCase) handleMCPStream(ctx context.Context, key *model.AIVirtualKey, server *model.AIMCPServer, apiKey, serverName string, w http.ResponseWriter, r *http.Request, clientIP string) {
	startTime := time.Now()
	client := &mcpgw.Client{BaseURL: server.BaseURL, APIKey: apiKey, HTTPClient: newProxyClient()}
	sessionID := r.Header.Get("Mcp-Session-Id")

	upstreamBody, header, statusCode, err := client.ForwardStream(ctx, sessionID)
	if err != nil {
		writeMCPError(w, http.StatusBadGateway, nil, mcpgw.ErrCodeInternal, "upstream MCP server unreachable: "+err.Error())
		uc.writeAuditLog(ctx, key, 0, serverName+"/stream", nil, nil, 0, 0, 0, 0,
			time.Since(startTime).Milliseconds(), http.StatusBadGateway, err.Error(), false, clientIP, "mcp", 0, 0, "")
		return
	}
	defer upstreamBody.Close()

	if ct := header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if sid := header.Get("Mcp-Session-Id"); sid != "" {
		w.Header().Set("Mcp-Session-Id", sid)
	}
	w.WriteHeader(statusCode)

	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
readLoop:
	for {
		select {
		case <-r.Context().Done():
			break readLoop
		default:
		}
		n, rerr := upstreamBody.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				break readLoop
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			break readLoop
		}
	}

	uc.writeAuditLog(ctx, key, 0, serverName+"/stream", nil, nil, 0, 0, 0, 0,
		time.Since(startTime).Milliseconds(), statusCode, "", false, clientIP, "mcp", 0, 0, "")
}
