package llm

import "testing"

func TestTiers(t *testing.T) {
	t.Run("Defaults", func(t *testing.T) {
		t.Setenv("LLM_TIER_SMART_MODEL", "")
		t.Setenv("LLM_TIER_MEDIUM_MODEL", "")
		t.Setenv("LLM_TIER_SMALL_MODEL", "")
		t.Setenv("LLM_TIER_TINY_MODEL", "")
		t.Setenv("LLM_TIER_TINY_BASE_URL", "")
		t.Setenv("LLM_TIER_MEDIUM_BASE_URL", "")
		t.Setenv("LOCALAI_CHAT_MODEL", "")
		t.Setenv("LOCALAI_OCR_MODEL", "")
		t.Setenv("LOCALAI_STT_MODEL", "")
		t.Setenv("LOCALAI_BASE_URL", "")

		if got := FeatureTier("title"); got != TierTiny {
			t.Errorf("FeatureTier(title) = %q, want %q", got, TierTiny)
		}
		if got := FeatureModel("title"); got != "gpt-3.5-turbo" {
			t.Errorf("FeatureModel(title) = %q, want gpt-3.5-turbo", got)
		}
		if got := FeatureTier("subtasks"); got != TierSmart {
			t.Errorf("FeatureTier(subtasks) = %q, want %q", got, TierSmart)
		}
		if got := FeatureBaseURL("stt"); got != "http://localhost:8080" {
			t.Errorf("FeatureBaseURL(stt) = %q, want http://localhost:8080", got)
		}
	})

	t.Run("TierModelOverride", func(t *testing.T) {
		t.Setenv("LLM_TIER_SMART_MODEL", "qwen-smart")
		if got := FeatureTier("subtasks"); got != TierSmart {
			t.Errorf("FeatureTier(subtasks) = %q, want %q", got, TierSmart)
		}
		if got := FeatureModel("subtasks"); got != "qwen-smart" {
			t.Errorf("FeatureModel(subtasks) = %q, want qwen-smart", got)
		}
	})

	t.Run("TierBaseURLOverride", func(t *testing.T) {
		t.Setenv("LLM_TIER_TINY_BASE_URL", "http://tiny:9000")
		if got := FeatureBaseURL("title"); got != "http://tiny:9000" {
			t.Errorf("FeatureBaseURL(title) = %q, want http://tiny:9000", got)
		}
	})

	t.Run("LegacyFallback", func(t *testing.T) {
		t.Setenv("LLM_TIER_MEDIUM_MODEL", "")
		t.Setenv("LOCALAI_OCR_MODEL", "legacy-ocr")
		if got := FeatureModel("ocr"); got != "legacy-ocr" {
			t.Errorf("FeatureModel(ocr) = %q, want legacy-ocr", got)
		}

		t.Setenv("LLM_TIER_MEDIUM_MODEL", "med")
		if got := FeatureModel("ocr"); got != "med" {
			t.Errorf("FeatureModel(ocr) = %q, want med (tier precedence)", got)
		}
	})
}
