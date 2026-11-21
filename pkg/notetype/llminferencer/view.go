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

// UsageView is the view's per-usage entry: the catalog entry plus this
// note's effective binding for it (nil binding = not mapped here).
type UsageView struct {
	ID          string       `json:"id"`
	Label       string       `json:"label"`
	Description string       `json:"description"`
	Wired       bool         `json:"wired"` // routed through inferencer notes yet?
	Binding     *BindingView `json:"binding,omitempty"`
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
// the warnings slice.
func usageViews(usages map[string]UsageBinding, tiers map[string]TierBinding, warnings *[]string) []UsageView {
	var checks *inferencerRow
	if usages != nil || tiers != nil {
		checks = &inferencerRow{Usages: usages, Tiers: tiers}
	}

	specs := llm.UsageSpecs()
	out := make([]UsageView, 0, len(specs))
	for _, spec := range specs {
		uv := UsageView{ID: spec.ID, Label: spec.Label, Description: spec.Description, Wired: spec.Wired}
		ub, has := usages[spec.ID]
		if has {
			var b *binding
			if checks != nil {
				b = checks.bindsUsage(spec.ID)
			}
			if b != nil && b.Model != "" {
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
		out = append(out, uv)
	}
	return out
}
