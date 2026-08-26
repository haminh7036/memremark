// internal/config/config_test.go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Language != "auto" {
		t.Errorf("expected default language 'auto', got %q", cfg.Language)
	}
	if cfg.Summarizer.GeminiModel != DefaultGeminiModel {
		t.Errorf("expected default gemini_model %q, got %q", DefaultGeminiModel, cfg.Summarizer.GeminiModel)
	}
	if cfg.Summarizer.AnthropicModel != DefaultAnthropicModel {
		t.Errorf("expected default anthropic_model %q, got %q", DefaultAnthropicModel, cfg.Summarizer.AnthropicModel)
	}
	if cfg.Summarizer.ClaudeModel != "haiku" {
		t.Errorf("expected default claude_model 'haiku', got %q", cfg.Summarizer.ClaudeModel)
	}
	if cfg.Summarizer.AntigravityModel != "gemini-3.7-flash-low" {
		t.Errorf("expected default antigravity_model 'gemini-3.7-flash-low', got %q", cfg.Summarizer.AntigravityModel)
	}
	if cfg.Summarizer.AntigravityEffort != "low" {
		t.Errorf("expected default antigravity_effort 'low', got %q", cfg.Summarizer.AntigravityEffort)
	}
}

func TestLoad_NonExistentFileReturnsDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error loading from non-existent config: %v", err)
	}
	if cfg.Summarizer.ClaudeModel != "haiku" {
		t.Errorf("expected default 'haiku', got %q", cfg.Summarizer.ClaudeModel)
	}
}

func TestLoad_CustomJSONFile(t *testing.T) {
	tmpDir := t.TempDir()
	memDir := filepath.Join(tmpDir, ".memremark")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}
	jsonContent := `{
		"summarizer": {
			"claude_model": "claude-3-5-haiku-20241022",
			"antigravity_model": "gemini-3.5-flash-low",
			"antigravity_effort": "medium"
		}
	}`
	if err := os.WriteFile(filepath.Join(memDir, "config.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.Summarizer.ClaudeModel != "claude-3-5-haiku-20241022" {
		t.Errorf("expected custom claude_model, got %q", cfg.Summarizer.ClaudeModel)
	}
	if cfg.Summarizer.AntigravityModel != "gemini-3.5-flash-low" {
		t.Errorf("expected custom antigravity_model, got %q", cfg.Summarizer.AntigravityModel)
	}
	if cfg.Summarizer.AntigravityEffort != "medium" {
		t.Errorf("expected custom antigravity_effort, got %q", cfg.Summarizer.AntigravityEffort)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MEMREMARK_LANGUAGE", "vi")
	t.Setenv("MEMREMARK_CLAUDE_MODEL", "custom-claude-env")
	t.Setenv("MEMREMARK_ANTIGRAVITY_MODEL", "custom-agy-env")
	t.Setenv("MEMREMARK_ANTIGRAVITY_EFFORT", "high")

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("failed to load config with env: %v", err)
	}
	if cfg.Language != "vi" {
		t.Errorf("expected env override for language, got %q", cfg.Language)
	}
	if cfg.Summarizer.ClaudeModel != "custom-claude-env" {
		t.Errorf("expected env override for claude, got %q", cfg.Summarizer.ClaudeModel)
	}
	if cfg.Summarizer.AntigravityModel != "custom-agy-env" {
		t.Errorf("expected env override for antigravity, got %q", cfg.Summarizer.AntigravityModel)
	}
	if cfg.Summarizer.AntigravityEffort != "high" {
		t.Errorf("expected env override for effort, got %q", cfg.Summarizer.AntigravityEffort)
	}
}

func TestLoad_MalformedJSONReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	memDir := filepath.Join(tmpDir, ".memremark")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "config.json"), []byte("{not-valid-json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(tmpDir)
	if err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
}

func TestLoad_PartialJSONPreservesOtherDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	memDir := filepath.Join(tmpDir, ".memremark")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}
	jsonContent := `{
		"summarizer": {
			"claude_model": "custom-claude-only"
		}
	}`
	if err := os.WriteFile(filepath.Join(memDir, "config.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.Summarizer.ClaudeModel != "custom-claude-only" {
		t.Errorf("expected custom claude_model, got %q", cfg.Summarizer.ClaudeModel)
	}
	// Other fields must retain their default values
	if cfg.Summarizer.AntigravityModel != DefaultAntigravityModel {
		t.Errorf("expected default antigravity_model %q, got %q", DefaultAntigravityModel, cfg.Summarizer.AntigravityModel)
	}
	if cfg.Summarizer.AntigravityEffort != DefaultAntigravityEffort {
		t.Errorf("expected default antigravity_effort %q, got %q", DefaultAntigravityEffort, cfg.Summarizer.AntigravityEffort)
	}
}

func TestConfig_UIDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.UI.Host != DefaultUIHost {
		t.Errorf("expected default UI host %q, got %q", DefaultUIHost, cfg.UI.Host)
	}
	if cfg.UI.Port != DefaultUIPort {
		t.Errorf("expected default UI port %d, got %d", DefaultUIPort, cfg.UI.Port)
	}
	if !cfg.UI.AutoOpen {
		t.Errorf("expected default UI auto_open true, got %v", cfg.UI.AutoOpen)
	}
}

func TestConfig_UICustomJSON(t *testing.T) {
	tmpDir := t.TempDir()
	memDir := filepath.Join(tmpDir, ".memremark")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}
	jsonContent := `{
		"ui": {
			"host": "0.0.0.0",
			"port": 9090,
			"auto_open": false
		}
	}`
	if err := os.WriteFile(filepath.Join(memDir, "config.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.UI.Host != "0.0.0.0" {
		t.Errorf("expected custom UI host '0.0.0.0', got %q", cfg.UI.Host)
	}
	if cfg.UI.Port != 9090 {
		t.Errorf("expected custom UI port 9090, got %d", cfg.UI.Port)
	}
	if cfg.UI.AutoOpen != false {
		t.Errorf("expected custom UI auto_open false, got %v", cfg.UI.AutoOpen)
	}
}

func TestConfig_UIEnvOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MEMREMARK_UI_HOST", "0.0.0.0")
	t.Setenv("MEMREMARK_UI_PORT", "9999")

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UI.Host != "0.0.0.0" {
		t.Errorf("expected env UI host '0.0.0.0', got %q", cfg.UI.Host)
	}
	if cfg.UI.Port != 9999 {
		t.Errorf("expected env UI port 9999, got %d", cfg.UI.Port)
	}
}

func TestConfig_UIInvalidPortEnv(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MEMREMARK_UI_PORT", "invalid-port")

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UI.Port != DefaultUIPort {
		t.Errorf("expected default UI port %d when invalid env provided, got %d", DefaultUIPort, cfg.UI.Port)
	}
}

func TestConfig_SummarizerProvider(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Summarizer.Provider != "auto" {
		t.Errorf("expected default provider %q, got %q", "auto", cfg.Summarizer.Provider)
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	jsonContent := `{
		"summarizer": {
			"provider": "antigravity"
		}
	}`
	if err := os.WriteFile(configPath, []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	loadedCfg, err := LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}
	if loadedCfg.Summarizer.Provider != "antigravity" {
		t.Errorf("expected loaded provider 'antigravity', got %q", loadedCfg.Summarizer.Provider)
	}

	t.Setenv("MEMREMARK_SUMMARIZER_PROVIDER", "claude")
	envCfg, err := LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile with env failed: %v", err)
	}
	if envCfg.Summarizer.Provider != "claude" {
		t.Errorf("expected env override 'claude', got %q", envCfg.Summarizer.Provider)
	}
}

func TestConfig_APIKeyOverrides(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "env-gemini-key")
	t.Setenv("ANTHROPIC_API_KEY", "env-anthropic-key")
	t.Setenv("MEMREMARK_GEMINI_MODEL", "gemini-2.5-pro")
	t.Setenv("MEMREMARK_ANTHROPIC_MODEL", "claude-3-haiku-20240307")

	cfg, err := LoadFromFile("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Summarizer.GeminiAPIKey != "env-gemini-key" {
		t.Errorf("expected GeminiAPIKey 'env-gemini-key', got '%s'", cfg.Summarizer.GeminiAPIKey)
	}
	if cfg.Summarizer.AnthropicAPIKey != "env-anthropic-key" {
		t.Errorf("expected AnthropicAPIKey 'env-anthropic-key', got '%s'", cfg.Summarizer.AnthropicAPIKey)
	}
	if cfg.Summarizer.GeminiModel != "gemini-2.5-pro" {
		t.Errorf("expected GeminiModel 'gemini-2.5-pro', got '%s'", cfg.Summarizer.GeminiModel)
	}
	if cfg.Summarizer.AnthropicModel != "claude-3-haiku-20240307" {
		t.Errorf("expected AnthropicModel 'claude-3-haiku-20240307', got '%s'", cfg.Summarizer.AnthropicModel)
	}
}

func TestConfig_MemremarkPrefixPrecedence(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "generic-gemini-key")
	t.Setenv("MEMREMARK_GEMINI_API_KEY", "prefixed-gemini-key")
	t.Setenv("ANTHROPIC_API_KEY", "generic-anthropic-key")
	t.Setenv("MEMREMARK_ANTHROPIC_API_KEY", "prefixed-anthropic-key")

	cfg, err := LoadFromFile("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Summarizer.GeminiAPIKey != "prefixed-gemini-key" {
		t.Errorf("expected GeminiAPIKey 'prefixed-gemini-key', got '%s'", cfg.Summarizer.GeminiAPIKey)
	}
	if cfg.Summarizer.AnthropicAPIKey != "prefixed-anthropic-key" {
		t.Errorf("expected AnthropicAPIKey 'prefixed-anthropic-key', got '%s'", cfg.Summarizer.AnthropicAPIKey)
	}
}

func TestConfig_APIKeysFromJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	jsonContent := `{
		"summarizer": {
			"gemini_api_key": "json-gemini-key",
			"gemini_model": "gemini-custom",
			"anthropic_api_key": "json-anthropic-key",
			"anthropic_model": "claude-custom"
		}
	}`
	if err := os.WriteFile(configPath, []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	if cfg.Summarizer.GeminiAPIKey != "json-gemini-key" {
		t.Errorf("expected GeminiAPIKey 'json-gemini-key', got '%s'", cfg.Summarizer.GeminiAPIKey)
	}
	if cfg.Summarizer.GeminiModel != "gemini-custom" {
		t.Errorf("expected GeminiModel 'gemini-custom', got '%s'", cfg.Summarizer.GeminiModel)
	}
	if cfg.Summarizer.AnthropicAPIKey != "json-anthropic-key" {
		t.Errorf("expected AnthropicAPIKey 'json-anthropic-key', got '%s'", cfg.Summarizer.AnthropicAPIKey)
	}
	if cfg.Summarizer.AnthropicModel != "claude-custom" {
		t.Errorf("expected AnthropicModel 'claude-custom', got '%s'", cfg.Summarizer.AnthropicModel)
	}
}




