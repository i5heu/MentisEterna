package llm

import (
	"os"
	"strings"
)

const (
	TierSmart  = "smart"
	TierMedium = "medium"
	TierSmall  = "small"
	TierTiny   = "tiny"
)

// featureSpec: tier assignment env var name, default tier, dedicated model env
// var ("" if none), legacy model env var, default model.
//
// OCR and STT are single-purpose models (they only perform one task), so they
// have dedicated model env vars that take precedence over the shared tier
// model vars.
type featureSpec struct {
	tierEnv        string
	defaultTier    string
	dedicatedModel string
	legacyModel    string
	defaultModel   string
}

var featureSpecs = map[string]featureSpec{
	"title":    {"LLM_FEATURE_TITLE_TIER", TierTiny, "", "LOCALAI_CHAT_MODEL", "gpt-3.5-turbo"},
	"tags":     {"LLM_FEATURE_TAGS_TIER", TierMedium, "", "LOCALAI_CHAT_MODEL", "gpt-3.5-turbo"},
	"subtasks": {"LLM_FEATURE_SUBTASKS_TIER", TierSmart, "", "LOCALAI_CHAT_MODEL", "gpt-3.5-turbo"},
	"ocr":      {"LLM_FEATURE_OCR_TIER", TierMedium, "LLM_OCR_MODEL", "LOCALAI_OCR_MODEL", "gpt-4o-mini"},
	"stt":      {"LLM_FEATURE_STT_TIER", TierSmall, "LLM_STT_MODEL", "LOCALAI_STT_MODEL", "nemo-parakeet-tdt-0.6b"},
}

// TierNames returns the four tier names in capability order.
func TierNames() []string { return []string{TierSmart, TierMedium, TierSmall, TierTiny} }

// FeatureTier returns the tier a feature is bound to (LLM_FEATURE_<UPPER(feature)>_TIER, else default).
func FeatureTier(feature string) string {
	spec := featureSpecs[feature]
	if v := os.Getenv(spec.tierEnv); v != "" {
		return strings.ToLower(v)
	}
	return spec.defaultTier
}

// TierModel returns LLM_TIER_<NAME>_MODEL ("" if unset).
func TierModel(name string) string { return os.Getenv("LLM_TIER_" + strings.ToUpper(name) + "_MODEL") }

// TierBaseURL returns LLM_TIER_<NAME>_BASE_URL ("" if unset).
func TierBaseURL(name string) string {
	return os.Getenv("LLM_TIER_" + strings.ToUpper(name) + "_BASE_URL")
}

// FeatureModel: dedicated feature model (OCR/STT only), else tier model, else
// legacy feature env, else default.
func FeatureModel(feature string) string {
	spec := featureSpecs[feature]
	if spec.dedicatedModel != "" {
		if v := os.Getenv(spec.dedicatedModel); v != "" {
			return v
		}
	}
	if m := TierModel(FeatureTier(feature)); m != "" {
		return m
	}
	if v := os.Getenv(spec.legacyModel); v != "" {
		return v
	}
	return spec.defaultModel
}

// FeatureBaseURL: tier base URL, else LOCALAI_BASE_URL (via existing llmBaseURL()).
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
