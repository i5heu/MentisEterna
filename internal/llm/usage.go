package llm

import "sort"

// UsageSpec describes one LLM usage (consumer) in the app. The catalog is
// the canonical list llm_inferencer notes map usages against (see
// pkg/notetype/llminferencer): an inferencer note binds usages by id and
// resolves them to a model on one of its tiers.
//
// Wired reports whether the usage is resolved through llm_inferencer notes:
// true → the call site asks the resolver and falls back to the config.toml
// feature/tier system when no inferencer note binds the usage; false → the
// usage is still bound to config.toml only (its call site has not been
// migrated yet). Unwired usages may still be configured in inferencer notes;
// the mapping stays inert until the backend wires the usage.
type UsageSpec struct {
	ID          string `json:"id"` // usage id referenced in inferencer notes
	Feature     string `json:"-"`  // featureSpecs key for the config fallback
	Label       string `json:"label"`
	Description string `json:"description"`
	Wired       bool   `json:"wired"`
}

// usageSpecs: the app's LLM usage catalog, in stable order.
//
// OCR, STT, and embeddings are deliberately absent: they use different API
// shapes (image/audio/file endpoints, dedicated models) and stay bound to
// config.toml.
var usageSpecs = []UsageSpec{
	{
		ID:          "rss_classify",
		Feature:     "rss_classify",
		Label:       "RSS Classification",
		Description: "Classifies fetched RSS articles (category, summary, quality, tags) for rss reader notes",
		Wired:       true,
	},
	{
		ID:          "title",
		Feature:     "title",
		Label:       "Note Title Generation",
		Description: "Auto-generates short kebab-case titles for newly created notes",
		Wired:       false,
	},
	{
		ID:          "tags",
		Feature:     "tags",
		Label:       "Auto Tags",
		Description: "Suggests tags for newly created or updated notes",
		Wired:       false,
	},
	{
		ID:          "subtasks",
		Feature:     "subtasks",
		Label:       "Subtask Generation",
		Description: "Generates subtask checklists for task notes",
		Wired:       false,
	},
}

// UsageSpecs returns the catalog of LLM usages (never nil; do not mutate).
func UsageSpecs() []UsageSpec {
	out := make([]UsageSpec, len(usageSpecs))
	copy(out, usageSpecs)
	return out
}

// UsageSpecByID returns the catalog entry for a usage id, or nil when unknown.
func UsageSpecByID(id string) *UsageSpec {
	for i := range usageSpecs {
		if usageSpecs[i].ID == id {
			spec := usageSpecs[i]
			return &spec
		}
	}
	return nil
}

// KnownUsage reports whether id is in the usage catalog (wired or not).
func KnownUsage(id string) bool { return UsageSpecByID(id) != nil }

// KnownUsageIDs returns the catalog's usage ids in stable order (for error
// messages and clients needing the valid set).
func KnownUsageIDs() []string {
	ids := make([]string, len(usageSpecs))
	for i, spec := range usageSpecs {
		ids[i] = spec.ID
	}
	return ids
}

// NewFeatureChatClient builds the config.toml-bound chat client for a usage
// (feature tier system, [llm.tiers] / [llm.features]). It returns nil when
// the usage is unknown or not a chat-type feature — callers must treat nil
// as "no client".
func NewFeatureChatClient(usage string) *ChatClient {
	spec := UsageSpecByID(usage)
	if spec == nil {
		return nil
	}
	switch spec.Feature {
	case "title":
		return NewTitleClient()
	case "tags":
		return NewAutoTaggerClient()
	case "subtasks":
		return NewSubTaskClient()
	case "rss_classify":
		return NewRSSClassifierClient()
	}
	return nil
}

// SystemDefault describes what the config.toml feature/tier system serves for
// a usage: the effective tier, model, and endpoint. These are the app's
// built-in defaults — what llm_inferencer notes override with their mappings,
// and what an llm_inferencer usage binding of {local: true} pins a usage to.
type SystemDefault struct {
	Tier    string `json:"tier,omitempty"` // smart/medium/small/tiny (config.toml)
	Model   string `json:"model"`
	BaseURL string `json:"base_url,omitempty"`
}

// UsageSystemDefault returns the config.toml feature/tier binding for a usage
// (per-feature tier override, per-tier model/base URL, built-in defaults
// last). ok=false only for usages outside the catalog.
func UsageSystemDefault(usage string) (SystemDefault, bool) {
	spec := UsageSpecByID(usage)
	if spec == nil {
		return SystemDefault{}, false
	}
	return SystemDefault{
		Tier:    FeatureTier(spec.Feature),
		Model:   FeatureModel(spec.Feature),
		BaseURL: FeatureBaseURL(spec.Feature),
	}, true
}

// TierNamesSorted returns tier names sorted alphabetically (helper for
// consumers iterating config tiers).
func TierNamesSorted() []string {
	names := TierNames()
	sort.Strings(names)
	return names
}
