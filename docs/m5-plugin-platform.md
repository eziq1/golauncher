# M5 插件平台设计方案（v0.2）

> 状态：设计 v1 · 2026-10-15 · 对应需求 R18（见 [requirements.md](requirements.md)）
> 前置文档：[design.md §5](design.md)（插件系统）、[plugin-render-model.md](plugin-render-model.md)（拉取式与推送式）、[plugin-settings.md](plugin-settings.md)（设置页集成）、[reliability.md](reliability.md)（稳定性）、[raycast-extensions-research.md](raycast-extensions-research.md)（Raycast 调研）

## 0. 目标与范围

把 golauncher 从"内置插件的集合"变成一个**可以由第三方扩展的插件平台**，同时补齐 v0.1 暴露出的模型短板。

| # | 子项 | 解决的问题 |
|---|------|------------|
| ① | `Host.Invalidate`：通知后再拉取 | 剪贴板、应用列表、Everything 的视图不会自动刷新 |
| ② | Form 视图与命令参数 | 插件无法收集结构化输入；Raycast 扩展里大量使用这两样 |
| ③ | 设置项扩展：按钮、动态选项、分组、条件显示 | 声明式设置表达力不够（OAuth 登录、测试连接等） |
| ④ | 外部插件：JSON-RPC、进程隔离、推送快照 | 第三方插件可以用任意语言编写，崩溃时不会拖垮宿主 |
| ⑤ | 兼容 Raycast Script Commands | 用户写一个脚本就能变成命令；顺带复用 Raycast 的脚本生态 |

**不在范围内**：

- Raycast 扩展（React/Node）的兼容，属于 M7。
- 插件商店和自动下载。
- 自动更新和代码签名，属于 M6。

**验收标准**：

1. 用 Go 和 Python 各写一个示例外部插件，都能跑通。
2. 外部插件崩溃或卡死时，宿主不受影响，并能自动重启它；连续失败会被熔断。
3. 剪贴板历史视图开着的时候复制新内容，列表自动刷新。
4. 带参数的命令和 Form 视图都能在 GUI 和 `glctl` 里完整操作。
5. 一个 `.ps1` 脚本命令能出现在根搜索里，并能运行。
6. 所有新能力都有测试，CI 全绿。

## 1. 总体架构

```
┌────────────── golauncher 进程 ───────────────┐
│ ui / shell                                   │
│ core.Controller ◄── invalidate / render ──┐  │
│ core.Engine ── 调用 ──► plugin.Plugin 接口 │  │
│        ├─ 内置插件（进程内）───────────────┤  │
│        └─ extproc.Proxy（实现同一套接口） ─┘  │
│              │ stdio · JSON-RPC 2.0（NDJSON） │
└──────────────┼───────────────────────────────┘
               ▼
     外部插件进程（任意语言；Go 插件用 pkg/plugin/rpcserve）
```

关键原则：

- **对 Engine 来说，外部插件和内置插件没有区别。** `extproc.Proxy` 实现了和内置插件同样的 `plugin.Plugin` / `RootProvider` / `ViewProvider` / `ActionHandler` / `LocaleProvider` 接口，引擎、排序、Controller、`glctl` 都不需要知道插件跑在哪里。
- **SDK 只扩展，不破坏。** 新能力都以新增方法、新增字段、新增可选接口的形式加入。v0.1 的插件不需要任何修改。
- **以拉取为主，推送为辅。** 详见 [plugin-render-model.md](plugin-render-model.md)。

## 2. ① Invalidate：通知后再拉取

### SDK

```go
type Host interface {
    // ...
    // Invalidate tells the host that data shown by the plugin changed. The
    // host re-pulls the root results and/or the plugin's open views whose ID
    // equals viewID ("" = all of them), coalescing bursts.
    Invalidate(viewID string)
}
```

### 宿主的行为

- `host.Services.OnInvalidate(pluginID, viewID)` 回调到 Engine，Engine 再转发给订阅了它的 Controller。
- Controller 判断是否需要重新拉取：
  - 栈顶是这个插件的视图，并且 `viewID` 为空或与视图 ID 相同 → `refresh`；
  - 栈顶是根搜索，并且这个插件提供 `RootProvider` → `refresh`。
- **合并**：同一个 Controller 在 50ms 内收到的多次通知只刷新一次。拉取期间又来了通知，就等这次拉取完成后再补一次。
- **选中项保持**：刷新后，按条目的 `Key` 找回原来选中的那一项；找不到才按原下标选中。用户正在看第 5 条时，不会因为刷新跳回第一条。
- **确定性**：`Controller.Wait` 要等合并中的通知也处理完。这样 `glctl` 会话和测试依旧确定。

### 插件改动

| 插件 | 调用时机 |
|------|----------|
| clipboard | 记录新内容、清理过期记录之后，调用 `Invalidate("history")` |
| apps | 后台索引刷新完成、图标生成完成之后，调用 `Invalidate("")` |
| everything | Everything 从"未运行"变为可用时，调用 `Invalidate("search")` |

## 3. ② Form 视图与命令参数

### 3.1 Form 视图

**SDK**：

```go
const ViewForm ViewKind = "form"

type View struct {
    // ...
    Form *Form `json:"form,omitempty"`
}

type Form struct {
    Fields []Field `json:"fields"`
    // Submit is run with the field values on Ctrl+Enter / the submit button.
    Submit Action `json:"submit"`
}

type FieldType string // text | password | textarea | number | checkbox | dropdown | date | file

type Field struct {
    ID          string         `json:"id"`
    Type        FieldType      `json:"type"`
    Title       string         `json:"title"`
    Placeholder string         `json:"placeholder,omitempty"`
    Info        string         `json:"info,omitempty"`     // 字段下方的说明文字
    Default     jsontext.Value `json:"default,omitempty"`
    Options     []Option       `json:"options,omitempty"`  // dropdown
    Required    bool           `json:"required,omitzero"`
}

type ActionDo struct {
    // ...
    // Values carries the form's field values (Form submit) or the entry's
    // argument values (commands with arguments), filled in by the host.
    Values map[string]jsontext.Value `json:"values,omitempty"`
}

type Result struct {
    // ...
    // FieldErrors keeps the form open and shows these messages by field ID.
    FieldErrors map[string]string `json:"fieldErrors,omitempty"`
}
```

**交互**（Controller 持有表单状态，和导航栈一样由宿主管理）：

- 进入 Form 视图后隐藏搜索框，焦点放在第一个字段。
- `Tab` / `Shift+Tab` 切换字段。`Enter` 在单行字段里跳到下一个字段，在最后一个字段里提交；`Ctrl+Enter` 在任何字段里都直接提交；`Esc` 返回上一级。
- 提交前，宿主先检查 `Required` 和 `number` 格式，失败就在字段下方显示错误，不调用插件。插件返回 `FieldErrors` 时同样显示。
- Controller 的 API：`SetField(id, value)`、`FocusField(id)`、`Snapshot.Form{Fields[{…, Value, Error}], Focused}`。
- `glctl session` 新增步骤 `field <id> <value>`，方便 Agent 测试表单。

### 3.2 命令参数

**SDK**：参数放在 `Item` 上（命令在根搜索里本身也是 Item），`Command` 也可以声明参数：

```go
type Argument struct {
    Name        string       `json:"name"`
    Type        ArgumentType `json:"type"` // text | password | dropdown
    Placeholder string       `json:"placeholder"`
    Required    bool         `json:"required,omitzero"`
    Options     []Option     `json:"options,omitempty"`
}
// Command.Arguments / Item.Arguments []Argument（最多 3 个，与 Raycast 一致）
// ViewRequest.Arguments map[string]string；ActionDo.Values 也会带上参数值
```

**交互**（参考 Raycast）：

- 选中带参数的条目时，搜索框右侧出现参数输入框。`Tab` 从搜索框进入第一个参数，参数之间用 `Tab` / `Shift+Tab` 切换。
- 按 `Enter` 时：
  - 必填参数都已填写 → 执行，参数值通过 `ViewRequest.Arguments` 或 `ActionDo.Values` 传给插件；
  - 有必填参数没填 → 焦点跳到第一个空着的参数，不执行。
- Controller 的 API：`SetArgument(name, value)`；`Snapshot.Arguments{Entry, Fields, Focused}`。
- `glctl session` 新增步骤 `arg <name> <value>`。

## 4. ③ 设置项扩展

| 扩展 | SDK | 宿主行为 |
|------|-----|----------|
| 按钮型设置项 | `PrefButton PrefType = "button"`；`Preference.Action ActionDo`（一般是 `Callback`） | 设置页显示一个按钮，点击后调用插件的 `HandleAction`，把返回的 Toast 显示在按钮旁边。用于"登录"、"测试连接" |
| 动态选项 | `Preference.DynamicOptions bool`；新增可选接口 `PreferenceOptionsProvider { PreferenceOptions(ctx, name string) ([]Option, error) }` | 打开插件设置页时调用一次，失败就显示错误 |
| 分组 | `Preference.Group string`（可以写成 `%key%`） | 同组的设置项放在一个带标题的分组里 |
| 条件显示 | `Preference.VisibleWhen *Condition{Pref string; Equals jsontext.Value}` | 只有当另一项设置等于指定值时才显示 |

`core.PluginInfo` 里带上这些字段（已经本地化好），`glctl plugin info` 也能看到。

## 5. ④ 外部插件

### 5.1 安装与发现

- **目录**：`%APPDATA%\golauncher\plugins\<id>\`，每个子目录是一个插件；另外 `GOLAUNCHER_PLUGIN_PATH` 环境变量可以追加目录，用 `;` 分隔，方便开发时直接指向源码目录。
- **清单** `plugin.json`：就是 `plugin.Manifest` 的 JSON 形式，再加上 `run` 字段：

```json
{
  "id": "example.hello-python",
  "name": "%plugin.name%",
  "version": "0.1.0",
  "protocol": 1,
  "run": { "command": "python", "args": ["-u", "main.py"] },
  "permissions": ["storage", "clipboard"],
  "commands": [ { "name": "greet", "title": "%command.greet.title%", "mode": "view" } ]
}
```

  - `run.command` 可以是相对路径（相对插件目录，比如 `hello.exe`），也可以是 PATH 里的程序（比如 `python`、`node`）。
  - 翻译文件放在 `locales/<lang>.json`，`%key%` 的解析规则和内置插件相同。
- **不签名、不提示**：按 [design.md §14](design.md) 的决定，第一版不做签名校验，也不弹信任提示。设置页如实说明插件以当前用户的权限运行。调研建议加首次运行提示，这个问题还在等你决定（见 §9）。
- **"重新加载插件"命令**（`builtin.core/reload-plugins`）：重新扫描插件目录，不需要重启 golauncher。

### 5.2 协议：JSON-RPC 2.0 over stdio

- **帧格式**：每行一个 JSON 对象（NDJSON，UTF-8）。各种语言都容易实现，Python 用 `sys.stdin` 逐行读即可。
- **stderr**：按行写进宿主日志，带上插件 ID。
- **版本协商**：`initialize` 交换 `protocol` 版本号，主版本号不一致就拒绝加载，插件标为不可用。

**宿主 → 插件（请求）**：

| 方法 | 参数 → 结果 |
|------|-------------|
| `initialize` | `{protocol, hostVersion, locale, dataDir, cacheDir, preferences}` → `{capabilities: {rootItems, views, actions, preferenceOptions, render}}` |
| `rootItems` | `{query}` → `Item[]` |
| `view` | `{request: ViewRequest}` → `View` |
| `handleAction` | `{action: ActionDo}` → `Result` |
| `preferenceOptions` | `{name}` → `Option[]` |
| `shutdown` | `{}` → `null`；之后宿主关闭 stdin，等待 2 秒，仍未退出就结束进程 |

**宿主 → 插件（通知）**：

- `$/cancelRequest {id}`：用户继续打字时，取消过期的请求；
- `localeChanged {locale}`；
- `preferencesChanged {preferences}`；
- `event {name}`：转发宿主事件，比如 `clipboard-changed`；
- `viewClosed {instance}`：视图被关闭，插件可以释放它的状态。

**插件 → 宿主（请求）**：

| 方法 | 需要的权限 |
|------|------------|
| `host/storage.get` / `set` / `delete` | `storage` |
| `host/clipboard.read` / `writeText` / `writeImage` / `writeFiles` | `clipboard` |
| `host/subscribe {event}` | 无 |
| `host/log {level, message}` | 无 |

**插件 → 宿主（通知）**：

- `host/invalidate {viewId}`：即 ①；
- `ui/render {instance, version, view}`：即 §5.4。

**权限检查**在宿主一侧，插件调用了 manifest 里没有声明的能力，会收到错误码 `-32001 PermissionDenied`。`exec` 和 `network` 是插件进程自己的行为，宿主没法拦截，这一点在文档和设置页里都如实说明。

### 5.3 进程生命周期与隔离（`internal/extproc`）

| 场景 | 行为 |
|------|------|
| 启动 | **懒启动**：第一次被调用时才启动进程。`initialize` 超时 5 秒。启动期间到达的调用排队等待 |
| 空闲 | 5 分钟没有任何调用、也没有打开的视图时，发送 `shutdown` 回收进程 |
| 单次调用 | 沿用 Engine 的超时：根结果 150ms、视图 10 秒、动作 30 秒。超时后发送 `$/cancelRequest` |
| 崩溃 | 进程意外退出时，正在等待的调用全部返回错误，界面显示"插件崩溃"。下一次调用时按退避策略重启：1 秒、2 秒、4 秒……最长 30 秒 |
| 熔断 | 2 分钟内崩溃 5 次，插件标为不可用，设置页显示原因；用户可以在设置页手动"重新启用" |
| 卡死 | 连续 3 次调用超时，视为卡死，结束进程，计一次崩溃 |
| 资源回收 | Windows 下用 Job Object 绑定插件进程，宿主退出时（包括宿主自己崩溃）插件进程会被系统一起回收 |

### 5.4 推送快照（`ui/render`）

- Controller 给每一个视图帧分配一个实例 ID，比如 `v17`，放在 `ViewRequest.Instance` 里传给插件。
- 插件随时可以发送 `ui/render {instance, version, view}`。宿主只接受当前栈里这个实例的、`version` 更新的快照；对栈顶的视图，替换内容并保持选中项（按 Key）。
- 推送频率由宿主限制，最多每 16ms 处理一次，多余的合并成最后一份。
- 进程内插件也可以用：SDK 新增 `Host.Render(instance string, v View)`，语义完全相同。
- 这是将来兼容 Raycast 扩展（M7）的基础：Node sidecar 内部运行 React，对外只发 `ui/render`。

### 5.5 `pkg/plugin/rpcserve`：Go 插件一行变成外部插件

```go
func main() { rpcserve.Serve(hello.New()) }
```

`Serve` 在插件进程里实现一个通过 JSON-RPC 回调宿主的 `plugin.Host`，再把宿主的请求分派给插件的各个接口。同一份插件代码既可以编进宿主，也可以作为外部插件运行。

### 5.6 示例与开发者工具

- `examples/plugins/go-hello`：用 rpcserve 写的 Go 插件，带视图、动作、存储。
- `examples/plugins/python-hello`：只用 Python 标准库，自带一个约 80 行的协议实现。有根结果、Form 视图、调用 `host/clipboard.writeText`。
- `glctl plugin validate <dir>`：检查 `plugin.json` 格式、翻译 key 是否齐全、`run.command` 能否找到。
- `glctl` 默认在假环境中运行，同样会加载外部插件：`--plugins DIR` 指定插件目录。外部插件的端到端测试（`internal/extproc`）也用它们来跑。

## 6. ⑤ 兼容 Raycast Script Commands（`plugins/scripts`）

[Script Commands](https://github.com/raycast/script-commands) 是带注释元数据的脚本，例如：

```powershell
# @raycast.schemaVersion 1
# @raycast.title Flush DNS
# @raycast.mode compact
# @raycast.packageName Network
# @raycast.icon 🌐
# @raycast.argument1 { "type": "text", "placeholder": "host", "optional": true }
# @raycast.needsConfirmation true
```

**插件 `builtin.scripts`**：

- **扫描目录**：偏好项 `directories`（stringList），默认是 `%APPDATA%\golauncher\scripts`。扫描时机：启动时、目录内容变化时（每次窗口显示时检查修改时间），变化后调用 `Invalidate("")`。
- **解释器**：

  | 扩展名 | 执行方式 |
  |--------|----------|
  | `.ps1` | `powershell -NoProfile -ExecutionPolicy Bypass -File` |
  | `.py` | `python` |
  | `.js` | `node` |
  | `.bat` / `.cmd` | `cmd /c` |
  | `.sh` | 找得到 Git Bash 或 WSL 才支持 |

  脚本第一行的 shebang 优先。
- **作为根搜索条目**：每个脚本是一条，分组为 Script Commands。标题、`packageName` 作为副标题、图标（emoji、图片路径或 URL）、关键词、参数（对应 ② 的命令参数），以及 `needsConfirmation` 对应 Destructive，都从元数据读取。
- **模式**：

  | 模式 | 行为 |
  |------|------|
  | `silent` | 后台运行，隐藏窗口，结束后不提示（失败时弹 Toast） |
  | `compact` | 运行结束后，把输出的最后一行作为 Toast 显示 |
  | `fullOutput` | 打开一个输出视图，边运行边追加输出（通过 `Invalidate` 刷新），结束后显示退出码 |
  | `inline` | 暂时按 `compact` 处理（Raycast 里它表示在根搜索里定时刷新显示，留待以后） |

- **运行**：用 `Host.Go` 启动进程，所有脚本都可以从输出视图里取消。环境变量 `GOLAUNCHER=1` 表示在 golauncher 里运行，同时也设置 `RAYCAST_*` 这类兼容变量。
- **命令**：`New Script Command`，在脚本目录里按模板生成一个 `.ps1` 并打开；`Open Scripts Folder`。
- 实测约 47% 的 Raycast 脚本依赖 macOS，仓库里的 `.ps1` 只有 3 个。所以这项的价值主要在于让用户能用自己写的脚本扩展 golauncher，导入 Raycast 现成的脚本只是顺带的好处。

## 7. 对现有代码的影响

| 模块 | 改动 |
|------|------|
| `pkg/plugin` | 新增 `Invalidate`、`Render`；Form、Field；Argument；`ActionDo.Values`、`Result.FieldErrors`；Preference 扩展；`PreferenceOptionsProvider`；`ViewRequest.Instance` 和 `Arguments` |
| `internal/host` | Host 新方法的实现；回调到 Engine；hostfake 同步实现 |
| `internal/core` | Engine 转发 invalidate 和 render；Controller 增加合并刷新、实例 ID、表单状态、参数状态，刷新后保持选中项；Snapshot 增加 Form 和 Arguments |
| `internal/ui` | 表单渲染、参数输入框、设置页的按钮、动态选项、分组和条件显示；插件页显示"外部插件"标识和崩溃或熔断状态 |
| `internal/extproc`（新） | 发现、Proxy、JSON-RPC、生命周期、权限检查、Job Object |
| `pkg/plugin/rpcserve`（新） | Go 外部插件的运行框架 |
| `plugins/scripts`（新） | Script Commands |
| `internal/builtin`、`internal/app` | 装配外部插件（真实环境和假环境）；`glctl --plugins` |
| `internal/cli` | 会话新增 `field`、`arg` 步骤；`plugin validate`；新的 golden 测试 |
| 文档 | 本文；更新 plugin-guide（英文），新增"外部插件协议"一节；更新 design.md §5.5 和 §12；CHANGELOG |

## 8. 实施计划（多 Agent 并行）

| 波次 | 内容 | 负责 |
|------|------|------|
| W0 | 本设计；SDK 全部新类型和接口；host/hostfake 的空实现；需求台账 | 主控（我） |
| W1-A | ① ② ③ + 推送快照的宿主侧（core Controller 和 Engine、ui、cli 会话步骤），以及三个内置插件调用 Invalidate | 子代理 A |
| W1-B | ④ 外部插件：extproc、rpcserve、示例插件、发现与装配、`glctl --plugins` 和 `plugin validate` | 子代理 B |
| W1-C | ⑤ Script Commands 插件（基于 W0 的 SDK，用 hostfake 测试） | 子代理 C |
| W2 | 集成、端到端测试（Python 插件崩溃、熔断、表单、参数、脚本）、文档、发布 0.2.0 | 主控 |

子代理 A 是 Controller 的唯一修改者，B 和 C 不碰 core，以免冲突。

## 9. 待决问题

1. **外部插件首次运行是否提示信任**：§14 定的是不签名、不提示；Raycast 调研建议至少做首次运行提示。本方案先按 §14 实现，提示只需要在 `extproc` 启动前加一个确认，改动很小，等你决定。
2. **Python 和 Node 解释器的发现**：第一版只在 PATH 里查找。找不到时插件标为不可用，原因写成"找不到 python"。
3. **`.sh` 脚本**：依赖 Git Bash 或 WSL，第一版只做"找得到才支持"。

## 10. 风险

| 风险 | 对策 |
|------|------|
| Controller 状态继续膨胀（表单、参数、实例 ID） | 表单状态和参数状态各自独立成结构体，挂在 frame 上；每一种都要有 `glctl` 会话级别的 golden 测试 |
| 外部插件的 stdout 混进了非协议输出（比如 Python 的 print） | 按行解析，非 JSON 的行当作日志处理并告警；文档要求插件只往 stderr 写日志 |
| Windows 上的子进程管理（僵尸进程、控制台窗口闪一下） | Job Object；创建进程时加 `CREATE_NO_WINDOW` |
| 推送和导航之间的竞态 | 同时用实例 ID 和版本号把关；为这种竞态写专门的测试 |

## 11. 实现记录：④ 外部插件（W1-B）

### 代码结构

| 位置 | 内容 |
|------|------|
| `pkg/plugin/jsonrpc` | 双向 JSON-RPC 2.0 连接，NDJSON 帧格式。收到的请求各自在一个 goroutine 里处理，处理函数可以反过来调用对方；通知在读循环里按顺序处理。处理函数 panic 时返回 `-32603` 错误，不会终止进程。ctx 结束时自动发送 `$/cancelRequest`。非 JSON 行记一条告警后丢弃 |
| `pkg/plugin/protocol` | 方法名、参数和结果类型、协议版本号 `Version = 1` |
| `pkg/plugin/rpcserve` | `Serve(p)`：插件进程里的 `plugin.Host` 实现和请求分派。`os.Stdout` 被重定向到 stderr，防止误打印破坏协议流 |
| `internal/extproc` | 发现（`Find`、`Load`、`DefaultRoots`、`EnvDirs`）、`Proxy`、进程管理、宿主侧权限检查、`Validate` |
| `internal/extproc/extproctest` | 测试辅助：把 `examples/plugins/<name>` 编译到临时目录 |
| `examples/plugins/{go-hello,python-hello,crashy}` | 示例插件和测试用的"捣乱"插件 |
| `docs/plugin-protocol.md` | 给第三方实现的协议规范（英文） |

### 与设计的差异和补充

- **协议细节**：`initialize` 的参数多了 `pluginId`；存储的值按文本传（`value`），不是合法 UTF-8 的值用 `base64` 字段；`host/log` 既可以作为请求发，也可以作为通知发（rpcserve 用通知，不用等回复）；`Init` 返回 `ErrUnavailable` 时，`initialize` 以错误码 `-32002` 返回，消息就是原因。
- **能力位**：`initialize` 返回的 `capabilities` 记在 Proxy 上。没有 `rootItems` 能力的插件，以后不会因为用户在根搜索里打字而被拉起来。
- **超时与卡死**：沿用 Engine 的 `callPlugin` 超时。只有 ctx 因 deadline 结束（`DeadlineExceeded`）才计入"连续超时"，用户继续打字导致的取消（`Canceled`）不算；等待进程启动期间超时也不算。
- **重启退避**：连续崩溃次数决定退避时间（1s、2s、4s……最长 30s），一次成功的调用会把它清零。熔断窗口 2 分钟内 5 次。启动失败（进程启动后立即退出、`initialize` 超时）也算一次崩溃。
- **不可用的三种来源**：manifest 的 `protocol` 不对、`run.command` 找不到（`Init` 时就检查，Engine 直接把插件列为不可用）；进程在 `initialize` 中报告不可用或协议版本不对（运行时，`ProcessState` 报 `unavailable`）。
- **glctl 的确定性**：假环境里外部插件在 `Init` 时就启动并等待初始化完成（`Options.Eager`），否则第一次查询会因为 150ms 的根搜索超时而拿不到外部插件的结果。真实环境仍是懒启动。
- **空闲回收与打开的视图**：Proxy 记录 `ViewRequest.Instance` 出现过的实例，直到 `ViewClosed(instance)` 才移除；有打开的视图时不回收。
- **翻译文件容错**：`locales/` 里解析失败的文件会被跳过并告警；没有 `en.json` 时补一个空的。这样一个坏的翻译文件不会让整个宿主启动失败（`i18n.Manager.Register` 遇错会让 Engine 构建失败）。
- **重复 ID**：与内置插件或先加载的外部插件 ID 相同的，跳过并告警。
- **示例放在主模块里**：`examples/plugins/*` 是主模块下的 `package main`，`go build ./...` 和 `go vet ./...` 会覆盖它们，测试也能直接编译。lint 的插件规则只检查 `plugins/`；`glctl i18n check` 跳过 `examples/`，示例插件的翻译键由 `glctl plugin validate` 检查（manifest 中的 `%key%`）。Python 示例只有 `.py`，不受影响。
- **Python 辅助代码**约 130 行（含注释），比设计里估计的 80 行多，因为要处理"等待宿主回复期间收到其他请求"的排队。

### 主控合并时需要接线的地方

1. **`viewClosed`**：`extproc.Proxy.ViewClosed(instance string)` 已实现（发通知、清理实例状态、允许空闲回收）。A 加上 `Engine.OnViewClosed` 后，在里面对插件做类型断言：`if vc, ok := l.p.(interface{ ViewClosed(string) }); ok { vc.ViewClosed(instance) }`。
2. **熔断后重新启用**：`core.Engine.ResetPlugin(id)`（在 `internal/core/plugins.go`）调用 `core.ProcessPlugin.Reset()`。设置页在 `PluginInfo.State == "disabled"` 时显示原因（`StateReason`）和"重新启用"按钮，按钮调用 `ResetPlugin`。
3. **设置页**：`PluginInfo.Origin`（`builtin` / `external`）和 `State`、`StateReason` 已经有了，UI 显示"外部插件"标识和状态由 A 或主控完成。
4. **"重新加载插件"命令**（§5.1 的 `builtin.core/reload-plugins`）没有做：Engine 目前在 `New` 时固定插件列表，需要 Engine 支持增删插件，留给主控。
5. **`hostVersion`**：`extproc.Options.HostVersion` 默认 `dev`；`builtin.All` 还拿不到版本号，需要的话由 shell 传入。
