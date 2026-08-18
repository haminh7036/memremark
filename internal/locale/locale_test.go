package locale

import (
	"os"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		input    string
		wantCode string
		wantName string
	}{
		{"", "en", "English"},
		{"auto", "en", "English"},
		{"AUTO", "en", "English"},
		{"vi", "vi", "Vietnamese"},
		{"VI", "vi", "Vietnamese"},
		{"vi_VN", "vi", "Vietnamese"},
		{"vi_VN.UTF-8", "vi", "Vietnamese"},
		{"vietnamese", "vi", "Vietnamese"},
		{"Vietnamese", "vi", "Vietnamese"},
		{"ja", "ja", "Japanese"},
		{"ja_JP", "ja", "Japanese"},
		{"ja_JP.eucJP", "ja", "Japanese"},
		{"japanese", "ja", "Japanese"},
		{"zh", "zh", "Chinese"},
		{"zh_CN", "zh", "Chinese"},
		{"zh_TW", "zh", "Chinese"},
		{"chinese", "zh", "Chinese"},
		{"ko", "ko", "Korean"},
		{"fr", "fr", "French"},
		{"de", "de", "German"},
		{"es", "es", "Spanish"},
		{"pt", "pt", "Portuguese"},
		{"ru", "ru", "Russian"},
		{"en", "en", "English"},
		{"en_US.UTF-8", "en", "English"},
		{"C", "en", "English"},
		{"POSIX", "en", "English"},
		{"it", "it", "It"},
		{"italian", "italian", "Italian"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Normalize(tt.input)
			if got.Code != tt.wantCode || got.Name != tt.wantName {
				t.Errorf("Normalize(%q) = %+v, want Code=%q, Name=%q", tt.input, got, tt.wantCode, tt.wantName)
			}
		})
	}
}

func TestDetectLanguage_Precedence(t *testing.T) {
	// Clear all env vars before testing
	origMemremark := os.Getenv("MEMREMARK_LANGUAGE")
	origLcAll := os.Getenv("LC_ALL")
	origLcMessages := os.Getenv("LC_MESSAGES")
	origLang := os.Getenv("LANG")
	defer func() {
		os.Setenv("MEMREMARK_LANGUAGE", origMemremark)
		os.Setenv("LC_ALL", origLcAll)
		os.Setenv("LC_MESSAGES", origLcMessages)
		os.Setenv("LANG", origLang)
	}()

	os.Unsetenv("MEMREMARK_LANGUAGE")
	os.Unsetenv("LC_ALL")
	os.Unsetenv("LC_MESSAGES")
	os.Unsetenv("LANG")

	// 1. All empty -> fallback English
	got := DetectLanguage("")
	if got.Code != "en" || got.Name != "English" {
		t.Fatalf("expected fallback English, got %+v", got)
	}

	// 2. $LANG set -> respects $LANG
	os.Setenv("LANG", "vi_VN.UTF-8")
	got = DetectLanguage("")
	if got.Code != "vi" || got.Name != "Vietnamese" {
		t.Fatalf("expected Vietnamese from $LANG, got %+v", got)
	}

	// 3. $LC_ALL overrides $LANG
	os.Setenv("LC_ALL", "ja_JP.UTF-8")
	got = DetectLanguage("")
	if got.Code != "ja" || got.Name != "Japanese" {
		t.Fatalf("expected Japanese from $LC_ALL, got %+v", got)
	}

	// 4. MEMREMARK_LANGUAGE overrides $LC_ALL
	os.Setenv("MEMREMARK_LANGUAGE", "fr")
	got = DetectLanguage("")
	if got.Code != "fr" || got.Name != "French" {
		t.Fatalf("expected French from MEMREMARK_LANGUAGE, got %+v", got)
	}

	// 5. Configured parameter overrides MEMREMARK_LANGUAGE
	got = DetectLanguage("de")
	if got.Code != "de" || got.Name != "German" {
		t.Fatalf("expected German from configured param, got %+v", got)
	}

	// 6. Configured "auto" falls back to MEMREMARK_LANGUAGE / env vars
	got = DetectLanguage("auto")
	if got.Code != "fr" || got.Name != "French" {
		t.Fatalf("expected French when configured=auto with MEMREMARK_LANGUAGE=fr, got %+v", got)
	}
}
