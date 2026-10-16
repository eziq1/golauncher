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
| [`scripts`](../plugins/scripts/) | run processes, stream output into a view with `Invalidate`, take arguments |
| [`clock`](../plugins/clock/) | score prefixed root queries itself, tick every second with `Invalidate` (root) and `Render` (view) |
| [`timer`](../plugins/timer/) | keep state in `Storage`, run a scheduler on `Host.Go`, notify with `Host.Toast` |
| [`quicklinks`](../plugins/quicklinks/) | offer fallback items, take keyword-prefixed text, create and edit data with forms, fetch in the background with debouncing and cancellation |
| [`processes`](../plugins/processes/) | sample only while shown (one loop for the root search and every open view), put system calls behind a `Backend` with a fixture and a dry-run variant, show values with bars (`Accessories`), keep sensitive parts from AI, need a space after a prefix (`Query.Raw`) |

A prefix that must be followed by a space (`ps ` but not `ps`, which finds
PowerShell) cannot be told apart in `Query.Text`, which the host trims:
read `Query.Raw`, the root search text as typed (empty outside the root
search, so fall back to `Text`).

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

Every new plugin ships **both `locales/en.json` and `locales/zh-CN.json`**
(Simplified Chinese, following the glossary and rules in
[i18n-zh-cn.md](i18n-zh-cn.md)); `glctl i18n check` fails on a key missing
from `zh-CN.json`. Chinese has a single plural form, so plural messages need
only `"other"`; where English says the `one` form differently (say, with
`{{.name}}`), add an exact form `"=1"`. Keyword messages list Chinese words
and their pinyin initials; the English keywords and title match too.

Register the plugin in [`internal/builtin`](../internal/builtin/builtin.go).

## Interfaces

A plugin implements `plugin.Plugin` plus any of the optional interfaces below.

| Interface | Called for |
|-----------|------------|
| `RootProvider` | Every keystroke in the root search. It must be fast, and results arriving after 150ms are dropped. |
| `ViewProvider` | Views opened by a `ModeView` command (the view ID is the command name) or by an `ActionPush`. It is called again on every keystroke, unless the view sets `FilterLocally`. |
| `ActionHandler` | `ModeNoView` commands (`ActionRunCommand`) and your own callbacks (`ActionCallback`). It returns a `Result`, which can push a view, show a toast, hide the window, pop to the root or refresh. |
| `LocaleProvider` | Your message catalogs. |
| `PreferenceOptionsProvider` | The options of preferences declared with `DynamicOptions`. |

### Commands

Commands declared in the manifest appear in the root search under
"Commands". Users can give them an alias and a global hotkey in Settings.

- Mark commands that delete data `Destructive: true`, and give such actions
  `Style: plugin.StyleDestructive`. The host then asks the user to confirm
  before running them.
- Also set `EndsSession: true` on destructive actions that end the Windows
  session (shut down, restart, sign out): the confirmation then says how
  many background tasks would be interrupted.
- `ConfirmMessage` replaces the confirmation's default text ("This cannot
  be undone.") with what the action will do, e.g. `l.N("end.confirm", n)`
  for "12 processes will be ended.". It is a user-visible string, so it
  comes from your catalog. It is also shown when an AI asks to run the
  action, destructive or not. `EndsSession`'s own message takes precedence.

### Preferences

Declare preferences in the manifest and Settings renders a form for them.
Read them with `Host.Preferences()`; values fall back to the declared
`Default`. `password` preferences are stored in Windows Credential Manager,
not in `config.json`.

Preferences can also be:

- **Grouped**: preferences with the same `Group` (may be a `%key%`) are shown
  under one heading, after the ungrouped ones.
- **Conditional**: `VisibleWhen: &plugin.Condition{Pref: "mode", Equals: plugin.MustPayload("advanced")}`
  shows the preference only while another one has that value (compared as
  JSON values; the default counts when nothing is set).
- **Buttons**: a `PrefButton` with an `Action` (usually an `ActionCallback`
  handled by your `HandleAction`), for "Sign in" or "Test connection". The
  `Toast` of the result is shown next to the button.
- **Dynamic selects**: a `PrefSelect` with `DynamicOptions: true` gets its
  options from your `PreferenceOptionsProvider.PreferenceOptions(ctx, name)`
  each time its plugin page opens. Errors are shown under the field; the
  call has the same time limit as a view.

### Command arguments

A command (or any `Item`) can declare up to three `Arguments`, which are
typed inline next to the search field, as in Raycast:

```go
plugin.Command{Name: "greet", Title: "%command.greet.title%", Mode: plugin.ModeView,
	Arguments: []plugin.Argument{
		{Name: "name", Type: plugin.ArgText, Placeholder: "%arg.name%", Required: true},
		{Name: "tone", Type: plugin.ArgDropdown, Placeholder: "%arg.tone%",
			Options: []plugin.Option{{Value: "warm", Title: "%tone.warm%"}}},
	}}
```

- With the entry selected, `Tab` moves from the search field into the
  arguments and between them; `Shift+Tab` goes back.
- `Enter` runs the primary action only when every required argument has a
  value; otherwise the first empty one gets the focus.
- The values reach your plugin as strings: in `ViewRequest.Arguments` for a
  view command or an `ActionPush`, and in `ActionDo.Values` (JSON strings)
  for no-view commands and callbacks.
- A global hotkey of a command with required arguments shows the command in
  the root search with its first argument focused instead of running it.
- In `glctl`: `glctl session arg -s s.json name Ada`.

### Form views

Return a view of kind `ViewForm` to collect structured input. The host keeps
the values, the focus and the errors, like the navigation stack:

```go
return plugin.View{Kind: plugin.ViewForm, Title: l.T("form.title"), Form: &plugin.Form{
	Fields: []plugin.Field{
		{ID: "subject", Type: plugin.FieldText, Title: l.T("form.subject"), Required: true},
		{ID: "count", Type: plugin.FieldNumber, Title: l.T("form.count"), Default: plugin.MustPayload(3)},
		{ID: "urgent", Type: plugin.FieldCheckbox, Title: l.T("form.urgent")},
	},
	Submit: plugin.Action{ID: "send", Title: l.T("form.send"),
		Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "send"}},
}}, nil
```

- Field types: `text`, `password`, `textarea`, `number`, `checkbox`,
  `dropdown` (with `Options`), `date` (`"YYYY-MM-DD"`) and `file` (a path).
  `Info` is shown under the field.
- Keys: `Tab`/`Shift+Tab` move between fields, `Enter` moves to the next
  field and submits from the last one (it breaks the line in a textarea),
  `Ctrl+Enter` submits from anywhere, `Escape` goes back.
- Before calling you, the host checks `Required` fields, numbers, dates and
  dropdown choices and shows its own messages.
- `Submit` runs with `ActionDo.Values`: numbers as JSON numbers (`null` when
  empty), checkboxes as booleans and everything else as strings. If `Submit`
  is an `ActionPush`, the values also arrive as `ViewRequest.Arguments`.
- To reject the values, return `Result{FieldErrors: map[string]string{"subject": l.T("err.tooShort")}}`:
  the form stays open with the messages under the fields. Return
  `PopToRoot`, `Push` or a `Toast` on success.
- Values typed survive when the form is pulled again (same field IDs).
- In `glctl`: `glctl session field -s s.json subject "Hello"`, then
  `glctl session key -s s.json Ctrl+Enter`.

### Updating what is shown: Invalidate

Views and root results are pulled: the host calls `View` / `RootItems` when
the user types or navigates. When your data changes on its own (a new
clipboard entry, an index that finished refreshing), call
`Host.Invalidate(viewID)` from any goroutine:

- If one of your views with that ID is on top (`""` means any of your
  views), the host pulls it again.
- If the root search is on top and you are a `RootProvider`, the host pulls
  the root results again.
- Bursts are coalesced (one refresh per 50ms), a refresh never cancels a
  search in flight, and the selected row is kept by item ID. So it is fine
  to call it often, but call it only when something changed.

### Pushing views: Render

For content that changes continuously (progress, streaming output), a view
can also be pushed. Every opened view gets an instance ID in
`ViewRequest.Instance` (for example `"v17"`); it changes each time the view
is opened. Push a complete new `View` for it with
`Host.Render(req.Instance, v)` from any goroutine:

- Pushes for instances that are no longer open are dropped.
- The host applies at most one push per 16ms per instance and keeps only the
  latest; pushes are ordered by arrival.
- The selection is kept by item ID, as with Invalidate.
- The next pull (the user typing) replaces the content again, so `View`
  should return the same current state.
- Implement `plugin.ViewCloser` to hear when an instance closes, so a timer
  pushing to it can stop. Also stop on the `window-hidden` event: while the
  launcher is hidden nothing is shown, and showing it again resets it to the
  root search (closing open views).

Prefer `Invalidate` unless pulling is too slow or too coarse: pulled views
stay simple, testable and deterministic.

### Values at the right edge: Accessories

`Item.Accessory` is one short text at the right edge of a row ("Application").
For values, such as a process's CPU and memory use, add `Item.Accessories`
after it, each a text with an optional small bar:

```go
cpu := 0.032 // a fraction in [0, 1]
plugin.Item{ID: "pid:4242", Title: "worker.exe", Accessories: []plugin.Accessory{
	{Text: l.T("cpu", "value", "3.2%"), Meter: &cpu, Load: true},
	{Text: "1.2 GB"},
}}
```

- `Meter` (nil for none) draws the bar, `0` empty to `1` full; the host
  clamps other values.
- `Load: true` colors it as a load, in the theme's colors: warning from 0.5,
  danger from 0.8. Without it the bar has the success color, as a progress.
- The host gives each value a fixed width with the text right-aligned, so
  values that change every second (a view refreshed with `Invalidate`) do not
  move the row. Keep the texts short: about seven characters.
- `glctl` shows the texts after the accessory, `(Process · CPU 3.2% · 1.2 GB)`;
  AI sees them joined the same way as the item's info.

### Acting on the search text: fallbacks and Tab

Two optional interfaces let a plugin act on whatever the user typed (the
AI chat, `builtin.ai`, uses both; a "search the web" plugin would too):

- **`FallbackProvider.FallbackItems(ctx, q)`** is called only when the root
  search found nothing good: no results, or none matching better than
  `core.FallbackThreshold` (0.5, before frecency). Its items are listed
  last under *Fallback Commands* and never recorded in Recent, so put the
  text into the action's payload.
- **`TabProvider.TabAction(q)`**: `Tab` in the root search with text runs
  the action of the first enabled plugin that answers `ok` — unless the
  selected entry takes arguments, which keep `Tab`.
- **`Item.Transient`**: a root item that carries the typed text (a web
  search for it, a typed URL) can set it so that, like fallback items, it
  is never recorded in Recent and the text is not stored.

Fallback items are listed by **`FallbackOrderer.FallbackOrder()`**, lowest
first, and in the order the plugins are registered where that is equal (0
for a plugin without the interface). A generic catch-all returns
`plugin.FallbackOrderLast` to come after every other fallback: the web
search of `builtin.quicklinks` does, so *Ask AI* of `builtin.ai` comes
first.

These are in-process only for now; the external plugin protocol does not
carry them.

### Chat-like views

A `ViewDetail` is shown across the whole window: `View.Detail` (Markdown,
then its metadata), scrolled to its end as it grows. Its first item is not
listed but supplies the actions (`Enter`, `Ctrl+K`, shortcuts; `Up`/`Down`
and `Ctrl+N`/`Ctrl+P` do not move a selection there), and the search field
is an input:

- callbacks receive the typed text in `ActionDo.Query`;
- `Result{ClearText: true}` empties the field and pulls the view again;
- `View.Escape` (an action) takes `Escape`, e.g. *Stop* while an answer is
  streaming with `Host.Render`; leave it nil and `Escape` goes back.

`Result.Preferences` stores values of the plugin's own (non-password)
preferences, as Settings would — for an *Enable* button in a first-use
prompt.

### How your plugin appears to AI

Claude (the launcher's AI chat, Claude Desktop, Claude Code) can use the
launcher's plugins through five generic tools (docs/ai.md §4): it lists the
plugins and their commands, searches the root search, opens view commands,
reads an item's `Detail` and runs an item's actions. You write no code for
this: your commands, root items, views, forms and actions are what it sees,
without icons or shortcuts, and items come back with short-lived handles so
it can only act on what your plugin actually returned.

- **Risk levels decide what asks the user first.** `Push` (opening a view)
  runs directly; `Open` and the copy actions run directly unless the user
  chose *Ask every time*; `Callback`, `RunCommand`, `RunProcess`, `Paste`,
  form submits and no-view commands ask every time (the user may allow the
  same action for the rest of the session); `StyleDestructive` actions and
  `Destructive` commands always ask, and `EndsSession` adds the background
  task warning.
- **`Action.ReadOnly`**: set it on a callback that only reads — *Refresh*,
  *Show details*, a lookup — and changes nothing on the computer or in your
  data. AI then runs it without asking. Never set it on anything that
  installs, deletes, sends or writes; a destructive action is never treated
  as read-only. The field travels in the protocol's JSON (`"readOnly"`), so
  external plugins can set it too.
- **`Item.Sensitive`**: set it on items whose content must never reach an
  AI (the clipboard history marks clips copied from password managers). The
  user still sees them; AI never does, even when the user lets AI use your
  plugin.
- **`Item.SubtitleSensitive`** and **`Metadata.Sensitive`**: for an item AI
  may see but with a part it may not — a window title naming a document,
  a command line that may hold a password. AI gets the item without that
  subtitle or detail row, and a search does not find the item by its
  sensitive subtitle. The user sees everything.
- **`Action.ConfirmMessage`** is shown in the confirmation AI asks for, so
  say there what the action will do ("3 processes will be ended.").
- **Exposure**: the user decides which plugins AI may use (Settings ›
  Plugins › AI). Built-in plugins are on by default except the clipboard
  history; plugins in their own process (third-party) are off until the user
  turns them on.
- Write clear titles, subtitles and `Detail` Markdown: they are all the
  model knows about an item. Views that load in the background and call
  `Invalidate` are rendered again for a moment, so results that arrive
  quickly are included.

### Context: acting on what the user was doing

When the launcher opens, the host looks at the window that was in the
foreground (unless the user turned **Context awareness** off in Settings)
and describes it as a `plugin.Context`:

```go
type Context struct {
    App      AppInfo           // Name ("explorer.exe"), Path, PID, Package (AUMID)
    Kind     string            // "explorer", "desktop", "path-in-title", or "" if unrecognized
    Folder   string            // the folder shown, absolute; "" for virtual folders
    Selected []string          // selected files and folders, absolute
    Title    string            // the window title
    Extra    map[string]string // detector details, e.g. "file" for a path-in-title file
}
```

Implement `plugin.ContextProvider` to offer items for it:

```go
func (p *Plugin) ContextItems(ctx context.Context, c plugin.Context) ([]plugin.Item, error) {
    if c.Folder == "" {
        return nil, nil // nothing to offer
    }
    l := p.h.I18n()
    return []plugin.Item{{
        ID: "git-status", Title: l.T("item.gitStatus"), Subtitle: c.Folder,
        Actions: []plugin.Action{{ID: "run", Title: l.T("action.run"),
            Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "status",
                Payload: plugin.MustPayload(c.Folder)}}},
    }}, nil
}
```

- The items of all providers are shown in an **"In <folder>"** section at the
  top of the empty root search, and they take part in the root search when
  the user types (with a small ranking bonus). Their `Group` is set by the
  host.
- Detection runs in the background (at most 300ms) so the window appears at
  once; when the context arrives after the root was shown, the host pulls it
  again. `ContextItems` is called for every root search, so keep it fast and
  return nil when the context does not apply.
- `RootItems` also receives the context in `Query.Context` (nil when none).
- The context lives in memory for one show only. Context items are never
  recorded in Recent, so put everything an action needs (the folder, the
  paths) into the action itself.
- Built-in detectors: File Explorer (the active tab on Windows 11, with the
  selected items), the desktop, and any window whose title holds an
  existing path (editors, terminals, other file managers). More are added in
  `internal/winapi/context` (`wincontext.Register`).
- Test with an injected context:
  `glctl query "" --context-folder C:\tmp --context-select C:\tmp\a.txt`
  (also `--context-app NAME`, and `glctl session new … --context-folder`).
  The built-in `builtin.files` plugin is a complete example.

External plugins get the context in the `rootItems` params and through the
`contextItems` method (see [plugin-protocol.md](plugin-protocol.md)).

### Background tasks

Long work (`scoop update`, a build, a download) should not keep the user
waiting in your view. Hand it to the host as a **background task**: the
action returns at once, and the host queues and runs the task, keeps its
console output, lets the user stop or rerun it, and notifies when it ends.
The user sees all tasks in **Background Tasks** (the `tasks` command, or
`Ctrl+J`, or the spinner in the launcher's footer). See
[background-tasks.md](background-tasks.md) (Chinese) for the design.

```go
func (p *Plugin) HandleAction(ctx context.Context, a plugin.ActionDo) (plugin.Result, error) {
	id := p.h.RunProcess(plugin.TaskSpec{
		Title:  l.T("task.build", "project", name), // "Build myapp"
		Icon:   plugin.Icon{Name: "package"},
		Queue:  "myplugin",          // one at a time
		Notify: plugin.NotifyAlways, // default: only failures
		OnDone: func(r plugin.TaskResult) { // on a background goroutine
			if r.State == plugin.TaskSucceeded {
				p.reload()
				p.h.Invalidate("")
			}
		},
	}, plugin.ProcessSpec{Argv: []string{"go", "build", "./..."}, Dir: dir})
	// Open the task's live output, or just say that it runs:
	return plugin.Result{Do: &plugin.ActionDo{Kind: plugin.ActionShowTask, Target: id}}, nil
}
```

- **`RunProcess(spec, cmd)`** runs a command line: without a console window,
  in a Job Object (Stop or quitting golauncher ends the whole process tree),
  with stdout and stderr as the output. Lines that are not UTF-8 are decoded
  from the OEM code page; ANSI colors and `\r` progress bars are cleaned up.
  A `.bat`/`.cmd` as `Argv[0]` runs through cmd.exe with correct quoting.
  `Env` entries are added to the launcher's environment; `CommandLine` is
  what the user sees when `Argv` is an implementation detail. It needs the
  `exec` permission: without it the task fails at once.
- **`RunTask(spec, fn)`** runs Go code: `fn(ctx, task)` writes output to
  `task` (an `io.Writer`) and may call `task.SetStatus("Downloading 3/5")`
  and `task.SetProgress(0.6)`. It must return when `ctx` is cancelled. A
  panic fails the task instead of crashing the launcher. (External plugins
  cannot show Go tasks in the launcher; `rpcserve` runs them unseen.)
- **Queues**: tasks with the same non-empty `Queue` run one after the other,
  also across plugins; other tasks run in parallel, at most 4 at once.
- **Notifications**: `NotifyOnFailure` (default), `NotifyAlways` or
  `NotifyNever`; nothing is shown after the user stopped a task. The
  notification is a toast in the launcher when it is visible, else a
  Windows notification that opens the task when clicked.
- **Learning the outcome**: events carry no payload, so set
  `TaskSpec.OnDone`; it receives the task's ID, state (`succeeded`,
  `failed`, `cancelled`), exit code, error and last output line, and runs
  again when the user reruns the task. The event `task-finished` is also
  published after every task (of any plugin).
- **Actions without code**: an action of kind `ActionRunProcess` (payload
  `plugin.RunProcessPayload{Spec, Cmd}`) starts a task directly, and
  `ActionShowTask` (target: a task ID, or `""` for all tasks) opens
  Background Tasks. A `Result.Do` runs such a host action after your
  handler, e.g. to show the task it started.
- **Toasts from anywhere**: `Host.Toast(t)` shows a toast from a background
  goroutine (in the launcher when it is visible, else as a Windows
  notification; `Toast.Silent` makes that notification silent).
- The host keeps the last 50 finished tasks with their full logs in
  `%LOCALAPPDATA%\golauncher\tasks`; the views show the last 2 MB of
  output.
- **Testing**: `hostfake` runs tasks with a fake runner that records each
  `ProcessSpec` and ends it at once (`h.Runner.Commands()`, configurable
  output with `h.Runner.Results` or `h.Runner.Hook`); `h.WaitTasks()` waits
  for them and their `OnDone`, and `h.Toasts()` returns the toasts. Use
  `h.SetTaskRunner(tasks.ProcessRunner{})` to run real programs. In `glctl`'s
  fake environment tasks are recorded too and held until a
  `glctl session wait-tasks` step; `glctl tasks list|show <id> -s s.json`
  prints them, and `--fixtures DIR` with a `tasks.json`
  (`[{"match": "scoop update", "output": "…", "exitCode": 1}]`) sets their
  output.

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
| `Events()` | Host events: `clipboard-changed`, `window-shown`, `window-hidden`, `locale-changed`, `config-changed`. |
| `I18n()` | Your translations plus number and relative-time formatting. |
| `Now()` | The current time. Use it rather than `time.Now()` so tests can fix the clock. |
| `Logger()` | A logger. |
| `Invalidate(viewID)` | Asks the host to pull your open view (or the root results) again; see "Updating what is shown". |
| `Render(instance, view)` | Pushes new content for an open view instance; see "Pushing views". |
| `RunProcess(spec, cmd)`, `RunTask(spec, fn)` | Runs long work as a background task of the host; see "Background tasks". |
| `Toast(t)` | Shows a toast from any goroutine: in the launcher when it is visible, else as a Windows notification. |
| `Go(fn)` | Runs background work on a goroutine. A panic in `fn` is logged and contained, so it cannot crash the launcher. Always use it instead of `go`; to wait for the work, call `wg.Add(1)` first and `defer wg.Done()` inside `fn`. |

## Rules enforced by tests

- Plugins import only `pkg/plugin`, `internal/winapi/...`, the standard
  library and an allowlist of modules. Host internals are off limits.
- No `go` statements or `WaitGroup.Go` in plugins; start goroutines with `Host.Go`.
- No user-visible string literals. Use the Localizer, and use `%key%` in
  manifests. `glctl i18n check` verifies that every key exists, in `en.json`
  and in `zh-CN.json`, with the same template arguments.

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
  as a toast (red on a non-zero exit code); `fullOutput` runs the script as
  a background task and opens it in Background Tasks, which streams stdout
  and stderr, shows the exit code and can stop or rerun the script (the
  script's *Show in Background Tasks* action, `Ctrl+T`, opens its last run
  again); `inline` is treated as `compact`.
- **Environment**: scripts run in their own folder (or
  `currentDirectoryPath`) without a console window, with `GOLAUNCHER=1`
  set. Stopping a script ends everything it started. Output that is not
  UTF-8 is decoded from the OEM code page; ANSI colors are removed.
- **Icons**: PNG and SVG files (relative to the script) are shown; emoji and
  URLs fall back to a generic icon.

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
