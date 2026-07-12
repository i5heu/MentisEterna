package server

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var inlineScriptTagPattern = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)

type spaHandler struct {
	root string
	fs   http.Handler
}

func newSPAHandler(root string) http.Handler {
	return &spaHandler{
		root: root,
		fs:   http.FileServer(http.Dir(root)),
	}
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(h.root, filepath.Clean("/"+r.URL.Path))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		if strings.EqualFold(filepath.Ext(path), ".html") {
			h.serveHTMLFile(w, r, path)
			return
		}
		h.fs.ServeHTTP(w, r)
		return
	}
	h.serveHTMLFile(w, r, filepath.Join(h.root, "index.html"))
}

func (h *spaHandler) serveHTMLFile(w http.ResponseWriter, r *http.Request, path string) {
	content, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body, nonce, changed := applyInlineScriptNonce(string(content))
	if changed {
		w.Header().Set("Content-Security-Policy", appContentSecurityPolicyWithNonce(nonce))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func applyInlineScriptNonce(html string) (string, string, bool) {
	changed := false
	nonce := ""
	out := inlineScriptTagPattern.ReplaceAllStringFunc(html, func(tag string) string {
		lower := strings.ToLower(tag)
		if strings.Contains(lower, "src=") || strings.Contains(lower, " nonce=") {
			return tag
		}
		if nonce == "" {
			nonce = generateCSPNonce()
			if nonce == "" {
				return tag
			}
		}
		changed = true
		return strings.Replace(tag, "<script", `<script nonce="`+nonce+`"`, 1)
	})
	return out, nonce, changed
}

func appContentSecurityPolicyWithNonce(nonce string) string {
	if strings.TrimSpace(nonce) == "" {
		return appContentSecurityPolicy
	}
	return strings.Replace(appContentSecurityPolicy, "script-src 'self'", "script-src 'self' 'nonce-"+nonce+"'", 1)
}

func generateCSPNonce() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return base64.RawStdEncoding.EncodeToString(buf)
}
