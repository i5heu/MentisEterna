package media

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/i5heu/MentisEterna/internal/config"
)

// EndpointConfig holds the configuration for a single S3-compatible endpoint.
type EndpointConfig struct {
	ID              string `json:"id"`
	Bucket          string `json:"bucket"`
	Region          string `json:"region"`
	Endpoint        string `json:"endpoint"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	ForcePathStyle  bool   `json:"force_path_style"`
}

// Config holds the media subsystem configuration.
type Config struct {
	CacheDir  string           `json:"cache_dir"`
	Endpoints []EndpointConfig `json:"endpoints"`
}

// LoadEndpointsFromEnv reads and validates MEDIA_S3_ENDPOINTS.
func LoadEndpointsFromEnv() ([]EndpointConfig, error) {
	endpointsJSON := os.Getenv("MEDIA_S3_ENDPOINTS")
	if endpointsJSON == "" {
		return nil, fmt.Errorf("MEDIA_S3_ENDPOINTS environment variable is required")
	}

	var endpoints []EndpointConfig
	if err := json.Unmarshal([]byte(endpointsJSON), &endpoints); err != nil {
		return nil, fmt.Errorf("MEDIA_S3_ENDPOINTS: invalid JSON: %w", err)
	}

	if len(endpoints) == 0 {
		return nil, fmt.Errorf("MEDIA_S3_ENDPOINTS: at least one endpoint is required")
	}

	seen := map[string]bool{}
	for i, ep := range endpoints {
		if ep.ID == "" {
			return nil, fmt.Errorf("MEDIA_S3_ENDPOINTS[%d]: id is required", i)
		}
		if seen[ep.ID] {
			return nil, fmt.Errorf("MEDIA_S3_ENDPOINTS[%d]: duplicate id %q", i, ep.ID)
		}
		seen[ep.ID] = true
		if ep.Bucket == "" {
			return nil, fmt.Errorf("MEDIA_S3_ENDPOINTS[%d]: bucket is required", i)
		}
		if ep.Endpoint == "" {
			return nil, fmt.Errorf("MEDIA_S3_ENDPOINTS[%d]: endpoint is required", i)
		}
		if ep.AccessKeyID == "" {
			return nil, fmt.Errorf("MEDIA_S3_ENDPOINTS[%d]: access_key_id is required", i)
		}
		if ep.SecretAccessKey == "" {
			return nil, fmt.Errorf("MEDIA_S3_ENDPOINTS[%d]: secret_access_key is required", i)
		}
	}

	return endpoints, nil
}

// LoadConfigFromEnv reads media configuration: the cache directory comes from
// config (media.cache_dir), while S3 endpoints stay in MEDIA_S3_ENDPOINTS
// (secret). MEDIA_S3_ENDPOINTS must be a JSON array of endpoint configs.
func LoadConfigFromEnv() (Config, error) {
	cacheDir := config.Get().Media.CacheDir
	if cacheDir == "" {
		return Config{}, fmt.Errorf("media.cache_dir is not set (set it in config.toml)")
	}

	endpoints, err := LoadEndpointsFromEnv()
	if err != nil {
		return Config{}, err
	}

	return Config{
		CacheDir:  cacheDir,
		Endpoints: endpoints,
	}, nil
}
