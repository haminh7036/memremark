// internal/config/config_test.go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
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
	t.Setenv("MEMREMARK_CLAUDE_MODEL", "custom-claude-env")
	t.Setenv("MEMREMARK_ANTIGRAVITY_MODEL", "custom-agy-env")
	t.Setenv("MEMREMARK_ANTIGRAVITY_EFFORT", "high")

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("failed to load config with env: %v", err)
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

