# 和 look 的功能对比

> 2026-10-18 · 对应需求 R104（见 [requirements.md](requirements.md)）· 资料来源：[kunkka19xx/look](https://github.com/kunkka19xx/look) 的 README 和 `docs/features.md`（v0.7.4，2026-10-16）

## 1. 概况

| | look | golauncher |
|---|---|---|
| 语言与界面 | Rust 核心；macOS 用 SwiftUI，Windows/Linux 用 gpui（GPU 绘制） | Go；MyGo 原生界面 |
| 平台 | macOS、Windows（只有 x64）、Linux | Windows（x64、arm64）、macOS（Intel、Apple Silicon） |
| 许可证 | GPLv3 | MIT |
| 定位 | "精选内置工具，不以插件为主"；插件机制只是远期方向 | 插件平台：内置插件 + SDK + 外部插件协议 + Script Commands |
| 签名 | macOS 已签名并公证 | macOS 只有 ad hoc 签名 |
| 安装 | Homebrew（官方 cask）、Nix、AUR、deb、AppImage、PowerShell 脚本、Scoop | zip、自安装、Homebrew（自己的 tap）、winget（审核中） |

## 2. look 有、golauncher 没有

| 功能 | look 的做法 | 价值 |
|---|---|---|
| **运行中应用切换栏** | 搜索栏右半边显示正在运行的应用图标，`Alt+1..9` 直接切换 | 高 |
| **Super actions 控制条** | 空输入时显示一排磁贴：蓝牙、Wi-Fi、电池、主题、防休眠、屏保、天气、麦克风、正在播放、重启、关机；`Alt+字母` 触发；布局用一个 TOML 文件"画"出来 | 中到高 |
| **文件多选和文件操作快捷键** | `Ctrl+P` 多选文件并放到剪贴板、`Ctrl+E` 用编辑器打开、`Ctrl+T` 在这里打开终端、`Ctrl+F` 在文件管理器中显示、`Cmd+D` 移到废纸篓；可声明首选的编辑器、终端、文件管理器 | 高 |
| **文件和文件夹预览面板** | 右侧预览文本、图片和文件夹内容（`layout=split`） | 中 |
| **自己的索引** | 应用、文件、文件夹建 SQLite 索引，文件系统事件增量更新；最近文件（`rc"`） | 中（golauncher 用 Everything / Spotlight） |
| **用户声明的数据源** | `~/.look/sources/*.toml`：目录、文件行、命令输出变成搜索结果，可以有子菜单、确认、预览、占位符，还能作为文件的附加动作 | 高 |
| **网页即时答案卡片** | DuckDuckGo、Wikipedia、天气、汇率、加密货币，边输边出 | 中 |
| **待办 todo** | 按日期分组，有完成率统计和热力图 | 中 |
| **网速测试 speed** | 下载、上传、延迟的动态仪表盘 | 低到中 |
| **番茄钟的完整界面** | 可编辑的番茄序列、三种表盘、背景音乐、菜单栏小计时器 | 中（golauncher 只有计时器） |
| **进程按端口查找** | `:3000` / `port 3000` 找到占用端口的进程 | 中（golauncher 网络工具有端口查询） |
| **macOS 上的 AI 助手** | 用自然语言加、改、取消日历事件和提醒（先预览再确认，`Cmd+Z` 撤销）；`join` 加入下一个会议；`call mom` 打电话或发消息；"remember …"记住偏好；本机 Apple Intelligence 或 Ollama | 中（只在 macOS 上） |
| **自然语言找文件** | "上周的 PDF"被模型转成结构化查询 | 中（只在 macOS 上） |
| **紧凑布局和动画开关** | `layout=compact`；可以关掉动画 | 低 |
| **从命令行直接进入某个模式** | `lookapp clipboard`、`lookapp calc 2+2`，可绑系统快捷键 | 中 |
| **Linux** | 支持 GNOME、KDE、i3、sway、Hyprland、niri | 不是 golauncher 的目标 |

## 3. golauncher 有、look 没有

- **插件平台**：SDK、外部插件协议、Script Commands（兼容 Raycast 格式）、每个插件的权限和设置页、每个命令单独的别名和快捷键。
- **AI 对话**：接入本机的 Claude Code、Codex、Gemini CLI，支持多轮对话、附件、历史，隐藏后回来还在原来的对话里；有 MCP 工具层，AI 可以调用启动器的动作（按风险分级确认）。look 的 AI 对话只在 macOS 上有。
- **选中文字的操作**：读取任意应用里选中的文字，然后翻译、解释、改写……（下一版可以自定义，R103）。
- **翻译插件**：多个引擎、词典、朗读、历史。look 有 `t"` 翻译和 `tw"` 词典，引擎不详。
- **主输入框接收文件**：拖入、粘贴、选择文件，列出能处理它们的动作。
- **包管理**：WinGet、Scoop、Homebrew 的搜索、安装、升级。
- **网络工具**：IP、ping、DNS、端口、路由跟踪、已保存的 Wi-Fi 密码、刷新 DNS。
- **系统命令和 Windows 管理工具直达**，以管理员身份运行，需要时弹出 UAC。
- **剪贴板历史**：更长的历史、搜索、图片、敏感内容不记录。look 默认只保留 10 条文本。
- **快速链接和网页搜索引擎**（可自定义）、**世界时钟**（全部城市）、**单位换算**、**计算器的汇率、颜色、时间戳**。
- **进程管理**：CPU 和内存条形图、结束进程树、切换到窗口。
- **主题系统**：内置主题和自定义主题，背景透明度。
- **自动更新**；glctl 无头 CLI；中英文界面。

## 4. 值得借鉴的

| 想法 | 建议 |
|---|---|
| 运行中应用切换栏 + `Alt+数字` | 和"窗口切换"一起做，作为外观设置里的可选项 |
| 文件多选、`Ctrl+E` / `Ctrl+T` / `Ctrl+F` 和首选工具 | 扩展 R97 的"收到的文件"和 Everything 插件的结果动作 |
| 用户声明的数据源（TOML） | 可以作为 Script Commands 的补充：声明式地把目录、文件、命令输出变成结果，比写插件门槛低 |
| 空输入时的控制条 | 可以做成"根视图小组件"：Wi-Fi、蓝牙、主题、防休眠、正在播放 |
| 命令行直接进入模式 | `golauncher --open <命令>`，和 Raycast 的深度链接一起做 |
| 网页即时答案 | 优先级低，AI 和快速链接已经覆盖大部分 |
