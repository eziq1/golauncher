# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Before 1.0.0, minor versions may contain breaking changes.

## [Unreleased]

### Added

- **Automatic updates** (R54, docs/auto-update.md): the installed copy checks
  GitHub Releases for the latest stable version 30 seconds after starting and
  then daily (or weekly), with ETag caching and GitHub's rate limits
  respected, through the system proxy. A newer version's archive for the
  running architecture is downloaded (resumed and retried), verified against
  the release's `checksums.txt` (SHA-256) and its version resource, and
  installed when golauncher is idle: hidden, no background task or AI answer
  running, not used for 3 minutes. A helper (a copy of the running version)
  installs it with the self-installer, restarts golauncher and waits for the
  new version to report itself healthy; when it does not, the previous
  version is restored and the new one is not installed again. After the
  restart a notification says "Updated to vX.Y.Z" and opens the release
  notes. *Settings › About* shows the status, *Check for Updates*,
  *Restart to Update*, *Skip This Version* and *What's new*, and chooses
  *Download and install* (default), *Only notify me* or *Off*, daily or
  weekly. The tray menu has *Check for Updates…*, and the launcher a
  *Check for Updates* command (`builtin.update`). Portable copies and
  development builds never update themselves; `GOLAUNCHER_NO_UPDATE=1` turns
  updates off. Updates never downgrade and never touch your settings or
  data.
- Optional ed25519 signatures of `checksums.txt` (`checksums.txt.sig`): when
  the repository variable `UPDATE_PUBLIC_KEY` and the secret
  `UPDATE_SIGNING_KEY` are set, the release workflow builds the public key in
  and signs the checksums, and updates then require a valid signature.
- `glctl update check`, `glctl update download --dry-run` (against a fake
  GitHub, or the real one with `--real`), `glctl update sign`, `verify` and
  `keygen`.

### Changed

- **AI web search is on by default** (R56): the AI chat's *Allow web
  search* preference now defaults to on; a preference you turned off stays
  off.
- **Ask AI comes before the web search** (R56): when the main search finds
  nothing good, *Ask AI: …* is listed before *Search Google/Bing for …*.
  SDK: the new optional `FallbackOrderer` (`FallbackOrder() int`, lowest
  first; `FallbackOrderLast` for a catch-all) orders fallback items instead
  of the order plugins are registered in. `Tab` still opens the AI chat.
- **"Raycast-style" themes renamed to Crimson Light/Dark** (绯红, R56), IDs
  `crimson-light` and `crimson-dark`; a configuration or a theme's `extends`
  naming `raycast-light`/`raycast-dark` gets the new theme.
- **golauncher Light/Dark slightly darker so text passes the contrast
  checks** (R56): dark accent `#3b82f6` → `#3472d8` (white on it 3.67 →
  4.62:1, selected row 3.98 → 4.95:1); light accent `#2563eb` → `#2461e6`
  (selected row 4.42 → 4.52:1) and secondary text `#71717a` → `#68686f`
  (3.10:1 over the material at worst). The Windows accent color is darkened
  just enough (or, for light colors such as cyan, gets dark text) so text
  on it and on the selected row stays readable whatever the accent.
- Configuration: new `updates` object (`mode`, `frequency`); older versions
  ignore it. Uninstalling also removes `%LOCALAPPDATA%\golauncher\update`.

## [0.4.0] - 2026-10-16

### Added

- **Quicklinks and web search** (`builtin.quicklinks`, docs/quicklinks.md):
  `g golang generics` searches Google, `bd 天气` Baidu, and `gh`, `so`, `yt`,
  `bili`, `wiki`, `zhihu`, `db`, `tb`, `jd`, `npm`, `go`, `mdn`, `tr`, `yd`,
  `map`, `amap`, `ddg`, `b` the other built-in engines (a keyword is
  followed by a space). The *Search Engines* command turns engines on and
  off, changes their keywords, sets the default and adds your own (URL with
  `{query}`). A typed URL (`github.com/foo`, `https://…`, `localhost:3000`,
  `192.168.1.1`) is offered first to open. When the main search finds
  nothing good, *Search Google for “…”* (Bing in the Chinese UI; a second
  engine can be added) is listed before *Ask AI*. Quicklinks (*Create
  Quicklink*, *Search Quicklinks*) are named links to URLs, files, folders
  and programs with `{query}` (typed inline or after the quicklink's
  keyword), `{clipboard}`, `{date}`, `{time}` and formats such as
  `{date:yyyyMMdd}`; edit, duplicate, delete, open with a chosen program,
  and import or export as JSON (Raycast's quicklinks export too). Every
  web item can be opened in each installed browser. Search suggestions are
  off by default; when on, only text typed after a keyword is sent,
  debounced and cancelled as you type.
- SDK: `Item.Transient` (JSON `transient`) keeps a root item that carries
  the typed text out of Recent, like fallback items.
- **Themes** (R50, R51, docs/themes.md): *Settings › Appearance* (the theme
  option moved there from *General*) has a gallery of 18 built-in themes
  drawn live with launcher components: golauncher Light/Dark (the look so
  far, unchanged), Raycast-style Light/Dark, GitHub Light/Dark, Nord,
  Dracula, Catppuccin Latte/Mocha, Tokyo Night, Solarized Light/Dark,
  Gruvbox Dark, One Dark, Paper (宣纸) and High Contrast Light/Dark.
  Following Windows, a light and a dark theme are chosen separately. The
  background opacity slider lets the Acrylic or Mica material show through
  while text and icons stay solid, with the launcher shown beside the
  settings as you drag; the background turns solid when Windows
  transparency effects are off, a contrast theme is on or the material is
  not available, and the settings say why. Also: material (Acrylic, Mica,
  solid), accent color (the theme's, the Windows accent, or your own),
  corner radius, density, text size, whole-window opacity (70–100%) and a
  faint backdrop under text below 40% opacity. All launcher colors,
  including the plugin icon tints, now come from the theme.
- **Custom themes**: JSON files in `%APPDATA%\golauncher\themes` with
  `extends`, any of `#rgb`, `#rrggbbaa` or `rgba(…)` colors, colors left out
  derived from others, a JSON Schema (`docs/theme.schema.json`), WCAG
  contrast checks (translucent backgrounds at their worst case), and hot
  reload: a file saved broken keeps its last good version and the settings
  list the problem. The settings import a file or pasted JSON, export the
  current theme with your adjustments, and duplicate any theme as an
  editable copy.
- `glctl theme list | show <id> | validate <file> | export <id>`
  (`--themes DIR`, `--real`): `validate` prints errors, warnings and the
  contrast results.
- **Timers and reminders** (`builtin.timer`, docs/timers.md): `timer 25m`,
  `timer 1h30m tea`, `timer 1:30:00`, `计时 半小时`, `倒计时 10分钟` start a
  countdown; `remind 15:30 meeting`, `remind tomorrow 9am standup`,
  `remind in 20m tea`, `提醒 明天 9点 交周报` set a reminder (a time that has
  passed today means tomorrow, and the preview says so). `timer` alone and
  the *Timers* command list the running timers with the time left, ticking
  every second while shown, plus presets (Pomodoro, short and long break)
  and recent countdowns; actions pause, resume, add 1 or 5 minutes,
  restart, rename, copy the end time and cancel (restorable from the
  history). When a timer ends, a Windows notification (or a toast in the
  launcher when it is visible) says so, once. Timers are stored with their
  wall-clock end, so they survive restarts, sleep and clock changes;
  reminders that came due while golauncher was not running are announced
  as missed when it starts. Preferences: prefixes, sound, history size,
  24-hour clock.
- SDK: `Toast.Silent` (JSON `silent`) shows the Windows notification of a
  `Host.Toast` without sound.
- **AI uses your plugins** (docs/ai.md §4, §5, §16): a tool layer with five
  generic tools (`list_plugins`, `search`, `run_command`, `get_detail`,
  `run_action`) that run through the launcher's engine. Items come back with
  short handles that expire after 10 minutes, and only handles can be acted
  on. Actions have a risk level: reading runs directly, opening (apps,
  links, folders, copying) runs directly unless you choose *Ask every time*,
  changes ask every time, destructive actions always ask. The confirmation
  shows in the launcher window (shown if hidden) with who asks, *Allow*,
  *Allow for this session* and *Deny*; it is denied after 60 seconds, and
  requests wait in a queue. Settings › Plugins › AI › AI Tools: which plugins
  AI may use (clipboard history and third-party plugins off by default;
  clips from password managers are never shared), the policy for opening,
  *Allow AI apps to use golauncher* (off by default), *Connect to Claude
  Desktop* (backs up its configuration and adds only the `golauncher`
  entry) and the `claude mcp add` command for Claude Code. *AI Activity*
  lists every tool call (client, tool, a short summary, outcome; no message
  content) of the last 30 days and can be cleared.
  - The AI chat uses the tools (preference *Let AI use launcher plugins*, on
    by default) and shows each step in the answer (`🔧 Search “port 8080”
    → 1 result`, `✅ Install “7-Zip”`, `⛔ Declined: …`); its confirmations
    appear over the chat.
  - `golauncher.exe mcp`: a stdio MCP server for Claude Desktop and Claude
    Code that forwards to the running launcher over a named pipe only your
    user can open, starting the launcher in the background when needed.
  - `glctl mcp`: the same MCP server on glctl's fake environment.
  - Plugin SDK: `Action.ReadOnly` marks a callback that only reads (AI runs
    it without asking); `Item.Sensitive` keeps an item from AI.
- **AI Chat** (`builtin.ai`, off by default): chat with Claude in the
  launcher through your installed Claude Code CLI and your Claude
  subscription. `Tab` after typing opens a chat with the text; *Ask AI:
  <text>* is offered when the main search finds nothing good (can be turned
  off). Streaming answers, follow-ups, `Esc` to stop (a second `Esc`
  closes), copy or paste the answer, *Continue in Claude* / *Continue in
  ChatGPT* with the current question (copied to the clipboard when it is
  too long for a link), `Ctrl+N` for a new chat, and *AI Chat History*
  (open, delete, clear; chats older than 30 days by default are removed).
  Settings › Plugins › AI: the master switch, the model, web search
  (WebSearch/WebFetch only) and how long chats are kept. Claude Code runs
  isolated from your own Claude Code setup (no settings, hooks, plugins,
  CLAUDE.md, memory, MCP servers or local tools), one process per chat
  kept for 10 minutes after the window hides (at most 2), with a fallback
  to one process per answer.
- **Plugin SDK**: `FallbackProvider` (items for searches with poor
  results), `TabProvider` (what `Tab` does in the main search with text),
  full-window `ViewDetail` views whose search field is an input
  (`ActionDo.Query`, `Result.ClearText`), `View.Escape`, and
  `Result.Preferences` (a plugin sets its own preferences, e.g. from a
  first-use prompt). `glctl` text output shows detail views.
- **WinGet**: search, install, upgrade and uninstall packages with the
  Windows Package Manager. Search reads the local index of the `winget`
  source, so it is instant; installed and upgradable packages come from
  `winget list` in the background (cached); details from `winget show`.
  Also *Upgrade All*, pin/unpin, sources and source updates, install or
  upgrade as administrator, and `winget <name>` in the main search.
  Operations run one at a time as background tasks. Disabled when winget is
  not installed.

### Changed

- Configuration: the new `appearance` object replaces `theme`, which is
  migrated (`system` follows Windows, `light` and `dark` become golauncher
  Light and Dark) and still written for older versions. The schema version
  stays 1, so an older golauncher still reads the file (it ignores
  `appearance`).
- The notification and the settings message about a launcher hotkey that
  cannot be registered name the usual culprits (the ChatGPT app and
  PowerToys Run also use Alt+Space).
- **World Clock** knows about 32,000 cities instead of 185: every city with
  15,000 people or more, from [GeoNames](https://www.geonames.org/)
  (CC BY 4.0), with Chinese names (and their pinyin) for about 11,000 of
  them. The 185 hand-picked cities keep their Chinese names, abbreviations
  (`nyc`, `sf`, `hk`) and ranking; other cities rank by population.
  Subtitles now name the country, plus the state or province when several
  cities share a name (*Springfield* in Missouri, Massachusetts, Illinois…),
  in Chinese in the Chinese UI; the detail shows the region. Searches stay
  within a few milliseconds through a prebuilt index. The data adds about
  0.7 MB to the program and is loaded in the background.
- **World Clock** uses much less memory (R41, `docs/world-clock.md` §10):
  the city index takes about 4 MB instead of 18 MB, through a compact
  table of cities, match keys and name index. It is built the first time
  the world clock is used (about 0.1 s, in the background) instead of at
  startup, and released after the launcher has been hidden for 10 minutes
  (not while a World Clock view is open). Search results are unchanged;
  most searches got faster.

### Fixed

- Apps registered under one name only can be found by their name in another
  language (R40, `docs/app-names.md`): typing `微信` (or `weixin`)
  now finds an app listed as *WeChat*. Apps get extra keywords: the shortcut's
  file name when the Start menu shows a localized name, the Chinese (or other
  non-Latin) names in the executable's version information, and the other
  names of well-known apps from a built-in table (`plugins/apps/names.json`).

## [0.3.0] - 2026-10-15

### Added

- **Simplified Chinese UI** (`zh-CN`): the launcher, settings, tray,
  notifications, installer, background tasks and every built-in plugin are
  translated. Chinese Windows (zh-CN, zh-Hans, zh-SG) picks it
  automatically; Traditional Chinese (zh-TW, zh-HK) stays English. Switch in
  *Settings › General › Language*, where each language is named in itself
  (*English*, *简体中文*); the change applies at once. In Chinese, commands
  still match their English titles and keywords, keywords match by pinyin
  and its initials, and the command list is sorted by pinyin.
- Message catalogs accept exact plural forms such as `"=1"`, and
  `glctl i18n check` reports a key missing from `zh-CN.json` as an error.

- **Unit conversion in the calculator** (R28, `docs/unit-conversion.md`):
  numbers can carry units, including compound ones (`km/h`, `kW·h`, `N*m`,
  `m/s^2`), with dimension checking: `10 km in mi`, `5 GB / 100 Mbps`,
  `3 kg * 9.8 m/s^2`, `1 mi + 200 m`, `5 ft 10 in in cm`. Conversion keywords
  `in`, `to`, `as`, `=>`, `换成`, `转换为`. A quantity alone (`30 ℃`, `10 km`,
  `100 usd`) lists up to five conversions; Show All Conversions opens every
  one with a detail pane. About 20 categories: length, area, volume, mass,
  temperature (with `Δ` differences), data sizes (SI and IEC, bits and bytes)
  and rates, speed, duration, pressure, energy, power, angle, frequency, fuel
  economy (`L/100km` ↔ `mpg`), force, torque, density, screen units
  (`px`/`pt`/`em`/`rem`/`dp` with DPI and root font size preferences),
  cooking, and Chinese market units (斤, 两, 亩, 尺…). Units are found by
  symbol, English name, Chinese name or pinyin (`gongli`, `gl`).
- **Currencies**: 166 currencies with rates from open.er-api.com (or
  Frankfurter as a fallback), no API key, refreshed in the background at most
  every 6 hours and cached for offline use. `¥` is the yuan unless the
  "¥ means Japanese yen" preference is on.
- **Timestamps, colors and sizes** in the calculator: `1700000000 to date`,
  `now in unix`, `2026-10-15 10:00 to unix` (local time or `in utc`),
  `#ff8800 to rgb` / `hsl(32,100%,50%) to hex` with a color swatch, and
  clothing and shoe sizes (`US 9 shoe in EU`, `鞋码 42 in us`).
- `glctl --fixtures DIR` reads exchange rates from `DIR/rates.json`.
- **System commands** (R31, `docs/system-commands.md`): Lock, Sleep,
  Hibernate, Restart, Shut Down, Sign Out, Empty Recycle Bin (shows its size),
  Toggle Dark Mode, volume and mute, Turn Off Display and Show Desktop, found
  in English, Chinese or pinyin (`lock`, `锁屏`, `gj`). Restart, Shut Down,
  Sign Out and Empty Recycle Bin ask first, and warn when background tasks
  would be interrupted.
- **Settings shortcuts**: about 160 Windows Settings pages (`ms-settings:`)
  and 20 classic tools (Device Manager, Services, Registry Editor, Event
  Viewer…) open straight from the root search, filtered by Windows version
  and edition; tools can run as administrator.
- **Terminal & WSL** (R31, `docs/terminal-wsl.md`): your Windows Terminal
  profiles (including fragments such as Git Bash) and WSL distributions with
  their running state; open one in a new window or tab, as administrator or
  in the current folder, set the default distribution, stop it, shut down or
  update WSL.
- **Network tools** (R31, `docs/network-tools.md`): `ip` shows adapters,
  gateway, DNS, Wi-Fi and your public IP; `ping <host>` pings live with
  statistics; `dns <domain> [@server]` looks up records; `port 8080` shows
  (and can end) the process on a port and `port host:443` checks whether a
  remote port is reachable; `wifi` copies the password of a saved network;
  `tracert`, Flush DNS Cache and Renew IP Address run in the background.
- SDK: `Action.EndsSession` marks actions that end the Windows session, and
  `Clipboard.WriteSecret` (`secret: true` in `host/clipboard.writeText`)
  copies text that clipboard managers, Windows clipboard history (Win+V) and
  cloud clipboard are asked not to record. Wi-Fi passwords use it.

### Changed

- `plugin.Clipboard` has a new method, `WriteSecret`: code that implements
  the interface (hosts, test fakes) must add it. Plugins that only use the
  clipboard are not affected.

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

- **New application icon** (executables, tray, README).
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

[Unreleased]: https://github.com/eziq1/golauncher/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/eziq1/golauncher/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/eziq1/golauncher/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/eziq1/golauncher/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/eziq1/golauncher/releases/tag/v0.1.0
