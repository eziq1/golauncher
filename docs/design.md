# golauncher 设计方案

> 状态：v0.3 · 2026-10-15（v0.1.0 已实现；§12 里程碑和 §15 状态已更新；需求台账见 [requirements.md](requirements.md)）
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
│   ├── core/              # Engine（排序、执行动作）+ Controller（输入/选中/动作面板/确认/导航栈状态机）
│   ├── app/               # 装配：配置、数据库、I18N、Host、Engine；真实/假环境共用；Recorder
│   ├── builtin/           # 内置插件注册表（All / Fake）
│   ├── match/             # 模糊匹配 + 拼音
│   ├── cli/               # glctl 实现
│   ├── control/           # GUI 的 E2E 控制通道（unix socket）
│   ├── shell/             # MyGo 适配层：窗口、托盘、热键、单实例
│   ├── ui/                # 启动器视图、设置视图、组件
│   ├── host/              # Host API 实现；hostfake 为插件测试用的内存实现
│   ├── config/            # 配置加载、迁移、schema
│   ├── store/             # SQLite (modernc.org/sqlite, 纯 Go)：插件 KV、frecency、最近使用
│   ├── i18n/              # 消息目录、语言匹配、格式化（§10）
│   │   └── locales/       # en.json（第一版唯一语言）；en-XA 伪语言由代码生成
│   ├── extproc/           # 外部插件进程管理 + JSON-RPC（阶段 2，未实现）
│   └── winapi/
│       ├── shell/         # 应用枚举、图标、ShellExecute（COM，无 cgo）
│       └── clip/          # 剪贴板读写、变化监听、粘贴到前一个窗口
├── plugins/
│   ├── apps/
│   ├── calc/
│   └── clipboard/
├── testdata/
└── docs/
```

### 3.2 进程与线程模型

- 单进程常驻。启动时用 `RequestSingleInstanceLock` 保证单实例；后启动的实例通过 `OnSecondInstance` 把参数转发给已运行的实例（比如 `golauncher --show`、`golauncher --settings`）。
- MyGo 的回调在主线程执行。搜索由 `Controller` 在后台 goroutine 中运行，带 `context.Context` 取消；完成后调用 `OnChange`，GUI 再用 `Window.Invalidate()` 触发重绘。
- 用户每输入一个字符，就取消上一轮查询（按代数丢弃过期结果）。根搜索并发调用各插件的 `RootItems`，单个插件超过 150ms 的结果直接丢弃。
- 按键（可能执行插件动作、启动程序）通过 GUI 的单个串行 worker goroutine 交给 `Controller.Key`，既不阻塞主线程，又保证按键顺序。
- 第一版未实现"按插件分批流式合并"和输入去抖；按 §11 的基准，同步合并已经足够快，需要时再加。

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
- 背景优先用 Mica/Acrylic。如果 MyGo 不支持，就退化为纯色主题（跟随系统深浅色）。颜色、材质和透明度由主题决定，见 [themes.md](themes.md)。
- **呼出**：默认 `Alt+Space`（已确定），可在设置里修改。再按一次，或失去焦点（`OnBlur`），或按 `Esc` 回到根视图时，窗口隐藏。
- **常驻预热**：窗口创建后只隐藏、不销毁，保证呼出延迟在 50ms 以内。

#### 4.1.1 搜索栏的高度固定（R74，2026-10-16）

**现象**：选中带参数的命令（如网络插件的"DNS 查询"）时，搜索框右侧出现参数输入框，搜索栏下方的分隔线下移几个像素；在有参数和无参数的项之间上下移动选中项，搜索栏高度来回跳。

**根因**：搜索栏是 `Row(...).Padding(sp(14), 16).AlignItems(Center)`，没有固定高度，行高等于最高子元素的高度加上下内边距。无参数时最高的是搜索框（`TextInputBase`，18 号字，一行 ≈ 23.94 DIP）。参数文本框是 14 号字（一行 ≈ 18.62 DIP）加上下各 4 的内边距，共 ≈ 26.62 DIP，比搜索框高 2.68 DIP；下拉框（`mui.Select`）用主题字号和主题间距（上下内边距 `Space(1.5)` + 1 像素边框），默认约 32.62 DIP，高 8.68 DIP，字号调大时更高。用 `mui.NewTester` 实测（默认主题）：第一行结果的 y 从 165.57 变成 168.25（文本参数）/ 174.25（文本 + 下拉参数）。用户屏幕 150% 缩放下 2.68 DIP ≈ 4–5 物理像素，和截图吻合。

**修复**（`internal/ui/launcher.go` 的 `searchBar`、`form.go` 的 `argumentFields`）：

- 搜索栏改为固定高度：`2 × sp(14) + 内容高`，内容高是搜索框一行的高度（按 18 号字测得），在子视图里不低于返回按钮的 28。它不再取决于行里还显示什么（参数框、加载动画），密度通过 `sp` 缩放。
- 行高用 `c.MeasureText` 测主题字号的一行再按字号比例换算：MyGo 的 `MeasureText` 在主题字号的段落里排版，行高不会低于主题字号的一行，直接测 18 号字在主题字号为 20 时会得到 20 号字的行高。
- 参数框收进内容高内：上下内边距取 `min(4, (内容高 − 14 号字一行) / 2)`，默认 2.66；下拉框也用 14 号字（和文本参数一致，不随主题字号变），内边距再减去 1 像素边框。所以参数框和搜索框一样高、垂直居中。
- 无参数时的外观不变（行高、各元素位置和以前一致，原有截图测试不变）；有参数时参数框比以前矮 2.7 DIP，下拉框矮 8.7 DIP，这是有意的改动。
- 同时检查了操作面板（Ctrl+K）的过滤框和确认对话框：过滤框单独一行，旁边没有别的控件；确认对话框没有输入框；表单视图的搜索栏是固定 28 高的占位框。它们都没有这个问题。

**测试**：`internal/ui/searchbar_test.go` 的 `TestSearchBarKeepsItsHeight`，在默认、紧凑、宽松密度和 20、11 号字下，在无参数、文本参数、文本 + 下拉参数的命令之间来回切换，断言搜索框的位置和高度、第一行结果的 y 完全不变，并且参数框不高于搜索框、和它垂直居中。修复前每种设置都失败。

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
- 窗口隐藏后再呼出，默认回到根视图；栈顶视图声明了 `View.KeepOnHide`（如 AI 对话）时整栈保留，下次呼出恢复（设置"回到主界面"：立即 / 10 分钟后（默认）/ 从不；读到新的选中文字、选中文字热键、插件拒绝恢复等情况仍回根）。详见 [keep-view.md](keep-view.md)（R75）。

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
      "defaultHotkey": "Alt+Shift+V"
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

以下是已实现的接口摘要，完整定义见 [pkg/plugin](../pkg/plugin/)。

```go
type Plugin interface {
    Manifest() Manifest
    Init(ctx context.Context, h Host) error
    Close() error
}

// 可选能力：插件按需实现
type RootProvider interface {   // 往根搜索里贡献条目（应用、计算结果…）
    RootItems(ctx context.Context, q Query) ([]Item, error)
}
type ViewProvider interface {   // 渲染 view 命令和 Push 动作打开的视图；每次输入都会再调用
    View(ctx context.Context, req ViewRequest) (View, error)
}
type ActionHandler interface {  // 执行 no-view 命令（RunCommand）和插件回调（Callback）
    HandleAction(ctx context.Context, a ActionDo) (Result, error)
}
type LocaleProvider interface { // 插件自带的消息目录（en.json …）
    Locales() fs.FS
}

type Item struct {
    ID, Title, Subtitle string
    Icon      Icon     // 内置图标名或图片文件路径
    Keywords  []string
    Accessory string   // 右侧附注，如 "Application"
    Group     string   // 根搜索里的分组标题
    Score     float64  // 0 表示交给宿主做模糊匹配；>0 表示插件已判定匹配（如计算结果）
    Actions   []Action // 第一个是主动作（Enter），第二个是次动作（Ctrl+Enter）
    Detail    *Detail  // 右侧预览：Markdown、图片、元数据
}

type Action struct {
    ID, Title string
    Shortcut  string      // "Ctrl+Shift+C"
    Style     ActionStyle // Destructive：红色显示，并且执行前要求确认
    Do        ActionDo
}

// ActionDo 是可序列化的"意图"，由宿主执行
type ActionDo struct {
    Kind    ActionKind // Open, CopyText, CopyImage, CopyFiles, Paste, Push, RunCommand, Callback
    Target  string
    Payload jsontext.Value
}

type View struct {
    Kind          ViewKind // list / detail
    Title, Placeholder, EmptyText string
    Sections      []Section
    FilterLocally bool     // 由宿主在本地模糊过滤，不再每次输入都调用插件
    ShowDetail    bool     // 在列表右侧显示选中项的 Detail
}

type Result struct {
    Push      *ViewRequest
    Toast     *Toast
    Hide, PopToRoot, Refresh bool
}
```

`Command` 有 `Destructive` 标记（例如"清空剪贴板历史"）。这类命令和 Destructive 动作一样，执行前由 Controller 要求确认：Enter 确认，Escape 取消。同分时它们排在普通条目后面。

**为什么动作是"意图"而不是闭包**：打开、复制、粘贴、跳转视图这些动作都由宿主统一执行。好处有两个：

- 测试时宿主用 `Recorder` 只记录"将要执行什么"，不产生副作用，`glctl` 默认就是这样。
- 外部插件可以用纯 JSON 表达动作。

需要插件自己处理的逻辑，用 `Kind=Callback` 回调到插件的 `HandleAction`。

#### 5.3.1 插件生命周期（R88，2026-10-17）

“禁用”不只是把插件从搜索里藏起来，而是让它不运行（代码审查 2.1、2.3，见 [code-review-2026-10-17.md](code-review-2026-10-17.md)）：

- **启动**：Engine 先登记全部插件（manifest、消息目录，重复 ID 在任何 `Init` 之前就报错），再按顺序只对已启用的插件调用 `Init`。禁用的插件不 `Init`，但设置页照常列出它（manifest、偏好、命令都来自登记信息），可以配置、可以启用。
- **运行中禁用**：配置变化后，Engine 在后台 goroutine 里对照配置调整（`core/lifecycle.go` 的 `syncPlugins`，按“最新配置”收敛，可重复执行）。禁用的插件先从所有调用路径里摘掉（根搜索、视图、动作、上下文/选中文本/回退/Tab、偏好选项、AI 工具），再调 `Close`（`guard` 兜底 panic，最多等 10 秒）；Controller 里该插件的视图关闭并回到根搜索，保留的视图（R75）也作废。
- **重新启用**：对同一个插件值再次 `Init`，配一个新的 `Host`；要等上一轮彻底结束才 `Init`：`Close` 已返回，上一轮的事件回调和 Engine 放弃等待的调用都已退出（`Host.Drain`、按插件计数的调用，最多等 10 秒，否则等它结束后再启动）。所以 `Init` 必须能在 `Close` 之后重新开始（清掉 closed 标志、重建被关掉的 context 和 channel）；`internal/builtin` 的 `TestPluginsStartAgain` 把每个内置插件关了再开，比对根搜索结果。
- **Host 吊销**：每次运行结束（`Close` 返回、`Close` 超时、`Init` 失败）后，Engine 吊销这一轮的 `Host`：通过它订阅的事件全部退订，`Invalidate`/`Render`/`Toast`/`RunProcess`/`RunTask` 变成空操作；`Go` 照常运行，免得插件在 `Close` 里等自己的 goroutine 时卡住。
- **Init 失败**：返回错误（含 `ErrUnavailable`）或 panic 后，Engine 调一次 `Close` 并吊销 Host，插件标为不可用。约定：`Close` 必须能处理只做了一半的 `Init`。
- **后台任务**：任务归宿主的任务管理器所有。禁用时正在运行的任务让它跑完（不中断安装等操作），结果照常出现在“后台任务”里；被吊销的 Host 不能再开新任务。
- **退出与回滚**：`Engine.Close` 按 `Init` 的逆序关闭仍在运行的插件，并等每个 `Close` 结束；`core.New` 中途失败（例如插件数据目录建不出来）时，同样逆序关闭已经初始化的插件。
- **外部插件**：禁用即 `Close`，停止进程；重新启用后，下一次调用时再启动新进程。
- **等待**：`Engine.WaitPlugins` 等后台的启停做完，`Controller.Wait` 先等它，所以测试和 glctl 在修改配置后调 `Wait` 即可看到结果。

### 5.4 Host API

```go
type Host interface {
    Logger() *slog.Logger
    Now() time.Time           // 测试和 glctl --now 可以固定时钟
    Storage() KV              // 每个插件独立的命名空间，SQLite 落盘
    Preferences() Preferences // 读取设置界面填写的偏好，缺省时取 manifest 默认值
    Clipboard() Clipboard     // 读写系统剪贴板
    Events() Events           // 订阅 clipboard-changed、window-shown、window-hidden、locale-changed、config-changed
    I18n() Localizer          // 限定在本插件命名空间的翻译与格式化（§10）
    DataDir() string
    CacheDir() string
    Go(fn func())             // 启动后台 goroutine，panic 由宿主兜底（见 reliability.md）
}
```

和最初的设计相比，`Open`、`Paste`、`Notify`、`HideWindow` 不在 Host 上。插件通过返回 `ActionDo` 或 `Result`（Toast、Hide）来表达这些意图，由宿主统一执行，这样所有副作用都能在 CLI 里观测到。

`internal/host` 的实现：

- **`host.Services` / `host.Host`**：真实实现，每个插件一份，`internal/app` 负责装配。
- **`hostfake`**：内存实现，供插件单元测试使用。
- **假环境**：`glctl` 和 `core` 的测试用 `app.Build(Fake: true)` 构建，里面是内存数据库、内存配置和假剪贴板。

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
  1. **通用**：呼出快捷键（录制控件）、开机自启（`SetOpenAtLogin`）、窗口位置、失焦隐藏开关、界面语言（默认"跟随系统"；第一版只有英文资源，只有一种语言时下拉框不显示）
  1. **外观**（R51，[themes.md §7](themes.md)）：主题画廊、跟随系统时的浅色/深色主题、材质和背景不透明度、强调色、圆角/密度/字号、窗口整体不透明度、自定义主题
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

- **无头**：直接构建 `core.Engine`、`core.Controller` 和插件，不需要 GUI 进程，也不依赖 WebView2 或桌面会话。CI 和 Agent 沙箱都能跑。
- **默认隔离**：默认就是假环境，包括内存配置、内存数据库、假剪贴板和临时数据目录，不会碰用户的真实数据。要用真实环境必须显式加 `--real`。
- **确定性**：
  - `--fixtures DIR`：从 `apps.json` 读取应用列表；`clipboard.json` 按时间顺序回放进假剪贴板，每条都触发 `clipboard-changed`。示例在 `testdata/fixtures/`。
  - `--now`：假环境下默认固定为 `2026-10-14T10:00:00Z`，保证 frecency 和相对时间可复现。
  - `--locale en`：默认值，不读系统语言，保证 golden 文件与机器无关。
- **机器可读**：默认输出 JSON，与 `core.Snapshot`、`core.Section` 同构。`--format text` 输出人类可读格式。
- **无副作用**：动作的副作用由 `Recorder` 记录，作为 `effects` 输出，并不真正执行。只有 `--real --exec` 才真正打开程序、写剪贴板。

### 7.2 命令

```text
glctl query <text>                         # 根搜索，输出排序后的分组结果
glctl run <plugin>/<command> [--text t]    # 运行命令，输出 outcome、effects 和视图快照
glctl session new -s FILE [env flags]      # 开始多步会话
glctl session type|key|select|action|show -s FILE [args]
glctl session hide|reopen [root] -s FILE  # 模拟窗口隐藏再呼出（keep-view.md）
glctl session drop -s FILE <path>...      # 把文件拖到窗口上（R91，ai.md §24）
glctl session choose -s FILE [<path>...]  # 下一次选择文件对话框的结果（不带路径即取消）
glctl session remove-chip -s FILE <id>    # 点卡片的 ×
glctl plugin list | plugin info <id>
glctl config path | get [key] | set <key> <value>   # 总是读写真实配置文件；key 是点分路径
glctl i18n check [--src DIR]               # 检查消息目录和代码里的 key 引用（§10.5）
glctl attach show|hide|type|key|select|snapshot [arg]  # 通过 unix socket 驱动正在运行的 GUI
glctl bench [--n N] [queries...]           # 根搜索延迟 p50/p95
```

第一版没有做 `ui snapshot`、`index rebuild`、`plugin validate`：
- UI 截图在 `internal/ui` 的测试里用 `ui.Render` 生成。
- 重建应用索引可以用 `glctl run builtin.apps/reload`。

### 7.3 输出示例

```text
$ glctl query "12*3+4" --format text
[Calculator]
> 40 — 12*3+4   builtin.calc/result  [copy, copy-expression Ctrl+Shift+C, paste Ctrl+Shift+V]
```

JSON 输出的完整样例见 `internal/cli/testdata/golden/`。

### 7.4 会话（多步交互）

会话文件只保存环境参数和步骤列表。每次调用都会重建假环境，把所有步骤重放一遍，再执行新步骤，最后输出这一步的 outcome、effects 和快照。因为环境是确定的，重放的结果和"真的一直开着"完全一样，而且会话文件可以直接提交，作为测试脚本。

```bash
glctl session new -s s.json --fixtures testdata/fixtures
glctl session type -s s.json "clipboard history"
glctl session key  -s s.json Enter          # Push 到剪贴板视图
glctl session type -s s.json "git"
glctl session key  -s s.json Enter          # effects: Paste "…"，outcome: hide
glctl session key  -s s.json Ctrl+K         # 打开动作面板
glctl session action -s s.json builtin.clipboard/1 pin
```

### 7.5 测试分层

| 层级 | 工具 | 运行环境 |
|------|------|----------|
| 插件单元测试 | `go test` + `hostfake` | 任意 |
| 引擎与集成测试 | `core` 测试 + `glctl` golden JSON（`internal/cli/testdata/golden/*.json`，`go test ./internal/cli -update` 更新） | 任意 |
| UI 组件测试 | `ui.NewTester`（`Type`/`Key`/`Find`） + `ui.Render` 截图比对 | 任意（离屏） |
| 端到端测试 | `glctl attach` 通过 `%APPDATA%\golauncher\control.sock` 连接真实 GUI 实例 | Windows 桌面会话 / `windows-latest` CI |

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
| 打开 | `Enter` | 和开始菜单一致：有快捷方式就打开 `.lnk`（保留工作目录、参数和任务栏分组），否则打开 `shell:AppsFolder\<AUMID>` |
| 以管理员身份运行 | `Ctrl+Shift+Enter` | `ShellExecuteExW` + `runas` |
| 在资源管理器中显示 | `Ctrl+Shift+F` | — |
| 复制路径 | `Ctrl+Shift+C` | — |
| 固定到收藏 | — | — |
| 隐藏此应用 | — | 写入插件配置 |

**搜索字段**：显示名、文件名、拼音、自定义别名。

### 8.2 `builtin.calc`：计算器

- **模式**：`inline`，在根搜索里直接显示结果。只有当输入"看起来像表达式"时才输出，避免干扰普通搜索。判断标准：含数字和运算符，或以 `=` 开头。
- **实现**：自写 Pratt parser，数值用 `math/big.Float`（精度 128 bit），避免 `0.1+0.2` 这类浮点误差。带单位的值在解析时就换算成基本单位，同时记住书写时的单位用于显示。
- **第一版支持**：
  - 四则运算、`%`、`^`、括号
  - 常量 `pi`、`e`
  - 函数 `sqrt`、`sin`、`cos`、`tan`、`log`、`ln`、`abs`、`round`、`floor`、`ceil`
  - 进制 `0x`/`0b` 及转换（`255 in hex`）
  - 千分位格式化；整数在 1e21 以内精确显示（如 `2^64`）。`ans`（上一次结果）第一版未实现
- **与语言的关系**：
  - 输入语法与语言无关，固定用 `.` 作小数点、英文函数名，保证表达式在任何语言下含义不变。
  - 结果显示按当前语言格式化（千分位、小数点），由 `Localizer` 的数字格式化负责。
  - "复制结果"复制的是不带千分位的原始数字，不受语言影响。
- **单位换算（R28，已实现）**：数字可以带单位，按量纲做类型检查（`10 km in mi`、`5 GB / 100 Mbps`、`30 ℃`），另有汇率（`network` 权限，后台刷新、离线用缓存）、时间戳、颜色、尺码。单位库在 `plugins/calc/units`。详见 [unit-conversion.md](unit-conversion.md)。
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
- 独立呼出快捷键（默认 `Alt+Shift+V`，直接打开历史视图；不用 `Ctrl+Shift+V`，因为全局注册会抢走各应用的"粘贴为纯文本"）

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
| 打包 | `go build -ldflags "-H windowsgui"`；用 `go-winres` 嵌入图标和 manifest；安装由 `golauncher.exe` 自己完成（[self-install.md](self-install.md)）；自动更新是自己实现的（GitHub Releases + SHA-256，可选 ed25519 签名；没有用 MyGo `plugins/updater`，原因见 [auto-update.md §2](auto-update.md)） |
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
| 消息库 | 自研的小型消息目录（`internal/i18n`）：JSON 资源，值为字符串或 CLDR 复数形式对象；`text/template` 命名参数（`{{.name}}`、`{{.Count}}`）；复数规则来自 `golang.org/x/text/feature/plural`。没有用 go-i18n，这样 key 检查和伪语言生成更容易控制 |
| 语言匹配 | `golang.org/x/text/language` 的 `Matcher`。系统语言来自 `GetUserPreferredUILanguages`，与可用资源匹配，匹配不上时用 `en`。置信度低于 High 的匹配（如繁体中文 zh-TW 对 zh-CN）也算匹配不上 |
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
- **搜索关键字**：当前语言的 keywords 和英文 keywords 同时参与匹配，命令的英文标题也作为关键字。即使用户切换了界面语言，原来用英文搜索命令的习惯也照样能用。中文关键字和标题一样，可以用拼音和拼音首字母匹配。

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

> 2026-10-15 更新：增加状态列；根据实际进展和新需求（见 [requirements.md](requirements.md)）调整了 M5 之后的规划。

| 阶段 | 内容 | 验收 | 状态 |
|------|------|------|------|
| **M0 技术验证** | 用 MyGo 实现：无边框置顶窗口 + 全局热键 + TextInput + 虚拟列表；确认 `WindowOptions` 能力、焦点抢占、渲染性能 | 能呼出、搜索、`Esc` 隐藏 | ✅ 改为直接研读 MyGo 源码和它自带的 launcher 示例，结论融入了 §2、§9，没有单独写 `spike-mygo.md` |
| **M1 核心 + CLI + I18N 基础** | `pkg/plugin`、`core.Engine`、Controller、Ranker、`hostfake`、`glctl`；`internal/i18n`、`en.json`、`en-XA`、`glctl i18n check`、规范检查 | golden 测试通过；i18n 检查和规范检查进入 CI | ✅ 规范检查用 `go/ast` 测试实现（`internal/lint`） |
| **M2 内置插件** | `calc`、`apps`、`clipboard`；之后追加 `everything`（R8） | 各插件单元测试 + `glctl` 集成测试 | ✅ |
| **M3 GUI 完整化** | 导航栈、动作面板、详情预览、确认对话框、主题、托盘 | `ui.NewTester` 覆盖主要交互 | ✅ |
| **M4 设置界面** | 通用、插件、高级、关于；偏好表单自动生成；热键录制；凭据管理器；导入导出 | 设置修改即时生效 | ✅ |
| **v0.1.0 发布**（原 M6 的一部分） | 发布工程（版本号、图标、CI、自动发布）、代码审查与修复、稳定性（R11、R12、R14）、自安装（R15）、文档 | CI 全绿；GitHub Release 包含 amd64 和 arm64 两个 zip | ✅ 功能齐备（含自安装，[self-install.md](self-install.md)）；发布记录见 [release.md](release.md) |
| **M5 插件平台（v0.2）** | ① `Host.Invalidate`：通知后再拉取（[plugin-render-model.md](plugin-render-model.md) 第一级）<br>② Form 视图和命令参数输入<br>③ 设置项扩展：按钮型、动态选项、分组和条件显示（[plugin-settings.md](plugin-settings.md)）<br>④ 外部插件：JSON-RPC、`extproc`、`rpcserve`；进程隔离、崩溃重启和熔断（[reliability.md §3.3](reliability.md)）；协议预留 `ui/render` 推送快照（第二级）<br>⑤ 兼容 Raycast Script Commands（[调研方案 C](raycast-extensions-research.md)） | 用 Go 和 Python 各写一个示例外部插件并跑通；外部插件崩溃不影响宿主；剪贴板和应用视图能自动刷新 | ✅ 已在 main 上实现（[m5-plugin-platform.md](m5-plugin-platform.md) §11–§13），随 v0.2.0 发布 |
| **v0.3.0 发布**（R37） | 单位换算与汇率（R28）、系统命令与设置直达、终端与 WSL、网络工具（R31）、简体中文界面（R35）；SDK 增加 `Action.EndsSession` 和 `Clipboard.WriteSecret` | 五个分支重放到 `main`、脱敏、CI 全绿；Release 为正式版 | ✅ |
| **M6 分发与维护（v0.4）** | 自动更新（R54，自己实现，不用 MyGo `plugins/updater`）、代码签名、计算器补全（`ans`；单位和汇率换算已在 v0.3.0 完成）、`Tab` 进入命令参数、快捷键显示名本地化；外部插件首次运行提示（调研指出"不签名、不提示"对第三方 JS 生态不够，需要你重新决定） | 满足 §11 指标；`en-XA` 截图无写死文字 | 🚧 自动更新已实现（待合并，[auto-update.md](auto-update.md)）；其余 📋 |
| **M7 Raycast 扩展兼容评估** | 约 2 人周的 Node sidecar 原型：自写 `@raycast/api` 子集和 React reconciler，通过 `ui/render` 推送；挑 3–5 个支持 Windows 的纯列表扩展试跑 | 根据结果决定是否投入方案 B（8–12 人周） | 📋 等你决定（R13） |
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

## 15. 第一版实现状态（2026-10-14）

**已完成（M0–M4 的范围）**：

- 插件 SDK
- 引擎和 Controller
- 排序：模糊匹配、拼音、frecency、别名
- I18N 管线：伪语言 `en-XA`、`glctl i18n check`
- 三个内置插件
- `glctl`：query、run、session、plugin、config、i18n、attach、bench
- GUI：Panel 启动窗、动作面板、确认对话框、设置窗口、托盘、全局和命令快捷键、单实例、开机自启
- E2E 控制通道

测试基线：

- `go test ./...` 和 `-race` 全部通过。
- 325 个真实应用时，根搜索 p95 约 1.6ms。

**与设计不同的地方**（详见各节）：

- Host 上没有 Open/Paste/Notify，所有副作用都通过 `ActionDo` 由宿主执行（§5.4）。
- `glctl` 默认在假环境中运行（§7）。
- I18N 用自研的消息目录，没有用 go-i18n（§10.2）。
- 新增 Destructive 命令和动作的确认流程（§5.3）。

**v0.1.0 发布前补充完成**：

- 新增 Everything 文件搜索插件：纯 Go 实现 IPC，不需要 DLL；没装 Everything 时插件不启用（SDK 新增 `plugin.ErrUnavailable`），设置页显示原因。
- `password` 偏好改存 Windows 凭据管理器；设置页支持配置的导出和导入。
- 规范检查（§5.1、§10.5）：用基于 `go/ast` 的测试实现（`internal/lint`），没有引入 go/analysis 依赖。
- 修复发布前审查发现的问题：
  - 粘贴失败：`SendInput` 的结构体布局错误。
  - 并发调用 `Controller.Wait` 时崩溃。
  - SQLite 路径含 `#`、`%` 时出错。
  - `config.json` 损坏时程序无法启动；现在会自动恢复为默认配置，并弹通知。
  - 数据库和剪贴板历史从漫游目录移到 `%LOCALAPPDATA%`。
  - E2E 控制通道改为只在设置 `GOLAUNCHER_CONTROL=1` 时开启。
  - 剪贴板历史的默认快捷键改为 `Alt+Shift+V`：全局注册 `Ctrl+Shift+V` 会抢走各应用的"粘贴为纯文本"。
- 发布工程：版本号注入、图标和版本资源、CI、tag 触发的自动发布（amd64 和 arm64）。

**尚未实现 / 已知限制**：都已排入 §12 的里程碑。

- **M5**：外部插件和进程隔离、Form 视图和命令参数、设置项扩展、`Host.Invalidate`、Script Commands 兼容。
- **M6**：~~自动更新~~（R54 已实现，待合并）、代码签名（Authenticode；更新包的 ed25519 签名已可选启用）、计算器补全（`ans`）、`Tab` 进入命令参数、快捷键显示名本地化。
- **M7**：Raycast 扩展兼容评估。

其他文档：

| 主题 | 文档 |
|------|------|
| 需求台账 | [requirements.md](requirements.md) |
| 稳定性 | [reliability.md](reliability.md) |
| 存储与迁移 | [storage.md](storage.md) |
| 主题 | [themes.md](themes.md) |
| Everything | [everything.md](everything.md) |
| 插件设置 | [plugin-settings.md](plugin-settings.md) |
| 渲染模型 | [plugin-render-model.md](plugin-render-model.md) |
| Raycast 调研 | [raycast-extensions-research.md](raycast-extensions-research.md) |
| 发布 | [release.md](release.md) |
| 自安装 | [self-install.md](self-install.md) |