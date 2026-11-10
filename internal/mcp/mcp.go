// Package mcp implements the minimal subset of the Model Context Protocol
// (MCP, streamable-http transport) needed to expose server tools to MCP
// clients such as Mistral Vibe. It is transport-agnostic and dependency-free:
// the caller feeds it a raw JSON-RPC request body and writes back the
// response body (or nothing at all for notifications).
//
// Deliberate limitations: single message per request (no batching), stateless
// (no session IDs, no server-initiated streams), plain application/json
// responses. This is spec-compliant for clients that operate request/response.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// JSONRPCVersion is the JSON-RPC 2.0 protocol tag.
const JSONRPCVersion = "2.0"

// LatestProtocolVersion is the most recent MCP protocol revision this server
// speaks. Older revisions in supportedProtocolVersions are echoed back when
// the client requests them.
const LatestProtocolVersion = "2025-06-18"

var supportedProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Request is a single JSON-RPC request or notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	ID      json.RawMessage `json:"id,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC response. Exactly one of Result / Error is set.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolHandler executes a tool call. args is the caller-supplied arguments
// object (JSON, possibly "null" when the client omitted arguments). Return
// values: a string is used verbatim as text content, a CallToolResult passes
// through, anything else is JSON-marshalled as text content. An error is
// reported in-band as a tool error (isError: true); wrap it in
// InvalidParamsError to report a -32602 protocol error instead.
type ToolHandler func(ctx context.Context, args json.RawMessage) (any, error)

// InvalidParamsError marks malformed tool input so the server reports it as
// JSON-RPC error -32602 rather than an in-band tool failure.
type InvalidParamsError struct {
	Message string
}

func (e *InvalidParamsError) Error() string { return e.Message }

// InvalidParamsf builds an InvalidParamsError.
func InvalidParamsf(format string, args ...any) error {
	return &InvalidParamsError{Message: fmt.Sprintf(format, args...)}
}

// Tool is the declaration of a callable tool. InputSchema is a JSON Schema
// object describing the arguments object.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Content is a content block in a tool result. Only text content is produced.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CallToolResult is the result payload of a tools/call request.
type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError"`
}

type ToolDefinition struct {
	Tool    Tool
	Handler ToolHandler
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type ServerCapabilities struct {
	Tools ToolsCapability `json:"tools"`
}

// InitializeResult is the payload returned for the initialize request.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
}

type ToolListResult struct {
	Tools []Tool `json:"tools"`
}

type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Server dispatches JSON-RPC requests to a set of registered tools.
type Server struct {
	info  ServerInfo
	tools []ToolDefinition
}

// NewServer creates an empty MCP server with the given identity.
func NewServer(name, version string) *Server {
	return &Server{info: ServerInfo{Name: name, Version: version}}
}

// AddTool registers a tool. Tool names must be unique; a duplicate
// registration panics, which is a programming error in the caller.
func (s *Server) AddTool(def ToolDefinition) {
	for _, t := range s.tools {
		if t.Tool.Name == def.Tool.Name {
			panic("mcp: duplicate tool name " + def.Tool.Name)
		}
	}
	s.tools = append(s.tools, def)
}

// Tools returns the registered tool declarations, in registration order.
func (s *Server) Tools() []Tool {
	tools := make([]Tool, len(s.tools))
	for i, t := range s.tools {
		tools[i] = t.Tool
	}
	return tools
}

// Handle processes one raw JSON-RPC message. The second return value is false
// when the message was a notification (or an unknown notification) and no
// response body should be written.
func (s *Server) Handle(ctx context.Context, body []byte) (Response, bool) {
	var raw json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return parseError(), true
	}
	trimmed := trimJSONSpace(raw)
	// Request batching is not supported.
	if len(trimmed) == 0 || trimmed[0] == '[' {
		return Response{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage("null"),
			Error:   &Error{Code: CodeInvalidRequest, Message: "invalid request: expected a single JSON-RPC message"},
		}, true
	}

	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return parseError(), true
	}
	if isNotification(req.ID) {
		// Notifications never get a response, including unknown ones.
		return Response{}, false
	}

	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		return Response{
			JSONRPC: JSONRPCVersion,
			ID:      req.ID,
			Result: InitializeResult{
				ProtocolVersion: negotiateProtocolVersion(params.ProtocolVersion),
				Capabilities:    ServerCapabilities{Tools: ToolsCapability{ListChanged: false}},
				ServerInfo:      s.info,
			},
		}, true

	case "ping":
		return Response{JSONRPC: JSONRPCVersion, ID: req.ID, Result: struct{}{}}, true

	case "tools/list":
		tools := []Tool{}
		for _, t := range s.tools {
			tools = append(tools, t.Tool)
		}
		return Response{
			JSONRPC: JSONRPCVersion,
			ID:     req.ID,
			Result: ToolListResult{Tools: tools},
		}, true

	case "tools/call":
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
			return Response{
				JSONRPC: JSONRPCVersion,
				ID:      req.ID,
				Error:   &Error{Code: CodeInvalidParams, Message: "invalid tools/call params: name is required"},
			}, true
		}
		def, ok := s.tool(params.Name)
		if !ok {
			return Response{
				JSONRPC: JSONRPCVersion,
				ID:      req.ID,
				Error:   &Error{Code: CodeInvalidParams, Message: fmt.Sprintf("unknown tool: %s", params.Name)},
			}, true
		}
		args := params.Arguments
		if len(args) == 0 {
			args = json.RawMessage("null")
		}
		result, err := def.Handler(ctx, args)
		if err != nil {
			var invalid *InvalidParamsError
			if errors.As(err, &invalid) {
				return Response{
					JSONRPC: JSONRPCVersion,
					ID:      req.ID,
					Error:   &Error{Code: CodeInvalidParams, Message: invalid.Message},
				}, true
			}
			return Response{
				JSONRPC: JSONRPCVersion,
				ID:      req.ID,
				Result:  CallToolResult{Content: []Content{{Type: "text", Text: err.Error()}}, IsError: true},
			}, true
		}
		return Response{
			JSONRPC: JSONRPCVersion,
			ID:      req.ID,
			Result:  toolResultContent(result),
		}, true

	default:
		return Response{
			JSONRPC: JSONRPCVersion,
			ID:      req.ID,
			Error:   &Error{Code: CodeMethodNotFound, Message: fmt.Sprintf("method not found: %s", req.Method)},
		}, true
	}
}

func (s *Server) tool(name string) (ToolDefinition, bool) {
	for _, t := range s.tools {
		if t.Tool.Name == name {
			return t, true
		}
	}
	return ToolDefinition{}, false
}

// toolResultContent converts a handler return value into a CallToolResult.
func toolResultContent(result any) CallToolResult {
	switch v := result.(type) {
	case nil:
		return CallToolResult{Content: []Content{{Type: "text", Text: "OK"}}}
	case string:
		return CallToolResult{Content: []Content{{Type: "text", Text: v}}}
	case CallToolResult:
		return v
	default:
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return CallToolResult{
				Content: []Content{{Type: "text", Text: fmt.Sprintf("mcp: encode tool result: %v", err)}},
				IsError: true,
			}
		}
		return CallToolResult{Content: []Content{{Type: "text", Text: string(encoded)}}}
	}
}

func isNotification(id json.RawMessage) bool {
	trimmed := trimJSONSpace(id)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

func trimJSONSpace(raw json.RawMessage) []byte {
	for len(raw) > 0 && (raw[0] == ' ' || raw[0] == '\t' || raw[0] == '\r' || raw[0] == '\n') {
		raw = raw[1:]
	}
	return raw
}

// negotiateProtocolVersion echoes the client's requested revision when it is
// supported, and otherwise offers the latest one.
func negotiateProtocolVersion(requested string) string {
	for _, v := range supportedProtocolVersions {
		if requested == v {
			return v
		}
	}
	return LatestProtocolVersion
}

func parseError() Response {
	return Response{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage("null"),
		Error:   &Error{Code: CodeParseError, Message: "parse error: invalid JSON"},
	}
}
