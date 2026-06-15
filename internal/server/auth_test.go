package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/i5heu/MentisEterna/internal/db"
)

func TestGeneratePasswordLength(t *testing.T) {
	for _, n := range []int{1, 10, 100} {
		pw, err := generatePassword(n)
		if err != nil {
			t.Fatalf("generatePassword(%d): %v", n, err)
		}
		if len(pw) != n {
			t.Errorf("generatePassword(%d) returned %d chars", n, len(pw))
		}
	}
}

func TestGeneratePasswordChars(t *testing.T) {
	pw, err := generatePassword(200)
	if err != nil {
		t.Fatalf("generatePassword: %v", err)
	}
	alphabet := pwChars
	for _, c := range pw {
		if !strings.ContainsRune(alphabet, c) {
			t.Errorf("invalid char %q in generated password", c)
		}
	}
}

func TestGeneratePasswordUnique(t *testing.T) {
	p1, err := generatePassword(32)
	if err != nil {
		t.Fatalf("generatePassword 1: %v", err)
	}
	p2, err := generatePassword(32)
	if err != nil {
		t.Fatalf("generatePassword 2: %v", err)
	}
	if p1 == p2 {
		t.Error("expected different passwords on consecutive calls")
	}
}

func TestIsAPIPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/health", true},
		{"/notes", true},
		{"/notes/1", true},
		{"/notes/1/history", true},
		{"/note-types", true},
		{"/tags", true},
		{"/tags?q=foo", true},
		{"/file/1/42", true},
		{"/file/999/888", true},
		{"/login", false},
		{"/", false},
		{"/static/app.js", false},
		{"/notesX", false},
		{"", false},
	}
	for _, tc := range cases {
		got := isAPIPath(tc.path)
		if got != tc.want {
			t.Errorf("isAPIPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestHandleLoginSuccess(t *testing.T) {
	s := newTestServer(t)
	if err := s.db.SetAdminPassword("pass123"); err != nil {
		t.Fatalf("set password: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":"pass123"}`))
	w := httptest.NewRecorder()
	s.handleLogin(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["token"] == "" {
		t.Error("expected token in response")
	}
	if resp["expires_at"] == "" {
		t.Error("expected expires_at in response")
	}
}

func TestHandleLoginWrongPassword(t *testing.T) {
	s := newTestServer(t)
	if err := s.db.SetAdminPassword("pass123"); err != nil {
		t.Fatalf("set password: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
	w := httptest.NewRecorder()
	s.handleLogin(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleLoginEmptyUsername(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"","password":"pass"}`))
	w := httptest.NewRecorder()
	s.handleLogin(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleLoginEmptyPassword(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":""}`))
	w := httptest.NewRecorder()
	s.handleLogin(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleLoginInvalidJSON(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("notjson"))
	w := httptest.NewRecorder()
	s.handleLogin(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleLoginWrongMethod(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()
	s.handleLogin(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestRequireAuthProtectsWrappedArbitraryPath(t *testing.T) {
	s := newTestServer(t)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := s.requireAuth(inner)

	r := httptest.NewRequest(http.MethodGet, "/exports/full", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrapped arbitrary path: expected 401, got %d", w.Code)
	}
}

func TestRequireAuthMissingToken(t *testing.T) {
	s := newTestServer(t)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := s.requireAuth(inner)

	r := httptest.NewRequest(http.MethodGet, "/notes", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("/notes without token: expected 401, got %d", w.Code)
	}
}

func TestRequireAuthValidToken(t *testing.T) {
	s := newTestServer(t)
	token := createTestSession(t, s)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := s.requireAuth(inner)

	r := httptest.NewRequest(http.MethodGet, "/notes", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("/notes with valid token: expected 200, got %d", w.Code)
	}
}

func TestRequireAuthInvalidToken(t *testing.T) {
	s := newTestServer(t)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := s.requireAuth(inner)

	r := httptest.NewRequest(http.MethodGet, "/notes", nil)
	r.Header.Set("Authorization", "Bearer invalidtoken")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("/notes with invalid token: expected 401, got %d", w.Code)
	}
}

func TestRequireAuthValidCookie(t *testing.T) {
	s := newTestServer(t)
	token := createTestSession(t, s)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := s.requireAuth(inner)

	r := httptest.NewRequest(http.MethodGet, "/notes", nil)
	r.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("/notes with valid cookie: expected 200, got %d", w.Code)
	}
}

func TestHandleSessionWithCookie(t *testing.T) {
	s := newTestServer(t)
	token := createTestSession(t, s)

	r := httptest.NewRequest(http.MethodGet, "/session", nil)
	r.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
	w := httptest.NewRecorder()
	s.handleSession(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	if resp["username"] != "admin" {
		t.Fatalf("expected username admin, got %#v", resp["username"])
	}
}

func TestHandleLogoutRevokesSessionAndClearsCookie(t *testing.T) {
	s := newTestServer(t)
	token := createTestSession(t, s)

	r := httptest.NewRequest(http.MethodPost, "/logout", nil)
	r.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
	w := httptest.NewRecorder()
	s.handleLogout(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if _, err := s.db.ValidateSession(token); err != db.ErrNotFound {
		t.Fatalf("expected revoked session, got %v", err)
	}
	cookies := w.Result().Cookies()
	var cleared *http.Cookie
	for _, c := range cookies {
		if c.Name == authCookieName {
			cleared = c
			break
		}
	}
	if cleared == nil || cleared.MaxAge != -1 {
		t.Fatalf("expected cleared auth cookie, got %#v", cleared)
	}
}

func TestHandleLoginAppliesThrottleAfterRepeatedFailures(t *testing.T) {
	s := newTestServer(t)
	if err := s.db.SetAdminPassword("pass123"); err != nil {
		t.Fatalf("set password: %v", err)
	}

	now := time.Now()
	s.loginThrottle.now = func() time.Time { return now }

	for i := 0; i < 4; i++ {
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "203.0.113.10:12345"
		w := httptest.NewRecorder()
		s.handleLogin(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, w.Code)
		}
	}

	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "203.0.113.10:12345"
	w := httptest.NewRecorder()
	s.handleLogin(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after repeated failures, got %d", w.Code)
	}

	now = now.Add(2 * time.Second)
	okReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":"pass123"}`))
	okReq.Header.Set("Content-Type", "application/json")
	okReq.RemoteAddr = "203.0.113.10:12345"
	okW := httptest.NewRecorder()
	s.handleLogin(okW, okReq)
	if okW.Code != http.StatusOK {
		t.Fatalf("expected throttle to clear after backoff, got %d: %s", okW.Code, okW.Body.String())
	}
}

func TestHandleLoginSetsSecureCookieWhenConfiguredForHTTPS(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "https://notes.example.com")
	s := newTestServer(t)
	if err := s.db.SetAdminPassword("pass123"); err != nil {
		t.Fatalf("set password: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":"pass123"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleLogin(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	cookies := w.Result().Cookies()
	for _, c := range cookies {
		if c.Name == authCookieName {
			if !c.Secure {
				t.Fatal("expected auth cookie to be Secure for HTTPS base URL")
			}
			return
		}
	}
	t.Fatal("expected auth cookie to be set")
}
