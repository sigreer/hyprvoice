package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/leonardotrapani/hyprvoice/internal/injection"
	"github.com/leonardotrapani/hyprvoice/internal/llm"
	"github.com/leonardotrapani/hyprvoice/internal/recording"
	"github.com/leonardotrapani/hyprvoice/internal/transcriber"
)

type Config struct {
	Recording     RecordingConfig     `toml:"recording"`
	Transcription TranscriptionConfig `toml:"transcription"`
	Injection     InjectionConfig     `toml:"injection"`
	Notifications NotificationsConfig `toml:"notifications"`
	Processing    ProcessingConfig    `toml:"processing"`
	LLM           LLMConfig           `toml:"llm"`
}

type ProcessingConfig struct {
	Mode string `toml:"mode"` // "raw" (default) or "llm"
}

type LLMConfig struct {
	Provider     string `toml:"provider"`      // "openai"
	APIKey       string `toml:"api_key"`
	Model        string `toml:"model"`         // Default: "gpt-4o-mini"
	Level        string `toml:"level"`         // "minimal", "moderate", "thorough", or "custom"
	CustomPrompt string `toml:"custom_prompt"` // Used when level is "custom"
}

type RecordingConfig struct {
	SampleRate        int           `toml:"sample_rate"`
	Channels          int           `toml:"channels"`
	Format            string        `toml:"format"`
	BufferSize        int           `toml:"buffer_size"`
	Device            string        `toml:"device"`
	ChannelBufferSize int           `toml:"channel_buffer_size"`
	Timeout           time.Duration `toml:"timeout"`
}

type TranscriptionConfig struct {
	Provider string `toml:"provider"`
	APIKey   string `toml:"api_key"`
	Language string `toml:"language"`
	Model    string `toml:"model"`
}

type InjectionConfig struct {
	Backends         []string      `toml:"backends"`
	YdotoolTimeout   time.Duration `toml:"ydotool_timeout"`
	WtypeTimeout     time.Duration `toml:"wtype_timeout"`
	ClipboardTimeout time.Duration `toml:"clipboard_timeout"`
}

type NotificationsConfig struct {
	Enabled bool   `toml:"enabled"`
	Type    string `toml:"type"` // "desktop", "log", "none"
}

func (c *Config) ToRecordingConfig() recording.Config {
	return recording.Config{
		SampleRate:        c.Recording.SampleRate,
		Channels:          c.Recording.Channels,
		Format:            c.Recording.Format,
		BufferSize:        c.Recording.BufferSize,
		Device:            c.Recording.Device,
		ChannelBufferSize: c.Recording.ChannelBufferSize,
		Timeout:           c.Recording.Timeout,
	}
}

func (c *Config) ToTranscriberConfig() transcriber.Config {
	config := transcriber.Config{
		Provider: c.Transcription.Provider,
		APIKey:   c.Transcription.APIKey,
		Language: c.Transcription.Language,
		Model:    c.Transcription.Model,
	}

	// Check for API key in environment variables if not in config
	if config.APIKey == "" {
		switch c.Transcription.Provider {
		case "openai":
			config.APIKey = os.Getenv("OPENAI_API_KEY")
		case "groq-transcription", "groq-translation":
			config.APIKey = os.Getenv("GROQ_API_KEY")
		}
	}

	return config
}

func (c *Config) ToInjectionConfig() injection.Config {
	return injection.Config{
		Backends:         c.Injection.Backends,
		YdotoolTimeout:   c.Injection.YdotoolTimeout,
		WtypeTimeout:     c.Injection.WtypeTimeout,
		ClipboardTimeout: c.Injection.ClipboardTimeout,
	}
}

func (c *Config) ToLLMConfig() llm.Config {
	config := llm.Config{
		Provider:     c.LLM.Provider,
		APIKey:       c.LLM.APIKey,
		Model:        c.LLM.Model,
		Level:        c.LLM.Level,
		CustomPrompt: c.LLM.CustomPrompt,
	}

	// Check for API key in environment variable if not in config
	if config.APIKey == "" {
		config.APIKey = os.Getenv("OPENAI_API_KEY")
	}

	// Default level to moderate if not set
	if config.Level == "" {
		config.Level = "moderate"
	}

	return config
}

func (c *Config) Validate() error {
	// Recording
	if c.Recording.SampleRate <= 0 {
		return fmt.Errorf("invalid recording.sample_rate: %d", c.Recording.SampleRate)
	}
	if c.Recording.Channels <= 0 {
		return fmt.Errorf("invalid recording.channels: %d", c.Recording.Channels)
	}
	if c.Recording.BufferSize <= 0 {
		return fmt.Errorf("invalid recording.buffer_size: %d", c.Recording.BufferSize)
	}
	if c.Recording.ChannelBufferSize <= 0 {
		return fmt.Errorf("invalid recording.channel_buffer_size: %d", c.Recording.ChannelBufferSize)
	}
	if c.Recording.Format == "" {
		return fmt.Errorf("invalid recording.format: empty")
	}
	if c.Recording.Timeout <= 0 {
		return fmt.Errorf("invalid recording.timeout: %v", c.Recording.Timeout)
	}

	// Transcription
	if c.Transcription.Provider == "" {
		return fmt.Errorf("invalid transcription.provider: empty")
	}

	// Validate provider-specific settings
	switch c.Transcription.Provider {
	case "openai":
		apiKey := c.Transcription.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if apiKey == "" {
			return fmt.Errorf("OpenAI API key required: not found in config (transcription.api_key) or environment variable (OPENAI_API_KEY)")
		}

		// Validate language code if provided (empty string means auto-detect)
		if c.Transcription.Language != "" && !isValidLanguageCode(c.Transcription.Language) {
			return fmt.Errorf("invalid transcription.language: %s (use empty string for auto-detect or ISO-639-1 codes like 'en', 'es', 'fr')", c.Transcription.Language)
		}

	case "groq-transcription":
		apiKey := c.Transcription.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("GROQ_API_KEY")
		}
		if apiKey == "" {
			return fmt.Errorf("Groq API key required: not found in config (transcription.api_key) or environment variable (GROQ_API_KEY)")
		}

		// Validate language code if provided (empty string means auto-detect)
		if c.Transcription.Language != "" && !isValidLanguageCode(c.Transcription.Language) {
			return fmt.Errorf("invalid transcription.language: %s (use empty string for auto-detect or ISO-639-1 codes like 'en', 'es', 'fr')", c.Transcription.Language)
		}

		// Validate Groq model
		validGroqModels := map[string]bool{"whisper-large-v3": true, "whisper-large-v3-turbo": true}
		if c.Transcription.Model != "" && !validGroqModels[c.Transcription.Model] {
			return fmt.Errorf("invalid model for groq-transcription: %s (must be whisper-large-v3 or whisper-large-v3-turbo)", c.Transcription.Model)
		}

	case "groq-translation":
		apiKey := c.Transcription.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("GROQ_API_KEY")
		}
		if apiKey == "" {
			return fmt.Errorf("Groq API key required: not found in config (transcription.api_key) or environment variable (GROQ_API_KEY)")
		}

		// For translation, language field hints at source language (output is always English)
		if c.Transcription.Language != "" && !isValidLanguageCode(c.Transcription.Language) {
			return fmt.Errorf("invalid transcription.language: %s (use empty string for auto-detect or ISO-639-1 codes like 'en', 'es', 'fr')", c.Transcription.Language)
		}

		// Validate Groq translation model - only whisper-large-v3 is supported (no turbo)
		if c.Transcription.Model != "" && c.Transcription.Model != "whisper-large-v3" {
			return fmt.Errorf("invalid model for groq-translation: %s (must be whisper-large-v3, turbo version not supported for translation)", c.Transcription.Model)
		}

	default:
		return fmt.Errorf("unsupported transcription.provider: %s (must be openai, groq-transcription, or groq-translation)", c.Transcription.Provider)
	}

	if c.Transcription.Model == "" {
		return fmt.Errorf("invalid transcription.model: empty")
	}

	// Injection
	if len(c.Injection.Backends) == 0 {
		return fmt.Errorf("invalid injection.backends: empty (must have at least one backend)")
	}
	validBackends := map[string]bool{"ydotool": true, "wtype": true, "clipboard": true}
	for _, backend := range c.Injection.Backends {
		if !validBackends[backend] {
			return fmt.Errorf("invalid injection.backends: unknown backend %q (must be ydotool, wtype, or clipboard)", backend)
		}
	}
	if c.Injection.YdotoolTimeout <= 0 {
		return fmt.Errorf("invalid injection.ydotool_timeout: %v", c.Injection.YdotoolTimeout)
	}
	if c.Injection.WtypeTimeout <= 0 {
		return fmt.Errorf("invalid injection.wtype_timeout: %v", c.Injection.WtypeTimeout)
	}
	if c.Injection.ClipboardTimeout <= 0 {
		return fmt.Errorf("invalid injection.clipboard_timeout: %v", c.Injection.ClipboardTimeout)
	}

	// Notifications
	validTypes := map[string]bool{"desktop": true, "log": true, "none": true}
	if !validTypes[c.Notifications.Type] {
		return fmt.Errorf("invalid notifications.type: %s (must be desktop, log, or none)", c.Notifications.Type)
	}

	// Processing (optional - defaults to "raw" if not set)
	if c.Processing.Mode == "" {
		c.Processing.Mode = "raw"
	}
	validModes := map[string]bool{"raw": true, "llm": true}
	if !validModes[c.Processing.Mode] {
		return fmt.Errorf("invalid processing.mode: %s (must be raw or llm)", c.Processing.Mode)
	}

	// LLM config (only validate if mode is "llm")
	if c.Processing.Mode == "llm" {
		if c.LLM.Provider == "" {
			c.LLM.Provider = "openai"
		}
		if c.LLM.Provider != "openai" {
			return fmt.Errorf("invalid llm.provider: %s (must be openai)", c.LLM.Provider)
		}
		if c.LLM.Model == "" {
			c.LLM.Model = "gpt-4o-mini"
		}
		// Validate and set default level
		if c.LLM.Level == "" {
			c.LLM.Level = "moderate"
		}
		validLevels := map[string]bool{"minimal": true, "moderate": true, "thorough": true, "custom": true}
		if !validLevels[c.LLM.Level] {
			return fmt.Errorf("invalid llm.level: %s (must be minimal, moderate, thorough, or custom)", c.LLM.Level)
		}
		// If level is custom, require a custom_prompt
		if c.LLM.Level == "custom" && c.LLM.CustomPrompt == "" {
			return fmt.Errorf("llm.custom_prompt is required when llm.level is 'custom'")
		}
		// Check for API key
		apiKey := c.LLM.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if apiKey == "" {
			return fmt.Errorf("LLM API key required when processing.mode is 'llm': not found in config (llm.api_key) or environment variable (OPENAI_API_KEY)")
		}
	}

	return nil
}

func isValidLanguageCode(code string) bool {
	validCodes := map[string]bool{
		"en": true, "es": true, "fr": true, "de": true, "it": true, "pt": true,
		"ru": true, "ja": true, "ko": true, "zh": true, "ar": true, "hi": true,
		"nl": true, "sv": true, "da": true, "no": true, "fi": true, "pl": true,
		"tr": true, "he": true, "th": true, "vi": true, "id": true, "ms": true,
		"uk": true, "cs": true, "hu": true, "ro": true, "bg": true, "hr": true,
		"sk": true, "sl": true, "et": true, "lv": true, "lt": true, "mt": true,
		"cy": true, "ga": true, "eu": true, "ca": true, "gl": true, "is": true,
		"mk": true, "sq": true, "az": true, "be": true, "ka": true, "hy": true,
		"kk": true, "ky": true, "tg": true, "uz": true, "mn": true, "ne": true,
		"si": true, "km": true, "lo": true, "my": true, "fa": true, "ps": true,
		"ur": true, "bn": true, "ta": true, "te": true, "ml": true, "kn": true,
		"gu": true, "pa": true, "or": true, "as": true, "mr": true, "sa": true,
		"sw": true, "yo": true, "ig": true, "ha": true, "zu": true, "xh": true,
		"af": true, "am": true, "mg": true, "so": true, "sn": true, "rw": true,
	}
	return validCodes[code]
}

func GetConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config directory: %w", err)
	}

	hyprvoiceDir := filepath.Join(configDir, "hyprvoice")
	if err := os.MkdirAll(hyprvoiceDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}

	return filepath.Join(hyprvoiceDir, "config.toml"), nil
}

// legacyInjectionConfig for migration from old mode-based config
type legacyInjectionConfig struct {
	Mode string `toml:"mode"`
}

type legacyConfig struct {
	Injection legacyInjectionConfig `toml:"injection"`
}

func Load() (*Config, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	// If config file doesn't exist, create it with defaults
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("Config: no config file found at %s, creating with defaults", configPath)
		if err := SaveDefaultConfig(); err != nil {
			return nil, fmt.Errorf("failed to create default config: %w", err)
		}
		log.Printf("Config: default configuration created successfully")
		return Load() // Recursively load the config, now file will exist
	}

	log.Printf("Config: loading configuration from %s", configPath)
	var config Config
	if _, err := toml.DecodeFile(configPath, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
	}

	// Migrate legacy mode-based config to backends
	if len(config.Injection.Backends) == 0 {
		var legacy legacyConfig
		toml.DecodeFile(configPath, &legacy)
		config.migrateInjectionMode(legacy.Injection.Mode)
	}

	log.Printf("Config: configuration loaded successfully")
	return &config, nil
}

// migrateInjectionMode converts old mode field to new backends array
func (c *Config) migrateInjectionMode(mode string) {
	switch mode {
	case "clipboard":
		c.Injection.Backends = []string{"clipboard"}
		log.Printf("Config: migrated injection.mode='clipboard' to backends=['clipboard']")
	case "type":
		c.Injection.Backends = []string{"wtype"}
		log.Printf("Config: migrated injection.mode='type' to backends=['wtype']")
	case "fallback":
		c.Injection.Backends = []string{"wtype", "clipboard"}
		log.Printf("Config: migrated injection.mode='fallback' to backends=['wtype', 'clipboard']")
	default:
		// Default for new installs or unknown modes
		c.Injection.Backends = []string{"ydotool", "wtype", "clipboard"}
		if mode != "" {
			log.Printf("Config: unknown injection.mode='%s', using default backends", mode)
		}
	}

	// Set default ydotool timeout if not set
	if c.Injection.YdotoolTimeout == 0 {
		c.Injection.YdotoolTimeout = 5 * time.Second
	}

	log.Printf("Config: legacy 'mode' config detected - please update your config.toml to use 'backends' instead")
}

func SaveDefaultConfig() error {
	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	file, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer file.Close()

	configContent := `# Hyprvoice Configuration
# This file is automatically generated with defaults.
# Edit values as needed - changes are applied immediately without daemon restart.

# Audio Recording Configuration
[recording]
  sample_rate = 16000          # Audio sample rate in Hz (16000 recommended for speech)
  channels = 1                 # Number of audio channels (1 = mono, 2 = stereo)
  format = "s16"               # Audio format (s16 = 16-bit signed integers)
  buffer_size = 8192           # Internal buffer size in bytes (larger = less CPU, more latency)
  device = ""                  # PipeWire audio device (empty = use default microphone)
  channel_buffer_size = 30     # Audio frame buffer size (frames to buffer)
  timeout = "5m"               # Maximum recording duration (e.g., "30s", "2m", "5m")

# Speech Transcription Configuration
[transcription]
  provider = "openai"          # Transcription service: "openai", "groq-transcription", or "groq-translation"
  api_key = ""                 # API key (or set OPENAI_API_KEY/GROQ_API_KEY environment variable)
  language = ""                # Language code (empty for auto-detect, "en", "it", "es", "fr", etc.)
  model = "whisper-1"          # Model: OpenAI="whisper-1", Groq="whisper-large-v3" or "whisper-large-v3-turbo"

# Text Injection Configuration
[injection]
  backends = ["ydotool", "wtype", "clipboard"]  # Ordered fallback chain (tries each until one succeeds)
  ydotool_timeout = "5s"       # Timeout for ydotool commands
  wtype_timeout = "5s"         # Timeout for wtype commands
  clipboard_timeout = "3s"     # Timeout for clipboard operations

# Desktop Notification Configuration
[notifications]
  enabled = true               # Enable desktop notifications
  type = "desktop"             # Notification type ("desktop", "log", "none")

# Post-Transcription Processing Configuration
[processing]
  mode = "raw"                 # Processing mode: "raw" (direct transcription) or "llm" (AI cleanup)

# LLM Configuration (used when processing.mode = "llm")
[llm]
  provider = "openai"          # LLM provider (currently only "openai" supported)
  api_key = ""                 # API key (or use OPENAI_API_KEY environment variable)
  model = "gpt-4o-mini"        # Model to use for text cleanup
  level = "moderate"           # Intervention level: "minimal", "moderate", "thorough", or "custom"
  custom_prompt = ""           # Custom system prompt (used when level = "custom")

# Backend explanations:
# - "ydotool": Uses ydotool (requires ydotoold daemon running). Most compatible with Chromium/Electron apps.
# - "wtype": Uses wtype for Wayland. May have issues with some Chromium-based apps.
# - "clipboard": Copies text to clipboard only (most reliable, but requires manual paste).
#
# The backends are tried in order. First successful one wins.
# Example configurations:
#   backends = ["clipboard"]                      # Clipboard only (safest)
#   backends = ["wtype", "clipboard"]             # wtype with clipboard fallback
#   backends = ["ydotool", "wtype", "clipboard"]  # Full fallback chain (default)
#
# Provider explanations:
# - "openai": OpenAI Whisper API (cloud-based, requires OPENAI_API_KEY)
# - "groq-transcription": Groq Whisper API for transcription (fast, requires GROQ_API_KEY)
#     Models: whisper-large-v3 or whisper-large-v3-turbo
# - "groq-translation": Groq Whisper API for translation to English (always outputs English text)
#     Models: whisper-large-v3 only (turbo not supported for translation)
#
# Language codes: Use empty string ("") for automatic detection, or specific codes like:
# "en" (English), "it" (Italian), "es" (Spanish), "fr" (French), "de" (German), etc.
# For groq-translation, the language field hints at the source audio language for better accuracy.
#
# Processing mode explanations:
# - "raw": Direct transcription output without any post-processing
# - "llm": Pass transcription through an LLM to clean up the text
#
# LLM level explanations:
# - "minimal":  Light touch - only fix typos, punctuation, and capitalization
# - "moderate": Balanced - remove filler words (um, uh) and fix punctuation while preserving voice
# - "thorough": Full rewrite - restructure for clarity and flow while preserving meaning
# - "custom":   Use your own system prompt defined in custom_prompt
#
# LLM provider explanations:
# - "openai": Uses OpenAI's API (requires OPENAI_API_KEY). Recommended model: gpt-4o-mini
`

	if _, err := file.WriteString(configContent); err != nil {
		return fmt.Errorf("failed to write config content: %w", err)
	}

	return nil
}
