package media

import (
	"fmt"
	"os"
	"strings"

	"github.com/i5heu/MentisEterna/internal/config"
)

// EndpointConfig holds the full configuration for a single S3-compatible
// endpoint, including the API credentials resolved from the environment.
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

// BuildEndpoints combines S3 endpoint definitions from config (media.endpoints)
// with the per-endpoint API keys from the environment
// (MEDIA_S3_<ID>_ACCESS_KEY_ID and MEDIA_S3_<ID>_SECRET_ACCESS_KEY). Endpoint
// definitions are non-secret and live in config.toml; only the credential keys
// stay in the environment.
func BuildEndpoints() ([]EndpointConfig, error) {
	defs := config.Get().Media.Endpoints
	if len(defs) == 0 {
		return nil, fmt.Errorf("media.endpoints is not set (set it in config.toml)")
	}

	seen := map[string]bool{}
	endpoints := make([]EndpointConfig, 0, len(defs))
	for i, def := range defs {
		if def.ID == "" {
			return nil, fmt.Errorf("media.endpoints[%d]: id is required", i)
		}
		if seen[def.ID] {
			return nil, fmt.Errorf("media.endpoints[%d]: duplicate id %q", i, def.ID)
		}
		seen[def.ID] = true
		if def.Bucket == "" {
			return nil, fmt.Errorf("media.endpoints[%d]: bucket is required", i)
		}
		if def.Endpoint == "" {
			return nil, fmt.Errorf("media.endpoints[%d]: endpoint is required", i)
		}

		envKey := strings.ToUpper(def.ID)
		keyID := os.Getenv("MEDIA_S3_" + envKey + "_ACCESS_KEY_ID")
		secret := os.Getenv("MEDIA_S3_" + envKey + "_SECRET_ACCESS_KEY")
		if keyID == "" {
			return nil, fmt.Errorf("media.endpoints[%d] %q: MEDIA_S3_%s_ACCESS_KEY_ID is required", i, def.ID, envKey)
		}
		if secret == "" {
			return nil, fmt.Errorf("media.endpoints[%d] %q: MEDIA_S3_%s_SECRET_ACCESS_KEY is required", i, def.ID, envKey)
		}

		endpoints = append(endpoints, EndpointConfig{
			ID:              def.ID,
			Bucket:          def.Bucket,
			Region:          def.Region,
			Endpoint:        def.Endpoint,
			AccessKeyID:     keyID,
			SecretAccessKey: secret,
			ForcePathStyle:  def.ForcePathStyle,
		})
	}
	return endpoints, nil
}

// LoadConfigFromEnv reads media configuration: the cache directory and endpoint
// definitions come from config.toml (media.cache_dir and media.endpoints),
// while per-endpoint API keys come from MEDIA_S3_<ID>_ACCESS_KEY_ID and
// MEDIA_S3_<ID>_SECRET_ACCESS_KEY (secrets).
func LoadConfigFromEnv() (Config, error) {
	cacheDir := config.Get().Media.CacheDir
	if cacheDir == "" {
		return Config{}, fmt.Errorf("media.cache_dir is not set (set it in config.toml)")
	}

	endpoints, err := BuildEndpoints()
	if err != nil {
		return Config{}, err
	}

	return Config{
		CacheDir:  cacheDir,
		Endpoints: endpoints,
	}, nil
}
