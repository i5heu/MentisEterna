// Package notetype defines the plugin interface for custom note types.
// Each note type (recipe, checklist, index, etc.) implements Plugin
// and registers itself via Register() so the server can route requests to it.
package notetype

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

// Plugin is the required base interface that every note type must implement.
//
// Lifecycle:
//  1. InitSchema is called once at server startup to create plugin tables.
//  2. Manifest provides static metadata (label, icon, editor/viewer modes, capabilities, actions).
//  3. Config is persisted/loaded/validated via the optional ConfigValidator, ConfigSaver, ConfigLoader interfaces.
//  4. View data is computed via the optional ViewBuilder interface.
//  5. Actions are dispatched via the optional ActionHandler interface.
//  6. CronJobs returns optional background tasks (e.g., generating grocery lists).
type Plugin interface {
	// ID returns a short, unique identifier for this plugin (e.g. "recipe").
	ID() string

	// InitSchema creates plugin-specific tables in the database.
	// Table names MUST be prefixed: ct_{pluginID}_
	// This is called once at server startup for every registered plugin.
	InitSchema(db *sql.DB) error

	// Manifest returns static metadata about this note type.
	Manifest() Manifest

	// CronJobs returns any background cron jobs this plugin needs.
	// Jobs are registered by the server and run on a shared scheduler.
	CronJobs() []CronJob
}

// Registry holds all active plugins, keyed by their ID().
var Registry = make(map[string]Plugin)

// Register adds a plugin to the global registry.
// Plugins typically call this from their init() function.
func Register(p Plugin) {
	Registry[p.ID()] = p
}

// --- Config/view/action interfaces ---

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

// ConfigValidator validates configuration payload before saving.
type ConfigValidator interface {
	ValidateConfig(config json.RawMessage) error
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

// ValidatePlugin checks that a plugin's implementation matches its declared capabilities.
// Returns nil if the plugin is valid, or an error describing the inconsistency.
func ValidatePlugin(p Plugin) error {
	m := p.Manifest()

	if m.HasConfig {
		if _, ok := p.(ConfigValidator); !ok {
			return fmt.Errorf("plugin %q: HasConfig=true but does not implement ConfigValidator", p.ID())
		}
		if _, ok := p.(ConfigSaver); !ok {
			return fmt.Errorf("plugin %q: HasConfig=true but does not implement ConfigSaver", p.ID())
		}
		if _, ok := p.(ConfigLoader); !ok {
			return fmt.Errorf("plugin %q: HasConfig=true but does not implement ConfigLoader", p.ID())
		}
	}

	if m.HasView {
		if _, ok := p.(ViewBuilder); !ok {
			return fmt.Errorf("plugin %q: HasView=true but does not implement ViewBuilder", p.ID())
		}
	}

	if m.HasActions {
		if _, ok := p.(ActionHandler); !ok {
			return fmt.Errorf("plugin %q: HasActions=true but does not implement ActionHandler", p.ID())
		}
		if len(m.Actions) == 0 {
			return fmt.Errorf("plugin %q: HasActions=true but Actions list is empty", p.ID())
		}
	}

	if len(m.Actions) > 0 && !m.HasActions {
		return fmt.Errorf("plugin %q: HasActions=false but Actions list is non-empty", p.ID())
	}

	if m.Editor.Mode == "schema" && len(m.Editor.Schema) == 0 {
		return fmt.Errorf("plugin %q: Editor.Mode is 'schema' but Editor.Schema is empty", p.ID())
	}

	return nil
}
