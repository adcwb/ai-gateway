// Package mcpgw implements the client-side mechanics of MCP (Model Context
// Protocol) Streamable HTTP: JSON-RPC 2.0 message shapes and a client that
// forwards a message to an upstream MCP server. It is dependency-free with
// respect to package biz — the same split used for internal/biz/guardrail and
// internal/biz/vectorindex — so tool-call governance (whitelists, the
// guardrail chain, quota, audit) lives in biz as the consumer.
package mcpgw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// JSON-RPC 2.0 message shapes (docs/design/09-extensibility.md "MCP gateway";
// spec: https://modelcontextprotocol.io, Streamable HTTP transport).

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes used by the gateway's own governance
// rejections (upstream errors are relayed verbatim, whatever code they used).
const (
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603
	// ErrCodeToolNotAllowed is in the reserved "server error" range
	// (-32000 to -32099), used for the gateway's own tool-whitelist/
	// guardrail rejections (not part of the upstream server's error space).
	ErrCodeToolNotAllowed = -32001
	ErrCodeGuardrailBlock = -32002
)

const (
	MethodInitialize = "initialize"
	MethodToolsList  = "tools/list"
	MethodToolsCall  = "tools/call"
)

// ToolCallParams is tools/call's params shape.
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolCallResult is tools/call's result shape.
type ToolCallResult struct {
	Content           json.RawMessage `json:"content,omitempty"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// Tool is one entry in tools/list's result.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type ToolsListResult struct {
	Tools      []Tool          `json:"tools"`
	NextCursor string          `json:"nextCursor,omitempty"`
	Extra      json.RawMessage `json:"-"` // unused; kept for documentation of the shape
}

// ParseRequest decodes one JSON-RPC message. Batched requests (a JSON array)
// are not supported — Streamable HTTP servers overwhelmingly receive one
// message per POST in practice, and this proxy would need to fan out
// per-message governance/audit for a batch, which is deferred (documented
// gap, docs/design/09-extensibility.md ADR addendum).
func ParseRequest(body []byte) (*Request, error) {
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("mcpgw: invalid JSON-RPC message: %w", err)
	}
	if req.Method == "" {
		return nil, fmt.Errorf("mcpgw: missing method")
	}
	return &req, nil
}

func ParseToolCallParams(req *Request) (*ToolCallParams, error) {
	var p ToolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, fmt.Errorf("mcpgw: invalid tools/call params: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("mcpgw: tools/call missing tool name")
	}
	return &p, nil
}

// ErrorResponse builds a gateway-originated JSON-RPC error response (as
// opposed to relaying an upstream error).
func ErrorResponse(id json.RawMessage, code int, message string) []byte {
	resp := Response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: message}}
	b, _ := json.Marshal(resp)
	return b
}

// Client forwards one JSON-RPC message to an upstream Streamable HTTP MCP
// server (docs/design/09-extensibility.md point 1, "MCP proxying").
type Client struct {
	BaseURL    string
	APIKey     string // optional bearer credential to the upstream server
	HTTPClient *http.Client
}

// ForwardResult is what the upstream server returned for one forwarded message.
type ForwardResult struct {
	Body        []byte
	ContentType string // "application/json" or "text/event-stream"
	SessionID   string // Mcp-Session-Id, if the upstream assigned/echoed one
	StatusCode  int
}

// Forward POSTs body to the upstream MCP endpoint, mirroring the client's
// session ID if one is already established (docs/design/09 "Streamable HTTP
// Transport" session management: MUST echo Mcp-Session-Id once assigned).
func (c *Client) Forward(ctx context.Context, sessionID string, body []byte) (*ForwardResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB ceiling on a single tool result
	if err != nil {
		return nil, err
	}
	contentType := "application/json"
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		contentType = ct
	}
	return &ForwardResult{
		Body:        respBody,
		ContentType: contentType,
		SessionID:   resp.Header.Get("Mcp-Session-Id"),
		StatusCode:  resp.StatusCode,
	}, nil
}
