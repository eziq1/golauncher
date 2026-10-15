# 稳定性：崩溃定位与插件隔离

对应需求：R11、R12、R14（见 [requirements.md](requirements.md)），以及 R6 的修复记录。

## 1. 文件搜索崩溃（R11）的复盘

**现象**：在 Everything 文件搜索视图里输入关键词时，程序直接退出。日志里只有"启动"记录，没有任何错误信息。

**定位**：

1. 用 `glctl --real` 在终端里跑同样的搜索，没有复现。
2. 写了模拟快速打字的压力测试，直接调用 Everything IPC，也没有复现。
3. 在界面测试里接上真实的 Everything，模拟打字、选择、退格，还是没有复现。
4. 加上崩溃捕获（§2）后，你在真机上复现了一次，日志里拿到了完整的调用栈：

```
panic: runtime error: index out of range [-1]
core.itemAt(...)                 internal/core/controller.go:242
core.Snapshot.SelectedEntry(...) internal/core/controller.go:553
ui.(*Launcher).footer.func1()    internal/ui/launcher.go:548
```

**根因**：Everything 搜不到结果时，视图返回一个空分组，这时 `Snapshot.Selected` 是 -1（表示没有选中项）。底栏调用 `SelectedEntry()`，`itemAt` 先取了 `s.Items[-1]`，之后才检查下标是否合法。这次越界发生在主线程的界面渲染里，所以整个进程退出了。

**为什么之前没测到**：已有测试的视图都开着 `FilterLocally`，空分组会被过滤掉；而复现时用的几个关键词恰好都有结果。

**修复**：`itemAt` 先检查 `i < 0`。另外加了三层回归测试：

- 核心层 `TestEmptySectionNoSelection`；
- 界面层 `TestEmptySectionDoesNotCrash`，CI 上也会运行；
- 接真实 Everything 的 `TestRealFileSearch`，加了一个搜不到结果的关键词。

这三个测试都确认过：修复前能复现同样的 panic，修复后通过。

**教训**：这次崩溃不在插件里，而在宿主自己的界面代码里。所以光防插件不够，宿主自身的崩溃也必须能留下现场（§2）。

## 2. 怎么逮住崩溃（R12）

GUI 程序用 `-H windowsgui` 构建，没有控制台，Go 运行时把 panic 信息写到 stderr 就丢了。Go 程序 panic 时会直接用退出码 2 结束进程，不经过 Windows 错误报告，所以事件日志里也没有记录。

**对策**（`internal/shell/log.go`）：

- 启动时调用 `debug.SetCrashOutput(日志文件)`。此后任何未捕获的 panic，以及运行时致命错误（比如并发写 map），完整调用栈都会追加到 `%LOCALAPPDATA%\golauncher\logs\golauncher.log`。
- `os.Stderr` 也重定向到日志文件，第三方库零散写到 stderr 的内容不会丢。

**排查流程**：

1. 让用户复现，然后看日志末尾 `panic:` 或 `fatal error:` 开头的调用栈。
2. 能复现时，可以用 `glctl --real` 在终端里跑同样的操作，或者构建一个不带 `-H windowsgui` 的版本，直接在终端看输出。
3. 修复时，必须先写一个能复现这次崩溃的测试，确认它在修复前失败、修复后通过。

**以后可以考虑**：程序启动时检测上次是否异常退出，比如日志末尾有调用栈、或者没有正常的退出记录，然后在界面上提示"上次运行异常退出，是否打开日志"。

## 3. 主程序与插件之间的防护（R14）

### 3.1 进程内防护（已实现）

| 插件出的问题 | 防护方式 | 代码位置 |
|------------|----------|----------|
| `RootItems`、`View`、`HandleAction` 里 panic | `guard` 用 `recover` 接住，这次调用变成错误，界面显示"插件崩溃"；调用栈写进日志 | `internal/core/engine.go` 的 `guard` |
| `Init` 或 `Close` 里 panic | 同上；`Init` 崩溃的插件标为不可用，设置页显示原因 | 同上 |
| 事件回调里 panic | `Bus.Publish` 通过 `host.Go` 分发事件，接住 panic | `internal/host/events.go` |
| 插件自己的后台 goroutine 里 panic | SDK 新增 `Host.Go(fn)`，插件的后台任务必须用它启动，由宿主兜底 | `pkg/plugin/host.go`、`internal/host/recover.go` |
| 插件卡死，比如死循环或无视 ctx 一直阻塞 | 根搜索每个插件限时 150ms；视图限时 10s，动作限时 30s。超时就放弃这次调用并提示用户，界面和后续按键照常响应 | `callPlugin` |

规范检查（`internal/lint`）会禁止内置插件直接使用 `go` 语句或 `wg.Go`，保证所有后台任务都经过 `Host.Go`。

测试：

- `TestPluginPanicIsContained`
- `TestHungPluginTimesOut`
- `TestInitPanicMakesPluginUnavailable`
- `TestEventAndGoroutinePanicsContained`

### 3.2 进程内防护做不到的

Go 的 `recover` 只能接住当前 goroutine 里的 panic。下面这些情况会直接终止整个进程，进程内没有办法拦截：

- 运行时致命错误：并发写 map、栈溢出、内存耗尽；
- 系统调用和 cgo 里的内存访问违规；
- 插件绕过 `Host.Go`，自己直接起的 goroutine 里发生的 panic；
- 超时被放弃的调用：对应的 goroutine 仍在运行，会继续占用资源。

### 3.3 彻底隔离：外部插件进程（M5）

第三方插件一律跑在独立进程里，通过 JSON-RPC 通信（见 [design.md §5.5](design.md)）：

- 插件进程崩溃只影响它自己，宿主按退避策略重启，连续失败就熔断停用，并在设置页显示原因；
- 每次调用都有超时，超时不会阻塞界面；
- 内存泄漏、死循环之类的问题，可以直接结束插件进程来回收资源。

内置插件都经过测试，继续留在进程内，以免增加 IPC 开销，靠 §3.1 的防护兜底。

## 4. 启动器顶部蓝条（R6）的修复记录

MyGo 为了让 Acrylic 背景铺满窗口，调用了 `DwmExtendFrameIntoClientArea(-1)`。系统开了"在标题栏上显示强调色"时，DWM 会在原来标题栏的位置画上强调色。修复办法是在窗口创建后设置 `DWMWA_CAPTION_COLOR = DWMWA_COLOR_NONE`（`internal/shell/dwm_windows.go`）。这个属性只有 Windows 11 支持，Windows 10 上调用会失败，但没有副作用。2026-10-15 已在真机上确认蓝条消失。
