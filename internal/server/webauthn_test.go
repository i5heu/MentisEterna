package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebAuthnRegisterBeginAcceptsAuthCookieAndSetsSecureSessionCookie(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "https://notes.example.com")
	s := newTestServer(t)
	token := createTestSession(t, s)

	r := httptest.NewRequest(http.MethodGet, "/webauthn/register/begin", nil)
	r.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
	w := httptest.NewRecorder()
	s.handleWebAuthnRegisterBegin(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	for _, c := range cookies {
		if c.Name == webAuthnSessionCookie {
			if !c.Secure {
				t.Fatal("expected WebAuthn session cookie to be Secure for HTTPS base URL")
			}
			return
		}
	}
	t.Fatal("expected WebAuthn session cookie to be set")
}
