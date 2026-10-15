package ui_test

import (
	"os"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/internal/winapi/everything"
	"github.com/eziq1/golauncher/pkg/plugin"
	everythingplugin "github.com/eziq1/golauncher/plugins/everything"
)

// TestRealFileSearch types into the file search view backed by the real
// Everything, rendering every frame with the detail pane, as a user would.
// It only reads Everything's index.
func TestRealFileSearch(t *testing.T) {
	if testing.Short() || os.Getenv("GOLAUNCHER_EVERYTHING_TESTS") != "1" || !everything.Running() {
		t.Skip("needs a running Everything")
	}
	clip := &hostfake.Clipboard{}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{everythingplugin.New()}, Fake: true, Locale: "en",
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	lt := &launcherTest{Env: env, t: t, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()

	if _, err := env.Controller.OpenCommand(t.Context(), "builtin.everything", "search"); err != nil {
		t.Fatal(err)
	}
	lt.step()
	// The last query finds nothing: an empty result section.
	for _, q := range []string{"notepad", "中文", "report", "*.go", "a", "zqxjkvwzqxjkvw"} {
		for _, r := range q {
			lt.tt.Type(string(r))
			lt.tt.Frame() // render while searches are in flight
		}
		lt.step()
		for range 5 {
			lt.key(0, mui.KeyDown)
		}
		for range len([]rune(q)) {
			lt.key(0, mui.KeyBackspace)
		}
	}
}
