// Package home implements a "home" note type that serves as the user's
// landing page showing latest notes, stats, and a "Mind Dump" section
// for quick note-taking.
package home

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

const pluginID = "home"

func init() {
	notetype.Register(&HomePlugin{})
}

type HomePlugin struct{}

func (p *HomePlugin) ID() string { return pluginID }

func (p *HomePlugin) InitSchema(db *sql.DB) error {
	// No dedicated tables needed — this is a read-only dashboard.
	return nil
}

// RecentNote is a lightweight note reference.
type RecentNote struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// HomeStats holds aggregate statistics for the home page.
type HomeStats struct {
	TotalNotes      int `json:"total_notes"`
	TotalUpdates    int `json:"total_updates"`
	TasksTodo       int `json:"tasks_todo"`
	TasksInProgress int `json:"tasks_in_progress"`
	TasksDone       int `json:"tasks_done"`
	NotesLast7Days  int `json:"notes_last_7_days"`
	NotesLast30Days int `json:"notes_last_30_days"`
}

// HomeData is the view returned to the frontend.
type HomeData struct {
	RecentNotes []RecentNote `json:"recent_notes"`
	Stats       HomeStats    `json:"stats"`
	MindDump    string       `json:"mind_dump"` // always empty, frontend handles this
}

// MindDumpParams is the JSON body for the mind_dump action.
type MindDumpParams struct {
	Body string   `json:"body"`
	Tags []string `json:"tags"`
}

func (p *HomePlugin) CronJobs() []notetype.CronJob {
	return nil
}

func (p *HomePlugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:            "home",
		Label:         "Home",
		Description:   "Landing page with latest notes, stats, and quick mind dump",
		Category:      "Navigation",
		SortOrder:     10,
		DefaultConfig: json.RawMessage(`{}`),
		Editor:        notetype.EditorMeta{Mode: "custom"},
		Viewer:        notetype.ViewerMeta{Mode: "custom"},
		Actions: []notetype.ActionMeta{
			{
				ID:              "mind_dump",
				Label:           "Mind Dump",
				Description:     "Quickly create a note from the mind dump section",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"body":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}}}}`),
				Dangerous:       false,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Mind dump saved as new note",
			},
		},
		HasConfig:  false,
		HasView:    true,
		HasActions: true,
	}
}

func (p *HomePlugin) BuildView(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	recentNotes, err := loadRecentNotes(db, 10)
	if err != nil {
		return nil, err
	}

	stats, err := computeHomeStats(db)
	if err != nil {
		return nil, err
	}

	return &HomeData{
		RecentNotes: recentNotes,
		Stats:       stats,
		MindDump:    "",
	}, nil
}

func (p *HomePlugin) HandleAction(ctx context.Context, db *sql.DB, userID int, noteID int64, actionID string, params json.RawMessage) (any, error) {
	// The "mind_dump" action is handled by the frontend (POST /notes with body + tags).
	// This plugin is view-only with no backend actions.
	return nil, fmt.Errorf("%w: %s", notetype.ErrUnknownAction, actionID)
}

// --- Helpers ---

func loadRecentNotes(db *sql.DB, limit int) ([]RecentNote, error) {
	rows, err := db.Query(`
		SELECT n.id, n.title, n.type, n.created_at,
		       COALESCE(u.body, '') AS body,
		       COALESCE(u.created_at, n.created_at) AS updated_at
		FROM notes n
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		ORDER BY COALESCE(u.created_at, n.created_at) DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("home: load recent notes: %w", err)
	}
	defer rows.Close()

	var notes []RecentNote
	for rows.Next() {
		var n RecentNote
		if err := rows.Scan(&n.ID, &n.Title, &n.Type, &n.CreatedAt, &n.Body, &n.UpdatedAt); err != nil {
			return nil, fmt.Errorf("home: scan recent note: %w", err)
		}
		notes = append(notes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if notes == nil {
		notes = []RecentNote{}
	}
	return notes, nil
}

func computeHomeStats(db *sql.DB) (HomeStats, error) {
	var s HomeStats

	// Total notes
	if err := db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&s.TotalNotes); err != nil {
		return s, err
	}

	// Total updates
	if err := db.QueryRow(`SELECT COUNT(*) FROM updates`).Scan(&s.TotalUpdates); err != nil {
		return s, err
	}

	// Task counts (from ct_task_config)
	db.QueryRow(`SELECT COUNT(*) FROM ct_task_config WHERE status = 'todo'`).Scan(&s.TasksTodo)
	db.QueryRow(`SELECT COUNT(*) FROM ct_task_config WHERE status = 'in_progress'`).Scan(&s.TasksInProgress)
	db.QueryRow(`SELECT COUNT(*) FROM ct_task_config WHERE status = 'done'`).Scan(&s.TasksDone)

	// Notes in last 7 / 30 days
	now := time.Now()
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour).Format("2006-01-02")
	thirtyDaysAgo := now.Add(-30 * 24 * time.Hour).Format("2006-01-02")

	db.QueryRow(`SELECT COUNT(*) FROM notes WHERE created_at >= ?`, sevenDaysAgo).Scan(&s.NotesLast7Days)
	db.QueryRow(`SELECT COUNT(*) FROM notes WHERE created_at >= ?`, thirtyDaysAgo).Scan(&s.NotesLast30Days)

	return s, nil
}
