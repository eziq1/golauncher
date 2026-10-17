# 简体中文支持方案

> 状态：已实现 · 2026-10-15 · 对应需求 R35（见 [requirements.md](requirements.md)）
>
> 需求：应用已有完善的 I18N 支持，给它加上中文支持。

## 1. 范围

- **新增语言**：`zh-CN`（简体中文）。语言标签用 `zh-CN`；系统语言是 `zh-Hans`、`zh-SG` 时，也通过语言匹配落到 `zh-CN`。繁体中文不在这次范围内。
- **要翻译的消息目录**：
  - 核心：`internal/i18n/locales/en.json` → `zh-CN.json`，包括启动器、设置、托盘、通知、安装器、后台任务、环境感知。
  - 每个内置插件的 `locales/en.json` → `zh-CN.json`：apps、calc、clipboard、everything、scripts、scoop、clock、files、tasks，以及合并时已经存在的其他插件。
  - 示例外部插件（`examples/plugins/*/locales`），作为第三方插件做多语言的示范。
- **不翻译的内容**：
  - 数据本身：应用名、文件名、剪贴板内容；
  - 机器可读的输出：`glctl` 的 JSON 字段和枚举值；
  - 快捷键的键名，比如 `Ctrl`、`Enter`；
  - 日志。

## 2. 规则

1. **术语表**：在本文 §4 维护一份，所有目录统一使用。例如 Command → 命令，Action → 操作，Plugin → 插件，Preferences → 偏好设置，Background Tasks → 后台任务，Clipboard History → 剪贴板历史，Script Command → 脚本命令。
2. **格式**：
   - 中文没有单复数之分，复数消息只需要 `other`（`one` 可以省略，会回退到 `other`）；
   - 模板参数（`{{.name}}`、`{{.Count}}`）必须和英文一一对应，`glctl i18n check` 会检查；
   - 中英文之间、中文和数字之间加空格；
   - 用中文全角标点，引号用“”。
3. **日期和时间**：
   - `time.dateLayout` 等日期格式改为 `2006年1月2日`；
   - 相对时间写成"5 分钟前""刚刚"；
   - 世界时钟的星期、月份名称和日期格式本身就是消息，直接翻译。
4. **搜索关键词**：中文语言下，命令和设置项的关键词要加上中文和拼音缩写，英文关键词保留（宿主会同时匹配两种语言的关键词，见 design.md §10.3）。
5. **长度**：中文通常比英文短，一般不会撑破布局；`en-XA` 伪语言测试仍然负责检查布局。

## 3. 界面与验证

- **语言选择**：设置 › 通用的"语言"下拉框，在可用语言超过一种时自动出现（R3 的设计），选项是"跟随系统 / English / 简体中文"。切换后立即生效，不需要重启（§10.2 的机制）。
- **字体**：真机截图里中文文件夹名已经能正常显示，说明 MyGo 的字体回退可用；还要验证整个界面切到中文后，各个页面渲染都正常。
- **测试**：
  - `glctl i18n check` 对 `zh-CN` 不允许有缺失的 key，也不允许参数不一致；
  - 新增 `zh-CN` 的界面渲染截图测试（离屏），检查文字没有被截断或重叠；
  - 新增 `glctl --locale zh-CN` 的 golden（根列表、计算器、剪贴板视图）；
  - 新增"系统语言为中文时自动选中 zh-CN"的测试。
- **后续插件**：以后新增插件时，必须同时提交 `en.json` 和 `zh-CN.json`，写进插件开发指南的检查清单。正在开发的四个分支（单位换算，以及系统命令、终端与 WSL、网络工具）合并时要补上中文翻译。

## 4. 术语表

以后翻译新插件时以这张表为准。左边是英文原文（不区分大小写和单复数），右边是统一的译法。

### 4.1 通用

| English | 简体中文 | 说明 |
|---------|----------|------|
| Launcher | 启动器 | |
| Command | 命令 | |
| Action | 操作 | 操作面板 = Actions 面板 |
| Plugin | 插件 | External plugin → 外部插件 |
| Settings | 设置 | 窗口、托盘菜单 |
| Preferences | 偏好设置 | 插件的设置项 |
| General / Plugins / Advanced / About | 通用 / 插件 / 高级 / 关于 | 设置页导航 |
| Root search / main search | 主搜索 | |
| Main screen（"回到主界面"） | 主界面 | 作为"要回去的地方"时用主界面，如设置"回到主界面"、页脚提示（docs/keep-view.md） |
| Recent | 最近使用（主搜索）/ 最近（剪贴板） | |
| Alias | 别名 | |
| Hotkey / Shortcut | 快捷键 | 录制快捷键 = Record shortcut |
| System (theme, language) | 跟随系统 | |
| Light / Dark | 浅色 / 深色 | |
| Appearance | 外观 | 设置页 |
| Theme | 主题 | 内置主题的专有名称不翻译（Nord、Dracula……）；Paper → 宣纸，High Contrast → 高对比度 |
| Acrylic / Mica / Solid | 亚克力 / 云母 / 纯色 | 背景材质，与 Windows 设置的叫法一致 |
| Accent color | 强调色 | |
| Opacity | 不透明度 | 背景不透明度、窗口整体不透明度 |
| Language | 语言 | 选项用语言自己的名称：English、简体中文 |
| Context awareness | 情境感知 | |
| Selected Text | 选中的文字 | 根列表分组标题（docs/selected-text.md） |
| Read selected text | 读取选中的文字 | |
| Replace selection | 替换选中的文字 | |
| Fall back to copying | 读不到时改用复制的方式 | 设计稿里叫“剪贴板方式” || Launch at login | 登录时启动 | |
| Background Tasks | 后台任务 | Queued 排队中，Running 正在运行，Finished 已结束 |
| Exit code | 退出代码 | |
| Log / Log file | 日志 / 日志文件 | |
| Manifest | 清单 | 插件清单、Scoop 清单 |
| Permission | 权限 | |
| Toast | 提示 | 提示文字多用“已……”开头，如“已复制到剪贴板” |
| Clear / Delete / Remove | 清除 / 删除 / 移除 | 清除：清空一类数据；删除：删掉数据本身；移除：从列表里拿掉 |
| Install / Uninstall / Update / Upgrade / Downgrade | 安装 / 卸载 / 更新 / 升级 / 降级 | |
| Open / Reveal | 打开 / 在文件资源管理器中显示 | |
| Copy Path | 复制路径 | |
| Run as Administrator | 以管理员身份运行 | 与 Windows 一致 |
| File Explorer | 文件资源管理器 | |
| Windows Credential Manager | Windows 凭据管理器 | |
| Command Prompt | 命令提示符 | |
| Folder / File | 文件夹 / 文件 | |
| Data folder / Configuration file | 数据文件夹 / 配置文件 | |
| Export / Import | 导出 / 导入 | |
| Ranking history | 排序记录 | |
| Filter… / Search… | 筛选… / 搜索… | 占位符以“…”结尾 |
| No results / No matching … | 没有结果 / 没有匹配的…… | |
| Could not … | 无法…… | |
| … failed | ……失败 | |
| Just now / 5 minutes ago | 刚刚 / 5 分钟前 | |

### 4.2 插件

| English | 简体中文 | 说明 |
|---------|----------|------|
| Applications | 应用 | |
| Calculator | 计算器 | |
| Clipboard History | 剪贴板历史 | |
| Pin / Unpin / Pinned | 固定 / 取消固定 / 已固定 | 与 Windows 剪贴板一致 |
| File Search (Everything) | 文件搜索（Everything） | |
| Files（情境操作插件） | 文件 | |
| Open Terminal Here | 在此处打开终端 | |
| Checksum | 校验和 | |
| Script Commands | 脚本命令 | |
| World Clock | 世界时钟 | |
| Favorites | 收藏 | |
| Time zone / UTC offset | 时区 / UTC 偏移 | |
| Daylight saving time | 夏令时 | |
| Scoop package | 软件包 | |
| Bucket | bucket | Scoop 术语，不翻译；在句首或标签里写 Bucket |
| Hold / Unhold / Held | 锁定版本 / 解除版本锁定 / 已锁定 | |
| Clean Up | 清理 | |
| Download cache | 下载缓存 | |
| Source (WinGet) | 源 | “更新源”“WinGet 源” |
| Moniker (WinGet) | 别名 | |
| Pin Version (WinGet) | 固定版本 / 取消固定版本 | 和剪贴板的“固定”一致 |
| Process / Processes | 进程 | 进程管理插件，用任务管理器的译法 |
| End Task / Force End / End Process Tree | 结束任务 / 强制结束 / 结束进程树 | |
| Not responding | 未响应 | |
| Apps / Background processes / Windows processes | 应用 / 后台进程 / Windows 进程 | |
| Memory (private working set) / Working set / Commit size | 内存（专用工作集）/ 工作集 / 提交大小 | |
| Handles / Threads | 句柄 / 线程 | |
| Elevated / Not elevated | 已提升 / 未提升 | |
| Restart Windows Explorer | 重启 Windows 资源管理器 | |

### 4.3 不翻译

- 产品名：golauncher、Windows Terminal、PowerShell、VS Code、Everything、Scoop、WinGet、WSL、MyGo、Raycast；
- 键名：Ctrl、Shift、Alt、Enter、Space；
- 单位和代码：KB、MB、GB、UTC、ISO 8601、SHA-256、ZIP、JSON、IANA、`YYYY-MM-DD`、URL 里的协议；
- 命令行：`scoop update *` 这类命令原样保留。

### 4.4 格式约定

- 日期：`2006年1月2日`（Go 时间格式）；任务时间 `1月2日 15:04:05`；世界时钟 `10月15日 星期四`、`2026年10月15日 星期四`，月份名就是“1月”到“12月”，星期写“星期一”；
- 12 小时制写“上午 9:30”“下午 2:30”；时差写“+8 小时”“+5 小时 30 分”；
- 数量词：`{{.Count}} 个应用`、`{{.Count}} 个后台任务`、`{{.Count}} 字节`；
- 复数：中文只有 `other`；英文 `one` 的说法不一样（比如用 `{{.name}}` 而不是数量）时，用精确形式 `"=1"`；
- 搜索关键词：中文词加拼音首字母，比如 `"剪贴板, jtb"`。英文关键词和英文标题由宿主自动参与匹配，运行时由插件自己给出的关键词（files 插件的 `keywords.*`、Everything 的 `context.keywords`）要把英文词也写进中文翻译里。

## 5. 实现记录

> 2026-10-15，R35 实现。

- **消息目录**：新增 `internal/i18n/locales/zh-CN.json`，以及 apps、calc、clipboard、clock、everything、files、scoop、scripts 八个内置插件的 `locales/zh-CN.json`；后台任务的消息在核心目录里。示例插件 go-hello、python-hello 原来就有中文，只调整了关键词。`glctl i18n check` 的结果是 0 个错误、0 个警告。
- **语言匹配**：`zh-CN`、`zh-Hans`、`zh-Hans-CN`、`zh-SG`、`zh` 都匹配到 `zh-CN`。x/text 的匹配器会把繁体中文（`zh-TW`、`zh-HK`、`zh-Hant`）以低置信度匹配到 `zh-CN`；按本方案，繁体中文不在范围内，所以现在置信度低于 High 的匹配一律回退到英文。用户首选语言列表里排在后面的简体中文仍然有效（`zh-TW, zh-CN` → `zh-CN`）。繁体用户可以在设置里手动选“简体中文”。测试见 `internal/i18n/zh_test.go`。
- **语言选择**：设置 › 通用 › 语言，选项是“跟随系统 / English / 简体中文”，每种语言用它自己的名称显示（`i18n.DisplayName`），切换后立即生效。外壳把当前语言同步给 MyGo（`App.SetLanguages`、`App.SetLocale`），MyGo 自带的控件也跟着切换，文本引擎也按中文为汉字选择字形（这一点需要在英文 Windows 上用真机确认）。
- **搜索**：非英文界面下，命令的英文标题和英文关键词也会加入关键词；关键词和标题一样支持拼音、拼音首字母匹配；主搜索的命令列表按拼音排序。所以在中文界面里输入 `clipboard`、`jtb`、`剪贴板` 都能找到“搜索剪贴板历史”。
- **复数**：消息目录支持 ICU 风格的精确形式 `"=1"`，优先于 CLDR 类别。中文只有 `other`，像“复制路径 / 复制 3 个路径”这类单数说法不同的消息用它。
- **检查**：`glctl i18n check` 把 `zh-CN` 视为必须完整的语言：已有 `zh-CN.json` 但缺 key 是错误，插件没有 `zh-CN.json` 是警告；另外检查非英文目录的模板语法，以及英文是复数消息时译文必须有 `other`。
- **测试**：
  - `internal/i18n/zh_test.go`：语言匹配、相对时间和日期格式、`=1` 复数、`ResolveSource`、语言名称；
  - `internal/ui/zh_test.go`：离屏渲染中文的启动器和设置页，检查关键文字都在窗口内、互不重叠；在设置里切换语言立即生效；中文字形确实画出来了（“中”和“国”渲染结果不同）。`TestSnapshots` 增加了 `zh-CN` 的浅色和深色截图（设置 `$GOLAUNCHER_SNAPSHOTS` 可以保存下来查看）；
  - `internal/cli/testdata/golden/zh_*.json`：`glctl --locale zh-CN` 的根列表、计算器、剪贴板历史、世界时钟，以及英文和拼音首字母搜索。
- **遗留**：
  - 世界时钟的城市名称是数据，仍显示英文（搜索时支持中文名）；国家和省/州名称自 R38 起在中文界面显示中文（见 [world-clock.md §9.6](world-clock.md)）；
  - 并行开发的单位换算、系统命令、终端与 WSL、网络工具四个分支合并后需要补 `zh-CN.json`，`glctl i18n check` 会以警告提醒缺少的目录；
  - 中文界面下用的是 Windows 自带的中文字体回退，没有内置字体。
