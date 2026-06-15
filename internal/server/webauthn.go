package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/i5heu/MentisEterna/internal/db"
)

// WebAuthnUser wraps db.User data to satisfy the webauthn.User interface.
type WebAuthnUser struct {
	ID       int64
	Username string
	db       *db.DB
}

// WebAuthnID returns the user handle as a byte slice.
func (u *WebAuthnUser) WebAuthnID() []byte {
	return db.Int64ToUserHandle(u.ID)
}

// WebAuthnName returns the username.
func (u *WebAuthnUser) WebAuthnName() string {
	return u.Username
}

// WebAuthnDisplayName returns the username (a dedicated display name can be added later).
func (u *WebAuthnUser) WebAuthnDisplayName() string {
	return u.Username
}

// WebAuthnCredentials queries the database and returns all stored credentials for this user.
func (u *WebAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	creds, err := u.db.GetWebAuthnCredentials(u.ID)
	if err != nil {
		log.Printf("webauthn: get credentials for user %d: %v", u.ID, err)
		return nil
	}
	return creds
}

const webAuthnSessionCookie = "wa_sid"

// webAuthnSessionStore is an in-memory store for WebAuthn session data.
type webAuthnSessionStore struct {
	mu       sync.Mutex
	sessions map[string]webauthnSessionEntry
}

type webauthnSessionEntry struct {
	Data    webauthn.SessionData
	Expires time.Time
}

func newWebAuthnSessionStore() *webAuthnSessionStore {
	return &webAuthnSessionStore{sessions: make(map[string]webauthnSessionEntry)}
}

func (s *webAuthnSessionStore) Set(token string, data webauthn.SessionData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = webauthnSessionEntry{Data: data, Expires: time.Now().Add(5 * time.Minute)}
}

func (s *webAuthnSessionStore) Get(token string) (webauthn.SessionData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.sessions[token]
	if !ok || time.Now().After(entry.Expires) {
		if ok {
			delete(s.sessions, token)
		}
		return webauthn.SessionData{}, false
	}
	delete(s.sessions, token) // single-use
	return entry.Data, true
}

// handleWebAuthnRegisterBegin generates a creation challenge and sends it to the client.
// GET /webauthn/register/begin
func (s *Server) handleWebAuthnRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	username := s.authenticateSession(r)
	if username == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	userID, err := s.db.GetUserID(username)
	if err != nil {
		writeErr(w, err)
		return
	}

	user := &WebAuthnUser{ID: userID, Username: username, db: s.db}

	creation, sessionData, err := s.webauthn.BeginRegistration(user)
	if err != nil {
		log.Printf("webauthn: begin registration: %v", err)
		writeErr(w, err)
		return
	}

	token := generateWebAuthnToken()
	s.sessionStore.Set(token, *sessionData)
	s.setWebAuthnSessionCookie(w, token)

	writeJSON(w, http.StatusOK, creation.Response)
}

// handleWebAuthnRegisterFinish validates the creation response and persists the credential.
// POST /webauthn/register/finish
func (s *Server) handleWebAuthnRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.maxJSONBodyBytes())

	username := s.authenticateSession(r)
	if username == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	cookie, err := r.Cookie(webAuthnSessionCookie)
	if err != nil {
		http.Error(w, "session not found", http.StatusBadRequest)
		return
	}

	sessionData, ok := s.sessionStore.Get(cookie.Value)
	if !ok {
		http.Error(w, "session expired or invalid", http.StatusBadRequest)
		return
	}

	userID, err := s.db.GetUserID(username)
	if err != nil {
		writeErr(w, err)
		return
	}

	user := &WebAuthnUser{ID: userID, Username: username, db: s.db}

	credential, err := s.webauthn.FinishRegistration(user, sessionData, r)
	if err != nil {
		log.Printf("webauthn: finish registration: %v", err)
		http.Error(w, "registration failed", http.StatusBadRequest)
		return
	}

	if err := s.db.InsertWebAuthnCredential(userID, credential); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"status": "ok",
	})
}

// handleWebAuthnLoginBegin generates an assertion challenge for discoverable login.
// GET /webauthn/login/begin
func (s *Server) handleWebAuthnLoginBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	assertion, sessionData, err := s.webauthn.BeginDiscoverableLogin()
	if err != nil {
		log.Printf("webauthn: begin discoverable login: %v", err)
		writeErr(w, err)
		return
	}

	token := generateWebAuthnToken()
	s.sessionStore.Set(token, *sessionData)
	s.setWebAuthnSessionCookie(w, token)

	writeJSON(w, http.StatusOK, assertion.Response)
}

// handleWebAuthnLoginFinish validates the assertion and issues a session token.
// POST /webauthn/login/finish
func (s *Server) handleWebAuthnLoginFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.maxJSONBodyBytes())

	cookie, err := r.Cookie(webAuthnSessionCookie)
	if err != nil {
		http.Error(w, "session not found", http.StatusBadRequest)
		return
	}

	sessionData, ok := s.sessionStore.Get(cookie.Value)
	if !ok {
		http.Error(w, "session expired or invalid", http.StatusBadRequest)
		return
	}

	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		uid, err := db.UserHandleToInt64(userHandle)
		if err != nil {
			return nil, err
		}
		username, err := s.db.GetUsernameByID(uid)
		if err != nil {
			return nil, err
		}
		return &WebAuthnUser{ID: uid, Username: username, db: s.db}, nil
	}

	user, credential, err := s.webauthn.FinishPasskeyLogin(handler, sessionData, r)
	if err != nil {
		log.Printf("webauthn: finish passkey login: %v", err)
		http.Error(w, "login failed", http.StatusUnauthorized)
		return
	}

	wu, ok := user.(*WebAuthnUser)
	if !ok {
		writeErr(w, errors.New("internal: unexpected user type"))
		return
	}

	if err := s.db.UpdateWebAuthnSignCount(credential.ID, credential.Authenticator.SignCount, credential.Flags); err != nil {
		log.Printf("webauthn: update sign count: %v", err)
	}

	token, expiresAt, err := s.db.CreateSession(wu.Username)
	if err != nil {
		writeErr(w, err)
		return
	}

	s.setAuthCookie(w, token, expiresAt)
	writeJSON(w, http.StatusOK, map[string]string{
		"token":      token,
		"expires_at": expiresAt.Format("2006-01-02T15:04:05Z"),
	})
}

func (s *Server) setWebAuthnSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     webAuthnSessionCookie,
		Value:    token,
		Path:     "/webauthn/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   300,
	})
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(auth) >= len(prefix) && auth[:len(prefix)] == prefix {
		return auth[len(prefix):]
	}
	return ""
}

func generateWebAuthnToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		log.Printf("webauthn: generate token: %v", err)
		return ""
	}
	return hex.EncodeToString(raw)
}
