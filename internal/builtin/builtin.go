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
	"github.com/eziq1/golauncher/plugins/scripts"
)

// All returns new instances of the built-in plugins for the real system.
func All() []plugin.Plugin {
	return []plugin.Plugin{
		apps.New(),
		calc.New(),
		clipboard.New(),
		everything.New(),
		scripts.New(),
	}
}

// Fake returns the built-in plugins for an isolated environment. Data that
// would come from the system is read from fixture files in dir when present
// ("" for none): apps.json (installed applications) and files.json
// (Everything's file index), and scripts/ (script commands). No icons are
// extracted and nothing is opened.
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
		scripts.NewWithOptions(scripts.Options{Directories: fixtureDirs(dir, "scripts"), Open: noOpen}),
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
