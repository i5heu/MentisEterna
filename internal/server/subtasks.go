package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/i5heu/MentisEterna/internal/llm"
)

// generateSubTasksTask is the job task handler for LLM-planned task subtasks.
// Payload: {"note_id": N}. Generated subtasks are APPENDED to any existing
// ones; nothing existing is deleted.
func (s *Server) generateSubTasksTask(db *sql.DB, payload []byte) (string, error) {
	if s.subtaskGen == nil {
		return "", fmt.Errorf("generate_subtasks: no subtask generator configured")
	}
	var p struct {
		NoteID int64 `json:"note_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", fmt.Errorf("generate_subtasks: invalid payload: %w", err)
	}
	if p.NoteID <= 0 {
		return "", fmt.Errorf("generate_subtasks: missing note_id")
	}

	added, err := s.generateAndPersistSubTasks(context.Background(), db, p.NoteID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Sprintf("Skipped subtask generation for missing note %d", p.NoteID), nil
	}
	if err != nil {
		return "", err
	}
	s.notifyNotesChanged("subtasks_generated", p.NoteID)
	return fmt.Sprintf("Generated %d subtasks for note %d", added, p.NoteID), nil
}

// generateAndPersistSubTasks runs the subtask generator on the task context
// (title + latest body + task description + existing subtask labels) and
// appends non-duplicate generated rows to ct_task_subtasks. Returns the
// number of rows added.
func (s *Server) generateAndPersistSubTasks(ctx context.Context, db *sql.DB, noteID int64) (int, error) {
	var title, body string
	err := db.QueryRow(`
		SELECT n.title, COALESCE(u.body, '') AS body
		FROM notes n
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		WHERE n.id = ?
	`, noteID).Scan(&title, &body)
	if err != nil {
		return 0, err
	}

	var description string
	err = db.QueryRow(`SELECT COALESCE(description, '') FROM ct_task_config WHERE note_id = ?`, noteID).Scan(&description)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("generate_subtasks: load task config: %w", err)
	}

	// Existing subtask labels, normalized for duplicate detection.
	rows, err := db.Query(`SELECT label FROM ct_task_subtasks WHERE note_id = ?`, noteID)
	if err != nil {
		return 0, fmt.Errorf("generate_subtasks: load existing subtasks: %w", err)
	}
	defer rows.Close()
	existing := map[string]bool{}
	existingOrder := []string{}
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			return 0, fmt.Errorf("generate_subtasks: scan subtask: %w", err)
		}
		key := strings.ToLower(strings.TrimSpace(label))
		if key != "" && !existing[key] {
			existing[key] = true
			existingOrder = append(existingOrder, key)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("generate_subtasks: subtask rows: %w", err)
	}

	release := llm.BeginBackendUse(s.subtaskGen)
	defer release()
	suggestion, err := s.subtaskGen.GenerateSubTasks(llm.SubTaskGenerationInput{
		Title:            title,
		Description:      description,
		Body:             body,
		ExistingSubtasks: existingOrder,
		MaxSubtasks:      10,
	})
	if err != nil {
		return 0, fmt.Errorf("generate_subtasks: generate: %w", err)
	}

	added := 0
	for _, item := range suggestion.Subtasks {
		if added >= 20 {
			break
		}
		label := strings.TrimSpace(item.Label)
		if label == "" {
			continue
		}
		key := strings.ToLower(label)
		if existing[key] {
			continue
		}
		existing[key] = true
		if _, err := db.Exec(
			`INSERT INTO ct_task_subtasks (note_id, label, checked, description) VALUES (?, ?, 0, ?)`,
			noteID, label, strings.TrimSpace(item.Description),
		); err != nil {
			return added, fmt.Errorf("generate_subtasks: insert subtask: %w", err)
		}
		added++
	}
	return added, nil
}
