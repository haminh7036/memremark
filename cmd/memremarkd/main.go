package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/haminh7036/memremark/internal/config"
	"github.com/haminh7036/memremark/internal/daemon"
	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

// pollTimeout bounds one poll cycle so a hung headless CLI invocation
// (`claude -p` / `agy -p` stuck on an auth prompt, a network hang, a stuck
// subprocess) can never wedge the daemon forever: exec.CommandContext kills
// the subprocess once this context's deadline fires. A poll cycle may
// include several real CLI invocations if multiple sessions go idle in the
// same tick, so this is generous, not tight.
const pollTimeout = 2 * time.Minute

// InvokerSetup holds the configured summarizer invokers and startup status message.
type InvokerSetup struct {
	ClaudeInvoker      summarizer.Invoker
	AntigravityInvoker summarizer.Invoker
	Summary            string
}

// resolveInvokers determines the appropriate summarizer invokers based on
// provider configuration, API keys, and which AI assistant CLI binaries exist
// in PATH at daemon startup.
func resolveInvokers(cfg config.Config, lookPath func(string) (string, error)) InvokerSetup {
	_, errClaude := lookPath("claude")
	_, errAgy := lookPath("agy")

	hasClaude := errClaude == nil
	hasAgy := errAgy == nil

	claudePrimary := summarizer.ClaudeCodeInvoker{
		Model: cfg.Summarizer.ClaudeModel,
	}
	antigravityPrimary := summarizer.AntigravityInvoker{
		Model:  cfg.Summarizer.AntigravityModel,
		Effort: cfg.Summarizer.AntigravityEffort,
	}

	geminiInvoker := summarizer.GeminiAPIInvoker{
		APIKey: cfg.Summarizer.GeminiAPIKey,
		Model:  cfg.Summarizer.GeminiModel,
	}
	anthropicInvoker := summarizer.AnthropicAPIInvoker{
		APIKey: cfg.Summarizer.AnthropicAPIKey,
		Model:  cfg.Summarizer.AnthropicModel,
	}

	hasGeminiKey := cfg.Summarizer.GeminiAPIKey != ""
	hasAnthropicKey := cfg.Summarizer.AnthropicAPIKey != ""

	// Explicit CLI provider configurations take absolute precedence over API keys
	if cfg.Summarizer.Provider == "antigravity" {
		if hasClaude && hasAgy {
			invoker := summarizer.FallbackInvoker{
				Primary:  antigravityPrimary,
				Fallback: claudePrimary,
				OnFallback: func(err error) {
					log.Printf("memremarkd: antigravity summarizer failed (%v), falling back to claude", err)
				},
			}
			return InvokerSetup{
				ClaudeInvoker:      invoker,
				AntigravityInvoker: invoker,
				Summary:            "active invokers: agy (primary, provider configured) + claude (fallback)",
			}
		}
		if hasAgy {
			return InvokerSetup{
				ClaudeInvoker:      antigravityPrimary,
				AntigravityInvoker: antigravityPrimary,
				Summary:            "active invokers: agy only (claude not found in PATH)",
			}
		}
		if hasClaude {
			return InvokerSetup{
				ClaudeInvoker:      claudePrimary,
				AntigravityInvoker: claudePrimary,
				Summary:            "active invokers: claude only (agy not found in PATH)",
			}
		}
		return InvokerSetup{
			ClaudeInvoker:      summarizer.NopInvoker{},
			AntigravityInvoker: summarizer.NopInvoker{},
			Summary:            "warning: neither claude nor agy found in PATH; summarization disabled",
		}
	}

	if cfg.Summarizer.Provider == "claude" {
		if hasClaude && hasAgy {
			invoker := summarizer.FallbackInvoker{
				Primary:  claudePrimary,
				Fallback: antigravityPrimary,
				OnFallback: func(err error) {
					log.Printf("memremarkd: claude summarizer failed (%v), falling back to antigravity", err)
				},
			}
			return InvokerSetup{
				ClaudeInvoker:      invoker,
				AntigravityInvoker: invoker,
				Summary:            "active invokers: claude (primary, provider configured) + agy (fallback)",
			}
		}
		if hasClaude {
			return InvokerSetup{
				ClaudeInvoker:      claudePrimary,
				AntigravityInvoker: claudePrimary,
				Summary:            "active invokers: claude only (agy not found in PATH)",
			}
		}
		if hasAgy {
			return InvokerSetup{
				ClaudeInvoker:      antigravityPrimary,
				AntigravityInvoker: antigravityPrimary,
				Summary:            "active invokers: agy only (claude not found in PATH)",
			}
		}
		return InvokerSetup{
			ClaudeInvoker:      summarizer.NopInvoker{},
			AntigravityInvoker: summarizer.NopInvoker{},
			Summary:            "warning: neither claude nor agy found in PATH; summarization disabled",
		}
	}

	// Prepare fallback CLI chains for Claude and Antigravity sessions
	var claudeBase summarizer.Invoker
	var agyBase summarizer.Invoker
	var cliSummary string

	if hasClaude && hasAgy {
		claudeBase = summarizer.FallbackInvoker{
			Primary:  claudePrimary,
			Fallback: antigravityPrimary,
			OnFallback: func(err error) {
				log.Printf("memremarkd: claude summarizer failed (%v), falling back to antigravity", err)
			},
		}
		agyBase = summarizer.FallbackInvoker{
			Primary:  antigravityPrimary,
			Fallback: claudePrimary,
			OnFallback: func(err error) {
				log.Printf("memremarkd: antigravity summarizer failed (%v), falling back to claude", err)
			},
		}
		cliSummary = "claude (primary/fallback) + agy (primary/fallback)"
	} else if hasClaude {
		claudeBase = claudePrimary
		agyBase = claudePrimary
		cliSummary = "claude only (agy not found in PATH)"
	} else if hasAgy {
		claudeBase = antigravityPrimary
		agyBase = antigravityPrimary
		cliSummary = "agy only (claude not found in PATH)"
	} else {
		claudeBase = summarizer.NopInvoker{}
		agyBase = summarizer.NopInvoker{}
		cliSummary = "neither claude nor agy found in PATH"
	}

	if cfg.Summarizer.Provider == "gemini" {
		if hasAnthropicKey {
			claudeFallback := summarizer.FallbackInvoker{
				Primary:  anthropicInvoker,
				Fallback: claudeBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: anthropic API failed (%v), falling back to CLI", err)
				},
			}
			agyFallback := summarizer.FallbackInvoker{
				Primary:  anthropicInvoker,
				Fallback: agyBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: anthropic API failed (%v), falling back to CLI", err)
				},
			}
			return InvokerSetup{
				ClaudeInvoker: summarizer.FallbackInvoker{
					Primary:  geminiInvoker,
					Fallback: claudeFallback,
					OnFallback: func(err error) {
						log.Printf("memremarkd: gemini API failed (%v), falling back to anthropic API", err)
					},
				},
				AntigravityInvoker: summarizer.FallbackInvoker{
					Primary:  geminiInvoker,
					Fallback: agyFallback,
					OnFallback: func(err error) {
						log.Printf("memremarkd: gemini API failed (%v), falling back to anthropic API", err)
					},
				},
				Summary: fmt.Sprintf("active invokers: gemini API (primary, provider configured) + anthropic API (fallback) + CLI fallback (%s)", cliSummary),
			}
		}
		return InvokerSetup{
			ClaudeInvoker: summarizer.FallbackInvoker{
				Primary:  geminiInvoker,
				Fallback: claudeBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: gemini API failed (%v), falling back to CLI", err)
				},
			},
			AntigravityInvoker: summarizer.FallbackInvoker{
				Primary:  geminiInvoker,
				Fallback: agyBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: gemini API failed (%v), falling back to CLI", err)
				},
			},
			Summary: fmt.Sprintf("active invokers: gemini API (primary, provider configured) + CLI fallback (%s)", cliSummary),
		}
	}

	if cfg.Summarizer.Provider == "anthropic" {
		if hasGeminiKey {
			claudeFallback := summarizer.FallbackInvoker{
				Primary:  geminiInvoker,
				Fallback: claudeBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: gemini API failed (%v), falling back to CLI", err)
				},
			}
			agyFallback := summarizer.FallbackInvoker{
				Primary:  geminiInvoker,
				Fallback: agyBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: gemini API failed (%v), falling back to CLI", err)
				},
			}
			return InvokerSetup{
				ClaudeInvoker: summarizer.FallbackInvoker{
					Primary:  anthropicInvoker,
					Fallback: claudeFallback,
					OnFallback: func(err error) {
						log.Printf("memremarkd: anthropic API failed (%v), falling back to gemini API", err)
					},
				},
				AntigravityInvoker: summarizer.FallbackInvoker{
					Primary:  anthropicInvoker,
					Fallback: agyFallback,
					OnFallback: func(err error) {
						log.Printf("memremarkd: anthropic API failed (%v), falling back to gemini API", err)
					},
				},
				Summary: fmt.Sprintf("active invokers: anthropic API (primary, provider configured) + gemini API (fallback) + CLI fallback (%s)", cliSummary),
			}
		}
		return InvokerSetup{
			ClaudeInvoker: summarizer.FallbackInvoker{
				Primary:  anthropicInvoker,
				Fallback: claudeBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: anthropic API failed (%v), falling back to CLI", err)
				},
			},
			AntigravityInvoker: summarizer.FallbackInvoker{
				Primary:  anthropicInvoker,
				Fallback: agyBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: anthropic API failed (%v), falling back to CLI", err)
				},
			},
			Summary: fmt.Sprintf("active invokers: anthropic API (primary, provider configured) + CLI fallback (%s)", cliSummary),
		}
	}

	// Provider == "auto" (default) or unrecognized
	if hasGeminiKey {
		if hasAnthropicKey {
			claudeFallback := summarizer.FallbackInvoker{
				Primary:  anthropicInvoker,
				Fallback: claudeBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: anthropic API failed (%v), falling back to CLI", err)
				},
			}
			agyFallback := summarizer.FallbackInvoker{
				Primary:  anthropicInvoker,
				Fallback: agyBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: anthropic API failed (%v), falling back to CLI", err)
				},
			}
			return InvokerSetup{
				ClaudeInvoker: summarizer.FallbackInvoker{
					Primary:  geminiInvoker,
					Fallback: claudeFallback,
					OnFallback: func(err error) {
						log.Printf("memremarkd: gemini API failed (%v), falling back to anthropic API", err)
					},
				},
				AntigravityInvoker: summarizer.FallbackInvoker{
					Primary:  geminiInvoker,
					Fallback: agyFallback,
					OnFallback: func(err error) {
						log.Printf("memremarkd: gemini API failed (%v), falling back to anthropic API", err)
					},
				},
				Summary: fmt.Sprintf("active invokers: gemini API (primary) + anthropic API (fallback) + CLI fallback (%s)", cliSummary),
			}
		}
		return InvokerSetup{
			ClaudeInvoker: summarizer.FallbackInvoker{
				Primary:  geminiInvoker,
				Fallback: claudeBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: gemini API failed (%v), falling back to CLI", err)
				},
			},
			AntigravityInvoker: summarizer.FallbackInvoker{
				Primary:  geminiInvoker,
				Fallback: agyBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: gemini API failed (%v), falling back to CLI", err)
				},
			},
			Summary: fmt.Sprintf("active invokers: gemini API (primary) + CLI fallback (%s)", cliSummary),
		}
	}

	if hasAnthropicKey {
		return InvokerSetup{
			ClaudeInvoker: summarizer.FallbackInvoker{
				Primary:  anthropicInvoker,
				Fallback: claudeBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: anthropic API failed (%v), falling back to CLI", err)
				},
			},
			AntigravityInvoker: summarizer.FallbackInvoker{
				Primary:  anthropicInvoker,
				Fallback: agyBase,
				OnFallback: func(err error) {
					log.Printf("memremarkd: anthropic API failed (%v), falling back to CLI", err)
				},
			},
			Summary: fmt.Sprintf("active invokers: anthropic API (primary) + CLI fallback (%s)", cliSummary),
		}
	}

	// No API keys present: keep existing CLI discovery and fallback unchanged
	if hasClaude && hasAgy {
		return InvokerSetup{
			ClaudeInvoker:      claudeBase,
			AntigravityInvoker: agyBase,
			Summary:            "active invokers: claude (primary/fallback) + agy (primary/fallback)",
		}
	}

	if hasClaude {
		return InvokerSetup{
			ClaudeInvoker:      claudePrimary,
			AntigravityInvoker: claudePrimary,
			Summary:            "active invokers: claude only (agy not found in PATH)",
		}
	}

	if hasAgy {
		return InvokerSetup{
			ClaudeInvoker:      antigravityPrimary,
			AntigravityInvoker: antigravityPrimary,
			Summary:            "active invokers: agy only (claude not found in PATH)",
		}
	}

	return InvokerSetup{
		ClaudeInvoker:      summarizer.NopInvoker{},
		AntigravityInvoker: summarizer.NopInvoker{},
		Summary:            "warning: neither claude nor agy found in PATH; summarization disabled",
	}
}

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

	dbPath := os.Getenv("MEMREMARK_DB")
	if dbPath == "" {
		dbPath = filepath.Join(home, ".memremark", "memremark.db")
	}

	store, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("memremarkd: open storage: %v", err)
	}
	defer store.Close()

	claudeProjectsRoot := os.Getenv("MEMREMARK_CLAUDE_PROJECTS")
	if claudeProjectsRoot == "" {
		claudeProjectsRoot = filepath.Join(home, ".claude", "projects")
	}
	antigravitySummariesDB := os.Getenv("MEMREMARK_ANTIGRAVITY_DB")
	if antigravitySummariesDB == "" {
		antigravitySummariesDB = filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	}

	targetLang := locale.DetectLanguage(cfg.Language)
	log.Printf("memremarkd: target locale '%s' (%s)", targetLang.Code, targetLang.Name)

	setup := resolveInvokers(cfg, exec.LookPath)
	log.Printf("memremarkd: %s", setup.Summary)

	d := daemon.New(store, claudeProjectsRoot, antigravitySummariesDB,
		setup.ClaudeInvoker, setup.AntigravityInvoker, targetLang)

	if err := d.Warmup(); err != nil {
		log.Printf("memremarkd: warmup (recover orphaned verbatim sessions) failed: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	log.Printf("memremarkd: started, polling every 3s (provider=%s, gemini_model=%s, anthropic_model=%s, claude_model=%s, antigravity_model=%s, antigravity_effort=%s)",
		cfg.Summarizer.Provider, cfg.Summarizer.GeminiModel, cfg.Summarizer.AnthropicModel, cfg.Summarizer.ClaudeModel, cfg.Summarizer.AntigravityModel, cfg.Summarizer.AntigravityEffort)

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

// poll runs one bounded poll cycle: a fresh timeout context derived per
// tick, instead of one context.Background() reused for the process's whole
// lifetime, so a single hung subprocess call inside PollOnce can't wedge
// every future tick too.
func poll(ctx context.Context, d *daemon.Daemon, now time.Time, timeout time.Duration) error {
	tickCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return d.PollOnce(tickCtx, now)
}
