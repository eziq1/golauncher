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
