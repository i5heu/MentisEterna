package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	if err := os.WriteFile(filepath.Join(dir, "inline.html"), []byte("<html><body><script>console.log('inline')</script></body></html>"), 0644); err != nil {
		t.Fatalf("write inline.html: %v", err)
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

func TestSPAHandlerAddsNonceToInlineScripts(t *testing.T) {
	dir := makeStaticDir(t)
	h := newSPAHandler(dir)

	r := httptest.NewRequest(http.MethodGet, "/inline.html", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<script nonce="`) {
		t.Fatalf("expected inline script nonce, got %q", body)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self' 'nonce-") {
		t.Fatalf("expected nonce-aware CSP, got %q", csp)
	}
}

func TestAppContentSecurityPolicyWithNonce(t *testing.T) {
	got := appContentSecurityPolicyWithNonce("abc123")
	wantFragment := "script-src 'self' 'nonce-abc123'"
	if !strings.Contains(got, wantFragment) {
		t.Fatalf("expected %q in CSP, got %q", wantFragment, got)
	}
}

func TestNewSPAHandler(t *testing.T) {
	dir := makeStaticDir(t)
	h := newSPAHandler(dir)
	if h == nil {
		t.Error("expected non-nil handler")
	}
}
