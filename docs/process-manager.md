# 进程管理插件设计方案

> 状态：设计 v1 · 已确认（§14）· 第一期已实现（待合并，实现记录见 §15）· 2026-10-16 · 对应需求 R52、R55（见 [requirements.md](requirements.md)）
>
> 需求：增加进程管理插件：① 搜索进程；② 结束进程；③ 查看各进程的资源占用。其他功能自行设计。
>
> 本文的进程名、路径、用户名都是虚构的示例（Contoso、Fabrikam、Northwind）。

## 0. 结论先行

- 新增内置插件 `builtin.processes`（`plugins/processes`）。系统调用放在新包 `internal/winapi/process`；网络插件里现有的 `ProcessInfo` / `Kill` 改为调用这个包，不再各写一份。
- **数据**：每次采样只调用一次 `NtQuerySystemInformation(SystemProcessInformation)`，就能拿到所有进程的 CPU 时间、内存、I/O、句柄、线程数、父进程和启动时间，不需要逐个打开进程。路径、用户、图标、窗口这类字段按 (PID, 启动时间) 缓存，只在第一次见到这个进程时读取。
- **只在显示时采样**：带前缀的根搜索或进程视图正在显示时，每秒采样一次；窗口隐藏、视图关闭或输入不再带前缀，就停止采样。沿用世界时钟和计时器的做法（`Invalidate` / `Render` / `ViewCloser` / `window-hidden`）。
- **按应用分组**（和任务管理器一样）：分成"应用"、"后台进程"、"Windows 进程"三组；同一应用的多个进程（浏览器的 8 个 `fabrikam.exe`）合成一行，可以展开。
- **结束**分三种：结束任务（有窗口的应用先发 `WM_CLOSE` 请它自己退出）、强制结束（`TerminateProcess`）、结束进程树。都要由宿主确认。用"进程句柄 + 启动时间"防止误杀复用了同一 PID 的新进程。系统关键进程不提供结束操作。提升权限的进程通过 UAC 单次提权结束，启动器本身不以管理员身份重启。
- **AI**：搜索、列表、详情是只读；所有结束操作是破坏性的，每次都要问；设置优先级算"改动"。命令行永远不交给 AI。

## 1. 定位与入口

### 1.1 前缀

| 前缀 | 作用 | Enter |
|------|------|-------|
| `ps ` | 搜索进程，查看占用 | 打开进程视图，并选中这一项 |
| `kill` | 搜索要结束的进程 | 结束任务（需要确认） |
| `进程` | 同 `ps` | 同 `ps` |

- 偏好 `prefixes`（stringList）可以改，留空就关闭前缀。规则和计时器、世界时钟一样：ASCII 前缀后面必须是空格或结尾，以汉字结尾的前缀后面可以直接接文字（`进程chrome`）。
- **`ps` 后面必须有空格**，这一点和其他插件不同：很多人输入 `ps` 是为了找 PowerShell 或 Photoshop。只输入 `ps` 时，插件只返回一条普通的"进程"命令条目（不预先打分），不会把应用挤下去；输入 `ps `（带空格）才显示进程列表。`kill` 和 `进程` 不会和应用名冲突，没有这个限制。
- **冲突检查**：`ps`、`kill`、`进程` 都不在现有前缀里（`time` `tz` `timer` `计时` `倒计时` `remind` `提醒` `winget` `wg` `scoop` `sc` `ip` `ping` `dns` `port` `wifi` `tracert`），也不是快速链接的内置关键词（`g` `b` `bd` `ddg` `gh` `so` `yt` `bili` `wiki` `zh` `db` `tb` `jd` `npm` `go` `mdn` `tr` `yd` `map` `amap` `gd`……）。实现时把这三个前缀加进 `plugins/quicklinks/engines.go` 的 `reservedPrefixes`，由那里现有的测试守住。
- 没有默认带 `top`、`task`、`tk`：`tasks` 已经是后台任务的命令，`top` 和 `tk` 太短。用户可以自己加。

### 1.2 命令（manifest）

| 命令 | 模式 | 说明 | 关键词（节选） |
|------|------|------|----------------|
| `processes` "进程" | view | 进程视图（§4.2） | process, task manager, 任务管理器, rwglq, 进程, jc |
| `hung` "结束未响应的应用" | view | 列出所有"未响应"的应用，可以逐个结束或全部结束 | not responding, hung, 未响应, wxy, 卡死 |
| `restart-explorer` "重启 Windows 资源管理器" | no-view，`Destructive` | 任务栏或资源管理器卡住时用（§5.1） | explorer, restart explorer, 任务栏, 资源管理器, cqzyglq |

都不带默认全局快捷键，用户可以自己设置别名和快捷键。

### 1.3 不带前缀时：只处理前台应用

不带前缀的输入**不返回进程**，不干扰普通搜索，也不需要为此采样。只有两处例外，都基于呼出启动器前那个前台窗口（`Query.Context.App`，见 [context-awareness.md](context-awareness.md)）：

| 条目 | 出现时机 | 动作 |
|------|----------|------|
| "退出 Contoso Writer" | `RootItems` 中，由宿主模糊匹配关键词 `quit` `close app` `退出` `tc` `kill` `结束` | Enter：结束任务（先请它自己退出）；Ctrl+Shift+X：强制结束 |
| "强制结束 Contoso Writer（未响应）" | `ContextItems`：**只在前台窗口未响应**（`IsHungAppWindow`）时出现，排在空白根搜索的最上方 | Enter：强制结束（需要确认） |

- 条目标 `Transient`：不进"最近使用"。否则下次从"最近使用"里点开时，结束的是一个早已不存在的 PID。
- 动作的 payload 带着 PID 和启动时间（§5.4），而不是应用名。
- 前台是 UWP 应用时，窗口属于 `ApplicationFrameHost.exe`，要在它的子窗口里找 `Windows.UI.Core.CoreWindow`，取真正的应用进程（§2.3）。
- 前台是桌面或任务栏（`explorer.exe` 拥有 `Shell_TrayWnd` / `Progman`）时不提供"退出"，改为提供"重启 Windows 资源管理器"。
- 关闭情境感知后，这两种条目都不出现。

## 2. 数据

### 2.1 一次快照拿到所有进程：`NtQuerySystemInformation`

| 方案 | 能拿到的信息 | 代价 | 结论 |
|------|--------------|------|------|
| `CreateToolhelp32Snapshot` + `Process32Next` | PID、父 PID、exe 名、线程数 | 每个进程的 CPU 和内存都要再 `OpenProcess` + `GetProcessTimes` / `GetProcessMemoryInfo`；受保护的进程打不开，就没有数据 | 不用（网络插件现在用它来找进程名，以后改掉） |
| **`NtQuerySystemInformation(SystemProcessInformation)`** | 每个进程的 `SYSTEM_PROCESS_INFORMATION`：PID、父 PID、exe 名、`CreateTime`、`UserTime` / `KernelTime`、`CycleTime`、`WorkingSetSize`、`WorkingSetPrivateSize`、`PrivatePageCount`（提交大小）、`PeakWorkingSetSize`、`PageFaultCount`、读写和其他 I/O 的次数与字节数、`HandleCount`、`NumberOfThreads`、`SessionID`、`BasePriority` | 一次调用，不用打开任何进程，受保护的进程（csrss、lsass）也有数据。`golang.org/x/sys/windows` 已经有这个结构体和常量 | **采用** |

- 缓冲区：初始 1 MB，返回 `STATUS_INFO_LENGTH_MISMATCH` 时按返回的大小加 64 KB 重试，最多 3 次；缓冲区在两次采样之间复用，避免每秒分配。
- 系统总量：`GetSystemTimes`（总 CPU）、`GlobalMemoryStatusEx`（已用和总内存）、`GetActiveProcessorCount(ALL_PROCESSOR_GROUPS)`（逻辑处理器数，超过 64 核也正确）。
- PID 0（System Idle Process）只用来校验 CPU 总量，不在列表里显示。

### 2.2 按需补充的字段

快照里没有的字段，在**第一次见到某个 (PID, CreateTime)** 时读取一次，然后缓存，直到进程退出。读取放在采样协程里，失败的字段留空，不影响其他字段。

| 字段 | API | 需要的权限 | 拿不到时 |
|------|-----|------------|----------|
| 完整路径 | `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` + `QueryFullProcessImageNameW` | 受限查询（对提升的同用户进程通常也能打开） | 只显示 exe 名；详情写"受保护的进程" |
| 显示名 | exe 的版本信息 `FileDescription`。`internal/winapi/shell` 已经有读版本信息的代码，提出来共用。按 exe 路径缓存 | 只读文件 | 用 exe 名去掉 `.exe` |
| 图标 | `shell.IconPNG`（已有），按 exe 路径的哈希缓存成 PNG，放在插件的 `CacheDir`，后台生成，生成完 `Invalidate`（和应用插件一样） | 只读文件 | 内置图标 `app` |
| 打包应用 | `GetPackageFullName` / `GetApplicationUserModelId` | 受限查询 | 当作普通进程 |
| 用户 | `OpenProcessToken(TOKEN_QUERY)` + `GetTokenInformation(TokenUser)` + `LookupAccountSidW`，按 SID 缓存名字 | 同用户可以；其他用户、服务的进程常被拒绝 | 按 `SessionID` 推断：会话 0 显示"系统服务"，否则显示"其他用户" |
| 是否提升、完整性级别 | `TokenElevation`、`TokenIntegrityLevel` | 同上 | 未知 |
| 关键进程 | `IsProcessCritical` | 受限查询 | 只靠名单判断（§5.3） |
| 命令行 | `NtQueryInformationProcess(ProcessCommandLineInformation)`（Windows 8.1 起），**只在需要时读取**（详情面板、复制命令行） | 受限查询；跨完整性级别能否读取，实现时实测 | "无法读取（需要管理员权限）" |
| 服务 | `EnumServicesStatusExW(SERVICE_WIN32, SERVICE_ACTIVE)` 给出每个服务的 PID（第二期，用于标出 svchost 托管了哪些服务） | 普通用户可以 | 不显示 |
| 监听端口 | `internal/winapi/network.Sockets()`（已有，按 PID 汇总） | 普通用户可以 | 不显示 |

### 2.3 窗口、应用归属、是否未响应

- 每次采样调用一次 `EnumWindows`（约 1 ms），保留"可见、没有所有者、不是工具窗口、有标题"的顶层窗口，用 `GetWindowThreadProcessId` 对应到 PID。这和任务栏、任务管理器"应用"一栏的判断方式一致。
- **UWP 应用**：顶层窗口 `ApplicationFrameWindow` 属于 `ApplicationFrameHost.exe`。要枚举它的子窗口，找到类名为 `Windows.UI.Core.CoreWindow` 的子窗口，把窗口算到真正的应用进程上。按 `GetPackageFullName` 分组。
- **未响应**：对每个保留下来的窗口调用 `IsHungAppWindow`（不发消息，不会阻塞）。
- 控制台程序：窗口属于 `conhost.exe` / `OpenConsole.exe` 或 Windows Terminal，程序本身算作后台进程。第一期不做"控制台窗口 → 程序"的映射。

### 2.4 指标怎么算

| 指标 | 计算 | 说明 |
|------|------|------|
| CPU % | Δ(UserTime + KernelTime) ÷ (Δ墙钟时间 × 逻辑处理器数) × 100 | 默认按**总量归一**（所有进程加起来最多 100%），和任务管理器一致。偏好 `cpuMode = perCore` 时不除以处理器数（像 `top`，最高 N×100%）。第一次采样没有差值，显示"—"。任务管理器从 Windows 8 起显示的是"处理器效用"（考虑睿频），所以 CPU 频率很高时两者会有几个百分点的差别，这在详情的说明里写明 |
| 内存 | `WorkingSetPrivateSize`（专用工作集） | 和任务管理器"进程"页的"内存"一列一致。详情里另外列出工作集、提交大小（`PrivatePageCount`）、峰值工作集 |
| I/O | Δ(ReadTransferCount + WriteTransferCount) ÷ Δt | 标题写"I/O"，不叫"磁盘"：这些计数包括文件缓存命中和命名管道，不等于物理磁盘读写。任务管理器的"磁盘"一列来自 ETW，需要管理员权限，这里不做 |
| 网络 | **不做逐进程流量** | 没有廉价的 API：`GetPerTcpConnectionEStats` 要先为每个连接打开统计（需要管理员权限），ETW 内核网络事件也要管理员权限。改为在详情里列出监听端口和连接数（`GetExtendedTcpTable`，已有） |
| GPU | 第三期再做 | 只能用 PDH 的 `\GPU Engine(pid_*)\Utilization Percentage`：实例数量很多（每个进程 × 每个引擎），通配展开一次要几十毫秒，还需要两次采样。以后作为默认关闭的偏好 |
| 句柄、线程 | 快照里直接有 | |
| 启动时间、运行时长 | `CreateTime` | 运行时长用 `Host.Now()` 计算，测试可以固定时间 |
| 父进程 | `InheritedFromUniqueProcessID`，并且要求父进程的 `CreateTime` 早于子进程 | 父进程已退出、PID 又被复用时，不会把无关的进程当成父进程 |

### 2.5 采样的生命周期（只在显示时采样）

```
没有使用者 ──(ps 前缀的 RootItems / 打开进程视图)──▶ 立即采样 → 250ms 后第二次采样（有了 CPU 差值）→ 每 interval 一次
     ▲                                                              │
     └──── 视图 ViewClosed、window-hidden、输入不带前缀、两个周期内没有被拉取、Close ◀──┘
```

- 一个插件只有一个采样器，按使用者计数（根搜索 + 每个打开的视图实例）。根搜索用 `Host.Invalidate`，视图用 `Host.Render` 推送整份快照，选中项由宿主按条目 ID 保持。
- 间隔默认 1 秒，偏好 `interval` 可以选 1、2、5 秒。
- `RootItems` 和 `View` **只读最近一次的样本**，从不在调用里采样，所以满足根搜索 150ms 的超时限制。
- 隐藏启动器之后，样本和按需字段的缓存保留 30 秒（下次打开不用重新读路径和图标），之后清空，只保留图标文件。

### 2.6 性能预算

以约 300 个进程、2000 个线程为准（实现时用 `go test -bench` 测量并写进实现记录，数据要匿名化）：

| 项目 | 目标 |
|------|------|
| `NtQuerySystemInformation` + 解析 | < 5 ms |
| 稳定状态下的一次采样（快照 + `EnumWindows` + `IsHungAppWindow` + 端口表 + 分组排序） | < 10 ms，1 秒间隔下约占一个核的 1% |
| 第一次采样（还要为每个进程读路径、用户、提升状态） | < 40 ms；版本信息和图标在后台逐步补上 |
| `RootItems` / `View`（只读样本、匹配、格式化） | < 3 ms |
| 插件常驻内存（采样中） | < 5 MB；停止采样 30 秒后 < 1 MB |
| 不显示时 | 0：没有协程，也没有定时器 |

## 3. 分组、排序、筛选

### 3.1 分组（默认开启，偏好 `groupByApp`）

| 分组 | 条件 |
|------|------|
| 应用 | 这一组里有可见的顶层窗口 |
| 后台进程 | 没有窗口，在当前用户的会话里 |
| Windows 进程 | 会话 0，或者是 `%SystemRoot%` 下、由 SYSTEM / LOCAL SERVICE / NETWORK SERVICE 运行的程序。偏好 `showWindowsProcesses`（默认开）可以隐藏这一组 |

**怎样算"一个应用"**：

1. 打包应用按包名（`PackageFullName`）归为一组。
2. 其他进程沿父进程链往上找，**直到父进程的 exe 路径不同为止**，最上面那个进程就是组的根。`fabrikam.exe` 的 8 个子进程归到主进程下面；`Code.exe` 拉起的 `node.exe` 路径不同，不归进去（和任务管理器一样，子进程可以在展开后看到）。
3. 组的指标是成员之和；组的标题是根进程的显示名，后面加成员数（"Fabrikam Browser (8)"）。

关闭分组时，每个进程单独一行，按同样的规则排序。

### 3.2 排序

可以按 CPU（默认）、内存、I/O、名称排序，在操作面板里切换（"排序方式：CPU / 内存 / I/O / 名称"）。选择存在插件的 KV 里，下次打开时沿用。分组之间顺序固定（应用、后台、Windows），排序只在组内生效。

- **防抖动**：每秒重排会让列表跳来跳去。数值相差不到 10% 的两行保持上一次的相对顺序；选中项本来就由宿主按 ID 保持。

### 3.3 筛选（搜索框）

插件自己匹配（视图不设 `FilterLocally`，因为指标每秒在变，而且要支持 PID 精确匹配）：

- 匹配显示名、exe 名、窗口标题、包名、显示名的拼音和拼音首字母（go-pinyin，已在白名单里）；
- 纯数字：PID 精确匹配排第一，其次才是名字里含这个数字的进程；
- 组里任何一个成员匹配，整组都显示，匹配的成员在展开后高亮；
- 第二期：偏好 `searchCommandLine`（默认关）可以按命令行搜索，比如 `ps vite` 找到跑 vite 的那个 `node.exe`（命令行按 (PID, 启动时间) 缓存，只读一次）。

## 4. 界面

### 4.1 根搜索里的样子（`ps fab`）

```
 进程
 ▸ [icon] Fabrikam Browser (8)         新标签页 - Fabrikam       CPU 3.2%  内存 1.2 GB
   [icon] Fabrikam Sync                 后台进程                  CPU 0.1%  内存 42 MB
   [⚙]   在进程视图中打开 "fab"
```

- 第一行（只在 `ps ` 后面没有文字时）是系统汇总："CPU 23% · 内存 58%（11.2 / 31.7 GB）· 312 个进程"，Enter 打开进程视图。
- 带文字时最多列 8 组，按匹配度、再按 CPU 排序；结果是预先打好分的，带前缀时排在其他结果前面。
- 附注（Accessory）用固定格式 `CPU 3.2%  内存 1.2 GB`；`kill` 前缀下附注换成"结束任务"的提示文字。

### 4.2 进程视图（命令 `processes`）

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ 🔍 筛选进程…                                                     [CPU ▾]      │
├───────────────────────────────────────────────┬──────────────────────────────┤
│ CPU 23% · 内存 58% · 312 个进程                 │ Fabrikam Browser              │
│ 应用 (6)                                        │ fabrikam.exe · 8 个进程        │
│ ▸ Fabrikam Browser (8)    CPU 3.2%  1.2 GB      │ CPU        3.2%               │
│   Contoso Writer   未响应  CPU 0.0%  310 MB     │ 内存       1.2 GB（专用工作集）│
│ 后台进程 (41)                                   │ I/O        120 KB/s           │
│   node.exe                CPU 1.1%  180 MB      │ PID        4120               │
│ Windows 进程 (98)                               │ 路径       C:\Program Files\… │
│   Desktop Window Manager  CPU 0.8%  95 MB       │ 用户       user（未提升）     │
│                                                 │ 启动于     今天 09:12（3 小时）│
├─────────────────────────────────────────────────┴──────────────────────────────┤
│ 结束任务 ↵   │  操作 Ctrl+K                                                       │
└──────────────────────────────────────────────────────────────────────────────┘
```

- 列表分三节（§3.1），节标题带数量；第一行是系统汇总。
- 未响应的应用在标题后面标"未响应"，图标用危险色（`TintDanger`）。
- 选中一组后按 `Ctrl+D`，推入**组视图**：列出组里的所有进程，按进程树缩进（"└ "），同样实时刷新、有详情面板。没有成员的单个进程按 `Ctrl+D` 也进入这个视图，只是只有一行。
- 视图的 `Placeholder` 提示可以输入名字或 PID。

### 4.3 详情面板

| 字段 | 内容 |
|------|------|
| 名称 | 显示名、exe 名；打包应用加包名 |
| CPU、内存、I/O | 当前值；内存另列工作集、提交大小、峰值 |
| PID、父进程 | 父进程的名字和 PID；父进程已退出时写"已退出" |
| 路径 | 完整路径；读不到时写原因 |
| 用户、权限 | 用户名；"已提升" / "未提升"；完整性级别 |
| 启动时间 | 绝对时间和运行时长（相对时间用 `Localizer.RelativeTime`） |
| 句柄、线程 | 数量 |
| 窗口 | 窗口标题，最多 5 个 |
| 监听端口 | `TCP 3000, 5173`（来自网络插件共用的 `winapi/network`） |
| 命令行 | 默认**脱敏**显示（§7），这一行标为敏感（§6） |
| 说明 | 关键进程、svchost 托管的服务（第二期）、"golauncher 本身"、"golauncher 的后台任务" |

### 4.4 操作和快捷键

| 操作 | 快捷键 | 说明 |
|------|--------|------|
| 结束任务 | Enter（`kill` 前缀和进程视图）/ `Ctrl+X` | 有窗口：先请它自己退出；没有窗口：结束进程。见 §5.1 |
| 打开进程视图并选中 | Enter（`ps` 前缀） | |
| 强制结束 | `Ctrl+Shift+X` | `TerminateProcess` |
| 结束进程树 | `Ctrl+Alt+X` | 确认框里写明会结束几个进程 |
| 切换到窗口 | `Ctrl+Enter` | 只有有窗口的应用才有这一项：`SetForegroundWindow`，窗口最小化时先还原 |
| 显示进程 / 展开 | `Ctrl+D` | 推入组视图（§4.2） |
| 在文件资源管理器中显示 | `Ctrl+Shift+F` | `ActionOpen` + `Reveal` |
| 复制 PID / 路径 / 名称 | `Ctrl+Shift+C`（PID）、`Ctrl+Alt+C`（路径） | 名称没有快捷键 |
| 复制命令行 | — | 回调，执行时才读取（不放进动作的数据里） |
| 在网上搜索 | — | 搜索 `"fabrikam.exe"`；用哪个搜索引擎和快速链接插件的默认规则一致（中文界面用 Bing，其他用 Google），见 §13 第 7 条 |
| 设置优先级 | 子菜单（第二期） | 低、低于正常、正常、高于正常、高；不提供"实时" |
| 效率模式 | 第二期 | `SetProcessInformation(ProcessPowerThrottling)` + `IDLE_PRIORITY_CLASS`，和任务管理器的效率模式一样 |
| 以管理员身份结束 | `Ctrl+Shift+A` | 只在因权限不足失败时出现（§5.5） |
| 排序方式、分组开关 | 操作面板 | 保存在 KV |
| 打开任务管理器 / 资源监视器 | 操作面板 | `taskmgr.exe` / `resmon.exe`（任务管理器不支持用命令行参数选中某个 PID） |
| 刷新 | `Ctrl+R` | 立即采样一次，`ReadOnly` |

快捷键沿用现有插件的约定：`Ctrl+X` 删除或结束（网络、计时器、剪贴板），`Ctrl+D` 详情（WinGet），`Ctrl+Shift+F` 在文件资源管理器中显示，`Ctrl+Shift+C` / `Ctrl+Alt+C` 复制，`Ctrl+Shift+A` 以管理员身份（WinGet），`Ctrl+R` 刷新。

### 4.5 指标的呈现：第一期文字，第二期加 Meter

现在 SDK 的 `Item.Accessory` 只是一段灰色文字（`internal/ui/launcher.go`），MyGo 有 `ui.Meter` 和 `ui.Progress`，但 SDK 没有暴露。

- **第一期**：附注用固定格式的文字。数字右对齐，宽度固定（`  3.2%`、`1.2 GB`），每秒刷新时不左右跳。
- **第二期（宿主改动，通用）**：SDK 增加 `Item.Accessories []Accessory{Text string; Meter *float64 /* 0..1 */; Tint IconTint}`，宿主用 `mui.Meter` 画一个小条。CPU 和内存各一个小条，超过 50% 和 80% 分别变成警告色和危险色。别的插件也能用：Scoop 的下载进度、回收站大小、电池电量。
- **第三期**：详情里显示最近 60 秒的 CPU 和内存曲线。可以由插件在 `CacheDir` 生成 PNG 交给 `Detail.Image`，也可以给 SDK 加一个曲线元数据类型，到时候再选。

## 5. 结束进程：语义与安全

### 5.1 三种结束

| 操作 | 有窗口的应用 | 没有窗口的进程 |
|------|--------------|----------------|
| **结束任务** | 对这一组每个可见的顶层窗口 `PostMessage(WM_CLOSE)`（不用 `SendMessage`，未响应的窗口不会卡住启动器）。然后在 `Host.Go` 里用 `WaitForMultipleObjects` 等待组内进程退出，最多等 `closeTimeout`（默认 5 秒）。都退出了，提示"已结束 X"；没退出，提示"X 还在运行，可能在询问是否保存。按 Ctrl+Shift+X 强制结束"。**窗口未响应时跳过这一步，直接强制结束** | 等同于强制结束 |
| **强制结束** | 对组内每个进程 `TerminateProcess(h, 1)` | 同左 |
| **结束进程树** | 从根进程开始，按父子关系（并核对启动时间）找出所有子孙。**先结束根**（免得它再拉起新的子进程），再逐层结束子孙。每个进程都单独做 §5.4 的检查；权限不足的汇总在提示里："已结束 11 个，1 个需要管理员权限" | 同左 |

**特殊处理**：

- **Windows 资源管理器**：拥有任务栏（`Shell_TrayWnd`）的那个 `explorer.exe` 不提供结束，改为"重启 Windows 资源管理器"：结束它，等 2 秒；如果系统没有自动拉起新的任务栏，就以**非提升**身份启动 `explorer.exe`。单独运行的资源管理器窗口进程（开了"在单独的进程中打开文件夹窗口"的情况）可以正常结束任务。
- **golauncher 自己**：不提供任何结束操作，详情里写"golauncher（本程序）"。golauncher 启动的后台任务进程可以结束，但详情和确认框会提醒"这是后台任务，建议在后台任务里停止"（在任务管理器里停止会留下正确的任务状态）。

### 5.2 确认规则

| 操作 | 确认 | 在 SDK 里怎么标 |
|------|------|-----------------|
| 结束任务、强制结束、结束进程树、结束所有未响应的应用、重启资源管理器 | **每次确认** | 动作 `StyleDestructive`；命令 `Destructive` |
| 切换到窗口、显示、复制、在文件资源管理器中显示、刷新 | 不确认 | 刷新标 `ReadOnly` |
| 设置优先级、效率模式（第二期） | 不确认（随时可以改回来） | 普通回调，AI 视为"改动" |

- "结束任务"对有窗口的应用只是请它关闭，效果和点 × 一样，但也要确认：根搜索里输入 `kill fab` 按 Enter，误关整个浏览器的代价太高。是否放宽，见 §13 第 2 条。
- **确认框的说明文字**：现在宿主只显示通用的"此操作无法撤销"（`core.T("confirm.message")`）。这里需要按目标写不同的提醒（"将结束 12 个进程"、"这是系统服务宿主，托管着 3 个服务"、"这是后台任务"）。为此 SDK 增加一个通用字段 `Action.ConfirmMessage string`：非空时替换确认框的默认说明，AI 的外部确认框也显示它。改动很小，外部插件协议里也加上这个字段。

### 5.3 不允许结束和需要额外提醒的进程

| 类别 | 判断 | 处理 |
|------|------|------|
| 内核和伪进程 | PID 0、4；`Registry`、`Memory Compression`、`Secure System`、`LsaIso` | **不提供**结束操作 |
| 关键系统进程 | `IsProcessCritical` 为真，或者是 `%SystemRoot%\System32` 下的 `csrss.exe` `smss.exe` `wininit.exe` `winlogon.exe` `services.exe` `lsass.exe`（**名字和路径都要对上**，同名的假冒程序不受保护） | **不提供**结束操作，详情写明"结束它会导致系统崩溃或注销" |
| golauncher 本身 | PID 等于 `os.Getpid()` | **不提供** |
| 会影响桌面的进程 | `explorer.exe`（拥有任务栏的那个） | 改为"重启"（§5.1） |
| 需要提醒的系统组件 | `svchost.exe`（托管的服务）、`dwm.exe`、`ctfmon.exe`（输入法，结束后中文输入会失效）、`sihost.exe`、`StartMenuExperienceHost.exe`、`ShellExperienceHost.exe`、`SearchHost.exe`、`TextInputHost.exe`、`audiodg.exe`、`spoolsv.exe` | 可以结束，`ConfirmMessage` 说明会发生什么 |

名单放在 `internal/winapi/process` 里（`Classify(p) Class`），插件和提权助手（§5.5）都用它，加上测试。

### 5.4 PID 复用保护

网络插件现在用"进程名没变"判断 PID 有没有被复用，这里改成更严格的做法，并让两个插件共用：

1. 条目和动作的数据都带 `{pid, created}`，`created` 是快照里的 `CreateTime`（FILETIME）。条目 ID 也是 `p:<pid>:<created>`，同一 PID 换了进程，宿主不会把选中项保持到新进程上。
2. 执行时 `OpenProcess(PROCESS_TERMINATE | PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE)`，**用同一个句柄**调用 `GetProcessTimes` 核对创建时间，然后 `TerminateProcess`。句柄打开期间 PID 不会被系统复用，所以"检查"和"结束"之间没有竞态。
3. 创建时间不一致：不结束，提示"X 已经退出，PID 现在属于另一个进程"，并刷新列表。

`internal/winapi/process` 对外提供：

```go
type ID struct{ PID uint32; Created int64 } // Created: FILETIME, 100ns
func Kill(id ID) error            // ErrNeedsAdmin, ErrGone (已退出或 PID 已复用), ErrProtected
func KillTree(id ID) (KillReport, error)
func CloseWindows(id ID, hwnds []uintptr) error // PostMessage WM_CLOSE
func Wait(ctx context.Context, ids []ID) (exited []ID)
```

网络插件的 `killPayload` 增加 `created`，`winnet.Kill` 改为调用 `process.Kill`（旧数据里没有 `created` 时，退回现在的按名字检查）。

### 5.5 提升权限的进程

非提升的启动器结束提升的进程，或者其他用户的进程时，会得到 `ERROR_ACCESS_DENIED`。处理方式：

- 提示"需要管理员权限"，同时这个条目多出"以管理员身份结束"（`Ctrl+Shift+A`）。
- 执行时用 `ShellExecuteEx`（`runas`，`SEE_MASK_NOCLOSEPROCESS`）运行**启动器自己的一个隐藏子命令**：`golauncher.exe kill --pid 1234 --created 133… [--tree] [--force]`。这时会弹出 UAC，但没有控制台窗口（启动器是 GUI 程序）。子命令在单实例检查之前处理（和 `golauncher mcp` 一样），做 §5.3 的分类和 §5.4 的检查，结束后立即退出，用退出代码报告结果。插件在 `Host.Go` 里等它退出，再用 Toast 报告结果；用户在 UAC 里取消（`ERROR_CANCELLED`）时提示"已取消"。
- **不提供"以管理员身份重启 golauncher"**：启动器提升后，从它启动的所有应用都会是提升的，粘贴到普通窗口和拖放也会受 UIPI 限制，代价太大。
- 不用 `taskkill /F` 提权：会闪一个控制台窗口，也没法核对启动时间。
- 受保护进程（PPL，比如部分杀毒软件）即使提权也会被拒绝：提示"受系统保护，无法结束"，不再提供提权选项。
- 启动器本身已经是提升运行时，以上都不需要，所有进程都能直接读取和结束（受保护进程除外）。

### 5.6 和网络插件的关系

- `port 8080` 里的"结束进程"用同一个 `process.Kill`（启动时间保护、关键进程名单、golauncher 自己）。
- 进程管理的详情里显示监听端口，数据来自 `winapi/network.Sockets()`。插件之间不能互相调用（`internal/lint` 的规则），所以共用的是 `internal/winapi` 这一层，而不是另一个插件的视图。
- 网络插件现在的 `ProcessInfo`（名字和路径）也改为调用 `process` 包，删掉重复的 Toolhelp 代码。

## 6. AI

| 内容 | 风险等级（[ai.md §4.2](ai.md)） |
|------|------|
| `search "ps …"`、打开进程视图、`get_detail` | 只读，直接执行 |
| 刷新 | 只读（`ReadOnly`） |
| 切换到窗口、复制 | 回调按"改动"处理；复制是"打开"级别 |
| 设置优先级、效率模式 | 改动，每次询问（可以"本次会话内都允许"） |
| 结束任务、强制结束、结束进程树、重启资源管理器、结束所有未响应的应用 | **破坏性**：每次询问，不能"本次会话内都允许"。确认框显示 `ConfirmMessage` |

- 插件默认允许 AI 使用（和其他内置插件一样），用户可以在设置里关掉。
- **命令行永远不交给 AI**：SDK 给 `Metadata` 增加 `Sensitive bool`（和 `Item.Sensitive` 同一个意思，只是粒度到一行）。工具层返回详情时去掉这些行，界面照常显示。复制命令行是回调，执行时才读取，命令行不在任何条目或动作的数据里。
- 窗口标题会交给 AI（只靠进程名分不清是哪个窗口）。是否也要隐藏，见 §13 第 4 条。

## 7. 隐私

- **命令行**可能带着密钥（`--token=…`、`-p 密码`、`https://user:pass@host`）。处理方式：
  - 详情里默认脱敏显示：`--password=` `--token` `--secret` `--key` `--api-key` `-p` 后面的值，以及 URL 里的 `user:pass@`，都替换成 `•••`。偏好 `redactCommandLine`（默认开）可以关掉脱敏。
  - "复制命令行"复制完整内容，用 `Clipboard.WriteSecret`，不进剪贴板历史和 Windows 云剪贴板；
  - 不交给 AI（§6），不写日志。
- **不落盘**：进程列表、窗口标题、命令行只在内存里，隐藏 30 秒后清空（§2.5）。KV 里只存排序方式和分组开关；`CacheDir` 里只有按 exe 路径哈希命名的图标 PNG。
- **日志**只记数量、耗时和错误码，不记进程名、路径、标题。
- 测试数据、fixture、golden、本文的示例全部是虚构的，不使用本机的真实进程列表、用户名或路径。

## 8. 偏好设置

| 名称 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `prefixes` | stringList | `["ps", "kill", "进程"]` | `kill` 前缀下 Enter 是结束任务 |
| `interval` | select | `1`（秒） | 1 / 2 / 5 |
| `cpuMode` | select | `total` | `total`（总量归一，像任务管理器）/ `perCore`（像 top） |
| `groupByApp` | bool | true | |
| `showWindowsProcesses` | bool | true | |
| `defaultSort` | select | `cpu` | cpu / memory / io / name；操作面板里改的值优先 |
| `closeTimeout` | number | 5（秒） | 结束任务时等待应用自己退出的时间 |
| `redactCommandLine` | bool | true | |
| `searchCommandLine` | bool | false | 第二期 |
| `foregroundQuit` | bool | true | §1.3 的两种前台条目 |

## 9. 国际化

- `plugins/processes/locales/en.json` 和 `zh-CN.json` 同时提交，`glctl i18n check` 通过；不允许硬编码界面文字（`internal/lint`）。
- 用 Windows 任务管理器的中文译法，补进 [i18n-zh-cn.md §4.2](i18n-zh-cn.md) 的术语表：

| English | 简体中文 |
|---------|----------|
| Process / Processes | 进程 |
| End task | 结束任务 |
| Force quit / End process | 强制结束 |
| End process tree | 结束进程树 |
| Not responding | 未响应 |
| Apps / Background processes / Windows processes | 应用 / 后台进程 / Windows 进程 |
| Memory (private working set) / Working set / Commit size | 内存（专用工作集）/ 工作集 / 提交大小 |
| Handles / Threads | 句柄 / 线程 |
| Elevated | 已提升 |
| Set priority: Realtime / High / Above normal / Normal / Below normal / Low | 设置优先级：实时 / 高 / 高于正常 / 正常 / 低于正常 / 低 |
| Efficiency mode | 效率模式 |
| Restart Windows Explorer | 重启 Windows 资源管理器 |

- 关键词带拼音首字母：进程 `jc`，任务管理器 `rwglq`，结束 `js`，强制结束 `qzjs`，未响应 `wxy`，资源管理器 `zyglq`。进程的中文显示名（版本信息里的 `FileDescription`）在运行时转成拼音参与匹配（§3.3）。
- 数字用 `Localizer.Number`；大小用和系统命令插件一样的格式（KB/MB/GB，不翻译单位）；运行时长用 `RelativeTime`。

## 10. 代码结构与宿主改动

| 位置 | 内容 |
|------|------|
| `internal/winapi/process`（新） | `process.go`：类型、CPU 和速率计算、分组和进程树、`Classify` 名单、命令行脱敏（纯 Go，任何平台都能用 fixture 测试）；`process_windows.go`：快照、按需字段、窗口枚举、`IsHungAppWindow`、`Kill` / `KillTree` / `CloseWindows` / `Wait`、`SetPriority`、提权执行；`process_other.go`：返回 `errors.ErrUnsupported`。注意不要和已有的 `internal/winapi/proc`（运行后台程序）混淆 |
| `internal/winapi/shell` | 把读取版本信息的函数导出，供 `process` 取 `FileDescription` |
| `internal/winapi/network` | `ProcessInfo`、`Kill` 改为调用 `process` 包 |
| `plugins/processes`（新） | `processes.go`（manifest、前缀、根搜索、前台条目）、`sampler.go`（采样器和使用者计数、停止条件）、`view.go`（进程视图、组视图、`hung` 视图）、`actions.go`（结束、提权、切换窗口）、`format.go`、`backend.go`（`Backend` 接口：`System` 真实实现、`Fixture` 读 fixture、`DryRun` 只读真实数据但不执行结束）、`locales/` |
| `plugins/network` | `killPayload` 加 `created` |
| `plugins/quicklinks/engines.go` | `reservedPrefixes` 加 `ps` `kill` `进程` |
| `pkg/plugin` | `Action.ConfirmMessage`、`Metadata.Sensitive`（第一期）；`Item.Accessories`（第二期）。`protocol`、`rpcserve`、`extproc` 同步 |
| `internal/core`、`internal/agent` | 确认框使用 `ConfirmMessage`；工具层去掉敏感的元数据行 |
| `internal/shell` | `golauncher kill …` 隐藏子命令（§5.5） |
| `internal/builtin/builtin.go` | `All` 里加 `processes.New()`，`Fake` 里加 `processes.NewFake(fixture(dir, "processes.json"))` |
| `internal/ui/icons.go` | 新图标 `cpu`、`process`、`stop` |

## 11. 测试

**原则**：除了测试自己启动的进程，**绝不结束任何真实进程**。凡是会结束进程、发 `WM_CLOSE`、改优先级、提权的调用，都在 `Backend` 接口后面；`glctl` 的假环境用 `Fixture`，`glctl --real` 不带 `--exec` 时用 `DryRun`。

| 层 | 内容 |
|----|------|
| `internal/winapi/process`（纯 Go） | 手工构造的 `SYSTEM_PROCESS_INFORMATION` 缓冲区解析（含 `NextEntryOffset` 链、UNICODE_STRING、缓冲区不够时的重试）；CPU 计算（总量归一和按核两种、处理器数、计数回绕、进程在两次采样之间重启）；I/O 速率；分组（同路径子进程归组、路径不同不归组、父进程已退出且 PID 被复用、UWP 按包名）；排序防抖动；`Classify`（真的 `System32\lsass.exe` 和放在别处的 `lsass.exe`）；命令行脱敏的各种写法 |
| `internal/winapi/process`（真机只读） | 快照里有自己（PID、路径、`CreateTime` 和 `GetProcessTimes` 一致）和 PID 4；自己的内存和线程数大于 0；`EnumWindows` 不出错；基准测试 `BenchmarkSnapshot` 记录耗时（只记录，超过 200 ms 才算失败，避免在慢的 CI 上不稳定） |
| `internal/winapi/process`（真机，只碰自己启动的进程） | 启动 `PING.EXE -n 30 127.0.0.1`（和网络插件的测试一样），用正确的 `created` 结束成功；用错误的 `created` 返回 `ErrGone` 且进程还活着；结束进程树：测试启动 `cmd /c ping …` 再结束整棵树；`CloseWindows`：测试自己创建一个隐藏的顶层窗口并处理 `WM_CLOSE`；拒绝 PID 0、4 和自己 |
| `plugins/processes`（hostfake + Fixture） | 前缀（`ps` 必须带空格、`进程chrome`、自定义前缀）；不带前缀不返回进程，也不启动采样；搜索（名字、拼音、PID 精确优先、组成员匹配）；三个分节和组视图；每个动作的 `Style`、`ConfirmMessage`、`ReadOnly`；关键进程和自己没有结束操作；explorer 换成"重启"；结束任务的流程（关窗口 → 退出 → 提示；超时 → 提示强制结束；未响应 → 直接强制）；PID 复用后拒绝；需要管理员权限 → 出现提权操作，提权的命令行正确；前台条目（`Transient`、UWP、桌面、情境感知关闭时不出现）；命令行脱敏、`Metadata.Sensitive`、`WriteSecret` |
| 采样生命周期（注入的计时器，推进假时钟，不真的等待） | 根搜索每个周期 `Invalidate`；视图每个周期 `Render`；无前缀、`window-hidden`、两个周期没有拉取、`ViewClosed`、`Close` 之后停止；多个使用者时只有一个采样器；30 秒后清空缓存 |
| `internal/agent` | 进程视图对 AI 只读；结束是破坏性、不能"本次会话内都允许"；`get_detail` 里没有命令行 |
| `glctl` golden | fixture `testdata/fixtures/processes.json`：约 40 个虚构的进程（Contoso Writer、Fabrikam Browser ×8、Northwind Sync、`node.exe`、一个未响应的应用、关键系统进程、golauncher 自己），带**两次采样**的计数，所以 CPU% 是确定的。用例：`query "ps "`、`query "ps fab"`、`query "kill writer"`、`query "进程 fab"`、`--locale zh-CN`、`run processes`、会话：`kill writer` → Enter → 确认框 → 完成（fixture 只记录调用）、关键进程没有结束操作、`--context-app` 注入一个未响应的前台应用 |

## 12. 分期

| 阶段 | 内容 |
|------|------|
| **第一期（MVP）** | `internal/winapi/process`，网络插件改用它；插件：三个前缀、进程视图和组视图、实时刷新、按应用分组、排序和筛选、详情面板；结束任务、强制结束、结束进程树、切换到窗口、在文件资源管理器中显示、复制、在网上搜索、打开任务管理器或资源监视器、刷新；前台应用的退出和未响应时的强制结束；`hung` 和 `restart-explorer` 两个命令；提权结束（`golauncher kill`）；SDK 的 `ConfirmMessage` 和 `Metadata.Sensitive`；中英文；测试和 golden |
| 第二期 | SDK `Item.Accessories`（CPU 和内存小条）；设置优先级、效率模式；svchost 托管的服务；按命令行搜索；"重启应用"（结束后用原路径和参数重新启动，只对有窗口、能读到命令行的应用）；**谁占用了这个文件**：用 Restart Manager（`RmGetList`，不需要管理员权限）查出占用某个文件的进程，在资源管理器里选中文件时作为情境条目出现，和 `builtin.files` 配合 |
| 第三期 | GPU（PDH，默认关闭）；详情里的 CPU 和内存曲线；应用插件在根搜索里给正在运行的应用标"正在运行"（共用 `process` 包，只在呼出时取一次快照）；装了 Process Explorer 时提供"在 Process Explorer 中打开"（`procexp64.exe /s:<pid>`）；可选的资源告警（某个进程持续高占用时通知，需要后台采样，默认关闭） |

## 13. 需要你决定

1. **前缀**：`ps `（必须带空格）、`kill`、`进程` 可以吗？要不要默认加上 `top` 或 `jc`？
2. **结束任务要不要确认**：本文的方案是所有结束操作都确认，包括对有窗口的应用只是"请它关闭"的结束任务。另一种做法是：结束任务和点 × 一样不确认，只有强制结束和结束进程树才确认（任务管理器"进程"页就是这样）。
3. **进程视图里 Enter 做什么**：本文是"结束任务"（`Ctrl+Enter` 切换到窗口）。如果你更常用来找窗口，也可以反过来，Enter 切换到窗口，`Ctrl+X` 结束。
4. **AI**：插件默认允许 AI 使用，命令行永远不给。窗口标题（可能含文档名、邮件标题）要不要也不给 AI？
5. **提升的进程**：用 UAC 单次提权、启动器自带的 `golauncher kill` 子命令来结束，可以吗？（不做"以管理员身份重启 golauncher"。）
6. **指标小条**：SDK 加 `Item.Accessories` 放在第二期，还是一起放进第一期？第一期只有文字。
7. **在网上搜索**：插件拿不到快速链接插件里你设置的默认搜索引擎（插件之间不能互相调用）。第一期按界面语言选 Bing 或 Google，可以吗？还是宿主加一个通用的"用默认搜索引擎搜索"动作？
8. **刷新间隔**默认 1 秒、**CPU** 默认按总量归一（和任务管理器一致），可以吗？

## 14. 决定（2026-10-16，R55）

1. **前缀**：`ps `、`kill`、`进程`，不加 `top`、`jc`。
2. **正常结束不需要确认**；强制结束和结束进程树仍然要确认，关键进程照旧不提供结束操作。
3. **进程视图里按 Enter**：切换到该应用的窗口（没有窗口的进程打开详情）；结束任务改用其他快捷键。
4. **窗口标题对 AI 隐藏**，和命令行一样。
5. **结束管理员进程时弹一次 UAC**（§ 中的一次性提权方案）同意。
6. **CPU 和内存条形图放在第一期**。
7. **在网上搜索进程用 Google**。
8. **每秒刷新，CPU% 按任务管理器的算法**，同意。
## 15. 实现记录（第一期，2026-10-16，R52 / R55）

### 15.1 做了什么

| 位置 | 内容 |
|------|------|
| `internal/winapi/process`（新） | `process.go`（纯 Go）：`ID`、`Proc`、`Snapshot`、`Info`、`Window`；`Rates`（总量归一 / 按核、计数回退按 0、两次采样之间新启动的进程按启动时间判断）、`SystemCPU`、`MemoryUsed`；`GroupRoots`（打包应用按包名，其他按"同一路径的父进程链"）、`Tree`（根在前、逐层）、`ParentOf`（父进程启动时间晚于子进程时不算父进程）；`Classify`；`RedactCommandLine`；提权助手的参数 `HelperRequest.Args` / `ParseHelperArgs`（严格：只接受 `kill --pid N --created N [--tree]`，PID 必须大于 4，纯十进制，不能重复、不能有多余参数）和退出码。`process_windows.go`：`Sampler.Snapshot`（`NtQuerySystemInformation`，缓冲区复用）、`ReadInfo`、`CommandLine`、`Windows`（`EnumWindows` + `IsHungAppWindow`，UWP 取 `CoreWindow` 的进程，标题用 `InternalGetWindowText`，不发消息）、`Identify`、`Kill`、`KillTree`、`CloseWindows`（`PostMessage(WM_CLOSE)`）、`Wait`、`SwitchTo`、`RunElevated`（`ShellExecuteExW` + `runas`）、`IsElevated`、`StartExplorer`、`HelperMain` |
| `internal/winapi/shell` | 导出 `FileDescription`，和应用插件共用读版本信息的代码 |
| `internal/winapi/network`、`plugins/network` | `ProcessInfo` 和 `Kill` 改为调用 `process`，删掉 Toolhelp 代码；`killPayload` 带 `created`（`omitzero`，旧 golden 不变）；旧数据没有 `created` 时仍按进程名核对；受保护的进程有单独的提示 |
| `plugins/processes`（新） | `processes.go`（manifest、前缀、根搜索、视图入口、动作分发）、`sampler.go`（唯一的采样循环、使用者计数、隐藏 30 秒后释放缓存）、`model.go`（分组、分节、排序防抖动、搜索和拼音）、`items.go`（条目、条形图、详情、动作、确认文字、三个视图）、`actions.go`（结束任务、强制结束、结束进程树、提权结束、切换窗口、复制命令行、结束所有未响应的应用、重启资源管理器、前台应用条目）、`backend.go`（`System`、`DryRun`、`Fixture`）、中英文目录 |
| `pkg/plugin` | `Item.Accessories`（`Accessory{Text, Meter, Load}`）、`Item.SubtitleSensitive`、`Metadata.Sensitive`、`Action.ConfirmMessage`、`Query.Raw`；外部插件协议的 `rootItems` 带 `raw`，其余字段随原有 JSON 传递 |
| `internal/ui` | 列表行在附注后面画 `Accessories`：`mui.Meter`（固定 40 px，`Load` 时 50% / 80% 变成主题的警告色 / 危险色，否则主题的成功色）加固定最小宽度、右对齐的文字，每秒刷新不跳动；颜色全部来自主题 |
| `internal/core`、`internal/agent` | 确认框和 AI 的外部确认框显示 `ConfirmMessage`；工具层去掉 `SubtitleSensitive` 的副标题和 `Sensitive` 的详情行，也不按敏感副标题匹配；`info` 带上 `Accessories` 的文字 |
| `internal/shell` | `golauncher kill …` 隐藏子命令，在单实例检查之前处理，用退出码报告结果 |
| `internal/cli` | `glctl --real` 不带 `--exec` 时用 `DryRun`（读真实进程，不结束任何进程）；文本输出带上 `Accessories` |
| `plugins/quicklinks/engines.go` | `reservedPrefixes` 加 `ps`、`kill`、`进程` |
| `testdata/fixtures/processes.json` | 36 个虚构进程（Contoso Writer、Fabrikam Browser ×8、Northwind Sync、Contoso Code + 两个 node、未响应的 Litware Paint、中文名的"南风笔记"、golauncher 自己和它的后台任务、需要管理员权限的 Tailspin Admin Tool、系统服务、关键系统进程、放在下载目录里的假 `lsass.exe`、父进程已退出 / PID 被复用的进程），带两次采样的计数 |

### 15.2 和设计不同的地方

1. **`ps` 必须带空格，需要宿主配合**：宿主在调用 `RootItems` 之前会去掉首尾空格，插件分不出 `ps` 和 `ps `。SDK 增加 `Query.Raw`（根搜索里未裁剪的原文），插件优先看它。只输入 `ps` 时插件不返回任何条目（§1.1 说返回一条"进程"命令条目，但"进程"命令本身已经由宿主匹配，再返回一条是重复的）。
2. **`ps` 前缀下按 Enter**：和进程视图一致（§14 第 3 条）——有窗口切换到窗口，没有窗口打开这个应用的进程列表（组视图，带详情），而不是"打开进程视图并选中"（SDK 没有"推入视图并选中某项"的办法）。
3. **没有窗口或未响应时的"结束任务"仍然确认**：§14 第 2 条说正常结束不需要确认，指的是请应用自己关闭（`WM_CLOSE`）。没有窗口可关、或者窗口未响应时，"结束任务"等于立即 `TerminateProcess`，所以按强制结束处理，要确认。见 §15.5 第 1 条。
4. **AI 的风险等级**：有窗口时的"结束任务"不再是"破坏性"，而是普通回调（"改动"：每次询问，但可以"本次会话内都允许"），和用户这边不确认保持一致；强制结束、结束进程树、重启资源管理器、结束所有未响应的应用仍是"破坏性"。见 §15.5 第 2 条。
5. **窗口标题**：§14 第 4 条要求对 AI 隐藏。为了列表里仍然显示窗口标题（区分多个窗口），SDK 除了设计里的 `Metadata.Sensitive` 之外，又加了 `Item.SubtitleSensitive`。限制：AI 用窗口标题里的词搜索时，插件仍会返回那个进程（不带标题），所以 AI 能推断"某个进程有一个标题含这个词的窗口"。见 §15.5 第 3 条。
6. **`Accessory` 没有 `Tint`**：改成 `Load bool`（按负载变色）。进度类的条用主题的成功色。
7. **图标**：没有新增 `cpu`、`process`、`stop` 图标，用已有的 `activity`、`circle-stop`、`rotate-cw`、`alert`；进程自己的图标在后台画进 `CacheDir/icons`（按路径哈希命名），画好后 `Invalidate`。
8. **提权助手**只有 `--pid`、`--created`、`--tree` 三个参数，总是 `TerminateProcess`（没有 `--force`）。助手里 `Kill` 照样做分类（关键进程、PID 0/4、助手自己）和启动时间核对；`KillTree` 遇到受保护的根就停，不会去结束它的子孙（比如 `services.exe` 下面的服务）。"以管理员身份结束"本身不是破坏性动作：UAC 就是确认。
9. **命令行**：在生成详情时读取（每个进程读一次，按 (PID, 启动时间) 缓存），而不是"打开详情面板时"——SDK 的详情是随条目一起给出的。
10. **分节规则**：路径在 `%SystemRoot%` 下、系统账户运行、会话 0、或者打不开也读不到路径的进程归入"Windows 进程"；有窗口的、以及拥有任务栏的 `explorer.exe` 归入"应用"。golauncher 自己、资源管理器和 golauncher 的后台任务总是单独一行，不并入别的组。
11. **fixture 的计数**：第 k 次采样按前两次的增量外推，所以在测试里多次刷新时 CPU% 保持不变。
12. `glctl --real --exec` 时提权结束会以 `glctl.exe kill …` 运行，而 glctl 没有这个子命令：提权只在 golauncher 里可用。

### 15.3 测试

- `internal/winapi/process`：纯 Go 的计算、分组、进程树、分类（`System32\lsass.exe` 和别处的 `lsass.exe`）、命令行脱敏、助手参数（缺参数、PID 0/4、非数字、十六进制、重复、多余参数、溢出）、退出码；手工构造的 `SYSTEM_PROCESS_INFORMATION` 缓冲区解析。结束类测试**只碰测试自己启动的进程**：`PING.EXE`（错误的启动时间返回 `ErrGone` 且进程还活着，正确的结束成功）、`cmd /c ping` 的进程树、`Wait`、`HelperMain`、测试自己创建的隐藏窗口收到 `WM_CLOSE`；PID 0、4 和自己被拒绝；对自己做 `KillTree` 时，它的子进程（测试启动的 ping）不被结束。不调用 `RunElevated`，不弹 UAC。真机只读测试（快照里有自己和 PID 4、窗口枚举、计时）要设置 `GOLAUNCHER_PROCESS_TESTS=1`，日志里只有数量和耗时。
- `plugins/processes`（fixture 后端）：前缀（`ps` 必须带空格、`进程fab`、`kill`、自定义和清空前缀，不带前缀不采样）；搜索（名称、exe、窗口标题、拼音全拼和首字母、PID 精确优先）；条形图（总量归一和按核）；三个分节、隐藏 Windows 进程、不分组；组视图的树和缩进；每类进程的动作和确认规则（关键进程、golauncher 自己、内核没有结束操作；资源管理器只有重启；有窗口的结束任务不确认、没窗口或未响应的要确认；强制结束和结束进程树确认，确认文字写明数量和后果）；结束任务的成功、超时后提示强制结束（同步和异步两种）；强制结束一组；结束进程树；PID 复用；伪造的动作也结束不了关键进程；需要管理员权限 → 出现"以管理员身份结束" → 提权参数正确；未响应视图；重启资源管理器；隐私（窗口标题和命令行标为敏感、命令行脱敏、复制命令行用 `WriteSecret` 且不在动作数据里）；前台应用条目（`Transient`、未响应时的强制结束、按名字注入、资源管理器 / 桌面 / 自己 / 关键进程不出现、关闭偏好）；排序防抖动；采样生命周期（注入的计时器：根搜索每个周期 `Invalidate`、视图每个周期 `Render` 并保留搜索文字、无前缀 / 两个周期没拉取 / `ViewClosed` / 隐藏 / `Close` 后停止、隐藏 30 秒后清缓存、多个使用者只有一个循环）。
- `internal/agent`：用 fixture 走 AI 工具，搜索、进程视图和每个条目的 `get_detail` 里都没有窗口标题和命令行；结束任务是"改动"，强制结束是"破坏性"、不能本次会话内都允许、确认框带插件的说明。
- `internal/cli` golden：`ps `、`ps fab`、`kill writer`、`进程 nfbj`、中文界面、进程视图、未响应视图、`--context-app paint.exe`；会话：`kill writer` → Enter 不确认直接完成，Ctrl+Shift+X 先确认再完成；`ps` 不带空格不列进程；真的 `lsass.exe` 没有结束操作、假的有。新进程插件的命令使 `query_empty` 等 8 个已有 golden 多了几条命令。
- `go vet ./...`、`go test ./...` 通过；改动过的包 `go test -race` 通过；`glctl i18n check` 通过。

### 15.4 实测耗时

在一台约 500 个进程、约 1.2 万个线程的台式机上（`GOLAUNCHER_PROCESS_TESTS=1`，只记录耗时）：

| 项目 | 目标（§2.6，按 300 个进程） | 实测 |
|------|------|------|
| 快照（`NtQuerySystemInformation` + 解析），10 次的中位数 | < 5 ms | 5.2 ms（基准测试 5.8 ms/次，每次分配约 78 KB） |
| 稳定状态的一次采样（快照 + 窗口枚举），中位数 | < 10 ms | 7.9 ms |
| 第一次采样（再加上为每个进程读路径、用户、提升状态、版本描述） | < 40 ms | 42.5 ms（约三分之一的进程打不开，按"受保护"处理） |
| `glctl query "ps " --real`（含启动 glctl 和所有插件） | — | 约 0.36 s |

进程数比设计假设的 300 多了约 70%，按比例算三项都在目标以内。

### 15.5 待定问题

1. 没有窗口的进程（`node.exe` 这类后台进程）"结束任务"现在要确认（§15.2 第 3 条）。如果你希望它和有窗口的应用一样不确认，改一行即可。
2. AI 的"结束任务"现在是"改动"，可以"本次会话内都允许"（§15.2 第 4 条）。要不要对 AI 仍按"破坏性"处理（每次都问）？这需要 SDK 多一个"只对 AI 是破坏性"的标记。
3. AI 能通过窗口标题里的词搜到进程（§15.2 第 5 条）。要彻底避免，可以让插件不按窗口标题匹配（用户也就不能按标题搜了），或者宿主告诉插件"这是 AI 的查询"。
4. 第二期（设置优先级、效率模式、svchost 托管的服务、按命令行搜索、重启应用、谁占用了文件）未做。
