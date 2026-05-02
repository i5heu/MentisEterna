package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func makeStaticDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>index</html>"), 0644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('hi')"), 0644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}
	return dir
}

func TestSPAHandlerExistingFile(t *testing.T) {
	dir := makeStaticDir(t)
	h := newSPAHandler(dir)

	r := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if body := w.Body.String(); body != "console.log('hi')" {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestSPAHandlerFallbackToIndex(t *testing.T) {
	dir := makeStaticDir(t)
	h := newSPAHandler(dir)

	r := httptest.NewRequest(http.MethodGet, "/nonexistent/route", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 fallback, got %d", w.Code)
	}
	if body := w.Body.String(); body != "<html>index</html>" {
		t.Errorf("expected index.html content, got %q", body)
	}
}

func TestNewSPAHandler(t *testing.T) {
	dir := makeStaticDir(t)
	h := newSPAHandler(dir)
	if h == nil {
		t.Error("expected non-nil handler")
	}
}
