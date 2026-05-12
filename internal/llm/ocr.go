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
// This allows mocking in tests without requiring a running LocalAI instance.
type OCRer interface {
	RunOCR(imageData []byte) (string, error)
}

// OCRClient communicates with a LocalAI instance to perform OCR using
// a multimodal vision model via the OpenAI-compatible /v1/chat/completions endpoint.
type OCRClient struct {
	BaseURL string
	Model   string
	http    *http.Client
}

// NewOCRClient creates an OCR client with sensible defaults. The base URL
// is configurable via LOCALAI_BASE_URL; the model via LOCALAI_OCR_MODEL.
func NewOCRClient() *OCRClient {
	return &OCRClient{
		BaseURL: llmBaseURL(),
		Model:   envOr("LOCALAI_OCR_MODEL", "gpt-4o-mini"),
		http:    &http.Client{},
	}
}

// --- OpenAI-compatible multimodal vision types ---

type visionMessage struct {
	Role    string       `json:"role"`
	Content []visionPart `json:"content"`
}

type visionPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *visionImageURL `json:"image_url,omitempty"`
}

type visionImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type visionCompletionRequest struct {
	Model    string          `json:"model"`
	Messages []visionMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type visionCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// RunOCR sends image bytes (PNG, JPEG, etc.) to the LocalAI
// /v1/chat/completions endpoint as a base64 data URL in a multimodal
// content array and returns the recognized text.
func (c *OCRClient) RunOCR(imageData []byte) (string, error) {
	// Detect MIME type from image header bytes
	mime := detectImageMIME(imageData)
	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(imageData)

	reqBody := visionCompletionRequest{
		Model: c.Model,
		Messages: []visionMessage{
			{
				Role: "user",
				Content: []visionPart{
					{
						Type: "text",
						Text: "Please extract all visible text from this image. Return only the extracted text, with no additional commentary or formatting.",
					},
					{
						Type: "image_url",
						ImageURL: &visionImageURL{
							URL:    dataURL,
							Detail: "high",
						},
					},
				},
			},
		},
		Stream: false,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal OCR request: %w", err)
	}

	url := c.BaseURL + "/v1/chat/completions"
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("localai OCR request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("localai OCR returned %d: %s", resp.StatusCode, string(body))
	}

	var cr visionCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", fmt.Errorf("decode OCR response: %w", err)
	}

	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("localai returned no choices for OCR")
	}

	return cr.Choices[0].Message.Content, nil
}

// detectImageMIME inspects the first few bytes to determine the image MIME type.
func detectImageMIME(data []byte) string {
	if len(data) < 4 {
		return "image/png" // default fallback
	}
	// PNG: \x89PNG
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "image/png"
	}
	// JPEG: \xFF\xD8\xFF
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	// GIF: GIF8
	if data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x38 {
		return "image/gif"
	}
	// WebP: RIFF....WEBP
	if data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 &&
		len(data) > 11 &&
		data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50 {
		return "image/webp"
	}
	// BMP: BM
	if data[0] == 0x42 && data[1] == 0x4D {
		return "image/bmp"
	}
	return "image/png" // default fallback
}

// envOr is defined in the llm package and shared across its files.
