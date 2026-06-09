package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersMiddlewareSetsAppHeaders(t *testing.T) {
	s := newTestServer(t)
	h := s.withSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	r := httptest.NewRequest(http.MethodGet, "/notes", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected nosniff, got %q", got)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("expected DENY, got %q", got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Fatalf("expected strict-origin-when-cross-origin, got %q", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); got != appContentSecurityPolicy {
		t.Fatalf("expected app CSP, got %q", got)
	}
}
