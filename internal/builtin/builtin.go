// Package builtin lists the plugins that ship with golauncher.
package builtin

import (
	"context"

	"golauncher/internal/winapi/shell"
	"golauncher/pkg/plugin"
	"golauncher/plugins/apps"
	"golauncher/plugins/calc"
	"golauncher/plugins/clipboard"
)

// All returns new instances of the built-in plugins for the real system.
func All() []plugin.Plugin {
	return []plugin.Plugin{
		apps.New(),
		calc.New(),
		clipboard.New(),
	}
}

// Fake returns the built-in plugins for an isolated environment: the apps
// index comes from the fixture file appsFixture ("" for no apps) and no
// icons are extracted.
func Fake(appsFixture string) []plugin.Plugin {
	src := apps.Source(func(context.Context) ([]shell.App, error) { return nil, nil })
	if appsFixture != "" {
		src = apps.FixtureSource(appsFixture)
	}
	return []plugin.Plugin{
		apps.NewWithSource(src, nil),
		calc.New(),
		clipboard.New(),
	}
}
