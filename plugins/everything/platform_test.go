package everything

import (
	"errors"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

func TestSpotlightWording(t *testing.T) {
	f := &fake{}
	p := NewWithBackend(f)
	p.spotlight = true
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); !errors.Is(err, plugin.ErrUnavailable) || !strings.Contains(err.Error(), "mdfind") {
		t.Errorf("Init = %v", err)
	}
	m := p.Manifest()
	if m.Name != "%plugin.name.spotlight%" || m.Commands[0].Subtitle != "%command.search.subtitle.spotlight%" {
		t.Errorf("manifest %q, %q", m.Name, m.Commands[0].Subtitle)
	}

	f = &fake{installed: true, running: true, results: sample()}
	p, _ = startWith(t, f, func(p *Plugin) { p.spotlight = true })
	if v := view(t, p, ""); v.EmptyText != "Type to search files with Spotlight" {
		t.Errorf("hint %q", v.EmptyText)
	}
	v := view(t, p, "report")
	if its := items(v); len(its) == 0 || its[0].Actions[1].Title != "Show in Finder" {
		t.Errorf("items %+v", its)
	}
	if v := view(t, p, "nothing-matches-this"); !strings.Contains(v.EmptyText, "mdutil -s /") {
		t.Errorf("no results %q", v.EmptyText)
	}
	items, _ := p.ContextItems(t.Context(), plugin.Context{Kind: plugin.ContextExplorer, Folder: "/Users/demo/Reports"})
	if len(items) != 1 || items[0].Title != "Search This Folder with Spotlight" {
		t.Errorf("context items %+v", items)
	}
}

func TestBaseName(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\demo\Reports`: "Reports",
		`\\server\share\docs`:   "docs",
		"/Users/demo/Reports":   "Reports",
		"C:":                    "C:",
	} {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
	// A backslash is a valid character of a macOS file name.
	if got := baseName(`/tmp/a\b`); !strings.HasSuffix(`/tmp/a\b`, got) || got == "" {
		t.Errorf("baseName(/tmp/a\\b) = %q", got)
	}
}
