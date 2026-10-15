# Writing a golauncher plugin

Everything golauncher does is implemented as a plugin, including the built-in
application launcher, calculator, clipboard history and file search. This
guide shows how to write one. It covers in-process Go plugins, which are the
only kind supported in v0.1. Out-of-process plugins in any language are
planned (design §5.5).

The SDK lives in [`pkg/plugin`](../pkg/plugin/). The built-in plugins in
[`plugins/`](../plugins/) are complete examples:

| Plugin | Shows how to |
|--------|--------------|
| [`calc`](../plugins/calc/) | add a result to the root search |
| [`apps`](../plugins/apps/) | supply many candidates, run background work, offer several actions |
| [`clipboard`](../plugins/clipboard/) | build a command with its own searchable view, a detail pane and callbacks |
| [`everything`](../plugins/everything/) | depend on external software and report when it is unavailable |
| [`scripts`](../plugins/scripts/) | run processes, stream output into a view with `Invalidate`, take arguments |

## The model

A plugin never draws UI and never performs side effects directly. Instead it:

1. **Describes what to show.** It returns `Item`s (title, icon, keywords, a
   detail pane) for the root search or for its own views.
2. **Describes what each action does.** Each item has `Action`s whose `Do`
   field is a serializable intent (`ActionDo`). Examples are "open this path",
   "copy this text", "paste this into the previous window" and "push my view
   X". The host carries these out.

Because actions are data, the GUI and the headless `glctl` CLI behave the
same, and tests can assert exactly what an action would do without doing it.

## A minimal plugin

```go
package hello

import (
	"context"
	"embed"
	"io/fs"

	"github.com/eziq1/golauncher/pkg/plugin"
)

//go:embed locales/*.json
var locales embed.FS

type Plugin struct{ h plugin.Host }

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:      "example.hello",
		Name:    "%plugin.name%", // resolved from locales/en.json
		Version: "0.1.0",
		Icon:    plugin.Icon{Name: "command"},
	}
}

func (p *Plugin) Init(_ context.Context, h plugin.Host) error { p.h = h; return nil }
func (p *Plugin) Close() error                                { return nil }

func (p *Plugin) Locales() fs.FS { sub, _ := fs.Sub(locales, "locales"); return sub }

// RootItems contributes to the root search. Items without a Score are
// fuzzy-matched by the host against Title and Keywords.
func (p *Plugin) RootItems(_ context.Context, q plugin.Query) ([]plugin.Item, error) {
	l := p.h.I18n()
	return []plugin.Item{{
		ID:       "greet",
		Title:    l.T("greet.title"),
		Keywords: []string{"hello", "hi"},
		Group:    l.T("group"),
		Actions: []plugin.Action{{
			ID:    "copy",
			Title: l.T("greet.copy"),
			Do:    plugin.ActionDo{Kind: plugin.ActionCopyText, Target: l.T("greet.text")},
		}},
	}}, nil
}
```

The message catalog goes in `locales/en.json`:

```json
{
  "plugin.name": "Hello",
  "group": "Hello",
  "greet.title": "Say Hello",
  "greet.copy": "Copy Greeting",
  "greet.text": "Hello, world!"
}
```

Register the plugin in [`internal/builtin`](../internal/builtin/builtin.go).

## Interfaces

A plugin implements `plugin.Plugin` plus any of the optional interfaces below.

| Interface | Called for |
|-----------|------------|
| `RootProvider` | Every keystroke in the root search. It must be fast, and results arriving after 150ms are dropped. |
| `ViewProvider` | Views opened by a `ModeView` command (the view ID is the command name) or by an `ActionPush`. It is called again on every keystroke, unless the view sets `FilterLocally`. |
| `ActionHandler` | `ModeNoView` commands (`ActionRunCommand`) and your own callbacks (`ActionCallback`). It returns a `Result`, which can push a view, show a toast, hide the window, pop to the root or refresh. |
| `LocaleProvider` | Your message catalogs. |

### Commands

Commands declared in the manifest appear in the root search under
"Commands". Users can give them an alias and a global hotkey in Settings.

- Mark commands that delete data `Destructive: true`, and give such actions
  `Style: plugin.StyleDestructive`. The host then asks the user to confirm
  before running them.

### Preferences

Declare preferences in the manifest and Settings renders a form for them.
Read them with `Host.Preferences()`; values fall back to the declared
`Default`. `password` preferences are stored in Windows Credential Manager,
not in `config.json`.

### Robustness

The host contains panics in every call into your plugin and gives up on
calls that take too long (10s for views, 30s for actions, 150ms for root
results). This keeps the launcher alive, but your plugin should still honor
`ctx` cancellation and return quickly. See
[reliability.md](reliability.md) (Chinese) for details.

### When a dependency is missing

If your plugin needs software that is not installed, return an error that
wraps `plugin.ErrUnavailable` from `Init`:

```go
return fmt.Errorf("%s: %w", l.T("unavailable.notInstalled"), plugin.ErrUnavailable)
```

The plugin is then left disabled, and Settings shows the reason instead of an
error.

## Host services

`Host` is the plugin's only gateway to the system:

| Method | Provides |
|--------|----------|
| `Storage()` | A private key/value store. |
| `DataDir()`, `CacheDir()` | Private directories. |
| `Clipboard()` | Clipboard access. |
| `Events()` | Host events: `clipboard-changed`, `window-shown`, `locale-changed`, `config-changed`. |
| `I18n()` | Your translations plus number and relative-time formatting. |
| `Now()` | The current time. Use it rather than `time.Now()` so tests can fix the clock. |
| `Logger()` | A logger. |
| `Go(fn)` | Runs background work on a goroutine. A panic in `fn` is logged and contained, so it cannot crash the launcher. Always use it instead of `go`; to wait for the work, call `wg.Add(1)` first and `defer wg.Done()` inside `fn`. |

## Rules enforced by tests

- Plugins import only `pkg/plugin`, `internal/winapi/...`, the standard
  library and an allowlist of modules. Host internals are off limits.
- No `go` statements or `WaitGroup.Go` in plugins; start goroutines with `Host.Go`.
- No user-visible string literals. Use the Localizer, and use `%key%` in
  manifests. `glctl i18n check` verifies that every key exists.

These checks run as part of `go test ./...` (see `internal/lint`).

## Testing

Use the in-memory host from `internal/host/hostfake`:

```go
func TestGreet(t *testing.T) {
	p := hello.New()
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	items, _ := p.RootItems(t.Context(), plugin.Query{Text: "hello"})
	// assert on items[0].Title, items[0].Actions[0].Do, …
}
```

For end-to-end checks of ranking and keyboard flows, drive the whole engine
with `glctl`:

```powershell
glctl query hello --format text
glctl session new -s s.json
glctl session type -s s.json "say hello"
glctl session key  -s s.json Enter   # prints the recorded effect: CopyText "Hello, world!"
```

## Script Commands

The quickest way to add a command is not a plugin at all: a script with
[Raycast Script Commands](https://github.com/raycast/script-commands)
metadata. The built-in `builtin.scripts` plugin scans the folders of its
**Script directories** setting (default `%APPDATA%\golauncher\scripts`, `%VAR%`
is expanded) whenever the window is shown, and lists every script that has a
`@raycast.title` in the root search under *Script Commands*.

```powershell
# @raycast.schemaVersion 1
# @raycast.title Greet
# @raycast.mode compact
# @raycast.packageName Examples
# @raycast.argument1 { "type": "text", "placeholder": "Name", "optional": true }
param([string]$Name = "World")
Write-Output "Hello, $Name!"
```

- **Metadata** is read from comments (`#`, `//`, `--`, `REM`, `::`) in the
  first 40 lines: `title` (required), `mode`, `packageName` (subtitle; defaults
  to the folder name), `icon`, `argument1..3`, `needsConfirmation`,
  `currentDirectoryPath`, `description`, `author`, `authorURL`. `schemaVersion`,
  `iconDark` and `refreshTime` are accepted and ignored. Files named
  `*.template.*` are skipped, as in Raycast.
- **Interpreters**: `.ps1` → `pwsh` if installed, else Windows PowerShell
  (`-NoProfile -ExecutionPolicy Bypass -File`); `.py` → `python` (or `py`);
  `.js` → `node`; `.bat`/`.cmd` → `cmd /c`; `.sh` → Git Bash (WSL's `bash.exe`
  is not used). A `#!` first line wins (`#!/usr/bin/env node`). A missing
  interpreter is reported when the script runs.
- **Arguments** (up to 3; `text`, `password`, `dropdown` with `data`) are typed
  next to the search field and passed as positional arguments, empty when an
  optional one is left out. `percentEncoded` arguments are URL-encoded.
- **Modes**: `silent` hides the window and runs in the background (failures
  are only logged); `compact` waits up to 25 s and shows the last output line
  as a toast (red on a non-zero exit code); `fullOutput` opens a view that
  streams stdout and stderr, shows the exit code and can stop the script;
  `inline` is treated as `compact`.
- **Environment**: scripts run in their own folder (or
  `currentDirectoryPath`) without a console window, with `GOLAUNCHER=1`
  set. Stopping a script ends everything it started. Output that is not
  UTF-8 is decoded from the OEM code page; ANSI colors are removed.
- **Icons**: PNG and SVG files (relative to the script) are shown; emoji and
  URLs fall back to a generic icon.