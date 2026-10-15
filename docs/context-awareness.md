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
