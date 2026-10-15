package calc

import (
	"context"
	"encoding/json/v2"
	"io/fs"
	"slices"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/pkg/plugin"
)

func newPlugin(t *testing.T) (*Plugin, *hostfake.Host) {
	t.Helper()
	p := New()
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return p, h
}

func TestManifest(t *testing.T) {
	_, h := newPlugin(t)
	m := New().Manifest()
	if m.ID != "builtin.calc" || m.Version != "1.0.0" || m.Icon.Name != "calculator" {
		t.Errorf("manifest = %+v", m)
	}
	if len(m.Commands) != 0 || len(m.Preferences) != 0 || len(m.Permissions) != 0 {
		t.Errorf("manifest has commands, preferences or permissions: %+v", m)
	}
	l := h.I18N.Localizer(m.ID)
	if got := l.Resolve(m.Name); got != "Calculator" {
		t.Errorf("name = %q, want Calculator", got)
	}
	if got := l.Resolve(m.Description); got == m.Description {
		t.Errorf("description %q is not translated", got)
	}
}

func TestMessageKeysExist(t *testing.T) {
	_, h := newPlugin(t)
	l := h.I18n()
	for _, key := range []string{"plugin.name", "plugin.description", "group", "action.copy", "action.copyExpression", "action.paste"} {
		if got := l.T(key); got == key || got == "" {
			t.Errorf("T(%q) = %q: missing from en.json", key, got)
		}
	}
	// And no unused keys.
	data, err := fs.ReadFile(New().Locales(), "en.json")
	if err != nil {
		t.Fatal(err)
	}
	var cat map[string]any
	if err := json.Unmarshal(data, &cat); err != nil {
		t.Fatal(err)
	}
	if got := len(cat); got != 6 {
		t.Errorf("en.json has %d keys, want 6", got)
	}
}

func TestRootItems(t *testing.T) {
	p, _ := newPlugin(t)
	items, err := p.RootItems(t.Context(), plugin.Query{Text: "1234 * 5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if it.ID != "result" || it.Title != "6,170" || it.Subtitle != "1234*5" ||
		it.Group != "Calculator" || it.Score != 1 || it.Icon.Name != "calculator" {
		t.Errorf("item = %+v", it)
	}
	want := []plugin.Action{
		{ID: "copy", Title: "Copy Result", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: "6170"}},
		{ID: "copy-expression", Title: "Copy Expression", Shortcut: "Ctrl+Shift+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: "1234*5 = 6170"}},
		{ID: "paste", Title: "Paste Result", Shortcut: "Ctrl+Shift+V",
			Do: plugin.ActionDo{Kind: plugin.ActionPaste, Target: "6170"}},
	}
	if !slices.EqualFunc(it.Actions, want, func(a, b plugin.Action) bool {
		return a.ID == b.ID && a.Title == b.Title && a.Shortcut == b.Shortcut &&
			a.Do.Kind == b.Do.Kind && a.Do.Target == b.Do.Target && a.Do.Payload == nil
	}) {
		t.Errorf("actions = %+v, want %+v", it.Actions, want)
	}
}

func TestRootItemsTitles(t *testing.T) {
	p, _ := newPlugin(t)
	tests := []struct{ in, title, raw string }{
		{"1234567*1", "1,234,567", "1234567"},
		{"-1234567.5*1", "-1,234,567.5", "-1234567.5"},
		{"0.1+0.2", "0.3", "0.3"},
		{"1 - 2.5", "-1.5", "-1.5"},
		{"123456789*1e20", "1.23456789e+28", "1.23456789e+28"},
		{"1/1e10", "1e-10", "1e-10"},
		{"255 in hex", "0xFF", "0xFF"},
		{"255 in bin", "0b11111111", "0b11111111"},
		{"255 in oct", "0o377", "0o377"},
		{"0xFFFF in dec", "65,535", "65535"},
		{"=1234", "1,234", "1234"},
	}
	for _, tt := range tests {
		items, err := p.RootItems(t.Context(), plugin.Query{Text: tt.in})
		if err != nil || len(items) != 1 {
			t.Errorf("RootItems(%q) = %v, %v", tt.in, items, err)
			continue
		}
		it := items[0]
		if it.Title != tt.title || it.Actions[0].Do.Target != tt.raw || it.Actions[2].Do.Target != tt.raw {
			t.Errorf("RootItems(%q): title %q raw %q, want %q %q", tt.in, it.Title, it.Actions[0].Do.Target, tt.title, tt.raw)
		}
	}
}

func TestRootItemsNoResult(t *testing.T) {
	p, _ := newPlugin(t)
	for _, in := range []string{"", "42", "code", "notepad", "e", "pi", "1/0", "sqrt(-1)", "(1+2", "1+2 abc", "1,5+1"} {
		items, err := p.RootItems(t.Context(), plugin.Query{Text: in})
		if err != nil || items != nil {
			t.Errorf("RootItems(%q) = %v, %v; want nothing", in, items, err)
		}
	}
}

func TestRootItemsCanceled(t *testing.T) {
	p, _ := newPlugin(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	items, err := p.RootItems(ctx, plugin.Query{Text: "1+1"})
	if err == nil || items != nil {
		t.Errorf("RootItems on canceled context = %v, %v", items, err)
	}
}

func TestRootItemsPseudoLocale(t *testing.T) {
	p, h := newPlugin(t)
	h.I18N.SetLocale(i18n.Pseudo)
	items, err := p.RootItems(t.Context(), plugin.Query{Text: "1234*5"})
	if err != nil || len(items) != 1 {
		t.Fatalf("RootItems = %v, %v", items, err)
	}
	if items[0].Group == "Calculator" || items[0].Actions[0].Title == "Copy Result" {
		t.Errorf("strings not localized: %+v", items[0])
	}
	if got := items[0].Actions[0].Do.Target; got != "6170" {
		t.Errorf("raw = %q, want 6170", got)
	}
}
