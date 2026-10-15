# Writing a golauncher plugin

Everything golauncher does is implemented as a plugin, including the built-in
application launcher, calculator, clipboard history and file search. This
guide shows how to write one. Built-in plugins are Go code compiled into the
launcher; [external plugins](#external-plugins) are programs in any language
that run in their own process and talk to golauncher over JSON-RPC.

The SDK lives in [`pkg/plugin`](../pkg/plugin/). The built-in plugins in
[`plugins/`](../plugins/) are complete examples:

| Plugin | Shows how to |
|--------|--------------|
| [`calc`](../plugins/calc/) | add a result to the root search |
| [`apps`](../plugins/apps/) | supply many candidates, run background work, offer several actions |
| [`clipboard`](../plugins/clipboard/) | build a command with its own searchable view, a detail pane and callbacks |
| [`everything`](../plugins/everything/) | depend on external software and report when it is unavailable |

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

## External plugins

An external plugin is a directory with a `plugin.json` manifest and a
program. golauncher starts the program when the plugin is first used and
talks to it over JSON-RPC 2.0 on stdin/stdout, one JSON object per line.
The plugin can be written in any language; a crash or hang only affects the
plugin, which golauncher restarts (and turns off if it keeps crashing).

| Example | Shows |
|---------|-------|
| [`examples/plugins/go-hello`](../examples/plugins/go-hello/) | A Go plugin run with `rpcserve`: root item, list view, callback, no-view command, storage |
| [`examples/plugins/python-hello`](../examples/plugins/python-hello/) | Standard-library Python: root item, a form whose submit writes the clipboard, storage |

### Manifest

`plugin.json` is the JSON form of `plugin.Manifest` plus two fields:

```json
{
  "id": "example.go-hello",
  "name": "%plugin.name%",
  "version": "0.1.0",
  "protocol": 1,
  "run": { "command": "go-hello.exe", "args": [] },
  "permissions": ["storage"],
  "commands": [{ "name": "greetings", "title": "%command.greetings.title%", "mode": "view" }]
}
```

- `protocol` is the protocol version (1).
- `run.command` is a path relative to the plugin directory or a program on
  `PATH` such as `python` or `node`. The working directory is the plugin
  directory.
- `%key%` strings are translated from `locales/<lang>.json`, as for
  built-in plugins. Strings your plugin returns at run time are yours to
  translate: `initialize` and `localeChanged` tell you the locale.
- `permissions` gate the host APIs: a plugin without `storage` cannot call
  `host/storage.*`, one without `clipboard` cannot call
  `host/clipboard.*` (error `-32001`). `network` and `exec` cannot be
  enforced; external plugins run with the user's rights, and Settings says
  so.

### Installing and developing

golauncher loads external plugins from `%APPDATA%\golauncher\plugins\<dir>\`
and from the directories listed in `GOLAUNCHER_PLUGIN_PATH` (separated by
`;`; an entry can be a plugin directory itself). Settings shows each
plugin's origin and process state (running, stopped, crashed, turned off
after repeated crashes, or unavailable with the reason).

During development, use `glctl`, which runs an isolated environment:

```powershell
glctl plugin validate examples/plugins/python-hello      # manifest, protocol, %key%s, run.command
glctl query hello --plugins examples/plugins/python-hello --format text
go build -o examples/plugins/go-hello/go-hello.exe ./examples/plugins/go-hello
glctl run example.go-hello/greetings --plugins examples/plugins/go-hello
```

`--plugins DIR` may be repeated and also accepts a directory of plugin
directories. glctl never loads the plugins in your `%APPDATA%` folder unless
you pass `--real`.

### Go: `rpcserve`

A Go plugin written against `pkg/plugin` becomes an external plugin with
one line:

```go
func main() {
	if err := rpcserve.Serve(&Hello{}); err != nil {
		log.Fatal(err)
	}
}
```

Inside the process, `rpcserve` provides a `plugin.Host` that forwards
`Storage`, `Clipboard`, `Events`, `Invalidate` and `Render` to golauncher,
keeps `Preferences` and `I18n` in sync with it, and logs to stderr. Panics in
your handlers become error responses and `Host.Go` contains panics, so the
process survives them. `os.Stdout` is redirected to stderr so stray prints
cannot corrupt the protocol.

### Other languages

Implement the protocol: [plugin-protocol.md](plugin-protocol.md) is the
complete reference. In short:

| Direction | Messages |
|-----------|----------|
| host → plugin requests | `initialize`, `rootItems`, `view`, `handleAction`, `preferenceOptions`, `shutdown` |
| host → plugin notifications | `$/cancelRequest`, `localeChanged`, `preferencesChanged`, `event`, `viewClosed` |
| plugin → host requests | `host/storage.get/set/delete`, `host/clipboard.read/writeText/writeImage/writeFiles`, `host/subscribe`, `host/log` |
| plugin → host notifications | `host/invalidate`, `ui/render` |

Keep stdout for protocol messages and log to stderr. Answer `rootItems`
quickly (150 ms), honor `$/cancelRequest`, and exit when stdin closes.
