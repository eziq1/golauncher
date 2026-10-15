# golauncher

A Raycast-style launcher for Windows, written in Go with the
[MyGo](https://github.com/egoist/mygo) native UI toolkit.

[简体中文](README.zh-CN.md)

- Press **Alt+Space** anywhere to search apps, commands, files and math.
- Everything is a plugin. The built-in features use the same
  [plugin SDK](pkg/plugin/) that third-party plugins use.
- **Built-in plugins**
  - **Applications**: Start menu apps, including Store apps, with pinyin
    matching (`wx` finds 微信).
  - **Calculator**: exact decimal arithmetic, functions, and base conversion
    (`255 in hex`).
  - **Clipboard History** (`Alt+Shift+V`): text, images and files, with full-text
    search. Copies from password managers are skipped.
  - **File Search**: powered by [Everything](https://www.voidtools.com/), when it
    is installed.
- **Settings window** (`Ctrl+,`): set hotkeys, aliases, plugin preferences and
  the theme.
- **`glctl`**: a headless CLI that drives the same engine, so you or an AI agent
  can test every flow without the GUI.

> **Status:** v0.1 is a first public preview. Its UI is English-only, but every
> string goes through the i18n pipeline. See the
> [design document](docs/design.md) for the architecture and the roadmap.

## Install

Download `golauncher_<version>_windows_<arch>.zip` from
[Releases](https://github.com/eziq1/golauncher/releases), unzip it anywhere and
run `golauncher.exe`. It lives in the system tray.

Requirements:

- Windows 10 or 11. The Acrylic panel background requires Windows 11.
- The WebView2 runtime is not needed.

```powershell
golauncher.exe --show       # show the launcher
golauncher.exe --settings   # open Settings
golauncher.exe --quit       # quit the running instance
```

| What | Where |
|------|-------|
| Configuration | `%APPDATA%\golauncher\config.json` |
| Databases, clipboard history | `%LOCALAPPDATA%\golauncher\data\` (never roams) |
| Logs | `%LOCALAPPDATA%\golauncher\logs\golauncher.log` |

## Keyboard

| Key | Action |
|-----|--------|
| `Alt+Space` | Show or hide the launcher (configurable) |
| `↑` `↓`, `Ctrl+N` `Ctrl+P` | Move the selection |
| `Enter` / `Ctrl+Enter` | Run the primary / secondary action |
| `Ctrl+K` | Open all actions of the selected item |
| `Esc` | Clear the search, go back, or hide |
| `Ctrl+,` | Open Settings |

## Build from source

Requires Go 1.27 or later. No cgo is needed.

```powershell
git clone https://github.com/eziq1/golauncher
cd golauncher
pwsh scripts/build.ps1          # builds dist/golauncher.exe and dist/glctl.exe
# or by hand:
go build -ldflags "-H windowsgui" -o golauncher.exe ./cmd/golauncher
go build -o glctl.exe ./cmd/glctl
```

## Test

```powershell
go test ./...                                  # unit, integration, golden and lint tests
go test -race ./internal/... ./plugins/...     # needs a C compiler for -race
go test ./internal/cli -update                 # refresh golden files after an intended output change
```

Tests that touch the real clipboard are skipped unless you opt in with `$env:GOLAUNCHER_CLIPBOARD_TESTS=1` (they restore the clipboard afterwards).

The end-to-end test drives a running GUI through a local control socket, which is only enabled when golauncher is started with `GOLAUNCHER_CONTROL=1`:

```powershell
$env:GOLAUNCHER_CONTROL=1; .\golauncher.exe
$env:GOLAUNCHER_E2E=1; go test ./internal/shell -run RunningInstance
```

## glctl: the headless CLI

```powershell
glctl query vsc --fixtures testdata/fixtures --format text
glctl query "12*3+4"
glctl run builtin.clipboard/history --text hello --fixtures testdata/fixtures
glctl session new  -s s.json --fixtures testdata/fixtures
glctl session type -s s.json "clipboard history"
glctl session key  -s s.json Enter
glctl i18n check
glctl attach type vsc           # drive a GUI started with GOLAUNCHER_CONTROL=1
```

By default `glctl` runs in an isolated, in-memory environment with a fixed
clock. Actions are recorded and printed, not performed. To use your real
configuration and data, pass `--real`. To actually perform actions, also pass
`--exec`.

## Writing plugins

See the [plugin guide](docs/plugin-guide.md).

## Project layout

| Path | Contents |
|------|----------|
| [`pkg/plugin`](pkg/plugin/) | Plugin SDK |
| [`internal/core`](internal/core/) | Engine and interaction state machine (UI-independent) |
| [`internal/ui`](internal/ui/), [`internal/shell`](internal/shell/) | GUI: views, windows, tray, hotkeys |
| [`internal/cli`](internal/cli/) | `glctl` |
| [`plugins/`](plugins/) | Built-in plugins |
| [`internal/winapi`](internal/winapi/) | Win32/COM bindings (pure Go) |
| [`docs/`](docs/) | Design document and guides |

## License

[MIT](LICENSE)
