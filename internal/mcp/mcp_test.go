package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func newTestRegistry() *Server {
	reg := NewServer("test-server", "0.1")
	reg.AddTool(ToolDefinition{
		Tool: Tool{
			Name:        "echo",
			Description: "echoes text",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return nil, InvalidParamsf("invalid arguments")
			}
			return in.Text, nil
		},
	})
	reg.AddTool(ToolDefinition{
		Tool: Tool{
			Name:        "sum",
			Description: "adds up counts",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return map[string]int{"count": 2}, nil
		},
	})
	reg.AddTool(ToolDefinition{
		Tool: Tool{
			Name:        "reject",
			Description: "rejects input",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return nil, InvalidParamsf("too many rejections")
		},
	})
	reg.AddTool(ToolDefinition{
		Tool: Tool{
			Name:        "broken",
			Description: "always fails",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return nil, errors.New("internal kaboom")
		},
	})
	return reg
}

func decode(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return raw
}

func TestHandleInitializeEchoesClientVersion(t *testing.T) {
	reg := newTestRegistry()
	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	resp, respond := reg.Handle(context.Background(), body)
	if !respond {
		t.Fatal("initialize must respond")
	}
	if string(resp.ID) != "7" {
		t.Fatalf("id echo = %s, want 7", resp.ID)
	}
	var result InitializeResult
	if err := json.Unmarshal(decode(t, resp.Result), &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if result.ProtocolVersion != "2025-03-26" {
		t.Fatalf("protocolVersion = %q, want 2025-03-26", result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "test-server" || result.ServerInfo.Version != "0.1" {
		t.Fatalf("serverInfo = %+v", result.ServerInfo)
	}
	var raw map[string]any
	if err := json.Unmarshal(decode(t, resp.Result), &raw); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	caps, ok := raw["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities missing from initialize result: %v", raw)
	}
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("tools capability not advertised: %v", caps)
	}
}

func TestHandleInitializeUnknownVersionFallsBackToLatest(t *testing.T) {
	reg := newTestRegistry()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	resp, _ := reg.Handle(context.Background(), body)
	var result InitializeResult
	if err := json.Unmarshal(decode(t, resp.Result), &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if result.ProtocolVersion != LatestProtocolVersion {
		t.Fatalf("protocolVersion = %q, want %q", result.ProtocolVersion, LatestProtocolVersion)
	}
}

func TestHandleNotificationIsNotAnswered(t *testing.T) {
	reg := newTestRegistry()
	for _, body := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/whatever","params":{}}`,
		`{"jsonrpc":"2.0","id":null,"method":"notifications/initialized"}`,
	} {
		resp, respond := reg.Handle(context.Background(), []byte(body))
		if respond {
			t.Fatalf("notification %s must not be answered (got %+v)", body, resp)
		}
	}
}

func TestHandleParseError(t *testing.T) {
	reg := newTestRegistry()
	resp, respond := reg.Handle(context.Background(), []byte(`not json at all`))
	if !respond {
		t.Fatal("parse error must respond")
	}
	if resp.Error == nil || resp.Error.Code != CodeParseError {
		t.Fatalf("error = %+v, want code %d", resp.Error, CodeParseError)
	}
	if string(resp.ID) != "null" {
		t.Fatalf("id = %s, want null", resp.ID)
	}
}

func TestHandleBatchRejected(t *testing.T) {
	reg := newTestRegistry()
	resp, respond := reg.Handle(context.Background(), []byte(`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`))
	if !respond {
		t.Fatal("invalid request must respond")
	}
	if resp.Error == nil || resp.Error.Code != CodeInvalidRequest {
		t.Fatalf("error = %+v, want code %d", resp.Error, CodeInvalidRequest)
	}
}

func TestHandleMethodNotFound(t *testing.T) {
	reg := newTestRegistry()
	resp, respond := reg.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":"abc","method":"no/such"}`))
	if !respond {
		t.Fatal("unknown method must respond")
	}
	if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("error = %+v, want code %d", resp.Error, CodeMethodNotFound)
	}
	if string(resp.ID) != `"abc"` {
		t.Fatalf("id = %s, want \"abc\"", resp.ID)
	}
}

func TestHandlePing(t *testing.T) {
	reg := newTestRegistry()
	resp, respond := reg.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if !respond || resp.Error != nil || resp.Result == nil {
		t.Fatalf("ping handled incorrectly: %+v respond=%v", resp, respond)
	}
}

func TestToolsList(t *testing.T) {
	reg := newTestRegistry()
	resp, _ := reg.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	var result ToolListResult
	if err := json.Unmarshal(decode(t, resp.Result), &result); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if len(result.Tools) != 4 {
		t.Fatalf("tools = %d, want 4", len(result.Tools))
	}
	if result.Tools[0].Name != "echo" || len(result.Tools[0].InputSchema) == 0 {
		t.Fatalf("first tool = %+v", result.Tools[0])
	}
}

func TestToolsCallStringResult(t *testing.T) {
	reg := newTestRegistry()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hello"}}}`)
	resp, _ := reg.Handle(context.Background(), body)
	var result CallToolResult
	if err := json.Unmarshal(decode(t, resp.Result), &result); err != nil {
		t.Fatalf("decode tools/call: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error")
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" || result.Content[0].Text != "hello" {
		t.Fatalf("content = %+v", result.Content)
	}
}

func TestToolsCallNilArgumentsBecomeNull(t *testing.T) {
	reg := newTestRegistry()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":null}}`)
	resp, _ := reg.Handle(context.Background(), body)
	var result CallToolResult
	if err := json.Unmarshal(decode(t, resp.Result), &result); err != nil {
		t.Fatalf("decode tools/call: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "" {
		t.Fatalf("content = %+v", result.Content)
	}
}

func TestToolsCallStructResultIsJSON(t *testing.T) {
	reg := newTestRegistry()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sum"}}`)
	resp, _ := reg.Handle(context.Background(), body)
	var result CallToolResult
	if err := json.Unmarshal(decode(t, resp.Result), &result); err != nil {
		t.Fatalf("decode tools/call: %v", err)
	}
	if !json.Valid([]byte(result.Content[0].Text)) || !strings.Contains(result.Content[0].Text, `"count"`) {
		t.Fatalf("content text = %q, want JSON with count", result.Content[0].Text)
	}
}

func TestToolsCallInvalidParamsError(t *testing.T) {
	reg := newTestRegistry()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"reject"}}`)
	resp, _ := reg.Handle(context.Background(), body)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("error = %+v, want code %d", resp.Error, CodeInvalidParams)
	}
	if !strings.Contains(resp.Error.Message, "too many rejections") {
		t.Fatalf("message = %q", resp.Error.Message)
	}
}

// TestToolsCallHandlerErrorIsInBand verifies the error contract: internal
// errors are answered in-band with a generic message (raw details never
// reach the caller), PublicError messages pass through verbatim.
func TestToolsCallHandlerErrorIsInBand(t *testing.T) {
	reg := newTestRegistry()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"broken"}}`)
	resp, _ := reg.Handle(context.Background(), body)
	if resp.Error != nil {
		t.Fatalf("tool handler error must be in-band, got %+v", resp.Error)
	}
	var result CallToolResult
	if err := json.Unmarshal(decode(t, resp.Result), &result); err != nil {
		t.Fatalf("decode tools/call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("result = %+v, want isError", result)
	}
	if text := result.Content[0].Text; text != internalToolErrorText {
		t.Fatalf("content text = %q, want %q", text, internalToolErrorText)
	}
	if strings.Contains(result.Content[0].Text, "internal kaboom") {
		t.Fatal("internal error message leaked to the caller")
	}
}

func TestToolsCallPublicErrorPassesThrough(t *testing.T) {
	reg := NewServer("test-server", "0.1")
	reg.AddTool(ToolDefinition{
		Tool: Tool{
			Name:        "checked",
			Description: "returns a public failure",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return nil, Publicf("no such item %d", 42)
		},
	})
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"checked"}}`)
	resp, _ := reg.Handle(context.Background(), body)
	if resp.Error != nil {
		t.Fatalf("public error must be in-band, got %+v", resp.Error)
	}
	var result CallToolResult
	if err := json.Unmarshal(decode(t, resp.Result), &result); err != nil {
		t.Fatalf("decode tools/call: %v", err)
	}
	if !result.IsError || result.Content[0].Text != "no such item 42" {
		t.Fatalf("result = %+v, want isError with the public message", result)
	}
}

func TestToolsCallUnknownTool(t *testing.T) {
	reg := newTestRegistry()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	resp, _ := reg.Handle(context.Background(), body)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("error = %+v, want code %d", resp.Error, CodeInvalidParams)
	}
}

func TestToolsCallMissingName(t *testing.T) {
	reg := newTestRegistry()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"arguments":{}}}`)
	resp, _ := reg.Handle(context.Background(), body)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("error = %+v, want code %d", resp.Error, CodeInvalidParams)
	}
}
