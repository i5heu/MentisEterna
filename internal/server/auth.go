package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/i5heu/MentisEterna/internal/db"
)

const pwChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// authCookieName is the name of the cookie used for browser-based auth
// (so that embedded resource requests like <img> can be authenticated).
const authCookieName = "auth_token"

func (s *Server) setAuthCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
		Expires:  expiresAt,
	})
}

func (s *Server) clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
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
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || in.Password == "" {
		http.Error(w, "username and password required", http.StatusBadRequest)
		return
	}

	usernameKey := throttleKeyUsername(strings.ToLower(in.Username))
	ipKey := throttleKeyIP(clientIPFromRequest(r))
	if wait, allowed := s.loginThrottle.allow(usernameKey, ipKey); !allowed {
		w.Header().Set("Retry-After", formatRetryAfter(wait))
		http.Error(w, "invalid credentials", http.StatusTooManyRequests)
		return
	}

	ok, err := s.db.CheckPassword(in.Username, in.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		s.loginThrottle.recordFailure(usernameKey, ipKey)
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	s.loginThrottle.recordSuccess(usernameKey, ipKey)
	token, expiresAt, err := s.db.CreateSession(in.Username)
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

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if token := sessionTokenFromRequest(r); token != "" {
		if err := s.db.DeleteSession(token); err != nil && !errors.Is(err, db.ErrNotFound) {
			writeErr(w, err)
			return
		}
	}
	s.clearAuthCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username, _, err := s.sessionUsername(r)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"username":      username,
	})
}

func isAPIPath(p string) bool {
	return p == "/health" || p == "/session" || p == "/logout" || p == "/notes" || strings.HasPrefix(p, "/notes/") ||
		p == "/note-types" ||
		p == "/jobs" || strings.HasPrefix(p, "/jobs/") ||
		strings.HasPrefix(p, "/webauthn/") || strings.HasPrefix(p, "/file/") ||
		strings.HasPrefix(p, "/files/") ||
		strings.HasPrefix(p, "/system/") || strings.HasPrefix(p, "/backup/") || strings.HasPrefix(p, "/maintenance/") ||
		p == "/tags" || strings.HasPrefix(p, "/tags?")
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// These endpoints handle their own auth (or are public for login/bootstrap).
		if r.URL.Path == "/login" || r.URL.Path == "/logout" || r.URL.Path == "/session" || r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/webauthn/") || !isAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		_, _, err := s.sessionUsername(r)
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

func sessionTokenFromRequest(r *http.Request) string {
	token := extractBearerToken(r)
	if token != "" {
		return token
	}
	if c, err := r.Cookie(authCookieName); err == nil {
		return c.Value
	}
	return ""
}

func (s *Server) sessionUsername(r *http.Request) (string, string, error) {
	token := sessionTokenFromRequest(r)
	if token == "" {
		return "", "", db.ErrNotFound
	}
	username, err := s.db.ValidateSession(token)
	if err != nil {
		return "", token, err
	}
	return username, token, nil
}

func (s *Server) authenticateSession(r *http.Request) string {
	username, _, err := s.sessionUsername(r)
	if errors.Is(err, db.ErrNotFound) {
		return ""
	}
	if err != nil {
		log.Printf("auth session: %v", err)
		return ""
	}
	return username
}

func clientIPFromRequest(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
