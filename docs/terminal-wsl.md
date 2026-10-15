# 终端配置与 WSL 插件设计方案

> 状态：设计 v1 · 2026-10-15 · 对应需求 R31（见 [requirements.md](requirements.md)）

内置插件 `builtin.terminal`（`plugins/terminal`）。Windows Terminal 和 WSL 都没有安装时，插件不启用（`ErrUnavailable`）。只装了其中一个时，只显示那一部分。

## 1. Windows Terminal 配置

- **配置文件位置**，按顺序查找：
  1. 商店版：`%LOCALAPPDATA%\Packages\Microsoft.WindowsTerminal_8wekyb3d8bbwe\LocalState\settings.json`
  2. 预览版：包名为 `Microsoft.WindowsTerminalPreview_…`
  3. 非商店版（scoop、winget 便携版等）：`%LOCALAPPDATA%\Microsoft\Windows Terminal\settings.json`
- **解析**：`settings.json` 是 JSONC，允许注释和末尾逗号，需要先做宽松的预处理再解析。读取 `profiles.list`，以及 `profiles.defaults` 和 `defaultProfile`。跳过 `hidden: true` 的配置。另外读取 `%LOCALAPPDATA%\Microsoft\Windows Terminal\Fragments` 下由其他程序动态添加的配置片段（比如 Git Bash、VS 开发者命令行）。
- **根搜索条目**：每个配置一条，标题是配置名，副标题是"Windows Terminal profile"和命令行，分组为"Terminal"。图标用配置里的图标（文件路径），没有就用内置的 `terminal` 图标。

| 动作 | 快捷键 | 命令 |
|------|--------|------|
| 新窗口打开（主动作） | `Enter` | `wt.exe -w new -p "<name>"` |
| 在已有窗口中新开标签页 | `Ctrl+Enter` | `wt.exe -w 0 nt -p "<name>"` |
| 以管理员身份打开 | `Ctrl+Shift+Enter` | `runas`，需要确认 |
| 在当前文件夹打开 | — | 有环境感知的 `Folder` 时才显示，加 `-d "<folder>"` |
| 复制命令行 | — | |

- **刷新**：每次窗口显示时检查 `settings.json` 的修改时间，有变化就重新读取，然后调用 `Invalidate`。

## 2. WSL 发行版

- **列表**：读注册表 `HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss\{guid}`，取 `DistributionName`、`BasePath`、`Version`，以及默认发行版 `DefaultDistribution`。读注册表很快，不需要启动任何进程。
- **运行状态**：在后台执行 `wsl.exe --list --verbose` 获取每个发行版是否在运行。输出是 UTF-16LE，需要先转码。这个命令不会启动 WSL 虚拟机；结果缓存 5 秒。
- **条目**：每个发行版一条，例如 `Ubuntu-22.04 · WSL 2 · Running`。

| 动作 | 命令 |
|------|------|
| 打开（主动作） | 有同名的 Windows Terminal 配置时用 `wt -p`，否则用 `wsl.exe -d <name>` |
| 在当前文件夹打开 | 有环境感知的 `Folder` 时才显示：`wsl.exe -d <name> --cd "<folder>"` |
| 在资源管理器中打开 | `\\wsl.localhost\<name>` |
| 设为默认 | `wsl.exe -s <name>` |
| 停止这个发行版 | `wsl.exe -t <name>`，需要确认 |

- **WSL 全局命令**：
  - Shut Down WSL：`wsl --shutdown`，需要确认；
  - Update WSL：`wsl --update`，作为后台任务执行（R24）。

## 3. 测试

- **配置文件**：fixture 覆盖带注释、末尾逗号、隐藏配置、动态配置片段的 `settings.json`；三个位置都要测；修改后能检测到变化。
- **WSL**：注册表通过接口读取，测试里用假数据；`wsl -l -v` 的输出用 UTF-16LE 的 fixture，覆盖中文环境下的输出格式（表头会是中文）。
- **命令**：用假的执行器，只验证拼出的命令行（引号、路径里有空格、`--cd`），**不在测试里真的启动终端或 WSL**。
- **真机只读测试**：只在设置 `GOLAUNCHER_TERMINAL_TESTS=1` 时运行，只读取本机真实的配置和发行版列表，不启动、不停止任何东西。
- `glctl --fixtures` golden。

## 4. 实现记录

> 2026-10-15 · 分支 `worktree-agent-abf4a76cfab54b62f`

### 4.1 代码结构

| 文件 | 内容 |
|------|------|
| `plugins/terminal/terminal.go` | 插件本体：`Options`（可注入 `Getenv`、`LocalAppData`/`ProgramData`、`Which`、`Registry`、`Run`、`Sync`）、`Init`/`Close`、刷新逻辑、`RootItems`、`HandleAction` |
| `plugins/terminal/settings.go` | JSONC 预处理（`StandardJSON`：去掉 `//`、`/* */` 注释和末尾逗号，字符串内容不动，去掉 UTF-8 BOM）、三个位置、配置片段、分层合并、图标解析、修改时间签名 |
| `plugins/terminal/wsl.go` | `Registry`/`Distro`、`DecodeOutput`（UTF-16LE，带或不带 BOM；`WSL_UTF8=1` 时的 UTF-8）、`ParseListVerbose` |
| `plugins/terminal/items.go` | 根搜索条目和动作的构造 |
| `plugins/terminal/system_windows.go` / `system_other.go` | 读 `HKCU\…\Lxss`（`golang.org/x/sys/windows/registry`），以及不弹窗口地运行 `wsl.exe`（`CREATE_NO_WINDOW`） |
| `plugins/terminal/fixture.go` | `FixtureOptions(dir)`：`glctl --fixtures` 用的假系统，读 `testdata/fixtures/terminal/` |
| `internal/winapi/cmdline` | 新的共享包：`Quote`/`Join`（按 `CommandLineToArgvW` 规则加引号）和 `WT`/`JoinWT`（额外把 `;` 转义成 `\;`，否则 wt.exe 会把它当成子命令分隔符） |

### 4.2 行为细节

- **可用性**：`wt.exe`（PATH 或 `%LOCALAPPDATA%\Microsoft\WindowsApps` 下的应用执行别名）、三个位置之一的 `settings.json`、注册表里至少一个发行版，三者都没有时 `Init` 返回 `ErrUnavailable`（"Neither Windows Terminal nor a WSL distribution is installed"）。只有 `wt.exe`、从没启动过 Windows Terminal 时插件可用但没有配置条目。是否可用只在启动时判断。
- **配置合并顺序**：`settings.json` 里的字段 → 片段里 `updates` 指向这个 GUID 的修改 → 同名同来源（`source` 等于片段所在目录名，如 `Git`）的片段配置 → `profiles.defaults`。`settings.json` 里还没出现的片段配置直接追加（Windows Terminal 下次启动也会加上）。`hidden: true`（无论来自哪一层）都跳过。片段读 `%LOCALAPPDATA%` 和 `%ProgramData%` 两处的 `Microsoft\Windows Terminal\Fragments\<应用>\*.json`；坏掉的片段只记日志，不影响其他配置。
- **`settings.json` 写到一半**（解析失败）时保留上一次读到的配置，签名照样更新，下次保存会重新读取。
- **内置配置的命令行**：真机上 Windows PowerShell 和命令提示符这两个由 Windows Terminal 自己生成的配置在 `settings.json` 里只有 `guid`/`name`/`hidden`，没有 `commandline`。按它们固定的 GUID 补上 `%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe` 和 `%SystemRoot%\System32\cmd.exe`，这样副标题和"复制命令行"才有内容。其他动态配置（PowerShell 7、VS 开发者命令行、Azure 等）没有命令行，只是不提供"复制命令行"。
- **图标**：支持文件路径（`.png/.jpg/.jpeg/.gif/.bmp/.ico/.svg`），展开 `%VAR%`，相对路径相对于所在文件（片段的图标相对于片段目录）；文件不存在、emoji、`ms-appx://` 等 URL 都退回内置 `terminal` 图标。条目同时设置 `Icon.Path` 和 `Icon.Name`，界面加载不了图片时（比如部分 `.ico`）也会显示内置图标。
- **同名配置**：真实环境里可能有多个同名的配置（不同 GUID），比如同一工具的几个开发者命令行。`wt -p <名称>` 只会打开第一个，所以名称重复时改用 `wt -p {guid}`。
- **wt 参数**：新窗口 `-w new -p <名称>`，新标签页 `-w 0 nt -p <名称>`，当前文件夹再加 `-d <目录>`（同时设置工作目录）。所有参数经过 `cmdline.JoinWT`：含空格或引号时加引号，结尾的反斜杠加倍（`"C:\My Dir\\"`），`;` 转义为 `\;`。管理员身份用 `Verb: "runas"`，动作是 `StyleDestructive`，由宿主先确认。
- **发行版与配置去重**：发行版条目的"打开"会使用同名的 Windows Terminal 配置（优先 `Windows.Terminal.Wsl` 生成的那个；同名多个时用 GUID），此时还提供"在新标签页中打开"（`Ctrl+Enter`）。同名、并且带 `source`（WSL 生成器或发行版自己的应用包，如 `CanonicalGroupLimited.Ubuntu_…`）的配置不再单独列出，避免同一个发行版出现两次；用户手写的同名配置照常列出。
- **没有对应配置时**用 `wsl.exe -d <名称> --cd ~` 打开（设计里是 `wsl.exe -d <名称>`；不加 `--cd ~` 会停在启动器自己的工作目录对应的 `/mnt/...`）。
- **发行版其他动作**：在当前文件夹打开 `wsl.exe -d <名称> --cd "<目录>"`；在资源管理器中打开 `\\wsl.localhost\<名称>`（`Ctrl+Shift+F`）；设为默认（默认发行版不显示）；停止（`Ctrl+X`，需要确认；已知是停止状态时不显示，状态未知时显示）；另加"复制名称"（`Ctrl+Shift+C`）。详情面板显示 `BasePath`。正在运行的发行版图标为绿色。
- **注册表**：跳过没有 `DistributionName` 或 `State` 不为 1（正在安装或卸载）的项；没有 `Version` 值的老发行版按 WSL 1 处理；`BasePath` 去掉 `\\?\` 前缀。
- **运行状态**：只在有发行版时执行 `wsl.exe --list --verbose`（5 秒超时，不弹窗口），结果缓存 5 秒；解析时把最后一列不是数字的行当作表头或提示信息跳过，所以表头语言无关。"运行中/已停止"按已知的文字归类（英文、简繁中文、德文、法文、日文、韩文），未知的状态文字原样显示。没有列出的发行版不显示状态。
- **刷新**：`window-shown` 时检查 `settings.json` 和所有片段的修改时间和大小（签名变了才重新解析）、重新读注册表（很快）、必要时重新列运行状态；任何一项变了就 `Invalidate("")`。后台工作都用 `Host.Go`，`Close` 会取消并等待。`RootItems` 只读内存里的快照，不做任何 I/O。
- **WSL 命令**：设为默认 `wsl.exe -s <名称>`、停止 `wsl.exe -t <名称>`、Shut Down WSL `wsl.exe --shutdown` 都是回调，经由注入的 `Run` 执行，成功后立即刷新状态并提示；失败时提示里显示 wsl.exe 输出的第一行（已从 UTF-16LE 解码）。Update WSL 是 `ActionRunProcess`（队列 `wsl`，总是通知），由宿主作为后台任务执行，glctl 的假任务执行器只记录。
- **全局命令做成根搜索条目**而不是清单里的 `Commands`，因为它们只在装有 WSL 时才出现；代价是不能在设置里给它们设别名和全局快捷键。
- **新增图标**（`internal/ui/icons.go`）：`square-terminal`（发行版）、`circle-stop`（关闭 WSL）、`circle-arrow-down`（更新 WSL）。

### 4.3 测试

- `settings_test.go`：JSONC（注释、末尾逗号、字符串里的 `//` 和 `/*`、转义、未闭合的块注释）；glctl fixture 的完整读取；三个位置及其优先级；`profiles.defaults`、片段 `updates`、片段补全存根、隐藏的片段配置、坏掉的片段、各种图标；签名能发现修改时间变化和新增片段；内置配置的命令行。
- `wsl_test.go`：`testdata/` 下 UTF-16LE 的 `wsl -l -v` 输出（英文表头；中文表头 `名称 状态 版本` 和 `正在运行`/`已停止`/未知状态 `正在转换`；中文的"没有已安装的分发"提示），带 BOM、UTF-8 两种变体。
- `terminal_test.go`：用假的执行器（只记录命令行，`--list --verbose` 返回 fixture），验证不可用、只有一半、各个动作拼出的参数（空格、引号、`;`、结尾反斜杠、`--cd`、`runas`、同名配置用 GUID）、回调执行的命令和提示（包括 UTF-16LE 的错误信息）、窗口显示时的刷新（5 秒缓存、修改时间、写坏的配置、注册表新增发行版）和后台刷新。
- `internal/cli/terminal_test.go`：golden `query_terminal_wsl`、`query_terminal_profile`（带 `--context-folder`），以及会话里确认后关闭 WSL、把 Update WSL 作为后台任务记录下来。`builtin.Fake` 用 `terminal.FixtureOptions(dir)`，fixture 在 `testdata/fixtures/terminal/`；没有这个目录时插件不可用，所以不带 `--fixtures` 的 golden 不受影响。`plugin_list` 和 `query_pinyin`（"wx" 会通过副标题匹配到几个配置）两个 golden 因此更新。
- **真机只读测试** `TestRealMachine`：设置 `GOLAUNCHER_TERMINAL_TESTS=1` 才运行；执行器只允许 `wsl.exe --list --verbose`，其他命令直接判失败，不打开、不停止任何东西。

### 4.4 真机只读测试的结果

（按"不把本机真实数据写进公开仓库"的规则，只记录结论，不列具体的配置和发行版名称。）

- 商店版 `settings.json` 能找到并完整解析；`wt.exe` 是 `%LOCALAPPDATA%\Microsoft\WindowsApps` 下的应用执行别名。
- 真实的配置里有三种情况，测试前没有预料到，都已经处理：
  - Windows Terminal 自己生成的两个内置配置没有 `commandline`（见 4.2 的补全）；
  - 存在同名的配置（见 4.2，按 GUID 打开）；
  - 发行版的应用包和 WSL 生成器各写了一个同名配置（见 4.2 的去重）。
- 第三方程序写入的配置带 `.ico` 图标，路径能正确解析。
- 注册表里的发行版和 `wsl --list --verbose` 的运行状态（英文表头、UTF-16LE）都解析正确。
