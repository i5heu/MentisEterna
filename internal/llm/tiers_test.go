package llm

import (
	"testing"

	"github.com/i5heu/MentisEterna/internal/config"
)

// setTierModel sets a tier's model in the global config, working around maps
// being non-addressable.
func setTierModel(name, model string) {
	c := config.Get()
	if c.LLM.Tiers == nil {
		c.LLM.Tiers = map[string]config.TierConfig{}
	}
	tiers := c.LLM.Tiers
	t := tiers[name]
	t.Model = model
	tiers[name] = t
	c.LLM.Tiers = tiers
}

// setTierBaseURL sets a tier's base URL in the global config, working around
// maps being non-addressable.
func setTierBaseURL(name, baseURL string) {
	c := config.Get()
	if c.LLM.Tiers == nil {
		c.LLM.Tiers = map[string]config.TierConfig{}
	}
	tiers := c.LLM.Tiers
	t := tiers[name]
	t.BaseURL = baseURL
	tiers[name] = t
	c.LLM.Tiers = tiers
}

func TestTiers(t *testing.T) {
	t.Run("Defaults", func(t *testing.T) {
		config.Reset()
		t.Cleanup(config.Reset)

		if got := FeatureTier("title"); got != TierTiny {
			t.Errorf("FeatureTier(title) = %q, want %q", got, TierTiny)
		}
		if got := FeatureModel("title"); got != "granite-4.1-3b-Q4_K_M" {
			t.Errorf("FeatureModel(title) = %q, want granite-4.1-3b-Q4_K_M (tiny default)", got)
		}
		if got := FeatureTier("subtasks"); got != TierSmart {
			t.Errorf("FeatureTier(subtasks) = %q, want %q", got, TierSmart)
		}
		if got := FeatureBaseURL("stt"); got != "http://localhost:8080" {
			t.Errorf("FeatureBaseURL(stt) = %q, want http://localhost:8080", got)
		}
		if got := FeatureModel("ocr"); got != "GLM-OCR-GGUF" {
			t.Errorf("FeatureModel(ocr) = %q, want GLM-OCR-GGUF (dedicated default)", got)
		}
		if got := FeatureModel("stt"); got != "vibevoice-cpp-asr" {
			t.Errorf("FeatureModel(stt) = %q, want vibevoice-cpp-asr (dedicated default)", got)
		}

		// Gate/retry defaults must match config.default.toml: they are the
		// behavior any deployment gets without an explicit [llm] section
		// (Docker image, config files predating the retry feature).
		llmCfg := config.Get().LLM
		if llmCfg.MaxConcurrency != 1 {
			t.Errorf("MaxConcurrency = %d, want 1", llmCfg.MaxConcurrency)
		}
		if llmCfg.RequestCooldownMS != 1000 {
			t.Errorf("RequestCooldownMS = %d, want 1000", llmCfg.RequestCooldownMS)
		}
		if llmCfg.RetryAttempts != 3 {
			t.Errorf("RetryAttempts = %d, want 3", llmCfg.RetryAttempts)
		}
		if llmCfg.RetryDelayMS != 3000 {
			t.Errorf("RetryDelayMS = %d, want 3000", llmCfg.RetryDelayMS)
		}
	})

	t.Run("TierModelOverride", func(t *testing.T) {
		config.Reset()
		t.Cleanup(config.Reset)
		setTierModel(TierSmart, "qwen-smart")
		if got := FeatureTier("subtasks"); got != TierSmart {
			t.Errorf("FeatureTier(subtasks) = %q, want %q", got, TierSmart)
		}
		if got := FeatureModel("subtasks"); got != "qwen-smart" {
			t.Errorf("FeatureModel(subtasks) = %q, want qwen-smart", got)
		}
	})

	t.Run("TierBaseURLOverride", func(t *testing.T) {
		config.Reset()
		t.Cleanup(config.Reset)
		setTierBaseURL(TierTiny, "http://tiny:9000")
		if got := FeatureBaseURL("title"); got != "http://tiny:9000" {
			t.Errorf("FeatureBaseURL(title) = %q, want http://tiny:9000", got)
		}
	})

	t.Run("DedicatedOCRSTTModel", func(t *testing.T) {
		config.Reset()
		t.Cleanup(config.Reset)
		setTierModel(TierMedium, "med")
		setTierModel(TierSmall, "small")

		// Dedicated model wins over tier.
		config.Get().LLM.OCRDedicatedModel = "dedicated-ocr"
		if got := FeatureModel("ocr"); got != "dedicated-ocr" {
			t.Errorf("FeatureModel(ocr) = %q, want dedicated-ocr", got)
		}

		// Unset dedicated -> tier model.
		config.Get().LLM.OCRDedicatedModel = ""
		if got := FeatureModel("ocr"); got != "med" {
			t.Errorf("FeatureModel(ocr) = %q, want med (tier)", got)
		}

		// STT same pattern.
		config.Get().LLM.STTDedicatedModel = "dedicated-stt"
		if got := FeatureModel("stt"); got != "dedicated-stt" {
			t.Errorf("FeatureModel(stt) = %q, want dedicated-stt", got)
		}
		config.Get().LLM.STTDedicatedModel = ""
		if got := FeatureModel("stt"); got != "small" {
			t.Errorf("FeatureModel(stt) = %q, want small (tier)", got)
		}
	})
}
