package media

import (
	"encoding/json"
	"fmt"
	"os"
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

// LoadConfigFromEnv reads media configuration from environment variables.
// MEDIA_S3_ENDPOINTS must be a JSON array of endpoint configs.
// MEDIA_CACHE_DIR must be set to a writable directory path.
func LoadConfigFromEnv() (Config, error) {
	cacheDir := os.Getenv("MEDIA_CACHE_DIR")
	if cacheDir == "" {
		return Config{}, fmt.Errorf("MEDIA_CACHE_DIR environment variable is required")
	}

	endpointsJSON := os.Getenv("MEDIA_S3_ENDPOINTS")
	if endpointsJSON == "" {
		return Config{}, fmt.Errorf("MEDIA_S3_ENDPOINTS environment variable is required")
	}

	var endpoints []EndpointConfig
	if err := json.Unmarshal([]byte(endpointsJSON), &endpoints); err != nil {
		return Config{}, fmt.Errorf("MEDIA_S3_ENDPOINTS: invalid JSON: %w", err)
	}

	if len(endpoints) == 0 {
		return Config{}, fmt.Errorf("MEDIA_S3_ENDPOINTS: at least one endpoint is required")
	}

	seen := map[string]bool{}
	for i, ep := range endpoints {
		if ep.ID == "" {
			return Config{}, fmt.Errorf("MEDIA_S3_ENDPOINTS[%d]: id is required", i)
		}
		if seen[ep.ID] {
			return Config{}, fmt.Errorf("MEDIA_S3_ENDPOINTS[%d]: duplicate id %q", i, ep.ID)
		}
		seen[ep.ID] = true
		if ep.Bucket == "" {
			return Config{}, fmt.Errorf("MEDIA_S3_ENDPOINTS[%d]: bucket is required", i)
		}
		if ep.Endpoint == "" {
			return Config{}, fmt.Errorf("MEDIA_S3_ENDPOINTS[%d]: endpoint is required", i)
		}
		if ep.AccessKeyID == "" {
			return Config{}, fmt.Errorf("MEDIA_S3_ENDPOINTS[%d]: access_key_id is required", i)
		}
		if ep.SecretAccessKey == "" {
			return Config{}, fmt.Errorf("MEDIA_S3_ENDPOINTS[%d]: secret_access_key is required", i)
		}
	}

	return Config{
		CacheDir:  cacheDir,
		Endpoints: endpoints,
	}, nil
}
