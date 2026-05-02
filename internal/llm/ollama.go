package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

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
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	model := os.Getenv("OLLAMA_EMBEDDING_MODEL")
	if model == "" {
		model = "hf.co/Qwen/Qwen3-Embedding-4B-GGUF:Q4_K_M"
	}
	return &EmbeddingClient{
		BaseURL: baseURL,
		Model:   model,
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
