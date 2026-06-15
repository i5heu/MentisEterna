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
	if got := w.Header().Get("Cross-Origin-Opener-Policy"); got != "same-origin" {
		t.Fatalf("expected same-origin COOP, got %q", got)
	}
	if got := w.Header().Get("Permissions-Policy"); got == "" {
		t.Fatal("expected permissions policy to be set")
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected no-store cache control, got %q", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); got != appContentSecurityPolicy {
		t.Fatalf("expected app CSP, got %q", got)
	}
}

func TestSecurityHeadersMiddlewareSetsHSTSWhenSecure(t *testing.T) {
	s := newTestServer(t)
	s.cfg.CookieSecure = true
	h := s.withSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	r := httptest.NewRequest(http.MethodGet, "/notes", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Header().Get("Strict-Transport-Security"); got != "max-age=63072000; includeSubDomains" {
		t.Fatalf("expected HSTS, got %q", got)
	}
}

func TestRequireTrustedRequestRejectsUntrustedHost(t *testing.T) {
	s := newTestServer(t)
	s.cfg.EnforceTrustedHost = true
	s.cfg.TrustedHosts = map[string]struct{}{"notes.example.com": {}}

	h := s.requireTrustedRequest(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	r := httptest.NewRequest(http.MethodGet, "/notes", nil)
	r.Host = "evil.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestRequireTrustedRequestRejectsCrossSiteCookieMutation(t *testing.T) {
	s := newTestServer(t)
	s.cfg.TrustedOrigins = map[string]struct{}{"https://notes.example.com": {}}

	h := s.requireTrustedRequest(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	r := httptest.NewRequest(http.MethodPost, "/notes/1/pin", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	r.AddCookie(&http.Cookie{Name: authCookieName, Value: "cookie-session"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequireTrustedRequestAllowsBearerMutationWithoutOrigin(t *testing.T) {
	s := newTestServer(t)
	s.cfg.TrustedOrigins = map[string]struct{}{"https://notes.example.com": {}}

	h := s.requireTrustedRequest(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	r := httptest.NewRequest(http.MethodPost, "/notes/1/pin", nil)
	r.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}
