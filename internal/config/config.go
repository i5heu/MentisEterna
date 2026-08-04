package config

import "github.com/BurntSushi/toml"

// Config is the complete non-secret application configuration. Secrets (S3
// credentials, LocalAI base URLs, backup encryption key) are deliberately NOT
// here — they stay in the environment (see config.default.toml).
type Config struct {
	Database DatabaseConfig `toml:"database"`
	Server   ServerConfig   `toml:"server"`
	Auth     AuthConfig     `toml:"auth"`
	LLM      LLMConfig      `toml:"llm"`
	Media    MediaConfig    `toml:"media"`
	Printer  PrinterConfig  `toml:"printer"`
	Jobs     JobsConfig     `toml:"jobs"`
	Recipe   RecipeConfig   `toml:"recipe"`
}

type DatabaseConfig struct {
	Path            string `toml:"path"`              // was DB_PATH
	VecExtPath      string `toml:"vec_ext_path"`      // was VEC_EXT_PATH
	LegacyVSSExtPath string `toml:"vss_ext_path"`     // was VSS_EXT_PATH (legacy alias)
}

type ServerConfig struct {
	Addr                 string `toml:"addr"`                    // was ADDR
	PublicBaseURL        string `toml:"public_base_url"`         // was PUBLIC_BASE_URL
	MaxUploadBytes       int64  `toml:"max_upload_bytes"`        // was MAX_UPLOAD_BYTES
	MaxInlineUploadBytes int64  `toml:"max_inline_upload_bytes"` // was MAX_INLINE_UPLOAD_BYTES
	MaxJSONBodyBytes     int64  `toml:"max_json_body_bytes"`     // was MAX_JSON_BODY_BYTES
	TLSCertFile          string `toml:"tls_cert_file"`           // was TLS_CERT_FILE
	TLSKeyFile           string `toml:"tls_key_file"`            // was TLS_KEY_FILE
}

type AuthConfig struct {
	WebAuthnRPID      string   `toml:"webauthn_rpid"`       // was WEBAUTHN_RPID
	WebAuthnRPOrigins []string `toml:"webauthn_rp_origins"` // was WEBAUTHN_RP_ORIGINS (comma list)
}

type LLMConfig struct {
	EmbeddingModel    string                   `toml:"embedding_model"`       // was LOCALAI_EMBEDDING_MODEL
	EmbeddingMaxChars int                      `toml:"embedding_max_chars"`   // was LOCALAI_EMBEDDING_MAX_CHARS
	TLSInsecure       bool                     `toml:"tls_insecure"`          // was LOCALAI_TLS_INSECURE
	ChatModel         string                   `toml:"chat_model"`            // legacy fallback, was LOCALAI_CHAT_MODEL
	OCRModel          string                   `toml:"ocr_model"`             // legacy fallback, was LOCALAI_OCR_MODEL
	STTModel          string                   `toml:"stt_model"`             // legacy fallback, was LOCALAI_STT_MODEL
	OCRDedicatedModel string                   `toml:"ocr_dedicated_model"`   // was LLM_OCR_MODEL
	STTDedicatedModel string                   `toml:"stt_dedicated_model"`   // was LLM_STT_MODEL
	Tiers             map[string]TierConfig    `toml:"tiers"`                 // was LLM_TIER_<NAME>_MODEL
	Features          map[string]FeatureConfig `toml:"features"`              // was LLM_FEATURE_<NAME>_TIER
}

type TierConfig struct {
	Model string `toml:"model"` // empty = fall back to legacy model
}

type FeatureConfig struct {
	Tier string `toml:"tier"` // empty = featureSpec default
}

type MediaConfig struct {
	CacheDir string `toml:"cache_dir"` // was MEDIA_CACHE_DIR (S3 endpoints stay env)
}

type PrinterConfig struct {
	Device             string  `toml:"device"`               // was THERMAL_PRINTER_DEVICE
	USBID              string  `toml:"usb_id"`               // was THERMAL_PRINTER_USB_ID
	CodePage           string  `toml:"codepage"`             // was THERMAL_PRINTER_CODEPAGE
	ImageThreshold     float64 `toml:"image_threshold"`      // was THERMAL_PRINTER_IMAGE_THRESHOLD
	ImageDarknessScale float64 `toml:"image_darkness_scale"` // was THERMAL_PRINTER_IMAGE_DARKNESS_SCALE
	WriteChunkBytes    int     `toml:"write_chunk_bytes"`    // was THERMAL_PRINTER_WRITE_CHUNK_BYTES
	WriteDelayMS       int     `toml:"write_delay_ms"`       // was THERMAL_PRINTER_WRITE_DELAY_MS
}

type JobsConfig struct {
	Workers int `toml:"workers"` // was JOB_WORKERS
}

type RecipeConfig struct {
	CategoryWorkers int `toml:"category_workers"` // was RECIPE_CATEGORY_WORKERS
}

var current = DefaultConfig()

// DefaultConfig returns a Config with every production default filled
// (identical to today's behavior when no env var is set).
func DefaultConfig() Config {
	return Config{
		Database: DatabaseConfig{Path: "mentis.db"},
		Server: ServerConfig{
			Addr:                 ":8080",
			MaxUploadBytes:       64 << 20,
			MaxInlineUploadBytes: 64 << 20,
			MaxJSONBodyBytes:     1 << 20,
		},
		LLM: LLMConfig{
			EmbeddingModel:    "text-embedding-ada-002",
			EmbeddingMaxChars: 16 * 1024,
			ChatModel:         "gpt-3.5-turbo",
			OCRModel:          "gpt-4o-mini",
			STTModel:          "nemo-parakeet-tdt-0.6b",
		},
		Printer: PrinterConfig{
			ImageThreshold:     120.0,
			ImageDarknessScale: 0.96,
		},
		Jobs:   JobsConfig{Workers: 10},
		Recipe: RecipeConfig{CategoryWorkers: 10},
	}
}

// Load replaces `current` with DefaultConfig() then decodes path over it, so
// absent fields keep defaults. Returns an error only for an unreadable/invalid
// file. Secrets are never read here.
func Load(path string) error {
	c := DefaultConfig()
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return err
	}
	current = c
	return nil
}

// Get returns the process-wide config; DefaultConfig() is returned until Load
// is called, so unloaded (test) use behaves identically to defaults.
func Get() *Config { return &current }

// Reset restores DefaultConfig(); used by tests that mutate Get().
func Reset() { current = DefaultConfig() }
