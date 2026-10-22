# 环境感知（Context Awareness）设计方案

> 状态：设计 v1 · 2026-10-15 · 对应需求 R25（见 [requirements.md](requirements.md)）
>
> 需求：让启动器能感知环境。比如焦点在文件管理器上时，打开 golauncher 后，弹出的菜单根据文件管理器当前所在的目录做调整：在当前位置打开 PowerShell、新建文件夹等。这个能力需要可扩展。

## 1. 总体思路

```
按下 Alt+Space
  └─ ① 记下当前前台窗口（在启动器抢走焦点之前）
       └─ ② 识别器（Detector）分析这个窗口 → Context：是哪个应用、当前目录、选中了哪些文件…
            └─ ③ 宿主把 Context 交给插件 → 插件返回针对这个环境的条目
                 └─ ④ 根列表最上方出现"在 <目录名> 中"分组；输入文字时，这些条目也参与搜索
```

可扩展体现在两处：

- **识别器**：负责"认出环境"。宿主内置资源管理器、桌面，以及一个通用的"标题里含路径"识别器；以后可以加 OneCommander、Total Commander、VS Code、浏览器等，各写一个识别器即可。
- **上下文插件**：负责"针对环境提供操作"。任何插件（包括外部插件）都可以实现 `ContextProvider`。

## 2. 数据模型（SDK）

```go
// Context describes what the user was doing when the launcher opened.
type Context struct {
    App      AppInfo   `json:"app"`                // 前台应用
    Kind     string    `json:"kind,omitempty"`     // "explorer" | "desktop" | "file-dialog" | …（由识别器给出）
    Folder   string    `json:"folder,omitempty"`   // 当前目录（文件管理器类）
    Selected []string  `json:"selected,omitempty"` // 选中的文件或文件夹（完整路径）
    Title    string    `json:"title,omitempty"`    // 窗口标题
    Extra    map[string]string `json:"extra,omitempty"` // 识别器附带的其他信息，比如浏览器的 URL（以后用）
}

type AppInfo struct {
    Name    string `json:"name"`              // 可执行文件名，例如 "explorer.exe"
    Path    string `json:"path,omitempty"`
    PID     int    `json:"pid,omitzero"`
    Package string `json:"package,omitempty"` // UWP 应用的 AUMID（例如 Files 应用）
}

// ContextProvider contributes items for the current context. They are shown
// in a section at the top of the empty root search and are searchable.
type ContextProvider interface {
    ContextItems(ctx context.Context, c Context) ([]Item, error)
}

// Query.Context carries the context to RootItems too (nil when unknown).
type Query struct {
    Text    string   `json:"text"`
    Context *Context `json:"context,omitempty"`
}
```

- 没有识别出任何环境时，`Context` 只包含 `App` 和 `Title` 两项。
- 外部插件：`rootItems` 的参数带上 `context`，协议新增 `contextItems` 方法，写进 `plugin-protocol.md`。

## 3. 获取环境（宿主）

### 3.1 时机

- 在热键回调里，也就是启动器抢走焦点**之前**，记下前台窗口的句柄（HWND）。shell 现在已经为"粘贴到上一个窗口"做了这件事，复用即可。
- 识别在后台进行，限时 300ms。启动器先按原样显示；识别完成后，宿主调用 `Invalidate` 刷新根列表，"在 <目录名> 中"分组随即出现，不拖慢窗口弹出的速度。
- 只在呼出时获取一次，不在后台持续监视其他窗口。

### 3.2 识别器（`internal/winapi/context`，可扩展的注册表）

```go
type Detector interface {
    Name() string
    // Detect fills in c for window hwnd, or reports false if it does not apply.
    Detect(ctx context.Context, hwnd uintptr, c *plugin.Context) bool
}
```

按顺序尝试，第一个命中的生效：

| 识别器 | 判断方式 | 得到的信息 |
|--------|----------|------------|
| 资源管理器 | 窗口类 `CabinetWClass`。通过 COM `IShellWindows` 找到对应的窗口（Windows 11 有多个标签页时，取当前激活的那个），再用 `IShellBrowser` / `IFolderView` 拿到当前文件夹和选中项 | `Kind=explorer`、`Folder`、`Selected`。"此电脑"、"回收站"这类虚拟文件夹没有路径，只给出 `Title` |
| 桌面 | 窗口类 `Progman` / `WorkerW` | `Kind=desktop`、`Folder=桌面路径`，选中的桌面图标放进 `Selected` |
| 文件对话框 | 窗口类 `#32770`，里面有地址栏 | `Kind=file-dialog`、`Folder`（第二阶段再做） |
| 通用：标题里含路径 | 窗口标题里能解析出一个存在的绝对路径（OneCommander、Total Commander、终端、很多编辑器都会把路径放在标题里） | `Kind=path-in-title`、`Folder`（如果是文件，就取它所在的目录） |
| 兜底 | 任何窗口 | 只有 `App` 和 `Title` |

以后要支持某个文件管理器，比如 Directory Opus 或 Files 应用，只要实现一个 Detector 并注册即可。另外也可以把识别器暴露给外部插件，协议里加一个 `detectContext` 方法。这一项列为第二阶段。

### 3.3 隐私

- 获取到的环境只在本机内存中使用，不落盘。"最近使用"里只保存条目本身，不保存当时的环境。
- 偏好项 `contextAware`（默认开启）可以整体关闭这个功能；关闭后不读取其他窗口的任何信息。

## 4. 内置上下文插件 `builtin.files`（`plugins/files`）

`Kind` 为 explorer、desktop、path-in-title、file-dialog 中的任意一种，并且有 `Folder` 时生效。

### 4.1 针对当前目录

| 条目 | 实现 |
|------|------|
| 在此处打开 PowerShell | `ActionOpen`：优先 `wt.exe -d <dir>`，没有 Windows Terminal 时用 `pwsh` 或 `powershell`，工作目录为 `<dir>` |
| 在此处打开命令提示符 | `cmd.exe`，工作目录为 `<dir>` |
| 在此处打开 VS Code | `code <dir>`，只在找得到 `code` 时显示 |
| 新建文件夹 | 带一个参数（文件夹名，默认"New folder"），回调里创建；重名时自动加序号 |
| 新建文本文件 | 带参数（文件名，默认 `New Text Document.txt`） |
| 复制当前路径 | `CopyText` |
| 在 Everything 中搜索此目录 | 打开文件搜索视图，搜索范围限定在这个目录（需要 Everything 插件可用） |
| 在 Windows Terminal 中以管理员身份打开 | `runas`，需要确认 |

### 4.2 针对选中的文件或文件夹（`Selected` 非空时）

| 条目 | 实现 |
|------|------|
| 复制路径 | 多个路径时每行一个 |
| 用 VS Code 打开 | `code <paths>` |
| 压缩为 ZIP | 用 Go 的 `archive/zip`，生成在同一目录；如果后台任务（R24）已经合入，就改为后台任务 |
| 计算 SHA-256 | 只对单个文件显示；结果复制到剪贴板 |
| 以管理员身份运行 | 只对 `.exe` / `.bat` 显示 |

### 4.3 呈现方式

- **根列表**：输入为空时，最上方是"在 <目录名> 中"分组，然后才是"最近使用"和"命令"。输入文字时，这些条目和其他结果一起参与模糊匹配，并且有一点加分，在环境相关的场景下更容易排在前面。
- **底栏**：显示一个小标签，比如"📁 Downloads"，表示当前识别到的环境；点击可以复制路径。
- **动作执行完后**：资源管理器会自动刷新，不需要我们处理。

## 5. glctl 与测试

- **glctl**：`--context-folder DIR`、`--context-select FILE`（可重复）、`--context-app NAME`，用来注入一个假的环境，例如：

  ```
  glctl query "" --context-folder C:\tmp
  glctl session new -s s.json --context-folder C:\tmp
  ```

- **识别器测试**：
  - 标题路径解析：带引号、带"- 应用名"后缀、中文路径、不存在的路径都要覆盖；
  - 窗口类分派；
  - 资源管理器的 COM 识别器做一个真实测试，并且要求**显式开启**：测试自己打开一个指向临时目录的资源管理器窗口，识别完再关掉，不碰你已经打开的窗口。
- **插件测试**（hostfake）：条目的生成规则；新建文件夹的重名处理；压缩和哈希在临时目录里验证；命令行拼接是否正确。
- **核心测试**：环境异步到达后根列表刷新；关闭功能后完全不获取环境；"最近使用"里不保存环境。

## 6. 分阶段

| 阶段 | 内容 |
|------|------|
| 第一阶段（本次） | SDK 和协议扩展；资源管理器、桌面、标题路径三个识别器；`builtin.files` 的 §4.1 和 §4.2；根列表分组和底栏标签；`glctl` 注入 |
| 第二阶段 | 文件对话框识别器；按应用的识别器（OneCommander、Total Commander、Files 应用）；外部插件提供识别器；浏览器 URL；选中文字（复制到剪贴板再还原，需要单独评估） |

## 7. 实现记录（第一阶段，2026-10-15）

### 7.1 做了什么

- **SDK / 协议**：`pkg/plugin/context.go` 定义 `Context`、`AppInfo`、`ContextProvider` 和 Kind 常量（`explorer`、`desktop`、`path-in-title`、`file-dialog`）；`Query.Context`。协议：`rootItems` 参数带 `context`，新方法 `contextItems`，新能力位 `capabilities.contextItems`；`extproc.Proxy` 与 `rpcserve` 都已实现，文档见 `plugin-protocol.md`。
- **宿主**：`core/context.go`。`Engine.DetectContext(detector)` 每次呼出调用一次：先清空上一次的环境（代数 +1），再在 `host.Go` 里以 300ms 预算运行识别器；结果到达时如果根列表已经拉取过，就对根做一次 invalidate（`Controller.contextChanged`），否则下一次根搜索直接带上。过期代数的结果直接丢弃。`contextAware` 关闭时识别器根本不会被调用；在设置里关掉时当前环境也立即清空。`Engine.SetContext` 给 glctl 注入用。
- **shell**：`showLauncher` 里原来记录 `prevForeground` 的地方，紧接着调用 `DetectContext(wincontext.Detect(fg))`，在 `launcher.Shown`（Reset）之前，保证顺序。
- **识别器**：`internal/winapi/context`（包名 `wincontext`），注册表 `Register` / `RegisterFirst`，默认顺序：资源管理器 → 桌面 → 标题路径；都不命中时只填 `App`、`Title`（`Kind` 为空）。自己进程的窗口不识别。
- **根列表**：空输入时第一个分组是“In <目录名>”（`section.context`）；有输入时上下文条目参与模糊匹配，排名 = 0.7×匹配分 + 0.1（固定加分，取代 frecency）。条目带 `Entry.Context=true`，执行时**不写入“最近使用”**（否则动作里的路径会落盘）。
- **底栏**：应用名后面一个小标签（文件夹图标 + 目录名），只在识别出 Kind 时出现；点击通过 `Controller.CopyContextPath` 复制路径（走 `System.CopyText`，glctl 可观察）。标签后面是 `Grow(1)` 的占位，R24 的任务指示器放在左侧不冲突。
- **设置**：General 页新增“Context awareness”开关（`config.Config.ContextAware`，默认 true，旧配置文件缺这个字段时也是 true）。
- **`builtin.files`**：§4.1、§4.2 全部条目；选中项相关的条目排在目录条目前面。
- **glctl**：`--context-folder`、`--context-select`（可重复）、`--context-app`，所有环境命令和 `session new` 都支持（写进 session 文件，回放时注入）。注入时不做任何识别。

### 7.2 与设计的出入

1. **识别器接口**：`Detect(ctx, w Window, c *plugin.Context) bool`，`Window` 里带 `HWND`、窗口类、标题和“WorkerW 是否承载桌面”，免得每个识别器重复取窗口信息，也方便在任何平台上测试分派逻辑。包名用 `wincontext`，避免和标准库 `context` 冲突。
2. **“在 Everything 中搜索此目录”由 `builtin.everything` 提供**，而不是 `builtin.files`：插件之间不能 Push 对方的视图。Everything 插件自己实现 `ContextProvider`，条目 Push 自己的 `search` 视图，Payload 为 `{"scope": dir}`；视图把输入改写成 `"<dir>\" <输入>`（Everything 对含反斜杠的词匹配完整路径），标题变为“Search in <目录名>”。Everything 不可用时插件本来就不加载，条目自然不出现。
3. **压缩 / 哈希**：在 `HandleAction` 里通过 `Host.Go` 运行并等待（受 30s 动作超时约束），完成后用 Result 的 toast 报告；超时则提示“仍在后台进行”。代码里留了 `TODO(R24)`，后台任务合入后改成任务。
4. **标题路径**：只接受真实存在的路径（不退回到最长存在的前缀，那样误报太多）；路径等于该进程自己的 exe 时忽略（`cmd.exe` 的标题是 `C:\Windows\system32\cmd.exe`，没有意义）；如果是文件，`Folder` 取其目录，文件本身放在 `Extra["file"]`，不放进 `Selected`。最多做 48 次 `stat`。
5. **以管理员身份运行**：除 `.exe`、`.bat` 外也接受 `.cmd`、`.msi`。管理员终端在没有 Windows Terminal 时用 PowerShell `-NoExit -Command Set-Location -LiteralPath '<dir>'`，因为提权后的进程不继承工作目录。
6. **新建文本文件**：输入的名字没有扩展名时自动加 `.txt`；重名按资源管理器的规则编号（`New folder (2)`、`todo (2).txt`）；非法名字（`<>:"/\|?*`、结尾的点或空格、`CON` 等设备名）给出失败 toast。
7. **外部插件**：初始化之前不知道它有没有 `contextItems` 能力，所以带环境的第一次呼出会启动一次外部插件；之后没有该能力的插件不再被调用（与 `rootItems` 的策略一致）。
8. **底栏标签**只在识别出 Kind 时显示（只有 App/Title 的兜底环境不显示）。

### 7.3 Windows 11 标签页

`CabinetWClass` 窗口里每个标签页是一个 `ShellTabWindowClass` 子窗口，`IShellWindows` 里每个标签页各是一项。做法：

1. 用 `FindWindowEx` 依次找 `ShellTabWindowClass` 子窗口，取**第一个可见的**——非活动标签页是隐藏的。
2. 枚举 `IShellWindows`（用 `_NewEnum` + `IEnumVARIANT`，避免按值传 VARIANT 的 ABI 问题），对每一项 `IServiceProvider::QueryService(SID_STopLevelBrowser, IID_IShellBrowser)`，再 `IShellBrowser::GetWindow` 得到它的标签页窗口；等于活动标签页的那一项就是要找的。
3. 没有标签页（Windows 10）或没匹配上时，退回到 `GetAncestor(GA_ROOT)` 等于该窗口的第一项。
4. 之后 `QueryActiveShellView` → `IFolderView`：`GetFolder(IID_IPersistFolder2)` + `GetCurFolder` + `SHGetPathFromIDListEx` 得到路径（虚拟文件夹失败 → `Folder` 为空，仍是 `explorer`）；`Items(SVGIO_SELECTION, IID_IShellItemArray)` + `GetDisplayName(SIGDN_FILESYSPATH)` 得到选中项（最多 500 个）。

桌面：`IShellWindows::FindWindowSW(CSIDL_DESKTOP, SWC_DESKTOP, SWFO_NEEDDISPATCH)` 拿到桌面的 dispatch，后面同上取选中的图标；目录用 `FOLDERID_Desktop`。

COM 在锁定的 OS 线程上以 STA 初始化，用完即释放，和 `internal/winapi/shell` 一样是纯 Go vtable 调用。

验证：显式开启的测试 `TestExplorerActiveTabReal` 在自己打开的窗口里发 `WM_COMMAND 0xA21B`（Ctrl+T 的命令）新开一个标签页（“主文件夹”，虚拟），断言识别结果不是原来那个标签页的目录——通过。注意：`IWebBrowser2::Quit` 关不掉“曾经有多个标签页的窗口”的最后一个标签页，所以这个测试最后对**自己打开的**窗口发 `WM_CLOSE`；如果文件夹被系统开成了用户已有窗口里的一个标签页，测试直接跳过，绝不碰用户的窗口。

### 7.4 测试与测量

- 单元测试：标题路径解析（引号、`- 应用名` 后缀、`*` 未保存标记、中文路径、UNC、`~`、`/` 分隔、`[1]` 这类文件名、不存在的路径、`stat` 次数上限）、窗口类分派、`RegisterFirst`；`builtin.files` 的条目生成规则、终端回退、命令行转义（`quoteArg`）、新建文件夹/文件的编号与非法名、ZIP（内容与失败时不留残档）、SHA-256（写入 hostfake 剪贴板）；Everything 的范围搜索；核心：环境异步到达后刷新根、先于根到达、超时丢弃、新一次呼出丢弃旧结果、关闭后识别器不被调用、上下文条目不进“最近使用”、带环境的搜索与 `Query.Context`、复制路径；协议：rpcserve 的 `contextItems` 和带 context 的 `rootItems`，extproc 对无此能力的插件不发请求。
- glctl golden：`query_context`（空根 + 注入的目录和选中文件）、`session_context_new_folder`（带参数新建文件夹，真实创建在临时目录里，输出中的临时路径替换为 `<DIR>`）。
- 真实资源管理器（`GOLAUNCHER_CONTEXT_TESTS=1 go test ./internal/winapi/context`）：自己用 `explorer.exe <临时目录>` 打开窗口、用 `IFolderView::SelectItem` 选中文件、识别、再只关闭自己的窗口/标签页。本机（Windows 11）测得一次完整识别（窗口信息 + 进程信息 + COM 枚举 + 目录 + 选中项）**中位数 27–39ms，最快 14–26ms，最慢约 150ms**（每轮第一次，含 COM 初始化），远低于 300ms 预算。

### 7.5 合并注意

- `internal/cli/testdata/golden/plugin_list.json` 多了 `builtin.files`；和 R24 合并后需要 `go test ./internal/cli -update` 重新生成。
- `internal/ui/icons.go` 新增 `terminal`、`code`、`folder-plus`、`file-plus`、`archive`、`hash`、`shield` 图标；`internal/ui/launcher.go` 底栏改为“标签不 Grow + 上下文标签 + Grow 占位”。
- `plugins/files` 的压缩/哈希在 R24 合入后应改用后台任务（见 `TODO(R24)`）。

## 9. 问题记录：环境分组晚到导致列表错位（R26，2026-10-15）

**现象**：在资源管理器里选中文件后打开启动器，"In Photos"这个分组标题和第一条"Open Terminal Here"画在了同一个位置，选中项停在下面"最近使用"里的 Calculator 上，列表也被滚动了。

**原因**（两个问题叠加）：

1. 环境识别是异步的，大约 30ms 后才到达。启动器先显示原来的列表，第一条被选中；环境到达后，根列表在顶部插入一组条目。刷新时，宿主按"保持选中项"的规则（M5 §11），继续选中原来那一条，并把它滚动到可见位置，于是列表向下滚动了。
2. MyGo 列表的分组标题是吸顶的：列表滚动时，当前分组的标题固定在顶部，需要标题行有不透明的背景。我们的标题行为了透出 Acrylic 没有背景，所以和下面滚上来的第一条叠在了一起。另外，行的标识里用的是分组序号，顶部插入新分组后，原有的行全都换了标识。

**修复**：

- **核心**：每一级视图记录"用户是否主动移动过选中项"（方向键、鼠标悬停或点击）；输入文字时重置这个标记。环境到达时，如果用户还没动过选中项，就选中第一条，也就是环境分组的第一个条目；动过的话保持不变。
- **界面**：分组标题不再吸顶（`ListState.Header = nil`），和普通行一起滚动；行的标识改为"分组标题 + 条目 Key"，插入分组后，原有的行依然保持同一个标识。
- **回归测试**：
  - `TestContextArrivalSelectsFirstRow`（核心）：修复前选中的是第 3 行"Background Tasks"；
  - `TestLateContextDoesNotOverlap`（界面）：修复前标题和第一条的纵坐标完全相同（Y=52.9），选中的是第 8 行。

  两个测试都确认了修复前失败、修复后通过。

**同时发现的问题**：全量测试时，连接真实 Everything 的测试因为本机的 Everything 正忙（CPU 占用很高，可能在重建索引）而超时。处理：

- 这类测试改为显式开启（`GOLAUNCHER_EVERYTHING_TESTS=1`），和剪贴板、资源管理器测试的约定一致，默认测试不再依赖本机软件的状态；
- Everything 无响应时，`SendMessageTimeout` 失败但不带错误码，以前会报出"sending the query: The operation completed successfully"这样自相矛盾的信息，现在返回明确的 `ErrNotResponding`："Everything 没有响应（正忙或挂起）"。

## 10. 问题记录：环境分组仍需向上滚动才能看到（R27，2026-10-15）

**现象**：R26 修复后，在文件夹里打开启动器，环境分组仍然在视口上方，选中项停在"最近使用"里的 Search Files 上。

**原因**（R26 的修复漏掉的三点）：

1. **鼠标不动也会"悬停"**：启动器弹出时，窗口正好出现在鼠标所在的位置。环境分组插到顶部后，下面的行整体下移，鼠标"底下"换成了另一行。界面代码把"鼠标下的行变了"当成悬停，于是选中了那一行。
2. **悬停算作了用户主动选择**：R26 加的"用户是否移动过选中项"标记，被悬停也置上了，于是环境到达时没有回到第一条。
3. **滚动只看序号**：列表只在选中的序号变化时才滚动。而环境到达前后，选中序号都可能是 0（先是旧列表的第一条，后是环境分组的第一条），序号没变，列表就不会滚回顶部。另外，`ScrollIntoView` 只保证选中行可见，它上面的分组标题仍可能留在视口外。

**修复**：

- **界面**：每帧比较所有行的 Key，内容一变化，就让悬停重新等待鼠标真正移动之后才生效（和启动器刚弹出时的处理一样）。是否滚动改为按选中条目的 Key 判断，内容变化时也会重新定位。选中第一条时，用 `ScrollTo(0, Start)` 滚到最顶部，分组标题一起露出来。
- **核心**：新增 `Controller.Hover`。悬停选中不置"用户移动过"的标记，只有方向键和点击才算主动选择。

**回归测试**：

- `TestLateContextWithPointerOnList` 模拟鼠标停在列表上并悬停到第三行，然后环境才到达；
- `TestLateContextDoesNotOverlap` 把测试插件的命令扩到 12 个，让列表比窗口高。

修复前，两者都复现了问题：选中第 12 行"Command 02"，分组标题和第一条都不在视口里；修复后都通过。

**教训**：R26 的回归测试列表太短，鼠标落点下面也没有行，覆盖不到真实场景。界面类回归测试要尽量还原真实条件：列表比窗口高、鼠标停在窗口里。

## 11. 选中的文字（R64、R65）

第二阶段里的“选中文字”已经单独设计并实现，见 [selected-text.md](selected-text.md)。它跟随本文的“情境感知”开关（关掉时不读取选中的文字），和目录环境共用“每次呼出一代”的状态（`core.contextState`）以及 §9、§10 的晚到规则；但选中的文字**不放进 `plugin.Context`**，只通过新的 `SelectionProvider` 交给声明了 `PermSelection` 的内置插件。

## 12. 动作环：指针下的项目（R120，2026-10-22）

**问题**：情境识别读的是资源管理器 / 访达里"已选中的项目"。动作环在鼠标所在处弹出，指着一个没选中的文件按住中键时，情境操作不针对它，必须先左键选中。

**规则**（只用于动作环，和 Windows 右键菜单一致；用快捷键呼出启动器时完全不变）：

| 弹出时指针在 | 报告的项目（`Context.Selected`） |
|---|---|
| 没选中的文件或文件夹 | 只有它（文件夹仍是当前文件夹） |
| 当前选中集合里的项目 | 整个选中集合（和以前一样） |
| 文件夹视图的空白处 | 没有项目：针对当前文件夹本身，即使有选中项 |
| 其他地方（导航窗格、功能区、地址栏、别的 App） | 和以前一样 |

只读，不改动真实的选中状态，不模拟点击和按键。桌面同理：指着桌面图标是那个项目、文件夹是桌面；空白处是桌面文件夹。

**一条代码路径**：`wincontext.Detect(ctx, target)` 变成 `DetectAt(ctx, target, ptr *Pointer)` 的薄包装（`ptr` 为 nil）。识别器拿到的 `Window` 多了 `Pointer` 字段；资源管理器和桌面的识别器仍是同一个 `browserView` 读文件夹和选中项，指针只决定报告哪些项目（`pointer.go` 的 `pointerSelection`，纯逻辑）。macOS 的 `DetectAt` 同样在原来的访达读取之后套用这张表。

**先读指针再弹圆环**：圆环窗口以指针为中心，弹出后 `ElementFromPoint` 只会碰到圆环自己。所以 `openRing` 在显示窗口之前调用 `wincontext.PointerAt(target, x, y)`，最多等 `PointerBudget`（100ms），只在资源管理器、桌面（按窗口类名，本地判断）或访达（前台 App 的 bundle id）上读，其他 App 不花时间；超时或读不到就是 nil，即以前的行为。结果交给 `ringContent`，和以前一样在 `ringContextBudget` 内调用 `DetectAt`。

**Windows**：
- 复用选中文字（R63）的 UI Automation 线程和客户端：`winselection.ListItemAt` 在那个线程上调用 `IUIAutomation::ElementFromPoint`，沿 Control View 向上走到列表（`listHitOf`，纯逻辑）：经过的第一个 ListItem/DataItem 是项目（UIA Name 和 SelectionItemPattern 的选中状态）；直接落在列表上（中间只有 Group/Pane）是空白处；表头、滚动条、导航树等都算"其他"。只认资源管理器的 `UIItemsView` 和桌面的 `SysListView32`。不碰本进程的元素。
- UIA 给的是显示名（隐藏扩展名后的 "report"、本地化的 "文档"）。在同一个 `IFolderView` 上枚举所有项（`Items(SVGIO_ALLVIEW)`），比较 `SIGDN_NORMALDISPLAY`，只对同名的项取 `SIGDN_FILESYSPATH`，得到准确路径（`namedItems`）；超过 2 万项或超时就放弃，保持选中集合。两项显示名相同（扩展名隐藏时的 a.txt 和 a.jpg）时用 UIA 的选中状态区分，仍分不清就保持选中集合。回收站等虚拟项目没有路径：没选中时报告"没有项目"。
- UIA 说项目已选中时直接用选中集合，不枚举。

**macOS**（尽力而为，没有 Mac 实测）：在访达上用系统级元素的 `AXUIElementCopyElementAtPosition`（新增 `ax.Element.ElementAt`），确认元素属于访达进程，沿 `AXParent` 向上找 `AXURL`（其次 `AXFilename`，再在文件夹里按文件名或去掉扩展名的名字匹配）；指针下就是视图背景（AXOutline/AXList/AXGroup 等，且在滚动区域里）算空白处；侧边栏（`AXSourceList` 或标识含 sidebar）和工具栏算"其他"。项目必须在当前文件夹里或在选中集合里，否则保持以前的行为（例如访达窗口在前、指针却在桌面图标上）。任何一步失败都退回以前的行为。

**测试**：`pointer_test.go`（表格、显示名匹配：隐藏扩展名、本地化名、同名区分、枚举不完整；识别器按指针报告、无指针时不查名字；`PointerAt`；访达元素链；按名字匹配本地文件）、`selection/listhit_test.go`（UIA 元素链，假的节点）、`detect_darwin_test.go`（访达 + 指针）、`shell/ring_test.go`（圆环把指针交给检测函数，假的检测函数）。不自动操作真实的资源管理器。
