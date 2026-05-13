// Package notetype defines the plugin interface for custom note types.
// Each note type (recipe, task list, collection, etc.) implements NoteType
// and registers itself via Register() so the server can route requests to it.
package notetype

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// CronJob describes a background task registered by a plugin.
// Schedule is a standard cron expression (e.g. "@daily", "0 8 * * *").
// Task receives the *sql.DB and an optional payload (nil for cron-triggered runs).
// It returns a human-readable result summary and an error.
type CronJob struct {
	Name     string
	Schedule string
	Task     func(db *sql.DB, payload []byte) (result string, err error)
}

// NoteType is the interface all custom note plugins must implement.
//
// Lifecycle:
//  1. InitSchema is called once at server startup to create plugin tables.
//  2. Validate is called before save to check the incoming payload.
//  3. ProcessSave is called within an active SQL transaction to persist plugin data.
//  4. ProcessLoad is called when loading a note to retrieve plugin-specific data.
//  5. UISchema provides a FormKit-compatible JSON schema for the frontend.
//  6. CronJobs returns optional background tasks (e.g., generating grocery lists).
type NoteType interface {
	// ID returns a short, unique identifier for this plugin (e.g. "recipe").
	ID() string

	// InitSchema creates plugin-specific tables in the database.
	// Table names MUST be prefixed: ct_{pluginID}_
	// This is called once at server startup for every registered plugin.
	InitSchema(db *sql.DB) error

	// Validate checks the custom payload before saving.
	// Returns nil if the payload is valid, or an error describing the problem.
	Validate(payload json.RawMessage) error

	// ProcessSave persists the plugin-specific data for a note.
	// It runs inside an active SQL transaction (tx) along with the core note insert/update.
	// The noteID is guaranteed to exist in the notes table at this point.
	ProcessSave(ctx context.Context, tx *sql.Tx, userID int, noteID int64, payload json.RawMessage) error

	// ProcessLoad retrieves the plugin-specific data for a note.
	// Returns nil, nil if no custom data exists for this note.
	ProcessLoad(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error)

	// UISchema returns a JSON schema that the frontend uses to render
	// a form for this note type. The schema should be FormKit-compatible:
	// https://formkit.com/essentials/schema
	UISchema() json.RawMessage

	// CronJobs returns any background cron jobs this plugin needs.
	// Jobs are registered by the server and run on a shared scheduler.
	CronJobs() []CronJob
}

// Registry holds all active plugins, keyed by their ID().
var Registry = make(map[string]NoteType)

// Register adds a plugin to the global registry.
// Plugins typically call this from their init() function.
func Register(nt NoteType) {
	Registry[nt.ID()] = nt
}

// --- New explicit config/view/action interfaces ---

// EditorMeta describes the editing experience for a note type.
type EditorMeta struct {
	Mode   string          `json:"mode"`
	Schema json.RawMessage `json:"schema,omitempty"`
}

// ViewerMeta describes the read-only display for a note type.
type ViewerMeta struct {
	Mode string `json:"mode"`
}

// ActionMeta describes a single plugin action (RPC endpoint).
type ActionMeta struct {
	ID              string          `json:"id"`
	Label           string          `json:"label"`
	Description     string          `json:"description,omitempty"`
	ParamsSchema    json.RawMessage `json:"params_schema,omitempty"`
	Dangerous       bool            `json:"dangerous"`
	RefreshStrategy string          `json:"refresh_strategy"`
	SuccessMessage  string          `json:"success_message,omitempty"`
}

// Manifest holds static metadata about a note type.
type Manifest struct {
	ID            string          `json:"id"`
	Label         string          `json:"label"`
	Description   string          `json:"description,omitempty"`
	Icon          string          `json:"icon,omitempty"`
	Category      string          `json:"category,omitempty"`
	SortOrder     int             `json:"sort_order"`
	DefaultConfig json.RawMessage `json:"default_config,omitempty"`
	Editor        EditorMeta      `json:"editor"`
	Viewer        ViewerMeta      `json:"viewer"`
	Actions       []ActionMeta    `json:"actions,omitempty"`
	HasConfig     bool            `json:"has_config"`
	HasView       bool            `json:"has_view"`
	HasActions    bool            `json:"has_actions"`
}

// ManifestProvider returns the static Manifest for this note type.
type ManifestProvider interface {
	Manifest() Manifest
}

// ConfigValidator validates configuration payload before saving.
type ConfigValidator interface {
	ValidateConfig(payload json.RawMessage) error
}

// ConfigSaver persists plugin configuration within a transaction.
type ConfigSaver interface {
	SaveConfig(ctx context.Context, tx *sql.Tx, userID int, noteID int64, config json.RawMessage) error
}

// ConfigLoader retrieves plugin configuration as raw JSON.
type ConfigLoader interface {
	LoadConfig(ctx context.Context, db *sql.DB, userID int, noteID int64) (json.RawMessage, error)
}

// ViewBuilder builds a dynamic, computed view of a note's data.
type ViewBuilder interface {
	BuildView(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error)
}

// ActionHandler dispatches a named plugin action and returns the result.
type ActionHandler interface {
	HandleAction(ctx context.Context, db *sql.DB, userID int, noteID int64, actionID string, params json.RawMessage) (any, error)
}

// ErrUnknownAction is returned by ActionHandler when the requested action is not recognised.
// The server translates this to HTTP 404.
var ErrUnknownAction = errors.New("unknown action")
