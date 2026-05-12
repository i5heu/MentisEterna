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
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type generateResponse struct {
	Response string `json:"response"`
}

// GenerateTitle asks the LLM to produce a short, concise title given a note's
// text content. It uses the Ollama /api/generate endpoint.
func (c *ChatClient) GenerateTitle(text string) (string, error) {
	systemPrompt := `You are a title generator for a personal note-taking app.
Given note content, output ONLY the title. Nothing else.

The title will be shown as raw text to the user.

Rules:
- The title must be at most 30 characters.
- Use only characters a-z, A-Z, 0-9, hyphens, and underscores. No spaces, no punctuation.
- Output the title and nothing else: no quotes, no labels, no explanations, no preamble, no markdown, no formatting.
- If the content is empty or unclear, use "Untitled".`

	prompt := systemPrompt + "\n\nNote content:\n" + text

	reqBody := generateRequest{
		Model:  c.Model,
		Prompt: prompt,
		Stream: false,
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
