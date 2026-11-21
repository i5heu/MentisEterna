package rss

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

const (
	fetchBodyLimit    = 2 << 20 // 2 MiB response cap
	fetchUserAgent    = "MentisEternaRSS/1.0"
	fetchMaxRedirects = 5
	acceptFeed        = "application/rss+xml, application/atom+xml, application/xml, text/xml;q=0.9, */*;q=0.8"
	acceptHTML        = "text/html, application/xhtml+xml;q=0.9, */*;q=0.8"
)

// allowPrivateHosts is a test seam: tests flip it to true so they can serve
// feeds and articles from httptest listeners on 127.0.0.1. Production leaves
// it false and the SSRF guard refuses dials to private/loopback/link-local
// addresses (feeding a LAN-hosted server's own endpoints via feed URLs must
// not be possible).
var allowPrivateHosts = false

// fetchResult is the outcome of one conditional GET.
type fetchResult struct {
	NotModified  bool
	Status       int
	Body         []byte
	Etag         string
	LastModified string
}

// newFetchClient builds the fetch client. The dialer's Control hook runs
// after DNS resolution with the concrete IP, so every dial — including TLS
// and each redirect hop — is checked against private address space.
func newFetchClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			if allowPrivateHosts {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("rss: refuse dial to %s: unparseable address", address)
			}
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
				ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
				return fmt.Errorf("rss: refuse dial to private address %s (SSRF guard)", ip)
			}
			return nil
		},
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          16,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}

	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= fetchMaxRedirects {
				return fmt.Errorf("rss: stopped after %d redirects", len(via))
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("rss: redirect to non-http scheme %q refused", req.URL.Scheme)
			}
			return nil
		},
	}
}

// sharedFetchClient is the process-wide client (cookieless, pooled).
var sharedFetchClient = newFetchClient()

// fetchURL performs a conditional GET. etag/lastModified (when non-empty)
// enable 304 Not Modified handling. Accept is one of acceptFeed / acceptHTML.
// Body is capped at fetchBodyLimit (silently truncated beyond).
func fetchURL(ctx context.Context, rawURL, etag, lastModified, accept string) (*fetchResult, error) {
	trimmed := strings.TrimSpace(rawURL)
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("rss: not a valid http(s) URL: %q", rawURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trimmed, nil)
	if err != nil {
		return nil, fmt.Errorf("rss: build request: %w", err)
	}
	req.Header.Set("User-Agent", fetchUserAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	resp, err := sharedFetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10)) }()
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return &fetchResult{NotModified: true, Status: resp.StatusCode}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rss: fetch %s: status %d", trimmed, resp.StatusCode)
	}

	body, err := readAllLimited(resp.Body, fetchBodyLimit)
	if err != nil {
		return nil, fmt.Errorf("rss: read body of %s: %w", trimmed, err)
	}

	return &fetchResult{
		Status:       resp.StatusCode,
		Body:         body,
		Etag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, nil
}

// publicDialEnvOverride is a documented escape hatch for self-hosters who
// deliberately point feeds at an internal server: setting
// MENTIS_RSS_ALLOW_PRIVATE_HOSTS=1 disables the SSRF dial guard.
func init() {
	if os.Getenv("MENTIS_RSS_ALLOW_PRIVATE_HOSTS") == "1" {
		allowPrivateHosts = true
	}
}
