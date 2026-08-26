// internal/config/config.go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const (
	DefaultLanguage          = "auto"
	DefaultProvider          = "auto"
	DefaultGeminiModel       = "gemini-2.5-flash"
	DefaultAnthropicModel    = "claude-3-5-haiku-20241022"
	DefaultClaudeModel       = "haiku"
	DefaultAntigravityModel  = "gemini-3.7-flash-low"
	DefaultAntigravityEffort = "low"
	DefaultUIHost            = "127.0.0.1"
	DefaultUIPort            = 8765
)

// SummarizerConfig specifies model parameters for headless distillers and direct API invokers.
type SummarizerConfig struct {
	Provider          string `json:"provider"`
	GeminiAPIKey      string `json:"gemini_api_key"`
	GeminiModel       string `json:"gemini_model"`
	AnthropicAPIKey   string `json:"anthropic_api_key"`
	AnthropicModel    string `json:"anthropic_model"`
	ClaudeModel       string `json:"claude_model"`
	AntigravityModel  string `json:"antigravity_model"`
	AntigravityEffort string `json:"antigravity_effort"`
}

// UIConfig specifies settings for the web dashboard.
type UIConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	AutoOpen bool   `json:"auto_open"`
}

// Config represents the root configuration for MemRemark.
type Config struct {
	Language   string           `json:"language"`
	Summarizer SummarizerConfig `json:"summarizer"`
	UI         UIConfig         `json:"ui"`
}

// DefaultConfig returns a Config struct with recommended low-cost defaults.
func DefaultConfig() Config {
	return Config{
		Language: DefaultLanguage,
		Summarizer: SummarizerConfig{
			Provider:          DefaultProvider,
			GeminiModel:       DefaultGeminiModel,
			AnthropicModel:    DefaultAnthropicModel,
			ClaudeModel:       DefaultClaudeModel,
			AntigravityModel:  DefaultAntigravityModel,
			AntigravityEffort: DefaultAntigravityEffort,
		},
		UI: UIConfig{
			Host:     DefaultUIHost,
			Port:     DefaultUIPort,
			AutoOpen: true,
		},
	}
}

// Load loads the configuration from $HOME/.memremark/config.json if it exists,
// fills in defaults for any missing fields, and applies environment variable overrides.
func Load(homeDir string) (Config, error) {
	configPath := filepath.Join(homeDir, ".memremark", "config.json")
	return LoadFromFile(configPath)
}

// LoadFromFile loads configuration from a specified file path.
func LoadFromFile(filePath string) (Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return cfg, fmt.Errorf("config: read %s: %w", filePath, err)
		}
	} else if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("config: parse %s: %w", filePath, err)
	}

	// Environment variable overrides
	if env := os.Getenv("MEMREMARK_LANGUAGE"); env != "" {
		cfg.Language = env
	}
	if env := os.Getenv("MEMREMARK_SUMMARIZER_PROVIDER"); env != "" {
		cfg.Summarizer.Provider = env
	}
	if env := os.Getenv("MEMREMARK_GEMINI_API_KEY"); env != "" {
		cfg.Summarizer.GeminiAPIKey = env
	} else if env := os.Getenv("GEMINI_API_KEY"); env != "" {
		cfg.Summarizer.GeminiAPIKey = env
	}
	if env := os.Getenv("MEMREMARK_GEMINI_MODEL"); env != "" {
		cfg.Summarizer.GeminiModel = env
	}
	if env := os.Getenv("MEMREMARK_ANTHROPIC_API_KEY"); env != "" {
		cfg.Summarizer.AnthropicAPIKey = env
	} else if env := os.Getenv("ANTHROPIC_API_KEY"); env != "" {
		cfg.Summarizer.AnthropicAPIKey = env
	}
	if env := os.Getenv("MEMREMARK_ANTHROPIC_MODEL"); env != "" {
		cfg.Summarizer.AnthropicModel = env
	}
	if env := os.Getenv("MEMREMARK_CLAUDE_MODEL"); env != "" {
		cfg.Summarizer.ClaudeModel = env
	}
	if env := os.Getenv("MEMREMARK_ANTIGRAVITY_MODEL"); env != "" {
		cfg.Summarizer.AntigravityModel = env
	}
	if env := os.Getenv("MEMREMARK_ANTIGRAVITY_EFFORT"); env != "" {
		cfg.Summarizer.AntigravityEffort = env
	}
	if env := os.Getenv("MEMREMARK_UI_HOST"); env != "" {
		cfg.UI.Host = env
	}
	if env := os.Getenv("MEMREMARK_UI_PORT"); env != "" {
		if p, err := strconv.Atoi(env); err == nil && p > 0 {
			cfg.UI.Port = p
		}
	}

	return cfg, nil
}
