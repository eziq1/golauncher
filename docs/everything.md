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
| `hideSystemFiles` | false | 见 §2.2：打开后不显示系统和缓存文件 |
| `extraLowPriorityPaths` | 空 | 见 §2.2：用户自己追加的低优先级路径 |

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

### 2.4 实现记录

代码：`plugins/everything/rank.go`（分级、打分、生成排除条件，都是纯函数）、`plugins/everything/search.go`（查询策略）、`internal/winapi/everything/folders_windows.go`（`UserFolders`，用 `SHGetKnownFolderPath` 取 7 个已知文件夹，带 `KF_FLAG_DONT_VERIFY`；非 Windows 平台返回空）。

**在真实 Everything 上验证过的语法**（MatchPath 关闭时）：

- `notepad !"C:\Windows\"` 能排除 `C:\Windows` 下的结果。含 `\` 的词按完整路径做子串匹配，大小写不敏感，所以 `!"\node_modules\"` 能匹配任意位置的这一段目录。
- 补集写成 `<"c:\windows\"|"\node_modules\"|…>`，也就是"只要系统级"，用作查询 B。
- `\\?\C:\Windows\` 这种写法不起作用（Everything 的路径里没有这个前缀），所以规则里不使用它，只在本地分级时去掉这个前缀。

**实测耗时**（本机，索引几百万个文件）：

| 查询 | 耗时 |
|------|------|
| 任意 3 个字符以上的词，Max=100 | 约 37–45ms，基本是 Everything 扫一遍索引的固定开销 |
| 同上，Max=1000 | 和 Max=100 差不多（多 2–4ms） |
| 带 20 个排除条件，3 个字符以上 | 和不带时一样 |
| 带排除条件，1–2 个字符（`a`、`re`） | 100–270ms，比不带时慢 4–10 倍 |
| A、B 并发发给 Everything | 约 75ms：Everything 是串行处理 IPC 查询的，并发没有用 |
| 本地给 1000 条结果分级、打分、排序 | 0.9ms |

**和 §2.2 的差异**：

1. **查询 A 只排除"系统与缓存"一级**，"程序"一级留在 A 里，靠本地打分排到后面。所以 `hideSystemFiles` 只隐藏系统与缓存文件，不隐藏 Program Files。
2. **查询 B 用补集**（`原始关键词 <系统路径1|系统路径2|…>`），不再用原始关键词。这样 B 的结果不会和 A 重复，`Max` 设为 `maxResults - A 的条数` 就够了。仍然按路径去重，作为保险。
3. **每次查询都取 1000 条（`poolMax`）**，而不是 `maxResults` 条。按名称排序后截断，用户文件可能排在第 100 条以后；取 1000 条几乎不增加耗时，本地重新排序后再截取 `maxResults`。
4. **少于 3 个字符的查询只发一次不带排除条件的查询**，取 1000 条在本地排序。原因是 1–2 个字符时排除条件特别慢（见上表）。
5. **"窄查询"预测**：如果上一次查询是这次的前缀，而且已经知道它是窄的（A 不满一页，或者不带条件的查询不到 1000 条），这次就先发一次不带条件的查询。结果不到 1000 条，说明已经拿到全部匹配项，本地排序后直接返回，不再发 A 和 B。所以边打字边搜索时，每个按键只需要一次查询；只有在查询刚变窄的那一个按键上，才会同时执行 A 和 B。这个预测只影响往返次数，不影响结果。
6. **级别规则有补充**：
   - 用户目录下的其他文件夹（比如 `~\Sources`）也算"用户文件"，和其他盘的普通目录一样处理。用户目录下以 `.` 开头的目录（`~\.vscode` 等）算"程序"。在用户文件这一级里，桌面、文档、下载、图片、视频、音乐、OneDrive 这 7 个已知文件夹额外加 0.3 分。
   - 整个 `AppData`（不只是 `AppData\Local\Programs`）和整个 `scoop`（包括存放应用数据的 `scoop\persist`）算"程序"。
   - 系统与缓存级别新增 `.venv`、`target\debug`、`target\release`（Cargo 的编译产物）和 `EBWebView`（WebView2 的浏览器配置目录）。
   - 系统盘取自 `%SystemRoot%`，系统目录取自 `%SystemRoot%` 和 `%ProgramData%`，不写死 `C:`。
7. **打分**：级别权重 2（用户 6、普通 4、程序 2、系统 0），比其余各项的总和（≤ 1.7）还大，所以级别是严格的：低一级的结果永远排在后面。名称匹配质量 × 0.9（完全相等 1、前缀 0.7、单词开头 0.5、包含 0.25；多个词取平均，Everything 的运算符、通配符、路径词不参与计算），新近度最多 0.5（30 天内线性衰减），文件夹减 0.05。分数相同时保持 Everything 的原有顺序。
8. **根搜索**只执行查询 A（取 200 条），排序后只保留用户文件和普通两级的前 5 条。
9. **`extraLowPriorityPaths`**：绝对路径（`D:\VMs`、`\\nas\backup`、`E:`）按前缀匹配，其他写法（`vendor`、`build/out`）按目录段匹配。匹配到的文件归入系统与缓存级别：查询 A 会排除它们，`hideSystemFiles` 打开时也会隐藏。会去掉 `"<>|` 这几个字符，以免破坏 Everything 的语法。不展开环境变量。
10. 用户输入里的引号不成对时，会在末尾补一个引号；输入里有 `|` 时，用 `<…>` 把它括起来，保证排除条件作用于整个输入。
11. 测试用的 fixture 后端支持 Everything 语法的一个子集：`!`、`<a|b>`、引号，以及含 `\` 的词按完整路径匹配。所以 fixture 测试走的是和真实查询相同的文本。

**排序前后对比**（`glctl run builtin.everything/search --real`，前几条）：

| 搜索 | 之前 | 之后 |
|------|------|------|
| `notepad` | 前 12 条全部在 `C:\Windows\WinSxS`、`C:\ProgramData\…\Containers\Layers\…\WinSxS` 里 | `~\scoop\apps\git\…\usr\bin\notepad`（程序级里名称完全相等的排第一）、VS Code 扩展的资源文件……再往后是 `Program Files\WindowsApps\…\Notepad.exe`；`WinSxS` 排在最后。本机用户目录里没有名字含 notepad 的文件，所以第一屏都是程序级 |
| `report` | `_bugreports`（AppData）、7 条 `site-packages\…\_model_report`、`go\pkg\mod\…` | 用户文档目录下的几个 reports 文件夹排在最前，之后才是开发目录里的同名文件夹 |
| `文档` | 第一条是用户文档目录里的一个文件夹，接着是 8 条某个应用数据目录（`scoop\persist\…`）里的文件 | 用户文档目录里的同名文档和文件夹排在前面，下载目录里的 PDF 随后 |

**视图耗时**（`TestRealEverything`，同一次运行里同时测了 R20 之前的"一次不带条件的查询"作为基线；本机同时有其他任务在跑，抖动较大，下面是 3 次运行的结果）：

| | p50 | p95 |
|---|---|---|
| 逐字输入 8 个词（126 次查询） | 45–50ms | 55–98ms |
| 直接粘贴整个词（24 次查询） | 44–51ms | 82–96ms |
| 基线：一次不带条件的查询，Max=100 | 41–45ms | 45–109ms |

结论：p50 只比基线多 4ms 左右。**p95 ≤ 50ms 的目标没有达到**，原因是 Everything 扫一遍索引本身就要 40ms 左右；凡是需要执行 A 和 B 两次查询的情况（也就是刚粘贴进来的、结果很少的词，比如 `invoice`），耗时就是两次扫描，大约 85ms。要进一步降低，需要先返回 A 的结果，再用 `Host.Render` 把 B 的结果补进来。但现在宿主还没有接上 `OnRender` 和 `OnInvalidate`，所以暂时没有这样做，留作后续工作。

## 3. 已知限制

- 没有在 1.5 alpha 版和 arm64 上实测过。
- Everything 需要管理员权限才能启动时，自动启动会失败，只显示提示，不会弹出 UAC。
- 按 §2 的方案重新排序后，Everything 自身的排序方式只影响同分结果的先后。
- 详情面板里的修改时间用的是固定格式 `2006-01-02 15:04`，因为 Localizer 还没有日期格式化功能。

## 4. 变更记录

- 2026-10-15：首次实现。同时修复了"搜索无结果时程序崩溃"的问题，详见 [reliability.md §1](reliability.md)。
- 2026-10-15：R20，用户自己的文件排在前面，系统和缓存文件排在最后。新增偏好项 `hideSystemFiles` 和 `extraLowPriorityPaths`，详见 §2.4。
