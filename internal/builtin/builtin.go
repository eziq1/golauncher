// Package builtin lists the plugins that ship with golauncher.
package builtin

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/eziq1/golauncher/internal/extproc"
	"github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/apps"
	"github.com/eziq1/golauncher/plugins/calc"
	"github.com/eziq1/golauncher/plugins/clipboard"
	"github.com/eziq1/golauncher/plugins/clock"
	"github.com/eziq1/golauncher/plugins/everything"
	"github.com/eziq1/golauncher/plugins/scoop"
	"github.com/eziq1/golauncher/plugins/scripts"
)

// HostVersion is sent to external plugins in initialize. Front ends set it
// to their stamped version before calling All.
var HostVersion = "dev"

// DryRun makes plugins that run external programs to change the system
// (Scoop) record those commands instead of running them. glctl --real sets
// it unless --exec is given.
var DryRun bool

// All returns new instances of the built-in plugins for the real system,
// followed by the external plugins installed in %APPDATA%\golauncher\plugins
// and in the directories of GOLAUNCHER_PLUGIN_PATH.
func All() []plugin.Plugin {
	builtins := []plugin.Plugin{
		apps.New(),
		calc.New(),
		clipboard.New(),
		clock.New(),
		everything.New(),
		scripts.New(),
		realScoop(),
	}
	return append(builtins, External(extproc.DefaultRoots(), builtins, extproc.Options{HostVersion: HostVersion})...)
}

// External returns the external plugins under roots (see extproc.Find),
// leaving out those whose ID is taken by one of loaded.
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
// ("" for none): apps.json (installed applications) and files.json
// (Everything's file index), scripts/ (script commands) and scoop/ (a Scoop
// root; Scoop is unavailable without it). No icons are extracted, nothing
// is opened and no Scoop command is run.
//
// External plugins are loaded from pluginRoots and GOLAUNCHER_PLUGIN_PATH
// (never from the user's plugin directory). They start eagerly, so results
// do not depend on how fast a plugin process starts.
func Fake(dir string, pluginRoots ...string) []plugin.Plugin {
	appsSrc := apps.Source(func(context.Context) ([]shell.App, error) { return nil, nil })
	if f := fixture(dir, "apps.json"); f != "" {
		appsSrc = apps.FixtureSource(f)
	}
	builtins := []plugin.Plugin{
		apps.NewWithSource(appsSrc, nil),
		calc.New(),
		clipboard.New(),
		clock.New(),
		everything.NewWithBackend(everything.FixtureBackend(fixture(dir, "files.json"))),
		scripts.NewWithOptions(scripts.Options{Directories: fixtureDirs(dir, "scripts"), Open: noOpen}),
		fakeScoop(dir),
	}
	roots := append(slices.Clone(pluginRoots), extproc.EnvDirs()...)
	return append(builtins, External(roots, builtins, extproc.Options{Eager: true})...)
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

// noOpen stands in for opening files in fake mode.
func noOpen(string, plugin.OpenOptions) error { return nil }

// realScoop is the Scoop plugin for the real system; with DryRun its
// commands are only recorded.
func realScoop() *scoop.Plugin {
	if DryRun {
		return scoop.NewWithOptions(scoop.Options{Runner: &scoop.FakeRunner{}})
	}
	return scoop.New()
}

// fakeScoop is the Scoop plugin reading the fake Scoop root dir/scoop (with
// globally installed apps in dir/scoop/global). It never looks at the real
// Scoop and records commands instead of running them.
func fakeScoop(dir string) *scoop.Plugin {
	o := scoop.Options{NoDetect: true, Runner: &scoop.FakeRunner{}, Sync: true}
	if roots := fixtureDirs(dir, "scoop"); len(roots) > 0 {
		o.Root = roots[0]
		o.Global = filepath.Join(roots[0], "global")
	}
	return scoop.NewWithOptions(o)
}
