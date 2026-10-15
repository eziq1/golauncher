package i18n

import (
	"testing"
	"testing/fstest"
	"time"
)

// TestChineseLocaleMatching is how the Windows display languages resolve to
// the shipped locales. Simplified Chinese in any region is zh-CN. Traditional
// Chinese is not offered zh-CN, whose script it does not read: it falls back
// to the next preferred language and, without one, to English.
func TestChineseLocaleMatching(t *testing.T) {
	m := newTestManager(t, "en")
	for _, tt := range []struct {
		prefs []string
		want  string
	}{
		{[]string{"zh-CN"}, "zh-CN"},
		{[]string{"zh-Hans-CN"}, "zh-CN"},
		{[]string{"zh-Hans"}, "zh-CN"},
		{[]string{"zh"}, "zh-CN"},
		{[]string{"zh-SG"}, "zh-CN"},
		{[]string{"zh-Hans-SG"}, "zh-CN"},
		{[]string{"en-IN"}, Source},
		{[]string{"en-XA"}, Source},
		{[]string{"zh-TW"}, Source},
		{[]string{"zh-Hant-TW"}, Source},
		{[]string{"zh-HK"}, Source},
		{[]string{"zh-TW", "zh-CN"}, "zh-CN"}, // Traditional first, Simplified as a fallback
		{[]string{"zh-TW", "en-US"}, Source},
		{[]string{"en-US", "zh-CN"}, Source},
		{[]string{"en-GB"}, Source},
		{[]string{"ja-JP"}, Source},
		{nil, Source},
	} {
		if got := m.match(tt.prefs); got != tt.want {
			t.Errorf("match(%q) = %q, want %q", tt.prefs, got, tt.want)
		}
	}
	for _, loc := range []string{"zh-CN", "zh-Hans-CN", "zh-SG"} {
		if m.SetLocale(loc); m.Locale() != "zh-CN" {
			t.Errorf("SetLocale(%q): Locale = %q", loc, m.Locale())
		}
	}
	if got := m.Available(); len(got) != 2 || got[0] != "en" || got[1] != "zh-CN" {
		t.Errorf("Available = %q", got)
	}
}

func TestDisplayName(t *testing.T) {
	for loc, want := range map[string]string{"en": "English", "zh-CN": "简体中文", "xx": "xx"} {
		if got := DisplayName(loc); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", loc, got, want)
		}
	}
}

func TestChineseFormatting(t *testing.T) {
	m := newTestManager(t, "zh-CN")
	l := m.Localizer(CoreNamespace)
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		10 * time.Second:    "刚刚",
		time.Minute:         "1 分钟前",
		5 * time.Minute:     "5 分钟前",
		3 * time.Hour:       "3 小时前",
		2 * 24 * time.Hour:  "2 天前",
		30 * 24 * time.Hour: "2026年9月14日",
	} {
		if got := l.RelativeTime(now.Add(-d), now); got != want {
			t.Errorf("RelativeTime(-%v) = %q, want %q", d, got, want)
		}
	}
	if got := l.N("ui.tasks.running", 1); got != "1 个后台任务正在运行" {
		t.Errorf("N = %q", got)
	}
	if got := l.GroupDigits("1234567.5"); got != "1,234,567.5" {
		t.Errorf("GroupDigits = %q", got)
	}
}

// TestExactPluralForm: "=1" wins over the CLDR category, which in Chinese is
// always "other".
func TestExactPluralForm(t *testing.T) {
	m := NewManager(Source, nil)
	if err := m.Register(CoreNamespace, CoreLocales()); err != nil {
		t.Fatal(err)
	}
	err := m.Register("p", fstest.MapFS{
		"en.json":    {Data: []byte(`{"sel": {"one": "{{.name}}", "other": "{{.Count}} selected"}}`)},
		"zh-CN.json": {Data: []byte(`{"sel": {"=1": "{{.name}}", "other": "已选择 {{.Count}} 项"}}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	m.SetLocale("zh-CN")
	l := m.Localizer("p")
	if got := l.N("sel", 1, "name", "a.txt"); got != "a.txt" {
		t.Errorf("N(1) = %q", got)
	}
	if got := l.N("sel", 3, "name", "a.txt"); got != "已选择 3 项" {
		t.Errorf("N(3) = %q", got)
	}
}

func TestResolveSource(t *testing.T) {
	m := NewManager(Source, nil)
	if err := m.Register(CoreNamespace, CoreLocales()); err != nil {
		t.Fatal(err)
	}
	err := m.Register("p", fstest.MapFS{
		"en.json":    {Data: []byte(`{"title": "Clipboard History"}`)},
		"zh-CN.json": {Data: []byte(`{"title": "剪贴板历史"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	m.SetLocale("zh-CN")
	l := m.Localizer("p")
	if got := l.Resolve("%title%"); got != "剪贴板历史" {
		t.Errorf("Resolve = %q", got)
	}
	if got := l.ResolveSource("%title%"); got != "Clipboard History" {
		t.Errorf("ResolveSource = %q", got)
	}
	if got := l.ResolveSource("plain"); got != "plain" {
		t.Errorf("ResolveSource(plain) = %q", got)
	}
}
