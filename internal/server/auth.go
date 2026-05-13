package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/i5heu/MentisEterna/internal/db"
)

const pwChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// authCookieName is the name of the cookie used for browser-based auth
// (so that embedded resource requests like <img> can be authenticated).
const authCookieName = "auth_token"

// setAuthCookie sets the auth_token cookie on the response.
func setAuthCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  expiresAt,
	})
}

func initAdminPassword(d *db.DB) {
	has, err := d.HasAdminPassword()
	if err != nil {
		log.Fatalf("auth: check admin password: %v", err)
	}
	if has {
		return
	}

	pw, err := generatePassword(100)
	if err != nil {
		log.Fatalf("auth: generate password: %v", err)
	}
	if err := d.SetAdminPassword(pw); err != nil {
		log.Fatalf("auth: set admin password: %v", err)
	}

	fmt.Printf("\n*** Admin password (shown only once): %s ***\n\n", pw)
}

func generatePassword(n int) (string, error) {
	alphabet := []byte(pwChars)
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[idx.Int64()]
	}
	return string(b), nil
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Username) == "" || in.Password == "" {
		http.Error(w, "username and password required", http.StatusBadRequest)
		return
	}

	ok, err := s.db.CheckPassword(in.Username, in.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	token, expiresAt, err := s.db.CreateSession(in.Username)
	if err != nil {
		writeErr(w, err)
		return
	}
	setAuthCookie(w, token, expiresAt)
	writeJSON(w, http.StatusOK, map[string]string{
		"token":      token,
		"expires_at": expiresAt.Format("2006-01-02T15:04:05Z"),
	})
}

func isAPIPath(p string) bool {
	return p == "/health" || p == "/notes" || strings.HasPrefix(p, "/notes/") ||
		p == "/note-types" ||
		p == "/jobs" || strings.HasPrefix(p, "/jobs/") ||
		strings.HasPrefix(p, "/webauthn/") || strings.HasPrefix(p, "/file/") ||
		strings.HasPrefix(p, "/files/") ||
		p == "/tags" || strings.HasPrefix(p, "/tags?")
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// WebAuthn endpoints handle their own auth (or are public for login).
		if r.URL.Path == "/login" || r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/webauthn/") || !isAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		token := extractBearerToken(r)
		if token == "" {
			// Fall back to cookie for browser-embedded requests (img, video, etc.)
			if c, err := r.Cookie(authCookieName); err == nil {
				token = c.Value
			}
		}
		if token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		_, err := s.db.ValidateSession(token)
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err != nil {
			writeErr(w, err)
			return
		}

		next.ServeHTTP(w, r)
	})
}
