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

- **扫描目录**：偏好项 `directories`（stringList），默认是 `%APPDATA%\golauncher\scripts`。扫描时机：启动时、目录内容变化时（每次窗口显示时检查修改时间），变化后调用 `Invalidate("")`。启动时的扫描在后台进行（R88），`Init` 不等它，慢速或断开的网络目录不会拖住启动；扫描完成前的根搜索只返回已知结果，扫描完成后调用 `Invalidate("")` 让宿主重新拉取。glctl 的假环境用 `Options.Sync` 在 `Init` 里同步扫描，保证结果确定。
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

### 6.1 实现记录（W1-C）

代码在 `plugins/scripts`，测试全部基于 hostfake，运行类测试用临时目录里真实的 `cmd` 和 `powershell` 脚本。

- **文件**：`metadata.go`（元数据解析）、`interp.go`（解释器选择，`System` 可注入以模拟找不到解释器）、`run.go`（运行、模式、输出视图）、`output.go`（输出收集）、`proc_windows.go`（无窗口启动、Job Object、OEM 代码页解码）、`scripts.go`（清单、扫描、根条目、命令）。
- **元数据**：读前 40 行，注释前缀支持 `#`、`//`、`--`、`::`、`REM`/`@REM`（`.bat`/`.cmd` 不读 shebang）。没有 `@raycast.title` 的文件不算脚本命令。`mode` 缺失或无法识别时按 `compact`；`inline` 按 `compact`（`refreshTime` 解析但忽略）。`packageName` 缺省为所在目录名（与 Raycast 一致）。`argumentN` 的 JSON 解析失败时，整个脚本跳过并记日志。已废弃的 `secure: true` 视为 `password`。和 Raycast 一样跳过 `*.template.*`。
- **参数**：参数名为 `argument1`..`argument3`，`optional` 映射为 `Required=false`，`dropdown` 的 `data` 映射为 `Options`。运行时按序号从 `ActionDo.Values` 取值，未填的可选参数传空字符串；`percentEncoded` 用 `url.QueryEscape` 并把 `+` 换成 `%20`（与 `encodeURIComponent` 接近）。
- **解释器**：`.ps1` 优先 `pwsh`，否则 `powershell`，参数 `-NoProfile -NonInteractive -ExecutionPolicy Bypass -File`；`.py` 依次找 `python`、`python3`、`py`；`.sh` 先找 `%ProgramFiles%\Git\bin\bash.exe` 等位置，再找 PATH 里的 `bash`，但**跳过 System32 下的 WSL bash**（它不认 Windows 路径）。shebang 优先，支持 `/usr/bin/env x` 和带参数的形式；指向 pwsh 时丢弃 Unix 侧参数。找不到解释器时条目照常显示，运行时弹失败 Toast“xxx was not found”。
- **批处理的命令行**：`.bat`/`.cmd` 通过 `SysProcAttr.CmdLine` 自己拼 `cmd /d /s /c ""脚本" "参数"…"`，参数里的 `&`、`|` 等不会被 cmd 解释；参数里的 `"` 只能写成 `""`，`%VAR%` 仍可能被 cmd 展开，这是 cmd 本身的限制。
- **扫描**：`Init` 里同步扫描一次（`glctl` 首次查询即可看到脚本）；`EventWindowShown` 和 `EventConfigChanged` 时在 `Host.Go` 里重新扫描。是否变化看的是目录内脚本文件的名称、大小、修改时间的哈希，而不只是目录 mtime——NTFS 上修改文件内容不会更新目录 mtime，只看目录会漏掉"改了标题"。有变化才 `Invalidate("")`。默认目录 `%APPDATA%\golauncher\scripts` 在 Init 时创建；偏好里的 `%VAR%` 会展开。
- **运行**：`exec.Cmd` 加 `CREATE_NO_WINDOW`，stdout 与 stderr 合并到同一个有上限（1 MiB，保留尾部）的缓冲；环境变量加 `GOLAUNCHER=1`、`PYTHONUNBUFFERED=1`（否则 Python 输出不流式）、`PYTHONIOENCODING=utf-8`。Raycast 文档里没有定义任何 `RAYCAST_*` 环境变量，所以没有设置。启动后把进程放进 Job Object，Stop 和插件 Close 时 `TerminateJobObject` 结束整个进程树；**不**设置 `KILL_ON_JOB_CLOSE`，脚本正常结束后它打开的程序（如 `start notepad`）继续运行。`WaitDelay` 2 秒，防止残留子进程占着管道导致等待不返回。输出按行解码：合法 UTF-8 原样使用，否则按 OEM 代码页解码（cmd 和 Windows PowerShell 往管道写的是 OEM）；去掉 ANSI 转义，`\r` 覆盖的进度行只保留最后一段。
- **模式**：`silent` 立即返回 `Hide`，失败只记日志（SDK 没有"稍后弹 Toast"的接口，Raycast 的"关闭窗口后用 HUD 显示最后一行"也做不到）；`compact` 同步等待，上限 25 秒（宿主的动作超时是 30 秒），超时则结束进程树并提示"took too long"，成功时 Toast 标题是最后一个非空输出行，退出码非 0 时标题是最后一行、内容是退出码；`fullOutput` 推入 `output` 视图（载荷 `{run}`）。
- **输出视图**：一个列表视图，只有一条带 Detail 的条目：标题是状态（运行中/退出码/已停止），附件文字是耗时，Detail 是输出尾部 32 KiB 的代码块，元数据是状态、耗时、脚本路径。动作：运行中时主动作是 Stop（Ctrl+X），然后是 Copy Output、Show in File Explorer。之所以不用 `ViewDetail`，是因为它没有挂动作的位置。运行期间每 100ms 最多一次 `Invalidate("output")`，结束时再发一次。每个插件实例最多保留 10 次已结束的运行，再早的视图显示"已不可用"。
- **命令**：`new`（Create Script Command）在第一个目录里写 `script-command[-N].ps1` 模板（带 UTF-8 BOM，Windows PowerShell 5.1 才能正确读取非 ASCII），然后打开它；`open-folder` 打开第一个目录。**偏差**：`plugin.Result` 不能携带一个由宿主执行的 `ActionDo`，所以这两个命令通过可注入的 `Options.Open`（默认 `internal/winapi/shell.Open`）自己打开文件；`builtin.Fake` 里换成空操作。以后如果 SDK 增加 `Result.Do`，改成返回 `ActionOpen` 即可。
- **图标**：`plugin.Icon` 只有 `Name` 和 `Path`。PNG/SVG 路径（相对脚本目录）用 `Path`；emoji、URL、JPEG 一律用内置的 `command` 图标。`iconDark` 忽略。
- **假环境**：`builtin.Fake(dir)` 在 `dir/scripts` 存在时把它作为脚本目录（`testdata/fixtures/scripts` 有 3 个示例），否则目录列表为空；所以假环境下 `directories` 的默认值是 `[]`，真实环境是 `["%APPDATA%\\golauncher\\scripts"]`。新增 golden 用例 `query_scripts`；`plugin_list`、`query_empty`、`session_confirm_done` 因多了插件和两个命令而更新。

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

## 11. 实现记录（W1-A：① ② ③ 与推送快照的宿主侧）

> 分支 `worktree-agent-a062767d87edd746e`，由子代理 A 完成，等待主控合并。需求台账 R18：①②③ 与 §5.4 宿主侧已实现并有测试（台账由主控更新）。

### ① Invalidate

- `core.New` 在初始化插件**之前**设置 `Services.OnInvalidate` / `OnRender`，插件在 `Init` 里启动的后台任务也能通知到。Engine 维护订阅它的 Controller 列表（`NewController` 自动订阅），逐个转发。
- Controller 收到通知时先判断是否和栈顶有关（栈顶是该插件 ID 匹配的视图，或栈顶是根搜索且插件实现了 `RootProvider`；根搜索不看 `viewID`），无关的直接丢弃，不起定时器。
- 合并：第一次通知启动 50ms 定时器，期间的通知只记录下来；到期时再判断一次是否仍然相关。如果这时有搜索正在进行，**不取消它**，而是置 `invAfter`，等所有进行中的搜索结束后再补一次刷新（否则持续通知的插件会让搜索永远完成不了）。
- 通知触发的刷新是"安静"的：不把 `loading` 置为 true，界面不会因为后台刷新闪出加载动画；这也让 `glctl` 快照不受后台刷新时机影响。
- 选中项保持：每个 frame 记录 `selKey`（选中条目的 Key）。上下键、`Select`、每次刷新或推送之后都会更新它；刷新结果到达时先按 `selKey` 找回，找不到才保留原下标。输入新文字（`SetText`）会清空 `selKey`，让最佳匹配重新排第一。普通的刷新（`Refresh`、动作返回 `Refresh`）同样保持。
- `Controller.Wait` 同时等待"进行中的搜索"和"待处理的通知/推送"两种计数。`Key`、`RunAction`、`SetField`、`SetArgument` 内部只等进行中的搜索（`waitSearches`），不会因为 50ms 的合并窗口变慢。
- 插件：clipboard 记录新条目后、配置变化重新清理后调用 `Invalidate("history")`（重复内容、被排除的内容不通知）；apps 在索引刷新后、生成了新图标后调用 `Invalidate("")`；everything 在后台启动 Everything 成功并等到它运行（最多 5 秒，插件关闭时提前结束）后调用 `Invalidate("search")`。

### §5.4 推送快照（宿主侧）

- 每次推入视图帧时分配实例 ID（`v1`、`v2`……，每个 Controller 递增），写进 `Nav.Request.Instance`，随每次 `View` 调用传给插件。`Outcome.Push` 本身不带实例 ID（保持 golden 不变）。
- `Host.Render(instance, view)` → Engine → 各 Controller：只接受栈里存在、插件 ID 和实例 ID 都匹配的帧；栈顶以外的帧也会更新内容（返回时看到最新内容）。
- **偏差**：SDK 的 `Render` 没有版本号，宿主按到达顺序处理，"更新的版本"就是"后到的推送"；外部插件协议里的 `version` 由 extproc（B）在转发前自行丢弃乱序的推送。
- 限流：每个实例两次应用之间至少 16ms，期间到达的推送只保留最后一份，由定时器应用；定时器触发时帧已关闭则丢弃。推送和拉取对同一帧是"后到者生效"。
- 视图关闭：帧被弹出（Escape、Backspace、PopToRoot、Reset、OpenCommand）时调用 `Engine.OnViewClosed(func(pluginID, instance string))` 注册的回调（后台 goroutine）。进程内插件的 SDK 没有对应回调，按计划跳过；extproc 用它转发 `viewClosed`。

### ② Form 视图与命令参数

- 表单状态 `formState` 挂在 frame 上：字段、值（统一用文本保存：checkbox 为 `"true"/"false"`，dropdown 为选项值，date 为 `YYYY-MM-DD`，file 为路径）、错误、焦点。视图被重新拉取或推送时，按字段 ID 保留已输入的值和错误、焦点。
- 键：`Tab`/`Shift+Tab` 循环切换；`Enter` 在单行字段跳到下一个、在最后一个字段提交，在 textarea 里不处理（由前端换行）；`Ctrl+Enter` 提交；`Space` 切换焦点所在的 checkbox（GUI 的复选框自己处理，主要给 `glctl` 用）；`Escape` 返回上一级；`Backspace` 在表单里**不**返回。表单没有搜索框，`SetText` 被忽略。
- 宿主校验：必填（checkbox 必须勾选）、number、date（`YYYY-MM-DD`）、dropdown 必须是选项之一；失败时显示错误、焦点移到第一个出错的字段，不调用插件。
- 提交值 `ActionDo.Values`：number 为 JSON 数字（空则 `null`），checkbox 为布尔值，其余为字符串。提交动作是 `Push` 时，值（转换成字符串）同时进入 `ViewRequest.Arguments`。插件返回 `FieldErrors`（新增 `Outcome.FieldErrors`）时表单保持打开，并忽略同一结果里的 Hide/Push/PopToRoot/Refresh；Toast 照常显示。
- API：`SetField(id, value)`、`FocusField(id)`；`Snapshot.Form{Fields[{Field…, value, error}], focused, submit}`（`submit` 是提交动作的标题，GUI 页脚按钮用）。
- 参数：`commandEntry` 把 `Command.Arguments` 本地化后放进条目的 `Item.Arguments`；任何 `Item` 都可以带参数。参数状态 `argState` 属于当前选中的条目（按 Key），选中别的条目后清空。`Tab`/`Shift+Tab` 在搜索框和各参数之间循环；参数获得焦点时 `Escape` 回到搜索框（不清空文字）。
- 执行主动作（Enter、面板里选主动作、`RunAction` 指定主动作）时，必填参数没填就把焦点移到第一个空参数、不执行；其他动作不检查必填。所有动作都带上参数值：`ActionDo.Values`（字符串），`Push` 动作同时转成 `ViewRequest.Arguments`。
- 全局热键打开带必填参数的命令（`OpenCommand`）：**偏差**——不直接执行，而是回到根搜索、把搜索文字设为命令标题、选中该命令并聚焦第一个必填参数（Raycast 的做法类似）。
- API：`SetArgument(name, value)`、`FocusArgument(name)`（`""` 为搜索框）；`Snapshot.Arguments{entry, fields[{Argument…, value}], focused}`。
- `glctl session field <id> <value>` 和 `arg <name> <value>`；值可以包含空格（其余参数用空格连接）。文本格式输出会列出表单字段和参数。golden：`session_form_invalid`、`session_form_submit`、`session_arg_missing`、`session_arg_run`（测试插件通过 `cli.fakeExtras` 注入假环境）。
- GUI（`internal/ui/form.go`）：表单替换结果列表、隐藏搜索框；text/password/number 为单行输入，textarea 为多行，checkbox、dropdown（Select），date 为文本（占位 `YYYY-MM-DD`），file 为文本（启动器的 Host 没有选择文件的钩子，**暂不提供"浏览"按钮**）。必填字段标题后加 `*`，`Info` 显示为说明，错误显示在字段下方。页脚按钮为提交动作标题 + `Ctrl+Enter`。参数输入框显示在搜索框右侧（dropdown 为 Select，第一项是占位文字）。焦点双向同步：Controller 的焦点变化时移动键盘焦点（并把光标放到末尾而不是全选），用户点击输入框时通知 Controller。
- 顺带修正：启动器在取快照**之前**判断是否有排队的输入，避免用过期快照覆盖刚输入的文字。

### ③ 设置项扩展

- `PluginInfo.Preferences` 的 `Group`、选项标题都已本地化；`Action`、`VisibleWhen`、`DynamicOptions` 原样带出（`glctl plugin info` 可见）。
- `Engine.PreferenceOptions(ctx, plugin, name)`：只对声明了 `DynamicOptions` 且插件实现 `PreferenceOptionsProvider` 的项有效；和视图调用一样受 panic 保护、10 秒超时；选项标题按 `%key%` 本地化。
- `Engine.RunPreferenceAction(ctx, plugin, name)`：只对 `PrefButton` 且有 `Action` 的项有效，走 `Engine.Run`（Callback/RunCommand 由插件处理，其他内置动作照常执行），返回 `Outcome`（主要用 Toast）。
- `core.PrefVisible(prefs, values, pref)`：`VisibleWhen` 的比较按 JSON 值（解码后深比较），未配置时用默认值。
- 设置页：未分组的项在前，各分组按首次出现的顺序带小标题；隐藏不满足条件的项（随配置变化即时更新）；按钮项显示说明、按钮，运行中显示加载动画，结束后在按钮旁显示 Toast（标题 + 消息）或错误；动态选项在打开插件页时后台加载，失败时在字段下方显示"无法加载选项"。后台调用结束后通过 `mui.Services.Invalidate` 重绘；`Settings.Wait()` 供测试使用。

### 改动范围外的文件（合并时注意）

- `internal/cli/cli.go`：`build` 里假环境的插件列表追加 `fakeExtras`（一行）；`writeStep` 文本输出；用法说明。
- `plugins/everything/everything.go`：`stop` 通道和 `waitRunning`（Everything 排序那一路也在改这个文件，可能需要手工合并）。
- `internal/i18n/locales/en.json`：只新增 key（`form.*`、`ui.form.*`、`settings.plugins.optionsFailed`、`settings.plugins.buttonFailed`）。

## 12. 实现记录：④ 外部插件（W1-B）

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

## 13. 集成记录（W2，2026-10-15）

四个分支各自在 worktree 里开发，按 C（Script Commands）→ R20（Everything 排序）→ A（交互能力）→ B（外部插件）的顺序合并到 main。

- **冲突**：只有 B 和前面几个分支之间有冲突，涉及 `.gitignore`、`internal/builtin/builtin.go`、`internal/core/plugins.go` 和两份文档，都是双方各自追加的内容，两边都保留。`plugin_list.json` 这个 golden 重新生成，差异只是新增了 `origin` 字段。
- **行为变化**：A 合入后，`Invalidate` 会真正触发重新拉取。外部插件 `go-hello` 的计数命令调用 `Invalidate` 后，根列表会刷新，所以 `run_external_command` 这个 golden 从"空快照"变成了包含最近使用和命令列表的快照。新的结果才是正确的，已更新。
- **B 留下的接口，已全部接好**：
  1. `Engine.OnViewClosed` 转发给实现了 `ViewClosed(instance)` 的插件，也就是外部插件代理。这样外部插件打开过的视图关闭后，进程空闲时可以被回收。
  2. 设置页的插件列表和详情页显示"External"标识；外部插件崩溃时显示原因，被熔断时显示原因并提供"Turn On Again"按钮（调用 `Engine.ResetPlugin`）。
  3. 宿主版本号通过 `builtin.HostVersion` 传给外部插件（GUI 用它自己的版本号，`glctl` 用 `cli.Version`）。
- **验证**：`gofmt`、`go vet`、`go test ./...` 和 `go test -race ./internal/... ./plugins/... ./pkg/...` 全部通过。
- **还没做的**：
  - `builtin.core/reload-plugins`（重新扫描插件目录），需要引擎支持在运行时增删插件，推迟到下一轮。
  - R20 的"先返回查询 A，再推送查询 B"：A 已经提供了推送能力，可以着手做了，列为后续任务。
  - §9 的待决问题 1（外部插件首次运行是否提示信任）仍然等你决定。

## 14. 后续 SDK 改进项（来自插件开发中的反馈）

| 来源 | 缺口 | 建议 |
|------|------|------|
| Scoop（R22） | 长时间操作在动作返回后才完成，插件没法在完成时弹出提示 | 新增 `Host.Toast(t Toast)`：插件在后台任务里也能调用；宿主显示在启动器底栏，窗口隐藏时改用系统通知 |
| Scoop、Script Commands | `Result` 不能携带让宿主执行的动作（比如"打开文件"），插件只好自己调用 `shell.Open` | `Result.Do *ActionDo`：处理完回调后，宿主再执行这个动作 |
| Scoop | 宿主的确认对话框只能显示固定文案，没法带上"可以释放 8.3 GB"这类动态信息 | `Action.Confirm *ConfirmText`：由插件提供确认对话框的标题和说明 |
| 世界时钟（R23） | "最近使用"保存的是条目快照，时钟条目在那里会一直显示被使用那一刻的时间（动作本身是正确的） | 允许插件声明条目不进入最近使用（`Item.NoRecent`），或者显示最近使用时由插件刷新条目内容 |

| 来源 | 缺口 | 建议 | 状态 |
|------|------|------|------|
| Scoop（R22） | 长时间操作在动作返回后才完成，插件没法在完成时弹出提示 | 新增 `Host.Toast(t Toast)`：插件在后台任务里也能调用；宿主显示在启动器底栏，窗口隐藏时改用系统通知 | ✅ 已解决（R24，[background-tasks.md](background-tasks.md)）：`Host.Toast` 按建议实现；长操作改用宿主后台任务 `Host.RunProcess`/`RunTask`，结束时宿主按 `TaskSpec.Notify` 统一通知，插件用 `TaskSpec.OnDone` 得到结果 |
| Scoop、Script Commands | `Result` 不能携带让宿主执行的动作（比如"打开文件"），插件只好自己调用 `shell.Open` | `Result.Do *ActionDo`：处理完回调后，宿主再执行这个动作 | ✅ 已解决（R24）：`Result.Do` 只允许宿主自己的动作类型（不能是 `Callback`/`RunCommand`，避免循环），结果合并：`Do` 的 `Push`/`Hide` 生效，`Toast` 以插件的为准。Script Commands 的 `fullOutput` 用它打开刚启动的任务（`ActionShowTask`）|
| Scoop | 宿主的确认对话框只能显示固定文案，没法带上"可以释放 8.3 GB"这类动态信息 | `Action.Confirm *ConfirmText`：由插件提供确认对话框的标题和说明 | 未做 |

> **R24 对 §6 的影响**：Script Commands 的 `fullOutput` 模式不再有自己的 `output` 视图（§6.1 中"输出视图"一条和 `run.go`/`output.go`/`proc_windows.go` 的描述已过时）。现在它用 `Host.RunProcess` 作为宿主后台任务运行，`Result.Do` 打开这个任务（Background Tasks 实时显示输出，可停止、重新运行）；脚本条目多了"Show in Background Tasks"动作（`Ctrl+T`，打开最近一次运行，还没运行过时打开任务列表）。`compact`、`silent` 不变，只是进程代码换成了共享的 `internal/winapi/proc`（批处理的 cmd.exe 引号规则也移到了那里）。

## 插件之间怎么交互（R57）

**插件之间不能直接调用。** SDK 里没有"拿到另一个插件"或"调用另一个插件的函数"的接口，全部经过宿主：

| 方式 | 谁发起 | 说明 |
|------|--------|------|
| 根搜索汇总 | 宿主 | 宿主把输入同时发给所有插件（`RootProvider`），合并、打分、排序后显示。插件互相不知道对方存在 |
| 兜底条目、Tab | 宿主 | `FallbackProvider`、`TabProvider` 由宿主按顺序询问，例如 AI 的"Ask AI"和网页搜索 |
| 共享的宿主服务 | 插件 → 宿主 | 剪贴板、后台任务、通知、存储（每个插件一份，互相看不到）。间接联动：一个插件写剪贴板，剪贴板历史插件通过宿主事件 `clipboard-changed` 记下来 |
| 宿主事件 | 宿主 → 插件 | `Events().Subscribe` 只能订阅宿主发的事件（窗口显示/隐藏、剪贴板变化等），插件不能发布自定义事件 |
| 动作 | 插件 → 宿主 | `ActionDo` 只能让宿主做通用的事（打开、复制、粘贴、推入视图、运行进程……）。`RunCommand` 只能运行**本插件**的命令 |
| AI 工具层 | 宿主 | `internal/agent` 替模型搜索并执行任意插件的动作，这是唯一"跨插件"的调用，但由宿主执行，并按风险等级确认 |
| 代码复用 | 编译期 | 内置插件可以共用 `internal/...` 包，例如网络工具和进程管理共用 `internal/winapi/process`。这是共享代码，不是运行时调用 |

**为什么这样设计**：

- **隔离**：插件崩溃或卡住不影响别人（`guard`、超时）；外部插件在独立进程里。
- **权限和审计**：所有副作用都经过宿主，确认框、活动记录、AI 风险等级才有地方挂。
- **版本**：插件之间没有接口依赖，各自升级不会互相破坏。

**代价**：插件拿不到别的插件的设置或能力。例如进程管理想"用默认搜索引擎搜索进程名"，却读不到快速链接插件里的默认引擎（R55 最后定为直接用 Google）。

**如果以后需要**，可以按需加（都经过宿主，不破坏隔离）：

1. **宿主级的通用动作**：例如新增 `ActionWebSearch`，由宿主交给负责网页搜索的插件处理。适合"能力"类的需求。
2. **跨插件运行命令**：`RunCommand` 的目标允许写 `插件ID/命令`，带参数。被调用方在 manifest 里声明哪些命令允许被别人调用。Raycast 的 `launchCommand` 也是这个思路。
3. **插件发布的事件**：插件可以发布带命名空间的事件（如 `builtin.timer/finished`），别的插件订阅。
## 能不能把插件做成 DLL 由启动器加载？（R81）

**结论**：不作为第三方插件的方式，保持独立进程。

- **Go 的限制**：
  - `-buildmode=plugin` 不支持 Windows。
  - `-buildmode=c-shared` 的 DLL 自带一套 Go 运行时，加载到 golauncher 后一个进程里有两套运行时，官方不保证可靠。
  - 接口只能走 C 函数，仍然要序列化，和 JSON-RPC 差不多。
- **DLL 方式的好处**：每次调用省零点几毫秒，不多开进程。
- **DLL 方式的坏处**：
  - 插件崩溃会带走整个启动器，违背 R12；
  - 插件卡死无法安全地强行终止；
  - 插件能读写启动器的内存，权限检查形同虚设；
  - Go 的 DLL 不能安全卸载，升级要重启启动器；
  - 二进制接口变化后，旧插件会崩溃；
  - 其他语言很难写；
  - 未签名的 DLL 更容易被杀毒软件拦截。
- **性能不是瓶颈**：一次进程间调用不到 1ms，而根搜索给每个插件的时间是 150ms。
- **如果需要进程内的轻量插件**：用 WebAssembly，例如纯 Go 的 wazero 运行时。它在沙箱里运行，可以限时、限内存，多种语言都能编译成 WASM，不需要 cgo，也没有第二套 Go 运行时。代价是不能直接调用 Windows API，适合纯逻辑插件。作为以后的补充类型，现在不做。
## 其他启动器的插件机制对比（R82）

（凭对各项目的了解整理，细节以各项目的最新文档为准。）

| 启动器 | 插件写法 | 运行方式 | 通信 | 分发 |
|---|---|---|---|---|
| Raycast | TypeScript + React（`@raycast/api`） | 独立 Node 进程，React 树转成原生界面 | 专用协议（插件描述界面，宿主绘制） | 官方商店 + 代码审核 |
| Alfred | Workflow（图形化 + 任意语言脚本） | 每次调用起新进程（Script Filter） | 脚本输出 JSON/XML | `.alfredworkflow` 文件 |
| PowerToys Run（旧） | C# DLL（`IPlugin`） | 进程内 | .NET 接口 | 手动放目录 |
| PowerToys 命令面板（新） | WinRT/COM（主要是 C#） | 进程外 COM 服务 | WinRT 接口 | MSIX（商店、winget） |
| Flow Launcher | C# DLL，或 Python/JS 等 | C# 进程内，其他语言独立进程 | 直接调用 / JSON-RPC | GitHub 清单仓库 |
| Wox / Wox 2 | C#、Python；Wox 2 为 Go 宿主 + Python/Node/脚本 | 多为独立进程 | JSON-RPC / WebSocket | 插件商店 |
| Ulauncher | Python | 每个扩展一个进程 | WebSocket | 扩展网站 + GitHub |
| Albert | C++ 共享库 / Python | 进程内（嵌入 Python） | 直接调用 | 随程序发行 |
| Spotlight | 不开放插件（App Intents / 快捷指令） | 系统管理 | 系统框架 | App Store |

**趋势**：

1. 新设计倾向进程隔离（命令面板、Raycast）。
2. 插件描述界面、宿主统一绘制。
3. 多语言插件走 stdio + JSON。
4. 生态靠分发（商店、审核、工具），而不是插件机制本身。

**golauncher 的现状**：

- **已经具备**：进程隔离加自动重启（同 Raycast、命令面板）；描述式界面加推送更新；JSON-RPC 多语言（同 Flow、Wox）；兼容 Raycast Script Commands；插件自动成为 AI 工具（同类启动器基本没有）。
- **还欠缺**：插件商店和一键安装、签名和审核、开发脚手架、自定义界面能力。

**建议补的顺序**：

1. 从 GitHub Release 或 zip 一键安装，首次运行时提示确认；
2. 用 GitHub 仓库做插件清单，在启动器里搜索和安装；
3. `glctl plugin new` 生成模板；
4. （可选）签名校验。
## 编译时打包插件：独立插件仓库 + 名单（R83，方案）

**思路**：参照 CoreDNS 的 `plugin.cfg` 和 Caddy 的 `xcaddy`，插件放在单独的仓库里，主仓库用一份名单决定把哪些插件编译进 exe。

```
golauncher
├─ plugins.cfg                     名单：模块路径@版本
├─ go generate → internal/builtin/zz_bundled.go
│     import 各插件模块，bundled() 返回它们的实例，和 builtin.All 合并
└─ go build → 单个 exe
```

- **版本**：名单里的版本写进 `go.mod`/`go.sum`，构建可复现；本地联调用 `go.work`。
- **好处**：
  - 编进去的插件跑在主进程，能用完整的 SDK（兜底、Tab、选中文字等外部协议还没有的能力）；
  - 发行包仍是单个 exe；
  - 核心仓库更小，平台插件（Scoop、WinGet、Homebrew）可以独立维护和发版；
  - 可以按名单出不同的发行版（Windows、mac、精简版）；
  - 插件在构建时经过审查。
- **代价**：
  - 崩溃隔离变弱：插件自己开的 goroutine panic、并发写 map 等致命错误会带走整个进程，所以只适合可信插件，插件仓库也要执行同样的 lint；
  - SDK（`pkg/plugin`）要拆成独立模块，避免主仓库和插件仓库互相依赖，之后按语义化版本管理；
  - 插件不能导入 `internal/...`，要搬出去的插件依赖的 Windows 包需要整理成公开包，这是主要工作量；
  - 用户不能自己加插件。第三方插件仍然走独立进程，两种方式并存。
- **建议的步骤**：
  1. 拆出 SDK 模块；
  2. 加 `plugins.cfg` 和生成器；
  3. 先拿依赖少的插件（世界时钟、计时器）试点；
  4. 再整理公开的 Windows 包，搬平台插件。