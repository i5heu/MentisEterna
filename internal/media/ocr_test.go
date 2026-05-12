package media

import (
	"context"
	"database/sql"
	"testing"
)

// mockOCRClient implements llm.OCRer for testing.
type mockOCRClient struct {
	text string
	err  error
}

func (m *mockOCRClient) RunOCR(_ []byte) (string, error) {
	return m.text, m.err
}

// testOCRService creates a Service with minimal configuration for OCR tests.
func testOCRService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	svc, _ := newTestService(t)
	return svc, svc.DB.DB
}

func TestIsOCRable(t *testing.T) {
	tests := []struct {
		mime     string
		expected bool
	}{
		{"image/png", true},
		{"image/jpeg", true},
		{"image/gif", true},
		{"image/webp", true},
		{"image/bmp", true},
		{"image/tiff", true},
		{"image/svg+xml", true},
		{"application/pdf", false},
		{"text/plain", false},
		{"video/mp4", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.mime, func(t *testing.T) {
			if got := IsOCRable(tt.mime); got != tt.expected {
				t.Errorf("IsOCRable(%q) = %v, want %v", tt.mime, got, tt.expected)
			}
		})
	}
}

func TestSaveAndGetOCRResult(t *testing.T) {
	svc, db := testOCRService(t)

	// Create a file record first
	res, err := db.Exec(`
		INSERT INTO files (original_note_id, storage_key, filename, mime_type, size_bytes,
		                   plaintext_sha256, ciphertext_sha256, aes_key, aes_nonce)
		VALUES (NULL, 'test-key', 'test.png', 'image/png', 100,
		        'aa', 'bb', x'0001', x'0002')
	`)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	fileID, _ := res.LastInsertId()

	// Save a result
	result, err := svc.saveOCRResult(fileID, "Hello World", "glm-ocr:latest")
	if err != nil {
		t.Fatalf("saveOCRResult: %v", err)
	}
	if result.OCRText != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", result.OCRText)
	}

	// Get it back
	got, err := svc.GetOCRResult(fileID)
	if err != nil {
		t.Fatalf("GetOCRResult: %v", err)
	}
	if got.OCRText != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", got.OCRText)
	}
	if got.Model != "glm-ocr:latest" {
		t.Errorf("expected 'glm-ocr:latest', got %q", got.Model)
	}
	if got.Error != "" {
		t.Errorf("expected no error, got %q", got.Error)
	}
}

func TestSaveOCRError(t *testing.T) {
	svc, db := testOCRService(t)

	res, err := db.Exec(`
		INSERT INTO files (original_note_id, storage_key, filename, mime_type, size_bytes,
		                   plaintext_sha256, ciphertext_sha256, aes_key, aes_nonce)
		VALUES (NULL, 'test-key-err', 'test.png', 'image/png', 100,
		        'ee', 'ff', x'0005', x'0006')
	`)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	fileID, _ := res.LastInsertId()

	result, err := svc.saveOCRError(fileID, "glm-ocr:latest", context.DeadlineExceeded)
	if err != nil {
		t.Fatalf("saveOCRError: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error in result")
	}
	if result.OCRText != "" {
		t.Errorf("expected empty OCR text, got %q", result.OCRText)
	}

	// Verify in DB
	got, err := svc.GetOCRResult(fileID)
	if err != nil {
		t.Fatalf("GetOCRResult: %v", err)
	}
	if got.Error == "" {
		t.Error("expected error stored in DB")
	}
}

func TestSaveOCRResultUpserts(t *testing.T) {
	svc, db := testOCRService(t)

	res, err := db.Exec(`
		INSERT INTO files (original_note_id, storage_key, filename, mime_type, size_bytes,
		                   plaintext_sha256, ciphertext_sha256, aes_key, aes_nonce)
		VALUES (NULL, 'test-key-upsert', 'test.png', 'image/png', 100,
		        'gg', 'hh', x'0007', x'0008')
	`)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	fileID, _ := res.LastInsertId()

	// First save
	_, err = svc.saveOCRResult(fileID, "First pass", "model-v1")
	if err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Second save (should update, not insert a second row)
	_, err = svc.saveOCRResult(fileID, "Second pass", "model-v2")
	if err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, err := svc.GetOCRResult(fileID)
	if err != nil {
		t.Fatalf("GetOCRResult: %v", err)
	}
	if got.OCRText != "Second pass" {
		t.Errorf("expected 'Second pass', got %q", got.OCRText)
	}
	if got.Model != "model-v2" {
		t.Errorf("expected 'model-v2', got %q", got.Model)
	}

	// Verify only one row
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM files_ocr WHERE file_id = ?`, fileID).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row after upsert, got %d", count)
	}
}

func TestGetOCRResultNotFound(t *testing.T) {
	svc, _ := testOCRService(t)

	_, err := svc.GetOCRResult(99999)
	if err == nil {
		t.Error("expected error for non-existent OCR result")
	}
}
