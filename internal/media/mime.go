package media

import (
	"bytes"
	"net/http"
	"strings"
)

func DetectMIME(data []byte) string {
	if len(data) == 0 {
		return "application/octet-stream"
	}
	if len(data) > 512 {
		data = data[:512]
	}

	trimmed := bytes.TrimSpace(data)
	lower := bytes.ToLower(trimmed)

	switch {
	case bytes.HasPrefix(lower, []byte("<!doctype html")), bytes.HasPrefix(lower, []byte("<html")):
		return "text/html"
	case bytes.HasPrefix(lower, []byte("<?xml")) && bytes.Contains(lower, []byte("<svg")):
		return "image/svg+xml"
	case bytes.HasPrefix(lower, []byte("<svg")), bytes.Contains(lower, []byte("<svg")):
		return "image/svg+xml"
	case bytes.HasPrefix(lower, []byte("<?xml")):
		return "application/xml"
	case len(data) >= 4 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case len(data) >= 8 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47:
		return "image/png"
	case len(data) >= 6 && data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46:
		return "image/gif"
	case len(data) >= 12 && data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 &&
		data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50:
		return "image/webp"
	case len(data) >= 4 && data[0] == 0x25 && data[1] == 0x50 && data[2] == 0x44 && data[3] == 0x46:
		return "application/pdf"
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		return detectMP4MIME(data[8:12])
	case len(data) >= 4 && data[0] == 'O' && data[1] == 'g' && data[2] == 'g' && data[3] == 'S':
		return "audio/ogg"
	case len(data) >= 4 && data[0] == 'f' && data[1] == 'L' && data[2] == 'a' && data[3] == 'C':
		return "audio/flac"
	case len(data) >= 12 && data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F' &&
		data[8] == 'W' && data[9] == 'A' && data[10] == 'V' && data[11] == 'E':
		return "audio/wav"
	case len(data) >= 3 && data[0] == 'I' && data[1] == 'D' && data[2] == '3':
		return "audio/mpeg"
	case len(data) >= 2 && data[0] == 0xFF && (data[1]&0xE0) == 0xE0:
		return "audio/mpeg"
	case len(data) >= 2 && data[0] == 0x50 && data[1] == 0x4B:
		return "application/zip"
	}

	detected := http.DetectContentType(data)
	detected = strings.ToLower(strings.TrimSpace(strings.SplitN(detected, ";", 2)[0]))
	if detected == "application/octet-stream" {
		return "application/octet-stream"
	}
	return detected
}

func detectMP4MIME(brand []byte) string {
	switch string(brand) {
	case "M4A ", "M4B ", "mp41", "mp42", "isom":
		return "audio/mp4"
	default:
		return "application/mp4"
	}
}

func IsSafeInlineImage(mimeType string) bool {
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func AllowsInline(mimeType string) bool {
	return IsSafeInlineImage(mimeType) || IsAudio(mimeType)
}
