package locale

import (
	"os"
	"strings"
)

// TargetLanguage represents the detected target language for summarization and context.
type TargetLanguage struct {
	Code string // "vi", "en", "ja", "zh", "ko", "fr", "de", "es", "pt", "ru", or custom
	Name string // "Vietnamese", "English", "Japanese", "Chinese", ...
}

var knownLanguages = map[string]TargetLanguage{
	"vi": {Code: "vi", Name: "Vietnamese"},
	"en": {Code: "en", Name: "English"},
	"ja": {Code: "ja", Name: "Japanese"},
	"zh": {Code: "zh", Name: "Chinese"},
	"ko": {Code: "ko", Name: "Korean"},
	"fr": {Code: "fr", Name: "French"},
	"de": {Code: "de", Name: "German"},
	"es": {Code: "es", Name: "Spanish"},
	"pt": {Code: "pt", Name: "Portuguese"},
	"ru": {Code: "ru", Name: "Russian"},
}

var defaultLanguage = TargetLanguage{Code: "en", Name: "English"}

// Normalize maps aliases, ISO codes, and full locale strings to a TargetLanguage.
func Normalize(raw string) TargetLanguage {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "auto") {
		return defaultLanguage
	}

	// Clean locale suffix (e.g., ".UTF-8", "@euro")
	clean := raw
	if idx := strings.IndexAny(clean, ".@"); idx != -1 {
		clean = clean[:idx]
	}

	lower := strings.ToLower(clean)

	// Direct match with known ISO-639-1 code
	if target, ok := knownLanguages[lower]; ok {
		return target
	}

	// Match prefixes like "vi_vn", "en_us", "ja_jp", "zh_cn"
	parts := strings.Split(lower, "_")
	if len(parts) > 0 {
		if target, ok := knownLanguages[parts[0]]; ok {
			return target
		}
	}

	// Match full English language names
	switch lower {
	case "vietnamese":
		return knownLanguages["vi"]
	case "english", "c", "posix":
		return knownLanguages["en"]
	case "japanese":
		return knownLanguages["ja"]
	case "chinese":
		return knownLanguages["zh"]
	case "korean":
		return knownLanguages["ko"]
	case "french":
		return knownLanguages["fr"]
	case "german":
		return knownLanguages["de"]
	case "spanish":
		return knownLanguages["es"]
	case "portuguese":
		return knownLanguages["pt"]
	case "russian":
		return knownLanguages["ru"]
	default:
		// Capitalize first letter for custom names
		name := raw
		if len(name) > 0 {
			name = strings.ToUpper(name[:1]) + strings.ToLower(name[1:])
		}
		return TargetLanguage{
			Code: lower,
			Name: name,
		}
	}
}

// DetectLanguage resolves the language from config, MEMREMARK_LANGUAGE, or system locale env vars.
func DetectLanguage(configuredLang string) TargetLanguage {
	if configuredLang != "" && !strings.EqualFold(configuredLang, "auto") {
		return Normalize(configuredLang)
	}

	if env := os.Getenv("MEMREMARK_LANGUAGE"); env != "" && !strings.EqualFold(env, "auto") {
		return Normalize(env)
	}

	for _, envVar := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if val := os.Getenv(envVar); val != "" && !strings.EqualFold(val, "auto") {
			return Normalize(val)
		}
	}

	return defaultLanguage
}
