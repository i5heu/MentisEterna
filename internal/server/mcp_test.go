package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "github.com/i5heu/MentisEterna/pkg/notetype/builtins"

	"github.com/i5heu/MentisEterna/internal/config"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// newMCPTestServer returns a test server with the task plugin schema
// initialized and a valid session token.
func newMCPTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	config.Reset()
	t.Cleanup(config.Reset)
	s := newTestServer(t)
	if err := notetype.Registry["task"].InitSchema(s.db.DB); err != nil {
		t.Fatalf("init task schema: %v", err)
	}
	return s, createTestSession(t, s)
}

// mcpPerform posts a single JSON-RPC request to /mcp with a Bearer token.
func mcpPerform(t *testing.T, s *Server, auth, method, params string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q`, method)
	if params != "" {
		body += `,"params":` + params
	}
	body += `}`
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	w := httptest.NewRecorder()
	s.handleMCP(w, r)
	return w
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpRPCEnvelope struct {
	Result json.RawMessage `json:"result"`
	Error  *mcpRPCError    `json:"error"`
}

// mcpToolCall invokes a tool and returns its text content, whether the tool
// reported isError, and any protocol-level error.
func mcpToolCall(t *testing.T, s *Server, auth, tool, args string) (text string, isToolError bool, rpcErr *mcpRPCError) {
	t.Helper()
	w := mcpPerform(t, s, auth, "tools/call", fmt.Sprintf(`{"name":%q,"arguments":%s}`, tool, args))
	if w.Code != http.StatusOK {
		t.Fatalf("tools/call %s: HTTP %d: %s", tool, w.Code, w.Body.String())
	}
	var env mcpRPCEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("tools/call %s: decode response: %v", tool, err)
	}
	if env.Error != nil {
		return "", false, env.Error
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("tools/call %s: decode tool result: %v", tool, err)
	}
	if len(result.Content) == 0 {
		t.Fatalf("tools/call %s: no content block", tool)
	}
	return result.Content[0].Text, result.IsError, nil
}

// helperAddTodo creates one task via the add_todo tool and returns its note ID.
func helperAddTodo(t *testing.T, s *Server, auth string) int64 {
	t.Helper()
	text, isErr, rpcErr := mcpToolCall(t, s, auth, "add_todo", `{"title":"Helper task"}`)
	if rpcErr != nil || isErr {
		t.Fatalf("add_todo helper failed: rpc=%+v isErr=%v text=%s", rpcErr, isErr, text)
	}
	var parsed struct {
		Task struct {
			NoteID int64 `json:"note_id"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("decode add_todo result %q: %v", text, err)
	}
	return parsed.Task.NoteID
}

func mapBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	return m
}

func TestMCPRequiresAuth(t *testing.T) {
	s, token := newMCPTestServer(t)

	if w := mcpPerform(t, s, "", "initialize", `{}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: HTTP %d, want 401", w.Code)
	}
	if w := mcpPerform(t, s, "bogus-token", "initialize", `{}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("bogus token: HTTP %d, want 401", w.Code)
	}
	if w := mcpPerform(t, s, token, "initialize", `{}`); w.Code != http.StatusOK {
		t.Fatalf("session token: HTTP %d, want 200", w.Code)
	}
}

func TestMCPStaticTokenAuth(t *testing.T) {
	t.Setenv("MCP_TOKEN", "static-mcp-secret")
	s, sessionToken := newMCPTestServer(t)

	if w := mcpPerform(t, s, "static-mcp-secret", "ping", `{}`); w.Code != http.StatusOK {
		t.Fatalf("static token: HTTP %d, want 200", w.Code)
	}
	if w := mcpPerform(t, s, "not-the-token", "ping", `{}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong static token: HTTP %d, want 401", w.Code)
	}
	if w := mcpPerform(t, s, sessionToken, "ping", `{}`); w.Code != http.StatusOK {
		t.Fatalf("session still accepted: HTTP %d, want 200", w.Code)
	}
}

func TestMCPHandshake(t *testing.T) {
	s, token := newMCPTestServer(t)
	w := mcpPerform(t, s, token, "initialize", `{"protocolVersion":"2025-06-18"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("initialize: HTTP %d: %s", w.Code, w.Body.String())
	}
	body := mapBody(t, w)
	if id, ok := body["id"].(float64); !ok || id != 1 {
		t.Fatalf("id = %v, want 1", body["id"])
	}
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("result missing: %v", body)
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != "MentisEterna" {
		t.Fatalf("serverInfo = %v", info)
	}
	if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatalf("tools capability not advertised: %v", result)
	}
}

func TestMCPToolsList(t *testing.T) {
	s, token := newMCPTestServer(t)

	w := mcpPerform(t, s, token, "tools/list", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("tools/list: HTTP %d: %s", w.Code, w.Body.String())
	}
	var env struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	found := map[string]bool{}
	for _, tool := range env.Result.Tools {
		found[tool.Name] = true
		if !json.Valid(tool.InputSchema) {
			t.Errorf("tool %q has invalid input schema", tool.Name)
		}
	}
	for _, want := range []string{"list_todos", "get_todo", "add_todo", "set_todo_status", "toggle_subtask"} {
		if !found[want] {
			t.Errorf("tool %q missing from tools/list", want)
		}
	}
}

func TestMCPNotificationAcceptedGetMethodRejected(t *testing.T) {
	s, token := newMCPTestServer(t)

	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.handleMCP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("notification: HTTP %d, want 202", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("notification body = %q, want empty", w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	s.handleMCP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: HTTP %d, want 405", w.Code)
	}
}

func TestMCPProtocolErrors(t *testing.T) {
	s, token := newMCPTestServer(t)

	// Invalid JSON body.
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`this is not json`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.handleMCP(w, r)
	body := mapBody(t, w)
	errObj, _ := body["error"].(map[string]any)
	if code, _ := errObj["code"].(float64); code != -32700 {
		t.Fatalf("parse error code = %v, want -32700", errObj)
	}

	// Unknown method.
	w = mcpPerform(t, s, token, "no/such/method", `{}`)
	body = mapBody(t, w)
	errObj, _ = body["error"].(map[string]any)
	if code, _ := errObj["code"].(float64); code != -32601 {
		t.Fatalf("method-not-found code = %v, want -32601", errObj)
	}
}

func TestMCPEndpointDisabled(t *testing.T) {
	s, token := newMCPTestServer(t)
	config.Get().MCP.Disabled = true

	if w := mcpPerform(t, s, token, "ping", `{}`); w.Code != http.StatusNotFound {
		t.Fatalf("disabled endpoint: HTTP %d, want 404", w.Code)
	}
}

func TestMCPAddTodoListAndGetFlow(t *testing.T) {
	s, token := newMCPTestServer(t)

	addArgs := `{
		"title": "Ship MCP endpoint",
		"description": "Wire the /mcp endpoint",
		"body": "Some markdown context",
		"due_date": "2030-01-02",
		"priority": 7,
		"difficulty": 3,
		"subtasks": [
			{"label": "Write tests"},
			{"label": "Docs"}
		]
	}`
	text, isErr, rpcErr := mcpToolCall(t, s, token, "add_todo", addArgs)
	if rpcErr != nil {
		t.Fatalf("add_todo rpc error: %+v", rpcErr)
	}
	if isErr {
		t.Fatalf("add_todo tool error: %s", text)
	}
	var created struct {
		Created bool `json:"created"`
		Task    struct {
			NoteID        int64  `json:"note_id"`
			Title         string `json:"title"`
			Status        string `json:"status"`
			Priority      int    `json:"priority"`
			DueDate       string `json:"due_date"`
			Description   string `json:"description"`
			SubtasksTotal int    `json:"subtasks_total"`
			Subtasks      []struct {
				ID    int64  `json:"id"`
				Label string `json:"label"`
			} `json:"subtasks"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(text), &created); err != nil {
		t.Fatalf("decode add_todo result %q: %v", text, err)
	}
	if !created.Created || created.Task.Title != "Ship MCP endpoint" || created.Task.Status != "todo" {
		t.Fatalf("add_todo result = %+v", created)
	}
	if created.Task.Priority != 7 || created.Task.DueDate != "2030-01-02" || len(created.Task.Subtasks) != 2 {
		t.Fatalf("add_todo task = %+v", created.Task)
	}
	noteID := created.Task.NoteID

	// The note persisted as a task with config, body, and subtasks.
	var noteType, title string
	if err := s.db.QueryRow(`SELECT type, title FROM notes WHERE id = ?`, noteID).Scan(&noteType, &title); err != nil {
		t.Fatalf("load note: %v", err)
	}
	if noteType != "task" || title != "Ship MCP endpoint" {
		t.Fatalf("note = %s/%s", noteType, title)
	}
	var status string
	var priority int
	if err := s.db.QueryRow(`SELECT status, priority FROM ct_task_config WHERE note_id = ?`, noteID).Scan(&status, &priority); err != nil {
		t.Fatalf("load task config: %v", err)
	}
	if status != "todo" || priority != 7 {
		t.Fatalf("task config = %s/%d", status, priority)
	}
	var subtaskCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ct_task_subtasks WHERE note_id = ?`, noteID).Scan(&subtaskCount); err != nil {
		t.Fatalf("count subtasks: %v", err)
	}
	if subtaskCount != 2 {
		t.Fatalf("subtask rows = %d, want 2", subtaskCount)
	}

	// Add a second, minimal task.
	_, isErr, rpcErr = mcpToolCall(t, s, token, "add_todo", `{"title":"Second task"}`)
	if rpcErr != nil || isErr {
		t.Fatalf("second add_todo: rpc=%+v isErr=%v", rpcErr, isErr)
	}

	// list_todos (default: open) returns both, no note body included.
	text, isErr, rpcErr = mcpToolCall(t, s, token, "list_todos", `{}`)
	if rpcErr != nil || isErr {
		t.Fatalf("list_todos: rpc=%+v isErr=%v text=%s", rpcErr, isErr, text)
	}
	var list struct {
		Count int `json:"count"`
		Todos []struct {
			NoteID int64  `json:"note_id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"todos"`
	}
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		t.Fatalf("decode list_todos %q: %v", text, err)
	}
	if list.Count != 2 || len(list.Todos) != 2 {
		t.Fatalf("list = %+v", list)
	}
	if strings.Contains(text, "Some markdown context") {
		t.Fatalf("list_todos must not include note bodies: %s", text)
	}

	// get_todo returns body and subtask checklist.
	text, isErr, rpcErr = mcpToolCall(t, s, token, "get_todo", fmt.Sprintf(`{"note_id":%d}`, noteID))
	if rpcErr != nil || isErr {
		t.Fatalf("get_todo: rpc=%+v isErr=%v text=%s", rpcErr, isErr, text)
	}
	var detail struct {
		NoteID      int64  `json:"note_id"`
		Body        string `json:"body"`
		Description string `json:"description"`
		Subtasks    []struct {
			ID      int64  `json:"id"`
			Label   string `json:"label"`
			Checked bool   `json:"checked"`
		} `json:"subtasks"`
	}
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("decode get_todo %q: %v", text, err)
	}
	if detail.NoteID != noteID || detail.Body != "Some markdown context" {
		t.Fatalf("get_todo detail = %+v", detail)
	}
	if len(detail.Subtasks) != 2 || detail.Subtasks[0].Label != "Write tests" {
		t.Fatalf("get_todo subtasks = %+v", detail.Subtasks)
	}
}

func TestMCPAddTodoValidation(t *testing.T) {
	s, token := newMCPTestServer(t)

	cases := []struct {
		name string
		args string
	}{
		{"missing title", `{"description":"no title"}`},
		{"bad due date", `{"title":"T","due_date":"spring"}`},
		{"priority out of range", `{"title":"T","priority":11}`},
		{"difficulty out of range", `{"title":"T","difficulty":99}`},
	}
	for _, tc := range cases {
		_, isErr, rpcErr := mcpToolCall(t, s, token, "add_todo", tc.args)
		// Validation failures must surface an error, never create a task.
		if rpcErr == nil && !isErr {
			t.Fatalf("%s: expected an error, got none", tc.name)
		}
		if rpcErr != nil && rpcErr.Code != -32602 {
			t.Fatalf("%s: protocol error code = %d, want -32602 (%s)", tc.name, rpcErr.Code, rpcErr.Message)
		}
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM notes WHERE type = 'task'`).Scan(&count); err != nil {
			t.Fatalf("count tasks: %v", err)
		}
		if count != 0 {
			t.Fatalf("%s: task was created despite error", tc.name)
		}
	}

	// A bogus list_todos status is a protocol error too.
	_, _, rpcErr := mcpToolCall(t, s, token, "list_todos", `{"status":"bogus"}`)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("list_todos bogus status: rpc=%+v, want -32602", rpcErr)
	}
}

func TestMCPSetTodoStatusFlow(t *testing.T) {
	s, token := newMCPTestServer(t)
	noteID := helperAddTodo(t, s, token)

	text, isErr, rpcErr := mcpToolCall(t, s, token, "set_todo_status", fmt.Sprintf(`{"note_id":%d,"status":"done"}`, noteID))
	if rpcErr != nil || isErr {
		t.Fatalf("set_todo_status done: rpc=%+v isErr=%v text=%s", rpcErr, isErr, text)
	}

	var status, completedAt string
	if err := s.db.QueryRow(`SELECT status, COALESCE(completed_at,'') FROM ct_task_config WHERE note_id = ?`, noteID).Scan(&status, &completedAt); err != nil {
		t.Fatalf("load config: %v", err)
	}
	if status != "done" || completedAt == "" {
		t.Fatalf("status = %q completed_at = %q, want done + timestamp", status, completedAt)
	}

	// Reopen: status back to todo (completed_at is left as-is, matching the
	// plugin's own set_status behavior).
	_, isErr, rpcErr = mcpToolCall(t, s, token, "set_todo_status", fmt.Sprintf(`{"note_id":%d,"status":"todo"}`, noteID))
	if rpcErr != nil || isErr {
		t.Fatalf("set_todo_status todo: rpc=%+v isErr=%v", rpcErr, isErr)
	}
	if err := s.db.QueryRow(`SELECT status FROM ct_task_config WHERE note_id = ?`, noteID).Scan(&status); err != nil {
		t.Fatalf("load status: %v", err)
	}
	if status != "todo" {
		t.Fatalf("status = %q, want todo", status)
	}

	// No done tasks yet.
	listJSON, _, _ := mcpToolCall(t, s, token, "list_todos", `{"status":"done"}`)
	var doneList struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(listJSON), &doneList); err != nil {
		t.Fatalf("decode done list %q: %v", listJSON, err)
	}
	if doneList.Count != 0 {
		t.Fatalf("done list count = %d, want 0", doneList.Count)
	}

	// Invalid status is rejected up front.
	_, _, rpcErr = mcpToolCall(t, s, token, "set_todo_status", fmt.Sprintf(`{"note_id":%d,"status":"weird"}`, noteID))
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("invalid status: rpc=%+v, want -32602", rpcErr)
	}

	// Unknown note is rejected.
	_, _, rpcErr = mcpToolCall(t, s, token, "set_todo_status", `{"note_id":999999,"status":"done"}`)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("unknown note: rpc=%+v, want -32602", rpcErr)
	}
}

func TestMCPToggleSubtaskFlow(t *testing.T) {
	s, token := newMCPTestServer(t)

	addArgs := `{"title":"Task with checklist","subtasks":[{"label":"First"},{"label":"Second"}]}`
	text, isErr, rpcErr := mcpToolCall(t, s, token, "add_todo", addArgs)
	if rpcErr != nil || isErr {
		t.Fatalf("add_todo: rpc=%+v isErr=%v text=%s", rpcErr, isErr, text)
	}
	var created struct {
		Task struct {
			NoteID   int64 `json:"note_id"`
			Subtasks []struct {
				ID    int64  `json:"id"`
				Label string `json:"label"`
			} `json:"subtasks"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(text), &created); err != nil {
		t.Fatalf("decode add_todo: %v", err)
	}
	if len(created.Task.Subtasks) != 2 {
		t.Fatalf("subtasks = %+v", created.Task.Subtasks)
	}
	noteID, subtaskID := created.Task.NoteID, created.Task.Subtasks[0].ID

	// Toggle on.
	_, isErr, rpcErr = mcpToolCall(t, s, token, "toggle_subtask", fmt.Sprintf(`{"subtask_id":%d,"checked":true}`, subtaskID))
	if rpcErr != nil || isErr {
		t.Fatalf("toggle_subtask: rpc=%+v isErr=%v", rpcErr, isErr)
	}

	// Verify via get_todo: first subtask checked, done count 1.
	text, isErr, rpcErr = mcpToolCall(t, s, token, "get_todo", fmt.Sprintf(`{"note_id":%d}`, noteID))
	if rpcErr != nil || isErr {
		t.Fatalf("get_todo: rpc=%+v isErr=%v", rpcErr, isErr)
	}
	var detail struct {
		SubtasksDone int `json:"subtasks_done"`
		Subtasks     []struct {
			ID      int64 `json:"id"`
			Checked bool  `json:"checked"`
		} `json:"subtasks"`
	}
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("decode get_todo: %v", err)
	}
	if detail.SubtasksDone != 1 || len(detail.Subtasks) != 2 || !detail.Subtasks[0].Checked {
		t.Fatalf("detail = %+v", detail)
	}

	// Toggle back off.
	var checked int
	_, isErr, rpcErr = mcpToolCall(t, s, token, "toggle_subtask", fmt.Sprintf(`{"subtask_id":%d,"checked":false}`, subtaskID))
	if rpcErr != nil || isErr {
		t.Fatalf("toggle off: rpc=%+v isErr=%v", rpcErr, isErr)
	}
	if err := s.db.QueryRow(`SELECT checked FROM ct_task_subtasks WHERE id = ?`, subtaskID).Scan(&checked); err != nil {
		t.Fatalf("load subtask: %v", err)
	}
	if checked != 0 {
		t.Fatalf("checked = %d, want 0", checked)
	}

	// Unknown subtask is rejected.
	_, _, rpcErr = mcpToolCall(t, s, token, "toggle_subtask", `{"subtask_id":424242,"checked":true}`)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("unknown subtask: rpc=%+v, want -32602", rpcErr)
	}
}

func TestMCPListTodosOnlyTasks(t *testing.T) {
	s, token := newMCPTestServer(t)
	helperAddTodo(t, s, token)

	// A standard note must never appear in list_todos.
	if _, err := s.db.Exec(`INSERT INTO notes (title, parent_id, type) VALUES ('Plain note', NULL, 'standard')`); err != nil {
		t.Fatalf("insert standard note: %v", err)
	}

	text, _, _ := mcpToolCall(t, s, token, "list_todos", `{"status":"all"}`)
	if strings.Contains(text, "Plain note") {
		t.Fatalf("standard note leaked into list_todos: %s", text)
	}
	if !strings.Contains(text, "Helper task") {
		t.Fatalf("task missing from list_todos: %s", text)
	}
}

func TestMCPGetTodoNotFound(t *testing.T) {
	s, token := newMCPTestServer(t)
	_, _, rpcErr := mcpToolCall(t, s, token, "get_todo", `{"note_id":987654}`)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("get_todo unknown note: rpc=%+v, want -32602", rpcErr)
	}
}
