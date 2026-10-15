<p align="center">
  <img src="assets/icon.svg" width="128" height="128" alt="golauncher icon">
</p>

<h1 align="center">golauncher</h1>

<p align="center">
  A Raycast-style launcher for Windows, written in Go with the
  <a href="https://github.com/egoist/mygo">MyGo</a> native UI toolkit.
  <br>
  <a href="README.zh-CN.md">简体中文</a>
</p>

- Press **Alt+Space** anywhere to search apps, commands, files and math.
- Everything is a plugin. The built-in features use the same
  [plugin SDK](pkg/plugin/) that third-party plugins use.
- **Context aware**: open it from File Explorer or the desktop and the top of
  the list offers actions for that folder and the selected files: open a
  terminal or VS Code there, create a folder or text file, copy paths,
  compress to ZIP, copy a SHA-256, search the folder with Everything. Works
  with Windows 11 tabs, and with any window whose title shows a path. Nothing
  is stored, and it can be turned off in Settings.
- **Built-in plugins**
  - **Applications**: Start menu apps, including Store apps, with pinyin
    matching (`wx` finds 微信).
  - **Calculator**: exact decimal arithmetic, functions, and base conversion
    (`255 in hex`), plus **unit conversion**: `10 km in mi`, `30 ℃`,
    `5 GB / 100 Mbps`, `3 斤 to kg`, `8 L/100km in mpg`, `16 px in rem`,
    currencies (`100 usd in cny`, free rates without an API key, cached for
    offline use), timestamps (`1700000000 to date`, `now in unix`), colors
    (`#ff8800 to hsl`) and clothing/shoe sizes (`US 9 shoe in EU`). Units have
    English, Chinese and pinyin names (`10 公里 换成 英里`).
  - **Clipboard History** (`Alt+Shift+V`): text, images and files, with full-text
    search. Copies from password managers are skipped.
  - **Files**: the context actions above (`builtin.files`).
  - **File Search**: powered by [Everything](https://www.voidtools.com/), when it
    is installed.
  - **Scoop**: search, install, update and remove [Scoop](https://scoop.sh)
    packages, when Scoop is installed.
  - **World Clock**: type `time` for your local time and major cities, or
    `time tokyo`, `time bj`, `time 北京`, `time utc+8`, `time pst` to find a
    city. The clocks tick; favorite cities stay on top.
- **Settings window** (`Ctrl+,`): set hotkeys, aliases, plugin preferences and
  the theme.
- **`glctl`**: a headless CLI that drives the same engine, so you or an AI agent
  can test every flow without the GUI.

> **Status:** v0.2 is an early public release. Its UI is English-only, but every
> string goes through the i18n pipeline. See the
> [design document](docs/design.md) for the architecture and the roadmap.

## Install

Download `golauncher_<version>_windows_<arch>.zip` from
[Releases](https://github.com/eziq1/golauncher/releases), unzip it and run
`golauncher.exe`. On first run it offers to install itself into
`%LOCALAPPDATA%\Programs\golauncher` (no administrator rights needed) with a
Start menu entry and an entry in *Settings › Apps* for uninstalling. Running a
newer or older download later offers to upgrade or downgrade; the same version
just starts the installed copy. Choose *Run without installing* (or pass
`--portable`) to run it from where it is. golauncher lives in the system tray.

Requirements:

- Windows 10 or 11. The Acrylic panel background requires Windows 11.
- The WebView2 runtime is not needed.

```powershell
golauncher.exe --show       # show the launcher
golauncher.exe --settings   # open Settings
golauncher.exe --quit       # quit the running instance
golauncher.exe --portable   # run without installing
golauncher.exe --uninstall  # remove the installation
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
$env:GOLAUNCHER_CONTROL=1; .\golauncher.exe --portable
$env:GOLAUNCHER_E2E=1; go test ./internal/shell -run RunningInstance
```

### Debugging the UI

Release builds (`scripts/build.ps1`, GitHub releases) leave MyGo's developer
tools out. For the inspector and the Performance panel, build a dev binary
(no version stamp, so the version is `dev`, the tools are on and the
installer is skipped):

```powershell
go build -ldflags "-H windowsgui" -o golauncher-dev.exe ./cmd/golauncher
.\golauncher.exe --quit   # only one instance runs at a time
.\golauncher-dev.exe
```

Then press **F12** or **Ctrl+Shift+I** in the launcher or Settings (pick an
element with **Ctrl+Shift+C**). A version-stamped build without the
`mygo_noinspector` tag shows them with `GOLAUNCHER_DEVTOOLS=1`;
`MYGO_FRAME_STATS=1` logs slow frames without the panel.

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

## Script Commands

Any script with [Raycast Script Commands](https://github.com/raycast/script-commands)
metadata becomes a command. Put `.ps1`, `.py`, `.js`, `.bat`/`.cmd` or `.sh`
files in `%APPDATA%\golauncher\scripts` (more folders can be added in the
settings), or run **Create Script Command** to start from a template:

```powershell
# @raycast.schemaVersion 1
# @raycast.title Flush DNS
# @raycast.mode compact
# @raycast.argument1 { "type": "text", "placeholder": "host", "optional": true }
ipconfig /flushdns | Select-Object -Last 1
```

Modes `silent`, `compact` and `fullOutput` are supported (`inline` runs as
`compact`). See the [plugin guide](docs/plugin-guide.md#script-commands).

## Scoop

When [Scoop](https://scoop.sh) is installed (`$SCOOP`, `%USERPROFILE%\scoop`,
or the folder set in the plugin settings), these commands appear:

| Command | What it does |
|---------|--------------|
| Search Scoop Packages | Search all bucket manifests by name, command or description as you type. `Enter` installs; installed apps offer update, hold/unhold, open folder and uninstall (`Ctrl+X`). |
| Installed Scoop Apps | Installed apps with their version, bucket and hold state; apps with a newer version first. |
| Scoop Updates | What `scoop status` would report, computed locally, with *Update All*. |
| Update All Scoop Apps / Update Scoop Buckets | `scoop update` and `scoop update *`. |
| Scoop Buckets | Added and known buckets; add, remove, or add a custom one by URL. |
| Clean Up Scoop | Shows how much old versions and the download cache take, then runs `scoop cleanup *` and `scoop cache rm *`. |

Searching reads Scoop's files directly (about 6,000 manifests in well under a
millisecond per query), so it does not start PowerShell. Changes run `scoop`
as background tasks, one at a time: the launcher stays usable, *Background Tasks*
(`Ctrl+J`, or the spinner in the footer) shows their live output, and a
notification tells when they finished.
Turn on *Search packages from the main search* to type `scoop <name>` or
`sc <name>` in the main search. Apps installed for all users (`-g`) are shown
read-only.

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
