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