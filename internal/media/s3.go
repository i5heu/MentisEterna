package media

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// S3ObjectInfo holds key and size for a listed S3 object.
type S3ObjectInfo struct {
	Key  string
	Size int64
}

// ReplicaStore defines the interface for S3-compatible object storage.
type ReplicaStore interface {
	Put(ctx context.Context, endpoint EndpointConfig, key string, src io.Reader, size int64) (etag string, err error)
	Get(ctx context.Context, endpoint EndpointConfig, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, endpoint EndpointConfig, key string) error
	List(ctx context.Context, endpoint EndpointConfig, prefix string) ([]string, error)
	ListObjects(ctx context.Context, endpoint EndpointConfig, prefix string) ([]S3ObjectInfo, error)
}

// S3Store implements ReplicaStore using SigV4-signed HTTP requests.
// Works with AWS S3 and any S3-compatible endpoint (MinIO, Backblaze B2, etc.).
type S3Store struct {
	client *http.Client
}

// NewS3Store creates a new S3Store with a default HTTP client.
// The 10-minute timeout accommodates large file uploads over slow connections.
func NewS3Store() *S3Store {
	return &S3Store{client: &http.Client{Timeout: 10 * time.Minute}}
}

// SetClient allows overriding the HTTP client (useful for tests).
func (s *S3Store) SetClient(c *http.Client) { s.client = c }

func (s *S3Store) objectURL(ep EndpointConfig, key string) string {
	base := strings.TrimRight(ep.Endpoint, "/")
	if ep.ForcePathStyle {
		return fmt.Sprintf("%s/%s/%s", base, ep.Bucket, key)
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		return fmt.Sprintf("%s/%s/%s", base, ep.Bucket, key)
	}
	parsed.Host = ep.Bucket + "." + parsed.Host
	parsed.Path = "/" + strings.TrimLeft(key, "/")
	return parsed.String()
}

// Put uploads an object. Returns the ETag (without quotes).
func (s *S3Store) Put(ctx context.Context, ep EndpointConfig, key string, src io.Reader, size int64) (string, error) {
	payload, payloadHash, cleanup, err := preparePayload(src)
	if err != nil {
		return "", fmt.Errorf("prepare payload: %w", err)
	}
	defer cleanup()

	u := s.objectURL(ep, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, payload)
	if err != nil {
		return "", err
	}
	req.ContentLength = size

	if err := s.signRequest(req, ep, payloadHash); err != nil {
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

	if err := s.signRequest(req, ep, sha256Hex(nil)); err != nil {
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

	if err := s.signRequest(req, ep, sha256Hex(nil)); err != nil {
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
	Key  string
	Size int64
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

		if err := s.signRequest(req, ep, sha256Hex(nil)); err != nil {
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

// ListObjects returns all objects (key + size) under the given prefix.
// Handles pagination automatically via continuation tokens.
func (s *S3Store) ListObjects(ctx context.Context, ep EndpointConfig, prefix string) ([]S3ObjectInfo, error) {
	var allObjects []S3ObjectInfo
	var continuationToken string

	for {
		u := s.objectURL(ep, "")
		parsed, err := url.Parse(u)
		if err != nil {
			return nil, fmt.Errorf("parse base url: %w", err)
		}

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

		if err := s.signRequest(req, ep, sha256Hex(nil)); err != nil {
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
				allObjects = append(allObjects, S3ObjectInfo{Key: obj.Key, Size: obj.Size})
			}
		}

		if !result.IsTruncated {
			break
		}
		continuationToken = result.NextToken
	}

	return allObjects, nil
}

// signRequest adds AWS Signature V4 authentication headers to the request.
func (s *S3Store) signRequest(req *http.Request, ep EndpointConfig, payloadHash string) error {
	t := time.Now().UTC()
	region := ep.Region
	if region == "" {
		region = "us-east-1"
	}
	service := "s3"

	// Task 1: Create a canonical request
	canonicalHeaders, signedHeaders := s.buildCanonicalHeaders(req, t, payloadHash)
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

func (s *S3Store) buildCanonicalHeaders(req *http.Request, t time.Time, payloadHash string) (string, string) {
	// Set required headers
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", t.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-SHA256", payloadHash)

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

func preparePayload(src io.Reader) (io.ReadSeeker, string, func(), error) {
	if src == nil {
		empty := strings.NewReader("")
		return empty, sha256Hex(nil), func() {}, nil
	}
	if seeker, ok := src.(io.ReadSeeker); ok {
		hash, err := hashReadSeeker(seeker)
		if err != nil {
			return nil, "", nil, err
		}
		return seeker, hash, func() {}, nil
	}

	tmpFile, err := os.CreateTemp("", "mentis-s3-put-*")
	if err != nil {
		return nil, "", nil, err
	}
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
	}

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmpFile, hasher), src); err != nil {
		cleanup()
		return nil, "", nil, err
	}
	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, "", nil, err
	}
	return tmpFile, hex.EncodeToString(hasher.Sum(nil)), cleanup, nil
}

func hashReadSeeker(src io.ReadSeeker) (string, error) {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, src); err != nil {
		return "", err
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
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
