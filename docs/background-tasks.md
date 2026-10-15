# 后台任务设计方案

> 状态：设计 v1 · 2026-10-15 · 对应需求 R24 · 已实现，见 [§8 实现记录](#8-实现记录r24)（见 [requirements.md](requirements.md)）
>
> 需求：给启动器增加后台任务功能。比如 `scoop update` 可能要执行 10 分钟，不能让用户在当前页面干等。要求：
> 1. 可以查看后台任务的状态；
> 2. 可以查看后台任务的控制台输出；
> 3. 启动器主界面上有一个小指示灯，显示正在运行的后台任务数量。

## 1. 定位：宿主级能力，而不是某个插件

后台任务由**宿主统一提供**，所有插件都能用：

- 插件只需要说明"要运行什么"，宿主负责排队、运行、捕获输出、取消、通知和展示。这样每个插件不用各自实现一套队列和输出视图。现在 Scoop 和 Script Commands 就各有一套，重复，而且不统一。
- 顺便补上 [M5 文档 §14](m5-plugin-platform.md) 里的缺口：长操作在动作返回后才完成，插件没法在完成时提示用户。任务结束时，宿主会统一发出通知。
- 任务的列表视图、主界面的指示灯和完成通知，都由宿主实现。

## 2. SDK

```go
// TaskSpec describes a background task.
type TaskSpec struct {
    Title    string // 例如 "scoop update"
    Subtitle string // 例如 "Updating 3 apps"
    Icon     Icon
    // Queue serializes tasks with the same non-empty key (Scoop must not run
    // two operations at once). Tasks in different queues run in parallel.
    Queue string
    // Notify controls the notification when the task ends: NotifyAlways,
    // NotifyOnFailure (default) or NotifyNever.
    Notify NotifyMode
}

// ProcessSpec is a command line the host runs as a task: no window, in a
// Job Object (the whole process tree ends on Stop or when golauncher quits),
// stdout and stderr captured as the task's console output.
type ProcessSpec struct {
    Argv []string
    Dir  string
    Env  []string // added to the host's environment
}

// Task is handed to a Go task function to report progress and output.
type Task interface {
    io.Writer                                 // 控制台输出（按行）
    SetStatus(status string)                  // 一行状态，例如 "Downloading 3/5"
    SetProgress(fraction float64)             // 0–1；负数表示不确定进度
}

type Host interface {
    // ...
    // RunProcess queues a command line as a background task and returns its ID.
    RunProcess(spec TaskSpec, cmd ProcessSpec) string
    // RunTask queues fn as a background task (for work done in Go). fn must
    // return when ctx is cancelled (Stop).
    RunTask(spec TaskSpec, fn func(ctx context.Context, t Task) error) string
    // Toast shows a short message: in the launcher when it is visible,
    // otherwise as a Windows notification. Safe from any goroutine.
    Toast(t Toast)
}

// 动作也可以直接提交后台任务，不用写回调：
const ActionRunProcess ActionKind = "RunProcess" // Payload: {"spec": TaskSpec, "cmd": ProcessSpec}
```

- **任务完成**时，宿主会按任务里的 `Notify` 设置发通知，并发布事件 `task-finished`，事件里带上任务 ID 和插件 ID。插件可以订阅它来刷新自己的数据，比如 Scoop 在任务结束后重建索引。
- **外部插件**：协议新增 `host/task.runProcess` 和 `host/toast`，写进 `plugin-protocol.md`。用 Go 函数实现的任务（`RunTask`）只给进程内插件用，外部插件运行在自己的进程里，不需要它。

## 3. 宿主：任务管理器（`internal/tasks`）

| 方面 | 设计 |
|------|------|
| 状态 | 排队中 → 运行中 → 成功、失败（显示退出码或错误）、已取消 |
| 并发 | 同一个 `Queue` 里的任务按顺序执行，不同队列并行执行；全局最多同时运行 4 个，可以配置 |
| 输出 | 按行保存在内存里，最多保留最后 2MB，超过就丢掉开头的部分；同时完整写入 `%LOCALAPPDATA%\golauncher\tasks\<id>.log`。不是 UTF-8 的行按 OEM 代码页解码，并去掉 ANSI 颜色控制码和 `\r` 进度覆盖符，处理方式和 Script Commands 一致 |
| 取消 | `Stop` 会结束任务的整棵进程树（Job Object）；Go 任务通过取消 ctx 结束 |
| 历史 | 保留最近 50 个已结束的任务，以及它们的日志文件，超出的删除。重启后从日志目录读回历史（只显示标题、状态和输出，不能重新运行） |
| 退出 | 有任务在运行时退出 golauncher，会先弹窗确认"N 个后台任务正在运行，退出会把它们结束"；确认后结束所有进程树 |
| 通知 | 启动器可见时，在底栏显示 Toast；不可见时，发 Windows 系统通知，点击通知打开这个任务的视图 |

## 4. 界面

### 4.1 主界面指示灯（需求 3）

- 底栏左侧，原来显示 `⌘ golauncher` 的位置：有任务在运行时，显示一个转动的小圆环和数字，比如 `◐ 2`，悬停提示"2 background tasks running"。
- 没有正在运行的任务、但有失败且没看过的任务时，显示一个红点；打开任务视图后，红点消失。
- 点击指示灯，或者按 `Ctrl+J`，打开任务视图。
- 托盘图标的悬停提示里也显示正在运行的任务数。

### 4.2 任务视图（需求 1、2）

- 宿主内置命令 **Background Tasks**，由插件 `builtin.tasks` 提供（实现在 `internal/tasks`）。根搜索输入 `tasks` 也能找到它。
- 列表按"运行中 → 排队中 → 已结束（最新的在前）"排列。每一条显示：
  - 标题、副标题（任务状态或进度）；
  - 右侧附注：运行中显示已用时间和进度，比如 `03:12 · 40%`；结束后显示 `✓ 2m 31s`、`✗ exit 1` 或 `⊘ cancelled`；
  - 图标：任务自己的图标，叠加状态色。
- **右侧详情面板**显示选中任务的控制台输出，只显示最后约 300 行；运行中实时刷新，宿主内部用的是 `Render` 推送。下方是元数据：命令行、工作目录、开始时间、耗时、退出码、日志文件路径。
- **动作**：

  | 动作 | 快捷键 | 说明 |
  |------|--------|------|
  | 停止 | `Ctrl+X`，需要确认 | 运行中的任务 |
  | 复制输出 | `Ctrl+Shift+C` | |
  | 打开完整日志 | `Enter` | 用默认编辑器打开 `.log` 文件 |
  | 重新运行 | `Ctrl+R` | 仅限进程任务 |
  | 从列表移除 | — | 已结束的任务 |
  | 清除已结束任务 | — | |

## 5. 迁移现有插件

| 插件 | 现在的做法 | 改为 |
|------|------------|------|
| Scoop（R22） | 插件自己的队列和输出视图 | `RunProcess(Queue: "scoop")`。动作立即返回，并提示"已在后台运行"；任务结束后，插件订阅 `task-finished` 重建索引。原来的"Scoop Operations"分组和输出视图删除 |
| Script Commands | `fullOutput` 模式用插件自己的输出视图 | 改为 `RunProcess`，同时提供"在任务视图中查看"的动作；`compact` 和 `silent` 模式保持不变 |

## 6. glctl 与测试

- **glctl**：
  - 假环境里，进程任务使用**假执行器**：只记录命令，立即以成功结束，可以配置输出内容。
  - `glctl tasks list`、`glctl tasks show <id>` 用来查看假环境里的任务。
  - `glctl session` 新增 `wait-tasks` 步骤，等所有任务结束后再取快照。
- **单元测试**：
  - 任务状态机；队列串行、不同队列并行、全局并发上限；
  - 取消（结束真实的 `cmd /c` 进程树）；
  - 输出截断和解码；日志文件轮转；历史读回；
  - 通知策略。
- **界面测试**：指示灯的数字和红点、`Ctrl+J`、任务视图的实时刷新、退出前的确认。
- **迁移后的回归**：Scoop 和 Script Commands 原有测试按新的行为更新。

## 7. 以后

- 任务完成后执行一个后续动作，比如下载完成后打开文件。
- 定时任务、开机任务。
- 外部插件用 Go 函数实现的任务，需要在协议里增加任务的进度上报。

## 8. 实现记录（R24）

### 代码结构

| 位置 | 内容 |
|------|------|
| `pkg/plugin/tasks.go` | `TaskSpec`、`ProcessSpec`、`Task`、`TaskFunc`、`NotifyMode`、`TaskState`、`TaskResult`、`RunProcessPayload`、事件名 `EventTaskFinished` |
| `pkg/plugin/host.go`、`types.go` | `Host.RunProcess`、`RunTask`、`Toast`；动作类型 `ActionRunProcess`、`ActionShowTask`；`Result.Do`；`Icon.Tint` |
| `internal/winapi/proc` | 从 Scoop 和 Script Commands 抽出来的共享进程代码：无窗口启动、Job Object（停止时结束整棵进程树）、批处理文件的 cmd.exe 引号、OEM 代码页解码、去 ANSI 和 `\r`（`CleanLine`、`LineWriter`）、环境变量合并 |
| `internal/tasks` | 任务管理器 `Manager`（`tasks.go`）、输出缓冲和日志（`output.go`）、历史（`history.go`）、执行器 `ProcessRunner`/`FakeRunner`（`runner.go`）、通知策略和文案（`notify.go`）、宿主插件 `builtin.tasks`（`plugin.go`） |
| `internal/host` | `Services.Tasks`（任务管理器接口）、`Services.OnToast`；`hostfake` 自带一个用假执行器的任务管理器 |
| `internal/core` | `TaskService` 接口、`Notice`、`Engine.SetNotifier`/`Notify`、两种新动作和 `Result.Do` 的执行、`Snapshot.Tasks`（指示灯）、`Ctrl+J`（`KeyTasks`）、`Controller.OpenTasks`、`ShowToast` |
| `internal/app` | 创建任务管理器并接线（`task-finished` 事件、通知）；总是加入 `builtin.tasks`；`Options.TaskRunner`/`TasksDir`/`TasksReadOnly`，`Env.Tasks`；`Env.Close` 先停任务再关插件 |
| `internal/ui` | 底栏指示灯、`Ctrl+J`、图标着色、内置图标 `activity`、`Launcher.OpenTasks` |
| `internal/shell/tasks.go` | 托盘提示、退出确认、启动器 Toast 或 Windows 通知（点击打开任务） |
| `pkg/plugin/protocol`、`internal/extproc`、`pkg/plugin/rpcserve` | `host/task.runProcess`、`host/toast`、通知 `taskFinished` |
| `internal/cli` | `glctl tasks list`、`glctl tasks show <id>`、会话步骤 `wait-tasks`、假环境的任务执行器和 `tasks.json` fixture |

### 决定和与设计的差异

1. **`task-finished` 怎么知道是哪个任务**：事件 API 没有载荷，所以选了 **`TaskSpec.OnDone func(TaskResult)`**（`json:"-"`，不序列化）。任务结束（成功、失败、取消都算）后在后台 goroutine 调用一次，参数是任务 ID、状态、退出码、错误和最后一行输出；从任务视图"重新运行"时会再次调用同一个回调。没有选 `Host.TaskState(id)` 查询：插件仍然要自己记 ID、再在无载荷的事件里逐个去查，更啰嗦。`task-finished` 事件照样发布（不带载荷，任何插件的任务结束都会触发），适合只想"有任务结束就刷新"的插件。外部插件收到 `taskFinished` 通知（带 `TaskResult`），`rpcserve` 把它转成 `OnDone` 调用；任务在 `host/task.runProcess` 返回 ID 之前就结束的情况也处理了（先暂存结果）。
2. **新增 `Result.Do` 和 `ActionShowTask`**（设计里没有）：Script Commands 的 `fullOutput` 要在启动任务后直接打开它，需要让插件的回调结果带一个宿主动作。`Result.Do` 就是 [M5 §14](m5-plugin-platform.md) 里提的改进项，这次一起做了：只允许宿主自己的动作类型（不允许 `Callback`/`RunCommand`，避免插件自己绕圈），结果合并（`Do` 的 `Push`、`Hide` 生效，`Toast` 以插件的为准）。`ActionShowTask` 的 `Target` 是任务 ID（空表示任务列表），插件动作和 `Result.Do` 都能用。
3. **新增 `ProcessSpec.CommandLine`**：给用户看的命令行。Scoop 的真实 `Argv` 是 `pwsh -EncodedCommand <base64>`，对用户没有意义；任务视图、`glctl tasks list` 和假执行器的输出都用 `CommandLine`（`scoop update && scoop update *`）。为空时用 `Argv` 拼接（带空格的参数加引号）。
4. **新增 `Icon.Tint`**（`success`/`danger`/`accent`/`muted`）：实现"任务自己的图标叠加状态色"——运行中强调色、成功绿色、失败红色、排队和取消灰色。选中行上不着色，否则和选中背景撞色。图片图标（`Path`）不着色。
5. **指示灯的数字**是"排队中 + 运行中"的任务数（`Snapshot.Tasks.Active`），悬停和无障碍标签是"2 background tasks running"。没有活动任务但有没看过的失败任务时显示红点；打开任务视图（列表或单个任务）就算看过。上次运行留下的失败任务不点亮红点。指示灯放在底栏最左边，Toast 显示时也保留。
6. **视图**：命令 `tasks`（"Background Tasks"，关键字含 `tasks`）打开列表视图，分"Running / Queued / Finished"三段，运行中和排队中按开始顺序、已结束的最新在前；另有一个单任务视图 `task`，给通知点击、`ActionShowTask` 和 `Result.Do` 用。右侧详情是输出最后 300 行（更早的输出有提示，可以打开完整日志），元数据是状态、命令行、目录、开始时间、耗时、退出码、队列、插件、日志路径。
7. **动作**：`Enter` 打开完整日志（没有日志文件时没有这个动作）、`Ctrl+Shift+C` 复制输出、`Ctrl+X` 停止（Destructive，先确认）、`Ctrl+R` 重新运行（只限本次运行的进程任务）、"从列表移除"、"清除已结束任务"；单任务视图多一个"Show All Tasks"。复制输出走回调 + `Result.Do`（`CopyText`），这样每个条目不必把最多 2 MB 的输出塞进动作里。
8. **实时刷新**：`builtin.tasks` 记录打开的视图实例（引擎的 `ViewClosed` 通知它关闭），任务有变化时最多每 250ms 用 `Host.Render` 推一次；有任务在运行时每秒推一次，更新已用时间。选中项按条目 ID 保持。
9. **输出**：每个任务在内存里按行保留最后 2 MiB（超出就丢最早的行），完整输出写到 `<日志目录>\<id>.log`（已经解码成 UTF-8 并清理过的行）。任务的描述写在 `<id>.json`，开始和结束时各写一次。ID 是 `t<yymmdd-hhmmss>-<序号>`，和历史不重复。
10. **历史**：启动时读 `*.json`，标记为历史（不能重新运行）；当时还没结束的任务标记为"Interrupted"（失败）。保留最近 50 个已结束任务（包括历史），超出的连同日志一起删除；"移除"和"清除"也删文件。
11. **权限**：`RunProcess`（包括 `ActionRunProcess` 和外部插件的 `host/task.runProcess`）要求清单声明 `exec`，否则任务立即失败（外部插件直接返回 `-32001`）。`RunTask` 是插件自己的 Go 代码，不检查。
12. **通知**：策略在 `tasks.ShouldNotify`：用户主动停止的任务从不通知；`NotifyAlways` 成功失败都通知；默认只在失败时通知。启动器可见时显示在底栏（`Controller.ShowToast`），不可见时发 Windows 通知（ID `task-<id>`，点击后显示启动器并打开这个任务）；`Host.Toast` 走同一条路。glctl 和测试没有设置通知器，通知显示为控制器的 Toast。
13. **退出**：`OnBeforeQuit` 里有活动任务就取消这次退出，弹出"N 个后台任务正在运行，退出会把它们结束"（默认按钮是取消）；确认后再退出，`Env.Close` 先停掉所有任务（结束进程树，最多等 5 秒）再关插件。另一个实例发来的 `--quit`（比如安装程序升级时）不询问。
14. **`Ctrl+J`** 在启动器的任何位置都打开任务列表，优先于动作快捷键。
15. **并发上限** 4 是 `tasks.Options.MaxParallel`，还没有做成用户设置。
16. **外部插件的 `RunTask`**：协议里没有 Go 任务，`rpcserve` 在插件进程里直接运行它（不出现在任务视图，不能从宿主停止），并记一条警告——即设计 §7 的"以后"。
17. **`builtin.tasks` 不在 `builtin.All` 里**，而是由 `app.Build` 总是加进去（GUI、glctl、测试都有）。它的文案在宿主的 `en.json`（`tasks.*`），出现在插件列表和设置里（可以设别名、快捷键）。
18. **glctl 的确定性**：假环境的执行器把任务"按住"，直到 `glctl session wait-tasks`（`tasks list/show` 也会先等），所以每一步的快照都不受时序影响：动作之后指示灯显示 1 个任务在运行，`wait-tasks` 之后任务结束、通知出现。`--fixtures DIR` 里的 `tasks.json`（`[{"match", "output", "exitCode"}]`，按命令行前缀匹配）设置假任务的输出和退出码。`--real` 不带 `--exec` 时用假执行器，并以只读方式读真实的任务历史（不写日志目录），`glctl tasks list --real` 可以看真实历史。
19. **迁移**：Scoop 见 [scoop.md §9](scoop.md)；Script Commands 的 `fullOutput` 改为 `RunProcess` + `Result.Do` 打开任务，条目多了"Show in Background Tasks"（`Ctrl+T`，打开最近一次运行），`compact`/`silent` 不变（进程代码换成 `internal/winapi/proc`）。两个插件自己的输出视图、停止回调和相关文案都删掉了。

### 测试

- `internal/tasks`：各种结局（成功、退出码、启动失败、没有 `exec` 权限、空命令、Go 任务成功/出错/panic）、`OnDone` 和 `OnFinished`、同一队列串行、不同队列并行、全局上限、列表顺序、停止排队中/运行中的进程任务和 Go 任务、输出上限和尾部、日志文件完整、`\r` 和 ANSI 清理、历史读回（输出、中断的任务、不能重跑、ID 不冲突）、保留 50 个的轮转和文件删除、只读模式、重跑保留 `OnDone`、移除、`MarkSeen`、`Close` 停止任务并拒绝新任务、变化通知、通知策略和文案、附注和时长格式；真实 `cmd /c` 进程树（`ping` 子进程）被停止、退出码和环境变量。插件：分段和顺序、附注、图标着色、动作、元数据、单任务视图、已不存在的任务、`Render` 推送和关闭后不再推送、复制/重跑/停止/移除/清除。
- `internal/winapi/proc`：`CleanLine`、`LineWriter`、环境变量合并、批处理参数引号（含 `&`、空参数、`"`）、`Stop` 结束进程树（WaitDelay 设为 1 分钟，进程树没结束就会卡住）。
- `internal/core`：动作里 `RunProcess` + `Result.Do` 打开任务、`OnDone`、`task-finished` 事件、`NotifyAlways` 的 Toast、没有 `exec` 权限、指示灯（数字、红点、`Ctrl+J` 后消失）、`Host.Toast` 走通知器、`Result.Do` 拒绝 `Callback`、`ActionRunProcess`。
- `internal/ui`：指示灯的数字和点击、`Ctrl+J`、红点、任务视图随输出实时刷新、任务结束后指示灯消失、完成通知显示在底栏。`internal/shell`：托盘提示文字、退出确认对话框的文字和默认按钮。
- `internal/extproc`、`rpcserve`：`host/task.runProcess` 的权限检查和排队、`host/toast`、`taskFinished` 转成 `OnDone`（包括 ID 返回前就结束的任务）。
- `internal/cli`：`session_scoop_install` golden（Toast + 指示灯）、`wait-tasks` 后用 `Ctrl+J` 看到任务和输出、`glctl tasks list`、卸载先确认、`tasks.json` fixture 让任务失败（红点、失败通知、`tasks show` 的输出和退出码）。`plugin_list`、`query_empty`、`session_confirm_done`、`run_external_command` 四个 golden 多了 Background Tasks 命令/插件。
- 验证：`gofmt -l .` 为空，`go vet ./...`、`go test ./...`、`go test -race ./internal/... ./plugins/... ./pkg/...`（CGO_ENABLED=1）全部通过；`glctl i18n check` 0 错误 0 警告。没有对真实的 Scoop 执行任何改动命令，没有碰真实剪贴板，没有启动 GUI。

### 合并时注意

- `pkg/plugin/host.go`：`Host` 接口加了三个方法。并行开发的世界时钟分支也改这个文件（加 `EventWindowHidden` 常量）；为了避免冲突，`EventTaskFinished` 放在新文件 `pkg/plugin/tasks.go` 里。其他实现 `plugin.Host` 的类型（如果别的分支新增了）需要补这三个方法。
- `internal/shell/shell.go`：`showLauncher` 拆成 `showLauncherThen`，`OnBeforeQuit` 换成 `beforeQuit`，`Shell` 加了两个字段，`ready` 里加了 `setUpTasks()`。世界时钟分支在这里发布 `EventWindowHidden`（隐藏窗口附近），可能有相邻行冲突，两边都保留即可。
- `internal/ui/icons.go`：新增的 `activity` 图标插在 `trash` 后面（世界时钟分支在末尾加 `clock`），`icon()` 加了着色。
- `internal/builtin/builtin.go`：删掉了 `DryRun`，`realScoop`/`fakeScoop` 不再用 `FakeRunner`。世界时钟分支往 `All`/`Fake` 里加插件，不冲突或只是相邻行。
- golden：`plugin_list`、`query_empty`、`session_confirm_done`、`run_external_command` 都多了 Background Tasks；世界时钟分支也会改其中几个（新命令），合并后用 `go test ./internal/cli -update` 重新生成并核对。
- `internal/core/core_test.go`、`internal/ui/ui_test.go`：两个检查插件列表的测试改成忽略 `builtin.tasks`，根列表的期望多了"Background Tasks"。

## 10. 集成记录（2026-10-15）

- R24 是在 R25（环境感知）合并之前分出来的，合并时 `internal/core/controller.go`、`internal/ui/launcher.go`、`internal/cli/cli.go` 和两份文档有冲突。两边都是在同一位置新增内容：R25 加的是环境标签（`Snapshot.Context`），R24 加的是任务指示灯（`Snapshot.Tasks`）。按结构合并：`Snapshot` 同时保留两个字段，两个新类型、两个渲染函数都保留；底栏两者都会显示，指示灯在左，环境标签紧随应用名之后。
- R25 留下的 `TODO(R24)` 已经处理：`builtin.files` 的"压缩为 ZIP"改为 `Host.RunTask`，`Notify: NotifyAlways`。动作立即返回"正在后台压缩"，完成或失败由任务通知告知。SHA-256 通常很快，仍然同步执行。
- golden `query_context` 和 `session_context_new_folder` 多了常驻的"Background Tasks"命令，差异里只有新增、没有删除，已重新生成。
- 验证：`go test ./...` 和 `go test -race ./internal/... ./plugins/... ./pkg/...` 全部通过。
