<p align="center">
  <img src="assets/icon.svg" width="128" height="128" alt="golauncher 图标">
</p>

<h1 align="center">golauncher</h1>

<p align="center">
  类 Raycast 的 Windows 和 macOS 启动器，用 Go 和 <a href="https://github.com/egoist/mygo">MyGo</a> 原生 UI 编写。
  <br>
  <a href="README.md">English</a>
</p>

<p align="center">
  <img src="assets/demo/ai-memory.gif" width="800" alt="问 AI 哪个应用占用内存最多：它通过启动器的工具打开进程列表并回答">
  <br>
  <em>问 AI 当前哪个应用最占内存：AI 通过启动器自己的工具读取进程列表，分析后给出答案</em>
</p>

<table>
  <tr>
    <td width="50%" align="center">
      <img src="assets/demo/ai-updates.gif" alt="问 AI 有哪些软件可以升级：它查询 WinGet 和 Scoop 并列出可升级的软件">
      <br><em>问 AI 有哪些软件可以升级（WinGet 和 Scoop）</em>
    </td>
    <td width="50%" align="center">
      <img src="assets/demo/currency.gif" alt="计算器里的汇率换算">
      <br><em>汇率换算</em>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <img src="assets/demo/translate.gif" alt="用 tr 边输边翻译">
      <br><em>用 <code>tr</code> 边输边翻译</em>
    </td>
    <td width="50%" align="center">
      <img src="assets/demo/context-aware.gif" alt="从资源管理器呼出启动器，列出针对所选文件和文件夹的操作">
      <br><em>情境感知：从资源管理器呼出，直接给出针对所选文件的操作</em>
    </td>
  </tr>
</table>

- 在任意位置按 **Alt+Space**（Mac 上是 **⌥Space**）呼出，可以搜索应用、命令、文件，也可以直接算数。macOS 上所有快捷键里的 Ctrl 都换成 ⌘。
- 一切皆插件：内置功能和第三方插件用的是同一套[插件 SDK](pkg/plugin/)。
- **环境感知**：在资源管理器、访达或桌面上呼出时，列表最上方会针对当前目录和选中的文件给出操作：在此处打开终端或 VS Code、新建文件夹或文本文件、复制路径、压缩为 ZIP、复制 SHA-256、用 Everything 或聚焦搜索此目录。支持 Windows 11 的多标签页，也支持标题里带路径的其他窗口（Mac 上是带文档路径的窗口）。获取到的信息不落盘，可以在设置里关闭（[docs/context-awareness.md](docs/context-awareness.md)）。
- **选中的文字**：在任意应用里选中一段文字再按 Alt+Space，列表最上方会针对它给出操作：翻译、问 AI（解释、总结、改写、回复建议，或者直接输入要求）、网页搜索、是网址就打开、是算式就计算，还能查看全文和字数。默认通过 UI Automation（Windows）或辅助功能接口（macOS）读取，不碰剪贴板；应用不支持时可以在设置里打开“读不到时改用复制的方式”（模拟 Ctrl+C 或 ⌘C 后立即还原剪贴板）。文字不会保存，只有你选择 AI 操作时才会发给 AI（[docs/selected-text.md](docs/selected-text.md)）。
- **主界面接收文件**：把文件拖到启动器上，按 `Ctrl+V` 粘贴复制的文件或图片，或者按 `Ctrl+O`、搜索“选择文件…”来选择文件。文件显示成一排卡片，列表最上方的“收到的文件”分组列出能做的事：问 AI、打开、打开方式…（Windows）、在资源管理器中显示或在访达中显示、复制路径或名称、压缩为 ZIP、复制 SHA-256 校验和、复制图片。输入文字可以筛选这些动作。卡片上的 × 移除一个文件，`Esc` 移除全部。除非你选“问 AI”，文件不会被记录，也不会给 AI 看（[docs/root-files.md](docs/root-files.md)）。
- **内置插件**
  - **应用启动器**：Windows 上是开始菜单里的应用，包括商店应用；macOS 上是“应用程序”和系统文件夹里的应用，显示本地化名称。支持拼音搜索，`wx` 能找到微信（[docs/app-names.md](docs/app-names.md)）。
  - **计算器**：精确的十进制运算，支持函数和进制转换，比如 `255 in hex`；还能做**单位换算**：`10 km in mi`、`30 ℃`、`5 GB / 100 Mbps`、`3斤换成kg`、`8 L/100km in mpg`、`16 px in rem`，汇率（`100 usd in cny`，免费接口、不需要 API Key，离线时用缓存）、时间戳（`1700000000 to date`、`now in unix`）、颜色（`#ff8800 to hsl`）和鞋码衣服尺码（`鞋码 42 in us`）。单位可以用英文、中文或拼音（`10 公里 换成 英里`、`3 jin to kg`）（[docs/unit-conversion.md](docs/unit-conversion.md)）。
  - **剪贴板历史**（`Alt+Shift+V`，Mac 上 `⌥⇧V`）：记录文字、图片和文件，支持全文搜索；不记录密码管理器复制的内容。
  - **文件操作**：上面说的环境感知操作（`builtin.files`）。
  - **文件搜索**：Windows 上基于 [Everything](https://www.voidtools.com/)，需要系统里已经安装 Everything；macOS 上基于聚焦（Spotlight）（[docs/everything.md](docs/everything.md)）。
  - **Scoop**（Windows）：搜索、安装、升级、卸载 [Scoop](https://scoop.sh) 软件包，需要系统里已经安装 Scoop（[下文](#scoop)、[docs/scoop.md](docs/scoop.md)）。
  - **WinGet**（Windows）：用 Windows 程序包管理器（`winget`）搜索、安装、升级、卸载软件包，需要系统里已经安装 winget（[下文](#winget)、[docs/winget.md](docs/winget.md)）。
  - **Homebrew**（macOS）：搜索 formula 和 cask，安装、升级、卸载，查看已安装和可升级的软件包，需要系统里已经安装 [Homebrew](https://brew.sh)（[docs/homebrew.md](docs/homebrew.md)）。
  - **系统命令与设置**：锁屏、睡眠、重启、关机、注销、清空回收站或废纸篓、切换深浅色、调节音量、关闭显示器（Windows 上还有休眠、显示桌面）；Windows 上能直达约 150 个设置页和设备管理器、服务、注册表编辑器等经典工具，macOS 上能直达系统设置的各个面板和实用工具。支持中英文和拼音搜索，比如 `蓝牙`、`wifi`、`关机`、`gj`。重启、关机、注销和清空回收站会先确认（[docs/system-commands.md](docs/system-commands.md)）。
  - **终端**：Windows 上列出 Windows Terminal 的配置（包括 Git Bash 等动态配置片段）和 WSL 发行版及其运行状态；可以在新窗口或新标签页中打开、以管理员身份打开、在当前文件夹打开，设置默认发行版、停止发行版、关闭或更新 WSL。macOS 上支持 Terminal、iTerm2、Ghostty、WezTerm、Alacritty、kitty、Warp：新窗口或新标签页、在当前文件夹打开、运行命令，可以选默认终端（[docs/terminal-wsl.md](docs/terminal-wsl.md)）。
  - **世界时钟**：输入 `time` 查看本地时间和主要城市的时间；`time tokyo`、`time bj`、`time 北京`、`time utc+8`、`time pst` 可以搜索城市，共约 3.2 万个（GeoNames 中人口 1.5 万以上的所有城市）。时间实时跳动，收藏的城市排在前面（[docs/world-clock.md](docs/world-clock.md)）。
  - **计时器与提醒**：`timer 25m`、`timer 1h30m 喝水`、`计时 半小时` 开始倒计时；`remind 15:30 开会`、`提醒 明天 9点 交周报`、`提醒 20分钟后 喝水` 设置提醒。输入 `timer` 列出正在运行的计时器和剩余时间（暂停、继续、加 5 分钟、重新开始、取消后可恢复），到点发通知。计时器在重启后保留，golauncher 没运行时错过的提醒在启动时补发（[docs/timers.md](docs/timers.md)）。
  - **快速链接与网页搜索**：`g golang generics`、`bd 天气`、`gh`、`yt`、`wiki`、`zhihu`、`bili` 等 20 个内置搜索引擎的关键词直接搜索网页（可以添加自己的搜索引擎、修改关键词）；输入网址（`github.com/foo`、`localhost:3000`）直接打开；没有匹配结果时提供“用 必应 搜索“…””（英文界面是 Google）。快速链接是你自己命名的网站、文件、文件夹和程序，支持 `{query}`、`{clipboard}`、`{date}` 等占位符，用表单创建，可以导入导出 JSON。搜索建议默认关闭。Google 翻译网页搜索的关键词是 `gt`（`tr` 归翻译插件）（[docs/quicklinks.md](docs/quicklinks.md)）。
  - **翻译**：在 `tr`、`fy` 或 `翻译` 后面输入文字，边输边出译文，每个引擎一行。不填 Key 时用 Google 翻译网页接口（非官方接口），失败时改用 MyMemory；填了你自己的 Key 后，DeepL、Azure AI 翻译、有道、百度排在前面。单个词还会显示词典；译文可以朗读，也可以交给 AI 对话（“用 AI 翻译”）。可以用 HTTP 模板添加自己的引擎，或者通过 Ollama 离线翻译（[下文](#翻译)、[docs/translator.md](docs/translator.md)）。
  - **网络工具**：`ip` 查看各网卡的地址、网关、DNS、Wi-Fi 名称和信号，以及公网 IP；`ping <主机>` 实时显示每次回复和统计；`dns <域名> [@服务器]` 查询 DNS 记录；`port 8080` 查看哪个进程占用端口（可以结束进程），`port 主机:443` 检测远程端口能否连通；`wifi` 复制已保存 Wi-Fi 的密码；`tracert <主机>`、刷新 DNS 缓存、续租 IP（仅 Windows）在后台执行（[docs/network-tools.md](docs/network-tools.md)）。
  - **进程管理**：输入 `ps `（带空格）、`kill` 或 `进程` 列出正在运行的应用和进程，按应用分组，CPU 和内存用条形图实时显示；可以按名称、窗口标题、拼音或 PID 搜索。Enter 切换到窗口；“结束任务”请应用自己关闭，“强制结束”和“结束进程树”会先确认；需要管理员权限的进程通过一次 UAC 提权结束（macOS 上是输入管理员密码）。系统关键进程和 golauncher 本身不能结束。“退出 <应用>”作用于呼出前的前台应用，它未响应时提供“强制结束”（[docs/process-manager.md](docs/process-manager.md)）。
  - **AI 对话**（默认关闭，需要你打开）：输入问题后按 `Tab`，或在没有匹配结果时选“问 AI”，就能通过本机安装的 [Claude Code](https://claude.com/product/claude-code) 命令行和你的 Claude 订阅与 Claude 对话（不需要 API Key）。回答实时显示，可以追问，按 `Esc` 停止，复制或粘贴回答，在 Claude 或 ChatGPT 应用中继续；“AI 对话历史”可以打开以前的对话（默认保留 30 天）。Claude 可以使用启动器的插件（“8080 端口被谁占了”“用 winget 装 7-Zip”）：它通过 golauncher 自己的工具搜索和运行插件，每一步都显示在对话里；会改动东西的操作先在启动器里问你（“允许”“本次会话内都允许”“拒绝”，破坏性操作每次都问）。联网搜索可选。在 设置 › 插件 › AI › 后端 里还可以改用 **Codex**（用你的 ChatGPT 账号；golauncher 运行原生的 Codex 程序，比如 Codex 应用、Homebrew 或 npm 装的那个）或 **Gemini CLI**（用你的 Google 账号；Gemini CLI 本身需要 Node.js）。它们都和你自己对这个 CLI 的配置隔离（不加载别的 MCP 服务器、钩子、扩展和记忆文件，没有读写文件和运行命令的工具）；每个对话固定使用开始时的后端；Gemini 请求使用工具的授权时，在同一个确认框里回答。设置 › 插件 › AI 的“后端”一节列出每个后端是否可用、实际运行的是哪个程序（路径、版本、在哪里找到的）以及找到的其他安装；装了不止一份时可以选用哪一份，或者填写路径。复制回答、到别处粘贴后再打开启动器，会回到原来的对话，没发出去的文字也还在（默认 10 分钟内；设置 › 通用 › 回到主界面；[docs/keep-view.md](docs/keep-view.md)）。**消息可以带图片和文本文件**：把文件拖到对话里，在对话里按 `Ctrl+V` 粘贴图片或复制的文件（从浏览器或聊天软件复制的图片也可以），或用“添加附件…”（`Ctrl+O`）选择文件。在主界面收到文件后，选“问 AI”会打开一个带着这些文件的新对话。图片（PNG、JPEG、GIF、WebP；BMP、TIFF 会转换）缩小到最长边 2000 像素以内，文本文件（最大 200 KB）直接放进消息；一条消息最多 10 个附件。PDF、Office 文档、视频和音频会标成不支持，移除之前不能发送（[docs/ai.md](docs/ai.md)）。
  - **在 Claude 桌面版或 Claude Code 里使用 golauncher**（默认关闭，在“设置 › 插件 › AI › 允许 AI 应用使用 golauncher”打开）：`golauncher mcp` 是一个提供你的插件的 MCP 服务器。设置页可以一键连接 Claude 桌面版（先备份，只改 `golauncher` 这一项），并给出添加到 Claude Code 的 `claude mcp add` 命令。哪些插件允许 AI 使用由你决定（剪贴板历史和第三方插件默认关闭）；“AI 活动记录”列出最近 30 天的每一次工具调用（[docs/ai.md](docs/ai.md)）。
- **设置窗口**（`Ctrl+,`，Mac 上 `⌘,`）：快捷键、别名、插件偏好、外观、语言；macOS 上还能在“设置 › 权限”查看 golauncher 拿到了哪些权限。
- **主题**（“设置 › 外观”，[docs/themes.md](docs/themes.md)）：18 个内置主题（golauncher 浅色/深色、绯红、GitHub、Nord、Dracula、Catppuccin、Tokyo Night、Solarized、Gruvbox、One Dark、宣纸和两个高对比度主题），画廊里是实时绘制的缩略图；跟随系统时可以分别选浅色和深色主题；Windows 上背景不透明度滑块让亚克力或云母材质透出来，文字始终清晰（关闭透明效果或开启对比度主题时自动变为纯色）；强调色可以用主题的、系统的或自己选；还能调圆角、密度和字号。自己的主题是 `%APPDATA%\golauncher\themes`（macOS 上是 `~/Library/Application Support/golauncher/themes`） 里的 JSON 文件，可以 `extends` 别的主题，保存后立即生效；`glctl theme validate` 检查格式和对比度。
- **中英文界面**：默认跟随系统的显示语言（zh-CN、zh-Hans、zh-SG 显示简体中文，其他显示英文）。要切换语言，打开“设置 › 通用 › 语言”，选择“跟随系统”“English”或“简体中文”，立即生效，不需要重启。在中文界面里，命令的英文名称和关键词照样能搜到，也支持拼音和拼音首字母。
- **`glctl`**：无头 CLI，驱动的是同一个引擎，你或 AI Agent 不打开界面也能测试所有交互流程。

> **状态**：v0.9 是支持 Windows 和 macOS 的早期公开版本，每个版本新增了什么见[更新日志](CHANGELOG.md)。架构和路线图见[设计文档](docs/design.md)。

## 安装

### Windows

1. 从 [Releases](https://github.com/eziq1/golauncher/releases) 下载 `golauncher_<版本>_windows_<架构>.zip`。
2. 解压后运行 `golauncher.exe`。第一次运行会弹出安装界面，默认安装到 `%LOCALAPPDATA%\Programs\golauncher`（不需要管理员权限），并创建开始菜单项、在"设置 › 应用"里注册卸载入口。之后运行更新或更旧的版本，会提示升级或降级；版本相同则直接启动已安装的程序。也可以选择"不安装，直接运行"（或加 `--portable` 参数）。程序常驻在系统托盘。
3. **自动更新**：已安装的副本在启动 30 秒后、之后每天（或每周）查询 GitHub Releases 的最新正式版，下载对应架构的 zip，用 Release 里的 SHA-256 校验和校验，在 golauncher 空闲时（窗口隐藏、没有后台任务和 AI 回答、几分钟内没用过）安装并自动重启，然后通知“已更新”。新版本起不来就自动恢复旧版本，并跳过这个版本。“设置 › 关于”里可以查看状态、立即检查、跳过某个版本，或改成“只通知我”“关闭”；托盘菜单和“检查更新”命令也能检查。便携运行和开发构建不会自动更新。详见 [docs/auto-update.md](docs/auto-update.md)。
4. **winget**（软件包被 winget 社区仓库接受之后）：`winget install eziq1.golauncher`，以后用 `winget upgrade eziq1.golauncher` 升级。软件包正在审核：PR [microsoft/winget-pkgs#454732](https://github.com/microsoft/winget-pkgs/pull/454732) 已经通过自动校验，在等人工审核。升级前请先退出 golauncher。从 0.9.0 起，用 winget 安装的副本不弹安装界面，也不自己更新，交给 winget（“设置 › 关于”里有说明）。详见 [docs/winget-packaging.md](docs/winget-packaging.md)。

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
| 日志 | `%LOCALAPPDATA%\golauncher\logs\golauncher.log`（更新：`update.log`） |
| 更新（下载、上一个版本） | `%LOCALAPPDATA%\golauncher\update\` |

### macOS

1. 从 [Releases](https://github.com/eziq1/golauncher/releases) 下载 `golauncher_<版本>_darwin_arm64.zip`（Apple 芯片）或 `golauncher_<版本>_darwin_amd64.zip`（Intel），解压。
2. 应用还没有经过公证，第一次打开时按住 Control 点按 `golauncher.app` › “打开”（macOS 15：先打开一次，再到“系统设置 › 隐私与安全性”点“仍要打开”），或者执行 `xattr -dr com.apple.quarantine golauncher.app`。第一次运行会提示安装到“应用程序”文件夹（没有权限时装到 `~/Applications`）。程序常驻在菜单栏，没有 Dock 图标，按 **⌥Space** 呼出。
3. “设置 › 权限”列出需要你允许的权限：**辅助功能**（粘贴、读取选中的文字）和**自动化 › 访达**（访达当前文件夹）。应用没有用 Developer ID 签名，所以每次更新后 macOS 会忘掉辅助功能权限，需要在列表里删掉 golauncher 再重新添加。自动更新和 Windows 一样。需要 macOS 13 或更高版本。详见 [docs/macos-port.md](docs/macos-port.md)。
4. **Homebrew**：`brew install --cask eziq1/tap/golauncher`，以后用 `brew upgrade --cask golauncher` 升级（升级后退出并重新打开 golauncher）。cask 在 tap 仓库 [eziq1/homebrew-tap](https://github.com/eziq1/homebrew-tap) 里。它把 golauncher.app 装到“应用程序”文件夹，并把 `glctl` 链接到 Homebrew 的 `bin`。应用只有 ad hoc 签名，没有经过公证，所以 macOS 会拦下第一次打开：先打开一次，再到“系统设置 › 隐私与安全性”点“仍要打开”。升级后可能还要再允许一次。从 0.9.0 起，用 Homebrew 安装的副本不弹安装界面，也不自己更新，交给 Homebrew（“设置 › 关于”里有说明）。如果 golauncher 已经自己安装到了“应用程序”文件夹，先退出并删掉那一份（配置和数据不受影响），或者在版本相同时用 `brew install --cask --adopt`。`brew uninstall --zap --cask golauncher` 会连配置和数据一起删掉。详见 [docs/homebrew-packaging.md](docs/homebrew-packaging.md)。

| 内容 | 位置 |
|------|------|
| 配置 | `~/Library/Application Support/golauncher/config.json` |
| 数据库、剪贴板历史 | `~/Library/Application Support/golauncher/data/` |
| 日志 | `~/Library/Caches/golauncher/logs/golauncher.log`（更新：`update.log`） |

## 快捷键

Mac 上用 ⌥ 代替 Alt、⌘ 代替 Ctrl（`⌥Space`、`⌘K`、`⌘,`）；`⌃N`、`⌃P` 也能移动选中项。

| 按键 | 作用 |
|------|------|
| `Alt+Space` | 呼出或隐藏（可修改） |
| `↑` `↓`、`Ctrl+N` `Ctrl+P` | 移动选中项 |
| `Enter` / `Ctrl+Enter` | 执行主动作 / 次动作 |
| `Ctrl+K` | 打开选中项的全部动作 |
| `Ctrl+O` | 主界面：选择要处理的文件。AI 对话：“添加附件…” |
| `Ctrl+V` | 主界面和 AI 对话：把复制的文件或图片作为文件粘贴进来（文字照常粘贴成文字） |
| `Esc` | 清空搜索 → 返回上一级 → 隐藏。主界面收到文件时：先清空文字，再清掉文件，然后隐藏 |
| `Ctrl+,` | 打开设置 |

选中项自己有 `Ctrl+O` 的动作时，以那个动作为准。

## 从源码构建

需要 Go 1.27 或更高版本，不需要 cgo。

Windows：

```powershell
git clone https://github.com/eziq1/golauncher
cd golauncher
pwsh scripts/windows/build.ps1          # 生成 dist/golauncher.exe 和 dist/glctl.exe
```

macOS：

```sh
git clone https://github.com/eziq1/golauncher
cd golauncher
scripts/macos/build.sh --app            # 生成 dist/golauncher.app（本机架构）
scripts/macos/build.sh --arch arm64 --app --zip   # Apple 芯片的发布包（Intel 用 amd64）
scripts/macos/build.sh --debug          # 不优化的 dist/golauncher 和 dist/glctl，供 Delve 调试
```

所有脚本见 [scripts/README.md](scripts/README.md)。

## 测试

```powershell
go test ./...                                  # 单元、集成、golden 和规范检查
go test -race ./internal/... ./plugins/...     # -race 需要 C 编译器
go test ./internal/cli -update                 # 输出格式有意变更后，更新 golden 文件
```

会碰真实剪贴板、资源管理器窗口或正在运行的 Everything 的测试默认跳过，分别设置 `GOLAUNCHER_CLIPBOARD_TESTS=1`、`GOLAUNCHER_CONTEXT_TESTS=1`、`GOLAUNCHER_EVERYTHING_TESTS=1` 才运行；读取真实控件选中文字的测试（只用测试自己创建的窗口）需要 `GOLAUNCHER_SELECTION_TESTS=1`。端到端测试通过本地控制通道驱动正在运行的 GUI，这个通道只有在启动 golauncher 时设置了 `GOLAUNCHER_CONTROL=1` 才开启。

### 调试界面

正式发布版（`scripts/windows/build.ps1` 和 GitHub Releases）不包含 MyGo 的开发者工具。要看元素和性能面板，需要构建一个开发版：不注入版本号，版本就是 `dev`，开发者工具默认开启，也不会弹出安装界面。

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
glctl theme validate my-theme.json --format text   # 检查主题文件和对比度
glctl attach type vsc           # 驱动以 GOLAUNCHER_CONTROL=1 启动的 GUI
glctl update check --current 0.4.0 --format text   # 对着假的 GitHub 检查
glctl update check --real       # 查询真实的 GitHub（只读）
glctl update download --dry-run # 下载并校验，不安装
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

## 翻译

输入 `tr`、`fy` 或 `翻译`，加一个空格，再输入文字。不输入前缀就不会发送任何文字。

| 输入 | 结果 |
|------|------|
| `tr hello world` | 译成界面语言（原文已经是界面语言时译成英文） |
| `tr ja 早上好`、`tr >ja …`、`tr 日 …` | 译成日语：语言代码、`>` 加代码，或者中文名称 |
| `tr en>zh apple` | 从英语译成中文 |
| `tr serendipity` | 单个词还会多一行“词典”（音标、词性、释义） |

`Enter` 复制译文，`Ctrl+Enter` 粘贴到前一个窗口。其他动作：“复制原文”（`Ctrl+Shift+C`）、“交换源语言和目标语言”、“朗读译文”和“朗读原文”（Windows 用 SAPI，macOS 用 `say`）、“用 AI 翻译”（打开 AI 对话），以及在 Google 翻译、DeepL、有道网页中打开。“翻译”命令以视图的形式打开同样的功能。

翻译引擎在翻译插件的设置里配置：

- 不需要 Key：Google 翻译网页接口（非官方接口，随时可能失效，中国大陆通常无法访问）。所有引擎都失败时改用 MyMemory。
- 填你自己的 Key：DeepL、Azure AI 翻译、有道、百度，填了就排在前面。Key 存在 Windows 凭据管理器或 macOS 钥匙串里。
- “自定义引擎”：每行一个 HTTP 模板（`名称 | GET 或 POST | 网址 | 译文的 JSON 路径 [| 请求体 [| 请求头]]`，可以用 `{text}`、`{from}`、`{to}`）。
- 通过 Ollama 用本地模型离线翻译。

有两个选项默认关闭：“选中文字后立即翻译”（在“选中的文字”分组里多一行译文，可以“替换选中的文字”）和“保存翻译历史”（只记录复制或粘贴过的译文，只存在本机；用“翻译历史”命令查看）。Google 翻译的网页搜索改用 `gt`。详见 [docs/translator.md](docs/translator.md)。

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

## WinGet

装有 Windows 程序包管理器（`winget`，随“应用安装程序”提供）时，会出现以下命令：

| 命令 | 作用 |
|------|------|
| Search WinGet Packages | 边输入边按名称、ID、别名、命令名、标签搜索 `winget` 源。`Enter` 安装；`Ctrl+D` 查看详情（发布者、简介、许可证、主页、发行说明）；已安装的软件包可以升级、固定/取消固定、卸载（`Ctrl+X`） |
| Installed WinGet Packages | `winget list` 列出的全部软件，包括不是用 winget 安装的（这些只能卸载）；有新版本的排在前面 |
| WinGet Updates | 有新版本的软件包，可以“全部升级”（跳过已固定版本的） |
| Upgrade All WinGet Packages | 确认后执行 `winget upgrade --all` |
| WinGet Sources / Update WinGet Sources | 已配置的源；`winget source update` |

搜索读 `winget` 源的本地索引（约 1.5 万个软件包，每次搜索几毫秒），输入时不会启动 winget。已安装的软件包来自 `winget list`，要几秒钟：它在后台运行，上次的结果会缓存下来，运行完自动刷新视图。安装、升级等操作作为后台任务一次执行一个，不会弹出提问（`--disable-interactivity`，自动接受协议）；需要管理员权限的安装程序会弹出 UAC，少数要求 winget 本身以管理员身份运行的软件包可以用“以管理员身份安装/升级”（`Ctrl+Shift+A`），在提升权限的控制台窗口里执行。在主搜索里输入 `winget <名称>` 或 `wg <名称>` 可以直接搜索。详见 [docs/winget.md](docs/winget.md)。

## 编写插件

见[插件开发指南](docs/plugin-guide.md)（英文）。

## 致谢

世界时钟的城市数据（人口 1.5 万以上的城市及其省/州、国家和中文名）来自 [GeoNames](https://www.geonames.org/)，按 [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/) 授权，由 [`scripts/data/gencities`](scripts/data/gencities/) 转换后嵌入程序。

## 许可证

[MIT](LICENSE)
