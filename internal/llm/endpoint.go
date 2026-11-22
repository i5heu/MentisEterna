package llm

import "strings"

// OpenAIEndpoint builds the absolute URL of an OpenAI-compatible endpoint
// (path: "chat/completions", "embeddings", "models", "audio/transcriptions")
// from a user-supplied base URL.
//
// Base-URL conventions vary ("worked in the OpenAI SDK, 404s here"): the
// OpenAI SDK's documented bases carry the version segment themselves
// (OpenRouter: https://openrouter.ai/api/v1, Mistral's terminaux:
// https://api.mistral.ai/v1), while this app treats the base as the API
// root (https://openrouter.ai/api) and appends /v1/<path>. Naive
// concatenation turns a documented base into …/v1/v1/chat/completions →
// 404 {"error":{"message":"Not Found","code":404}}. All conventions are
// accepted:
//
//	base https://host/api               → https://host/api/v1/chat/completions
//	base https://host/api/v1            → https://host/api/v1/chat/completions
//	base https://host.ui/               → stripped of trailing slashes first
//	base https://host/api/chat/...       (full endpoint pasted) → unchanged
//	base ""                             → /v1/<path> (tests, defaults)
func OpenAIEndpoint(base, path string) string {
	path = strings.Trim(strings.TrimSpace(path), "/")
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if b == "" {
		return "/v1/" + path
	}

	// The complete endpoint URL was pasted verbatim — nothing to append.
	if strings.HasSuffix(b, "/v1/"+path) {
		return b
	}

	// OpenAI-SDK-style bases carry the version segment themselves; strip
	// exactly one trailing "/v1" (deeper paths are left alone).
	b = strings.TrimSuffix(b, "/v1")
	return b + "/v1/" + path
}
