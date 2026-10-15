<p align="center">
  <img src="assets/icon.svg" width="128" height="128" alt="golauncher 图标">
</p>

<h1 align="center">golauncher</h1>

<p align="center">
  类 Raycast 的 Windows 启动器，用 Go 和 <a href="https://github.com/egoist/mygo">MyGo</a> 原生 UI 编写。
  <br>
  <a href="README.md">English</a>
</p>

- 在任意位置按 **Alt+Space** 呼出，可以搜索应用、命令、文件，也可以直接算数。
- 一切皆插件：内置功能和第三方插件用的是同一套[插件 SDK](pkg/plugin/)。
- **环境感知**：在资源管理器或桌面上呼出时，列表最上方会针对当前目录和选中的文件给出操作：在此处打开终端或 VS Code、新建文件夹或文本文件、复制路径、压缩为 ZIP、复制 SHA-256、用 Everything 搜索此目录。支持 Windows 11 的多标签页，也支持标题里带路径的其他窗口。获取到的信息不落盘，可以在设置里关闭。
- **内置插件**
  - **应用启动器**：开始菜单里的应用，包括商店应用；支持拼音搜索，`wx` 能找到微信。
  - **计算器**：精确的十进制运算，支持函数和进制转换，比如 `255 in hex`；还能做**单位换算**：`10 km in mi`、`30 ℃`、`5 GB / 100 Mbps`、`3斤换成kg`、`8 L/100km in mpg`、`16 px in rem`，汇率（`100 usd in cny`，免费接口、不需要 API Key，离线时用缓存）、时间戳（`1700000000 to date`、`now in unix`）、颜色（`#ff8800 to hsl`）和鞋码衣服尺码（`鞋码 42 in us`）。单位可以用英文、中文或拼音（`10 公里 换成 英里`、`3 jin to kg`）。
  - **剪贴板历史**（`Alt+Shift+V`）：记录文字、图片和文件，支持全文搜索；不记录密码管理器复制的内容。
  - **文件操作**：上面说的环境感知操作（`builtin.files`）。
  - **文件搜索**：基于 [Everything](https://www.voidtools.com/)，需要系统里已经安装 Everything。
  - **Scoop**：搜索、安装、升级、卸载 [Scoop](https://scoop.sh) 软件包，需要系统里已经安装 Scoop。
  - **系统命令与设置**：锁屏、睡眠、休眠、重启、关机、注销、清空回收站、切换深浅色、调节音量、关闭显示器、显示桌面；还能直达约 150 个 Windows 设置页和设备管理器、服务、注册表编辑器等经典工具。支持中英文和拼音搜索，比如 `蓝牙`、`wifi`、`关机`、`gj`。重启、关机、注销和清空回收站会先确认。
  - **世界时钟**：输入 `time` 查看本地时间和主要城市的时间；`time tokyo`、`time bj`、`time 北京`、`time utc+8`、`time pst` 可以搜索城市。时间实时跳动，收藏的城市排在前面。
- **设置窗口**（`Ctrl+,`）：快捷键、别名、插件偏好、主题。
- **`glctl`**：无头 CLI，驱动的是同一个引擎，你或 AI Agent 不打开界面也能测试所有交互流程。

> **状态**：v0.2 是早期公开版本。界面暂时只有英文，但所有文字都经过 I18N 管线。架构和路线图见[设计文档](docs/design.md)。

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

会碰真实剪贴板、资源管理器窗口或正在运行的 Everything 的测试默认跳过，分别设置 `GOLAUNCHER_CLIPBOARD_TESTS=1`、`GOLAUNCHER_CONTEXT_TESTS=1`、`GOLAUNCHER_EVERYTHING_TESTS=1` 才运行。端到端测试通过本地控制通道驱动正在运行的 GUI，这个通道只有在启动 golauncher 时设置了 `GOLAUNCHER_CONTROL=1` 才开启。

### 调试界面

正式发布版（`scripts/build.ps1` 和 GitHub Releases）不包含 MyGo 的开发者工具。要看元素和性能面板，需要构建一个开发版：不注入版本号，版本就是 `dev`，开发者工具默认开启，也不会弹出安装界面。

```powershell
go build -ldflags "-H windowsgui" -o golauncher-dev.exe ./cmd/golauncher
.\golauncher.exe --quit   # golauncher 只允许运行一个实例
.\golauncher-dev.exe
```

然后在启动器或设置窗口里按 **F12** 或 **Ctrl+Shift+I** 打开开发者工具，**Ctrl+Shift+C** 用来点选元素。

另外两个开关：

- 注入了版本号、但构建时没加 `mygo_noinspector` 的版本，设置 `GOLAUNCHER_DEVTOOLS=1` 也能打开开发者工具；
- `MYGO_FRAME_STATS=1` 不打开面板，只把慢帧记进日志。
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

## Scoop

装有 [Scoop](https://scoop.sh) 时（`$SCOOP`、`%USERPROFILE%\scoop`，或在插件设置里指定的目录），会出现以下命令：

| 命令 | 作用 |
|------|------|
| Search Scoop Packages | 边输入边按名称、可执行文件名、简介搜索所有 bucket 的清单。`Enter` 安装；已安装的软件可以升级、锁定/解锁、打开目录、卸载（`Ctrl+X`） |
| Installed Scoop Apps | 已安装软件的版本、来源 bucket、是否锁定；有新版本的排在前面 |
| Scoop Updates | 本地计算的 `scoop status`，可以"全部升级" |
| Update All Scoop Apps / Update Scoop Buckets | `scoop update` 和 `scoop update *` |
| Scoop Buckets | 已添加的和推荐的 bucket，可以添加、删除，也可以按 URL 添加自定义 bucket |
| Clean Up Scoop | 先显示旧版本和下载缓存占了多少空间，再执行 `scoop cleanup *` 和 `scoop cache rm *` |

搜索直接读 Scoop 的本地文件（约 6000 个清单，每次搜索不到 1 毫秒），不启动 PowerShell。安装、升级等操作调用 `scoop` 命令行，作为后台任务一次执行一个：启动器不用等，按 `Ctrl+J`（或点底栏的转圈图标）打开 Background Tasks 查看实时输出，可以停止、复制输出，结束时会弹出通知。打开"Search packages from the main search"后，可以在主搜索里输入 `scoop <名称>` 或 `sc <名称>` 直接搜索。全局安装（`-g`）的软件只读显示。

## 编写插件

见[插件开发指南](docs/plugin-guide.md)（英文）。

## 许可证

[MIT](LICENSE)
