package ui_test

import (
	"context"
	"math"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// argsPlugin has root commands with and without arguments, as the network
// plugin's "DNS 查询" next to its plain commands (R74).
type argsPlugin struct{}

func (argsPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: "args", Name: "Args Plugin", Version: "1",
		Commands: []plugin.Command{
			{Name: "plain", Title: "Zeta Plain", Mode: plugin.ModeNoView},
			{Name: "text", Title: "Zeta Text", Mode: plugin.ModeNoView, Arguments: []plugin.Argument{
				{Name: "domain", Type: plugin.ArgText, Placeholder: "domain [@server]", Required: true},
			}},
			{Name: "pick", Title: "Zeta Pick", Mode: plugin.ModeNoView, Arguments: []plugin.Argument{
				{Name: "host", Type: plugin.ArgText, Placeholder: "host"},
				{Name: "kind", Type: plugin.ArgDropdown, Placeholder: "Kind", Options: []plugin.Option{{Value: "a", Title: "A"}}},
			}},
		},
	}
}

func (argsPlugin) Init(context.Context, plugin.Host) error { return nil }
func (argsPlugin) Close() error                            { return nil }

func newArgsLauncher(t *testing.T) *launcherTest {
	t.Helper()
	clip := &hostfake.Clipboard{}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{argsPlugin{}}, Fake: true, Locale: "en",
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	lt := &launcherTest{Env: env, t: t, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	return lt
}

// barMetrics is where the search field and the results' rows are.
type barMetrics struct {
	searchY, searchH float32
	rowY             float32 // of the title of the first command
}

func measureBar(t *testing.T, lt *launcherTest) barMetrics {
	t.Helper()
	in, ok := lt.tt.Find("Search")
	if !ok {
		t.Fatalf("no search field in %q", lt.tt.Texts())
	}
	row, ok := lt.tt.Find("Zeta Plain")
	if !ok {
		t.Fatalf("no row in %q", lt.tt.Texts())
	}
	return barMetrics{searchY: in.Y, searchH: in.H, rowY: row.Y}
}

// selectTitle moves the selection to the command titled title.
func selectTitle(t *testing.T, lt *launcherTest, title string) {
	t.Helper()
	for range 10 {
		if e, ok := lt.Controller.Snapshot().SelectedEntry(); ok && e.Title == title {
			return
		}
		lt.key(0, mui.KeyDown)
	}
	t.Fatalf("cannot select %q", title)
}

// TestSearchBarKeepsItsHeight: the inputs of a command's arguments next to
// the search field do not change the height of the search bar, so the rows
// under it stay put as the selection moves (R74).
func TestSearchBarKeepsItsHeight(t *testing.T) {
	density := func(d string) func(a *config.Appearance) { return func(a *config.Appearance) { a.Density = &d } }
	fontSize := func(s float64) func(a *config.Appearance) { return func(a *config.Appearance) { a.FontSize = &s } }
	for _, tc := range []struct {
		name string
		set  func(a *config.Appearance)
	}{
		{"default", nil},
		{"compact", density("compact")},
		{"comfortable", density("comfortable")},
		{"font20", fontSize(20)},
		{"font11", fontSize(11)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lt := newArgsLauncher(t)
			if tc.set != nil {
				lt.Config.Update(func(c *config.Config) { tc.set(&c.Appearance) })
				lt.step()
			}
			lt.typeText("zeta")
			selectTitle(t, lt, "Zeta Plain")
			if lt.Controller.Snapshot().Arguments != nil {
				t.Fatal("plain command has arguments")
			}
			want := measureBar(t, lt)
			for _, title := range []string{"Zeta Text", "Zeta Pick", "Zeta Plain", "Zeta Text", "Zeta Plain"} {
				selectTitle(t, lt, title)
				if args := lt.Controller.Snapshot().Arguments; (args != nil) != (title != "Zeta Plain") {
					t.Fatalf("%s: arguments %+v", title, args)
				}
				if got := measureBar(t, lt); got != want {
					t.Errorf("%s selected: %+v, want %+v", title, got, want)
				}
				// The inputs fit in the bar, centered on the search field.
				for _, name := range []string{"domain [@server]", "host", "Kind"} {
					r, ok := lt.tt.Find(name)
					if !ok {
						continue
					}
					if r.H > want.searchH+0.01 || math.Abs(float64(r.Y+r.H/2-(want.searchY+want.searchH/2))) > 0.5 {
						t.Errorf("%s: input %q at %+v, search field at y %v, height %v", title, name, r, want.searchY, want.searchH)
					}
				}
			}
		})
	}
}
