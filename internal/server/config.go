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
	defaultMaxJSONBodyBytes = 1 << 20
)

type serverConfig struct {
	PublicBaseURL        string
	CookieSecure         bool
	WebAuthnRPID         string
	WebAuthnOrigins      []string
	TrustedOrigins       map[string]struct{}
	TrustedHosts         map[string]struct{}
	EnforceTrustedHost   bool
	MaxUploadBytes       int64
	MaxInlineUploadBytes int64
	MaxJSONBodyBytes     int64
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
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	baseURL = strings.TrimRight(parsed.String(), "/")

	cookieSecure := parsed.Scheme == "https"
	if !cookieSecure && !isLocalhostHost(parsed.Hostname()) {
		log.Fatalf("server: PUBLIC_BASE_URL=%q must use https:// for non-localhost deployments", baseURL)
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

	maxJSONBodyBytes := envOrInt64("MAX_JSON_BODY_BYTES", defaultMaxJSONBodyBytes)

	return serverConfig{
		PublicBaseURL:        baseURL,
		CookieSecure:         cookieSecure,
		WebAuthnRPID:         rpID,
		WebAuthnOrigins:      origins,
		TrustedOrigins:       buildTrustedOrigins(baseURL, origins),
		TrustedHosts:         buildTrustedHosts(parsed),
		EnforceTrustedHost:   !isLocalhostHost(parsed.Hostname()),
		MaxUploadBytes:       maxUploadBytes,
		MaxInlineUploadBytes: maxInlineUploadBytes,
		MaxJSONBodyBytes:     maxJSONBodyBytes,
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

func buildTrustedOrigins(baseURL string, extraOrigins []string) map[string]struct{} {
	trusted := make(map[string]struct{})
	for _, candidate := range append([]string{baseURL}, extraOrigins...) {
		if normalized := normalizeOrigin(candidate); normalized != "" {
			trusted[normalized] = struct{}{}
		}
	}
	return trusted
}

func buildTrustedHosts(u *url.URL) map[string]struct{} {
	trusted := make(map[string]struct{})
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return trusted
	}

	if port := strings.TrimSpace(u.Port()); port != "" {
		trusted[canonicalHostPort(host, port)] = struct{}{}
		return trusted
	}

	trusted[canonicalHost(host)] = struct{}{}
	if defaultPort := defaultPortForScheme(u.Scheme); defaultPort != "" {
		trusted[canonicalHostPort(host, defaultPort)] = struct{}{}
	}
	return trusted
}

func normalizeOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return ""
	}
	port := strings.TrimSpace(u.Port())
	if port == defaultPortForScheme(scheme) {
		port = ""
	}
	if port != "" {
		return scheme + "://" + canonicalHostPort(host, port)
	}
	return scheme + "://" + canonicalHost(host)
}

func normalizeHostHeader(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	if host, port, err := net.SplitHostPort(raw); err == nil {
		return canonicalHostPort(strings.Trim(host, "[]"), port)
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		return raw
	}
	return raw
}

func canonicalHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func canonicalHostPort(host, port string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	port = strings.TrimSpace(port)
	if host == "" || port == "" {
		return ""
	}
	return net.JoinHostPort(host, port)
}

func defaultPortForScheme(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}
