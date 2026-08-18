// internal/config/config.go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultClaudeModel       = "haiku"
	DefaultAntigravityModel  = "gemini-3.7-flash-low"
	DefaultAntigravityEffort = "low"
)

// SummarizerConfig specifies model parameters for headless distillers.
type SummarizerConfig struct {
	ClaudeModel       string `json:"claude_model"`
	AntigravityModel  string `json:"antigravity_model"`
	AntigravityEffort string `json:"antigravity_effort"`
}

// Config represents the root configuration for MemRemark.
type Config struct {
	Summarizer SummarizerConfig `json:"summarizer"`
}

// DefaultConfig returns a Config struct with recommended low-cost defaults.
func DefaultConfig() Config {
	return Config{
		Summarizer: SummarizerConfig{
			ClaudeModel:       DefaultClaudeModel,
			AntigravityModel:  DefaultAntigravityModel,
			AntigravityEffort: DefaultAntigravityEffort,
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
	if env := os.Getenv("MEMREMARK_CLAUDE_MODEL"); env != "" {
		cfg.Summarizer.ClaudeModel = env
	}
	if env := os.Getenv("MEMREMARK_ANTIGRAVITY_MODEL"); env != "" {
		cfg.Summarizer.AntigravityModel = env
	}
	if env := os.Getenv("MEMREMARK_ANTIGRAVITY_EFFORT"); env != "" {
		cfg.Summarizer.AntigravityEffort = env
	}

	return cfg, nil
}
