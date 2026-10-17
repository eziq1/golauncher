# 代码审查报告

审查日期：2026-10-17

## 1. 范围与总体结论

本次审查围绕架构设计、代码整洁度和重复代码展开，重点检查了核心装配、Engine、Controller、Host、配置、UI 和多个内置插件，并扫描了非测试 Go 文件中的重复片段。本次审查未修改业务代码。

**总体判断：架构方向合理，代码总体可读，测试基础较好；主要问题是插件生命周期管理不完整，以及相似插件之间开始出现明显复制。** 建议保留现有分层，做针对性整理。

下述问题来自静态代码审查；现有测试通过并不代表已覆盖这些触发条件。代码行号以审查时的工作区版本为准。

## 2. 优先处理的问题

### 2.1 [P1] 禁用剪贴板插件后，仍然会后台记录

代码位置：

- [internal/core/engine.go:177](../internal/core/engine.go#L177)
- [plugins/clipboard/clipboard.go:144](../plugins/clipboard/clipboard.go#L144)

Engine 初始化所有插件，没有跳过配置中标记为 `Disabled` 的插件；配置变化时也只更新偏好，没有停止插件。剪贴板插件始终订阅剪贴板事件，记录路径没有检查启用状态。

因此，用户关闭插件开关后，搜索入口消失，但新复制的内容仍可能写入历史数据库。这是需要优先修复的隐私行为问题。

建议明确插件启用、停用的生命周期，让后台订阅和采集随之停止，并覆盖“启动时已禁用”和“运行中禁用”两个场景。

### 2.2 [P2] 并发更新配置时，监听器可能收到乱序状态

代码位置：

- [internal/config/config.go:537](../internal/config/config.go#L537)
- [internal/core/engine.go:204](../internal/core/engine.go#L204)
- [internal/ui/launcher.go:130](../internal/ui/launcher.go#L130)

`Store.Update` 在锁内保存配置，解锁后调用监听器。两个并发更新可能按 A → B 保存，却按 B → A 通知。

Engine 会直接用通知中的配置覆盖插件偏好，UI 外观也采用类似方式，最终可能出现“磁盘和 `Config.Get()` 是 B，运行时却回到 A”。这是逻辑竞态，`-race` 不一定发现。

建议按提交顺序分发通知，或使用版本号丢弃过期通知，并通过可控的并发交错验证最终状态。

### 2.3 [P2] 插件初始化失败时，资源回收不完整

代码位置：

- [internal/core/engine.go:177](../internal/core/engine.go#L177)
- [internal/core/engine.go:307](../internal/core/engine.go#L307)

Engine 遇到 `Init` 错误或 panic 后直接跳过该插件；`Engine.Close` 只关闭成功加载的插件。失败插件若已创建订阅、goroutine 或打开资源，就没有统一清理路径。

同样，后续插件注册失败导致 `New` 返回时，也不会回滚此前已初始化的插件。

建议明确失败初始化的清理契约，并为装配过程增加逆序回滚，覆盖初始化中途失败以及多个插件装配中途失败的场景。

## 3. 架构设计评价

### 3.1 值得保留的设计

- `core` 与 UI 解耦，GUI、CLI 共用引擎和 Controller，行为一致性较好。
- 插件采用声明式视图和可序列化动作，适合测试及跨进程协议。
- 外部插件有独立进程、超时、退避和熔断机制。
- 仓库通过测试检查插件导入边界、后台 goroutine 和国际化约定。

现有架构没有表现出需要整体重写的问题，应优先补齐生命周期和并发一致性。

### 3.2 启动路径上的同步工作需要收敛

代码位置：[plugins/scripts/scripts.go:149](../plugins/scripts/scripts.go#L149)。

脚本插件初始化时同步扫描用户配置的目录，而引擎的 `Init` 调用没有超时。目录位于缓慢或失联的网络盘时，可能拖住整个启动过程。

建议沿用应用索引插件的做法，将扫描放到后台，让初始化只完成必要的状态建立。也应明确初始化和关闭阶段的时间预算及取消约定。

## 4. 代码整洁度评价

命名、注释和文件划分基本一致，代码总体可读。但部分文件职责已经偏多，增加了定位和修改成本。

| 文件 | 审查时行数 | 建议 |
|---|---:|---|
| [internal/core/controller.go](../internal/core/controller.go) | 1,210 | 将刷新调度、推送合并与键盘交互按职责拆文件 |
| [internal/ui/settings.go](../internal/ui/settings.go) | 1,121 | 分离插件偏好控件、密码配置、导入导出 |
| [internal/ui/launcher.go](../internal/ui/launcher.go) | 1,023 | 分离搜索栏、结果列表、动作面板的渲染 |

这些首先是维护成本问题。拆分可先在原 package 内完成，暂时不需要引入新的框架或复杂抽象；文件长度本身也不等同于设计错误。

## 5. 重复代码

确实存在大段重复代码。以下是扫描中发现的部分片段：忽略空行和整行注释后，仍逐行相同。统计表示对应片段的相同有效行数，不是整个文件或仓库的重复率，也不代表已覆盖所有结构相似的实现。

| 重复内容 | 位置一 | 位置二 | 相同有效行 |
|---|---|---|---:|
| 已安装列表等待、刷新调度 | [plugins/homebrew/homebrew.go:472](../plugins/homebrew/homebrew.go#L472) | [plugins/winget/winget.go:478](../plugins/winget/winget.go#L478) | 42 |
| 关闭插件、启动后台任务 | [plugins/homebrew/homebrew.go:259](../plugins/homebrew/homebrew.go#L259) | [plugins/winget/winget.go:253](../plugins/winget/winget.go#L253) | 30 |
| 整秒刷新循环 | [plugins/clock/refresh.go:151](../plugins/clock/refresh.go#L151) | [plugins/timer/refresh.go:132](../plugins/timer/refresh.go#L132) | 33 |
| Windows 参数转义 | [internal/winapi/cmdline/cmdline.go:12](../internal/winapi/cmdline/cmdline.go#L12) | [plugins/files/ops.go:20](../plugins/files/ops.go#L20) | 23 |

### 5.1 建议的去重方式

最值得立即消除的是参数转义重复：已有公共 `cmdline.Quote` / `cmdline.Join`，文件插件直接复用即可。

其次可以提取后台刷新合并、缓存读写等小组件。包管理器各自的命令规划和数据解析差异较大，适合继续独立保留，避免为了去重构造过大的通用包管理器框架。

整秒刷新循环也可以考虑提取为小型、可测试的调度辅助组件，保留各插件自己的视图和状态管理。

## 6. 验证结果与限制

在 Windows 环境下执行：

- `go test ./...`：通过。
- `go vet ./...`：通过。
- 核心、配置、Host、存储、lint、插件 SDK，以及 Homebrew、winget、Scoop、时钟和计时器的专项测试：通过。

本次未运行 `-race`，未进行 macOS 实机验证，也未为上述静态发现新增复现测试。

## 7. 建议处理顺序

1. 修复禁用插件后仍然采集数据的行为。
2. 保证配置通知顺序与最终运行时状态一致。
3. 补齐初始化失败及装配失败时的生命周期清理。
4. 消除参数转义等已有公共实现的重复代码，再提取小型公共组件。
5. 按职责拆分大文件，并逐步减少启动路径上的同步扫描。

## 8. 核实结论（2026-10-17，R87）

逐条对照代码核实，结论属实：

- **2.1**：属实，而且范围更大。"禁用"只在搜索时过滤（`engine.go` 的 `enabled`、`root.go`），`Init` 照常执行，剪贴板监听始终开着，所以**所有禁用的插件都还在后台运行**（计时器提醒、包管理器刷新索引等）。应该作为插件生命周期问题整体修：禁用的插件不执行 `Init`，运行中禁用时 `Close`、启用时 `Init`，AI 工具层也不暴露它。
- **2.2**：属实，触发概率很低（`Store.Update` 出锁后才通知快照）。
- **2.3**：属实，实际影响小（现有插件在订阅前就返回错误；`New` 失败后程序退出）。
- **3.2**：属实（`scripts.Init` 同步调用 `rescan`）。
- **4、5**：属实（`plugins/files/ops.go` 的 `quoteArg`/`quoteArgs` 与 `cmdline.Quote`/`Join` 重复等）。

修复时先为每个问题写复现测试，再改代码。顺序：生命周期 → 配置通知 → 初始化失败清理 → 脚本后台扫描 → 去重 → 拆大文件。

## 9. 修复记录（R88 第一批：2.1–2.3）

每条都先写复现测试，确认在修复前的代码上失败，再改代码。

### 9.1 插件生命周期（2.1）

契约写在 [plugin-guide.md "Lifecycle"](plugin-guide.md) 和 [design.md §5.3.1](design.md)，SDK 注释见 `pkg/plugin/plugin.go`。要点：

- `core.New` 先登记全部插件，只 `Init` 已启用的；禁用的插件保留 manifest、消息目录和偏好，设置页照常显示、可以启用。
- 配置变化后 Engine 在后台按最新配置收敛（`internal/core/lifecycle.go`）：禁用即从所有调用路径摘除再 `Close`（guard 兜底，最多等 10 秒），启用即用新 `Host` 再次 `Init`。Controller 关闭该插件的视图、回到根搜索；AI 工具层本来就按 `Enabled` 过滤，现在 Engine 层也拒绝调用。
- 每轮运行结束后吊销 `Host`（`host.Host.Revoke`）：退订通过它订阅的事件，丢弃 `Invalidate`/`Render`/`Toast`/`RunProcess`/`RunTask`。插件忘了退订也收不到事件。
- 后台任务：正在运行的让它跑完（不打断安装），吊销的 Host 不能再开新任务。
- 外部插件：`Close` 停止进程；`Proxy.Init` 现在可以在 `Close` 之后重新使用。
- 重新 `Init` 要等上一轮彻底结束：`Close` 返回、上一轮的事件回调（`Host.Drain`）和 Engine 放弃等待的调用都已退出。`-race` 在 `TestPluginsStartAgain` 里抓到过上一轮的 `config-changed` 回调与新一轮 `Init` 并发读写，这样修掉。
- 重新 `Init` 用的是同一个插件值，所以各内置插件的 `Init` 开头都补了"从 `Close` 留下的状态重新开始"（closed 标志、context、被关掉的 channel；winget/homebrew/scoop 回到新建时的状态；AI 的后端 `Reopen`）。

复现测试（修复前均失败）：

- `plugins/clipboard`：`TestDisabledAtStartupRecordsNothing`（修复前历史里有"secret while off"）、`TestDisabledAtRunTimeRecordsNothing`（同上；重新启用后能继续记录）。
- `plugins/timer`：`TestDisabledSendsNoNotifications`（修复前禁用后仍发出 1 条通知；修复后不发，重新启用时补报期间到点的计时器）。
- `internal/core`：`TestDisabledAtStartupIsNotInitialized`（修复前 Init 了 1 次）、`TestDisabledAtRunTimeIsClosed`（修复前 0 次 Close；还检查视图关闭、事件/失效/Toast 被丢弃、重新启用后新 Host）。
- `internal/builtin`：`TestPluginsStartAgain`，把每个内置插件关了再开、比对根搜索结果（没改插件时 winget 重新 `Init` 会 panic："close of closed channel"）。
- 另有 `internal/ui` 的 `TestSettingsPlugins`（设置页开关真正关闭/启动插件）、`internal/agent` 的 `TestDisabledPluginNotExposed`、`internal/extproc` 的 `TestCloseStopsProcess`（关闭后能再次 Init）。

### 9.2 配置通知顺序（2.2）

`config.Store` 改为单一投递者：`Update` 提交后若没有别的 goroutine 正在通知，就由它循环投递"当前最新配置"，直到监听器看过最后提交的版本；投递期间其他 goroutine（或监听器自己）调用 `Update` 只提交、不通知，由正在投递的 goroutine 在本轮之后补发。因此监听器按提交顺序看到配置，最后一次一定是存储里的值；中间版本可能被合并。监听器里调用 `Get`/`Update` 不会死锁。

复现测试：`internal/config` 的 `TestNotifyInCommitOrder`（修复前监听器依次收到 `["B" "A"]`，停在旧值 A），以及 `TestNotifyNestedUpdate`（监听器里嵌套 `Update`）。

### 9.3 初始化失败的清理（2.3）

契约：`Init` 返回错误（含 `ErrUnavailable`）或 panic 后，宿主调用一次 `Close`，并吊销这一轮的 `Host`；`Close` 必须能处理只做了一半的 `Init`。`core.New` 中途失败时，逆序关闭已初始化的插件；重复 ID 和消息目录错误在任何 `Init` 之前就报出。`Engine.Close` 按 `Init` 的逆序关闭（运行中重新启用的插件排在最后）。

复现测试（`internal/core`，修复前均失败）：`TestFailedInitIsCleanedUp`（修复前 0 次 Close，失败的插件还收到事件）、`TestNewRollsBack`（插件数据目录建不出来时，修复前已 Init 的 a、b 没有 Close；重复 ID 同理）、`TestCloseReverseOrder`。

## 10. 修复记录：3.2 与 5（R88 第二批）

### 10.1 脚本插件启动扫描（3.2）

- 复现测试 `plugins/scripts/startup_test.go`：注入一个阻塞的目录读取（`readDir`），修复前 `Init` 卡住超过 2 秒（测试失败），修复后立即返回。
- 修复：`Init` 只建立状态，首次扫描（含创建默认目录）通过 `Host.Go` 在后台进行。扫描完成前的根搜索返回已知结果并记下 `missed`，扫描完成后若有这样的搜索就调用 `Invalidate("")`，与时钟插件的 `loaded`/`missed` 做法相同；没有搜索则不通知。
- glctl 假环境和原有测试用新增的 `Options.Sync`，在 `Init` 里同步扫描，结果确定，golden 不变。
- 未改：`Close` 仍等待后台扫描结束，挂起的网络目录仍可能拖住退出。关闭阶段的时间预算属于插件生命周期约定（第一批），这里没有处理。

### 10.2 重复代码（5）

- **参数转义**：删除 `plugins/files/ops.go` 的 `quoteArg`/`quoteArgs`，改用 `internal/winapi/cmdline.Quote`/`Join`。插件导入规则本来就允许 `internal/winapi/`，lint 不用改。`TestQuoteArg` 改测 `cmdline`，保留原用例，并补充空参数、末尾反斜杠、引号、制表符、`Join` 等情况。
- **后台刷新与关闭**：新增 `pkg/plugin/bg`（插件导入规则允许 `pkg/`），只放不带插件状态的小组件：
  - `Group`：经 `Host.Go` 启动后台任务，`Close` 后不再启动，`Wait` 等待结束；
  - `Refresher`：同一时间只跑一次刷新，运行中再次请求只会在结束后补跑一次，`Group` 关闭后停止补跑；
  - `Ready`：首个结果就绪的标记，可带时限等待（等待者）。
  homebrew、winget、scoop（与 winget 完全相同的部分）的 `Close`/`spawnLocked`、`scheduleIndex`/`scheduleInstalled`/`scheduleRefresh`、`waitIndex`/`waitInstalled` 改用这些组件。命令规划、输出解析、过期判断（`staleAfter`）和刷新后的 `Invalidate` 条件仍由各插件自己负责，没有做通用包管理器框架。
- **整秒刷新循环**：`bg.SecondLoops`（在下一个整秒之后 `TickSlack` 触发，`Host`、`After`、`WaitGroup` 可注入）和 `bg.UntilNextSecond` 替代 clock 与 timer 各自的 `loop`/`untilNextSecond`。两个插件的 `Options.After` 注入点不变，视图、根搜索漏拉计数等状态仍由各自维护。
- 组件有单独的测试（`pkg/plugin/bg/bg_test.go`，含 `-race`）。各插件测试通过（只把读取内部计数的几处改成 `loops.Running()`、`listing.Running()`），`go test ./internal/cli` 的 golden 没有改动。

仍有重复但这次没动的（结构相同，但牵涉插件自身的状态或类型，提取收益不大）：

- clock 与 timer 的 `rootLoop`/`rootTick`/`viewPulled`/`ViewClosed`/`stopAll`（约 35 行）：漏拉计数和视图表，依赖各自的锁和视图类型；
- homebrew 与 winget 的 `loadInstalledCache`/`saveInstalledCache`、`installedState` 的失败合并（约 40 行）：`Installed` 类型不同，需要泛型；
- homebrew、winget、scoop 的 `maxResults`、`rootQuery`、`failure`、`truncate` 等小函数（每个几行）；
- scripts、terminal、apps 各自的 `spawn`（与 `bg.Group` 相似，但和各自的关闭状态、运行表绑在一起）。

## 11. 修复记录：拆分大文件（4）

三个大文件在原 package 内按职责拆开，只移动代码：标识符、签名、函数体和注释都没改，每个声明连同它前面的注释整体搬到新文件。各文件只删掉了不再用到的 import，新文件开头加了一行说明职责的注释（放在 `package` 之后，不算包文档）。

`internal/core`（原 `controller.go` 1,238 行）：

| 文件 | 行数 | 职责 |
|---|---:|---|
| `controller.go` | 330 | `Controller` 及其状态类型、构造、`Wait`、导航栈、`Reset`/`SetText`、搜索与 `setView` |
| `controller_refresh.go` | 130 | 刷新调度：合并 `Host.Invalidate`（`invalidateDelay`/`renderInterval`）、插件启停和上下文变化后的刷新 |
| `controller_push.go` | 83 | 推送视图：`Host.Render` 按 `renderInterval` 合并到打开的视图实例 |
| `controller_rows.go` | 142 | 当前层的行：本地过滤、选中项保持、`Select`/`Hover`、动作面板的动作过滤 |
| `controller_keys.go` | 208 | 键盘与导航：`Key`、`back`、`panelKey`、`KeyTasks` |
| `controller_actions.go` | 203 | 执行动作和命令（`Activate`/`RunAction`/`OpenCommand`/`run`/`execFrom`）、后台任务、Toast、`CopyContextPath` |
| `controller_snapshot.go` | 201 | `Snapshot` 及各子快照类型、快照构建 |

`internal/ui`（原 `settings.go` 1,121 行、`launcher.go` 1,023 行）：

| 文件 | 行数 | 职责 |
|---|---:|---|
| `settings.go` | 375 | 设置窗口骨架：`Settings`、`SettingsHost`、页面切换、通知、配置更新、通用控件（`setting`/`toggle`/`button`）、快捷键录制 |
| `settings_general.go` | 155 | 通用页（含选中文本设置、`ClipboardHistoryHost`）和关于页 |
| `settings_plugins.go` | 212 | 插件列表页、单个插件页、进程状态、命令行 |
| `settings_prefs.go` | 244 | 插件偏好控件：取值、动态选项、按钮偏好 |
| `settings_password.go` | 74 | 密码偏好：保存/清除系统凭据 |
| `settings_advanced.go` | 124 | 高级页：数据目录、配置文件、日志级别，以及设置导入/导出（`ExportFileName`） |
| `launcher.go` | 272 | 包文档、`Launcher`、`Host`、构造、`View`、主题与 Toast |
| `launcher_input.go` | 162 | 输入：按键、文本、点击转给 `core.Controller` |
| `launcher_searchbar.go` | 103 | 搜索栏 |
| `launcher_results.go` | 234 | 结果列表、行、附属信息、详情 |
| `launcher_footer.go` | 185 | 底栏：上下文/选中文本标签、后台任务指示、动作提示 |
| `launcher_panel.go` | 125 | 动作面板和确认对话框 |

核对方法：用一次性的 Go 程序（未提交）解析拆分前（`HEAD`）和拆分后的 `internal/core`、`internal/ui` 全部非测试文件，按"种类 + 接收者 + 名字"列出每个顶层声明，比较声明连同文档注释的原文：core 247 个、ui 219 个，前后一一对应、原文完全相同；所有注释也逐条相同，只多了新文件的职责注释。`gofmt`、`go vet ./...`（含 `GOOS=darwin`）、`go test ./...`、`go test -race ./internal/core ./internal/ui` 通过，`go test ./internal/cli` 的 golden 没有改动。
