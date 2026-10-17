package i18n

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func newTestManager(t *testing.T, locale string) *Manager {
	t.Helper()
	m := NewManager(Source, nil)
	if err := m.Register(CoreNamespace, CoreLocales()); err != nil {
		t.Fatal(err)
	}
	err := m.Register("p", fstest.MapFS{
		"en.json": {Data: []byte(`{"hello": "Hello, {{.name}}!", "items": {"one": "{{.Count}} item", "other": "{{.Count}} items"}}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	m.SetLocale(locale)
	return m
}

func TestTranslate(t *testing.T) {
	l := newTestManager(t, "en").Localizer("p")
	if got := l.T("hello", "name", "Ada"); got != "Hello, Ada!" {
		t.Errorf("T = %q", got)
	}
	if got := l.N("items", 1); got != "1 item" {
		t.Errorf("N(1) = %q", got)
	}
	if got := l.N("items", 3); got != "3 items" {
		t.Errorf("N(3) = %q", got)
	}
	// Plugin localizers fall back to core messages, then to the key itself.
	if got := l.T("time.justNow"); got != "Just now" {
		t.Errorf("core fallback = %q", got)
	}
	if got := l.T("no.such.key"); got != "no.such.key" {
		t.Errorf("missing = %q", got)
	}
}

func TestUnsupportedLocaleFallsBackToEnglish(t *testing.T) {
	m := newTestManager(t, "fr-FR")
	if m.Locale() != Source {
		t.Fatalf("Locale = %q", m.Locale())
	}
}

func TestPseudoLocale(t *testing.T) {
	l := newTestManager(t, Pseudo).Localizer("p")
	got := l.T("hello", "name", "Ada")
	if !strings.HasPrefix(got, "[Ĥéļļö, Ada!") || !strings.HasSuffix(got, "]") {
		t.Fatalf("pseudo = %q", got)
	}
	if Pseudolocalize("{{.x}}") != "[{{.x}}]" {
		t.Fatalf("templates must survive: %q", Pseudolocalize("{{.x}}"))
	}
}

func TestFormatting(t *testing.T) {
	l := newTestManager(t, "en").Localizer("p")
	if got := l.GroupDigits("-1234567.25"); got != "-1,234,567.25" {
		t.Errorf("GroupDigits = %q", got)
	}
	if got := l.Number(1234.5); got != "1,234.5" {
		t.Errorf("Number = %q", got)
	}
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		10 * time.Second:    "Just now",
		time.Minute:         "1 minute ago",
		5 * time.Minute:     "5 minutes ago",
		3 * time.Hour:       "3 hours ago",
		2 * 24 * time.Hour:  "2 days ago",
		30 * 24 * time.Hour: "Sep 14, 2026",
	} {
		if got := l.RelativeTime(now.Add(-d), now); got != want {
			t.Errorf("RelativeTime(-%v) = %q, want %q", d, got, want)
		}
	}
}

func TestManifestKey(t *testing.T) {
	for s, want := range map[string]string{"%a.b%": "a.b", "plain": "", "%%": "", "%a b%": "", "50% off%": ""} {
		got, ok := ManifestKey(s)
		if got != want || ok != (want != "") {
			t.Errorf("ManifestKey(%q) = %q, %v", s, got, ok)
		}
	}
}

func TestPlatformVariants(t *testing.T) {
	m := newTestManager(t, "en")
	if err := m.Register("x", fstest.MapFS{
		"en.json":    {Data: []byte(`{"a": "Windows", "a@darwin": "macOS", "b": "B", "c": "C", "c@darwin": "C mac"}`)},
		"zh-CN.json": {Data: []byte(`{"a": "视窗", "a@darwin": "苹果", "b": "乙", "c": "丙"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	l := m.Localizer("x")
	if l.T("a") != "Windows" || l.T("b") != "B" {
		t.Errorf("without a platform: %q %q", l.T("a"), l.T("b"))
	}
	m.SetPlatform("darwin")
	if l.T("a") != "macOS" || l.T("b") != "B" || l.T("c") != "C mac" {
		t.Errorf("darwin: %q %q %q", l.T("a"), l.T("b"), l.T("c"))
	}
	m.SetLocale("zh-CN")
	// A translated generic text wins over an untranslated variant.
	if l.T("a") != "苹果" || l.T("b") != "乙" || l.T("c") != "丙" {
		t.Errorf("zh-CN darwin: %q %q %q", l.T("a"), l.T("b"), l.T("c"))
	}
}
