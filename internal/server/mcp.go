package server

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/i5heu/MentisEterna/internal/config"
	"github.com/i5heu/MentisEterna/internal/mcp"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// mcpServerVersion is the protocol-reported server version.
const mcpServerVersion = "1.0.0"

// mcpMaxListLimit caps the list_todos limit parameter.
const mcpMaxListLimit = 200

var mcpDueDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// newMCPRegistry wires the MCP protocol server with the tool implementations.
func newMCPRegistry(s *Server) *mcp.Server {
	reg := mcp.NewServer("MentisEterna", mcpServerVersion)
	reg.AddTool(mcp.ToolDefinition{
		Tool: mcp.Tool{
			Name:        "list_todos",
			Description: "List task notes from the personal notes app (MentisEterna). Returns open (not done) tasks by default, ordered open-first, then by due date and priority.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"status": {"type": "string", "enum": ["open", "todo", "in_progress", "done", "all"], "description": "Which tasks to return. Default: open (status is todo or in_progress)."},
					"limit": {"type": "integer", "minimum": 1, "maximum": 200, "description": "Maximum tasks to return. Default: 50."}
				},
				"additionalProperties": false
			}`),
		},
		Handler: s.mcpListTodos,
	})
	reg.AddTool(mcp.ToolDefinition{
		Tool: mcp.Tool{
			Name:        "get_todo",
			Description: "Get one task note in full: status, priority, due date, description, note body, and its checklist of subtasks.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"note_id": {"type": "integer", "description": "ID of the task note."}
				},
				"required": ["note_id"],
				"additionalProperties": false
			}`),
		},
		Handler: s.mcpGetTodo,
	})
	reg.AddTool(mcp.ToolDefinition{
		Tool: mcp.Tool{
			Name:        "add_todo",
			Description: "Create a new task (todo) in the personal notes app, with optional due date, priority, difficulty, tags, and a subtask checklist.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Short task title."},
					"description": {"type": "string", "description": "Detailed task description (shown in the task UI)."},
					"body": {"type": "string", "description": "Optional markdown note body for the task."},
					"due_date": {"type": "string", "description": "Due date in YYYY-MM-DD format, or empty."},
					"priority": {"type": "integer", "minimum": 0, "maximum": 10, "description": "0 = low, 10 = critical. Default 0."},
					"difficulty": {"type": "integer", "minimum": 0, "maximum": 10, "description": "0 = trivial, 10 = extremely hard. Default 0."},
					"tags": {"type": "array", "items": {"type": "string"}, "description": "Note tags."},
					"subtasks": {"type": "array", "items": {"type": "object", "properties": {"label": {"type": "string"}, "description": {"type": "string"}}, "required": ["label"], "additionalProperties": false}, "description": "Checklist items."}
				},
				"required": ["title"],
				"additionalProperties": false
			}`),
		},
		Handler: s.mcpAddTodo,
	})
	reg.AddTool(mcp.ToolDefinition{
		Tool: mcp.Tool{
			Name:        "set_todo_status",
			Description: "Change a task's status: mark it done, redo it (todo), or set it in progress.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"note_id": {"type": "integer", "description": "ID of the task note."},
					"status": {"type": "string", "enum": ["todo", "in_progress", "done"]}
				},
				"required": ["note_id", "status"],
				"additionalProperties": false
			}`),
		},
		Handler: s.mcpSetTodoStatus,
	})
	reg.AddTool(mcp.ToolDefinition{
		Tool: mcp.Tool{
			Name:        "toggle_subtask",
			Description: "Check or uncheck a subtask (checklist item) of a task. Subtask IDs come from get_todo.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"subtask_id": {"type": "integer", "description": "ID of the subtask."},
					"checked": {"type": "boolean", "description": "true = checked/done, false = unchecked."}
				},
				"required": ["subtask_id", "checked"],
				"additionalProperties": false
			}`),
		},
		Handler: s.mcpToggleSubtask,
	})
	return reg
}

// handleMCP serves the /mcp endpoint (MCP streamable-http transport, JSON only).
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if config.Get().MCP.Disabled {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		// The server offers no server-initiated streams and no sessions:
		// GET/DELETE for the streamable-http transport are answered with 405.
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Brute-force guard: unauthenticated failures are throttled per client IP
	// with exponential backoff (429 + Retry-After once blocked).
	ipKey := throttleKeyIP(clientIPFromRequest(r))
	if wait, allowed := s.mcpThrottle.allow(ipKey); !allowed {
		w.Header().Set("Retry-After", formatRetryAfter(wait))
		http.Error(w, "too many failed attempts", http.StatusTooManyRequests)
		return
	}
	if !s.mcpAuthorized(r) {
		s.mcpThrottle.recordFailure(ipKey)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mcpThrottle.recordSuccess(ipKey)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxJSONBodyBytes()))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	resp, respond := s.mcpServer.Handle(r.Context(), body)
	if !respond {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// mcpAuthorized authenticates an MCP request. It accepts the static MCP_TOKEN
// (constant-time compare, same pattern as the ingest token) or a regular
// session token via Authorization: Bearer / cookie.
func (s *Server) mcpAuthorized(r *http.Request) bool {
	if s.mcpToken != "" {
		provided := extractBearerToken(r)
		if provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(s.mcpToken)) == 1 {
			return true
		}
	}
	_, _, err := s.sessionUsername(r)
	return err == nil
}

// --- Task tool implementations ---

// mcpTodo is the wire representation of a task note in MCP tool results.
type mcpTodo struct {
	NoteID         int64          `json:"note_id"`
	Title          string         `json:"title"`
	Status         string         `json:"status"`
	Priority       int            `json:"priority"`
	Difficulty     int            `json:"difficulty"`
	Fun            int            `json:"fun"`
	Description    string         `json:"description"`
	Body           string         `json:"body,omitempty"`
	DueDate        string         `json:"due_date"`
	TimeEstimation string         `json:"time_estimation"`
	TimeUsed       string         `json:"time_used"`
	Recurring      string         `json:"recurring"`
	CompletedAt    string         `json:"completed_at"`
	SubtasksDone   int            `json:"subtasks_done"`
	SubtasksTotal  int            `json:"subtasks_total"`
	Subtasks       []mcpSubtask   `json:"subtasks,omitempty"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
	ParentID       int64          `json:"parent_id"`
	Tags           []string       `json:"tags,omitempty"`
}

type mcpSubtask struct {
	ID          int64  `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Checked     bool   `json:"checked"`
}

const mcpTodoSelectSQL = `
	SELECT n.id, n.title, COALESCE(n.parent_id, 0), n.created_at,
	       COALESCE(u.body, ''),
	       COALESCE(u.created_at, n.created_at),
	       COALESCE(tc.status, 'todo'),
	       COALESCE(tc.priority, 0),
	       COALESCE(tc.difficulty, 0),
	       COALESCE(tc.fun, 0),
	       COALESCE(tc.description, ''),
	       COALESCE(tc.due_date, ''),
	       COALESCE(tc.time_estimation, ''),
	       COALESCE(tc.time_used, ''),
	       COALESCE(tc.recurring, 'none'),
	       COALESCE(tc.completed_at, ''),
	       COALESCE(sc.done, 0),
	       COALESCE(sc.total, 0)
	FROM notes n
	LEFT JOIN updates u ON u.id = (
		SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
	)
	LEFT JOIN ct_task_config tc ON tc.note_id = n.id
	LEFT JOIN (
		SELECT note_id, COUNT(*) AS total, COALESCE(SUM(checked), 0) AS done
		FROM ct_task_subtasks GROUP BY note_id
	) sc ON sc.note_id = n.id
	WHERE n.type = 'task'`

const mcpTodoOrderSQL = `
	ORDER BY CASE WHEN COALESCE(tc.status, 'todo') = 'done' THEN 1 ELSE 0 END,
	         CASE WHEN COALESCE(tc.due_date, '') = '' THEN 1 ELSE 0 END,
	         tc.due_date ASC,
	         COALESCE(tc.priority, 0) DESC,
	         n.created_at DESC`

func scanMCPTodo(row interface{ Scan(...any) error }) (mcpTodo, error) {
	var t mcpTodo
	err := row.Scan(&t.NoteID, &t.Title, &t.ParentID, &t.CreatedAt,
		&t.Body, &t.UpdatedAt,
		&t.Status, &t.Priority, &t.Difficulty, &t.Fun,
		&t.Description, &t.DueDate, &t.TimeEstimation, &t.TimeUsed,
		&t.Recurring, &t.CompletedAt,
		&t.SubtasksDone, &t.SubtasksTotal)
	if err != nil {
		return mcpTodo{}, err
	}
	return t, nil
}

// mcpParseArgs decodes tool arguments into dst, mapping bad JSON to an
// InvalidParams error (JSON-RPC -32602).
func mcpParseArgs(args json.RawMessage, dst any) error {
	if err := json.Unmarshal(args, dst); err != nil {
		return mcp.InvalidParamsf("invalid arguments: %v", err)
	}
	return nil
}

func (s *Server) mcpListTodos(ctx context.Context, args json.RawMessage) (any, error) {
	var p struct {
		Status string `json:"status"`
		Limit  int    `json:"limit"`
	}
	if err := mcpParseArgs(args, &p); err != nil {
		return nil, err
	}
	if p.Status == "" {
		p.Status = "open"
	}

	query := mcpTodoSelectSQL
	var queryArgs []any
	switch p.Status {
	case "", "open":
		query += ` AND COALESCE(tc.status, 'todo') IN ('todo', 'in_progress')`
	case "todo", "in_progress", "done":
		query += ` AND COALESCE(tc.status, 'todo') = ?`
		queryArgs = append(queryArgs, p.Status)
	case "all":
	default:
		return nil, mcp.InvalidParamsf("invalid status %q (valid: open, todo, in_progress, done, all)", p.Status)
	}

	limit := 50
	if p.Limit > 0 {
		limit = p.Limit
	}
	if limit > mcpMaxListLimit {
		return nil, mcp.InvalidParamsf("limit must be at most %d", mcpMaxListLimit)
	}
	query += mcpTodoOrderSQL + ` LIMIT ?`
	queryArgs = append(queryArgs, limit)

	rows, err := s.db.Query(query, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	todos := []mcpTodo{}
	for rows.Next() {
		t, err := scanMCPTodo(rows)
		if err != nil {
			return nil, err
		}
		// The note body is omitted from list results; use get_todo for it.
		t.Body = ""
		todos = append(todos, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"todos": todos,
		"count": len(todos),
		"note":  fmt.Sprintf("%d matching task(s). Use get_todo for the subtask checklist of a task.", len(todos)),
	}, nil
}

func (s *Server) mcpGetTodo(ctx context.Context, args json.RawMessage) (any, error) {
	var p struct {
		NoteID int64 `json:"note_id"`
	}
	if err := mcpParseArgs(args, &p); err != nil {
		return nil, err
	}
	if p.NoteID <= 0 {
		return nil, mcp.InvalidParamsf("note_id is required")
	}

	t, err := s.mcpLoadTodo(p.NoteID)
	if err != nil {
		return nil, err
	}
	if err := s.mcpAttachSubtasks(&t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Server) mcpLoadTodo(noteID int64) (mcpTodo, error) {
	t, err := scanMCPTodo(s.db.QueryRow(mcpTodoSelectSQL+` AND n.id = ?`, noteID))
	if errors.Is(err, sql.ErrNoRows) {
		return mcpTodo{}, mcp.InvalidParamsf("task note %d not found", noteID)
	}
	if err != nil {
		return mcpTodo{}, err
	}
	tags, err := loadTags(s.db.DB, t.NoteID)
	if err != nil {
		return mcpTodo{}, err
	}
	if tags != nil {
		t.Tags = tags
	}
	return t, nil
}

func (s *Server) mcpAttachSubtasks(t *mcpTodo) error {
	rows, err := s.db.Query(`SELECT id, label, description, checked FROM ct_task_subtasks WHERE note_id = ? ORDER BY id`, t.NoteID)
	if err != nil {
		return err
	}
	defer rows.Close()

	t.Subtasks = []mcpSubtask{}
	checked := 0
	for rows.Next() {
		var st mcpSubtask
		var flag int
		if err := rows.Scan(&st.ID, &st.Label, &st.Description, &flag); err != nil {
			return err
		}
		st.Checked = flag != 0
		if st.Checked {
			checked++
		}
		t.Subtasks = append(t.Subtasks, st)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	t.SubtasksDone = checked
	t.SubtasksTotal = len(t.Subtasks)
	return nil
}

func (s *Server) mcpAddTodo(ctx context.Context, args json.RawMessage) (any, error) {
	var p struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Body        string `json:"body"`
		DueDate     string `json:"due_date"`
		Priority    *int   `json:"priority"`
		Difficulty  *int   `json:"difficulty"`
		Tags        []string
		Subtasks    []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"subtasks"`
	}
	if err := mcpParseArgs(args, &p); err != nil {
		return nil, err
	}
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" {
		return nil, mcp.InvalidParamsf("title is required")
	}
	p.DueDate = strings.TrimSpace(p.DueDate)
	if p.DueDate != "" && !mcpDueDatePattern.MatchString(p.DueDate) {
		return nil, mcp.InvalidParamsf("due_date must be YYYY-MM-DD, got %q", p.DueDate)
	}
	priority, difficulty := 0, 0
	if p.Priority != nil {
		priority = *p.Priority
	}
	if p.Difficulty != nil {
		difficulty = *p.Difficulty
	}
	if priority < 0 || priority > 10 {
		return nil, mcp.InvalidParamsf("priority must be 0-10")
	}
	if difficulty < 0 || difficulty > 10 {
		return nil, mcp.InvalidParamsf("difficulty must be 0-10")
	}

	subtasks := []map[string]any{}
	for _, st := range p.Subtasks {
		label := strings.TrimSpace(st.Label)
		if label == "" {
			continue
		}
		subtasks = append(subtasks, map[string]any{
			"label":       label,
			"description": strings.TrimSpace(st.Description),
			"checked":     false,
		})
	}

	customData, err := json.Marshal(map[string]any{
		"status":                               "todo",
		"difficulty":                           difficulty,
		"fun":                                  0,
		"priority":                             priority,
		"description":                          strings.TrimSpace(p.Description),
		"due_date":                             p.DueDate,
		"time_estimation":                      "",
		"time_used":                            "",
		"recurring":                            "none",
		"recurring_days":                       0,
		"completed_at":                         "",
		"pending_does_not_force_daily_inclusion": false,
		"subtasks":                             subtasks,
	})
	if err != nil {
		return nil, err
	}

	n, err := s.createNoteCore(createNoteInput{
		Title:      p.Title,
		Body:       p.Body,
		Type:       "task",
		CustomData: customData,
		Tags:       p.Tags,
	})
	if err != nil {
		var badReq *notetype.BadRequestError
		if errors.As(err, &badReq) {
			return nil, mcp.InvalidParamsf("%s", badReq.Error())
		}
		return nil, err
	}

	t, err := s.mcpLoadTodo(n.ID)
	if err != nil {
		return nil, err
	}
	if err := s.mcpAttachSubtasks(&t); err != nil {
		return nil, err
	}
	return map[string]any{
		"created": true,
		"task":    t,
	}, nil
}

func (s *Server) mcpSetTodoStatus(ctx context.Context, args json.RawMessage) (any, error) {
	var p struct {
		NoteID int64  `json:"note_id"`
		Status string `json:"status"`
	}
	if err := mcpParseArgs(args, &p); err != nil {
		return nil, err
	}
	if p.NoteID <= 0 {
		return nil, mcp.InvalidParamsf("note_id is required")
	}
	switch p.Status {
	case "todo", "in_progress", "done":
	default:
		return nil, mcp.InvalidParamsf("invalid status %q (valid: todo, in_progress, done)", p.Status)
	}
	actionParams, err := json.Marshal(map[string]any{"status": p.Status})
	if err != nil {
		return nil, err
	}
	return s.mcpTaskAction(ctx, p.NoteID, "set_status", actionParams)
}

func (s *Server) mcpToggleSubtask(ctx context.Context, args json.RawMessage) (any, error) {
	var p struct {
		SubtaskID int64 `json:"subtask_id"`
		Checked   bool  `json:"checked"`
	}
	if err := mcpParseArgs(args, &p); err != nil {
		return nil, err
	}
	if p.SubtaskID <= 0 {
		return nil, mcp.InvalidParamsf("subtask_id is required")
	}

	var noteID int64
	err := s.db.QueryRow(`SELECT note_id FROM ct_task_subtasks WHERE id = ?`, p.SubtaskID).Scan(&noteID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, mcp.InvalidParamsf("subtask %d not found", p.SubtaskID)
	}
	if err != nil {
		return nil, err
	}

	actionParams, err := json.Marshal(map[string]any{"subtask_id": p.SubtaskID, "checked": p.Checked})
	if err != nil {
		return nil, err
	}
	return s.mcpTaskAction(ctx, noteID, "toggle_subtask", actionParams)
}

// mcpTaskAction runs a task plugin action on behalf of an MCP client and
// mirrors the HTTP action dispatcher's post-processing (live notification).
func (s *Server) mcpTaskAction(ctx context.Context, noteID int64, actionID string, params json.RawMessage) (any, error) {
	var noteType string
	err := s.db.QueryRow(`SELECT type FROM notes WHERE id = ?`, noteID).Scan(&noteType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, mcp.InvalidParamsf("note %d not found", noteID)
	}
	if err != nil {
		return nil, err
	}
	if noteType != "task" {
		return nil, mcp.InvalidParamsf("note %d is of type %q, not task", noteID, noteType)
	}

	plugin, ok := notetype.Registry[noteType]
	if !ok {
		return nil, mcp.Publicf("task actions are not available")
	}
	ah, ok := plugin.(notetype.ActionHandler)
	if !ok {
		return nil, mcp.Publicf("task actions are not available")
	}

	result, err := ah.HandleAction(ctx, s.db.DB, 0, noteID, actionID, params)
	if err != nil {
		// The plugin's BadRequestErrors are deliberate, caller-safe messages
		// (validation failures, not-found cases). Everything else — e.g.
		// wrapped database errors — stays internal and is answered with the
		// generic in-band failure by the protocol core.
		var badReq *notetype.BadRequestError
		if errors.As(err, &badReq) {
			return nil, mcp.Publicf("%s", badReq.Error())
		}
		return nil, err
	}
	if shouldNotifyForAction(plugin, actionID) {
		s.notifyNotesChanged("plugin_action", actionRelatedNoteIDs(noteID, result)...)
	}
	return result, nil
}
