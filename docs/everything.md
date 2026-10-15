# Everything 文件搜索插件

对应需求：R8（见 [requirements.md](requirements.md)）。

> 调用 Everything 来搜索电脑上的文件。它是 Windows 特有的插件，依赖系统上已经安装的 Everything；检测到没有安装时，就不启用这个插件。

## 1. 方案

### 1.1 和 Everything 通信

直接实现 Everything 的 IPC 协议，用纯 Go 编写（`internal/winapi/everything`），不依赖它的 SDK DLL，也不调用 `es.exe`，同样不需要 cgo。

1. 查找 Everything 的 IPC 窗口：先找窗口类 `EVERYTHING_TASKBAR_NOTIFICATION`，再找 1.5 alpha 版的 `EVERYTHING_TASKBAR_NOTIFICATION_(1.5a)`。
2. 在一个锁定的 OS 线程上创建 message-only 窗口，用来接收回复。
3. 通过 `WM_COPYDATA` 发送 `EVERYTHING_IPC_QUERY2` 请求，请求的字段有：文件名、路径、大小、修改时间。
4. 接收并解析 `EVERYTHING_IPC_LIST2` 回复，解析时做边界检查。
5. 等待回复时会响应取消，也有超时：以 ctx 的截止时间为准，最长 10 秒。

结构体布局取自官方的 `everything_ipc.h`，在 amd64 和 arm64 上都适用。

### 1.2 检测是否安装

依次检查，命中任何一项就算已安装：

1. 正在运行的 Everything：通过 IPC 窗口找到所属进程，拿到 exe 路径。
2. 注册表：`App Paths\Everything.exe`；HKLM、WOW6432Node、HKCU 下的 Uninstall 条目，条件是条目名为 `Everything`，或者 DisplayName 以 Everything 开头且发布者是 voidtools。
3. 常见安装目录：Program Files（含 x86），`%LOCALAPPDATA%\Programs\Everything`，以及 Scoop 的安装目录和 shim。

### 1.3 插件行为（`plugins/everything`，ID `builtin.everything`）

- **没有安装**：`Init` 返回包装了 `plugin.ErrUnavailable` 的错误，并附上原因（"Everything is not installed. Get it from voidtools.com."，原因文本会本地化）。插件不启用，设置页的插件列表显示这个原因，开关不可操作。这个"插件不可用"的机制是为此新加的 SDK 能力，其他依赖外部软件的插件也能用。
- **已安装但没有运行**：启动时在后台用 `-startup` 参数启动一次。搜索时如果仍然没有运行，列表只显示一条"Everything is not running"，并提供"启动 Everything"的动作。
- **命令** `search`（Search Files）：输入文字后调用 Everything 搜索。每条结果显示文件名、所在目录（用户目录缩写成 `~`）和相对修改时间。右侧详情面板显示路径、类型、大小和修改时间。

可用的动作：

| 动作 | 快捷键 |
|------|--------|
| 打开 | `Enter` |
| 在资源管理器中显示 | `Ctrl+Shift+F` |
| 复制路径 | `Ctrl+Shift+C` |
| 复制文件 | `Ctrl+Alt+C` |

偏好设置：

| 偏好项 | 默认值 | 说明 |
|--------|--------|------|
| `maxResults` | 100 | 每次最多显示的结果数，取值范围 1–1000 |
| `matchPath` | false | 是否连同路径一起匹配 |
| `showInRoot` | false | 打开后，根搜索输入 3 个字符以上时附带最多 5 个文件结果；这些结果排在应用和命令之后 |

### 1.4 测试

- 不依赖 Everything 的测试：用合成的二进制数据测试 IPC 编码和解析；用 fixture 后端（`testdata/fixtures/files.json`）测试插件逻辑。`glctl --fixtures` 也会用到这份 fixture。
- 依赖真实 Everything 的测试：没有运行 Everything 时自动跳过，所以在 CI 上不会运行。包括真实搜索、模拟快速打字的压力测试 `TestStressTyping`，以及界面层的 `TestRealFileSearch`。

## 2. 搜索结果排序：优先显示用户自己的文件（R20）

> 需求：Windows 里有很多缓存目录、系统目录，也就是非用户文件的目录。把它们的权重降到最低，搜索时优先展示用户自己的文件。

### 2.1 问题

现在的做法是让 Everything 按文件名排序，再截取前 `maxResults` 条。以 `notepad` 为例，前几条全是 `WinSxS` 和 `Containers\Layers` 里的系统文件；搜 `report` 会先出来 `site-packages` 里的源码目录。用户自己的文档反而被截断在 100 条以外，根本显示不出来。

### 2.2 方案：分两次查询，再在本地重新排序

**第一步：对路径分级**（`plugins/everything/rank.go`，纯函数，便于测试）

| 级别 | 权重 | 默认规则（大小写不敏感） |
|------|------|--------------------------|
| 用户文件 | 高 | 用户目录下的已知文件夹：桌面、文档、下载、图片、视频、音乐、OneDrive（用 `SHGetKnownFolderPath` 取真实路径，支持被重定向的文件夹）；非系统盘上的普通目录 |
| 普通 | 中 | 其他位置 |
| 程序 | 低 | `Program Files`、`Program Files (x86)`、`%LOCALAPPDATA%\Programs`、`scoop\apps` |
| 系统与缓存 | 最低 | `C:\Windows`、`C:\ProgramData`、`$Recycle.Bin`、`System Volume Information`、`AppData\Local\Temp`，以及路径中任一段是 `node_modules`、`.git`、`__pycache__`、`.cache`、`Cache`、`CacheStorage`、`site-packages`、`go\pkg\mod`、`.nuget\packages`、`WinSxS`、`Containers\Layers` |

**第二步：分两次查询**，保证用户文件不被截断：

1. **查询 A**：在用户的关键词后面追加排除条件，比如 `notepad !"C:\Windows\" !"\node_modules\" …`（Everything 的语法：`!` 表示排除，包含 `\` 的词会去匹配完整路径）。最多取 `maxResults` 条。
2. **查询 B**：如果 A 的结果不满 `maxResults`，就用原始关键词再查一次，去掉和 A 重复的结果后补足。这些补进来的结果大多是系统和缓存文件，会排在最后。

**第三步：本地重新排序**：

```
得分 = 路径级别权重
     + 名称匹配质量（完全相等 > 前缀 > 单词开头 > 包含）
     + 新近度（30 天内修改过的加分，按时间衰减）
     + 文件夹比文件略低（同等条件下）
```

排序后再截取 `maxResults` 条。查询 A 是 Everything 的索引查询，速度很快；B 只在结果不足时才执行。实际耗时要用 `glctl bench --real` 验证，目标是 p95 不超过 50ms。

**第四步：可配置**（新增的偏好项）：

| 偏好项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `hideSystemFiles` | bool | false | 打开后不执行查询 B，系统和缓存文件完全不显示 |
| `extraLowPriorityPaths` | stringList | 空 | 用户自己追加的低优先级路径或目录名 |

根搜索里的文件结果（`showInRoot`）用同一套排序，并且只取用户文件和普通两级。

### 2.3 测试

- `rank.go` 用表驱动测试覆盖各类路径，包括大小写、`\\?\` 前缀、被重定向的已知文件夹。
- 用 fixture 后端测试"用户文档排在系统文件前面"，以及 `hideSystemFiles` 打开后的效果。
- 接真实 Everything 的测试（没有运行 Everything 时跳过）：搜 `notepad` 时第一条不能在 `WinSxS` 里。
## 3. 已知限制

- 没有在 1.5 alpha 版和 arm64 上实测过。
- Everything 需要管理员权限才能启动时，自动启动会失败，只显示提示，不会弹出 UAC。
- 按 §2 的方案重新排序后，Everything 自身的排序方式只影响同分结果的先后。
- 详情面板里的修改时间用的是固定格式 `2006-01-02 15:04`，因为 Localizer 还没有日期格式化功能。

## 4. 变更记录

- 2026-10-15：首次实现。同时修复了"搜索无结果时程序崩溃"的问题，详见 [reliability.md §1](reliability.md)。
