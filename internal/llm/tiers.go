package llm

import (
	"strings"

	"github.com/i5heu/MentisEterna/internal/config"
)

const (
	TierSmart  = "smart"
	TierMedium = "medium"
	TierSmall  = "small"
	TierTiny   = "tiny"
)

// featureSpec: default tier, dedicated model ("ocr"/"stt" if the feature has a
// dedicated model override, else ""), default model.
//
// OCR and STT are single-purpose models (they only perform one task), so they
// have dedicated model overrides that take precedence over the shared tier
// models.
type featureSpec struct {
	defaultTier  string
	dedicated    string // "ocr"/"stt" if the feature has a dedicated model override, else ""
	defaultModel string
}

var featureSpecs = map[string]featureSpec{
	"title":    {TierTiny, "", "granite-4.1-3b-Q4_K_M"},
	"tags":     {TierMedium, "", "gemma-4-e2b-it-qat-q4_0"},
	"subtasks": {TierSmart, "", "Qwen3.6-27B-MTP-GGUF"},
	"ocr":      {TierMedium, "ocr", "GLM-OCR-GGUF"},
	"stt":      {TierSmall, "stt", "vibevoice-cpp-asr"},
}

// TierNames returns the four tier names in capability order.
func TierNames() []string { return []string{TierSmart, TierMedium, TierSmall, TierTiny} }

// FeatureTier returns the tier a feature is bound to (config override, else default).
func FeatureTier(feature string) string {
	spec := featureSpecs[feature]
	if t := config.Get().LLM.Features[feature].Tier; t != "" {
		return strings.ToLower(t)
	}
	return spec.defaultTier
}

// TierModel returns the model for a tier from config ("" if unset).
func TierModel(name string) string { return config.Get().LLM.Tiers[name].Model }

// TierBaseURL returns the per-tier base URL from config ("" if unset).
func TierBaseURL(name string) string {
	return config.Get().LLM.Tiers[name].BaseURL
}

func dedicatedModel(spec featureSpec) string {
	switch spec.dedicated {
	case "ocr":
		return config.Get().LLM.OCRDedicatedModel
	case "stt":
		return config.Get().LLM.STTDedicatedModel
	}
	return ""
}

// FeatureModel: dedicated feature model (OCR/STT only), else tier model, else default.
func FeatureModel(feature string) string {
	spec := featureSpecs[feature]
	if d := dedicatedModel(spec); d != "" {
		return d
	}
	if m := TierModel(FeatureTier(feature)); m != "" {
		return m
	}
	return spec.defaultModel
}

// FeatureBaseURL: tier base URL, else [llm] base_url (via llmBaseURL()).
func FeatureBaseURL(feature string) string {
	if u := TierBaseURL(FeatureTier(feature)); u != "" {
		return u
	}
	return llmBaseURL()
}

// NewTitleClient returns a chat client bound to the title feature's tier.
func NewTitleClient() *ChatClient {
	return NewChatClient(FeatureBaseURL("title"), FeatureModel("title"))
}

// NewAutoTaggerClient returns a chat client bound to the tags feature's tier.
func NewAutoTaggerClient() *ChatClient {
	return NewChatClient(FeatureBaseURL("tags"), FeatureModel("tags"))
}

// NewSubTaskClient returns a chat client bound to the subtasks feature's tier.
func NewSubTaskClient() *ChatClient {
	return NewChatClient(FeatureBaseURL("subtasks"), FeatureModel("subtasks"))
}

// NewTieredOCRClient returns an OCR client bound to the ocr feature's tier.
func NewTieredOCRClient() *OCRClient {
	return NewOCRClient(FeatureBaseURL("ocr"), FeatureModel("ocr"))
}

// NewTieredSTTClient returns an STT client bound to the stt feature's tier.
func NewTieredSTTClient() *STTClient {
	return NewSTTClient(FeatureBaseURL("stt"), FeatureModel("stt"))
}
