package llm

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockOCRServer returns an httptest server that emulates the Ollama
// /api/generate endpoint for OCR requests.
func mockOCRServer(t *testing.T, response string, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if statusCode != http.StatusOK {
			w.WriteHeader(statusCode)
			w.Write([]byte(`{"error":"model not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"response":"` + response + `"}`))
	}))
}

func TestOCRClientRunOCR(t *testing.T) {
	srv := mockOCRServer(t, "Hello from image!", http.StatusOK)
	defer srv.Close()

	client := &OCRClient{
		BaseURL: srv.URL,
		Model:   "glm-ocr:latest",
		http:    &http.Client{},
	}

	// Generate a minimal valid PNG image (1x1 pixel)
	// This is a valid PNG file: 1x1 white pixel
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG header
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}

	text, err := client.RunOCR(pngData)
	if err != nil {
		t.Fatalf("RunOCR: %v", err)
	}
	if text != "Hello from image!" {
		t.Errorf("expected 'Hello from image!', got %q", text)
	}
}

func TestOCRClientRunOCRSendsBase64Image(t *testing.T) {
	var capturedBase64 string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse the request body to capture the base64 image
		var req ocrGenerateRequest
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Images) > 0 {
			capturedBase64 = req.Images[0]
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"response":"OK"}`))
	}))
	defer srv.Close()

	client := &OCRClient{
		BaseURL: srv.URL,
		Model:   "glm-ocr:latest",
		http:    &http.Client{},
	}

	testData := []byte("test image data")
	_, err := client.RunOCR(testData)
	if err != nil {
		t.Fatalf("RunOCR: %v", err)
	}

	// Verify the captured base64 decodes back to the original
	decoded, err := base64.StdEncoding.DecodeString(capturedBase64)
	if err != nil {
		t.Fatalf("decode captured base64: %v", err)
	}
	if string(decoded) != "test image data" {
		t.Errorf("expected base64 to encode 'test image data', got %q", string(decoded))
	}
}

func TestOCRClientRunOCRErrorOnHTTPError(t *testing.T) {
	srv := mockOCRServer(t, "", http.StatusInternalServerError)
	defer srv.Close()

	client := &OCRClient{
		BaseURL: srv.URL,
		Model:   "glm-ocr:latest",
		http:    &http.Client{},
	}

	_, err := client.RunOCR([]byte{0})
	if err == nil {
		t.Error("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain status 500, got: %v", err)
	}
}

func TestOCRClientRunOCRErrorOnConnectionFailure(t *testing.T) {
	client := &OCRClient{
		BaseURL: "http://localhost:0", // invalid port
		Model:   "glm-ocr:latest",
		http:    &http.Client{},
	}

	_, err := client.RunOCR([]byte{0})
	if err == nil {
		t.Error("expected connection error")
	}
}

func TestNewOCRClientDefaults(t *testing.T) {
	// Test only that NewOCRClient returns a non-nil client with defaults.
	// We can't test the actual Ollama connection here.
	client := NewOCRClient()
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.Model == "" {
		t.Error("expected non-empty model")
	}
	if client.BaseURL == "" {
		t.Error("expected non-empty base URL")
	}
}
