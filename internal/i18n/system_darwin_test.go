package i18n

import (
	"slices"
	"testing"
)

func TestParseAppleLanguages(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{"(\n    \"zh-Hans-CN\",\n    \"en-CN\"\n)\n", []string{"zh-Hans-CN", "en-CN"}},
		{"(\n    en,\n    \"ja-JP\"\n)", []string{"en", "ja-JP"}},
		{"(\n)", nil},
		{"", nil},
		{"The domain/default pair of (kCFPreferencesAnyApplication, AppleLanguages) does not exist", nil},
	} {
		if got := parseAppleLanguages(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("parseAppleLanguages(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeLanguages(t *testing.T) {
	got := normalizeLanguages([]string{"zh_Hans_CN", " en-US ", ""})
	if want := []string{"zh-Hans-CN", "en-US"}; !slices.Equal(got, want) {
		t.Errorf("normalizeLanguages = %q, want %q", got, want)
	}
}

// TestSystemLanguages reads this Mac's languages (read-only).
func TestSystemLanguages(t *testing.T) {
	langs := systemLanguages()
	if len(langs) == 0 {
		t.Skip("no preferred languages")
	}
	t.Logf("preferred languages: %q", langs)
}

// TestMacLanguageMatching is how macOS preferred languages resolve.
func TestMacLanguageMatching(t *testing.T) {
	m := newTestManager(t, "en")
	for _, tt := range []struct {
		prefs []string
		want  string
	}{
		{[]string{"zh-Hans-CN", "en-CN"}, "zh-CN"},
		{[]string{"zh-Hant-TW", "zh-Hans-CN"}, "zh-CN"},
		{[]string{"en-CN", "zh-Hans-CN"}, Source},
		{[]string{"en"}, Source},
	} {
		if got := m.match(normalizeLanguages(tt.prefs)); got != tt.want {
			t.Errorf("match(%q) = %q, want %q", tt.prefs, got, tt.want)
		}
	}
}
