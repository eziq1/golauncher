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
	"github.com/eziq1/golauncher/plugins/everything"
)

// All returns new instances of the built-in plugins for the real system,
// followed by the external plugins installed in %APPDATA%\golauncher\plugins
// and in the directories of GOLAUNCHER_PLUGIN_PATH.
func All() []plugin.Plugin {
	builtins := []plugin.Plugin{
		apps.New(),
		calc.New(),
		clipboard.New(),
		everything.New(),
	}
	return append(builtins, External(extproc.DefaultRoots(), builtins, extproc.Options{})...)
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
// (Everything's file index). No icons are extracted.
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
		everything.NewWithBackend(everything.FixtureBackend(fixture(dir, "files.json"))),
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
