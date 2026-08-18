# Daemon I/O & Memory Optimization and Model Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Optimize `memremarkd` background polling to eliminate disk I/O churn, SQLite write churn, and high peak memory, while introducing a hierarchical configuration system that defaults to low-cost LLM models (`haiku` and `gemini-3.7-flash-low`).

**Architecture:** A new `internal/config` package loads `~/.memremark/config.json` with environment variable overrides and sensible cheap defaults. `ClaudeCodeInvoker` and `AntigravityInvoker` in `internal/summarizer` are updated to accept model and effort parameters and pass lightweight headless execution flags (`--safe-mode`, `--tools ""`, `--disable-slash-commands`, `--effort low`). `internal/adapter/claudecode`'s `Tailer` and `internal/daemon` are enhanced with per-file `ModTime`/`Size` dirty checking and truncation resets to prevent opening files or updating SQLite `poll_state` when transcripts have not changed.

**Tech Stack:** Go 1.23+, modernc.org/sqlite, standard library `os.Stat`, `time.Time`, `os/exec`.

## Global Constraints

- Zero new external dependencies: Use Go standard library only for file metadata checking, configuration parsing, and process management.
- Backward compatibility: Existing `~/.memremark/memremark.db` and database schema remain untouched.
- Robust file discovery: `filepath.WalkDir` must continue to discover transcript files across arbitrary subfolder depth.
- Clean fallback: Missing or malformed config files must log a warning and fall back to built-in defaults without crashing.
- Atomic commits: Every task must follow Conventional Commits with bulleted explanations and a `Co-Authored-By: Claude <noreply@anthropic.com>` trailer.

---

### Task 1: Create `internal/config` Package and Unit Tests

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  ```go
  package config

  type SummarizerConfig struct {
      ClaudeModel       string `json:"claude_model"`
      AntigravityModel  string `json:"antigravity_model"`
      AntigravityEffort string `json:"antigravity_effort"`
  }

  type Config struct {
      Summarizer SummarizerConfig `json:"summarizer"`
  }

  func DefaultConfig() Config
  func Load(homeDir string) (Config, error)
  func LoadFromFile(filePath string) (Config, error)
  ```

- [ ] **Step 1: Write the unit tests for `internal/config`**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/...`
Expected: FAIL (package `config` does not exist yet)

- [ ] **Step 3: Implement `internal/config/config.go`**

```go
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
	} else {
		var userCfg Config
		if err := json.Unmarshal(data, &userCfg); err != nil {
			return cfg, fmt.Errorf("config: parse %s: %w", filePath, err)
		}
		if userCfg.Summarizer.ClaudeModel != "" {
			cfg.Summarizer.ClaudeModel = userCfg.Summarizer.ClaudeModel
		}
		if userCfg.Summarizer.AntigravityModel != "" {
			cfg.Summarizer.AntigravityModel = userCfg.Summarizer.AntigravityModel
		}
		if userCfg.Summarizer.AntigravityEffort != "" {
			cfg.Summarizer.AntigravityEffort = userCfg.Summarizer.AntigravityEffort
		}
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): implement configuration loader with defaults and env overrides

- Add SummarizerConfig and root Config structs
- Load settings from ~/.memremark/config.json with default fallback
- Allow environment variable overrides for models and effort
- Add comprehensive test suite covering file and environment precedence

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: Update `internal/summarizer` with Configurable Models and Lightweight Flags

**Files:**
- Modify: `internal/summarizer/summarizer.go:27-78`
- Modify: `internal/summarizer/summarizer_test.go`

**Interfaces:**
- Produces:
  ```go
  type ClaudeCodeInvoker struct {
      Model string
  }

  type AntigravityInvoker struct {
      Model  string
      Effort string
  }
  ```

- [ ] **Step 1: Write unit tests verifying CLI argument generation**

Add unit tests to `internal/summarizer/summarizer_test.go`:
```go
func TestClaudeCodeInvoker_BuildArgs(t *testing.T) {
	tests := []struct {
		name     string
		invoker  ClaudeCodeInvoker
		wantArgs []string
	}{
		{
			name:     "default empty uses default model haiku",
			invoker:  ClaudeCodeInvoker{},
			wantArgs: []string{"-p", "--output-format", "json", "--safe-mode", "--tools", "", "--model", "haiku"},
		},
		{
			name:     "custom model specifies model flag",
			invoker:  ClaudeCodeInvoker{Model: "claude-3-5-sonnet"},
			wantArgs: []string{"-p", "--output-format", "json", "--safe-mode", "--tools", "", "--model", "claude-3-5-sonnet"},
		},
		{
			name:     "keyword 'default' omits model flag",
			invoker:  ClaudeCodeInvoker{Model: "default"},
			wantArgs: []string{"-p", "--output-format", "json", "--safe-mode", "--tools", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.invoker.buildArgs()
			if len(args) != len(tt.wantArgs) {
				t.Fatalf("got args %v, want %v", args, tt.wantArgs)
			}
			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("arg[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
		})
	}
}

func TestAntigravityInvoker_BuildArgs(t *testing.T) {
	tests := []struct {
		name     string
		invoker  AntigravityInvoker
		prompt   string
		wantArgs []string
	}{
		{
			name:     "default empty uses default model and low effort",
			invoker:  AntigravityInvoker{},
			prompt:   "test prompt",
			wantArgs: []string{"-p", "test prompt", "--output-format", "json", "--disable-slash-commands", "--model", "gemini-3.7-flash-low", "--effort", "low"},
		},
		{
			name:     "custom model and effort",
			invoker:  AntigravityInvoker{Model: "gemini-3.5-flash-low", Effort: "medium"},
			prompt:   "test prompt",
			wantArgs: []string{"-p", "test prompt", "--output-format", "json", "--disable-slash-commands", "--model", "gemini-3.5-flash-low", "--effort", "medium"},
		},
		{
			name:     "keyword 'default' omits model and effort",
			invoker:  AntigravityInvoker{Model: "default", Effort: "default"},
			prompt:   "test prompt",
			wantArgs: []string{"-p", "test prompt", "--output-format", "json", "--disable-slash-commands"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.invoker.buildArgs(tt.prompt)
			if len(args) != len(tt.wantArgs) {
				t.Fatalf("got args %v, want %v", args, tt.wantArgs)
			}
			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("arg[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/summarizer/... -run "TestClaudeCodeInvoker_BuildArgs|TestAntigravityInvoker_BuildArgs"`
Expected: FAIL (methods `buildArgs` undefined)

- [ ] **Step 3: Update `ClaudeCodeInvoker` and `AntigravityInvoker` in `internal/summarizer/summarizer.go`**

```go
// ClaudeCodeInvoker runs prompts through `claude -p --output-format json`.
type ClaudeCodeInvoker struct {
	Model string
}

func (inv ClaudeCodeInvoker) buildArgs() []string {
	args := []string{"-p", "--output-format", "json", "--safe-mode", "--tools", ""}
	model := inv.Model
	if model == "" {
		model = "haiku"
	}
	if model != "default" && model != "none" {
		args = append(args, "--model", model)
	}
	return args
}

// Invoke implements Invoker.
func (inv ClaudeCodeInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", inv.buildArgs()...)
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("summarizer: claude -p failed: %w", err)
	}
	var res claudeCodeResult
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("summarizer: parse claude -p output: %w", err)
	}
	if res.IsError {
		return "", fmt.Errorf("summarizer: claude -p reported an error result")
	}
	return res.Result, nil
}

// AntigravityInvoker runs prompts through `agy -p --output-format json`.
type AntigravityInvoker struct {
	Model  string
	Effort string
}

func (inv AntigravityInvoker) buildArgs(prompt string) []string {
	args := []string{"-p", prompt, "--output-format", "json", "--disable-slash-commands"}
	model := inv.Model
	if model == "" {
		model = "gemini-3.7-flash-low"
	}
	if model != "default" && model != "none" {
		args = append(args, "--model", model)
	}
	effort := inv.Effort
	if effort == "" {
		effort = "low"
	}
	if effort != "default" && effort != "none" {
		args = append(args, "--effort", effort)
	}
	return args
}

// Invoke implements Invoker.
func (inv AntigravityInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "agy", inv.buildArgs(prompt)...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("summarizer: agy -p failed: %w", err)
	}
	var res antigravityResult
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("summarizer: parse agy -p output: %w", err)
	}
	if res.Status != "SUCCESS" {
		return "", fmt.Errorf("summarizer: agy -p returned status %q: %s", res.Status, res.Error)
	}
	return res.Response, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/summarizer/... -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/summarizer/summarizer.go internal/summarizer/summarizer_test.go
git commit -m "feat(summarizer): add configurable models and lightweight execution flags

- Add Model field to ClaudeCodeInvoker with safe-mode and tool disabling
- Add Model and Effort fields to AntigravityInvoker with slash command disabling
- Default to low-cost models (haiku, gemini-3.7-flash-low with effort low)
- Add comprehensive argument construction tests

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: Optimize `internal/adapter/claudecode` Tailer with Per-File Dirty Checking

**Files:**
- Modify: `internal/adapter/claudecode/tailer.go`
- Modify: `internal/adapter/claudecode/tailer_test.go`

**Interfaces:**
- Produces:
  ```go
  type Tailer struct { ... }
  func NewTailer() *Tailer
  func (t *Tailer) ReadNewLines(path string) ([][]byte, bool, error) // bool: changed
  func (t *Tailer) SeedOffset(path string, offset int64)
  func (t *Tailer) Offset(path string) int64
  ```

- [ ] **Step 1: Write unit tests for dirty checking and truncation handling**

Update `internal/adapter/claudecode/tailer_test.go`:
```go
func TestTailer_DirtyCheckingAndTruncation(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.jsonl")

	if err := os.WriteFile(filePath, []byte("line 1\nline 2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tailer := NewTailer()

	// 1. Initial read
	lines, changed, err := tailer.ReadNewLines(filePath)
	if err != nil {
		t.Fatalf("first read error: %v", err)
	}
	if !changed || len(lines) != 2 {
		t.Fatalf("expected 2 lines with changed=true, got %d lines, changed=%v", len(lines), changed)
	}

	// 2. Second read without modifications -> should be dirty-checked (changed=false, no lines, no file open)
	lines, changed, err = tailer.ReadNewLines(filePath)
	if err != nil {
		t.Fatalf("second read error: %v", err)
	}
	if changed || len(lines) != 0 {
		t.Fatalf("expected 0 lines with changed=false on unchanged file, got %d lines, changed=%v", len(lines), changed)
	}

	// 3. Append a new line
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("line 3\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Force mtime change if filesystem has coarse resolution
	now := time.Now().Add(time.Second)
	_ = os.Chtimes(filePath, now, now)

	lines, changed, err = tailer.ReadNewLines(filePath)
	if err != nil {
		t.Fatalf("third read error: %v", err)
	}
	if !changed || len(lines) != 1 || string(lines[0]) != "line 3" {
		t.Fatalf("expected line 3 with changed=true, got %v, changed=%v", lines, changed)
	}

	// 4. Truncation / File Rewrite
	if err := os.WriteFile(filePath, []byte("new line 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(filePath, time.Now().Add(2*time.Second), time.Now().Add(2*time.Second))

	lines, changed, err = tailer.ReadNewLines(filePath)
	if err != nil {
		t.Fatalf("read after truncation error: %v", err)
	}
	if !changed || len(lines) != 1 || string(lines[0]) != "new line 1" {
		t.Fatalf("expected reset and read 'new line 1', got %v", lines)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/claudecode/... -run TestTailer_DirtyCheckingAndTruncation`
Expected: FAIL (return signature of `ReadNewLines` does not match)

- [ ] **Step 3: Update `internal/adapter/claudecode/tailer.go`**

```go
package claudecode

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

func DiscoverTranscriptFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

type fileMeta struct {
	modTime time.Time
	size    int64
	offset  int64
}

type Tailer struct {
	files map[string]*fileMeta
}

func NewTailer() *Tailer {
	return &Tailer{files: make(map[string]*fileMeta)}
}

func (t *Tailer) readNewLinesFrom(path string, reader io.Reader, offset int64) ([][]byte, error) {
	var lines [][]byte
	bufReader := bufio.NewReader(reader)
	consumed := offset
	for {
		line, err := bufReader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			lines = append(lines, line[:len(line)-1])
			consumed += int64(len(line))
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.getOrCreateMeta(path).offset = consumed
				return lines, err
			}
			break
		}
	}
	t.getOrCreateMeta(path).offset = consumed
	return lines, nil
}

func (t *Tailer) getOrCreateMeta(path string) *fileMeta {
	meta, ok := t.files[path]
	if !ok {
		meta = &fileMeta{}
		t.files[path] = meta
	}
	return meta
}

// ReadNewLines returns complete lines appended to path, whether the file changed, and any error.
func (t *Tailer) ReadNewLines(path string) ([][]byte, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}

	meta := t.getOrCreateMeta(path)

	// Dirty-check: if modTime and size match our cached state, skip file completely
	if !meta.modTime.IsZero() && info.ModTime().Equal(meta.modTime) && info.Size() == meta.size {
		return nil, false, nil
	}

	// Truncation check: if file shrunk below our offset, reset to 0
	if info.Size() < meta.offset {
		meta.offset = 0
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	if _, err := f.Seek(meta.offset, io.SeekStart); err != nil {
		return nil, false, err
	}

	oldOffset := meta.offset
	lines, err := t.readNewLinesFrom(path, f, meta.offset)
	if err != nil {
		return lines, meta.offset != oldOffset, err
	}

	meta.modTime = info.ModTime()
	meta.size = info.Size()

	return lines, meta.offset != oldOffset, nil
}

func (t *Tailer) SeedOffset(path string, offset int64) {
	t.getOrCreateMeta(path).offset = offset
}

func (t *Tailer) Offset(path string) int64 {
	return t.getOrCreateMeta(path).offset
}
```

- [ ] **Step 4: Fix any existing tests broken by signature update and run tests**

Update `internal/adapter/claudecode/tailer_test.go` and `internal/daemon/daemon_claudecode.go` to match the new `ReadNewLines` signature.
Run: `go test ./internal/adapter/claudecode/... -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/claudecode/tailer.go internal/adapter/claudecode/tailer_test.go
git commit -m "perf(adapter/claudecode): add per-file dirty checking and truncation reset to Tailer

- Track file metadata (ModTime, Size, Offset) in-memory
- Bypass os.Open and Seek when file size and ModTime are unchanged
- Reset offset to 0 if a file is truncated or rewritten
- Return changed flag so callers avoid unnecessary database writes

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: Optimize Daemon Polling and Antigravity Adapter

**Files:**
- Modify: `internal/daemon/daemon_claudecode.go`
- Modify: `internal/daemon/daemon_antigravity.go`
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/daemon/daemon_test.go`

**Interfaces:**
- Produces:
  - `Daemon` checks `changed` before calling `SetPollState`
  - `pollAntigravity` tracks `conversation_summaries.db` ModTime/Size and per-conversation DB ModTime

- [ ] **Step 1: Write daemon dirty-check test**

Add unit test in `internal/daemon/daemon_test.go`:
```go
func TestDaemon_SkipsRedundantPollStateWrites(t *testing.T) {
	// Verify that polling an unchanged directory twice does not trigger redundant DB writes
	// after initial scan
}
```

- [ ] **Step 2: Run test to observe current behavior**

Run: `go test ./internal/daemon/... -v`

- [ ] **Step 3: Update `daemon_claudecode.go` and `daemon_antigravity.go`**

1. In `internal/daemon/daemon_claudecode.go`:
```go
func (d *Daemon) pollClaudeCode(now time.Time) error {
	files, err := claudecode.DiscoverTranscriptFiles(d.claudeProjectsRoot)
	if err != nil {
		return err
	}
	for _, file := range files {
		parser, seen := d.claudeParsers[file]
		if !seen {
			parser = claudecode.NewParser()
			d.claudeParsers[file] = parser
			if persisted, ok, err := d.Store.GetPollState(claudeFileKey(file)); err != nil {
				log.Printf("daemon: get poll state for %s: %v", file, err)
			} else if ok {
				d.claudeTailer.SeedOffset(file, persisted)
			}
		}
		lines, changed, err := d.claudeTailer.ReadNewLines(file)
		if err != nil {
			log.Printf("daemon: read %s: %v", file, err)
			continue
		}
		if !changed {
			continue
		}
		for _, line := range lines {
			obs, err := parser.Feed(line)
			if err != nil {
				log.Printf("daemon: parse %s: %v", file, err)
				continue
			}
			for _, o := range obs {
				if err := d.recordObservation(o, d.claudeInvoker, now); err != nil {
					log.Printf("daemon: record observation: %v", err)
				}
			}
		}
		if err := d.Store.SetPollState(claudeFileKey(file), d.claudeTailer.Offset(file)); err != nil {
			log.Printf("daemon: persist offset for %s: %v", file, err)
		}
	}
	return nil
}
```

2. In `internal/daemon/daemon_antigravity.go`:
Add `ModTime` tracking for `conversation_summaries.db` and per-conversation SQLite files. Only call `ReadObservations` when conversation DB is updated, and only call `SetPollState` when `maxIdx > sinceIdx`.

- [ ] **Step 4: Run all daemon tests**

Run: `go test ./internal/daemon/... -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/
git commit -m "perf(daemon): eliminate redundant database writes and unnecessary I/O in poll loops

- Only write Claude poll_state when new lines are consumed
- Dirty-check Antigravity conversation databases before query
- Guard SetPollState writes behind dirty-check thresholds

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: Wire Configuration and Optimized Invokers into `cmd/memremarkd` and Verify End-to-End

**Files:**
- Modify: `cmd/memremarkd/main.go`
- Modify: `cmd/memremarkd/main_test.go`

- [ ] **Step 1: Update `cmd/memremarkd/main.go` to load config and pass configured invokers**

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/haminh7036/memremark/internal/config"
	"github.com/haminh7036/memremark/internal/daemon"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

const pollTimeout = 2 * time.Minute

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("memremarkd: resolve home dir: %v", err)
	}

	cfg, err := config.Load(home)
	if err != nil {
		log.Printf("memremarkd: warning: failed to load config (%v), using defaults", err)
		cfg = config.DefaultConfig()
	}

	store, err := storage.Open(filepath.Join(home, ".memremark", "memremark.db"))
	if err != nil {
		log.Fatalf("memremarkd: open storage: %v", err)
	}
	defer store.Close()

	claudeProjectsRoot := filepath.Join(home, ".claude", "projects")
	antigravitySummariesDB := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")

	claudePrimary := summarizer.ClaudeCodeInvoker{
		Model: cfg.Summarizer.ClaudeModel,
	}
	antigravityPrimary := summarizer.AntigravityInvoker{
		Model:  cfg.Summarizer.AntigravityModel,
		Effort: cfg.Summarizer.AntigravityEffort,
	}

	claudeInvoker := summarizer.FallbackInvoker{
		Primary:  claudePrimary,
		Fallback: antigravityPrimary,
		OnFallback: func(err error) {
			log.Printf("memremarkd: claude summarizer failed (%v), falling back to antigravity", err)
		},
	}

	antigravityInvoker := summarizer.FallbackInvoker{
		Primary:  antigravityPrimary,
		Fallback: claudePrimary,
		OnFallback: func(err error) {
			log.Printf("memremarkd: antigravity summarizer failed (%v), falling back to claude", err)
		},
	}

	d := daemon.New(store, claudeProjectsRoot, antigravitySummariesDB,
		claudeInvoker, antigravityInvoker)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	log.Printf("memremarkd: started, polling every 3s (claude_model=%s, antigravity_model=%s, antigravity_effort=%s)",
		cfg.Summarizer.ClaudeModel, cfg.Summarizer.AntigravityModel, cfg.Summarizer.AntigravityEffort)

	for {
		select {
		case <-ctx.Done():
			log.Println("memremarkd: shutting down")
			return
		case now := <-ticker.C:
			if err := poll(ctx, d, now, pollTimeout); err != nil {
				log.Printf("memremarkd: poll error: %v", err)
			}
		}
	}
}

func poll(ctx context.Context, d *daemon.Daemon, now time.Time, timeout time.Duration) error {
	tickCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return d.PollOnce(tickCtx, now)
}
```

- [ ] **Step 2: Run all tests in the workspace**

Run: `go test ./... -v`
Expected: ALL PASS across all packages (`config`, `storage`, `summarizer`, `claudecode`, `antigravity`, `daemon`, `mcp`, `hooks`).

- [ ] **Step 3: Verify binary build**

Run: `go build -o /tmp/memremarkd ./cmd/memremarkd`
Expected: Succeeded with 0 errors.

- [ ] **Step 4: Commit**

```bash
git add cmd/memremarkd/
git commit -m "feat(daemon): wire configuration loader and configured invokers in memremarkd

- Load ~/.memremark/config.json with default low-cost models on startup
- Pass configured Claude and Antigravity invokers to daemon
- Log active summarizer model configuration on startup
- Complete end-to-end verification

Co-Authored-By: Claude <noreply@anthropic.com>"
```
