<p align="center">
  <img src="assets/icon.svg" width="128" height="128" alt="golauncher icon">
</p>

<h1 align="center">golauncher</h1>

<p align="center">
  A Raycast-style launcher for Windows and macOS, written in Go with the
  <a href="https://github.com/egoist/mygo">MyGo</a> native UI toolkit.
  <br>
  <a href="README.zh-CN.md">简体中文</a>
</p>

- Press **Alt+Space** (**⌥Space** on a Mac) anywhere to search apps, commands,
  files and math. On macOS, ⌘ takes the place of Ctrl in every shortcut.
- Everything is a plugin. The built-in features use the same
  [plugin SDK](pkg/plugin/) that third-party plugins use.
- **Context aware**: open it from File Explorer, Finder or the desktop and
  the top of the list offers actions for that folder and the selected files:
  open a terminal or VS Code there, create a folder or text file, copy paths,
  compress to ZIP, copy a SHA-256, search the folder with Everything or
  Spotlight. Works with Windows 11 tabs, and with any window whose title (or,
  on a Mac, whose document) shows a path. Nothing is stored, and it can be
  turned off in Settings.
- **Selected text**: select some text in any app, press Alt+Space, and the
  top of the list offers actions for it: translate, ask AI (explain,
  summarize, rewrite, suggest replies, or type an instruction), search the
  web, open it if it is a link, calculate it if it is an expression, see the
  full text and its word count. It is read with UI Automation on Windows and
  the Accessibility API on macOS, without touching the clipboard; copying
  with Ctrl+C / ⌘C (the clipboard put back at once) is an opt-in fallback for
  apps that do not support it. The text is
  never stored and is sent to AI only when you pick an AI action
  ([docs/selected-text.md](docs/selected-text.md)).
- **Built-in plugins**
  - **Applications**: Start menu apps, including Store apps, on Windows; the
    apps in Applications and the system folders, with their localized names,
    on macOS. Pinyin matching (`wx` finds 微信).
  - **Calculator**: exact decimal arithmetic, functions, and base conversion
    (`255 in hex`), plus **unit conversion**: `10 km in mi`, `30 ℃`,
    `5 GB / 100 Mbps`, `3 斤 to kg`, `8 L/100km in mpg`, `16 px in rem`,
    currencies (`100 usd in cny`, free rates without an API key, cached for
    offline use), timestamps (`1700000000 to date`, `now in unix`), colors
    (`#ff8800 to hsl`) and clothing/shoe sizes (`US 9 shoe in EU`). Units have
    English, Chinese and pinyin names (`10 公里 换成 英里`).
  - **Clipboard History** (`Alt+Shift+V`, `⌥⇧V`): text, images and files, with
    full-text search. Copies from password managers are skipped.
  - **Files**: the context actions above (`builtin.files`).
  - **File Search**: powered by [Everything](https://www.voidtools.com/) on
    Windows, when it is installed, and by Spotlight on macOS.
  - **Scoop** (Windows): search, install, update and remove
    [Scoop](https://scoop.sh) packages, when Scoop is installed.
  - **WinGet** (Windows): search, install, upgrade and remove packages with
    the Windows Package Manager (`winget`), when it is installed.
  - **Homebrew** (macOS): search formulae and casks, install, upgrade and
    uninstall them, and list installed and outdated packages, when
    [Homebrew](https://brew.sh) is installed.
  - **System**: Lock, Sleep, Restart, Shut Down, Sign Out, Empty Recycle Bin
    or Trash, Toggle Dark Mode, volume and Turn Off Display (plus Hibernate
    and Show Desktop on Windows), with about 150 Windows Settings pages and
    the classic tools (Device Manager, Services, Registry Editor, …) on
    Windows, or the System Settings panes and utilities on macOS; searchable
    in English, Chinese and pinyin (`蓝牙`, `wifi`, `关机`, `gj`). Restart,
    Shut Down, Sign Out and emptying the bin ask first.
  - **Terminal**: on Windows, your Windows Terminal profiles (including
    fragments such as Git Bash) and WSL distributions with their running
    state; open one in a new window or tab, as administrator or in the
    current folder, set the default distribution, stop it, shut down or
    update WSL. On macOS, Terminal, iTerm2, Ghostty, WezTerm, Alacritty, kitty
    and Warp: a new window or tab, in the current folder, or running a
    command, with a default terminal of your choice.
  - **World Clock**: type `time` for your local time and major cities, or
    `time tokyo`, `time bj`, `time 北京`, `time utc+8`, `time pst` to find
    one of about 32,000 cities (every city of 15,000 people or more, from
    GeoNames). The clocks tick; favorite cities stay on top.
  - **Timers & Reminders**: `timer 25m`, `timer 1h30m tea`, `计时 半小时`
    start a countdown; `remind 15:30 meeting`, `remind tomorrow 9am`,
    `提醒 明天 9点 交周报`, `remind in 20m` set a reminder. `timer` lists
    the running ones with the time left (pause, resume, +5 min, restart,
    cancel and undo); a notification comes when they end. Timers survive
    restarts, and reminders missed while golauncher was not running are
    announced when it starts.
  - **Quicklinks & Web Search**: `g golang generics`, `bd 天气`, `gh`, `yt`,
    `wiki`, `zhihu`, `bili`… (20 built-in engines) search the web (add
    your own engines, change keywords); a typed URL (`github.com/foo`,
    `localhost:3000`) opens directly; when nothing matches, *Search Google
    for “…”* is offered (Bing in the Chinese UI). Quicklinks are your named
    links to websites, files, folders and programs with `{query}`,
    `{clipboard}` and `{date}` placeholders, created with a form and
    imported or exported as JSON. Search suggestions are off by default.
  - **Network Tools**: `ip` shows your adapters' addresses, gateway, DNS,
    Wi-Fi network and signal, and your public IP; `ping <host>` pings live
    with statistics; `dns <domain> [@server]` looks up records;
    `port 8080` shows (and can end) the process listening on a port, and
    `port host:443` checks whether a port can be reached; `wifi` copies the
    password of a saved network; `tracert <host>`, Flush DNS Cache and
    (on Windows) Renew IP Address run in the background.
  - **Processes**: `ps ` (with a space), `kill` or `进程` lists running apps
    and processes grouped by app, with live CPU and memory bars; search by
    name, window title, pinyin or PID. Enter switches to the window; End
    Task asks an app to close, Force End and End Process Tree ask first;
    elevated processes are ended through a one-off UAC prompt (on macOS,
    the administrator password prompt). Critical
    system processes and golauncher itself cannot be ended. *Quit <app>*
    acts on the app in front, and *Force Quit* is offered when it hangs.
  - **AI Chat** (off until you turn it on): press `Tab` after typing a
    question, or pick *Ask AI* when nothing matches, to chat with Claude
    through your installed [Claude Code](https://claude.com/product/claude-code)
    CLI and your Claude subscription (no API key). Answers stream in; ask
    follow-ups, stop with `Esc`, copy or paste the answer, continue in the
    Claude or ChatGPT app, and reopen chats from *AI Chat History* (kept 30
    days by default). Claude can use the launcher's plugins ("8080 port —
    who has it?", "install 7-Zip with winget"): it searches and runs them
    through golauncher's own tools, every step shows in the chat, and
    anything that changes something asks you first in the launcher
    (*Allow*, *Allow for this session*, *Deny*; destructive actions always
    ask). Web search is an option. Instead of Claude Code you can choose
    **Codex** (your ChatGPT account; golauncher runs the native Codex
    binary, e.g. the one the Codex app, Homebrew or npm installs) or
    **Gemini CLI** (your Google
    account; Gemini CLI itself needs Node.js) in *Settings › Plugins › AI ›
    Backend*. Each runs isolated from your own setup of that CLI (no other
    MCP servers, hooks, extensions or memory files, no file or shell tools),
    each chat stays on the backend it started with, and when Gemini asks for
    permission to use a tool you answer in the same dialog. *Settings ›
    Plugins › AI › Backends* shows, for each backend, whether it is
    available, which installed program it runs (path, version, where it was
    found) and the other installations it found; when one is installed more
    than once you can pick which one, or enter a path. Copy an answer,
    paste it elsewhere and reopen the launcher: you are back in the chat,
    with your unsent draft (within 10 minutes by default; *Settings ›
    General › Return to the main screen*).
  - **Use golauncher from Claude Desktop or Claude Code** (off until you
    turn on *Settings › Plugins › AI › Allow AI apps to use golauncher*):
    `golauncher mcp` is an MCP server for your plugins. The settings
    page connects Claude Desktop for you (only its `golauncher` entry, after
    a backup) and shows the `claude mcp add` command for Claude Code. You
    choose which plugins AI may use (clipboard history and third-party
    plugins are off by default); *AI Activity* lists every tool call of the
    last 30 days.
- **Settings window** (`Ctrl+,`, `⌘,`): set hotkeys, aliases, plugin
  preferences, the appearance and the language, and, on macOS, see which
  permissions golauncher has (*Settings › Permissions*).
- **Themes** (*Settings › Appearance*, [docs/themes.md](docs/themes.md)): 18
  built-in themes (golauncher Light/Dark, Crimson, GitHub, Nord,
  Dracula, Catppuccin, Tokyo Night, Solarized, Gruvbox, One Dark, Paper and
  two high-contrast themes) in a gallery of live thumbnails; separate light
  and dark themes when following the system; a background opacity slider
  that lets the Acrylic or Mica material show through on Windows while text
  stays solid (solid when transparency effects or a contrast theme are on);
  the theme's, the system's or your own accent color; corner radius,
  density and text size. Your own themes are JSON files in
  `%APPDATA%\golauncher\themes` (`~/Library/Application Support/golauncher/themes`
  on macOS) that can `extend` another theme and reload
  as you save them; `glctl theme validate` checks them, contrast included.
- **English and Simplified Chinese UI**: golauncher follows the system's
  display language (Simplified Chinese for zh-CN, zh-Hans and zh-SG, English
  otherwise). To switch, open *Settings › General › Language* and pick
  *System default*, *English* or *简体中文*; the change applies at once.
  English command names and keywords keep working in the Chinese UI.
- **`glctl`**: a headless CLI that drives the same engine, so you or an AI agent
  can test every flow without the GUI.

> **Status:** v0.7 is an early public release, the first for macOS. See the
> [design document](docs/design.md) for the architecture and the roadmap.

## Install

### Windows

Download `golauncher_<version>_windows_<arch>.zip` from
[Releases](https://github.com/eziq1/golauncher/releases), unzip it and run
`golauncher.exe`. On first run it offers to install itself into
`%LOCALAPPDATA%\Programs\golauncher` (no administrator rights needed) with a
Start menu entry and an entry in *Settings › Apps* for uninstalling. Running a
newer or older download later offers to upgrade or downgrade; the same version
just starts the installed copy. Choose *Run without installing* (or pass
`--portable`) to run it from where it is. golauncher lives in the system tray.

**Automatic updates**: the installed copy checks GitHub Releases for a new
stable version (30 seconds after starting, then daily or weekly), downloads
the archive for your architecture, verifies it against the release's SHA-256
checksums and installs it when golauncher is idle (hidden, no background task
or AI answer running, not used for a few minutes); it then restarts and says
what's new. If the new version does not start, the previous one is restored
and the version is skipped. *Settings › About* shows the status and lets you
check now, skip a version, or choose *Only notify me* or *Off*; the tray menu
and the *Check for Updates* command check too. Portable copies and
development builds never update themselves. See
[docs/auto-update.md](docs/auto-update.md).

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
| Logs | `%LOCALAPPDATA%\golauncher\logs\golauncher.log` (updates: `update.log`) |
| Updates (downloads, previous version) | `%LOCALAPPDATA%\golauncher\update\` |

### macOS

Download `golauncher_<version>_darwin_arm64.zip` (Apple silicon) or
`golauncher_<version>_darwin_amd64.zip` (Intel) from
[Releases](https://github.com/eziq1/golauncher/releases) and unzip it. The app
is not notarized yet, so the first time Control-click `golauncher.app` ›
*Open* (on macOS 15: open it once, then *System Settings › Privacy & Security
› Open Anyway*), or run `xattr -dr com.apple.quarantine golauncher.app`. On
first run it offers to install itself into `/Applications` (or
`~/Applications`). It lives in the menu bar, with no Dock icon; press
**⌥Space** to search.

*Settings › Permissions* shows what macOS needs you to allow: **Accessibility**
(pasting, the selected text) and **Automation › Finder** (the current Finder
folder). Because the app is not signed with a Developer ID, macOS forgets the
Accessibility permission after each update: remove golauncher from the list
and add it again. Automatic updates work as on Windows. Requires macOS 13 or
later. See [docs/macos-port.md](docs/macos-port.md).

| What | Where |
|------|-------|
| Configuration | `~/Library/Application Support/golauncher/config.json` |
| Databases, clipboard history | `~/Library/Application Support/golauncher/data/` |
| Logs | `~/Library/Caches/golauncher/logs/golauncher.log` (updates: `update.log`) |

## Keyboard

On a Mac, press ⌥ for Alt and ⌘ for Ctrl (`⌥Space`, `⌘K`, `⌘,`); `⌃N` and `⌃P` also move the selection.

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

Windows:

```powershell
git clone https://github.com/eziq1/golauncher
cd golauncher
pwsh scripts/windows/build.ps1          # builds dist/golauncher.exe and dist/glctl.exe
# or by hand:
go build -ldflags "-H windowsgui" -o golauncher.exe ./cmd/golauncher
go build -o glctl.exe ./cmd/glctl
```

macOS:

```sh
git clone https://github.com/eziq1/golauncher
cd golauncher
scripts/macos/build.sh --app            # builds dist/golauncher.app (this Mac's architecture)
scripts/macos/build.sh --arch arm64 --app --zip   # the release zip for Apple silicon (amd64 for Intel)
scripts/macos/build.sh --debug          # unoptimized dist/golauncher and dist/glctl, for Delve
```

All the scripts are listed in [scripts/README.md](scripts/README.md).

## Test

```powershell
go test ./...                                  # unit, integration, golden and lint tests
go test -race ./internal/... ./plugins/...     # needs a C compiler for -race
go test ./internal/cli -update                 # refresh golden files after an intended output change
```

Tests that touch the real clipboard are skipped unless you opt in with `$env:GOLAUNCHER_CLIPBOARD_TESTS=1` (they restore the clipboard afterwards). Tests that read the selected text of real controls (only in windows the test creates) need `$env:GOLAUNCHER_SELECTION_TESTS=1`.

The end-to-end test drives a running GUI through a local control socket, which is only enabled when golauncher is started with `GOLAUNCHER_CONTROL=1`:

```powershell
$env:GOLAUNCHER_CONTROL=1; .\golauncher.exe --portable
$env:GOLAUNCHER_E2E=1; go test ./internal/shell -run RunningInstance
```

### Debugging the UI

Release builds (`scripts/windows/build.ps1`, GitHub releases) leave MyGo's developer
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
glctl theme validate my-theme.json --format text
glctl attach type vsc           # drive a GUI started with GOLAUNCHER_CONTROL=1
glctl update check --current 0.4.0 --format text   # against a fake GitHub
glctl update check --real       # the real GitHub (read only)
glctl update download --dry-run # download and verify, install nothing
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

## WinGet

When the Windows Package Manager (`winget`, part of App Installer) is
installed, these commands appear:

| Command | What it does |
|---------|--------------|
| Search WinGet Packages | Search the `winget` source by name, ID, moniker, command or tag as you type. `Enter` installs; `Ctrl+D` shows the details (publisher, description, license, homepage, release notes); installed packages offer upgrade, pin/unpin and uninstall (`Ctrl+X`). |
| Installed WinGet Packages | Everything `winget list` reports, including software installed without winget (which can only be uninstalled); packages with a newer version first. |
| WinGet Updates | Packages with a newer version, with *Upgrade All* (pinned packages are skipped). |
| Upgrade All WinGet Packages | `winget upgrade --all`, after a confirmation. |
| WinGet Sources / Update WinGet Sources | The configured sources; `winget source update`. |

Searching reads the local index of the `winget` source (about 15,000
packages, a couple of milliseconds per query), so typing never starts winget.
The installed packages come from `winget list`, which takes seconds: it runs
in the background, the last result is cached, and the views refresh when it is
done. Changes run `winget` as background tasks, one at a time, without prompts
(`--disable-interactivity`, agreements accepted); installers that need
administrator rights show the UAC prompt, and *Install/Upgrade as
Administrator* (`Ctrl+Shift+A`) runs the command in an elevated console for
packages that need winget itself elevated. Type `winget <name>` or `wg <name>`
in the main search to list matching packages. See [docs/winget.md](docs/winget.md).

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

## Acknowledgements

The World Clock's city data comes from [GeoNames](https://www.geonames.org/)
(cities with a population of 15,000 or more, their regions, countries and
Chinese names), licensed under
[CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). It is converted by
[`scripts/data/gencities`](scripts/data/gencities/) and embedded in the program.

## License

[MIT](LICENSE)
