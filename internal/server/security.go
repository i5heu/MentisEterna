package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	appContentSecurityPolicy  = "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; object-src 'none'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; font-src 'self' data:; form-action 'self'"
	fileContentSecurityPolicy = "default-src 'none'; frame-ancestors 'none'; sandbox"
)

func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if h.Get("X-Content-Type-Options") == "" {
			h.Set("X-Content-Type-Options", "nosniff")
		}
		if h.Get("X-Frame-Options") == "" {
			h.Set("X-Frame-Options", "DENY")
		}
		if h.Get("Referrer-Policy") == "" {
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		}
		if h.Get("Cross-Origin-Opener-Policy") == "" {
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
		}
		if h.Get("Permissions-Policy") == "" {
			h.Set("Permissions-Policy", "accelerometer=(), autoplay=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
		}
		if s.cfg.CookieSecure && h.Get("Strict-Transport-Security") == "" {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		if isAPIPath(r.URL.Path) && h.Get("Cache-Control") == "" {
			h.Set("Cache-Control", "no-store")
		}
		if !strings.HasPrefix(r.URL.Path, "/file/") && h.Get("Content-Security-Policy") == "" {
			h.Set("Content-Security-Policy", appContentSecurityPolicy)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireTrustedRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.EnforceTrustedHost {
			host := normalizeHostHeader(r.Host)
			if _, ok := s.cfg.TrustedHosts[host]; !ok {
				http.Error(w, "invalid host", http.StatusBadRequest)
				return
			}
		}

		if !s.requiresTrustedOrigin(r) {
			next.ServeHTTP(w, r)
			return
		}

		if err := s.validateTrustedOrigin(r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) requiresTrustedOrigin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return false
	}
	if isLoginRoute(r.URL.Path) {
		return false
	}
	if !(isAPIPath(r.URL.Path) || strings.HasPrefix(r.URL.Path, "/webauthn/")) {
		return false
	}
	if usesAuthCookie(r) || usesWebAuthnSessionCookie(r) {
		return true
	}
	if extractBearerToken(r) != "" {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	referer := strings.TrimSpace(r.Header.Get("Referer"))
	return origin != "" || referer != ""
}

func (s *Server) validateTrustedOrigin(r *http.Request) error {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		normalized := normalizeOrigin(origin)
		if normalized == "" {
			return errors.New("untrusted origin")
		}
		if _, ok := s.cfg.TrustedOrigins[normalized]; !ok {
			return fmt.Errorf("origin %q is not allowed", normalized)
		}
		return nil
	}

	referer := strings.TrimSpace(r.Header.Get("Referer"))
	if referer == "" {
		return errors.New("origin or referer required")
	}
	refURL, err := url.Parse(referer)
	if err != nil {
		return errors.New("invalid referer")
	}
	normalized := normalizeOrigin(refURL.Scheme + "://" + refURL.Host)
	if normalized == "" {
		return errors.New("invalid referer")
	}
	if _, ok := s.cfg.TrustedOrigins[normalized]; !ok {
		return fmt.Errorf("referer %q is not allowed", normalized)
	}
	return nil
}

func usesAuthCookie(r *http.Request) bool {
	_, err := r.Cookie(authCookieName)
	return err == nil
}

func usesWebAuthnSessionCookie(r *http.Request) bool {
	_, err := r.Cookie(webAuthnSessionCookie)
	return err == nil
}

func isLoginRoute(path string) bool {
	return path == "/login" || strings.HasPrefix(path, "/webauthn/login/")
}

func (s *Server) decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxJSONBodyBytes())
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	if err := ensureJSONEOF(dec); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	return true
}

func (s *Server) decodeOptionalJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxJSONBodyBytes())
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	if err := ensureJSONEOF(dec); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	return true
}

func (s *Server) maxJSONBodyBytes() int64 {
	if s.cfg.MaxJSONBodyBytes > 0 {
		return s.cfg.MaxJSONBodyBytes
	}
	return defaultMaxJSONBodyBytes
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("extra JSON value")
		}
		return err
	}
	return nil
}
