package server

import (
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/i5heu/MentisEterna/internal/media"
)

func fileDisposition(mimeType, filename string) string {
	dispositionType := "attachment"
	if media.AllowsInline(mimeType) {
		dispositionType = "inline"
	}
	filename = sanitizeFilename(filename)
	if value := mime.FormatMediaType(dispositionType, map[string]string{"filename": filename}); value != "" {
		return value
	}
	return dispositionType
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\r", "_")
	name = strings.ReplaceAll(name, "\n", "_")
	name = strings.TrimSpace(name)
	if name == "" {
		return "download"
	}
	return name
}

func applyFileResponseHeaders(w http.ResponseWriter, mimeType, filename string, sizeBytes int64) {
	h := w.Header()
	h.Set("Content-Type", mimeType)
	h.Set("Content-Disposition", fileDisposition(mimeType, filename))
	h.Set("Accept-Ranges", "bytes")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", fileContentSecurityPolicy)
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	if sizeBytes >= 0 {
		h.Set("Content-Length", strconv.FormatInt(sizeBytes, 10))
	}
}

func limitUploadBody(w http.ResponseWriter, r *http.Request, maxBytes int64) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
}
