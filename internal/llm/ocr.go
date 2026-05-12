package llm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// OCRer defines the interface for OCR (optical character recognition) on images.
// This allows mocking in tests without requiring a running Ollama instance.
type OCRer interface {
	RunOCR(imageData []byte) (string, error)
}

// OCRClient communicates with an Ollama instance to perform OCR using
// the glm-ocr model (or any configured multimodal model).
type OCRClient struct {
	BaseURL string
	Model   string
	http    *http.Client
}

// NewOCRClient creates an OCR client with sensible defaults. The base URL
// is configurable via OLLAMA_BASE_URL; the model via OLLAMA_OCR_MODEL.
func NewOCRClient() *OCRClient {
	return &OCRClient{
		BaseURL: ollamaBaseURL(),
		Model:   envOr("OLLAMA_OCR_MODEL", "glm-ocr:latest"),
		http:    &http.Client{},
	}
}

// ocrGenerateRequest is the JSON body for the Ollama /api/generate endpoint
// when sending an image for OCR.
type ocrGenerateRequest struct {
	Model  string   `json:"model"`
	Prompt string   `json:"prompt"`
	Images []string `json:"images"`
	Stream bool     `json:"stream"`
}

type ocrGenerateResponse struct {
	Response string `json:"response"`
}

// RunOCR sends image bytes (PNG, JPEG, etc.) to the Ollama /api/generate
// endpoint as a base64-encoded image and returns the recognized text.
func (c *OCRClient) RunOCR(imageData []byte) (string, error) {
	b64 := base64.StdEncoding.EncodeToString(imageData)

	reqBody := ocrGenerateRequest{
		Model:  c.Model,
		Prompt: "Please extract all visible text from this image. Return only the extracted text, with no additional commentary or formatting.",
		Images: []string{b64},
		Stream: false,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal OCR request: %w", err)
	}

	url := c.BaseURL + "/api/generate"
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("ollama OCR request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama OCR returned %d: %s", resp.StatusCode, string(body))
	}

	var gr ocrGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return "", fmt.Errorf("decode OCR response: %w", err)
	}

	return gr.Response, nil
}

// envOr is already defined in ollama.go, but we import from the same
// package — it's shared.
