# macOS 移植调研

> 状态：调研 v1 · 2026-10-15 · 对应需求 R46（见 [requirements.md](requirements.md)）；第一期的实现进展见 §10（2026-10-16，`macos-support` 分支）
> 范围：§0–§9 只调研，不改代码。结论基于本仓库源码、`GOOS=darwin` 交叉编译结果和 MyGo v0.5.1 的源码与文档。没有在 Mac 上实际运行过，标"待实测"的地方需要真机验证。

## 0. 结论

| 问题 | 结论 |
|------|------|
| 能不能做 | 能。架构本来就把平台相关代码收在 `internal/winapi/*`、`internal/shell` 和各插件的 backend 接口后面，`core`、`pkg/plugin`、`store`、`i18n`、`config`、`extproc`、`tasks`、`glctl` 基本是可移植的 Go |
| 现在离"能编译"有多远 | **很近**：`GOOS=darwin` 下只有 2 个包、4 处编译错误（§2）。但编过之后几乎所有系统功能都是 `errors.ErrUnsupported` 的桩 |
| MyGo | macOS 是它的一等平台（purego 驱动 AppKit，无 cgo，Metal 渲染，自带 macOS CI）。golauncher 用到的 API 都有实现，还额外提供应用列表/图标、钥匙串、`.app`/DMG/签名/公证打包（§3）。要自己补的只有：剪贴板变化轮询、前台应用、发送 ⌘V、权限检测 |
| 工作量 | 全部做完约 **50–80 人日**；最小可用版（核心 + 应用/计算器/剪贴板/时钟/文件搜索）约 **30–40 人日**（§6.5） |
| 建议 | 保持一个代码库；新增 `internal/darwin/*`（或把 `internal/winapi` 改名为 `internal/platform` 下的分平台实现）；分三期：核心可用 → 插件补齐 → 签名分发（§7） |
| 前提 | 需要一台 Mac（Apple Silicon）做开发和测试；正式分发需要 Apple Developer Program（99 美元/年）做签名和公证 |

## 1. 平台相关代码盘点

### 1.1 已有的分平台文件

所有 `*_windows.go` 都有对应的 `*_other.go`（`//go:build !windows`），但 `_other` 一律返回 `errors.ErrUnsupported` 或空结果，没有任何 darwin 实现。

| 包 | Windows 实现（行数） | 用到的 Windows 机制 | `_other` 现状 |
|----|---------------------|---------------------|---------------|
| `internal/winapi/clip` | `clip_windows.go`（666）+ `dib.go` | `OpenClipboard`、PNG/DIB/`CF_HDROP`、`AddClipboardFormatListener` 消息窗口、`GetClipboardOwner`、排除格式、`SendInput` 粘贴、`GetForegroundWindow` | 全部 `ErrUnsupported` |
| `internal/winapi/context` | `detect_windows.go`（422） | `IShellWindows`/`IFolderView` COM、窗口类名、窗口标题 | `Detect` 恒为 false |
| `internal/winapi/cred` | `cred_windows.go`（108） | 凭据管理器 `CredWrite/CredRead` | `ErrUnsupported` |
| `internal/winapi/everything` | `everything_windows.go`（399）+ `folders_windows.go` | Everything 的 `WM_COPYDATA` IPC、注册表、Known Folders | `ErrUnsupported` |
| `internal/winapi/install` | `install_windows.go`（282）+ `shortcut_windows.go`（168） | 注册表卸载项、`IShellLinkW`、版本资源、`MoveFileEx` 延迟删除 | **没有 `_other`**（编译错误来源） |
| `internal/winapi/network` | `network_windows.go`（535） | `GetAdaptersAddresses`、WLAN API、`IcmpSendEcho2`、`GetExtendedTcpTable` | `ErrUnsupported` / `ErrNoWiFi` |
| `internal/winapi/proc` | `proc_windows.go`（104） | Job Object（`KILL_ON_JOB_CLOSE`）、`CREATE_NO_WINDOW`、cmd.exe 引号、OEM 代码页 | `group.kill()` 是空操作：**Stop 杀不掉子进程树** |
| `internal/winapi/shell` | `shell_windows.go`（469） | `shell:AppsFolder` 枚举、`IShellItemImageFactory` 图标、`ShellExecuteExW`（含 `runas`） | `ErrUnsupported` |
| `internal/winapi/system` | `system_windows.go`（223） | `LockWorkStation`、`SetSuspendState`、`ExitWindowsEx`、回收站、注册表深浅色、媒体键 | `ErrUnsupported` |
| `internal/winapi/cmdline` | —（纯 Go） | Windows 命令行引号规则、`wt.exe` 转义 | 可编译，但语义只对 Windows 成立 |
| `internal/extproc` | `proc_windows.go` | Job Object、`CREATE_NO_WINDOW` | 空 job：宿主崩溃时外部插件不会被连带结束 |
| `internal/i18n` | `system_windows.go` | `GetUserPreferredUILanguages` | 读 `LC_ALL/LC_MESSAGES/LANG`（**从 Finder/launchd 启动的 GUI 程序通常没有这些变量**，见 §5.2） |
| `internal/shell` | `dwm_windows.go` | `DwmSetWindowAttribute` 去标题栏色条 | **没有 `_other`**（编译错误来源） |
| `plugins/terminal` | `system_windows.go` | 注册表读 WSL 发行版、隐藏窗口运行 `wsl.exe` | 无发行版 |
| `plugins/winget` | `locate_windows.go` | 打包应用 API 定位 winget 索引 | `errNotInstalled` |
| `cmd/*` | `rsrc_windows_{amd64,arm64}.syso` | 图标、版本资源、manifest（`go-winres`） | darwin 下自动忽略 |

### 1.2 没有分平台、但隐含 Windows 假设的代码

| 位置 | 假设 | macOS 上的问题 |
|------|------|----------------|
| `internal/tasks/runner.go:49` | `syscall.EscapeArg` | 只在 Windows 存在，**编译错误** |
| `internal/app/app.go` `DefaultDataDir` | `os.UserCacheDir()` 下放 `data/`（Windows 上是 `%LOCALAPPDATA%`） | macOS 上变成 `~/Library/Caches/golauncher/data`：Caches 可能被系统清理、不进 Time Machine，**剪贴板历史和 frecency 数据库不能放这里**，要改到 `~/Library/Application Support/golauncher` |
| `internal/extproc/manifest.go` `EnvDirs` | `GOLAUNCHER_PLUGIN_PATH` 按 `;` 分割；命令名补 `.exe` | Unix 习惯是 `:`，应改用 `filepath.SplitList` |
| `internal/winapi/proc` `MergeEnv` | 环境变量名不区分大小写 | Unix 区分大小写 |
| `pkg/plugin` `OpenOptions.Verb`（`runas`）、`Reveal`（"在资源管理器中显示"） | Windows 语义 | `runas` 无对应，`Reveal` 映射到 Finder |
| `pkg/plugin` `AppInfo.Name`（`explorer.exe`）、`AppInfo.Package`（AUMID）、`ContextExplorer` | Windows 进程名 | 需要增加 `BundleID`，Finder 复用或新增 Kind |
| `internal/ui/keys.go` | `Super` 显示为 `Win`；`CmdOrCtrl` 归一化成 `Ctrl` | macOS 习惯用 ⌘ 作主修饰键（§5.1） |
| 插件里约 98 处 `"Ctrl+…"` 快捷键字面量 | Ctrl 是主修饰键 | 同上 |
| `plugins/scripts` | 默认目录 `%APPDATA%\golauncher\scripts`；bash 找 Git for Windows | 目录要换；PATH 问题（§5.3） |
| `plugins/files` | `wt.exe`、`pwsh`、`cmd.exe`、`Code.exe` | 换成 Terminal/iTerm2/VS Code 的 macOS 打开方式 |
| `internal/builtin` 假环境 | `fakeWhich` 返回 `wt.exe`、`C:\Program Files\…`；Scoop 用 `powershell.exe` | 假环境本身可跨平台跑，只是夹具数据是 Windows 风格 |
| 消息目录 | `en.json` 里约 53 条含 Explorer/Windows/administrator/Ctrl/Recycle 等字样（core 9、system 13、files 8、terminal 6……） | 要么加平台变体 key，要么改中性措辞 |
| CI/发布 | `windows-latest`、`build.ps1`、zip、`-H windowsgui`、`.syso` | 要新增 macOS job 和 `.app` 打包脚本（§6） |

### 1.3 已经可移植的部分

`pkg/plugin`（含 `jsonrpc`、`protocol`、`rpcserve`）、`internal/core`、`internal/match`、`internal/store`（`modernc.org/sqlite` 纯 Go，支持 darwin/arm64 和 amd64）、`internal/config`、`internal/i18n`（除系统语言探测）、`internal/install` 的版本比较与流程（与 `System` 接口解耦）、`internal/control`（unix socket，macOS 原生支持；注意 `sun_path` 限 104 字节）、`internal/tasks`（除上面的引号函数）、`internal/extproc`（stdio JSON-RPC）、`plugins/calc`、`plugins/clock`（嵌入 `time/tzdata`）、`glctl` 和假环境（`app.Build(Fake: true)`、`hostfake`、`tasks.FakeRunner`）。

## 2. `GOOS=darwin` 编译结果

命令：`GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...` 和 `go vet ./...`（amd64 结果相同）。Go 一次只报告最先失败的包，所以在临时副本里逐个补桩后重跑，直到全部通过。

| # | 包 | 错误 | 数量 | 修法 |
|---|----|------|------|------|
| 1 | `internal/tasks` | `runner.go:49: undefined: syscall.EscapeArg` | 1 | 改用平台中立的显示用引号（Windows 用 `cmdline.Quote`，Unix 用 shell 单引号） |
| 2 | `internal/shell` | `installer.go:66: undefined: winstall.Paths` | 1 | 给 `internal/winapi/install` 加 darwin 实现（或桩） |
| 3 | `internal/shell` | `installer.go:78: undefined: winstall.New` | 1 | 同上 |
| 4 | `internal/shell` | `shell.go:211: undefined: hideCaptionColor` | 1 | 加 `dwm_other.go` 空实现 |
| | **合计** | | **4 处 / 2 个包** | |

- 第 1 处让 `tasks` 失败，进而让依赖它的 21 个包（`cmd/*`、`internal/app`、`cli`、`core`、`ui`、所有插件……）在 `go build`/`go vet` 时都报同一个错；它是唯一的根因。
- 补上这 4 处之后，`go build ./...` 和 `go vet ./...`（含测试代码）在 darwin/arm64 和 darwin/amd64 下**全部通过**，`cmd/golauncher` 能链接出 Mach-O 可执行文件。这说明 MyGo 的 darwin 后端在 `CGO_ENABLED=0` 下也能编译（它依赖 `github.com/ebitengine/purego`，见 §3）。
- **测试没有在非 Windows 系统上跑过**。84 个不带 `_windows` 后缀的测试文件中有 36 个含 `C:\`、`.exe`、`APPDATA` 等字样；`internal/cli/testdata` 的 69 个 golden 文件中有 13 个含 Windows 盘符路径。预计 golden 和夹具测试在 macOS 上会有一批失败，需要按平台拆夹具或把路径改成虚拟的 `/`-风格（§6.4）。

## 3. MyGo 在 macOS 上的能力

MyGo 的 macOS 后端是正式支持的平台，不是桩：

- **实现方式**：`internal/darwin`（文件用 `//go:build darwin`）。通过 `ebitengine/purego` 在运行时 `dlopen` AppKit、Foundation、CoreGraphics、Carbon 等框架，直接调 `objc_msgSend`，运行时定义 ObjC 子类（`MyGoWindow`、`MyGoPanel`……）。**没有 cgo**，项目规定"一切必须能 `CGO_ENABLED=0` 构建"（MyGo `AGENTS.md`），所以从 Windows 交叉编译能成功。
- **渲染**：原生 UI 用 Metal 画到 `CAMetalLayer`（着色器预编译后嵌入，编译时不需要 Xcode），文字用 CoreText；有 CPU 回退（`MYGO_GPU=0`）。最低 macOS 12。
- **成熟度**：MyGo 自己的 CI 有 `macos-latest` job，跑单元测试和 GUI E2E（`internal/e2e`）；文档 `docs/launchers.md` 讲的正是 Spotlight/Raycast 式启动器在 macOS 上的做法。

### 3.1 golauncher 用到的能力

| 能力 | golauncher 的用法 | macOS 上 | 备注 |
|------|-------------------|----------|------|
| `WindowOptions.Panel` | 启动窗 | ✅ `NSPanel` 子类，创建时即为 `NonactivatingPanel`；可成为 key window；所有桌面空间、全屏应用之上可见；Show/Focus 不激活本应用 | 正是启动器需要的：前一个应用保持激活，隐藏后焦点自然回去 |
| `Frameless` | 启动窗 | ✅ 保留 titled 样式 + 全尺寸内容视图 + 隐藏标题栏和红绿灯 | 有原生圆角和阴影 |
| `Transparent` | 启动窗 | ✅ | |
| `Vibrancy: VibrancyAcrylic` | 启动窗 | ✅ `NSVisualEffectView`，Acrylic 映射到 HUD 材质（13） | 可改用 `popover`/`menu`/`under-window` 等材质，按观感挑；Windows 专用的 `hideCaptionColor` 在 macOS 上不需要 |
| `DisableResize`、`Hidden`、Show/Hide/Focus/IsVisible/SetPosition/Size | 启动窗、设置窗 | ✅ | 坐标已翻转成"主屏左上角为原点"的 DIP，和 Windows 一致，`showLauncherThen` 的定位逻辑可以照用 |
| `OnBlur`、`OnClose`、`OnHide` | 失焦隐藏、关窗即隐藏 | ✅ `windowDidResignKey:`、`windowShouldClose:`；`OnHide` 由 Go 层的 `Window.Hide()` 触发 | 系统级隐藏（⌘H）可能不触发 `OnHide`，待实测 |
| `NativeHandle()` | 传给 `hideCaptionColor` | 返回 `NSWindow*` | 缺口可以拿它用 purego 自己补 |
| `Screen.DisplayNearestPoint`、`CursorScreenPoint`、`WorkArea` | 在鼠标所在屏幕居中 | ✅ `visibleFrame`（已扣除菜单栏和 Dock）、`backingScaleFactor` | |
| `GlobalShortcut.Register` | 呼出键、命令快捷键 | ✅ Carbon `RegisterEventHotKey`，**不需要辅助功能/输入监控权限** | 加速键语法 `Cmd/Super/Win` → ⌘，`Alt/Option` → ⌥，`CmdOrCtrl` → ⌘；`shell.accelerator` 把 `Win` 改成 `Super` 的做法在 macOS 上就成了 ⌘。键码表按 ANSI 布局，非美式键盘待实测 |
| `NewTray` | 托盘 | ✅ `NSStatusItem`（菜单栏图标），支持模板图标（`IconIsTemplate`）、菜单、点击事件 | 菜单栏图标应该用单色模板图，要新画一个 |
| 激活策略 | — | ✅ `App.SetActivationPolicy(Accessory)` / `App.Dock.Hide()`；或在 Info.plist 写 `LSUIElement` | 不显示 Dock 图标；golauncher 现在没调用，要加 |
| `SetOpenAtLogin`、`WasOpenedAtLogin` | 开机自启 | ✅ macOS 13+ `SMAppService.mainAppService`，12 退回 LaunchAgent | **必须是 `.app` 包**，否则报错；需要用户在"登录项"里批准时返回错误 |
| `RequestSingleInstanceLock`、`OnSecondInstance` | 单实例、`--show` 转发 | ✅ 跨平台 Go 实现（unix socket） | 键是应用名（打包后加 bundle ID） |
| `NewNotification` | 热键冲突、配置恢复、任务完成 | ✅ `UNUserNotificationCenter` | **必须是带 bundle ID 的 `.app`**，裸二进制返回 `ErrUnsupported` |
| 剪贴板模块 | — | ✅ `NSPasteboard`：文本、HTML、PNG、文件列表、任意 UTI | **没有变化监听 API**，要自己轮询 `changeCount`；不支持写 Concealed 标记的话也要自己补 |
| `Dialog.Save/Open` | 导入导出配置 | ✅ `NSSavePanel`/`NSOpenPanel` | |
| `Theme` | 主题 | ✅ 跟随系统外观，KVO 监听变化 | |
| `Secrets` | — | ✅ 登录钥匙串 `SecItem*` | 可直接替代 `internal/winapi/cred`；签名身份变化后读取会弹授权 |
| `Shell.InstalledApps`、`FileIcon`、`LaunchApp` | — | ✅ | **应用启动器的数据源和图标可以直接用 MyGo**（它自带的 `examples/launcher` 就是这么做的），省掉 `Assets.car` 图标的难题 |
| 文本输入 / 输入法 | 搜索框 | ✅ `NSTextInputClient`（marked text、候选框位置） | 中文输入法要真机实测 |
| Retina | — | ✅ | |
| 菜单栏（编辑菜单） | — | ✅ 默认有 App/Edit 等菜单，`cut:`/`copy:`/`paste:`/`selectAll:` | 非激活面板在本应用未激活时，⌘C/⌘V 是否能到达搜索框待实测 |
| 多窗口 | 启动窗 + 设置窗 + 安装窗 | ✅ | |
| Inspector | `devTools()` | ✅ 原生 UI 的 MyGo inspector（⌥⌘I） | |
| `plugins/updater` | M6 计划 | ✅ 替换整个 `.app` | |
| 辅助功能 / 自动化权限 | — | ❌ 没有 API（`permissions.go` 只管网页的摄像头、麦克风等） | 要自己用 purego 调 `AXIsProcessTrustedWithOptions` 等 |
| 发送按键（粘贴） | — | ❌ | 自己用 `CGEventCreateKeyboardEvent` + `CGEventPost` |
| 前台应用 | — | ❌（未暴露） | 自己调 `NSWorkspace.frontmostApplication` |

### 3.2 打包工具

MyGo 的 CLI `mygo build` 能直接产出 macOS 发布物：

- `build/darwin-<arch>/<App>.app` 和 DMG（拖到"应用程序"）；`-platform darwin/universal` 用 Go 写出 Universal 二进制，**不需要 `lipo`**。
- 生成 Info.plist（`CFBundle*`、`LSMinimumSystemVersion`、`NSHighResolutionCapable`），`macos.infoPlist` 里可以加 `LSUIElement` 等；PNG 转 `.icns`。
- 签名（Developer ID 时开 Hardened Runtime + 时间戳，`macos.entitlements`）、公证（`notarytool submit --wait` + `stapler`）。签名、公证、DMG 只能在 macOS 上做，其他系统会跳过签名并警告。
- `docs/github-actions.md` 有带 macOS runner、钥匙串从 Secrets 导入的发布工作流模板。
- `mygo dev` 会生成开发用 `.app`，通知、开机自启在开发时也能用。

golauncher 现在用纯 `go build` + `build.ps1`。macOS 上建议改用 `mygo build`（需要一个 MyGo 配置文件写 bundle ID、图标、`LSUIElement`、签名参数），比自己写打包脚本省事得多。

### 3.3 结论

MyGo 覆盖了 golauncher 在 macOS 上需要的窗口、热键、托盘、单实例、自启、通知、剪贴板、钥匙串、应用列表和图标、打包签名。**需要 golauncher 自己补的只有四样**：剪贴板变化轮询（和 Concealed 类型）、前台应用、发送 ⌘V、辅助功能/自动化权限检测；外加环境感知和各插件的系统调用。它们都能用 purego + objc 写，保持 `CGO_ENABLED=0`。

## 4. 逐个插件和宿主能力的改造方案

### 4.1 宿主层

| 能力 | Windows 现状 | macOS 方案 | 权限 | 工作量 |
|------|--------------|------------|------|--------|
| 启动窗 | MyGo Panel + Acrylic + `hideCaptionColor` | MyGo Panel 原样可用（非激活、所有桌面空间、全屏应用之上，§3.1）；`hideCaptionColor` 改为空实现；加 `SetActivationPolicy(Accessory)`；挑一个合适的 Vibrancy 材质 | — | S |
| 前台应用与粘贴 | `GetForegroundWindow` 记住窗口，`SendInput` 发 Ctrl+V | 呼出前记 `NSWorkspace.frontmostApplication`（pid、bundle ID），供环境感知和来源应用用；因为面板不激活本应用，隐藏后焦点自然回到原应用，再用 `CGEventPost` 发 ⌘V（必要时先 `NSRunningApplication.activate`） | **辅助功能**（Accessibility）：发送按键必须 | M |
| 剪贴板读写 | Win32 | MyGo 剪贴板模块（文本、HTML、PNG、文件列表、任意 UTI） | — | S |
| 剪贴板监听 | `WM_CLIPBOARDUPDATE` | macOS **没有变化通知**，只能轮询 `changeCount`（250–500ms；启动窗隐藏时也要跑，开销很小） | — | S |
| 敏感内容过滤 | `ExcludeClipboardContentFromMonitorProcessing` 等格式 | 跳过带 `org.nspasteboard.ConcealedType`、`org.nspasteboard.TransientType`、`org.nspasteboard.AutoGeneratedType` 的条目（1Password、Bitwarden、KeePassXC 等都会写）；`WriteSecret` 写入时也加 Concealed 类型 | — | S |
| 来源应用 | `GetClipboardOwner` → 进程名 | 没有 owner 概念：取 `changeCount` 变化时的前台应用 bundle ID。`ignoreApps` 默认值要换成 bundle ID（`com.1password.1password`、`com.bitwarden.desktop`…） | — | S |
| 环境感知 | `IShellWindows` COM | Finder：AppleScript `tell application "Finder" to get target of front window` 和 `selection`（或 Scripting Bridge 经 objc）；桌面：前台是 Finder 且无窗口时取 `~/Desktop`；"标题含路径"：AX API 读 `AXDocument`/窗口标题 | Finder 走 AppleScript 需要**自动化**（Apple Events，对每个目标应用单独弹窗）；读其他应用窗口标题需要**辅助功能**（或屏幕录制） | M |
| 密码类偏好 | 凭据管理器 | MyGo 的 Secrets 模块（登录钥匙串 generic password），键沿用 `golauncher/<插件ID>/<偏好名>` | 签名身份变化后读取会弹钥匙串授权 | S |
| 进程管理（任务、脚本、外部插件） | Job Object | `SysProcAttr{Setpgid: true}` + `syscall.Kill(-pgid, SIGTERM→SIGKILL)` 结束整棵进程组；宿主崩溃时外部插件靠 stdin EOF 自行退出（rpcserve 已如此），后台任务可能成孤儿，启动时按记录的 pgid 清理 | — | S |
| 输出解码 | OEM 代码页 | 直接 UTF-8；保留 ANSI 和 `\r` 处理 | — | S |
| 单实例、第二实例转发 | MyGo | MyGo（§3）；另外 macOS 上用户再次双击 `.app` 时系统只会激活已运行的实例（发 `applicationShouldHandleReopen`），要把它当成 `--show` | — | S |
| 开机自启 | MyGo `SetOpenAtLogin`（Run 键） | MyGo 不变（macOS 13+ `SMAppService`，12 用 LaunchAgent），**必须以 `.app` 运行**；需要用户批准时把错误显示在设置页 | — | S |
| 通知 | MyGo | MyGo 不变（`UNUserNotificationCenter`），**必须以 `.app` 运行**，裸二进制返回 `ErrUnsupported`，此时退回启动窗内提示 | 通知权限（首次弹窗） | S |
| 系统语言 | `GetUserPreferredUILanguages` | `CFLocaleCopyPreferredLanguages` / `NSLocale.preferredLanguages`（或读 `defaults read -g AppleLanguages`）；不能依赖 `LANG` | — | S |
| 数据目录 | `%APPDATA%`、`%LOCALAPPDATA%` | 配置和数据库：`~/Library/Application Support/golauncher`；缓存（图标、汇率）：`~/Library/Caches/golauncher`；日志：`~/Library/Logs/golauncher`；外部插件和脚本：`~/Library/Application Support/golauncher/{plugins,scripts}` | — | S |
| E2E 控制通道 | unix socket | 不变 | — | — |
| 自安装 | 复制 exe、注册表、`.lnk` | 见 §4.3 | — | M |

### 4.2 插件

| 插件 | macOS 方案 | 要点与风险 | 工作量 |
|------|------------|------------|--------|
| `apps` | 数据源先用 MyGo 的 `Shell.InstalledApps`，图标用 `Shell.FileIcon`（内部走 `NSWorkspace`，系统应用 `Assets.car` 里的图标也能取到），启动用 `Shell.LaunchApp`；`apps.Source` 已是可替换的接口，只需写一个转换成 `shell.App` 的 darwin 源。若 MyGo 的列表不全，再补扫 `/Applications`、`/System/Applications`（含 `Utilities`）、`~/Applications`、`/System/Library/CoreServices`，或 `mdfind "kMDItemContentType == 'com.apple.application-bundle'"` | 本地化名：核对 MyGo 返回的是否为当前语言的显示名；不够时从 `<lang>.lproj/InfoPlist.strings` 或 Spotlight 的 `kMDItemDisplayName` 取（"微信"就在 `zh-Hans.lproj` 里，比 Windows 的 R40 好办），英文名和本地化名都放进 `Keywords`。"在 Finder 中显示"：`open -R`；去掉"以管理员身份运行"；ID 用 bundle ID 或路径去重；FSEvents 或呼出时重扫做增量 | S–M |
| `calc` | 无改动 | 汇率请求、单位换算都是纯 Go | — |
| `clipboard` | 用 §4.1 的剪贴板层 | 粘贴依赖辅助功能权限，没授权时退化为"已复制，按 ⌘V 粘贴"；`ignoreApps` 换 bundle ID；快捷键 `Alt+Shift+V` → `⌥⇧V`（或 `⌘⇧V`，与 Raycast 一致） | S |
| `clock` | 无改动 | 已嵌入 tzdata | — |
| `files`（环境感知动作） | 当前文件夹：在终端打开（Terminal/iTerm2/Warp/Ghostty，按已安装和偏好）、用 VS Code 打开（`open -a "Visual Studio Code" <dir>` 或 `code`）、在 Finder 中显示、复制路径 | 删掉 PowerShell（管理员）、cmd 等条目；`.exe/.bat/.msi` 判断换成 `.app/.command/.sh` | S |
| `everything` → **Spotlight 文件搜索** | 新 backend：`mdfind -name <q>`（每次输入一个进程，可接受）或 `NSMetadataQuery`（objc，可流式、可取消、更快）；作用域默认 `~` 加用户选的目录；仍复用插件现有的 `Backend` 接口和排序 | Spotlight 的子串匹配和速度不如 Everything；被用户排除的目录、外接盘可能没有索引；没索引时提示 `mdutil -s`。插件名和文案要改（"Everything" → "File Search"） | M |
| `scoop`、`winget` → **Homebrew 插件**（新） | 搜索读 Homebrew 本地缓存的 API JSON（`~/Library/Caches/Homebrew/api/formula.jws.json`、`cask.jws.json`），不起进程，思路同 winget 读 SQLite 索引；已安装 `brew info --json=v2 --installed`；可升级 `brew outdated --json=v2`；安装、卸载、升级、`brew update` 作为后台任务 | `brew` 位置：Apple Silicon `/opt/homebrew/bin/brew`，Intel `/usr/local/bin/brew`；GUI 进程 PATH 里没有它（§5.3）。Scoop/WinGet 插件在 macOS 上不注册 | M（新插件，可大量复用 scoop/winget 的视图和任务代码） |
| `system`（系统命令） | 锁屏：`pmset displaysleepnow`（配合"立即要求密码"）或私有 `SACLockScreenImmediate`（login.framework）；睡眠 `pmset sleepnow`；重启/关机/注销：AppleScript `tell application "System Events" to restart/shut down/log out`（或向 `loginwindow` 发 Apple Event，可避免二次确认）；清空废纸篓：`tell application "Finder" to empty trash`；深浅色：`tell application "System Events" to tell appearance preferences to set dark mode to not dark mode`；音量/静音：`osascript -e "set volume …"`；关闭显示器：`pmset displaysleepnow`；显示桌面：无公开 API，可省略或发 Mission Control 按键 | 休眠没有用户可触发的接口，删除；System Events / Finder 的 Apple Event 需要**自动化**权限（每个目标应用一次）；`Destructive` 确认和后台任务提醒逻辑不变 | M |
| `system`（设置直达） | 用 `x-apple.systempreferences:<pane-id>[?anchor]` 打开系统设置页，另列"活动监视器""磁盘工具""控制台""钥匙串访问""终端"等工具（`open -b`） | macOS 13 起"系统设置"改版，面板 ID 混用旧的 `com.apple.preference.*` 和新的 `com.apple.*.extension`，不同版本有差异，目录要按版本标注并在真机逐条验证（约 60–80 条，少于 Windows 的 150 条） | M |
| `terminal`（终端配置） | Terminal.app 配置文件（`defaults export com.apple.Terminal` 里的 `Window Settings`）、iTerm2 配置文件（`~/Library/Preferences/com.googlecode.iterm2.plist` 和 `DynamicProfiles/`）、Warp（launch configurations）；打开方式：`open -a Terminal`、iTerm2 的 AppleScript、`warp://` URL | plist 是二进制格式，需要纯 Go 解析库（如 `howett.net/plist`）或 `plutil -convert json`；用 AppleScript 控制 iTerm2 要自动化权限 | M |
| `terminal`（WSL） | 不适用，macOS 上不注册。可选的替代：`~/.ssh/config` 里的主机、Docker/OrbStack 容器 | — | —（替代功能另计） |
| `network` | 本机 IP：`net.Interfaces` 拿地址和 MAC（纯 Go），网关用 `golang.org/x/net/route`（纯 Go），DNS 用 `scutil --dns` 或 `SCDynamicStore`；Ping：`golang.org/x/net/icmp` 的 `udp4`（macOS 允许非 root 的 ICMP datagram socket）；DNS 查询、端口检测：不变；本地端口占用：`lsof -nP -iTCP -sTCP:LISTEN`（或 libproc，需要 objc/purego 调 C）；结束进程：`syscall.Kill`；路由追踪：`traceroute`；刷新 DNS：`dscacheutil -flushcache; killall -HUP mDNSResponder`（**需要管理员**，通过 `osascript … with administrator privileges` 弹系统密码框） | Wi-Fi：macOS 14 起读当前 SSID 需要**定位服务**权限（CoreWLAN），命令行方式在新版本上被逐步屏蔽，待实测；保存的 Wi-Fi 密码在系统钥匙串，`security find-generic-password -D "AirPort network password" -a <SSID> -w` 会弹管理员授权框，只能"用户确认后读取"，体验不如 Windows | M |
| `scripts`（Raycast Script Commands） | **macOS 才是 Raycast 的主场**：社区脚本大多是 `bash`/`zsh`/`osascript`（AppleScript）/`swift`/`python3`。有 shebang 且可执行时直接执行文件；`.applescript` 用 `osascript`；去掉 Git Bash、cmd、PowerShell 的特殊处理 | GUI 进程的 PATH（§5.3）；很多脚本会触发自动化权限弹窗（这是 macOS 的正常体验） | S |
| 外部插件（`extproc`） | 进程组代替 Job Object；可执行位检查；`GOLAUNCHER_PLUGIN_PATH` 用 `:` 分隔 | 未签名的插件二进制如果带 quarantine 属性，Gatekeeper 会拦；文档里提示 `xattr -d com.apple.quarantine` | S |
| 后台任务（`tasks`） | 不变，换进程组和引号函数 | — | S |
| AI（[ai.md](ai.md)） | Claude Code CLI 在 macOS 上一样可用；工作目录 `~/Library/Application Support/golauncher/ai`；进程组代替 Job Object；`golauncher mcp` 的本机管道从命名管道（`\\.\pipe\…`）换成 unix socket（目录权限 0700，同时校验 `getpeereid` 的 uid） | `claude` 通常装在 `~/.local/bin` 或 Homebrew 下，同样受 PATH 影响 | S |

### 4.3 安装、更新与卸载

macOS 的约定和 Windows 的"exe 自己安装自己"完全不同，建议**不移植自安装流程**，改用标准做法：

| 项 | 方案 |
|----|------|
| 形态 | `golauncher.app`：`Contents/MacOS/golauncher`、`Contents/Info.plist`（`CFBundleIdentifier`、`LSUIElement=true` 不显示 Dock 图标、`LSMinimumSystemVersion`、`NSAppleEventsUsageDescription` 等权限说明文字）、`Contents/Resources/golauncher.icns`（从 `assets/` 的 512/1024 PNG 用 `iconutil` 生成）。`glctl` 放进 `Contents/MacOS/` 或 `Contents/Helpers/`，安装时提示用户软链到 `/usr/local/bin` 或 `~/.local/bin` |
| 分发 | DMG（拖到"应用程序"）为主，同时提供 zip；可选 Homebrew Cask（`brew install --cask golauncher`，自己维护 tap 即可） |
| 首次运行 | 如果从"下载"目录或 App Translocation 的随机路径运行，提示"移到应用程序文件夹"（类似 LetsMove）；这是 `internal/install` 里唯一值得保留的逻辑 |
| 构建 | `mygo build -platform darwin/universal`（§3.2）：一步出 `.app`、DMG、签名、公证 |
| 升级 | MyGo `plugins/updater`（macOS 上替换整个 `.app`），与 Windows 的 M6 计划共用 |
| 卸载 | 拖到废纸篓；设置里提供"删除所有数据"（Application Support、Caches、Logs、钥匙串条目、LaunchAgent） |
| 签名与公证 | Developer ID Application 证书签名（开 Hardened Runtime）→ `xcrun notarytool submit --wait` → `xcrun stapler staple`。不签名的话：Apple Silicon 上 Go 链接器会自动做 ad-hoc 签名，能运行，但用户首次打开会被 Gatekeeper 拦下，macOS 15 起必须去"系统设置 › 隐私与安全性"点"仍要打开"；而且 **TCC 权限按签名身份记录，ad-hoc 签名每次构建都变，每次升级都要重新授予辅助功能权限** |

### 4.4 权限（TCC）一览

| 权限 | 哪些功能需要 | 没有时的退化 |
|------|--------------|--------------|
| 辅助功能（Accessibility） | 粘贴到前一个应用（发 ⌘V）；读其他应用的窗口标题、文档路径（环境感知的"标题含路径"） | 只复制不粘贴，提示用户手动 ⌘V；环境感知只用 Finder |
| 自动化（Apple Events），按目标应用分别授权 | Finder 当前文件夹和选中项、清空废纸篓；System Events 的重启/关机/注销/深浅色；iTerm2 控制；很多 Script Commands | 对应条目提示"需要在 系统设置 › 隐私与安全性 › 自动化 中允许" |
| 定位服务 | 读当前 Wi-Fi 名称（macOS 14+） | 不显示 SSID |
| 通知 | 后台任务完成、热键冲突等通知 | 只在启动窗内提示 |
| 完全磁盘访问 | 一般不需要；只有想读 `~/Library/Mail` 之类受保护目录时 | — |
| 输入监控 | 不需要：MyGo 的全局热键用 Carbon `RegisterEventHotKey`，不需要授权（§3.1） | — |

建议在首次运行时加一个"权限引导"页：逐项说明用途、显示当前状态（`AXIsProcessTrusted()` 等）、一键打开对应的系统设置页（`x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility`）。

## 5. 跨平台设计要点

### 5.1 快捷键

- **主修饰键**：引入语义修饰键 `Mod`（Windows/Linux 上是 Ctrl，macOS 上是 ⌘），和 Electron 的 `CmdOrCtrl` 同义。插件 manifest 和动作里的 `Ctrl+K`、`Ctrl+Enter`、`Ctrl+Shift+C` 等改写成 `Mod+…`，并让旧写法 `Ctrl+…` 在 macOS 上也解释为 ⌘（兼容外部插件）。需要真 Control 的场合写 `Control+…`（如 `Control+N`/`Control+P` 移动选中项，这在 macOS 上本来就是 Emacs 风格的习惯）。
- **显示**：macOS 上显示 `⌘⌥⇧⌃` 符号，顺序按 Apple 规范（⌃⌥⇧⌘）；`i18n.Localizer.Shortcut` 已经是本地化显示的入口，正好放这里。
- **默认呼出键**：`Alt+Space` → `⌥Space`（Alfred 的默认值，和系统无冲突）。`⌘Space` 是 Spotlight，`⌃Space` 是切换输入法（中文用户很常用），都不要占用；设置页可以提示"想用 ⌘Space 需先在系统设置里关掉 Spotlight 快捷键"。
- **设置**：`Ctrl+,` → `⌘,`（macOS 统一约定）；`⌘Q` 只关窗口还是退出要定（Raycast 是隐藏）。

### 5.2 代码组织

建议的目标结构（一次性重命名，避免 `winapi` 下放 darwin 代码的别扭）：

```
internal/platform/          # 平台中立的接口和类型（现在各 winapi 包里的 App、Result、Adapter…）
  clip/      clip.go  clip_windows.go  clip_darwin.go  clip_other.go
  shell/     …        shell_darwin.go（NSWorkspace、mdfind、plist）
  context/   …        detect_darwin.go（Finder AppleScript、AX）
  cred/      …        cred_darwin.go（Keychain）
  proc/      …        proc_unix.go（进程组）
  system/    …        system_darwin.go
  network/   …        network_darwin.go
  install/   …        install_darwin.go
internal/darwin/objc/       # purego + objc 的小封装：NSPasteboard、NSWorkspace、AX、CGEvent
```

- 平台差异尽量停在这一层；插件里只保留"这个平台有没有这项功能"的判断（已有 `plugin.ErrUnavailable` 机制，没有的功能插件直接不启用，设置页显示原因）。
- `internal/builtin.All` 按平台挑插件：darwin 上不注册 `scoop`、`winget`、`everything`（换成 Spotlight backend）、WSL 部分；注册新的 `homebrew`。用 `builtin_windows.go`/`builtin_darwin.go` 两个列表最清楚。
- 原生调用优先 **purego + objc 运行时**（MyGo 已依赖 `ebitengine/purego`，它自带 `objc` 包），保持 `CGO_ENABLED=0`、能从任何平台交叉编译；只有 purego 难以表达的（block 回调多的 API，如 `NSMetadataQuery` 通知、`UNUserNotificationCenter`）再考虑 cgo，或改用命令行工具（`mdfind`、`osascript`、`pmset`、`security`、`plutil`）。
- 命令行工具方案开发快、易测（输出可做夹具），但每次起进程有 20–50ms 开销，不适合每次按键都调（Spotlight 搜索除外，可以容忍）。

### 5.3 GUI 进程的 PATH

从 Finder、Dock、登录项启动的 `.app`，环境是 launchd 给的，`PATH` 只有 `/usr/bin:/bin:/usr/sbin:/sbin`，找不到 Homebrew（`/opt/homebrew/bin`）、`~/.local/bin` 里的 `brew`、`python3`、`node`、`code`、`claude`。脚本、外部插件、Homebrew、AI 后端都会受影响。方案：启动时在后台跑一次 `$SHELL -l -i -c 'printf %s "$PATH"'`（带超时）取用户登录 shell 的 PATH 并缓存，失败时退回"默认 PATH + `/opt/homebrew/bin` + `/usr/local/bin` + `~/.local/bin`"；设置里允许手动追加。

### 5.4 国际化

- 现有 `en`、`zh-CN` 目录可以直接用；需要处理的是**平台相关的措辞**：资源管理器/Finder、回收站/废纸篓、以管理员身份运行、开始菜单、Ctrl。建议 key 后缀约定 `…@darwin`，`Localizer.T` 先找平台变体再退回通用 key；`glctl i18n check` 同步支持。
- 中文术语跟随 macOS 简体中文的官方叫法：访达、废纸篓、系统设置、启动台、钥匙串、聚焦（Spotlight）、菜单栏。
- 中文输入法（IME 预编辑、候选框位置）依赖 MyGo 的 `NSTextInputClient` 实现，列为第一期必测项（§3）。

## 6. 工程

### 6.1 构建矩阵

| 目标 | 方式 |
|------|------|
| darwin/arm64、darwin/amd64 | `CGO_ENABLED=0`，可在任何系统交叉编译（已验证能链接）；golauncher 自己的原生调用也坚持 purego，不引入 cgo |
| Universal 2 | `mygo build -platform darwin/universal`（Go 写出 fat binary，不需要 `lipo`） |
| `.app` 打包 | `mygo build`：Info.plist（版本号、`LSUIElement`、权限说明文字 `NSAppleEventsUsageDescription` 等放 `macos.infoPlist`）、`.icns`、签名、DMG、公证。`glctl` 作为附带的可执行文件放进包内（签名时一起签） |
| 最低系统版本 | MyGo 要求 macOS 12；建议定 macOS 13（Ventura）：`SMAppService`、新版系统设置，面板 ID 少一套 |

### 6.2 CI

- `ci.yml` 增加 `macos-latest`（Apple Silicon）job：`gofmt`、`go vet ./...`、`go test ./...`、`-race`（race 需要 cgo，macOS runner 自带 Xcode CLT，没问题）、构建 `.app`、`glctl i18n check`。公开仓库的 macOS runner 免费。
- 在 Windows job 里加一步 `GOOS=darwin go build ./... && GOOS=darwin go vet ./...`，防止以后又引入只在 Windows 存在的符号（例如这次的 `syscall.EscapeArg`），成本几乎为零，建议**第一期之前就加**。
- `release.yml` 增加 macOS job（参照 MyGo `docs/github-actions.md` 的模板）：`mygo build -platform darwin/universal` → 签名 → 公证 → stapler → DMG/zip → 加入 checksums。签名证书（`.p12` + 密码）和公证凭据放 GitHub Secrets。

### 6.3 签名与公证的成本

| 项 | 成本 |
|----|------|
| Apple Developer Program | 99 美元/年（个人账号即可，显示个人姓名；组织账号需要 D-U-N-S 编号） |
| Developer ID Application 证书 | 包含在会员内；有效期 5 年 |
| 公证 | 免费；每次几分钟；在 CI 的 macOS runner 上由 `mygo build` 调 `notarytool` 完成（MyGo 在非 macOS 上会跳过签名） |
| 不加入的后果 | 只能 ad-hoc 签名：用户要手动"仍要打开"，每次升级 TCC 权限失效；不适合普通用户 |

### 6.4 测试策略

| 层 | macOS 上怎么做 |
|----|----------------|
| 插件单元测试、`core`、`glctl` golden | 天然可移植；golden 里 13 个含 Windows 路径的文件要改成平台中立的虚拟路径，或按平台分目录（`testdata/golden/darwin/`）。macOS 专有插件（Homebrew、Spotlight、Finder 环境）照现有做法用夹具：`brew` 的 JSON、`mdfind` 输出、AppleScript 返回值 |
| 平台层 | 解析逻辑（plist、`lsof`、`scutil`、`InfoPlist.strings`）与系统调用分开，夹具测试；真机只读测试用环境变量开关（沿用 `GOLAUNCHER_*_TESTS=1` 的约定） |
| 会改系统状态的调用 | 一律走 `System` 接口的假实现（同 Windows 的约定） |
| UI | `ui.NewTester`、`ui.Render` 离屏渲染与平台无关；字体回退可能导致截图差异，截图比对按平台分基线 |
| 端到端 | `glctl attach` + unix socket 在 macOS 上可用；CI 的 macOS runner 有图形会话，但没有 TCC 授权，粘贴、AppleScript 相关的 E2E 只能在本地 Mac 跑 |

### 6.5 工作量估算

单位：人日，按熟悉本仓库、有一台 Mac 的开发者估。S ≈ 1–2，M ≈ 3–6，L ≈ 8–15。

| 模块 | 内容 | 估算 |
|------|------|------|
| 编译修复 + CI 交叉编译守护 | §2 的 4 处，加 `GOOS=darwin` vet 步骤 | 0.5 |
| 平台层重组 | `winapi` → `platform`，接口提纯，`builtin` 分平台 | 3–4 |
| objc/purego 小封装 | `changeCount`、pasteboard 类型、`frontmostApplication`、`CGEventPost`、`AXIsProcessTrusted`、`CFLocaleCopyPreferredLanguages`（MyGo 已覆盖的不重复做） | 2–3 |
| shell / 窗口适配 | 激活策略、Vibrancy 材质、Reopen、菜单栏模板图标、⌘C/⌘V 在面板里的验证、IME 验证 | 2–4 |
| 剪贴板 + 粘贴 | MyGo 读写 + 轮询、Concealed、来源应用、⌘V | 2–3 |
| 应用索引 + 图标 | 基于 `mygo.Shell`，补本地化名、增量更新 | 2–4 |
| 路径、语言、PATH、凭据（MyGo Secrets）、进程组 | §4.1 各小项 | 2–3 |
| 快捷键语义（`Mod`）+ 显示 | `keys.go`、manifest、约 98 处字面量、文档 | 2–3 |
| Spotlight 文件搜索 | backend + 文案 | 3–5 |
| 系统命令 + 设置目录 | 命令实现、约 70 条设置页逐条验证 | 4–6 |
| 终端插件（macOS 版） | Terminal/iTerm2/Warp 配置读取与打开 | 3–5 |
| Homebrew 插件 | 复用 winget/scoop 结构 | 5–8 |
| 网络工具 | 网卡、ping、端口、Wi-Fi | 3–5 |
| 环境感知 | Finder、AX 标题、权限退化 | 3–4 |
| 脚本、外部插件、任务、AI 的平台差异 | 进程组、shebang、socket | 2–3 |
| i18n 平台变体 | 机制 + 约 53 条文案 + 中文术语 | 2 |
| 打包、签名、公证、DMG、发布流水线 | `mygo build` 配置 + release.yml + 证书 | 2–3 |
| 权限引导页、首次运行、"移到应用程序" | UI + 检测 | 2–3 |
| 测试与真机验证 | golden 分平台、夹具、真机回归、文档 | 5–8 |
| **合计** | | **约 50–80 人日** |

最小可用版（第 0 期 + 第一期：编译修复、平台层、窗口、剪贴板、应用、快捷键、路径/语言/PATH、Spotlight、权限引导、测试的一部分）约 **30–40 人日**。

## 7. 分期建议

| 期 | 内容 | 验收 |
|----|------|------|
| **第 0 期（半天，现在就可以做）** | 修 §2 的 4 处编译错误；CI 加 `GOOS=darwin` 的 build/vet；`DefaultDataDir` 在 macOS 上指向 Application Support（还没用户，零迁移成本） | Windows CI 全绿，darwin 交叉编译通过 |
| **第一期：核心可用（约 6–8 周）** | 平台层重组；窗口/热键/托盘（菜单栏图标）/单实例/开机自启；剪贴板（含粘贴、Concealed）；应用启动器（含本地化名、图标）；计算器、世界时钟、剪贴板历史；Spotlight 文件搜索；`Mod` 快捷键；系统语言、路径、PATH、钥匙串；权限引导；ad-hoc 签名的 `.app` 供内测 | 在 Mac 上：⌥Space 呼出 < 50ms，搜应用（含"微信"）、计算、剪贴板粘贴、中文输入法正常；`go test ./...` 在 macos-latest 全绿 |
| **第二期：插件补齐（约 3–4 周）** | 系统命令与设置直达；终端（Terminal/iTerm2/Warp）；网络工具；Homebrew 插件；环境感知（Finder）；Script Commands 的 macOS 适配；外部插件和后台任务的进程组 | 各插件夹具测试 + 真机只读测试；Raycast 社区的若干 bash/AppleScript 脚本直接可用 |
| **第三期：分发与打磨（约 2 周）** | Developer ID 签名、公证、DMG、Universal 2、release 流水线、自动更新、Homebrew Cask；平台文案变体；README/文档的 macOS 部分 | 下载 DMG → 拖入应用程序 → 双击即开，无 Gatekeeper 警告；升级后权限保留 |

## 8. 风险

| 风险 | 影响 | 对策 |
|------|------|------|
| MyGo 仍是 0.x；golauncher 只在 Windows 上验证过它 | 中 | MyGo 的 macOS 支持本身较成熟（有 macOS GUI CI）。第一期第一周先做技术验证：Panel 焦点、⌘C/⌘V 在面板里、中文输入法、Vibrancy 观感、非美式键盘热键；缺口用 `NativeHandle()` 拿 `NSWindow*` 自己补 |
| 粘贴、环境感知依赖 TCC 权限，用户可能不授权 | 中 | 设计成可退化；权限引导页说明用途 |
| 未签名分发体验差，ad-hoc 签名导致每次升级重新授权 | 中 | 第三期必须有 Developer ID；内测阶段固定用一个自签名证书签名，至少让本机 TCC 记录稳定 |
| 系统设置面板 ID 随 macOS 版本变化 | 低 | 目录带版本范围，CI 里不验证，发版前真机抽查 |
| Spotlight 搜索的速度和准确度不如 Everything | 中 | 先用 `mdfind`，体验不够再换 `NSMetadataQuery`；或者可选接入 `fd`/自建索引（另立项） |
| 快捷键语义改动影响现有 Windows 用户和外部插件 | 低 | `Mod` 在 Windows 上就是 Ctrl；旧写法继续有效 |
| 平台层重命名牵动面大，和正在进行的 AI 分支冲突 | 中 | 等 AI 第二期合并后再做重命名；或者先不改名，只在 `winapi/*` 下加 `_darwin.go` |

## 9. 需要你决定的问题

1. **是否投入**：macOS 版是正式目标，还是先做第 0 期（只保证能编译、防回归）观望？
2. **开发机**：有没有可长期使用的 Mac（建议 Apple Silicon）？没有的话，第一期之后的真机验证无法进行，CI 只能覆盖编译和单元测试。
3. **Apple Developer 账号**：是否愿意每年 99 美元做签名公证？不签的话，只适合自己和开发者用。
4. **一个代码库**：建议保持一个仓库、一个版本号，Windows 和 macOS 同时发版。同意吗？
5. **平台层改名**：`internal/winapi` → `internal/platform`（一次性大重命名），还是保留目录名只加 `_darwin.go`？
6. **快捷键**：同意引入 `Mod`（macOS 上是 ⌘），并把默认呼出键定为 `⌥Space` 吗？
7. **范围取舍**：WSL 没有对应物，是否做 SSH 主机 / Docker 容器作为替代？Homebrew 插件放第二期还是第一期？
8. **最低系统版本**：macOS 13 可以吗（覆盖 2017 年以后的大部分 Mac）？
9. **分发渠道**：DMG + zip 之外，要不要 Homebrew Cask？Mac App Store 因沙盒限制（全局热键、辅助功能、AppleScript）不建议。

## 10. 实现状态（2026-10-16，`macos-support` 分支）

没有做 §5.2 的平台层改名：darwin 实现直接以 `_darwin.go` 放在 `internal/winapi/*` 下（§8 最后一条的备选方案），`_other.go` 收窄为 `!windows && !darwin`。原生调用走新增的 `internal/darwin/ns`（purego + objc 运行时的小封装：NSString/NSData/NSArray 转换、autorelease pool），**没有 cgo**，`CGO_ENABLED=0` 可交叉编译。

### 10.1 已实现

| 模块 | 实现 | 测试 |
|------|------|------|
| 编译 | §2 的 4 处：`tasks` 的显示用引号分平台（Unix 上能用双引号时与 Windows 相同，否则单引号）；`winapi/install` 加 `_other`（全部 `ErrUnsupported`，shell 因此跳过自安装、自动更新显示为 portable）；`shell/dwm_other.go` | darwin 上 `go build/vet/test ./...` 全过；Windows、darwin/arm64、linux 交叉 `build`/`vet` 通过 |
| 数据目录 | `DefaultDataDir` 在 macOS 上是 `~/Library/Application Support/golauncher/data`；配置和外部插件目录本来就在 Application Support，缓存在 `~/Library/Caches/golauncher` | — |
| 外部插件路径 | `GOLAUNCHER_PLUGIN_PATH` 用 `filepath.SplitList`（Unix 上是 `:`） | 单元测试 |
| 环境变量 | `proc.MergeEnv`/`Getenv` 在 Unix 上区分大小写 | 单元测试 |
| 进程组 | `proc`：`Setpgid` + `kill(-pgid, SIGKILL)`，结束整棵进程树（20ms、200ms 后各补发一次，防止信号时恰在 fork 的子进程漏网）；`extproc`：同样用进程组代替 Job Object，插件进程退出后清掉组里的残留进程 | `TestStopEndsTree`（Unix 版） |
| AI 工具管道 | `winapi/pipe` 的 Unix 实现：unix socket，放在只有本人能进的目录（`$XDG_RUNTIME_DIR/golauncher` 或 `~/Library/Caches/golauncher/run`，0700），socket 0600，并用 `LOCAL_PEERCRED`（Linux 上 `SO_PEERCRED`）校验对端 uid；名字过长时取哈希以适应 104 字节的 `sun_path`；`golauncher mcp` 找不到启动器时用 `Setsid` 在后台启动它 | 单元测试 |
| 快捷键 | 语义不变、写法不变：manifest 里的 `Ctrl+…` 在 macOS 上就是 ⌘（`core.MacKeys`），`Control` 表示真正的 ⌃，`Cmd/Win/Super/Mod/CmdOrCtrl` 都归一为主修饰键；按键显示为 `⌃⌥⇧⌘` 符号（Apple 顺序）；⌃N/⌃P 也移动选中项；全局热键转成 MyGo 的 `Cmd`/`Ctrl`。`core` 和 `ui` 两份重复的规范化函数合并为 `core.NormalizeShortcut` | `TestKeysMac`、`TestAccelerator` |
| 启动窗 | `SetActivationPolicy(Accessory)`：无 Dock 图标 | 待实测 |
| GUI 进程的 PATH | §5.3：PATH 看起来是 launchd 给的时，启动时问一次登录 shell（`$SHELL -l -c`，2 秒超时），失败则补 `/opt/homebrew/bin` 等常见目录 | 合并逻辑有单元测试 |
| 应用 | `winapi/shell`：扫描 `/Applications`、`/System/Applications`、CoreServices 的 Applications、`~/Applications`（各下一层）和访达；名称取访达的本地化显示名，`Info.plist`、`*.lproj/InfoPlist.strings`、`InfoPlist.loctable` 里的中英日韩名作为关键词（"微信"/"WeChat" 都能搜到）；跳过后台应用；图标用 `NSWorkspace iconForFile:` 画成 PNG；打开用 `/usr/bin/open`，在访达中显示用 `open -R`，`runas` 返回 `ErrUnsupported`；`Browsers`、`ProtocolRegistered` 走 Launch Services。`apps` 插件对 `.app` 去掉"以管理员身份运行"，显示"在访达中显示" | 用假的 bundle 目录做单元测试；只读的真机测试（列出访达、计算器） |
| 剪贴板 | `winapi/clip`：NSPasteboard 读写文本、文件、PNG（只有 TIFF 时转 PNG）；带 Concealed/Transient/AutoGenerated 等标记的内容按"排除"处理，`WriteSecret` 写 Concealed 标记；300ms 轮询 `changeCount`（忽略自己的写入）；来源应用取前台应用的可执行文件名；`ForegroundWindow` 在 macOS 上返回前台应用的 pid；`PasteTo` 先激活该应用，再用 `CGEventPost` 发 ⌘V，没有辅助功能权限时返回 `ErrNotTrusted`；`CopySelection` 不支持。剪贴板插件在 macOS 上的 `ignoreApps` 默认值换成 macOS 的名字 | 纯逻辑单元测试；真实粘贴板测试需 `GOLAUNCHER_CLIPBOARD_TESTS=1`（会保存并恢复粘贴板，从不发 ⌘V） |
| 系统语言 | `NSLocale preferredLanguages`，失败时读 `defaults read -g AppleLanguages`，再退回 `LANG` | 单元测试 |
| 钥匙串 | `winapi/cred`：Security 框架的 generic password（purego 调 `SecItem*`，密码不出现在命令行上），service 是原来的 target，account 是 `golauncher` | 真机测试需 `GOLAUNCHER_KEYCHAIN_TESTS=1` |
| 系统命令 | `winapi/system`：锁屏（`SACLockScreenImmediate`，失败时 `pmset displaysleepnow`）、睡眠（`pmset sleepnow`）、注销/重启/关机（System Events）、清空废纸篓（Finder）、废纸篓大小（`~/.Trash` 和各卷的废纸篓，无完全磁盘访问权限时报错）、深浅色、音量/静音、关闭显示器；休眠不支持。`system` 插件在 Mac 上隐藏休眠和"显示桌面"，用 macOS 措辞，设置直达换成 45 个"系统设置"面板（`settings_macos.json`）和 8 个实用工具 | 全部通过可替换的 `run` 变量做假实现，测试只核对命令行，**从不真的执行** |
| 文件搜索 | `winapi/everything` 的 darwin 后端用 `mdfind -0 [-onlyin]`：把 Everything 风格的查询（多词、引号、`!`、`|`、`*?`、`ext:`、`file:`/`folder:`、路径词）转成 Spotlight 查询，结果再按完整查询在 Go 里过滤；取消即结束 `mdfind`；系统目录、缓存、app bundle 内部排在后面。插件在 macOS 上显示 "File Search (Spotlight)"、"在访达中显示"，无结果时提示 `mdutil -s /` | 查询构造、过滤、排序、输出解析有单元测试；只读的真机 `mdfind` 测试 |
| 脚本命令 | Unix 规则：shebang 的绝对路径存在就直接用，`#!/usr/bin/env prog` 在 PATH 和 Homebrew 等目录里找；按扩展名默认 bash/zsh/python3/node/ruby/swift/osascript/pwsh，额外扫描 `.zsh .rb .swift .applescript`；默认目录 `~/Library/Application Support/golauncher/scripts` | `TestInterpreterUnix` |
| 平台文案 | `i18n` 支持平台变体 key `id@darwin`（用户语言的通用译文优先于未翻译的变体；`glctl i18n check` 认得变体）；先改了 8 条核心文案（钥匙串、跟随 macOS 深浅色、关于等）。只有真实的 shell 打开变体，glctl 的假环境在任何平台上都保持 Windows 文案 | `TestPlatformVariants` |
| 时钟 | 顺带修了一个 Windows 上的 bug：Go 自带的 zoneinfo 在年末给出不是真实切换的边界，"下次切换"曾显示"1 月 1 日 → UTC+11"（偏移没变），现在跳过这种边界；golden 随之更新 | `TestNextChangeSlimZoneinfo` |
| CI | Windows job 加 `GOOS=darwin` 的 build/vet；新增 `macos-latest` job：vet、test、构建、`glctl i18n check`、Windows 交叉编译 | — |
| Homebrew | 新增独立插件 `builtin.homebrew`（[homebrew.md](homebrew.md)，与 scoop/winget 不共享代码）：读 brew 缓存里的 API 索引（新版 `api/internal/packages.*.jws.json`、旧版 `formula/cask.jws.json`、退路 `*_names.txt`）搜索 formula 和 cask；`brew info --json=v2 --installed` 列出已安装/可升级（后台、缓存）；⌘D 详情（`brew info`）；安装/升级/卸载/zap/全部升级/`brew update` 走后台任务队列 `homebrew`，实时输出；brew 按 `/opt/homebrew`、`/usr/local`、PATH 查找，找不到时显示"没有安装 Homebrew"并链接 brew.sh。只在 darwin 注册（`internal/builtin` 一处 `TODO(platforms)`）；假环境不含它，`glctl i18n check` 经 `builtin.PlatformPlugins()` 检查其翻译 | 夹具来自本机 Homebrew 7.0.9；只读真机测试需 `GOLAUNCHER_HOMEBREW_TESTS=1`；测试从不安装或卸载 |

### 10.2 测试的平台约定

- glctl 的假环境（`app.Build(Fake: true)`、`builtin.Fake`）在所有平台上都模拟一台 Windows 机器：剪贴板默认值、Everything 文案、系统插件（`system.Fake.Mac` 默认 false）、消息变体都按 Windows 走，所以绝大多数 golden 在 macOS 上逐字节相同。
- 夹具本身就是 Windows 路径、而代码按本机规则解析路径的测试，在非 Windows 上跳过并写明原因：`internal/cli` 的 11 个 golden（`windowsGoldens`，命令照样运行，只是不比对输出）、Windows Terminal、Scoop 的 PowerShell 脚本、进程管理器的 Windows 进程树、开始菜单过滤、Windows 解释器规则（`TestInterpreter`）。
- macOS 首次运行刚编译出的可执行文件会被系统检查（空闲时约 0.7 秒，机器繁忙时可能超过插件 5 秒的启动时限），`extproctest.BuildExample` 编译完先空跑一次。

### 10.3 还没做

- **环境感知**（Finder 当前文件夹/选中项、AX 窗口标题）：`winapi/context` 在 macOS 上仍恒为 false；选中文本（`winapi/selection`）、进程管理器（`winapi/process`）、网络工具（`winapi/network`）仍是 `ErrUnsupported` 桩。
- **终端插件**的 macOS 版（Terminal/iTerm2/Warp）、`files` 插件的 macOS 打开方式（目前仍是 `wt.exe`、`Code.exe` 等）、**Homebrew 插件**；`scoop`、`winget`、`terminal`、`processes` 在 macOS 上照常注册，只是不可用（§5.2 建议的 `builtin_darwin.go` 分平台列表没做）。
- **权限引导页**（辅助功能、自动化的状态与跳转）：只有 `clip.Trusted()` 可用于检测辅助功能权限，界面没做；没有权限时粘贴只会失败并报错，还没有退化成"已复制，请按 ⌘V"。
- 再次双击 `.app` 时的 `applicationShouldHandleReopen` → `--show`、菜单栏单色模板图标、Vibrancy 材质挑选。
- 插件和设置页里其余约 45 条 Windows 措辞（资源管理器、回收站、以管理员身份运行、Win+V……）还没加 `@darwin` 变体；`OpenOptions.Reveal` 等注释仍写 Explorer。`glctl i18n check` 对 everything 插件用 `msg("…")` 拼出的 key 报"未引用"警告（不是错误）。
- 自安装、自动更新：macOS 上一律跳过（显示为 portable）；`.app` 打包、签名、公证、DMG、release 流水线（第三期）都没做。
- 自动更新用的系统代理在 macOS 上只读环境变量（没有读 `scutil --proxy`）。
- （2026-10-16 更新）Homebrew 插件已完成，见 §10.1；需要 sudo 的 cask 在后台任务里会失败（无终端），见 [homebrew.md](homebrew.md) §7。

### 10.4 需要在真机上手动验证

- 启动窗：⌥Space 呼出、非激活面板的焦点、⌘C/⌘V 在搜索框里、中文输入法、无 Dock 图标、关闭/失焦隐藏。
- 应用：启动和切到前台、"在访达中显示"、`open -a … --args` 是否收到参数、中英文系统下的名称、Retina 下的图标清晰度、应用很多时的扫描耗时（本机约 1.2 秒）。
- 剪贴板：授予**辅助功能**后粘贴到 TextEdit/Safari/终端/Electron 应用（激活时序、热键残留的修饰键）；未授权时的报错；来源应用是否正确；1Password/Bitwarden/KeePassXC 的 Concealed 跳过；预览/截图只有 TIFF 的图片；访达里的文件复制粘贴。
- 系统命令：锁屏（`SACLockScreenImmediate` 在 13–15 上是否可用，退回方案是否锁屏）；注销/重启/关机、清空废纸篓、切换深浅色首次触发的**自动化**授权弹窗；废纸篓计数在有/无**完全磁盘访问**时；音量、静音、关闭显示器；45 个系统设置面板抽查（尤其 `Privacy_*`）。
- 钥匙串：Set/Get/Delete；签名身份变化后是否弹授权（本次在无界面的会话里写入被拒绝：`-25308 User interaction is not allowed`，属环境原因）。
- 文件搜索：每次查询 0.5–1.2 秒，打字时的体感；关闭索引的磁盘；排序是否合理。
- 系统语言（本机返回 `zh-Hans-CN`、`en-CN`，解析为 zh-CN）、登录 shell PATH（从访达启动后脚本能否找到 Homebrew 的 python3/node）、`golauncher mcp` 在启动器未运行时能否把它拉起来。
- Homebrew：从访达启动后能否找到 brew；搜索（"vs code"、"微信"、`brew rg`）；安装/卸载一个小 formula（如 `cowsay`）和一个 cask，⌘J 看实时输出、停止；可升级列表与 `brew outdated` 一致；需要密码的 cask 的报错；没有 brew 的机器上的"没有安装 Homebrew"状态和 brew.sh 链接。
