package server

import (
	"net/http"
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
		if !strings.HasPrefix(r.URL.Path, "/file/") && h.Get("Content-Security-Policy") == "" {
			h.Set("Content-Security-Policy", appContentSecurityPolicy)
		}
		next.ServeHTTP(w, r)
	})
}
