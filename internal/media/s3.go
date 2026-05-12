package media

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ReplicaStore defines the interface for S3-compatible object storage.
type ReplicaStore interface {
	Put(ctx context.Context, endpoint EndpointConfig, key string, src io.Reader, size int64) (etag string, err error)
	Get(ctx context.Context, endpoint EndpointConfig, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, endpoint EndpointConfig, key string) error
	List(ctx context.Context, endpoint EndpointConfig, prefix string) ([]string, error)
}

// S3Store implements ReplicaStore using SigV4-signed HTTP requests.
// Works with AWS S3 and any S3-compatible endpoint (MinIO, Backblaze B2, etc.).
type S3Store struct {
	client *http.Client
}

// NewS3Store creates a new S3Store with a default HTTP client.
func NewS3Store() *S3Store {
	return &S3Store{client: &http.Client{Timeout: 30 * time.Second}}
}

// SetClient allows overriding the HTTP client (useful for tests).
func (s *S3Store) SetClient(c *http.Client) { s.client = c }

func (s *S3Store) objectURL(ep EndpointConfig, key string) string {
	base := strings.TrimRight(ep.Endpoint, "/")
	if ep.ForcePathStyle {
		return fmt.Sprintf("%s/%s/%s", base, ep.Bucket, key)
	}
	return fmt.Sprintf("%s/%s/%s", base, ep.Bucket, key)
}

// Put uploads an object. Returns the ETag (without quotes).
func (s *S3Store) Put(ctx context.Context, ep EndpointConfig, key string, src io.Reader, size int64) (string, error) {
	body, err := io.ReadAll(src)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	u := s.objectURL(ep, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.ContentLength = int64(len(body))

	if err := s.signRequest(req, ep, body); err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("s3 put: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("s3 put: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return strings.Trim(resp.Header.Get("ETag"), "\""), nil
}

// Get downloads an object. Caller must close the returned ReadCloser.
func (s *S3Store) Get(ctx context.Context, ep EndpointConfig, key string) (io.ReadCloser, error) {
	u := s.objectURL(ep, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	if err := s.signRequest(req, ep, nil); err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3 get: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, fmt.Errorf("s3 get: not found")
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("s3 get: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp.Body, nil
}

// Delete removes an object. Returns nil if the object does not exist.
func (s *S3Store) Delete(ctx context.Context, ep EndpointConfig, key string) error {
	u := s.objectURL(ep, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}

	if err := s.signRequest(req, ep, nil); err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("s3 delete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("s3 delete: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// listObjectsV2Response is a minimal XML struct for parsing S3 ListObjectsV2 responses.
type listObjectsV2Response struct {
	XMLName     xml.Name `xml:"ListBucketResult"`
	Contents    []s3Object
	IsTruncated bool
	NextToken   string `xml:"NextContinuationToken"`
}

type s3Object struct {
	Key string
}

// List returns all object keys under the given prefix using the ListObjectsV2 API.
// Handles pagination automatically via continuation tokens.
// Returns an empty slice (not error) if no objects exist under the prefix.
func (s *S3Store) List(ctx context.Context, ep EndpointConfig, prefix string) ([]string, error) {
	var allKeys []string
	var continuationToken string

	for {
		u := s.objectURL(ep, "")
		parsed, err := url.Parse(u)
		if err != nil {
			return nil, fmt.Errorf("parse base url: %w", err)
		}

		// Build RawQuery with unencoded values. canonicalQueryString does
		// the single encoding pass. Using url.Values.Encode() would
		// double-encode because it encodes "/" as "%2F", and then
		// canonicalQueryString re-encodes "%" to "%25".
		var qparts []string
		qparts = append(qparts, "list-type=2")
		qparts = append(qparts, "prefix="+prefix)
		if continuationToken != "" {
			qparts = append(qparts, "continuation-token="+continuationToken)
		}
		parsed.RawQuery = strings.Join(qparts, "&")

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return nil, err
		}

		if err := s.signRequest(req, ep, nil); err != nil {
			return nil, fmt.Errorf("sign list: %w", err)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("s3 list: %w", err)
		}

		if resp.StatusCode >= 300 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("s3 list: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read list body: %w", err)
		}

		var result listObjectsV2Response
		if err := xml.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse list xml: %w (body=%s)", err, string(body))
		}

		for _, obj := range result.Contents {
			if obj.Key != "" {
				allKeys = append(allKeys, obj.Key)
			}
		}

		if !result.IsTruncated {
			break
		}
		continuationToken = result.NextToken
	}

	return allKeys, nil
}

// signRequest adds AWS Signature V4 authentication headers to the request.
func (s *S3Store) signRequest(req *http.Request, ep EndpointConfig, body []byte) error {
	t := time.Now().UTC()
	region := ep.Region
	if region == "" {
		region = "us-east-1"
	}
	service := "s3"

	// Task 1: Create a canonical request
	canonicalHeaders, signedHeaders := s.buildCanonicalHeaders(req, t, ep, body)
	payloadHash := sha256Hex(body)
	canonicalRequest := strings.Join([]string{
		req.Method,
		s.urlEncodePath(req.URL.Path),
		s.canonicalQueryString(req.URL),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// Task 2: Create a string to sign
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request",
		t.Format("20060102"), region, service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		t.Format("20060102T150405Z"),
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	// Task 3: Calculate the signature
	signingKey := s.deriveSigningKey(ep.SecretAccessKey, t.Format("20060102"), region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// Task 4: Add Authorization header
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		ep.AccessKeyID, credentialScope, signedHeaders, signature,
	))

	return nil
}

func (s *S3Store) buildCanonicalHeaders(req *http.Request, t time.Time, ep EndpointConfig, body []byte) (string, string) {
	// Set required headers
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", t.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-SHA256", sha256Hex(body))

	// Collect and sort header names
	var names []string
	for name := range req.Header {
		names = append(names, strings.ToLower(name))
	}
	sort.Strings(names)

	var lines []string
	for _, name := range names {
		lines = append(lines, name+":"+strings.TrimSpace(req.Header.Get(name))+"\n")
	}
	return strings.Join(lines, ""), strings.Join(names, ";")
}

func (s *S3Store) canonicalQueryString(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	// URL-encode query parameters and sort by key
	var params []string
	for _, p := range strings.Split(u.RawQuery, "&") {
		kv := strings.SplitN(p, "=", 2)
		k := urlEncode(kv[0])
		v := ""
		if len(kv) == 2 {
			v = urlEncode(kv[1])
		}
		params = append(params, k+"="+v)
	}
	sort.Strings(params)
	return strings.Join(params, "&")
}

func (s *S3Store) urlEncodePath(path string) string {
	// Encode the path segment by segment
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, seg := range segments {
		segments[i] = urlEncode(seg)
	}
	return "/" + strings.Join(segments, "/")
}

func urlEncode(s string) string {
	// AWS SigV4 requires specific URL encoding: encode everything except unreserved chars
	var buf strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' || c == '~' || c == '.' {
			buf.WriteByte(c)
		} else {
			buf.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return buf.String()
}

func (s *S3Store) deriveSigningKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
