package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// Embedder defines the interface for generating text embeddings.
// This allows mocking in tests without requiring a running Ollama instance.
type Embedder interface {
	GenerateEmbedding(text string) ([]float64, error)
}

// Generator defines the interface for generating text via an LLM.
// This allows mocking in tests without requiring a running Ollama instance.
type Generator interface {
	GenerateTitle(text string) (string, error)
}

// --- Shared HTTP client & base URL helpers ---

// ollamaBaseURL returns the Ollama base URL, configurable via the
// OLLAMA_BASE_URL environment variable (default: http://localhost:11434).
func ollamaBaseURL() string {
	if u := os.Getenv("OLLAMA_BASE_URL"); u != "" {
		return u
	}
	return "http://localhost:11434"
}

// EmbeddingClient communicates with an Ollama instance to generate embeddings.
type EmbeddingClient struct {
	BaseURL string
	Model   string
	http    *http.Client
}

// NewEmbeddingClient creates a client with sensible defaults. The base URL and
// model can be overridden via environment variables OLLAMA_BASE_URL and
// OLLAMA_EMBEDDING_MODEL.
func NewEmbeddingClient() *EmbeddingClient {
	return &EmbeddingClient{
		BaseURL: ollamaBaseURL(),
		Model:   envOr("OLLAMA_EMBEDDING_MODEL", "hf.co/Qwen/Qwen3-Embedding-4B-GGUF:Q4_K_M"),
		http:    &http.Client{},
	}
}

// ChatClient communicates with an Ollama instance for text generation
// (e.g., auto-generating note titles).
type ChatClient struct {
	BaseURL string
	Model   string
	http    *http.Client
}

// NewChatClient creates a chat client with sensible defaults. The base URL
// is configurable via OLLAMA_BASE_URL; the model via OLLAMA_CHAT_MODEL.
func NewChatClient() *ChatClient {
	return &ChatClient{
		BaseURL: ollamaBaseURL(),
		Model:   envOr("OLLAMA_CHAT_MODEL", "hf.co/nvidia/NVIDIA-Nemotron-3-Nano-4B-GGUF:Q4_K_M"),
		http:    &http.Client{},
	}
}

type embeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type embeddingResponse struct {
	Embedding []float64 `json:"embedding"`
}

// GenerateEmbedding hits the Ollama /api/embeddings endpoint and returns a
// slice of float64 values representing the sentence embedding.
func (c *EmbeddingClient) GenerateEmbedding(text string) ([]float64, error) {
	reqBody := embeddingRequest{
		Model:  c.Model,
		Prompt: text,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.BaseURL + "/api/embeddings"
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(body))
	}

	var er embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return er.Embedding, nil
}

// --- Chat / Generation ---

type generateRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	System  string         `json:"system"`
	Stream  bool           `json:"stream"`
	Options map[string]any `json:"options,omitempty"`
}

type generateResponse struct {
	Response string `json:"response"`
}

// GenerateTitle asks the LLM to produce a short, concise title given a note's
// text content. It uses the Ollama /api/generate endpoint.
func (c *ChatClient) GenerateTitle(text string) (string, error) {
	systemPrompt := `You are a highly constrained, automated backend microservice responsible for generating note titles. Your sole function is to receive raw note content and output a single, strictly formatted text string.

CRITICAL RULES:
1. MAXIMUM LENGTH: The output must not exceed 30 characters.
2. ALLOWED CHARACTERS: Strictly limited to alphanumeric characters, hyphens, spaces and underscores "[a-zA-Z0-9_-]". Absolutely NO emojis, and NO punctuation.
3. WORD SEPARATION: Because spaces are forbidden, you must use kebab-case (e.g., my-new-note) or snake_case (e.g., my_new_note) to separate words.
4. CONTENT EXTRACTION: Identify the core subject, action, or entity. Discard filler words (a, the, and).
5. FALLBACK: If the input is empty, completely unreadable, or lacks clear meaning, output exactly: Untitled
6. ZERO-SHOT OUTPUT: You must output ONLY the final string. NO markdown code blocks (do not use '''), NO quotation marks, NO preamble ("Here is the title:"), and NO conversational text.

EXAMPLES:
Input: "Need to remember to buy milk, eggs, and bread from the store tomorrow."
Output: grocery-list

Input: "Meeting with the design team regarding the new UI wireframes for the mobile app."
Output: design-team-ui-wireframes

Input: "12345 67890"
Output: 12345-67890

Input: ""
Output: Untitled

INPUT TO PROCESS:
[Insert User Note Content Here]`

	reqBody := generateRequest{
		Model:  c.Model,
		System: systemPrompt,
		Prompt: text,
		Stream: false,
		Options: map[string]any{
			"num_predict": 40,
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := c.BaseURL + "/api/generate"
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(body))
	}

	var gr generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return gr.Response, nil
}

// EmbeddingToJSON marshals a float64 slice to a VSS-compatible JSON array
// string like "[0.1,0.2,...]".
func EmbeddingToJSON(vec []float64) string {
	b, _ := json.Marshal(vec)
	return string(b)
}

// CombineTitleBody returns a single input string for the embedding model.
func CombineTitleBody(title, body string) string {
	if body == "" {
		return title
	}
	return title + "\n" + body
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
