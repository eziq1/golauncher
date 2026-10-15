# 后台任务设计方案

> 状态：设计 v1 · 2026-10-15 · 对应需求 R24（见 [requirements.md](requirements.md)）
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
