# golauncher 设计方案

> 状态：草案 v0.2 · 2026-10-14（已确定 §14 的四项决策）
> 目标平台：Windows 10/11（x64 / arm64）
> 技术栈：Go 1.27+、[MyGo](https://github.com/egoist/mygo) v0.5.x（原生 UI 模式）

## 1. 目标与范围

| # | 目标 | 本方案的对应设计 |
|---|------|------------------|
| 1 | 类 Raycast 的 GUI | 无边框置顶浮窗，全局快捷键呼出；"搜索框 + 结果列表 + 操作面板"三段式布局（§4） |
| 2 | 完善的插件系统，自带功能也走插件 | 统一的 `Plugin` 接口和声明式视图协议；内置插件与外部插件共用同一套 Host API（§5） |
| 3 | 配置界面 | 独立的设置窗口，插件偏好项由 manifest 声明并自动生成表单（§6） |
| 4 | 供 Agent 做 UT 的 CLI | `glctl` 无头 CLI，直接驱动核心引擎，输出 JSON（§7） |
| 5 | 内置应用启动器、计算器、剪贴板管理器 | 三个内置插件 `apps` / `calc` / `clipboard`（§8） |

**非目标（第一版不做）**：跨平台支持、插件商店、云同步、AI 对话、英文以外的界面语言。架构上会预留这些能力，但不实现。国际化的设计见 §10：第一版只带英文资源，但从第一天起所有界面文字都走 I18N 管线。

## 2. 为什么用 MyGo 原生 UI，而不是 Web 前端

MyGo 支持两种窗口：WebView2 加载网页，或用 `ui` 包在 GPU 上直接绘制的原生 UI。本项目选原生 UI，原因如下：

- **启动和呼出速度**：启动器每天要呼出几百次，原生 UI 不需要初始化 WebView2，也没有 JS 运行时。
- **全部用 Go 写**：不需要 Bun、Vite、TS 客户端生成。CLI、插件和 UI 用同一种语言，Agent 改代码的门槛最低。
- **组件够用**：`ui` 包已有 `TextInput`、虚拟化的 `List(c, *ListState, n, row)`、`Image`/`Icon`、`Split`、`Scroll`、`Theme`（`DarkTheme`/`LightTheme`）、`OnShortcut` 和 `HandleInput`，覆盖了启动器的全部需求。
- **可测试**：`ui.NewTester(view, w, h)` 提供 `Type`、`Key`、`Click`、`Find`、`Texts` 等方法，`ui.Render` 可以离屏截图，UI 层也能在 `go test` 里跑。

用到的 MyGo 应用级 API：

- `GlobalShortcut.Register(acc, fn)`：全局呼出快捷键
- `NewTray(TrayOptions)`：托盘图标
- `RequestSingleInstanceLock` / `OnSecondInstance`：单实例
- `SetOpenAtLogin`：开机自启
- `Window` 的 `Show`/`Hide`/`Center`/`Focus`/`SetAlwaysOnTop`/`SetSkipTaskbar`/`OnBlur`：窗口控制
- `ClipboardModule` 的 `ReadText`/`ReadImage`/`ReadFiles`/`Write*`：剪贴板读写

> ⚠️ **待验证**：MyGo 还处于 0.x 阶段。`WindowOptions` 的完整字段（无边框、透明、Mica/Acrylic 背景）还没查实，API 也可能变化。所有 MyGo 调用都收敛在 `internal/shell` 一层（§3），框架变动只影响这一层。

## 3. 总体架构

```
┌──────────────────────────────────────────────────────────────┐
│ cmd/golauncher (GUI 进程)          cmd/glctl (无头 CLI)       │
├───────────────────────┬──────────────────────────────────────┤
│ internal/shell        │ internal/cli                         │
│ MyGo 窗口/托盘/热键   │ 命令解析，JSON 输出                  │
│ internal/ui           │                                      │
│ 视图渲染 (mygo/ui)    │                                      │
├───────────────────────┴──────────────────────────────────────┤
│ internal/core  —— 与 UI 无关的引擎                           │
│   Engine: 查询分发 / 合并排序 / 动作执行 / 导航栈            │
│   Ranker: 模糊匹配 + frecency                                │
├──────────────────────────────────────────────────────────────┤
│ pkg/plugin  —— 公开的插件 SDK（接口、视图模型、Host API）     │
├──────────────────────────────────────────────────────────────┤
│ internal/host   Host API 实现（存储、剪贴板、通知、打开…）   │
│ internal/config 配置读写与 schema 校验                       │
│ internal/winapi Win32/COM 封装（syscall，无 cgo）             │
├──────────────────────────────────────────────────────────────┤
│ plugins/apps   plugins/calc   plugins/clipboard   (内置)      │
│ 外部插件：独立进程，JSON-RPC over stdio（阶段 2）             │
└──────────────────────────────────────────────────────────────┘
```

**核心原则：`core` 不依赖任何 UI。** GUI 和 CLI 都只是 `core.Engine` 的前端。这样 Agent 用 CLI 测到的行为，就是用户在 GUI 里看到的行为。

### 3.1 目录结构

```
golauncher/
├── cmd/
│   ├── golauncher/        # GUI 入口（-H windowsgui）
│   └── glctl/             # 无头 CLI 入口
├── pkg/plugin/            # 插件 SDK（对外稳定 API）
├── internal/
│   ├── core/              # Engine、Ranker、Session、导航栈
│   ├── shell/             # MyGo 适配层：窗口、托盘、热键、单实例
│   ├── ui/                # 启动器视图、设置视图、组件
│   ├── host/              # Host API 实现 + fake 实现
│   ├── config/            # 配置加载、迁移、schema
│   ├── store/             # SQLite (modernc.org/sqlite, 纯 Go)
│   ├── i18n/              # Localizer、语言匹配、格式化（§10）
│   │   └── locales/       # en.json（第一版唯一语言）；en-XA 伪语言由代码生成
│   ├── extproc/           # 外部插件进程管理 + JSON-RPC（阶段 2）
│   └── winapi/            # Win32/COM 封装
├── plugins/
│   ├── apps/
│   ├── calc/
│   └── clipboard/
├── testdata/
└── docs/
```

### 3.2 进程与线程模型

- 单进程常驻。启动时用 `RequestSingleInstanceLock` 保证单实例；后启动的实例通过 `OnSecondInstance` 把参数转发给已运行的实例（比如 `golauncher --show`、`golauncher --settings`）。
- MyGo 的回调在主线程执行。插件的 `Search` 一律在 worker goroutine 中运行，带 `context.Context` 超时和取消。结果通过 channel 回到 Engine，再用 `Window.Invalidate()` 触发重绘。
- 用户每输入一个字符，取消上一轮查询的 context。Engine 做 30ms 去抖，并按插件分批流式合并结果：快的插件先出，慢的插件后补，列表不会闪动。

## 4. GUI 设计（类 Raycast）

### 4.1 主窗口

```
┌────────────────────────────────────────────────────┐
│ 🔍  Search apps, commands, and more…          [^K] │  ← TextInput
├────────────────────────────────────────────────────┤
│  Applications                                       │  ← 分组标题
│  ▸ [icon] Visual Studio Code          Application   │  ← 选中项
│    [icon] Windows Terminal            Application   │
│  Calculator                                         │
│    = 1,234 × 5 = 6,170                Copy Result   │
│  Clipboard History                                  │
│    [icon] Search Clipboard History    Command       │
├────────────────────────────────────────────────────┤
│ [logo] golauncher       Open ↵  │  Actions Ctrl+K    │  ← 底部操作栏
└────────────────────────────────────────────────────┘
```

- 默认尺寸 750×475，屏幕居中略偏上，无边框、圆角、阴影，置顶，不在任务栏显示。
- 背景优先用 Mica/Acrylic。如果 MyGo 不支持，就退化为纯色主题（跟随系统深浅色）。
- **呼出**：默认 `Alt+Space`（已确定），可在设置里修改。再按一次，或失去焦点（`OnBlur`），或按 `Esc` 回到根视图时，窗口隐藏。
- **常驻预热**：窗口创建后只隐藏、不销毁，保证呼出延迟在 50ms 以内。

### 4.2 键盘交互

| 按键 | 行为 |
|------|------|
| `↑`/`↓`、`Ctrl+N`/`Ctrl+P` | 移动选中项 |
| `Enter` | 执行主动作 |
| `Ctrl+Enter` | 执行第二动作 |
| `Ctrl+K` | 打开当前项的 Action Panel（浮层动作列表，可搜索） |
| `Tab` | 进入命令的参数输入（例如 `g ` 后接搜索词） |
| `Esc` | 返回上一级视图；在根视图时隐藏窗口 |
| `Ctrl+,` | 打开设置 |
| 动作自带的快捷键 | 例如 `Ctrl+C` 复制、`Ctrl+Shift+Enter` 以管理员身份运行 |

### 4.3 导航栈

Raycast 的"进入命令 → 子视图 → 返回"模型由 Engine 维护的 `[]ViewState` 栈表示。

- 根视图是全局搜索，聚合所有插件的 `RootItems`。
- 执行 `Push(view)` 类型的动作时，压入插件返回的新视图。
- 栈顶视图决定 UI 渲染哪种布局：List、Grid、Detail 或 Form。

## 5. 插件系统

### 5.1 设计要点

1. **内置功能也是插件**：`apps`、`calc`、`clipboard` 只通过 `pkg/plugin` 的公开 API 实现，不能 import `internal/core`。CI 用 `depguard` 规则强制这一点。
2. **声明式 UI**：插件不直接画界面，而是返回视图模型（List/Grid/Detail/Form + Actions），由宿主统一渲染。这样做的好处：
   - 外观一致，主题统一
   - 外部插件可以跨进程，只需传 JSON
   - CLI 能完整输出插件"渲染了什么"，Agent 可以直接断言
3. **能力受控**：插件只能通过 `Host` 接触系统资源，manifest 声明权限（`clipboard`、`network`、`exec` 等）。

### 5.2 Manifest

内置插件在 Go 代码里返回 manifest；外部插件用 `plugin.json` 声明。

```json
{
  "id": "builtin.clipboard",
  "name": "%plugin.name%",
  "version": "1.0.0",
  "icon": "clipboard.svg",
  "defaultLocale": "en",
  "permissions": ["clipboard", "storage"],
  "commands": [
    {
      "name": "history",
      "title": "%command.history.title%",
      "mode": "view",
      "keywords": ["%command.history.keywords%"],
      "defaultHotkey": "Ctrl+Shift+V"
    }
  ],
  "preferences": [
    { "name": "maxItems", "type": "number", "title": "%pref.maxItems.title%", "default": 1000 },
    { "name": "ignoreApps", "type": "stringList", "title": "%pref.ignoreApps.title%", "default": ["KeePassXC.exe"] }
  ]
}
```

所有面向用户的文字都写成 `%key%` 占位符（与 VS Code 的 `package.nls.json` 相同的约定），从插件目录下的 `locales/<lang>.json` 取值，规则见 §10.3。`plugin validate` 会检查占位符在默认语言里都有对应的值。

命令的 `mode` 有三种：

- `view`：进入一个视图
- `no-view`：直接执行，例如"锁屏"
- `inline`：在根搜索里直接给出结果，例如计算器

### 5.3 Go SDK（`pkg/plugin`）

```go
package plugin

type Plugin interface {
    Manifest() Manifest
    Init(ctx context.Context, h Host) error
    Close() error
}

// 可选能力：插件按需实现
type RootProvider interface { // 往根搜索里贡献条目（应用、计算结果…）
    RootItems(ctx context.Context, q Query) ([]Item, error)
}
type CommandRunner interface { // 执行 manifest 中的命令
    RunCommand(ctx context.Context, name string, args Args) (Result, error)
}
type SearchableView interface { // 视图内的增量搜索
    Search(ctx context.Context, viewID string, q Query) (View, error)
}

type Query struct {
    Text  string
    Words []string
}

type Item struct {
    ID        string
    Title     string
    Subtitle  string
    Icon      Icon       // 内置 SVG / 文件路径 / 应用图标引用 / data URI
    Accessory []Tag
    Keywords  []string
    Score     float64    // 插件自身的相关度，0~1，最终排序由 Ranker 融合
    Actions   []Action   // 第一个为主动作
    Detail    *Detail    // 可选右侧预览（Markdown + 元数据）
}

type Action struct {
    ID       string
    Title    string
    Shortcut string        // "Ctrl+C"
    Style    ActionStyle   // Default / Destructive
    Do       ActionDo      // 见下
}

// ActionDo 是可序列化的"意图"，由宿主执行，便于 CLI 观测与外部插件复用
type ActionDo struct {
    Kind    ActionKind // Open, CopyText, Paste, Push, RunCommand, Callback, ...
    Target  string
    Payload json.RawMessage
}

type View struct {
    Kind     ViewKind // List / Grid / Detail / Form
    ID       string
    Title    string
    Sections []Section
    Detail   *Detail
    Form     *Form
    Loading  bool
}

type Result struct {
    View    *View
    Toast   *Toast
    Hide    bool
    PopToRoot bool
}
```

**为什么动作是"意图"而不是闭包**：大多数动作（打开、复制、粘贴、跳转视图）都由宿主统一执行。这样 CLI 只用 `--dry-run` 就能打印出"将要执行什么"，不会产生副作用；外部插件也能用纯 JSON 表达动作。需要插件自己处理的逻辑，用 `Kind=Callback` 回调到插件的 `RunCommand`。

### 5.4 Host API

```go
type Host interface {
    Logger() *slog.Logger
    Storage() KV                    // 每个插件独立的命名空间，SQLite 落盘
    Preferences() Preferences       // 读取设置界面填写的偏好
    Clipboard() Clipboard           // 需要 clipboard 权限
    Open(target string) error       // 文件 / URL / shell: 路径 / AUMID
    Notify(t Toast)
    HideWindow()
    Paste(text string) error        // 写剪贴板 → 隐藏窗口 → 向前一个前台窗口发送 Ctrl+V
    I18n() Localizer                // 当前语言下、限定在本插件命名空间的翻译与格式化（§10）
    Events() EventBus               // 订阅 window-shown、clipboard-changed 等宿主事件
    DataDir() string
}
```

`internal/host` 有两套实现：

- **真实实现**：调用 MyGo 和 `winapi`
- **`hostfake`**：内存实现，供单元测试和 `glctl --fake` 使用

### 5.5 外部插件（阶段 2）

- **形态**：任意语言写的可执行文件，放在 `%APPDATA%\golauncher\plugins\<id>\`，用 `plugin.json` 声明入口。
- **协议**：JSON-RPC 2.0 over stdio。
  - 宿主 → 插件：`initialize`、`rootItems`、`runCommand`、`search`、`shutdown`
  - 插件 → 宿主：`host/storage.get`、`host/clipboard.read`、`host/notify` 等，与 Host API 一一对应
- **生命周期**：懒启动（第一次被用到时才拉起），空闲 5 分钟后回收。崩溃后自动重启，并做熔断。
- **超时**：`rootItems` 默认 150ms。超时的结果直接丢弃，不阻塞根搜索。
- **Go 插件**：用 `pkg/plugin/rpcserve.Serve(myPlugin)` 一行包装就能变成外部插件，内置插件和外部插件共用同一套代码。
- **语言**：`initialize` 会传入当前 `locale`；用户切换语言时，宿主发送 `localeChanged` 通知。manifest 里的文字由宿主翻译，插件运行时产生的文字由插件自己翻译（§10.3）。
- **信任模型（已确定：不做签名）**：
  - 外部插件不需要签名，宿主也不做签名校验或安装时的信任提示。
  - 外部插件是以当前用户权限运行的普通进程，manifest 里的 `permissions` 只约束它能调用哪些 Host API，并不是安全边界。设置页的插件详情会如实写明这一点。
  - 只从用户手动放入的目录加载插件，不提供自动下载。

> 不采用 Go 原生 `plugin` 包（不支持 Windows），也不采用 WASM（Host API 桥接成本高，阶段 3 再评估）。

### 5.6 排序（Ranker）

最终得分 = `w1·match + w2·frecency + w3·pluginScore + 别名精确命中加成`。

- **match**：fzf 风格的模糊匹配，同时支持：
  - 首字母缩写，例如 `vsc` 匹配 Visual Studio Code
  - 拼音全拼和首字母，例如 `wx` 匹配"微信"（用 `go-pinyin`）
- **frecency**：按 `(query 前缀, itemID)` 记录选择次数和最近使用时间，指数衰减（半衰期 7 天），存入 SQLite。
- **别名**：用户可以在设置里给任意命令设置别名（如 `cb` → 剪贴板历史）。别名精确命中时置顶。

## 6. 配置界面

- **入口**：
  - 托盘菜单 → 设置
  - 主窗口 `Ctrl+,`
  - 根搜索里的 "Settings" 命令
  - `golauncher --settings`
- **窗口**：独立的普通窗口（有标题栏，可调整大小），左侧导航 + 右侧内容（`ui.Split`）。
- **页面**：
  1. **通用**：呼出快捷键（录制控件）、开机自启（`SetOpenAtLogin`）、主题（系统/浅/深）、窗口位置、失焦隐藏开关、界面语言（默认"跟随系统"；第一版只有英文资源，只有一种语言时下拉框不显示）
  2. **插件**：插件列表（启用开关、版本、权限）。点进单个插件后：
     - 每个命令的别名、独立全局快捷键、是否出现在根搜索
     - 偏好项表单，由 manifest 的 `preferences` 自动生成（类型有 `text`、`password`、`number`、`bool`、`select`、`stringList`、`path`、`hotkey`）
  3. **高级**：数据目录、日志级别、导出/导入配置、清除 frecency
  4. **关于**
- **存储**：`%APPDATA%\golauncher\config.json`
  - 文件带 `schemaVersion`，配有迁移函数
  - 修改即时生效：设置窗口写入 `config.Store`，`Store` 广播 `config-changed`，Engine 和插件订阅这个事件
  - `password` 类型的偏好不写入 JSON，存进 Windows 凭据管理器（`CredWrite`）

## 7. CLI：`glctl`（供 Agent 做 UT）

### 7.1 设计原则

- **无头**：直接实例化 `core.Engine` 和插件，不需要 GUI 进程，也不依赖 WebView2 或桌面会话。CI 和 Agent 沙箱都能跑。
- **确定性**：
  - `--fake` 使用 `hostfake`（内存剪贴板、内存存储）
  - `--fixtures dir` 注入固定数据（如一组假的应用列表）
  - `--now 2026-10-14T10:00:00Z` 固定时钟，保证 frecency 可复现
  - `--locale en` 固定语言，不读取系统语言；默认值就是 `en`，保证 golden 文件与机器无关
- **机器可读**：默认输出 JSON，与插件 SDK 的类型同构。`--format text` 输出人类可读格式。
- **无副作用**：执行动作默认是 dry-run，只打印 `ActionDo`。需要真实执行时加 `--exec`。

### 7.2 命令

```text
glctl query <text>                       # 根搜索，输出排序后的 Item 列表
glctl run <plugin>/<command> [--arg k=v] # 执行命令，输出 Result/View
glctl action <itemID> [<actionID>]       # 对上次查询结果执行动作（会话见下）
glctl session new|step|show              # 多步交互：模拟 输入→选择→Push→搜索→返回
glctl plugin list|info <id>|validate <dir>
glctl config get|set|path|schema
glctl index rebuild apps                 # 触发应用索引重建并输出统计
glctl ui snapshot --query "vsc" -o a.png # 用 ui.Render 离屏渲染启动器截图（视觉回归）
glctl i18n check [--plugin <id>]         # 检查缺失 / 多余的翻译 key 和占位符参数（§10.5）
glctl attach <subcmd>                    # 通过命名管道连接正在运行的 GUI 实例（端到端）
```

### 7.3 输出示例

```bash
$ glctl query "12*3+4" --fake
```

```json
{
  "query": "12*3+4",
  "elapsedMs": 3,
  "items": [
    {
      "id": "builtin.calc/result",
      "plugin": "builtin.calc",
      "title": "40",
      "subtitle": "12*3+4",
      "score": 0.98,
      "actions": [
        {"id": "copy", "title": "Copy Result", "shortcut": "Enter",
         "do": {"kind": "CopyText", "target": "40"}}
      ]
    }
  ]
}
```

### 7.4 会话（多步交互）

`glctl session` 把导航栈序列化到 `--session file.json`，每次 `step` 读取、执行、写回。Agent 可以写出这样的测试脚本：

```bash
glctl session new -s s.json --fake --fixtures testdata/clip
glctl session step -s s.json type "Search Clipboard History"
glctl session step -s s.json key Enter       # Push 到剪贴板视图
glctl session step -s s.json type "hello"
glctl session show -s s.json                 # 断言视图内容
```

### 7.5 测试分层

| 层级 | 工具 | 运行环境 |
|------|------|----------|
| 插件单元测试 | `go test` + `hostfake` | 任意 |
| 引擎与集成测试 | `glctl` + golden JSON（`testdata/golden/*.json`，`-update` 更新） | 任意 |
| UI 组件测试 | `ui.NewTester`（`Type`/`Key`/`Find`） + `ui.Render` 截图比对 | 任意（离屏） |
| 端到端测试 | `glctl attach` 连接真实 GUI 实例 | Windows 桌面会话 / `windows-latest` CI |

`glctl` 的 JSON 输出格式视为对外契约：变更时必须更新 golden 文件，并在 CHANGELOG 里记录。

## 8. 内置插件

### 8.1 `builtin.apps`：Windows 应用启动器

**数据源**：

1. **Win32 应用**：扫描
   - `%ProgramData%\Microsoft\Windows\Start Menu\Programs`
   - `%APPDATA%\Microsoft\Windows\Start Menu\Programs`

   用 `IShellLinkW` 解析 `.lnk` 的目标、参数和图标。
2. **UWP / MSIX 应用**：通过 COM 枚举 `FOLDERID_AppsFolder`（即 `shell:AppsFolder`），拿到 AUMID 和显示名。这一步同时覆盖 Win32 应用，用来去重和补全。
3. **可选**：用户在设置里配置的额外目录，以及 PATH 中的 `.exe`（默认关闭）。

**索引**：

- 启动时先从 SQLite 缓存加载，保证秒开；再在后台全量重扫。
- 用 `ReadDirectoryChangesW` 监听开始菜单目录，增量更新。
- 去重 key：规范化后的目标路径，或 AUMID。
- 过滤卸载程序、帮助文档类链接（名称规则 + 目标扩展名）。

**图标**：

- 用 `IShellItemImageFactory::GetImage` 取 64px 图标，转成 PNG 后缓存到 `%LOCALAPPDATA%\golauncher\icons\<hash>.png`。
- 只为可见行懒加载图标。

**动作**：

| 动作 | 快捷键 | 实现 |
|------|--------|------|
| 打开 | `Enter` | `ShellExecuteExW`；UWP 用 `IApplicationActivationManager::ActivateApplication` |
| 以管理员身份运行 | `Ctrl+Shift+Enter` | `ShellExecuteExW` + `runas` |
| 在资源管理器中显示 | `Ctrl+Shift+F` | — |
| 复制路径 | `Ctrl+Shift+C` | — |
| 固定到收藏 | — | — |
| 隐藏此应用 | — | 写入插件配置 |

**搜索字段**：显示名、文件名、拼音、自定义别名。

### 8.2 `builtin.calc`：计算器

- **模式**：`inline`，在根搜索里直接显示结果。只有当输入"看起来像表达式"时才输出，避免干扰普通搜索。判断标准：含数字和运算符，或以 `=` 开头。
- **实现**：自写 Pratt parser，数值用 `math/big.Float`（精度 128 bit），避免 `0.1+0.2` 这类浮点误差。
- **第一版支持**：
  - 四则运算、`%`、`^`、括号
  - 常量 `pi`、`e`
  - 函数 `sqrt`、`sin`、`cos`、`tan`、`log`、`ln`、`abs`、`round`、`floor`、`ceil`
  - 进制 `0x`/`0b` 及转换（`255 in hex`）
  - 千分位格式化，以及 `ans`（上一次结果）
- **与语言的关系**：
  - 输入语法与语言无关，固定用 `.` 作小数点、英文函数名，保证表达式在任何语言下含义不变。
  - 结果显示按当前语言格式化（千分位、小数点），由 `Localizer` 的数字格式化负责。
  - "复制结果"复制的是不带千分位的原始数字，不受语言影响。
- **阶段 2**：单位换算（`10 km in mi`），汇率换算（需要 `network` 权限，结果缓存）。
- **动作**：
  - `Enter` 复制结果
  - `Ctrl+Enter` 复制表达式和结果
  - 粘贴结果到前一个窗口
- **测试**：表驱动单元测试，覆盖率目标 95% 以上。表达式解析是 Agent 最容易做回归测试的部分。

### 8.3 `builtin.clipboard`：剪贴板管理器

**监听**：

- MyGo 目前没有确认提供剪贴板变化事件。插件在 `winapi` 里自建一个 message-only 窗口，调用 `AddClipboardFormatListener` 接收 `WM_CLIPBOARDUPDATE`，再通过 Host 的 `EventBus` 发出 `clipboard-changed` 事件。
- 读取内容优先用 MyGo `ClipboardModule`，自定义格式再走 Win32。

**采集**：

- 支持三种类型：
  - 文本（含 HTML/RTF 时保留纯文本和原始格式）
  - 图片（PNG 落盘，存缩略图）
  - 文件列表
- 来源应用：`GetClipboardOwner` → 进程名，用于"忽略的应用"过滤。
- 尊重 `ExcludeClipboardContentFromMonitorProcessing` 和 `CanIncludeInClipboardHistory` 格式。密码管理器的复制会自动跳过。
- 用 SHA-256 去重：重复复制只更新时间戳，不新增条目。

**存储**：

- SQLite 表 `clips(id, kind, text, hash, size, source_app, created_at, last_used_at, pinned)`
- FTS5 全文索引
- 图片存在 `DataDir/clips/`

**视图**：

- 列表 + 右侧 Detail 预览：文本高亮，图片显示缩略图和尺寸，文件显示列表
- 按类型筛选（全部 / 文本 / 图片 / 文件）

**动作**：

| 动作 | 快捷键 | 说明 |
|------|--------|------|
| 粘贴 | `Enter` | 写回剪贴板 → 隐藏窗口 → 恢复前一个前台窗口 → `SendInput` 发送 `Ctrl+V` |
| 仅复制 | `Ctrl+Enter` | — |
| 固定 / 取消固定 | `Ctrl+Shift+P` | — |
| 删除 | `Ctrl+X` | — |
| 清空历史 | — | 需要二次确认 |

**偏好项**：

- 最多保留条数和天数
- 是否记录图片
- 忽略的应用
- 独立呼出快捷键（默认 `Ctrl+Shift+V`，直接打开历史视图）

**隐私（已确定：不加密）**：

- 历史以明文存在本地 SQLite 中，这样 FTS5 全文检索可以直接使用；不上传，也不同步。
- 主要靠"采集时过滤"保护敏感内容：上面提到的两种排除格式，加上"忽略的应用"列表。
- 设置里提供"暂停记录"和"清空历史"。
- 历史中的时间显示（如 "2 minutes ago"）走 `Localizer` 的相对时间格式化。

## 9. Windows 平台细节

| 主题 | 方案 |
|------|------|
| 前台窗口焦点 | 呼出前用 `GetForegroundWindow` 记住上一个窗口，粘贴或隐藏时还原。Windows 限制后台进程抢焦点，呼出时在热键回调里调用 `SetForegroundWindow`（此时有输入权限）；失败时退回 `AttachThreadInput` 方案 |
| `Alt+Space` 冲突 | 默认键已确定为 `Alt+Space`。注册全局热键后，它会覆盖系统窗口菜单的 `Alt+Space`（与 Raycast for Windows 的做法相同）。如果被 PowerToys Run 等程序占用导致注册失败，就弹出通知并打开设置页的热键录制，建议改用 `Ctrl+Space` 或 `Alt+Shift+Space` |
| DPI | 声明 Per-Monitor V2，窗口显示在当前鼠标所在的显示器上 |
| 纯 Go | 不用 cgo；Win32 走 `golang.org/x/sys/windows` + `syscall`；SQLite 用 `modernc.org/sqlite` |
| 打包 | `go build -ldflags "-H windowsgui"`；用 `go-winres` 嵌入图标和 manifest；用 MSIX 或 Inno Setup 做安装包；更新用 MyGo `plugins/updater` |
| 运行时依赖 | 原生 UI 模式不依赖 WebView2（待验证：MyGo 原生 UI 在 Windows 上的渲染后端） |

## 10. 国际化（I18N）

第一版只发布英文，但所有面向用户的文字从第一天起都走下面的管线。以后增加一种语言，只需要添加资源文件，不需要改代码。

### 10.1 原则

1. **代码里不写死界面文字**：GUI、托盘菜单、通知、设置页、错误提示，一律通过 message key 取文字。英文也写在 `en.json` 里，不直接写在代码中。
2. **英文是源语言，也是兜底语言**：任何 key 在当前语言里找不到时，退回英文；英文也没有时，显示 key 本身，并记一条日志。
3. **翻译与格式化分开**：
   - 句子用带命名参数的消息（`"Copied {count, plural, one {# item} other {# items}}"`），不在代码里拼接字符串。
   - 数字、日期、相对时间统一用 locale 感知的格式化函数，不用 `fmt.Sprintf`。
4. **数据不翻译**：应用名、文件名、剪贴板内容、计算表达式都是数据，原样显示。
5. **CLI 的机器输出与语言无关**：JSON 里的 key、枚举值、`ActionDo` 都是固定的英文标识符。只有 `title` 这类显示字段会被翻译。

### 10.2 实现

| 部分 | 方案 |
|------|------|
| 消息库 | [`github.com/nicksnyder/go-i18n/v2`](https://github.com/nicksnyder/go-i18n)：JSON 资源、CLDR 复数规则、命名参数 |
| 语言匹配 | `golang.org/x/text/language` 的 `Matcher`。系统语言来自 `GetUserPreferredUILanguages`，与可用资源匹配，匹配不上时用 `en` |
| 数字 / 日期格式化 | `golang.org/x/text/message` + `x/text/number`；相对时间用自己的小模块（CLDR 字符串放在资源文件里） |
| 资源打包 | `//go:embed locales/*.json`，编进二进制文件；外部插件的资源从插件目录读取 |
| key 命名 | `<作用域>.<区域>.<名称>`，例如 `core.search.placeholder`、`settings.general.hotkey.label`、`apps.action.runAsAdmin` |

核心 API（`internal/i18n`，并通过 `pkg/plugin.Localizer` 暴露给插件）：

```go
type Localizer interface {
    Locale() language.Tag
    T(key string, args ...Arg) string            // 翻译，args 为命名参数
    N(key string, count int, args ...Arg) string // 带复数
    Number(v any) string                         // 千分位 / 小数点
    RelativeTime(t time.Time) string             // "2 minutes ago"
    Shortcut(s string) string                    // "Ctrl+Shift+V" 的本地化显示名
}
```

切换语言时，`i18n.Manager` 广播 `locale-changed` 事件。UI 调用 `Invalidate()` 重绘，Engine 重新执行当前查询，所以界面文字会立即更新，不需要重启。

### 10.3 插件的国际化

- **manifest 文字**：`name`、`title`、`description`、偏好项标题、`keywords` 都可以写成 `%key%` 占位符，宿主用插件目录下的 `locales/<lang>.json` 解析。没有占位符的文字原样显示，这样个人用的小插件可以不做国际化。
- **运行时文字**：
  - 内置插件和用 Go 写的外部插件，调用 `Host.I18n()`，拿到限定在本插件命名空间的 `Localizer`。
  - 用其他语言写的外部插件，从 `initialize` 拿到 `locale`，自行翻译。
- **搜索关键字**：当前语言的 keywords 和英文 keywords 同时参与匹配。即使用户切换了界面语言，原来用英文搜索命令的习惯也照样能用。

### 10.4 UI 适配

- **布局不依赖文字长度**：译文通常比英文长 30%～40%。所有文字区域都允许截断加省略号，并显示 tooltip；按钮和标签不设固定宽度。
- **方向**：第一版不支持从右到左的语言，但布局代码用 start/end 而不是 left/right 命名，便于以后支持 RTL。
- **字体**：以后加入中日韩语言时，需要确认 MyGo 原生 UI 的字体回退能力，记录在 M0 的技术验证中。

### 10.5 测试与检查

- **伪语言 `en-XA`**：从 `en.json` 自动生成，把文字变长、加上重音字母和括号（如 `[Šéåŕçĥ åþþš… one two]`）。debug 构建和 `glctl --locale en-XA` 可以使用。用它截图，可以一眼找出写死的英文和被截断的布局。
- **`glctl i18n check`**：检查代码引用的 key 都存在于 `en.json`、没有未使用的 key、各语言的参数名一致。CI 必跑。
- **lint**：自定义 `go/analysis` 分析器，禁止在 `internal/ui`、`internal/shell`、`plugins/*` 的非测试代码中，把字符串字面量直接传给 `ui.Text`、`Item.Title`、`Toast` 等显示字段。

## 11. 性能目标

| 指标 | 目标 |
|------|------|
| 冷启动到可呼出 | < 500ms |
| 热键到窗口可见 | < 50ms |
| 按键到结果刷新（P95，含 1000 个应用、1 万条剪贴板历史） | < 16ms |
| 常驻内存 | < 80MB |

`glctl bench` 子命令输出上述指标，CI 跟踪回归。

## 12. 里程碑

| 阶段 | 内容 | 验收 |
|------|------|------|
| **M0 技术验证（1 周）** | 用 MyGo 实现：无边框置顶窗口 + 全局热键 + TextInput + 虚拟列表；确认 `WindowOptions` 能力、焦点抢占、渲染性能 | 能呼出、搜索假数据、`Esc` 隐藏；产出 `docs/spike-mygo.md` |
| **M1 核心 + CLI + I18N 基础** | `pkg/plugin`、`core.Engine`、Ranker、`hostfake`、`glctl query/run/session`；`internal/i18n`、`en.json`、`en-XA` 伪语言、`glctl i18n check`、lint 分析器 | golden 测试通过；`i18n check` 和 lint 进入 CI |
| **M2 三个内置插件** | `calc` → `apps` → `clipboard` | 各插件单元测试 + `glctl` 集成测试 |
| **M3 GUI 完整化** | 导航栈、Action Panel、Detail 预览、主题、托盘 | `ui.NewTester` 用例覆盖主要交互 |
| **M4 设置界面** | 通用 / 插件 / 高级页面，偏好表单自动生成，热键录制 | 设置修改即时生效 |
| **M5 外部插件** | JSON-RPC 协议、`extproc`、`rpcserve`、示例插件 | 用 Go 和 Python 各写一个示例插件并跑通 |
| **M6 发布** | 安装包、自动更新、性能基线 | 满足 §11 指标；`en-XA` 截图检查无写死文字 |

## 13. 风险与对策

| 风险 | 影响 | 对策 |
|------|------|------|
| MyGo 处于 0.x，API 变动或 Windows 后端不成熟 | 高 | 所有调用收敛在 `internal/shell` 和 `internal/ui`。M0 验证关键能力；不满足时，窗口层可替换为直接调用 Win32 |
| MyGo 缺少透明/亚克力窗口支持 | 中 | 退化为纯色圆角主题，或通过 `NativeHandle()` 拿到 HWND，调用 `DwmSetWindowAttribute` 自行设置 |
| UWP 应用枚举和图标获取复杂 | 中 | 优先用 `shell:AppsFolder` 统一入口，少写 MSIX 专用逻辑 |
| 粘贴到前一个窗口不可靠（焦点、UAC 窗口） | 中 | 失败时退化为"已复制"提示；对提升权限的目标窗口给出说明 |
| 外部插件拖慢根搜索 | 中 | 超时丢弃、熔断、显示每个插件的耗时 |

## 14. 已确定的决策（2026-10-14）

| # | 问题 | 决定 | 影响的章节 |
|---|------|------|------------|
| 1 | 默认呼出键 | `Alt+Space`；注册失败时引导用户改键 | §4.1、§9 |
| 2 | 剪贴板历史是否加密 | 不加密，明文存储，保留 FTS5 全文检索 | §8.3 |
| 3 | 外部插件是否签名 | 不需要签名，也没有信任提示 | §5.5 |
| 4 | 国际化 | 第一版只做英文，整体按可扩展的 I18N 架构设计 | §10 |

## 参考

- MyGo 仓库：https://github.com/egoist/mygo
- MyGo API 文档：https://pkg.go.dev/github.com/egoist/mygo 、https://pkg.go.dev/github.com/egoist/mygo/ui
- MyGo 上手文章：https://azukiazusa.dev/en/blog/mygo-desktop-app/
- Raycast 扩展 API（交互模型参考）：https://developers.raycast.com
