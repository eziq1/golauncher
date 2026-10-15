# golauncher

类 Raycast 的 Windows 启动器，用 Go 和 [MyGo](https://github.com/egoist/mygo) 原生 UI 编写。设计文档见 [docs/design.md](docs/design.md)。

- 全局快捷键 `Alt+Space` 呼出，可以搜索应用、命令、计算表达式。
- 内置插件：应用启动器、计算器、剪贴板历史（`Ctrl+Shift+V`）。
- 所有内置功能都通过 [pkg/plugin](pkg/plugin/) 插件 SDK 实现。
- 设置窗口：`Ctrl+,`，或者托盘菜单 › Settings…
- 第一版界面只有英文，但所有文字都走 I18N 管线。

## 环境要求

- Windows 10/11
- Go 1.27+
- 不需要 cgo，也不需要 WebView2。

## 构建与运行

```powershell
go build -ldflags "-H windowsgui" -o golauncher.exe ./cmd/golauncher
.\golauncher.exe            # 启动后常驻托盘；再次运行时可以带参数：
.\golauncher.exe --show     # 呼出启动器
.\golauncher.exe --settings # 打开设置
.\golauncher.exe --quit     # 退出正在运行的实例
```

数据位置：

| 内容 | 路径 |
|------|------|
| 配置 | `%APPDATA%\golauncher\config.json` |
| 数据库 | `%APPDATA%\golauncher\golauncher.db` |
| 日志 | `%LOCALAPPDATA%\golauncher\logs\golauncher.log` |

## 测试

```powershell
go test ./...                       # 全部单元、集成测试和 golden 测试
go test -race ./internal/... ./plugins/...
go test ./internal/cli -update      # 输出格式有意变更时，更新 golden 文件
```

端到端测试要先启动 GUI，然后运行：

```powershell
$env:GOLAUNCHER_E2E=1; go test ./internal/shell -run RunningInstance
```

## glctl：给 Agent 用的无头 CLI

```powershell
go build -o glctl.exe ./cmd/glctl
.\glctl help
.\glctl query vsc --fixtures testdata/fixtures --format text
.\glctl query "12*3+4"
.\glctl run builtin.clipboard/history --text 世界 --fixtures testdata/fixtures
.\glctl session new -s s.json --fixtures testdata/fixtures
.\glctl session type -s s.json "clipboard history"
.\glctl session key  -s s.json Enter
.\glctl i18n check
.\glctl attach type "vsc"           # 驱动正在运行的 GUI
```

`glctl` 默认在隔离的假环境中运行，不会碰真实配置、数据库和剪贴板，动作的副作用也只记录不执行。要用真实环境，必须显式加 `--real`；要真正执行动作，再加 `--exec`。

## 目录

| 路径 | 内容 |
|------|------|
| [pkg/plugin](pkg/plugin/) | 插件 SDK |
| [internal/core](internal/core/) | 引擎与交互状态机（与 UI 无关） |
| [internal/ui](internal/ui/) | 界面 |
| [internal/shell](internal/shell/) | MyGo 窗口、托盘、热键 |
| [internal/cli](internal/cli/) | glctl |
| [plugins/](plugins/) | 内置插件：apps、calc、clipboard |
| [internal/winapi](internal/winapi/) | Win32/COM 封装 |
