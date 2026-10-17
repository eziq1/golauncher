// Package builtin lists the plugins that ship with golauncher.
package builtin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/eziq1/golauncher/internal/extproc"
	"github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai"
	"github.com/eziq1/golauncher/plugins/ai/backend"
	"github.com/eziq1/golauncher/plugins/apps"
	"github.com/eziq1/golauncher/plugins/calc"
	"github.com/eziq1/golauncher/plugins/clipboard"
	"github.com/eziq1/golauncher/plugins/clock"
	"github.com/eziq1/golauncher/plugins/everything"
	"github.com/eziq1/golauncher/plugins/files"
	"github.com/eziq1/golauncher/plugins/network"
	"github.com/eziq1/golauncher/plugins/processes"
	"github.com/eziq1/golauncher/plugins/quicklinks"
	"github.com/eziq1/golauncher/plugins/scoop"
	"github.com/eziq1/golauncher/plugins/scripts"
	"github.com/eziq1/golauncher/plugins/selection"
	"github.com/eziq1/golauncher/plugins/system"
	"github.com/eziq1/golauncher/plugins/terminal"
	"github.com/eziq1/golauncher/plugins/timer"
	"github.com/eziq1/golauncher/plugins/winget"
)

// HostVersion is sent to external plugins in initialize and in the User-Agent
// of the calculator's exchange rate requests. Front ends set it
// to their stamped version before calling All.
var HostVersion = "dev"

// All returns new instances of the built-in plugins for the real system,
// followed by the external plugins installed in %APPDATA%\golauncher\plugins
// and in the directories of GOLAUNCHER_PLUGIN_PATH.
func All() []plugin.Plugin {
	builtins := []plugin.Plugin{
		apps.New(),
		selection.New(), // the "Selected Text" preview row comes first (docs/selected-text.md §5.1)
		calc.NewWithOptions(calc.Options{UserAgent: "golauncher/" + HostVersion}),
		clipboard.New(),
		clock.New(),
		timer.New(),
		files.New(),
		everything.New(),
		scripts.New(),
		realScoop(),
		winget.New(),
		system.New(),
		terminal.New(),
		network.New(),
		processes.New(),
		quicklinks.New(), // its fallback comes after ai's: FallbackOrderLast (docs/quicklinks.md §3)
		ai.NewWithOptions(ai.Options{Backends: aiBackends()}),
	}
	return assemble(runtime.GOOS, builtins, extproc.DefaultRoots(), extproc.Options{HostVersion: HostVersion})
}

// FakePlatform is the platform of the fake environment (Fake): it imitates
// a Windows machine on every system, so its results are the same
// everywhere.
const FakePlatform = plugin.PlatformWindows

// assemble returns the built-ins that run on platform (see
// plugin.Manifest.Platforms) followed by the external plugins under roots
// for platform. All and FakeOn go through it, so it is the one place that
// filters plugins by platform: the built-ins here, the external plugins in
// extproc.Load (given o.Platform). The IDs of all built-ins stay reserved,
// even of those left out.
func assemble(platform string, builtins []plugin.Plugin, roots []string, o extproc.Options) []plugin.Plugin {
	o.Platform = platform
	return append(ForPlatform(platform, builtins), External(roots, builtins, o)...)
}

// ForPlatform returns the plugins of ps whose manifest supports platform
// (a GOOS name), logging the others at debug level.
func ForPlatform(platform string, ps []plugin.Plugin) []plugin.Plugin {
	out := ps[:0:0]
	for _, p := range ps {
		if m := p.Manifest(); !m.SupportsPlatform(platform) {
			slog.Debug("built-in plugin skipped: not for this platform", "plugin", m.ID, "platforms", m.Platforms, "platform", platform)
			continue
		}
		out = append(out, p)
	}
	return out
}

// External returns the external plugins under roots (see extproc.Find),
// leaving out those whose ID is taken by one of loaded and those whose
// platforms leave out o.Platform (runtime.GOOS when empty).
func External(roots []string, loaded []plugin.Plugin, o extproc.Options) []plugin.Plugin {
	var ids []string
	for _, p := range loaded {
		ids = append(ids, p.Manifest().ID)
	}
	var out []plugin.Plugin
	for _, p := range extproc.Load(roots, ids, o, nil) {
		out = append(out, p)
	}
	return out
}

// Fake returns the built-in plugins for an isolated environment. Data that
// would come from the system is read from fixture files in dir when present
// ("" for none): apps.json (installed applications), files.json
// (Everything's file index), rates.json (exchange rates in the format of
// open.er-api.com; without it the calculator has no rates and never goes
// online), scripts/ (script commands) and scoop/ (a Scoop root; Scoop is
// unavailable without it). No icons are extracted and nothing is opened;
// programs (Scoop commands) run as background tasks, which the fake
// environment's task runner only records.
// ("" for none): apps.json (installed applications) and files.json
// (Everything's file index), scripts/ (script commands) and scoop/ (a Scoop
// root; Scoop is unavailable without it), terminal/ (Windows Terminal and WSL,
// see terminal.FixtureOptions), winget/ (winget's index, installed packages
// and sources, see winget.FixtureOptions; WinGet is unavailable without it).
// No icons are extracted and nothing
// is opened; programs (Scoop and winget commands) run as background tasks, which the
// fake environment's task runner only records.
//
// External plugins are loaded from pluginRoots and GOLAUNCHER_PLUGIN_PATH
// (never from the user's plugin directory). They start eagerly, so results
// do not depend on how fast a plugin process starts.
//
// Plugins are chosen for FakePlatform (Windows) whatever the system, so
// Scoop and WinGet are there on macOS too.
func Fake(dir string, pluginRoots ...string) []plugin.Plugin {
	return FakeOn(FakePlatform, dir, pluginRoots...)
}

// FakeOn is Fake with the plugins chosen for platform (a GOOS name, see
// plugin.Manifest.Platforms) instead of FakePlatform. The plugins
// themselves stay those of the fake environment.
func FakeOn(platform, dir string, pluginRoots ...string) []plugin.Plugin {
	appsSrc := apps.Source(func(context.Context) ([]shell.App, error) { return nil, nil })
	if f := fixture(dir, "apps.json"); f != "" {
		appsSrc = apps.FixtureSource(f)
	}
	builtins := []plugin.Plugin{
		apps.NewWithSource(appsSrc, nil),
		selection.New(),
		fakeCalc(dir),
		clipboard.NewWithOptions(clipboard.Options{}),   // Windows' defaults on every platform
		clock.NewWithOptions(clock.Options{Sync: true}), // deterministic loading
		timer.New(),
		files.NewWithOptions(files.Options{Which: fakeWhich}),
		everything.NewWithBackend(everything.FixtureBackend(fixture(dir, "files.json"))),
		scripts.NewWithOptions(scripts.Options{Directories: fixtureDirs(dir, "scripts"), Open: noOpen}),
		fakeScoop(dir),
		winget.NewWithOptions(winget.FixtureOptions(dir)),
		system.NewWithOptions(system.Options{System: system.NewFake(), Sync: true}),
		terminal.NewWithOptions(terminal.FixtureOptions(dir)),
		network.NewFake(fixture(dir, "network.json")),
		processes.NewFake(fixture(dir, "processes.json")),
		fakeQuicklinks(),
		fakeAI(),
	}
	roots := append(slices.Clone(pluginRoots), extproc.EnvDirs()...)
	return assemble(platform, builtins, roots, extproc.Options{Eager: true})
}

// fakeCalc is the calculator with the exchange rates of dir/rates.json, or
// without rates; it never downloads any. Timestamps are shown in UTC so
// that results do not depend on the machine's time zone.
func fakeCalc(dir string) *calc.Plugin {
	o := calc.Options{Offline: true, Location: time.UTC}
	if f := fixture(dir, "rates.json"); f != "" {
		if r, err := calc.FixtureRates(f); err == nil {
			o.Rates = r
		}
	}
	return calc.NewWithOptions(o)
}

// fixture returns dir/name if that file exists, else "".
func fixture(dir, name string) string {
	if dir == "" {
		return ""
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	return p
}

// fixtureDirs returns [dir/name] if that directory exists, else nil.
func fixtureDirs(dir, name string) []string {
	if dir == "" {
		return nil
	}
	p := filepath.Join(dir, name)
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		return nil
	}
	return []string{p}
}

// fakeWhich stands in for finding programs in fake mode: Windows Terminal
// and VS Code are installed.
func fakeWhich(name string) string {
	switch name {
	case "wt":
		return "wt.exe"
	case "code":
		return `C:\Program Files\Microsoft VS Code\Code.exe`
	}
	return ""
}

// fakeAI is the AI chat with a fake backend that answers "You asked: …" at
// once (AI is off until a session turns it on, as for a new user); it never
// runs Claude Code. Links go to the websites (no Claude app).
func fakeAI() *ai.Plugin {
	n := 0
	return ai.NewWithOptions(ai.Options{Backend: &backend.Fake{}, Sync: true, HasProtocol: func(string) bool { return false },
		NewID: func() string { n++; return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }})
}

// fakeQuicklinks is the quicklinks and web search plugin with one fake
// browser; it never goes online (search suggestions fail).
func fakeQuicklinks() *quicklinks.Plugin {
	return quicklinks.NewWithOptions(quicklinks.Options{Offline: true, Sync: true, Browsers: func() []shell.Browser {
		return []shell.Browser{{Name: "Firefox", Path: `C:\Program Files\Mozilla Firefox\firefox.exe`}}
	}})
}

// noOpen stands in for opening files in fake mode.
func noOpen(string, plugin.OpenOptions) error { return nil }

// realScoop is the Scoop plugin for the real system. Its commands run as
// background tasks, so glctl --real without --exec only records them (its
// task runner is fake).
func realScoop() *scoop.Plugin { return scoop.New() }

// fakeScoop is the Scoop plugin reading the fake Scoop root dir/scoop (with
// globally installed apps in dir/scoop/global). It never looks at the real
// Scoop; its commands go to the fake task runner.
func fakeScoop(dir string) *scoop.Plugin {
	o := scoop.Options{NoDetect: true, Sync: true, Shell: "powershell.exe"}
	if roots := fixtureDirs(dir, "scoop"); len(roots) > 0 {
		o.Root = roots[0]
		o.Global = filepath.Join(roots[0], "global")
	}
	return scoop.NewWithOptions(o)
}
