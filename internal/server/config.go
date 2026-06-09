package server

import (
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	defaultPublicBaseURL    = "http://localhost:8080"
	defaultMaxUploadBytes   = 64 << 20
	defaultMaxInlineUploads = 64 << 20
)

type serverConfig struct {
	PublicBaseURL        string
	CookieSecure         bool
	WebAuthnRPID         string
	WebAuthnOrigins      []string
	MaxUploadBytes       int64
	MaxInlineUploadBytes int64
}

func loadServerConfig() serverConfig {
	baseURL := strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultPublicBaseURL
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		log.Printf("server: invalid PUBLIC_BASE_URL=%q, falling back to %s", baseURL, defaultPublicBaseURL)
		parsed, _ = url.Parse(defaultPublicBaseURL)
		baseURL = defaultPublicBaseURL
	}
	baseURL = strings.TrimRight(parsed.String(), "/")

	cookieSecure := parsed.Scheme == "https"
	if !cookieSecure && !isLocalhostHost(parsed.Hostname()) {
		log.Printf("server: PUBLIC_BASE_URL=%q is not HTTPS; cookies will not be Secure", baseURL)
	}

	rpID := strings.TrimSpace(os.Getenv("WEBAUTHN_RPID"))
	if rpID == "" {
		rpID = parsed.Hostname()
	}
	if rpID == "" {
		rpID = "localhost"
	}

	originsRaw := strings.TrimSpace(os.Getenv("WEBAUTHN_RP_ORIGINS"))
	origins := splitCommaList(originsRaw)
	if len(origins) == 0 {
		origins = []string{baseURL}
	}

	maxUploadBytes := envOrInt64("MAX_UPLOAD_BYTES", defaultMaxUploadBytes)
	maxInlineUploadBytes := envOrInt64("MAX_INLINE_UPLOAD_BYTES", defaultMaxInlineUploads)
	if maxInlineUploadBytes > maxUploadBytes {
		maxInlineUploadBytes = maxUploadBytes
	}

	return serverConfig{
		PublicBaseURL:        baseURL,
		CookieSecure:         cookieSecure,
		WebAuthnRPID:         rpID,
		WebAuthnOrigins:      origins,
		MaxUploadBytes:       maxUploadBytes,
		MaxInlineUploadBytes: maxInlineUploadBytes,
	}
}

func envOrInt64(key string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 {
		log.Printf("server: invalid %s=%q; using default %d", key, v, def)
		return def
	}
	return n
}

func splitCommaList(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func isLocalhostHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
