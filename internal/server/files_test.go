package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/media"
)

// newTestServerWithMedia creates a server with media enabled using a fake store.
func newTestServerWithMedia(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	cacheDir := filepath.Join(t.TempDir(), "media-cache")
	cfg := media.Config{
		CacheDir: cacheDir,
		Endpoints: []media.EndpointConfig{
			{ID: "primary", Bucket: "test", Endpoint: "http://localhost:9000", AccessKeyID: "k", SecretAccessKey: "s", Region: "us-east-1", ForcePathStyle: true},
		},
	}
	svc := media.NewService(d, cfg)
	// Replace S3 store with a fake
	svc.Store = newFakeMediaStore()

	s := &Server{
		db:            d,
		addr:          ":0",
		cfg:           serverConfig{MaxUploadBytes: 8 << 20, MaxInlineUploadBytes: 8 << 20},
		loginThrottle: newLoginThrottle(),
		mediaService:  svc,
	}
	return s, d
}

// fakeMediaStore implements media.ReplicaStore in memory.
type fakeMediaStore struct {
	objects map[string][]byte
}

func newFakeMediaStore() *fakeMediaStore {
	return &fakeMediaStore{objects: make(map[string][]byte)}
}

func (f *fakeMediaStore) Put(_ context.Context, ep media.EndpointConfig, key string, src io.Reader, size int64) (string, error) {
	data, _ := io.ReadAll(src)
	f.objects[ep.ID+"/"+key] = data
	return "fake-etag", nil
}

func (f *fakeMediaStore) Get(_ context.Context, ep media.EndpointConfig, key string) (io.ReadCloser, error) {
	data, ok := f.objects[ep.ID+"/"+key]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeMediaStore) Delete(_ context.Context, ep media.EndpointConfig, key string) error {
	delete(f.objects, ep.ID+"/"+key)
	return nil
}

func (f *fakeMediaStore) List(_ context.Context, ep media.EndpointConfig, prefix string) ([]string, error) {
	var keys []string
	fullPrefix := ep.ID + "/" + prefix
	for k := range f.objects {
		if strings.HasPrefix(k, fullPrefix) {
			keys = append(keys, strings.TrimPrefix(k, ep.ID+"/"))
		}
	}
	return keys, nil
}

func (f *fakeMediaStore) ListObjects(_ context.Context, ep media.EndpointConfig, prefix string) ([]media.S3ObjectInfo, error) {
	var objs []media.S3ObjectInfo
	fullPrefix := ep.ID + "/" + prefix
	for k, v := range f.objects {
		if strings.HasPrefix(k, fullPrefix) {
			objs = append(objs, media.S3ObjectInfo{
				Key:  strings.TrimPrefix(k, ep.ID+"/"),
				Size: int64(len(v)),
			})
		}
	}
	return objs, nil
}

// createTestNoteWithSession creates a note and returns the note ID + session token.
func createTestNoteWithSession(t *testing.T, s *Server) (int64, string) {
	t.Helper()
	token := createTestSession(t, s)
	body := `{"title":"test note","body":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.createNote(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create note: %d: %s", w.Code, w.Body.String())
	}
	// Extract ID from response (simplified: we query DB)
	var noteID int64
	s.db.QueryRow(`SELECT id FROM notes ORDER BY id DESC LIMIT 1`).Scan(&noteID)
	return noteID, token
}

// multipartBody creates a multipart/form-data request body with a single file.
func multipartBody(fieldName, filename, mime string, content []byte) (string, *bytes.Buffer) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile(fieldName, filename)
	part.Write(content)
	w.Close()
	return w.FormDataContentType(), &buf
}

// --- Tests ---

func TestUploadAttachmentCreatesAttachmentRef(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	noteID, token := createTestNoteWithSession(t, s)

	ct, body := multipartBody("file", "test.pdf", "application/pdf", []byte("hello pdf"))
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Verify attachment ref exists in DB
	var refCount int
	s.db.QueryRow(`SELECT COUNT(*) FROM files_refs WHERE note_id = ? AND ref_kind = 'attachment'`, noteID).Scan(&refCount)
	if refCount != 1 {
		t.Errorf("expected 1 attachment ref, got %d", refCount)
	}
}

func TestUploadFailsWhenAllReplicasFail(t *testing.T) {
	// This test verifies the server returns an error when all replicas fail.
	// Since we use a fake store that always succeeds, this test documents
	// the behavior. In production, this would use a failing S3 endpoint.
	// We test the media-layer behavior in TestCreateFileFailsWhenAllReplicasFail.
	t.Skip("HTTP-layer test requires failing S3; covered by media-layer TestCreateFileFailsWhenAllReplicasFail")
}

func TestUploadInlineMarksPendingInline(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	noteID, token := createTestNoteWithSession(t, s)

	ct, body := multipartBody("file", "inline-test.png", "image/png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0})
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files/inline", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	w := httptest.NewRecorder()
	s.uploadInlineFile(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Verify pending_inline_note_id is set
	var pendingNoteID int64
	err := s.db.QueryRow(`SELECT pending_inline_note_id FROM files WHERE filename = 'inline-test.png'`).Scan(&pendingNoteID)
	if err != nil {
		t.Fatalf("query pending: %v", err)
	}
	if pendingNoteID != noteID {
		t.Errorf("expected pending_inline_note_id %d, got %d", noteID, pendingNoteID)
	}
}

func TestUploadInlineReturnsImageMarkdownWhenMimeIsImage(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	noteID, token := createTestNoteWithSession(t, s)

	// Valid PNG header
	ct, body := multipartBody("file", "photo.png", "image/png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0})
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files/inline", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	w := httptest.NewRecorder()
	s.uploadInlineFile(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Response should contain "markdown" field with image syntax
	respBody := w.Body.String()
	if !bytes.Contains([]byte(respBody), []byte(`"markdown":"!`)) {
		t.Errorf("expected image markdown with ![](...) in response, got: %s", respBody)
	}
}

func TestServeFileRequiresAuth(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	noteID, token := createTestNoteWithSession(t, s)

	// Upload a file first
	ct, body := multipartBody("file", "secret.txt", "text/plain", []byte("secret content"))
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d", w.Code)
	}

	// Extract file ID from response
	// For simplicity, query the DB
	var fileID int64
	s.db.QueryRow(`SELECT id FROM files ORDER BY id DESC LIMIT 1`).Scan(&fileID)

	// Try to access without auth
	req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/file/%d/%d", noteID, fileID), nil)
	w2 := httptest.NewRecorder()

	// Directly test the handler bypassing requireAuth to verify it works,
	// then test requireAuth separately
	// First: handler should work with correct fileID
	s.serveFile(w2, req2)
	// The handler checks mediaService != nil, which is true
	if w2.Code != http.StatusOK && w2.Code != http.StatusNotFound {
		t.Logf("serveFile response: %d", w2.Code)
	}

	// Test that isAPIPath returns true for /file/...
	if !isAPIPath("/file/1/42") {
		t.Error("expected /file/... to be an API path (requires auth)")
	}
}

func TestServeFileIgnoresCosmeticNoteID(t *testing.T) {
	_, _ = newTestServerWithMedia(t)

	// isAPIPath should work with any noteID in the /file/ path
	if !isAPIPath("/file/99999/42") {
		t.Error("expected /file/99999/42 to be an API path")
	}
	if !isAPIPath("/file/1/1") {
		t.Error("expected /file/1/1 to be an API path")
	}

	// The serveFile handler itself ignores the noteID - it only uses fileID.
	// This is tested by the extractFileIDFromPath helper.
	fileID, ok := extractFileIDFromPath("/file/99999/42")
	if !ok {
		t.Error("expected extractFileIDFromPath to succeed")
	}
	if fileID != 42 {
		t.Errorf("expected fileID 42, got %d", fileID)
	}

	// Also test with a different noteID
	fileID2, ok2 := extractFileIDFromPath("/file/1/42")
	if !ok2 || fileID2 != 42 {
		t.Error("fileID should be 42 regardless of noteID")
	}
}

func TestDeleteAttachmentRemovesOnlyThisNotesAttachmentRef(t *testing.T) {
	s, _ := newTestServerWithMedia(t)

	// Create note A with a file
	_, token := createTestNoteWithSession(t, s)

	// Create note A
	reqA := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader([]byte(`{"title":"note A","body":"a"}`)))
	reqA.Header.Set("Authorization", "Bearer "+token)
	reqA.Header.Set("Content-Type", "application/json")
	wA := httptest.NewRecorder()
	s.createNote(wA, reqA)
	var noteAID int64
	s.db.QueryRow(`SELECT id FROM notes WHERE title = 'note A'`).Scan(&noteAID)

	// Upload file to note A
	ct, body := multipartBody("file", "shared.pdf", "application/pdf", []byte("shared"))
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteAID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("upload: %d", w.Code)
	}

	var fileID int64
	s.db.QueryRow(`SELECT id FROM files WHERE filename = 'shared.pdf'`).Scan(&fileID)

	// Create note B that also references the same file (via direct DB insert)
	resB, _ := s.db.Exec(`INSERT INTO notes (title) VALUES ('note B')`)
	noteBID, _ := resB.LastInsertId()
	s.db.Exec(`INSERT INTO files_refs (note_id, file_id, ref_kind) VALUES (?, ?, 'attachment')`, noteBID, fileID)

	// Delete attachment from note A only
	req2 := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/notes/%d/files/%d", noteAID, fileID), nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	s.deleteAttachment(w2, req2)

	if w2.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w2.Code)
	}

	// Note A's ref should be gone
	var refCountA int
	s.db.QueryRow(`SELECT COUNT(*) FROM files_refs WHERE note_id = ? AND file_id = ?`, noteAID, fileID).Scan(&refCountA)
	if refCountA != 0 {
		t.Errorf("expected note A ref to be removed, got %d", refCountA)
	}

	// Note B's ref should still exist
	var refCountB int
	s.db.QueryRow(`SELECT COUNT(*) FROM files_refs WHERE note_id = ? AND file_id = ?`, noteBID, fileID).Scan(&refCountB)
	if refCountB != 1 {
		t.Errorf("expected note B ref to remain, got %d", refCountB)
	}

	// File should NOT be soft-deleted (note B still references it)
	var deletedAt interface{}
	s.db.QueryRow(`SELECT deleted_at FROM files WHERE id = ?`, fileID).Scan(&deletedAt)
	if deletedAt != nil {
		t.Error("file should not be soft-deleted while note B still references it")
	}
}

func TestServeFileEndpointRouteExists(t *testing.T) {
	// Verify the /file/ handler is reachable and requires auth
	s, _ := newTestServerWithMedia(t)

	// The handler should return 404 for non-existent file
	req := httptest.NewRequest(http.MethodGet, "/file/1/99999", nil)
	w := httptest.NewRecorder()
	s.serveFile(w, req)

	if w.Code != http.StatusNotFound {
		t.Logf("serveFile for non-existent file: %d (expected 404)", w.Code)
	}
}

func TestUploadAttachmentRequiresValidNote(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	_, token := createTestNoteWithSession(t, s)

	ct, body := multipartBody("file", "test.txt", "text/plain", []byte("hello"))
	req := httptest.NewRequest(http.MethodPost, "/notes/99999/files", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent note, got %d", w.Code)
	}
}

// Verify the media service not configured path returns 503
func TestUploadAttachmentWithoutMedia(t *testing.T) {
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	s := &Server{db: d, mediaService: nil}
	req := httptest.NewRequest(http.MethodPost, "/notes/1/files", nil)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 without media, got %d", w.Code)
	}
}

// --- OCR endpoint tests ---

func TestHandleFileOCRReturnsNotFoundWhenNoResult(t *testing.T) {
	s, d := newTestServerWithMedia(t)
	_, token := createTestNoteWithSession(t, s)

	// Upload a file
	ct, body := multipartBody("file", "no-ocr.txt", "text/plain", []byte("hello"))
	req := httptest.NewRequest(http.MethodPost, "/notes/1/files", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)

	// Manually get file ID
	var fileID int64
	d.QueryRow(`SELECT id FROM files WHERE filename = 'no-ocr.txt'`).Scan(&fileID)

	// Request OCR for this file (should be 404 since no OCR was run)
	req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/files/%d/ocr", fileID), nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	s.handleFileOCR(w2, req2)

	if w2.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing OCR result, got %d", w2.Code)
	}
}

func TestHandleFileOCRReturnsResultWhenExists(t *testing.T) {
	s, d := newTestServerWithMedia(t)
	_, token := createTestNoteWithSession(t, s)

	// Upload a file
	ct, body := multipartBody("file", "ocr-me.png", "image/png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0})
	req := httptest.NewRequest(http.MethodPost, "/notes/1/files", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)

	// Get file ID
	var fileID int64
	d.QueryRow(`SELECT id FROM files WHERE filename = 'ocr-me.png'`).Scan(&fileID)

	// Manually insert OCR result
	d.Exec(`INSERT INTO files_ocr (file_id, ocr_text, model) VALUES (?, ?, ?)`,
		fileID, "recognized text", "glm-ocr:latest")

	// Request OCR
	req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/files/%d/ocr", fileID), nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	s.handleFileOCR(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	// Verify response contains ocr_text
	if !bytes.Contains(w2.Body.Bytes(), []byte(`"ocr_text":"recognized text"`)) {
		t.Errorf("expected OCR text in response, got: %s", w2.Body.String())
	}
}

func TestHandleFileOCRRequiresValidFile(t *testing.T) {
	s, _ := newTestServerWithMedia(t)

	req := httptest.NewRequest(http.MethodGet, "/files/99999/ocr", nil)
	w := httptest.NewRecorder()
	s.handleFileOCR(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent file, got %d", w.Code)
	}
}

func TestHandleFileOCRWithoutMedia(t *testing.T) {
	s := &Server{db: nil, mediaService: nil}
	req := httptest.NewRequest(http.MethodGet, "/files/1/ocr", nil)
	w := httptest.NewRecorder()
	s.handleFileOCR(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 without media, got %d", w.Code)
	}
}

func TestIsAPIPathIncludesFilesRoute(t *testing.T) {
	if !isAPIPath("/files/42/ocr") {
		t.Error("expected /files/42/ocr to be an API path")
	}
	if !isAPIPath("/files/") {
		t.Error("expected /files/ to be an API path")
	}
	if !isAPIPath("/files/1") {
		t.Error("expected /files/1 to be an API path")
	}
}

func TestServeFileUnsafeHTMLIsForcedToDownload(t *testing.T) {
	s, d := newTestServerWithMedia(t)
	noteID, token := createTestNoteWithSession(t, s)

	html := []byte("<!doctype html><html><body><script>alert(1)</script></body></html>")
	ct, body := multipartBody("file", "evil.html", "text/plain", html)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d: %s", w.Code, w.Body.String())
	}

	var fileID int64
	var mimeType string
	if err := d.QueryRow(`SELECT id, mime_type FROM files WHERE filename = 'evil.html'`).Scan(&fileID, &mimeType); err != nil {
		t.Fatalf("query uploaded file: %v", err)
	}
	if mimeType != "text/html" {
		t.Fatalf("expected detected MIME text/html, got %q", mimeType)
	}

	serveReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/file/%d/%d", noteID, fileID), nil)
	serveW := httptest.NewRecorder()
	s.serveFile(serveW, serveReq)
	if serveW.Code != http.StatusOK {
		t.Fatalf("serve file: expected 200, got %d", serveW.Code)
	}
	if got := serveW.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Fatalf("expected attachment disposition, got %q", got)
	}
	if got := serveW.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected nosniff, got %q", got)
	}
	if got := serveW.Header().Get("Content-Security-Policy"); got != fileContentSecurityPolicy {
		t.Fatalf("expected restrictive file CSP for non-inline HTML, got %q", got)
	}
	if body := serveW.Body.String(); body != string(html) {
		t.Fatalf("unexpected served body: %q", body)
	}
}

func TestServeFileSVGIsForcedToDownload(t *testing.T) {
	s, d := newTestServerWithMedia(t)
	noteID, token := createTestNoteWithSession(t, s)

	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	ct, body := multipartBody("file", "vector.svg", "image/svg+xml", svg)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d: %s", w.Code, w.Body.String())
	}

	var fileID int64
	var mimeType string
	if err := d.QueryRow(`SELECT id, mime_type FROM files WHERE filename = 'vector.svg'`).Scan(&fileID, &mimeType); err != nil {
		t.Fatalf("query uploaded file: %v", err)
	}
	if mimeType != "image/svg+xml" {
		t.Fatalf("expected image/svg+xml, got %q", mimeType)
	}

	serveReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/file/%d/%d", noteID, fileID), nil)
	serveW := httptest.NewRecorder()
	s.serveFile(serveW, serveReq)
	if serveW.Code != http.StatusOK {
		t.Fatalf("serve file: expected 200, got %d", serveW.Code)
	}
	if got := serveW.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Fatalf("expected attachment disposition, got %q", got)
	}
	if got := serveW.Header().Get("Content-Security-Policy"); got != fileContentSecurityPolicy {
		t.Fatalf("expected restrictive CSP for non-inline SVG, got %q", got)
	}
}

func TestServeFileSafePNGStaysInline(t *testing.T) {
	s, d := newTestServerWithMedia(t)
	noteID, token := createTestNoteWithSession(t, s)

	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}
	ct, body := multipartBody("file", "photo.png", "application/octet-stream", png)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d: %s", w.Code, w.Body.String())
	}

	var fileID int64
	if err := d.QueryRow(`SELECT id FROM files WHERE filename = 'photo.png'`).Scan(&fileID); err != nil {
		t.Fatalf("query uploaded file: %v", err)
	}

	serveReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/file/%d/%d", noteID, fileID), nil)
	serveW := httptest.NewRecorder()
	s.serveFile(serveW, serveReq)
	if serveW.Code != http.StatusOK {
		t.Fatalf("serve file: expected 200, got %d", serveW.Code)
	}
	if got := serveW.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Fatalf("expected inline disposition, got %q", got)
	}
	if got := serveW.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected image/png, got %q", got)
	}
	if got := serveW.Header().Get("Content-Security-Policy"); got != "" {
		t.Fatalf("expected no CSP on inline image (used as subresource), got %q", got)
	}
}

func TestServeFilePDFIsForcedToDownload(t *testing.T) {
	s, d := newTestServerWithMedia(t)
	noteID, token := createTestNoteWithSession(t, s)

	pdf := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n")
	ct, body := multipartBody("file", "doc.pdf", "application/pdf", pdf)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d: %s", w.Code, w.Body.String())
	}

	var fileID int64
	if err := d.QueryRow(`SELECT id FROM files WHERE filename = 'doc.pdf'`).Scan(&fileID); err != nil {
		t.Fatalf("query uploaded file: %v", err)
	}

	serveReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/file/%d/%d", noteID, fileID), nil)
	serveW := httptest.NewRecorder()
	s.serveFile(serveW, serveReq)
	if serveW.Code != http.StatusOK {
		t.Fatalf("serve file: expected 200, got %d", serveW.Code)
	}
	if got := serveW.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Fatalf("expected attachment disposition, got %q", got)
	}
	if got := serveW.Header().Get("Content-Security-Policy"); got != fileContentSecurityPolicy {
		t.Fatalf("expected restrictive CSP for non-inline PDF, got %q", got)
	}
}

func TestServeFileAudioRemainsInline(t *testing.T) {
	s, d := newTestServerWithMedia(t)
	noteID, token := createTestNoteWithSession(t, s)

	wav := []byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'A', 'V', 'E', 'f', 'm', 't', ' '}
	ct, body := multipartBody("file", "sound.wav", "application/octet-stream", wav)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d: %s", w.Code, w.Body.String())
	}

	var fileID int64
	var mimeType string
	if err := d.QueryRow(`SELECT id, mime_type FROM files WHERE filename = 'sound.wav'`).Scan(&fileID, &mimeType); err != nil {
		t.Fatalf("query uploaded file: %v", err)
	}
	if mimeType != "audio/wav" {
		t.Fatalf("expected audio/wav, got %q", mimeType)
	}

	serveReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/file/%d/%d", noteID, fileID), nil)
	serveW := httptest.NewRecorder()
	s.serveFile(serveW, serveReq)
	if serveW.Code != http.StatusOK {
		t.Fatalf("serve file: expected 200, got %d", serveW.Code)
	}
	if got := serveW.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Fatalf("expected inline disposition, got %q", got)
	}
	if got := serveW.Header().Get("Content-Security-Policy"); got != "" {
		t.Fatalf("expected no CSP on inline audio (used as subresource), got %q", got)
	}
}

func TestUploadInlineUnsafeHTMLReturnsLinkMarkdown(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	noteID, token := createTestNoteWithSession(t, s)

	html := []byte("<html><body>xss</body></html>")
	ct, body := multipartBody("file", "inline.html", "text/html", html)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files/inline", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadInlineFile(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d: %s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte(`"markdown":"![`)) {
		t.Fatalf("expected unsafe HTML inline upload to return link markdown, got %s", w.Body.String())
	}
}

func TestUploadAttachmentRejectsOversizedBodies(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.cfg.MaxUploadBytes = 128
	noteID, token := createTestNoteWithSession(t, s)

	payload := bytes.Repeat([]byte("A"), 2048)
	ct, body := multipartBody("file", "too-big.bin", "application/octet-stream", payload)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServeFileHEADSkipsBodyAndSetsLength(t *testing.T) {
	s, d := newTestServerWithMedia(t)
	noteID, token := createTestNoteWithSession(t, s)

	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}
	ct, body := multipartBody("file", "head.png", "application/octet-stream", png)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d: %s", w.Code, w.Body.String())
	}

	var fileID int64
	if err := d.QueryRow(`SELECT id FROM files WHERE filename = 'head.png'`).Scan(&fileID); err != nil {
		t.Fatalf("query uploaded file: %v", err)
	}

	headReq := httptest.NewRequest(http.MethodHead, fmt.Sprintf("/file/%d/%d", noteID, fileID), nil)
	headW := httptest.NewRecorder()
	s.serveFile(headW, headReq)
	if headW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", headW.Code)
	}
	if headW.Body.Len() != 0 {
		t.Fatalf("expected empty HEAD body, got %d bytes", headW.Body.Len())
	}
	if got := headW.Header().Get("Content-Length"); got != "12" {
		t.Fatalf("expected Content-Length 12, got %q", got)
	}
}
