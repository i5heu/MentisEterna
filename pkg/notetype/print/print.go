// Package print implements a "print" note type that prints a target note
// (e.g. a recipe) on the thermal receipt printer.
//
// The user selects a target note to print via the frontend.  The plugin
// loads the target's type-specific data, formats it for the receipt printer,
// and sends it to the device.
package print

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/recipe"
	"github.com/i5heu/MentisEterna/pkg/printer"
)

const pluginID = "print"

func init() {
	notetype.Register(&PrintPlugin{})
}

type PrintPlugin struct{}

func (p *PrintPlugin) ID() string { return pluginID }

func (p *PrintPlugin) InitSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_print_target (
			note_id        INTEGER PRIMARY KEY,
			target_note_id INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
	`)
	return err
}

// --- Payload types ---

// Config is the JSON payload stored for a print note.
type Config struct {
	TargetNoteID int64 `json:"target_note_id"`
}

// Candidate is a note that can be printed.
type Candidate struct {
	NoteID    int64  `json:"note_id"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	TypeLabel string `json:"type_label"`
}

// ViewData is what BuildView returns.
type ViewData struct {
	TargetNoteID int64       `json:"target_note_id"`
	Candidates   []Candidate `json:"candidates"`
}

// Printable types — note types that have dedicated receipt formatters.
// Each has a format function: func(db, noteID) (*printer.Buf, string, error).
var printableTypes = map[string]string{
	"recipe": "Recipe",
}

// PrintRequest is the JSON body for the print action.
type PrintRequest struct {
	TargetNoteID int64 `json:"target_note_id"`
}

// PrintResponse is the result of the print action.
type PrintResponse struct {
	Printed bool   `json:"printed"`
	Preview string `json:"preview,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (p *PrintPlugin) CronJobs() []notetype.CronJob {
	return nil
}

func (p *PrintPlugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:            "print",
		Label:         "Print",
		Description:   "Print a note (e.g. recipe) on the thermal receipt printer",
		Category:      "Tools",
		SortOrder:     500,
		DefaultConfig: json.RawMessage(`{"target_note_id":0}`),
		Editor:        notetype.EditorMeta{Mode: "custom"},
		Viewer:        notetype.ViewerMeta{Mode: "custom"},
		Actions: []notetype.ActionMeta{
			{
				ID:              "print",
				Label:           "Print",
				Description:     "Format and print the selected note on the thermal receipt printer",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"target_note_id":{"type":"integer"}},"required":["target_note_id"]}`),
				Dangerous:       false,
				RefreshStrategy: "none",
				SuccessMessage:  "Printed",
			},
		},
		HasConfig:  true,
		HasView:    true,
		HasActions: true,
	}
}

// --- Config interfaces ---

func (p *PrintPlugin) ValidateConfig(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}
	var cfg Config
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return fmt.Errorf("print: invalid config: %w", err)
	}
	return nil
}

func (p *PrintPlugin) SaveConfig(ctx context.Context, tx *sql.Tx, userID int, noteID int64, config json.RawMessage) error {
	var cfg Config
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("print: unmarshal config: %w", err)
		}
	}

	if _, err := tx.Exec(`DELETE FROM ct_print_target WHERE note_id = ?`, noteID); err != nil {
		return fmt.Errorf("print: delete old target: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO ct_print_target (note_id, target_note_id) VALUES (?, ?)`,
		noteID, cfg.TargetNoteID,
	); err != nil {
		return fmt.Errorf("print: insert target: %w", err)
	}

	return nil
}

func (p *PrintPlugin) LoadConfig(ctx context.Context, db *sql.DB, userID int, noteID int64) (json.RawMessage, error) {
	var cfg Config
	err := db.QueryRow(
		`SELECT target_note_id FROM ct_print_target WHERE note_id = ?`,
		noteID,
	).Scan(&cfg.TargetNoteID)
	if err == sql.ErrNoRows {
		return json.RawMessage(`{"target_note_id":0}`), nil
	} else if err != nil {
		return nil, fmt.Errorf("print: load target: %w", err)
	}
	return json.Marshal(cfg)
}

// --- View ---

func (p *PrintPlugin) BuildView(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	// Load current target.
	cfgRaw, err := p.LoadConfig(ctx, db, userID, noteID)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		return nil, err
	}

	// Load candidates: notes of printable types.
	typeNames := make([]string, 0, len(printableTypes))
	typeArgs := make([]interface{}, 0, len(printableTypes))
	for t := range printableTypes {
		typeNames = append(typeNames, "?")
		typeArgs = append(typeArgs, t)
	}

	candidates := []Candidate{}
	if len(typeNames) > 0 {
		rows, err := db.Query(
			`SELECT id, title, type FROM notes WHERE type IN (`+strings.Join(typeNames, ",")+`) ORDER BY title`,
			typeArgs...,
		)
		if err != nil {
			return nil, fmt.Errorf("print: load candidates: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var c Candidate
			if err := rows.Scan(&c.NoteID, &c.Title, &c.Type); err != nil {
				return nil, fmt.Errorf("print: scan candidate: %w", err)
			}
			c.TypeLabel = printableTypes[c.Type]
			if c.TypeLabel == "" {
				c.TypeLabel = c.Type
			}
			candidates = append(candidates, c)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	return &ViewData{
		TargetNoteID: cfg.TargetNoteID,
		Candidates:   candidates,
	}, nil
}

// --- Action ---

func (p *PrintPlugin) HandleAction(ctx context.Context, db *sql.DB, userID int, noteID int64, actionID string, params json.RawMessage) (any, error) {
	switch actionID {
	case "print":
		if db == nil {
			return nil, fmt.Errorf("no database available")
		}
		return p.printTarget(ctx, db, params)
	default:
		return nil, fmt.Errorf("%w: %s", notetype.ErrUnknownAction, actionID)
	}
}

func (p *PrintPlugin) printTarget(ctx context.Context, db *sql.DB, params json.RawMessage) (any, error) {
	var pr PrintRequest
	if err := json.Unmarshal(params, &pr); err != nil {
		return nil, fmt.Errorf("print: invalid params: %w", err)
	}
	if pr.TargetNoteID <= 0 {
		return nil, fmt.Errorf("print: target_note_id is required")
	}

	// Get the target note's type and title.
	var noteType, title string
	if err := db.QueryRow(
		`SELECT type, title FROM notes WHERE id = ?`, pr.TargetNoteID,
	).Scan(&noteType, &title); err != nil {
		return nil, fmt.Errorf("print: target note %d: %w", pr.TargetNoteID, err)
	}

	// Build the receipt buffer based on note type.
	buf, preview, err := formatNote(db, noteType, pr.TargetNoteID, title)
	if err != nil {
		return nil, fmt.Errorf("print: format %s note %d: %w", noteType, pr.TargetNoteID, err)
	}

	// Connect to the printer.
	prDev, err := printer.FindPrinter()
	if err != nil {
		log.Printf("print: printer not available (%v), returning preview", err)
		return &PrintResponse{
			Printed: false,
			Preview: preview,
			Error:   err.Error(),
		}, nil
	}

	// Send and cut.
	if err := printer.SendAndCut(prDev, buf); err != nil {
		return nil, fmt.Errorf("print: send to printer: %w", err)
	}

	return &PrintResponse{Printed: true}, nil
}

// formatNote builds an ESC/POS buffer for the given note type.
// Returns the buffer, a plain-text preview, and any error.
func formatNote(db *sql.DB, noteType string, noteID int64, title string) (*printer.Buf, string, error) {
	switch noteType {
	case "recipe":
		return formatRecipeForPrint(db, noteID, title)
	default:
		// Fetch latest body for generic note types.
		var body string
		db.QueryRow(`SELECT body FROM updates WHERE note_id = ? ORDER BY id DESC LIMIT 1`, noteID).Scan(&body)
		return formatGenericForPrint(title, body)
	}
}

// formatRecipeForPrint loads a recipe's config and formats it.
func formatRecipeForPrint(db *sql.DB, noteID int64, title string) (*printer.Buf, string, error) {
	var payload recipe.Payload
	var servings, attentionTime, totalTime, gramsPerServing, kcalPerServing string
	var freezableInt int
	var preCookServings string

	// Load metadata.
	err := db.QueryRow(`
		SELECT servings, attention_time, total_time, grams_per_serving,
		       kcal_per_serving, freezable, pre_cook_servings
		FROM ct_recipe_meta WHERE note_id = ?`, noteID,
	).Scan(&servings, &attentionTime, &totalTime, &gramsPerServing,
		&kcalPerServing, &freezableInt, &preCookServings)
	if err != nil && err != sql.ErrNoRows {
		return nil, "", fmt.Errorf("load recipe meta: %w", err)
	}

	// Load ingredients.
	rows, err := db.Query(
		`SELECT id, name, amount, unit FROM ct_recipe_ingredients
		 WHERE note_id = ? ORDER BY sort_order`, noteID,
	)
	if err != nil {
		return nil, "", fmt.Errorf("load ingredients: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ing recipe.IngredientRow
		if err := rows.Scan(&ing.ID, &ing.Name, &ing.Amount, &ing.Unit); err != nil {
			return nil, "", fmt.Errorf("scan ingredient: %w", err)
		}
		payload.Ingredients = append(payload.Ingredients, ing)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	payload.Servings = servings
	payload.AttentionTime = attentionTime
	payload.TotalTime = totalTime
	payload.GramsPerServing = gramsPerServing
	payload.KcalPerServing = kcalPerServing
	payload.Freezable = freezableInt != 0
	payload.PreCookServings = preCookServings

	// Fetch latest body.
	var body string
	db.QueryRow(`SELECT body FROM updates WHERE note_id = ? ORDER BY id DESC LIMIT 1`, noteID).Scan(&body)

	buf := recipe.FormatRecipeReceipt(payload, title, body)
	preview := recipe.RecipeTextPrint(payload, title, body)
	return buf, preview, nil
}

// formatGenericForPrint formats a simple fallback receipt for note types
// without dedicated formatters.
func formatGenericForPrint(title string, body string) (*printer.Buf, string, error) {
	w := 48
	b := new(printer.Buf)
	b.Init()
	b.BigSize()
	b.AlignCenter()
	b.Text(title)
	b.Ln()
	b.AlignLeft()
	b.HLine(w)
	if strings.TrimSpace(body) != "" {
		for _, line := range recipe.WrapLines(body, w-2) {
			b.Text("  " + line + "\n")
		}
	} else {
		b.Text("  (no content)\n")
	}
	b.HLine(w)

	var sb strings.Builder
	sb.WriteString(recipe.CenterPad(title, w))
	sb.WriteByte('\n')
	sb.WriteString(strings.Repeat("-", w))
	sb.WriteByte('\n')
	if strings.TrimSpace(body) != "" {
		for _, line := range recipe.WrapLines(body, w-2) {
			sb.WriteString("  " + line + "\n")
		}
	} else {
		sb.WriteString("  (no content)\n")
	}
	sb.WriteString(strings.Repeat("-", w))
	sb.WriteByte('\n')
	preview := sb.String()

	return b, preview, nil
}
