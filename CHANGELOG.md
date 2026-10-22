# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Before 1.0.0, minor versions may contain breaking changes.

## [Unreleased]

### Added

- **Action Ring** (R116, docs/action-ring.md): hold the middle mouse button
  (300 ms by default) to open a ring of up to eight actions around the
  pointer, without taking the focus from the app you are in. Four fixed
  actions (by default Open golauncher, Ask AI — about the selected text if
  there is some —, Screenshot and Lock) and actions for the selected text and
  for the folder under the pointer (open a terminal or VS Code there, copy
  the path…), or recently used items. Drag toward an action and let go, or
  let go first and then click one or press `1`–`8`; `Esc`, the × or a click
  outside closes it. A short click of the middle button still goes to the
  app, and so does dragging with it (autoscroll, panning). Remote desktops,
  virtual machines, Blender and full-screen games are left alone. On by
  default; *Settings › Action Ring* sets the hold time, the fixed actions,
  the context actions and the exceptions. On macOS it needs the
  Accessibility permission (the page says so; nothing is asked at startup).

### Changed

- **Action Ring** (R117): every action now shows its name in a pill outside
  the ring, all the time, not only when pointed at (as the Logitech Options+
  Actions Ring does); the action pointed at or dragged to is highlighted
  with its pill, and a click on a pill runs it. Long names end with "…" and
  show whole in a tooltip. *Open Command Prompt Here* (and the
  administrator Command Prompt of System Tools) gets its own icon instead of
  the terminal's.

### Fixed

- **Action Ring on Windows** (R117): the ring no longer sits on a black box
  with rounded corners and a border: its window is now a transparent
  flyout, so that the desktop shows around the ring.

## [0.11.0] - 2026-10-20

### Added

- **LaTeX math in Markdown** (R114, docs/math.md): AI answers and plugin
  details now show display math (`$$…$$`, `\[…\]`) laid out — fractions,
  scripts, roots, stretchy brackets, sums and integrals with limits,
  accents, matrices and cases — in the theme's font (Chinese in `\text{}`
  works) and colors. Math within text (`$…$`, `\(…\)`) shows as readable
  Unicode text (`(期末值 / 期初值)^(1/n) − 1`), and so does any formula with
  a command the layout does not support. Copying still gives the TeX. The
  TeX is parsed by the new dependency
  [treeblood](https://github.com/wyatt915/treeblood) (MIT, pure Go).

## [0.10.1] - 2026-10-19

### Fixed

- **AI backends installed under Node.js version managers** (R111,
  docs/ai.md §22.7): Claude Code, Codex and Gemini CLI installed with npm
  under nvm, fnm, Volta, asdf, mise, nodenv or n (and nvm-windows, fnm,
  Volta and mise on Windows), or into npm's own prefix (`~/.npmrc`
  `prefix=`), are now found, also when the folder is not on golauncher's
  PATH (on macOS, nvm is usually set up only in `~/.zshrc`, which the login
  shell golauncher asks for its PATH does not read). The one on PATH is
  still preferred; otherwise nvm's default version comes first, then the
  highest version. Such a CLI now runs with its folder first on its PATH,
  so its `#!/usr/bin/env node` (or the npm `.cmd` shim) finds the node next
  to it instead of failing with "env: node: No such file or directory"; a
  program chosen by its path in *Settings › Plugins › AI › Backends* gets
  the same. The Backends section says which manager an installation was
  found through ("found via nvm"). On macOS, Claude Code is also looked for
  in `~/.local/bin`, `~/.claude/local`, `/opt/homebrew/bin` and
  `/usr/local/bin`.

## [0.10.0] - 2026-10-19

### Added

- **Codex model list and reasoning effort** (R109, docs/ai.md §25): the AI
  plugin's *Model (Codex)* now lists the models your Codex CLI offers for
  your account (`codex debug models`; offline, the models built into the
  CLI), instead of only *Default*. New *Reasoning effort (Codex)* and
  *Reasoning effort (Claude Code)* choices sit next to the model choices:
  Codex gets `-c model_reasoning_effort=…` with the levels the chosen model
  supports, Claude Code gets `--effort` with the levels its version lists.
  A level the model or version does not take is not passed (the setting
  says so), so a chat never fails because of it; a chosen model that is no
  longer listed stays selected and is marked. *Re-detect* in the Backends
  section lists the models again.

- **Custom AI actions** (R103 phase 1, docs/ai-actions.md): *Settings ›
  Plugins › AI › AI Actions* lists the actions on the selected text and on
  files handed to the main search. Built-in actions (Translate, Explain,
  Summarize, Rewrite, Suggest Replies) can be renamed, given another prompt,
  turned off and reordered, and *Restore Defaults* (after a confirmation)
  puts them back; your own actions can be added, edited and deleted.
  - An action has a name, a prompt, what it applies to (selected text,
    files or both), its result, keywords, an icon and an optional minimum
    length. Prompts may use `{selection}`, `{lang}` (the Translate target
    language), `{input}` (an argument typed for the action), `{app}` and
    `{date}`; without `{selection}` the text follows as a quote, as before.
    Files are sent as attachments.
  - *Shows the result*: a view with only the answer as it is written;
    Enter replaces the selected text (or pastes the answer), Ctrl+C copies
    it, Tab turns it into a chat to follow up, Esc goes back. It is saved in
    AI Chat History like any chat.
  - Stored in `config.json` under the AI plugin's `actions` preference, as
    what differs from the built-in actions, so new built-in actions show up.
    The built-in actions send the same prompts as before. Not shown to AI
    tools (MCP); the log records counts only.
- `glctl --pref <plugin>:<name>=<JSON>` sets a plugin preference in the fake
  environment.

### Changed

- **Homebrew tap updates itself** (R108, issue #4): the release workflow now
  pushes each release's cask to `eziq1/homebrew-tap` with a deploy key that
  can write to the tap only (secret `HOMEBREW_TAP_DEPLOY_KEY`;
  `HOMEBREW_TAP_TOKEN` still works).
- **README**: demo GIFs at the top (R105).

## [0.9.0] - 2026-10-17

### Added

- **Translator** (R99, issue #3, `builtin.translator`, docs/translator.md):
  `tr hello`, `fy …` or `翻译 …` translates as you type (after a 300 ms
  pause; a new text cancels the last request; answers are cached in
  memory), one row per engine with the detected source language.
  - Languages: `tr ja 你好`, `tr >de …`, `tr 日 …`, `tr en>zh …` (codes and
    Chinese names); by default the interface language, or English for text
    already in it.
  - Engines without a key: Google Translate's web endpoint (unofficial);
    when every engine fails, MyMemory is asked instead (an optional email
    address raises its daily quota). With your own keys, asked first: DeepL
    API Free/Pro, Azure AI Translator (key and region), Youdao AI Cloud (app
    ID and secret) and Baidu Translate (APP ID and secret). Keys are stored
    in the Credential Manager or Keychain; secrets are only used to sign.
  - *Custom engines*: one HTTP template per line (`name | GET/POST | URL |
    JSON path [| body [| headers]]` with `{text}`, `{from}`, `{to}`). A
    local model through Ollama's OpenAI-compatible endpoint (model chosen
    from the server's list; offline).
  - A single word gets a *Dictionary* row (phonetics, parts of speech,
    meanings) from Google or Youdao.
  - Actions: copy (Enter), paste into the previous window (Ctrl+Enter),
    swap the languages, copy the original, open in Google Translate, DeepL
    or Youdao, *Read Translation Aloud* / *Read Original Aloud* (the
    system's voices: SAPI on Windows, `say` on macOS; only when picked),
    *Translate with AI* (opens an AI chat with the text). A *Translate*
    command opens the same as a view.
  - Off by default: an instant translation of selected text (a row in the
    *Selected Text* section, with *Replace Selected Text*), and a
    *Translation History* of the translations you copy or paste (kept
    locally for 1–90 days, never synced; *Clear History* in the settings,
    and turning it off deletes it).
  - Privacy: text is sent only after a prefix; rows are never recorded in
    Recent; logs have lengths and timings only.
- **Files in the main search** (R97, docs/root-files.md): drop files onto
  the main search, paste copied files or an image with `Ctrl+V`, or choose
  them with `Ctrl+O` or *Choose Files…*. They show as chips above the
  footer, and a **Files** section at the top lists what you can do with
  them: *Ask AI about…* (a new chat with them attached; with text typed,
  *Ask AI: <text>* sends it with them at once), open, *Open With…*
  (Windows), show in File Explorer or Finder, copy the paths or names,
  compress to ZIP, copy the SHA-256 checksum, copy a PNG image. Typing
  filters the actions; a chip's × drops a file, `Escape` drops them all.
  They are read only when an action needs them, are never recorded or shown
  to AI, and go when the launcher hides; a pasted image is kept as a
  temporary file in the cache for a minute after.
- **Attachments in the AI chat** (R91, docs/ai.md §24): attach images and
  text files to a message by dropping files onto a chat, by pasting with
  `Ctrl+V` (a screenshot or copied files; other clipboard content pastes as
  text) or with *Add Attachment…* (`Ctrl+O`). They show as chips under the
  chat, with thumbnails, and in the transcript and the history (📎 count)
  once sent. Images (PNG, JPEG, GIF, WebP; BMP and TIFF are converted to
  PNG) are scaled down to at most 2000 px and 3.75 MB; text files are
  detected by content (UTF-8, UTF-16 with BOM) up to 200 KB and sent inline
  with their names; at most 10 attachments and 16 MB per message. Claude
  Code gets them as stream-json content blocks, Codex images as `--image`
  (also when resuming), Gemini CLI as ACP image blocks. Files that cannot be
  sent (PDF and Office documents for now, video, audio, archives, or a kind
  the chat's backend does not take) are shown in red with the reason and
  keep the message from being sent. Copies live in the chat's folder in
  golauncher's data and are deleted with the chat (30-day retention); logs
  only have counts, kinds and sizes.
- **Homebrew cask** (R98, docs/homebrew-packaging.md):
  `brew install --cask eziq1/tap/golauncher` from the tap
  [eziq1/homebrew-tap](https://github.com/eziq1/homebrew-tap) (live, with the
  0.8.0 cask) installs golauncher.app from the release's macOS zip for Apple
  silicon or Intel and links `glctl`. The app is ad hoc signed, not
  notarized: the first start needs *System Settings › Privacy & Security ›
  Open Anyway*, maybe again after an upgrade. A copy installed with Homebrew
  does not offer to install itself or update itself; *Settings › About* says
  it is managed by Homebrew, and a check for updates tells you to run
  `brew upgrade --cask golauncher`. The cask is written by
  `scripts/release/brewcask` from the release zips and `checksums.txt`; the
  release workflow builds it as an artifact and checks it with `brew style`
  and `brew audit`, and pushes it to the tap only when the
  `HOMEBREW_TAP_TOKEN` secret is set.
- **winget packaging** (R92, issue #2, docs/winget-packaging.md): manifests
  for the package `eziq1.golauncher` (the release zip as a portable package,
  with the `golauncher` and `glctl` commands, x64 and arm64), generated by
  `scripts/release/wingetmanifest` from the release zips and
  `checksums.txt`; the 0.8.0 manifests are in `packaging/winget/`. The
  release workflow builds them as an artifact; nothing is submitted
  automatically. The 0.8.0 package is under review in
  [microsoft/winget-pkgs#454732](https://github.com/microsoft/winget-pkgs/pull/454732);
  once accepted, `winget install eziq1.golauncher`. A copy installed with
  winget does not offer to install itself and does not update itself:
  *Settings › About* says it is managed by winget, and a check for updates
  tells you to run `winget upgrade eziq1.golauncher`.
- **Plugin SDK** (docs/plugin-guide.md):
  - `ActionAskAI` (`plugin.AskAI`, `plugin.AIAsker`): any plugin's action
    can hand text with an instruction to the AI chat; with AI off the chat
    asks to turn it on first ("Asking AI").
  - Files in views: `plugin.FileReceiver`, `View.AcceptFiles`, `View.Chips`,
    `View.Draft` and `ActionChooseFiles` ("Files").
  - `plugin.FileActionsProvider` and `PermFiles`:
    `FileItems(ctx, FilesQuery{Files []FileInfo, Source, Text})` offers
    items for the files handed to the main search, as `SelectionProvider`
    does for the selected text; `FileReceiver` is no longer called for the
    main search ("Files in the root search").

### Changed

- Dropping files onto the main search no longer opens an AI chat at once:
  *Ask AI about…* is the first action of the new **Files** section (R97).
- Web search keywords (R99): Google Translate's web search is now `gt`
  (and still `translate`); `tr` and `翻译` (formerly Youdao's) belong to the
  translator, and `fy` too. Youdao's web search keeps `yd` and `有道`. A
  keyword you set yourself is kept.
- Dependencies (R100, docs/deps-2026-10.md): MyGo v0.5.1 → v0.5.5 and
  `golang.org/x/image` v0.46.0 → v0.47.0; no indirect version moved. MyGo
  brings double clicks counted by the system's double-click settings,
  Escape typed ahead into a just-opened view going to that view, the
  Windows clipboard waiting while another program holds it, and renderer
  fixes. No code change was needed.
- Internal: the background refresh scheduling of the Homebrew, WinGet and
  Scoop plugins and the whole-second refresh of the World Clock and Timers
  share small helpers in `pkg/plugin/bg`; the Files plugin quotes command
  lines with `internal/winapi/cmdline` (code review 5). No visible change.
- Internal: `internal/core/controller.go`, `internal/ui/settings.go` and
  `internal/ui/launcher.go` are split by responsibility into files of the
  same packages (code review 4); the code was moved unchanged.

### Fixed

- **Pasting an image into the AI chat works whatever it was copied from**
  (R96, docs/ai.md §24.8): `Ctrl+V` only attached an image when nothing but
  the image was on the clipboard, so a screenshot attached but an image
  copied from a browser or a chat app — which put its address or a
  "[图片]" placeholder next to it — was pasted as that text instead. Now
  the image is attached when the text next to it only describes it (blank,
  a single URL without a line break, or bracketed image placeholders); real
  text (a word, a number, several lines, a spreadsheet cell) is still
  pasted as text. On macOS `⌘V` and *Edit › Paste* never reached the
  launcher's check (they paste through the Edit menu) and never attached
  anything; they do now. With *Fall back to copying* on (selected text),
  the user's copy put back afterwards is no longer refused as "not to be
  recorded".
- **A plugin turned off no longer runs in the background** (R88, code review
  2.1): turning off the clipboard history in Settings › Plugins used to hide it
  from the search while copies were still recorded; every disabled plugin
  (timers, package managers, …) kept running. Now a plugin turned off is not
  started at all, one turned off while the launcher runs is closed (its open
  views close, nothing reaches it, AI cannot use it, an external plugin's
  process stops), and turning it on again starts it again. Timers that ended
  meanwhile are announced when it starts.
- **Settings changed at the same moment no longer end in the older state**
  (code review 2.2): listeners were told about two concurrent changes in any
  order, so the engine or the appearance could keep the older one; they are
  now told in the order the changes were saved.
- **A plugin whose start fails is cleaned up** (code review 2.3): it is
  closed and its event subscriptions removed; when the launcher fails to
  start, the plugins it started are closed again, and plugins are closed in
  the reverse order of their start.
- **A slow scripts folder no longer holds up the start** (R88, code review
  3.2): the Script Commands plugin scans its folders in the background
  instead of during startup, so a slow or disconnected network folder cannot
  delay the launcher. A search made before the scan ends is shown again
  when the scripts are known.

## [0.8.0] - 2026-10-17

### Added

- **AI backends in the settings** (R84, docs/ai.md §22): *Settings › Plugins
  › AI › Backends* lists Claude Code, Codex (ChatGPT) and Gemini CLI with
  their status (available, not installed, not signed in, version too old),
  the program in use (path, version, how it was found: PATH, a usual install
  folder, the desktop app, inside an npm installation) and why it was chosen,
  and the other installations found (marked too old, launcher script not
  used, found later). *Program* chooses one of them or a custom path, which is
  checked first (it must exist and print an acceptable version); *Re-detect*
  looks again. A chosen program that disappears falls back to the automatic
  choice, and the chat says so. Errors that may come from the wrong
  installation (too old, client no longer supported) name the program that
  ran. glctl ai backends [--real] prints the same as JSON.
- **AI chat works with older Claude Code versions** (docs/ai.md §23): the
  backend reads `claude --help` once per installed version (cached) and runs
  the CLI with the flags it has. Without `--permission-prompts`, `--safe-mode`
  and other newer flags it uses an older equivalent with the same isolation;
  a feature that has none (web search, launcher plugins, streaming, model
  choice) is turned off, never the isolation. The chat's footer names what an
  older version runs without; *Settings › Plugins › AI › Backends* shows the
  version in use.

### Changed

- golauncher describes itself as a launcher for Windows and macOS: the
  About text, and on a Mac the installer window (no Start menu, tray or
  `C:\` wording). The AI chat is told which system it runs on, and the
  instructions golauncher's MCP server gives AI apps name both systems and
  Homebrew.

### Fixed

- The AI chat failed with `error: unknown option --permission-prompts` on
  Claude Code versions older than 2.1.259. A version that lacks a flag
  golauncher cannot do without is now reported as too old, with the version
  found and how to update it (`claude update`; on macOS also Homebrew and
  npm).

## [0.7.0] - 2026-10-16

### Added

- **macOS support** (R46, docs/macos-port.md): golauncher now runs on macOS 13
  or later, Apple silicon and Intel. Releases have
  `golauncher_<version>_darwin_arm64.zip` and `…_darwin_amd64.zip` with an
  ad hoc signed `golauncher.app` (not notarized: Control-click › Open the
  first time). It lives in the menu bar, without a Dock icon, and opens with
  ⌥Space. ⌘ is the main modifier: shortcuts written `Ctrl+…` mean ⌘ and are
  shown as ⌃⌥⇧⌘ symbols. On a Mac:
  - **Applications** from `/Applications` and the system folders, with
    localized names (`微信` and `WeChat` both find WeChat) and their icons.
  - **Clipboard History**: text, images and files; copies that password
    managers mark as concealed are skipped. Pasting needs Accessibility;
    without it the item is copied and a notification says to press ⌘V.
  - **Context awareness**: the current Finder folder and the selected files
    (Automation › Finder), or the desktop. **Selected text** is read with
    the Accessibility API, never from password fields; copying with ⌘C is the
    same opt-in fallback as on Windows.
  - **File Search** with Spotlight (`mdfind`) instead of Everything.
  - **Terminal**: Terminal, iTerm2, Ghostty, WezTerm, Alacritty, kitty and
    Warp, with a default terminal setting; new window or tab, open a folder,
    run a command. The files actions open the folder in that terminal, in VS
    Code, Cursor or Zed, show it in Finder, or Quick Look the selection.
  - **Homebrew**: a new plugin (`builtin.homebrew`, docs/homebrew.md) to
    search formulae and casks, see details, install, upgrade, uninstall and
    upgrade all as background tasks, and list installed and outdated
    packages.
  - **Processes** and **Network** tools: the process list with CPU and
    memory, quit and force quit (as administrator through the macOS
    password prompt); interfaces, Wi-Fi, ping, listening ports, trace route
    and flushing the DNS cache.
  - **System**: lock, sleep, log out, restart, shut down, empty and measure
    the Trash, dark mode, volume, display off, and 45 System Settings panes.
  - Passwords in the keychain; the system language from the macOS
    preferences; the login shell's PATH when started from Finder.
  - **AI chat** with Codex and Gemini CLI too: Codex runs as the native
    `codex` (on PATH, from Homebrew, or the binary inside an npm install of
    `@openai/codex`; still never through Node.js), Gemini CLI as the
    `gemini` that npm or Homebrew installs (an administrator's settings in
    `/Library/Application Support/GeminiCli` are kept under golauncher's).
  - **Settings › Permissions** shows the status of Accessibility,
    Automation › Finder and (optional) Full Disk Access, with buttons that
    open System Settings.
  - Installing into `/Applications`, launch at login, and automatic updates
    of the whole app bundle, verified like the Windows ones.
- **Plugin platforms**: a manifest may list the platforms a plugin runs on
  (`"platforms": ["windows"]`, GOOS names); elsewhere it is not loaded and
  its process never starts. `glctl plugin validate` rejects unknown names.
  Scoop and WinGet are Windows-only, Homebrew macOS-only.
- **Views kept across a hide** (R75): reopening the launcher after leaving
  an AI chat (copy the answer, paste it elsewhere) returns to the chat, with
  the whole navigation stack and the text you had typed, instead of the main
  screen. A footer hint (*Esc Back to main screen*, also clickable) shows on
  the restored view. *Settings › General › Return to the main screen*:
  immediately, after 10 minutes (default) or never. The main screen comes
  back anyway when text was selected for the new show (unless the kept view
  holds an unsent draft, R76) or the selected-text hotkey was used, when a command hotkey or a task notification opened the
  launcher, when the chat was deleted, AI was turned off or the plugin was
  disabled. Plugins opt in with `View.KeepOnHide` and may refuse a restore
  (`ViewRequest.Restore`); `glctl session` has `hide` and `reopen` steps.

- **AI chat with Codex and Gemini CLI** (R72): Settings › Plugins › AI ›
  *Backend* offers *Codex (ChatGPT)* and *Gemini CLI* next to Claude Code,
  each with its own *Model* setting. Codex runs as the native `codex.exe`
  (on PATH, the one the Codex app installs, or the binary inside an npm
  install; never through Node.js), one process per answer with
  `codex exec --json` and `resume`, without your `config.toml`, rules,
  shell or other tools, in a read-only sandbox. Gemini CLI runs over the
  Agent Client Protocol (`gemini --acp`; Gemini CLI itself needs Node.js)
  with golauncher's own system settings: no local file or shell tools, no
  other MCP servers, extensions, hooks, skills or GEMINI.md memory. Both
  get golauncher's MCP tools. When Gemini asks for permission to use a
  tool, the launcher's confirmation dialog asks you (*Allow*, *Allow for
  this session* when offered and not destructive, *Deny*; no answer in 60
  seconds or stopping the answer denies), and AI Activity records it.
  Each chat stays on the backend it was started with; the history shows
  which; a chat whose backend is gone says so; deleting a chat (or pruning
  it) also forgets the CLI's session ID. When a CLI cannot continue an
  earlier conversation, the chat says that the answer starts a new one.
- **Background tasks as administrator** (R68): the SDK's
  `ProcessSpec.Elevate` (JSON `elevate`, also for external plugins) runs a
  task's program as administrator through a UAC prompt, while golauncher
  stays unelevated. Only the exit code is known (the task log says the
  output is not available); declining the prompt marks the task cancelled.
  When golauncher already runs as administrator the program runs as usual.
- **Retry as Administrator**: a background task that fails because it
  needs administrator rights (error 740, or the "requires elevation"
  message of ipconfig and netsh, or sc.exe's "FAILED 5:", in English or
  Chinese) offers *Retry as Administrator* in Background Tasks (Enter), and
  its failure notification says so.

### Changed

- **Scripts** are grouped by platform: `scripts/windows/build.ps1`,
  `scripts/macos/build.sh`, `scripts/release/changelog.ps1`,
  `scripts/data/gencities` (scripts/README.md).

- **AI chat: Backend setting** (R71): Settings › Plugins › AI has a
  *Backend* choice (Claude Code, the default; Codex and Gemini CLI since
  R72). A configuration naming a backend this version does not have
  explains that in the chat. Since R72 changing it affects new chats only.
- **AI chat internals** (R71, docs/ai.md §20): the Claude Code backend is
  now the first adapter of a shared agent runtime (`internal/ai/agentrt`:
  processes, fallback to one process per turn, idle release, process limit,
  working directory, golauncher's MCP tool server, version gate, error
  kinds), so that other agent CLIs (ACP agents such as Gemini CLI, Codex
  CLI) can be added later. Nothing changes in how the chat works; the
  command lines Claude Code is started with are pinned byte for byte by a
  test.

### Fixed

- **World clock**: the next time change no longer shows a change on January
  1 for zones that have none ("Next change: Jan 1 → UTC+11").
- **Flush DNS Cache** and **Renew IP Address** failed with "The requested
  operation requires elevation." They now ask for administrator rights with
  a UAC prompt (the toast says so) unless golauncher already has them.
- The search bar no longer grows by a few pixels when the selected command
  takes arguments (R74): moving the selection between commands with and
  without arguments made the list under it jump. The bar now has a fixed
  height, and the argument inputs and drop-downs fit in it, vertically
  centered; drop-downs use the inputs' 14-DIP font.

## [0.6.0] - 2026-10-16

### Added

- **Selected text** (R64, R65, docs/selected-text.md): select text in any app
  and open the launcher; a "Selected Text" section at the top of the root
  offers a preview (Enter shows the full text with its word, character and
  line counts), AI actions (translate — into the UI language by default, or
  the language chosen in the AI settings —, ask AI with an optional
  instruction, explain, summarize, rewrite, suggest replies, and "AI handles
  the selection: <what you typed>"), a web search with the default engine,
  web translation, "open link" when the text is a URL, and the calculator's
  result (with "paste to replace" in editable fields) when it is an
  expression. It follows the Context awareness switch. The text is read
  with UI Automation (the focused element before the launcher takes the
  focus, the text in the background; the list updates when it arrives,
  keeping the first row selected); password fields, excluded apps (password
  managers by default) and golauncher itself are never read. An opt-in
  setting "Fall back to copying when the text can't be read" copies the
  selection with Ctrl+C when UI Automation cannot read the app (never in
  terminals, Excel, line-copying editors, password fields or elevated
  apps), saving and restoring every clipboard format (up to 16 MB) and
  hiding the temporary content from golauncher's clipboard history; Windows
  clipboard history, the cloud clipboard and other clipboard managers may
  still record the app's copy, as the setting says. Also a hotkey "Open for
  the selected text", a list of apps that always use copying, and the
  maximum length (10,000 characters by default; longer text is truncated
  and marked). The text is kept in memory for one show, never stored or
  logged (only lengths and timings), hidden from AI tools, and sent to AI
  only when you pick an AI action, shown in full in the message.
- SDK: `plugin.SelectionProvider`, `plugin.Selection`, `plugin.SelectionQuery`
  and the `selection` permission (`plugin.PermSelection`), for built-in
  plugins for now. New built-in plugin `builtin.selection`.
- glctl: `--selection`, `--selection-app`, `--selection-source`,
  `--selection-truncated`, `--selection-editable` inject a selection.
### Fixed

- The settings window (and the installer's) showed Windows' generic icon in its title bar and taskbar button instead of golauncher's (R62): the program's icon resource is now ID 1, the one the windows load.

## [0.5.0] - 2026-10-16

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

- **Process manager** (`builtin.processes`, R52/R55, docs/process-manager.md):
  `ps ` (with a space: `ps` alone still finds PowerShell), `kill` and `进程`
  in the main search, and the *Processes* command, list the running apps
  and processes grouped by app as in Task Manager (Apps, Background
  processes, Windows processes), with CPU and memory bars and values
  refreshed every second while the list shows (nothing is sampled
  otherwise). Search by name, executable, window title, pinyin or PID.
  Enter switches to the app's window (a process without windows opens its
  processes and details); *End Task* (Ctrl+X) asks an app to close without
  confirmation and offers *Force End* when it is still running; *Force End*
  (Ctrl+Shift+X) and *End Process Tree* (Ctrl+Alt+X) ask first, with a
  warning naming what stops. Each process is identified by its PID and
  creation time, checked on the handle that ends it, so a reused PID is
  never ended. Critical system processes and golauncher itself cannot be
  ended; Windows Explorer is restarted instead. A process that needs
  administrator rights is ended through a one-off UAC prompt (*End as
  Administrator*, Ctrl+Shift+A, running a hidden `golauncher kill` helper);
  the launcher itself never runs elevated. Details list memory (private
  working set, working set, commit, peak), I/O, parent, path, user and
  elevation, start time, handles, threads, windows, listening ports and the
  command line (secrets such as `--token=…` hidden). *Quit <app>* acts on
  the app that was in front, and *Force Quit <app> (Not Responding)* tops
  the empty search when that app hangs. Commands *End Apps Not Responding*
  and *Restart Windows Explorer*. Window titles and command lines are never
  given to AI.
- SDK: `Item.Accessories` (values with an optional bar at the right edge),
  `Item.SubtitleSensitive` and `Metadata.Sensitive` (shown, never given to
  AI), `Action.ConfirmMessage` (the text of the confirmation, also shown
  when an AI asks) and `Query.Raw` (the root search text untrimmed, for a
  prefix that needs a space). They travel in the external plugin protocol
  too (`raw` in `rootItems`).

### Changed

- **AI asks every time before ending a task** (R60): the process manager's *End Task* no longer offers *Allow for this session* when an AI asks. SDK: `Action.AskAlways` (JSON `askAlways`) makes an AI ask every time without changing anything for the user's own keys and clicks.
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
- Network tools: the port owners' *End Process* uses the process manager's
  code: it checks the process's creation time instead of its name, and
  refuses protected processes with a message.

### Fixed

- **AI Chat no longer leaves processes running** (R58, R59): a chat's `claude.exe` (and the `golauncher mcp` bridge it starts) stops 3 minutes after its last answer, even while the launcher is in use; the next message resumes the session. Before, they stopped only 10 minutes after the window hid, which rarely happened.

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

[Unreleased]: https://github.com/eziq1/golauncher/compare/v0.11.0...HEAD
[0.11.0]: https://github.com/eziq1/golauncher/compare/v0.10.1...v0.11.0
[0.10.1]: https://github.com/eziq1/golauncher/compare/v0.10.0...v0.10.1
[0.10.0]: https://github.com/eziq1/golauncher/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/eziq1/golauncher/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/eziq1/golauncher/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/eziq1/golauncher/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/eziq1/golauncher/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/eziq1/golauncher/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/eziq1/golauncher/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/eziq1/golauncher/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/eziq1/golauncher/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/eziq1/golauncher/releases/tag/v0.1.0
