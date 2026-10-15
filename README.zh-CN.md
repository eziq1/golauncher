# golauncher

类 Raycast 的 Windows 启动器，用 Go 和 [MyGo](https://github.com/egoist/mygo) 原生 UI 编写。

[English](README.md)

- 在任意位置按 **Alt+Space** 呼出，可以搜索应用、命令、文件，也可以直接算数。
- 一切皆插件：内置功能和第三方插件用的是同一套[插件 SDK](pkg/plugin/)。
- **内置插件**
  - **应用启动器**：开始菜单里的应用，包括商店应用；支持拼音搜索，`wx` 能找到微信。
  - **计算器**：精确的十进制运算，支持函数和进制转换，比如 `255 in hex`。
  - **剪贴板历史**（`Alt+Shift+V`）：记录文字、图片和文件，支持全文搜索；不记录密码管理器复制的内容。
  - **文件搜索**：基于 [Everything](https://www.voidtools.com/)，需要系统里已经安装 Everything。
  - **世界时钟**：输入 `time` 查看本地时间和主要城市的时间；`time tokyo`、`time bj`、`time 北京`、`time utc+8`、`time pst` 可以搜索城市。时间实时跳动，收藏的城市排在前面。
- **设置窗口**（`Ctrl+,`）：快捷键、别名、插件偏好、主题。
- **`glctl`**：无头 CLI，驱动的是同一个引擎，你或 AI Agent 不打开界面也能测试所有交互流程。

> **状态**：v0.1 是首个公开预览版。界面暂时只有英文，但所有文字都经过 I18N 管线。架构和路线图见[设计文档](docs/design.md)。

## 安装

1. 从 [Releases](https://github.com/eziq1/golauncher/releases) 下载 `golauncher_<版本>_windows_<架构>.zip`。
2. 解压后运行 `golauncher.exe`。第一次运行会弹出安装界面，默认安装到 `%LOCALAPPDATA%\Programs\golauncher`（不需要管理员权限），并创建开始菜单项、在"设置 › 应用"里注册卸载入口。之后运行更新或更旧的版本，会提示升级或降级；版本相同则直接启动已安装的程序。也可以选择"不安装，直接运行"（或加 `--portable` 参数）。程序常驻在系统托盘。

需要 Windows 10 或 11，Acrylic 毛玻璃背景只在 Windows 11 上有。不需要 WebView2 运行时。

```powershell
golauncher.exe --show       # 呼出启动器
golauncher.exe --settings   # 打开设置
golauncher.exe --quit       # 退出正在运行的实例
golauncher.exe --portable   # 不安装，直接运行
golauncher.exe --uninstall  # 卸载
```

| 内容 | 位置 |
|------|------|
| 配置 | `%APPDATA%\golauncher\config.json` |
| 数据库、剪贴板历史 | `%LOCALAPPDATA%\golauncher\data\`（不会漫游同步） |
| 日志 | `%LOCALAPPDATA%\golauncher\logs\golauncher.log` |

## 快捷键

| 按键 | 作用 |
|------|------|
| `Alt+Space` | 呼出或隐藏（可修改） |
| `↑` `↓`、`Ctrl+N` `Ctrl+P` | 移动选中项 |
| `Enter` / `Ctrl+Enter` | 执行主动作 / 次动作 |
| `Ctrl+K` | 打开选中项的全部动作 |
| `Esc` | 清空搜索 → 返回上一级 → 隐藏 |
| `Ctrl+,` | 打开设置 |

## 从源码构建

需要 Go 1.27 或更高版本，不需要 cgo。

```powershell
git clone https://github.com/eziq1/golauncher
cd golauncher
pwsh scripts/build.ps1          # 生成 dist/golauncher.exe 和 dist/glctl.exe
```

## 测试

```powershell
go test ./...                                  # 单元、集成、golden 和规范检查
go test -race ./internal/... ./plugins/...     # -race 需要 C 编译器
go test ./internal/cli -update                 # 输出格式有意变更后，更新 golden 文件
```

会读写真实剪贴板的测试默认跳过，需要设置 `GOLAUNCHER_CLIPBOARD_TESTS=1` 才运行（运行完会恢复剪贴板）。端到端测试通过本地控制通道驱动正在运行的 GUI，这个通道只有在启动 golauncher 时设置了 `GOLAUNCHER_CONTROL=1` 才开启。

## glctl：无头 CLI

```powershell
glctl query vsc --fixtures testdata/fixtures --format text
glctl session new  -s s.json --fixtures testdata/fixtures
glctl session type -s s.json "clipboard history"
glctl session key  -s s.json Enter
glctl i18n check
glctl attach type vsc           # 驱动以 GOLAUNCHER_CONTROL=1 启动的 GUI
```

`glctl` 默认在隔离的内存环境中运行，时钟固定，动作只记录、不执行。要用真实的配置和数据，加 `--real`；要真正执行动作，再加 `--exec`。

## 脚本命令

带有 [Raycast Script Commands](https://github.com/raycast/script-commands) 元数据注释的脚本会自动成为命令。把 `.ps1`、`.py`、`.js`、`.bat`/`.cmd` 或 `.sh` 放进 `%APPDATA%\golauncher\scripts`（设置里可以再添加目录），或者运行 **Create Script Command** 从模板开始：

```powershell
# @raycast.schemaVersion 1
# @raycast.title Flush DNS
# @raycast.mode compact
# @raycast.argument1 { "type": "text", "placeholder": "host", "optional": true }
ipconfig /flushdns | Select-Object -Last 1
```

支持 `silent`、`compact`、`fullOutput` 三种模式（`inline` 按 `compact` 处理）。详见[插件开发指南](docs/plugin-guide.md#script-commands)。
## 编写插件

见[插件开发指南](docs/plugin-guide.md)（英文）。

## 许可证

[MIT](LICENSE)
