package server

import "testing"

func TestLoadServerConfigDefaultsToHTTPWithoutTLS(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("TLS_CERT_FILE", "")
	t.Setenv("TLS_KEY_FILE", "")

	cfg := loadServerConfig(":8080")
	if got := cfg.PublicBaseURL; got != "http://localhost:8080" {
		t.Fatalf("PublicBaseURL = %q, want %q", got, "http://localhost:8080")
	}
	if cfg.CookieSecure {
		t.Fatal("expected CookieSecure to be false for default HTTP config")
	}
	if cfg.TLSEnabled() {
		t.Fatal("expected TLS to be disabled by default")
	}
}

func TestLoadServerConfigDefaultsToHTTPSWhenTLSConfigured(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("TLS_CERT_FILE", "/tls/server.crt")
	t.Setenv("TLS_KEY_FILE", "/tls/server.key")

	cfg := loadServerConfig(":8443")
	if got := cfg.PublicBaseURL; got != "https://localhost:8443" {
		t.Fatalf("PublicBaseURL = %q, want %q", got, "https://localhost:8443")
	}
	if !cfg.CookieSecure {
		t.Fatal("expected CookieSecure to be true for HTTPS config")
	}
	if !cfg.TLSEnabled() {
		t.Fatal("expected TLS to be enabled when cert and key are configured")
	}
	if cfg.TLSCertFile != "/tls/server.crt" {
		t.Fatalf("TLSCertFile = %q, want %q", cfg.TLSCertFile, "/tls/server.crt")
	}
	if cfg.TLSKeyFile != "/tls/server.key" {
		t.Fatalf("TLSKeyFile = %q, want %q", cfg.TLSKeyFile, "/tls/server.key")
	}
	if len(cfg.WebAuthnOrigins) != 1 || cfg.WebAuthnOrigins[0] != "https://localhost:8443" {
		t.Fatalf("WebAuthnOrigins = %#v, want [https://localhost:8443]", cfg.WebAuthnOrigins)
	}
}

func TestDefaultPublicBaseURLFallsBackForEphemeralPort(t *testing.T) {
	if got := defaultPublicBaseURL(":0", false); got != "http://localhost:8080" {
		t.Fatalf("defaultPublicBaseURL(:0, false) = %q, want %q", got, "http://localhost:8080")
	}
	if got := defaultPublicBaseURL(":0", true); got != "https://localhost:8080" {
		t.Fatalf("defaultPublicBaseURL(:0, true) = %q, want %q", got, "https://localhost:8080")
	}
}
