# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Before 1.0.0, minor versions may contain breaking changes.

## [Unreleased]

## [0.2.0] - 2026-10-15

### Added

- **External plugins** in any language, in their own process, over JSON-RPC
  2.0 on stdio (see `docs/plugin-protocol.md`). Plugins live in
  `%APPDATA%\golauncher\plugins\<id>\` (plus `GOLAUNCHER_PLUGIN_PATH`). A
  crashing or hung plugin never takes golauncher down: it is restarted with
  backoff and turned off after repeated crashes (Settings › Plugins can turn
  it on again). Go plugins become external with `rpcserve.Serve`; examples
  for Go and Python are in `examples/plugins/`. `glctl --plugins DIR` and
  `glctl plugin validate DIR` support plugin development.
- **Raycast Script Commands**: `.ps1`, `.py`, `.js`, `.bat`/`.cmd` and `.sh`
  scripts with `@raycast.*` metadata in `%APPDATA%\golauncher\scripts` show up
  in the root search, with arguments and the silent, compact and full-output
  modes. "Create Script Command" starts a new one.
- **Forms and command arguments** for plugins: form views with text,
  password, text area, number, checkbox, dropdown, date and file fields, and
  up to three inline arguments next to the search field.
- **Live updates**: plugins can ask the host to refresh what they show
  (`Host.Invalidate`) or push new content (`Host.Render`). Clipboard history,
  the app list and file search now refresh by themselves.
- **Scoop**: search, install, uninstall and update Scoop packages; installed
  and outdated lists, update all, bucket management, hold/unhold and clean-up.
  Reads Scoop's files directly, so search is instant; operations run one at a
  time with live output. Disabled when Scoop is not installed.
- **World Clock**: type `time` (or `tz`) for your local time and major cities;
  `time bj`, `time 北京` or `time utc+8` find a city. The clocks tick every
  second; favorite cities stay on top. 185 cities, time zone data built in.
- **Background tasks**: long operations (Scoop updates, long scripts,
  compressing folders) run in the background. A spinner and count in the
  launcher's footer show running tasks (a red dot for unseen failures);
  `Ctrl+J` or *Background Tasks* lists them with live console output, stop,
  rerun and full logs. A notification tells you when a task ends. Plugins use
  `Host.RunProcess` / `Host.RunTask`; external plugins `host/task.runProcess`.
- **Context awareness**: golauncher notices where you were when you opened it.
  From File Explorer (including the active Windows 11 tab), the desktop, or a
  window whose title shows a path, the launcher offers actions for that
  folder and the selected files: open Terminal, PowerShell, cmd or VS Code
  there, new folder or text file, copy paths, compress to ZIP, SHA-256, run as
  administrator, and search the folder with Everything. Plugins can add their
  own actions (`ContextProvider`). Can be turned off in Settings.
- **Plugin settings**: buttons (e.g. "Sign in"), options loaded at runtime,
  groups and preferences shown only when another one has a certain value.

### Fixed

- With a file selected in Explorer, opening the launcher could leave the list
  scrolled with the context section's header drawn over its first row.
- MyGo's developer tools (inspector) were enabled in release builds: F12,
  Ctrl+Shift+I and Ctrl+Shift+C opened them, and Ctrl+Shift+C ("Copy Path")
  did not reach the launcher. They are now left out of release builds; set
  `GOLAUNCHER_DEVTOOLS=1` to enable them for debugging.

### Changed

- **File search** ranks your own files first and system, cache and program
  files last; new preferences "Hide system and cache files" and extra
  low-priority paths.

## [0.1.0] - 2026-10-15

First public release: a Raycast-style launcher for Windows 10/11, written in Go
with the [MyGo](https://github.com/egoist/mygo) native UI framework (no cgo, no
WebView2).

### Added

- **Launcher panel** summoned with the global hotkey `Alt+Space`: a frameless
  Acrylic panel that searches apps, commands and calculator expressions, with
  an action panel for every result, keyboard navigation, toasts, and command
  hotkeys. Lives in the system tray; single instance; optional start at login.
  `golauncher.exe --show | --settings | --quit` drive the running instance.
- **Ranking**: fuzzy matching, pinyin matching, frecency and aliases. Root
  search p95 is about 1.6 ms with 325 installed apps.
- **Plugin SDK** (`pkg/plugin`): every built-in feature is a plugin. Side
  effects (open, paste, notify, ...) are returned as actions and performed by
  the host, so plugins are testable without a desktop.
- **Built-in plugins**:
  - Applications: finds and launches installed apps.
  - Calculator: evaluates expressions inline, with base conversion
    (`255 in hex`).
  - Clipboard history (`Alt+Shift+V`): searchable history with paste, pin
    and delete actions.
  - File search powered by [Everything](https://www.voidtools.com/), talking
    to an installed Everything over its IPC protocol (no DLL). The plugin is
    disabled, with the reason shown in Settings, when Everything is not
    installed.
- **Settings window** (`Ctrl+,` or tray › Settings…): General, Plugins
  (enablement and per-plugin preferences), Advanced and About pages.
  Password preferences are stored in Windows Credential Manager, never in
  `config.json`. Settings can be exported to and imported from a file.
- **Data locations**: `config.json` in `%APPDATA%\golauncher`; databases and
  clipboard history in `%LOCALAPPDATA%\golauncher\data` so they never roam.
  A corrupt `config.json` is set aside and the defaults are used.
- **Self-installer**: on first run golauncher offers to install itself into
  `%LOCALAPPDATA%\Programs\golauncher` (per user, no UAC) with a Start menu
  shortcut, optional desktop shortcut, launch at login and an uninstall entry
  in *Settings › Apps*. Running another version offers an upgrade or
  downgrade; the same version starts the installed copy. `--portable` runs
  without installing; `--uninstall` removes it.
- **Robustness**: plugins cannot take the launcher down. Panics in plugin
  calls, `Init`/`Close`, event handlers and plugin goroutines (started with
  the new `Host.Go`) are contained and logged; hung plugin calls time out
  (10 s for views, 30 s for actions). Fatal crashes write their traceback to
  `%LOCALAPPDATA%\golauncher\logs\golauncher.log`.
- **Destructive-action confirmation**: commands and actions marked
  destructive ask for confirmation in the GUI and in `glctl` sessions.
- **`glctl`**, a headless CLI for agents and tests: `query`, `run`,
  `session` (multi-step replayable sessions), `plugin`, `config`, `i18n`,
  `attach` (drives a GUI started with `GOLAUNCHER_CONTROL=1`), `bench` and
  `version`. Runs in an isolated fake environment by default (fixtures, fixed
  clock, recorded side effects); `--real` and `--exec` opt into the real
  environment. JSON output by default, covered by golden tests.
- **Internationalization pipeline**: message catalogs for every UI string,
  the `en` locale and the `en-XA` pseudo-locale for layout testing, and
  `glctl i18n check` to verify catalogs and key references. Tests enforce that
  plugins use only the public SDK and that no UI string is hard-coded.
- Application icon, Windows manifest (per-monitor DPI v2, UTF-8 code page)
  and version information embedded in both executables.
- CI on Windows (gofmt, vet, tests, race tests, i18n check) and a release
  workflow publishing `windows/amd64` and `windows/arm64` zip archives with
  SHA-256 checksums.

### Known limitations

- External (out-of-process) plugins over JSON-RPC are not implemented yet.
- The calculator has no `ans`, unit or currency conversion yet.
- `Tab` does not yet move into command arguments.
- Hotkey display names are not localized.
- The UI ships in English only (plus the `en-XA` pseudo-locale).
- No auto-update yet: download a new release and run it to upgrade.
- The executables are not code-signed, so SmartScreen may warn on first run.

[Unreleased]: https://github.com/eziq1/golauncher/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/eziq1/golauncher/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/eziq1/golauncher/releases/tag/v0.1.0
