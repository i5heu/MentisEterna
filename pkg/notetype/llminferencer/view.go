package llminferencer

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/i5heu/MentisEterna/internal/llm"
)

// BindingView is the effective binding of a usage on this note.
type BindingView struct {
	Tier  string `json:"tier,omitempty"` // "" for direct model bindings
	Model string `json:"model"`
}

// SystemDefaultView is what the app's system configuration (config.toml
// feature/tier system — the LOCAL LLM) serves for a usage when no
// llm_inferencer binding overrides it.
type SystemDefaultView struct {
	Tier    string `json:"tier,omitempty"` // smart/medium/small/tiny (config.toml tier)
	Model   string `json:"model"`
	BaseURL string `json:"base_url,omitempty"`
}

// UsageView is the view's per-usage entry: the catalog entry, the system
// config default for the usage, and this note's effective binding for it
// (nil binding + Local=false = not mapped here → system default applies).
type UsageView struct {
	ID          string       `json:"id"`
	Label       string       `json:"label"`
	Description string       `json:"description"`
	Wired       bool         `json:"wired"` // routed through inferencer notes yet?
	Binding     *BindingView `json:"binding,omitempty"`
	// Local: this note explicitly pins the usage to the local LLM
	// (config.toml) with a {local: true} binding — it overrides the note's
	// default_tier; SystemDefault shows what it resolves to.
	Local bool `json:"local,omitempty"`
	// ViaDefault: no explicit binding, but the note's default_tier serves
	// the usage (Binding then shows that tier/model).
	ViaDefault bool `json:"via_default,omitempty"`
	// SystemDefault is what config.toml serves for this usage — the
	// built-in default shown next to every mapping (and the fallback for
	// every unmapped usage).
	SystemDefault SystemDefaultView `json:"system_default"`
	// Incomplete reports an explicit binding that yields no concrete model
	// (e.g. bound to a tier with no model configured).
	Incomplete bool `json:"incomplete,omitempty"`
}

// TierView is one configured tier of the note.
type TierView struct {
	Tier    string `json:"tier"` // big|middle|small|tiny
	Model   string `json:"model"`
	BaseURL string `json:"base_url,omitempty"`
}

// View is the computed view data for an llm_inferencer note. It never
// contains the API key (api_key_set only).
type View struct {
	Provider       string      `json:"provider"`
	ProviderLabel  string      `json:"provider_label"`
	BaseURL        string      `json:"base_url"`
	DefaultBaseURL string      `json:"default_base_url,omitempty"` // built-in provider default
	APIKeySet      bool        `json:"api_key_set"`
	DefaultTier    string      `json:"default_tier"`
	Tiers          []TierView  `json:"tiers"`
	Usages         []UsageView `json:"usages"`
	Warnings       []string    `json:"warnings,omitempty"`
}

// BuildView implements notetype.ViewBuilder.
func (p *Plugin) BuildView(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	if db == nil {
		return View{
			Provider:      ProviderOpenRouter,
			ProviderLabel: providerLabel(ProviderOpenRouter),
			Tiers:         []TierView{},
			Usages:        usageViews(nil, nil, nil),
		}, nil
	}

	row, ok, err := loadInferencerRow(db, noteID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return View{
			Provider:       ProviderOpenRouter,
			ProviderLabel:  providerLabel(ProviderOpenRouter),
			DefaultBaseURL: providerDefaultBaseURL[ProviderOpenRouter],
			Tiers:          []TierView{},
			Usages:         usageViews(nil, nil, nil),
		}, nil
	}

	var warnings []string

	// The note type is a singleton; more than one can only come from a
	// database created before the uniqueness rule. Surface it loudly —
	// resolution uses the lowest note id.
	if n, err := countInferencerNotes(db); err == nil && n > 1 {
		warnings = append(warnings, fmt.Sprintf(
			"%d llm_inferencer notes exist — the note type is limited to one per app; the lowest note id currently governs, delete the extras", n))
	}

	if row.DefaultTier != "" && !validTier(row.DefaultTier) {
		warnings = append(warnings, fmt.Sprintf("default_tier %q is not one of big/middle/small/tiny", row.DefaultTier))
	}

	// Tiers in capability order.
	tiers := make([]TierView, 0, len(tierNames()))
	for _, tier := range tierNames() {
		tb, has := row.Tiers[tier]
		if !has {
			continue
		}
		model := strings.TrimSpace(tb.Model)
		if model == "" {
			warnings = append(warnings, fmt.Sprintf("tier %q has no model configured", tier))
		} else if tb.BaseURL != "" {
			if err := validateBaseURL(fmt.Sprintf("tier %q base_url", tier), tb.BaseURL); err != nil {
				warnings = append(warnings, err.Error())
			}
		}
		tiers = append(tiers, TierView{Tier: tier, Model: model, BaseURL: strings.TrimSpace(tb.BaseURL)})
	}

	// A note-level endpoint override is worth surfacing (providers have
	// well-known defaults; overriding them is a deliberate act).
	if strings.TrimSpace(row.BaseURL) != "" && row.Provider != ProviderCompatible {
		if def := providerDefaultBaseURL[row.Provider]; def != "" && row.BaseURL != def {
			warnings = append(warnings, "base_url overrides the provider default endpoint")
		}
	}

	return View{
		Provider:       row.Provider,
		ProviderLabel:  providerLabel(row.Provider),
		BaseURL:        strings.TrimSpace(row.BaseURL),
		DefaultBaseURL: providerDefaultBaseURL[row.Provider],
		APIKeySet:      row.Key != "",
		DefaultTier:    row.DefaultTier,
		Tiers:          tiers,
		Usages:         usageViews(row.Usages, row.Tiers, &warnings),
		Warnings:       warnings,
	}, nil
}

// usageViews builds the per-usage view entries from the usage catalog plus
// this note's raw binding maps, collecting incomplete-binding warnings into
// the warnings slice. Every entry carries the config.toml system default
// for the usage (the fallback and the {local: true} target).
func usageViews(usages map[string]UsageBinding, tiers map[string]TierBinding, warnings *[]string) []UsageView {
	var checks *inferencerRow
	if usages != nil || tiers != nil {
		checks = &inferencerRow{Usages: usages, Tiers: tiers}
	}

	specs := llm.UsageSpecs()
	out := make([]UsageView, 0, len(specs))
	for _, spec := range specs {
		uv := UsageView{ID: spec.ID, Label: spec.Label, Description: spec.Description, Wired: spec.Wired}
		if sd, ok := llm.UsageSystemDefault(spec.ID); ok {
			uv.SystemDefault = SystemDefaultView{Tier: sd.Tier, Model: sd.Model, BaseURL: sd.BaseURL}
		}
		ub, has := usages[spec.ID]
		if has {
			if ub.Local {
				// Explicit pin to the local LLM (config.toml).
				uv.Local = true
			} else {
				var b *binding
				if checks != nil {
					b = checks.bindsUsage(spec.ID)
				}
				if b != nil && !b.Local && b.Model != "" {
					uv.Binding = &BindingView{Tier: b.Tier, Model: b.Model}
				} else {
					// Explicit binding that yields no concrete model.
					uv.Incomplete = true
					model := strings.TrimSpace(ub.Model)
					if b != nil && b.Model != "" {
						model = b.Model
					}
					if ub.Tier != "" || model != "" {
						uv.Binding = &BindingView{Tier: ub.Tier, Model: model}
					}
					if warnings != nil {
						*warnings = append(*warnings, fmt.Sprintf(
							"usage %q is bound but yields no model (tier without a configured model?)", spec.ID))
					}
				}
			}
		} else if checks != nil {
			// No explicit binding: the note's default_tier may still serve
			// the usage — surface it as the effective binding.
			if b := checks.bindsUsage(spec.ID); b != nil && !b.Local {
				uv.Binding = &BindingView{Tier: b.Tier, Model: b.Model}
				uv.ViaDefault = true
			}
		}
		out = append(out, uv)
	}
	return out
}

// countInferencerNotes returns how many llm_inferencer notes exist (used to
// warn about pre-uniqueness databases; the type is a singleton).
func countInferencerNotes(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM notes WHERE type = 'llm_inferencer'`).Scan(&n)
	return n, err
}
