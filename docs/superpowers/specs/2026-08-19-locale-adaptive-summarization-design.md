# Design Specification: Locale-Adaptive Knowledge Distillation & Multi-Language Context Injection

**Date:** 2026-08-19  
**Status:** Approved  
**Version Target:** `v0.1.2` (Patch on Phase 1)  

---

## 1. Overview & Motivation

MemRemark distills developer sessions into structured memory drawers (`fact`, `discovery`, `preference`, `advice`) and injects them into future sessions. Previously, the summarization prompt and context injection headers were hardcoded in Vietnamese.

While functional, this hardcoded approach has limitations:
1. International developers or environments with English/other locales receive prompts mismatched to their language preferences.
2. Conversely, completely English prompts cause distilled memory notes to be written entirely in English, requiring translation when viewed on the Web Dashboard or injected into native-language sessions.

This specification designs **Locale-Adaptive Knowledge Distillation & Context Injection**:
- Automatically detects the user's country/language environment via system locale (`$LC_ALL`, `$LC_MESSAGES`, `$LANG`).
- Supports explicit overrides via `~/.memremark/config.json` (`"language": "vi"`, `"ja"`, `"en"`, `"auto"`, etc.) and `MEMREMARK_LANGUAGE`.
- Uses an English-framed, token-optimized system prompt with strict schema enforcement, explicit output language directives, and dual-layer technical term preservation rules.
- Localizes context injection headers in CLI hooks (`SessionStart`, `PreInvocation`).

---

## 2. Architecture & Components

```
                +----------------------------------------+
                |        System Environment & Config     |
                |  - ~/.memremark/config.json ("language")|
                |  - $MEMREMARK_LANGUAGE                 |
                |  - $LC_ALL / $LC_MESSAGES / $LANG      |
                +-------------------+--------------------+
                                    |
                                    v
                       +-------------------------+
                       |     internal/locale     |
                       |  DetectLanguage(cfgLang)|
                       +------------+------------+
                                    | TargetLanguage{Code, Name}
                  +-----------------+-----------------+
                  |                                   |
                  v                                   v
    +---------------------------+       +---------------------------+
    |    internal/summarizer    |       |     internal/hookctx      |
    |  buildPrompt(obs, lang)   |       | FormatSummaries(draw,lang)|
    +---------------------------+       +---------------------------+
                  |                                   |
                  v                                   v
    +---------------------------+       +---------------------------+
    | Background Daemon LLM     |       | Claude / Antigravity CLI  |
    | (haiku / gemini-flash)    |       | Session Context Injection |
    +---------------------------+       +---------------------------+
```

### 2.1 Module: `internal/locale`

A dedicated, lightweight package for locale detection and normalization:

```go
package locale

type TargetLanguage struct {
    Code string // e.g., "vi", "en", "ja", "zh", "ko", "fr", "de", "es", "pt", "ru"
    Name string // e.g., "Vietnamese", "English", "Japanese", "Chinese", ...
}

// DetectLanguage resolves the target language with precedence:
// 1. Explicit configured string (if not empty and not "auto")
// 2. $MEMREMARK_LANGUAGE environment variable
// 3. $LC_ALL / $LC_MESSAGES / $LANG environment variables
// 4. Default fallback: TargetLanguage{Code: "en", Name: "English"}
func DetectLanguage(configuredLang string) TargetLanguage

// Normalize maps aliases ("vi", "vi_VN", "vietnamese", "VI") to canonical TargetLanguage.
func Normalize(raw string) TargetLanguage
```

#### Supported Core Locales (Built-in Dictionary):
| Code | Canonical Name | Aliases & Patterns |
| :--- | :--- | :--- |
| `vi` | `Vietnamese` | `vi`, `vi_vn`, `vietnamese` |
| `en` | `English` | `en`, `en_us`, `en_gb`, `c`, `posix`, `english` |
| `ja` | `Japanese` | `ja`, `ja_jp`, `japanese` |
| `zh` | `Chinese` | `zh`, `zh_cn`, `zh_tw`, `chinese` |
| `ko` | `Korean` | `ko`, `ko_kr`, `korean` |
| `fr` | `French` | `fr`, `fr_fr`, `french` |
| `de` | `German` | `de`, `de_de`, `german` |
| `es` | `Spanish` | `es`, `es_es`, `spanish` |
| `pt` | `Portuguese` | `pt`, `pt_br`, `pt_pt`, `portuguese` |
| `ru` | `Russian` | `ru`, `ru_ru`, `russian` |

*Any unrecognized custom string is passed directly as `TargetLanguage{Code: "custom", Name: raw}`.*

---

### 2.2 Configuration (`internal/config`)

`config.Config` is extended with the `language` field:

```go
type Config struct {
    Language   string           `json:"language"` // "auto" (default), "vi", "en", "ja", etc.
    Summarizer SummarizerConfig `json:"summarizer"`
}
```

- Default value: `"auto"`.
- Environment variable `MEMREMARK_LANGUAGE` overrides the file config when present.

---

### 2.3 Prompt Design (`internal/summarizer`)

The system prompt framing is kept in English to maximize schema compliance on small models (`haiku`, `gemini-3.7-flash-low`) while enforcing target language output and dual-layer term preservation:

```text
Given the following raw tool observations from a coding session, distill them into concise memory items.
Each item must belong to one of 4 halls:
- fact (settled architectural decisions, conventions, invariants)
- discovery (new findings, root causes, investigation results)
- preference (user habits, workflow choices, preferences)
- advice (actionable recommendations, solutions to pitfalls)

Rules:
1. Output language: Write the "content" field in {TargetLanguage.Name} (e.g. Vietnamese). Use natural, standard technical terminology appropriate for {TargetLanguage.Name} (e.g. Katakana for Japanese, standard IT terms for Chinese, or common English terms where standard).
2. Strict code preservation: ALWAYS keep code identifiers, file paths, tool/command names, CLI flags, package names, and symbols in their exact original form (e.g., `main.go`, `go test -race`, `SQLite`, `memremarkd`).
3. Style: Write direct, concise, telegraphic bullet points. Avoid filler words.
4. Format: Respond ONLY with a valid JSON array of objects: [{"hall":"...","content":"..."}]. If nothing is worth memorizing, return [].

Observations:
- [ToolName] Observation content
```

---

### 2.4 Context Injection Header (`internal/hookctx`)

`hookctx.FormatSummaries` accepts `TargetLanguage` (or derives it) and renders localized headers:
- `vi`: `"Bối cảnh từ các phiên làm việc trước (memremark):\n"`
- `ja`: `"過去のセッションからのコンテキスト (memremark):\n"`
- `zh`: `"来自先前会话的上下文 (memremark):\n"`
- `en` / default: `"Context from prior sessions (memremark):\n"`

---

## 3. Decision Log

1. **Package Separation**: Created `internal/locale` as an independent package to prevent circular dependencies between `config`, `summarizer`, and `hookctx`.
2. **English Prompt Framing with Localized Output Directive**: Kept prompt structure in English for optimal token efficiency (~30% fewer tokens than pure non-English prompts) and maximum JSON validity on budget models.
3. **Dual-Layer Term Preservation**: Allowed natural localized terminology (e.g., Katakana in Japanese, standard Chinese technical terms) while strictly preserving code symbols, commands, and file paths in Latin/English.
4. **Forgiving Normalizer**: Accepts 2-letter codes (`vi`), full locale strings (`vi_VN.UTF-8`), and case-insensitive English names (`vietnamese`).
5. **Zero-Config Default**: Default `"language": "auto"` works out of the box with zero user configuration required.

---

## 4. Verification & Testing Plan

1. **`internal/locale` Unit Tests**:
   - Extraction of ISO 639-1 code from complex `$LANG` strings (`vi_VN.UTF-8`, `ja_JP.eucJP`, `en_US.UTF-8`, `C`, `POSIX`).
   - Normalization of aliases (`vi`, `VI`, `vietnamese`, `Vietnamese`).
   - Precedence order (`config.json` > `MEMREMARK_LANGUAGE` > `$LANG` > fallback `en`).
2. **`internal/summarizer` Unit Tests**:
   - Verify `buildPrompt` correctly interpolates `{TargetLanguage.Name}`.
   - Verify `parseSummaryItems` parses multi-byte Unicode JSON responses (Vietnamese diacritics, CJK characters) without corruption.
3. **`internal/hookctx` Unit Tests**:
   - Verify header rendering across `vi`, `ja`, `zh`, and fallback `en`.
4. **End-to-End Build & Race Check**:
   - `go test -v -race ./...` (100% pass).
   - `make build` (clean compilation).
