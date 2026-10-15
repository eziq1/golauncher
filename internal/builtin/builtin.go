// Package builtin lists the plugins that ship with golauncher.
package builtin

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/apps"
	"github.com/eziq1/golauncher/plugins/calc"
	"github.com/eziq1/golauncher/plugins/clipboard"
	"github.com/eziq1/golauncher/plugins/everything"
)

// All returns new instances of the built-in plugins for the real system.
func All() []plugin.Plugin {
	return []plugin.Plugin{
		apps.New(),
		calc.New(),
		clipboard.New(),
		everything.New(),
	}
}

// Fake returns the built-in plugins for an isolated environment. Data that
// would come from the system is read from fixture files in dir when present
// ("" for none): apps.json (installed applications) and files.json
// (Everything's file index). No icons are extracted.
func Fake(dir string) []plugin.Plugin {
	appsSrc := apps.Source(func(context.Context) ([]shell.App, error) { return nil, nil })
	if f := fixture(dir, "apps.json"); f != "" {
		appsSrc = apps.FixtureSource(f)
	}
	return []plugin.Plugin{
		apps.NewWithSource(appsSrc, nil),
		calc.New(),
		clipboard.New(),
		everything.NewWithBackend(everything.FixtureBackend(fixture(dir, "files.json"))),
	}
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
