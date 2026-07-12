package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
)

// STTer defines the interface for Speech-to-Text transcription on audio files.
// This allows mocking in tests without requiring a running LocalAI instance.
type STTer interface {
	RunSTT(audioData []byte, filename string) (string, error)
}

// STTClient communicates with a LocalAI instance to perform transcription using
// a whisper model via the OpenAI-compatible /v1/audio/transcriptions endpoint.
type STTClient struct {
	BaseURL string
	Model   string
	http    *http.Client
}

// NewSTTClient creates an STT client with sensible defaults. The base URL
// is configurable via LOCALAI_BASE_URL; the model via LOCALAI_STT_MODEL.
func NewSTTClient() *STTClient {
	return &STTClient{
		BaseURL: llmBaseURL(),
		Model:   envOr("LOCALAI_STT_MODEL", "nemo-parakeet-tdt-0.6b"),
		http:    newLLMHTTPClient(),
	}
}

// OpenAI-compatible transcription response type.
type transcriptionResponse struct {
	Text string `json:"text"`
}

// RunSTT sends audio bytes to the LocalAI /v1/audio/transcriptions endpoint
// as a multipart form upload and returns the transcribed text.
func (c *STTClient) RunSTT(audioData []byte, filename string) (string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add the file part.
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".wav" // default fallback
	}
	part, err := writer.CreateFormFile("file", "audio"+ext)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(audioData); err != nil {
		return "", fmt.Errorf("write audio data: %w", err)
	}

	// Add the model part.
	if err := writer.WriteField("model", c.Model); err != nil {
		return "", fmt.Errorf("write model field: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	url := c.BaseURL + "/v1/audio/transcriptions"
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		return "", fmt.Errorf("create STT request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("localai STT request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("localai STT returned %d: %s", resp.StatusCode, string(body))
	}

	var tr transcriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode STT response: %w", err)
	}

	return tr.Text, nil
}
