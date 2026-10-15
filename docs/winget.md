# WinGet 插件设计方案

> 状态：设计 v1 · 2026-10-15 · 对应需求 R39（见 [requirements.md](requirements.md)）
>
> 需求：增加一个内置的 WinGet（Windows 程序包管理器）插件，功能参照现有的 Scoop 插件（[scoop.md](scoop.md)）。

## 1. 定位

- 内置插件 `builtin.winget`（`plugins/winget`）。和 Scoop、Everything 一样依赖系统里已有的软件：**找不到 `winget.exe` 时插件不启用**，`Init` 返回 `plugin.ErrUnavailable`，设置页显示原因（"WinGet is not installed. Install App Installer from the Microsoft Store."）。
- **读操作尽量不启动 winget**：搜索直接读 winget 源的本地索引（SQLite），边输入边出结果；已安装列表和可升级列表要调用一次 `winget list`，在后台运行并缓存，界面先显示缓存。
- **写操作**（安装、卸载、升级、全部升级、固定、更新源）调用 winget 命令行，作为宿主的后台任务运行（[background-tasks.md](background-tasks.md)），一次一个，实时输出，可以停止。

## 2. 功能清单

| 功能 | 入口 | 实现 |
|------|------|------|
| 搜索软件包 | 命令 **Search WinGet Packages**（视图）；根搜索 `winget <关键词>` / `wg <关键词>`（偏好项 `showInRoot`，默认开启） | 本地索引里按 ID、名称、moniker、命令名、标签模糊搜索 |
| 软件包详情 | 列表右侧详情面板（索引里的字段，立即可得）；动作 **Show Details**（`Ctrl+D`）推入详情视图 | 详情视图调用 `winget show --id <id> --exact --source <源>`：发布者、主页、许可证、简介、发行说明、标签、安装程序类型等 |
| 安装 | 未安装软件包的主动作 `Enter` | `winget install --id <id> --exact --source <源> --accept-source-agreements --accept-package-agreements --disable-interactivity` |
| 卸载 | `Ctrl+X`（Destructive，宿主先确认） | `winget uninstall --id <id> --exact [--source <源>] --accept-source-agreements --disable-interactivity` |
| 升级 | 有新版本时为主动作，`Ctrl+U` | `winget upgrade --id <id> --exact [--source <源>] --accept-source-agreements --accept-package-agreements --disable-interactivity` |
| 已安装列表 | 命令 **Installed WinGet Packages**（视图） | 解析 `winget list`：包括不是用 winget 安装的软件（`ARP\…`、`MSIX\…`），有新版本的排在前面 |
| 可升级列表 | 命令 **WinGet Updates**（视图） | `winget list` 里"可用版本"一列不为空的行；第一条是"全部升级" |
| 全部升级 | 命令 **Upgrade All WinGet Packages**（Destructive）；可升级列表的第一条 | `winget upgrade --all --accept-source-agreements --accept-package-agreements --disable-interactivity`（固定的软件包由 winget 自己跳过） |
| 源列表 | 命令 **WinGet Sources**（视图） | `winget source export`（JSON，不受界面语言影响）：名称、地址、类型、是否显式源；可以更新单个源或全部源 |
| 更新源 | 命令 **Update WinGet Sources**；源列表的动作 | `winget source update [--name <源>]` |
| 固定 / 取消固定 | 已安装且来自某个源的软件包的动作 `Ctrl+Shift+H` | `winget pin add --id <id> --exact --source <源>` / `winget pin remove …`；当前的固定状态直接读 `pinning.db` |
| 以管理员身份运行 | 安装、升级的次要动作 `Ctrl+Shift+A` | 用 `runas` 打开一个控制台窗口运行同样的命令，并加 `--wait`（结束后按任意键关闭），见 §5 |
| 其他动作 | 所有软件包 | 打开主页（详情视图里）、在 GitHub 上打开清单（`winget` 源）、复制 ID、复制安装命令 |

不做的：添加、删除、重置源（需要管理员权限，误操作影响大；`source add/remove/reset`）；`winget configure`、`import/export`；Microsoft Store 源（`msstore`）的搜索，见 §8。

## 3. 数据来源

### 3.1 可选方案的比较（本机实测，详见 §10）

| 方案 | 结论 |
|------|------|
| 解析 `winget search` / `list` / `upgrade` 的表格输出 | 每次要启动一个进程，`search` 约 2.3 秒、`list` 约 4.4 秒、`upgrade` 1.5–2.2 秒，不能边输入边搜。输出是**本地化的**（表头随 Windows 显示语言变化）、按显示宽度对齐的表格。没有 `--output json` 之类的选项（`winget --help` 里没有） |
| `winget export -o x.json --include-versions` | JSON，不受语言影响，但要 8 秒，而且只含能在源里找到的软件包，没有"可用版本" |
| PowerShell 模块 Microsoft.WinGet.Client | 返回对象（有 `IsUpdateAvailable`），但大多数机器上没有装（本机就没有），要从 PSGallery 安装；每次还要启动 PowerShell 并加载模块，延迟以秒计 |
| COM / WinRT API（`Microsoft.Management.Deployment`） | 最"正规"，但从 Go 调用要手写 WinRT 的 `IInspectable` 虚表、`IAsyncOperation`、`IVectorView` 等绑定，工作量和风险都很大；激活还依赖 App Installer 的打包 COM 服务器。放到以后（§8） |
| **winget 源的本地索引 `index.db`** | `winget` 源是一个"预索引"的 MSIX 包（`Microsoft.Winget.Source_8wekyb3d8bbwe`），里面的 `Public\index.db` 是 SQLite（schema 2.0）：表 `packages`（`id`、`name`、`moniker`、`latest_version`、`arp_min_version`、`arp_max_version`、`hash`）以及 `tags2`、`commands2`、`norm_names2`、`norm_publishers2`、`productcodes2` 等映射表。约 1.5 万个软件包，用项目已有的 `modernc.org/sqlite`（纯 Go，插件允许使用）只读打开，一次读进内存约几十毫秒 |
| **`pinning.db`** | `%LOCALAPPDATA%\Packages\Microsoft.DesktopAppInstaller_8wekyb3d8bbwe\LocalState\pinning.db`，SQLite，表 `pin(package_id, source_id, type, version)`。比 `winget pin list`（本地化表格，0.65 秒）快而且不受语言影响 |
| **`winget source export`** | 每个源一行 JSON（`Name`、`Arg`、`Type`、`Data`、`Identifier`、`Explicit`），约 0.13 秒，不受语言影响 |

### 3.2 选定的方案（混合）

1. **搜索 → 本地索引。** 先 `winget source export` 得到源列表；对每个 `Type` 为 `Microsoft.PreIndexed.Package`、非显式（`Explicit: false`）的源，`Data` 就是索引包的包系列名，用 `GetPackagesByPackageFamily` + `GetPackagePathByFullName`（kernel32，Windows 8.1 起就有）找到安装目录，打开 `<目录>\Public\index.db`（`mode=ro&immutable=1`：包里的文件不会变，源更新时会装成一个新版本的目录）。`source export` 失败时退回固定的包系列名 `Microsoft.Winget.Source_8wekyb3d8bbwe`、源名 `winget`。只支持 schema 主版本 2；不认识的版本不报错，只是搜索不可用并在视图里说明。
   - 读进内存：`packages` 全表 + 标签 + 命令。搜索打分参照 Scoop：ID 或名称完全相同 > moniker 相同 > 名称/ID 前缀 > 命令名相同 > 名称包含 > ID 里的段（`Git.Git` 的 `Git`）前缀 > 标签 > 子序列；多个词都要匹配。
   - **刷新**：窗口显示时检查索引包的完整名称有没有变化（一次 API 调用，不启动进程）；变了就在后台重新读，然后 `Invalidate`。
2. **已安装 / 可升级 → `winget list`，后台运行并缓存。**
   - 命令：`winget list --accept-source-agreements --disable-interactivity`。stdout 是管道时 winget 不截断列（本机实测输出里没有 `…`），编码是 UTF-8。
   - **表格解析不依赖语言**：找到全是 `-` 的分隔行，它上面一行是表头；表头里每个"空格之后的非空格"位置是候选的列起点。位置按**显示宽度**计算（中日韩字符占两格，winget 也是这样对齐的，用 `golang.org/x/text/width`）。再用数据行验证：某个候选列起点的前一格在所有行里都必须是空格，否则说明它在某个多词的本地化表头内部，合并掉。遇到空行或汇总行（如 "8 upgrades available."）就停止。列按顺序是：名称、ID、版本、可用版本、源；只有 4 列时，最后一列的值都是已知的源名就当作"源"，否则当作"可用版本"。
   - 不是用 winget 装的软件（ID 以 `ARP\`、`MSIX\` 开头，没有源）也显示，可以卸载，但不能固定和升级。
   - **缓存**：结果写到 `CacheDir\installed.json`，下次启动立即可用。已安装、可升级、搜索视图打开时，如果数据超过 5 分钟就在后台再跑一次 `winget list`（同时只跑一个），视图先显示缓存，跑完 `Invalidate`。第一次还没有任何数据时，视图显示"正在读取已安装的软件包…"。操作（安装、卸载等）结束后也刷新一次。
   - 可升级 = "可用版本"不为空；固定状态从 `pinning.db` 读，在条目上标出"已固定"（`upgrade --all` 会跳过它们，"全部升级"的计数也不含它们）。
3. **详情 → 按需 `winget show`。** 列表的详情面板只用索引里的字段（ID、名称、版本、moniker、源、标签、命令，已安装时加已安装版本和可用版本）。"Show Details" 推入一个视图，运行 `winget show --id <id> --exact --source <源> --accept-source-agreements --disable-interactivity`（本机约 0.3 秒，最多等 8 秒），结果按 ID+版本缓存。
   - `show` 的输出也是本地化的："`键: 值`" 的行，值可以换行缩进（`Description:`、`Tags:` 下面的列表等）。解析成有序的键值对，**键原样显示**（和用户的 Windows 语言一致），值里的 URL 变成可点击的动作。英文键（`Publisher`、`Homepage`、`License`、`License Url`、`Release Notes Url`、`Description`）能识别出来，用于"打开主页"等动作和 Markdown 正文；不是英文时，"打开主页"取第一个 `http(s)` 值。

## 4. 执行操作（后台任务）

- **执行方式**：直接运行 `winget.exe`，参数作为 `Argv` 逐个传入（不经过 shell，没有引号问题），`ProcessSpec.CommandLine` 是给用户看的 `winget install --id Contoso.App --exact …`。宿主负责无窗口启动、Job Object、输出解码（winget 的进度条用 `\r` 覆盖，宿主会清理）。
- **串行**：所有操作进宿主的队列 `"winget"`，同时只运行一个；排队、停止、复制输出、重新运行都由宿主提供。
- **非交互**：每条命令都带 `--disable-interactivity`；会用到源的命令带 `--accept-source-agreements`；安装、升级带 `--accept-package-agreements`。偏好项 `installMode`：`default`（winget 默认，安装程序可能显示进度窗口）、`silent`（加 `--silent`）、`interactive`（加 `--interactive`）。
- **动作立即返回**：Info Toast "Install Contoso App: running in the background"（按 `Ctrl+J` 查看输出），任务结束时宿主按 `NotifyAlways` 通知。`TaskSpec.OnDone` 里刷新已安装列表（`installed.json` 失效）和固定状态；更新源之后重新读索引。
- **确认**：卸载、全部升级是 Destructive，宿主先确认；全部升级同时是 Destructive 命令（`Command.Destructive`）。
- **参数校验**：源里的 ID 必须匹配 `^[A-Za-z0-9][A-Za-z0-9._+-]*$`；已安装列表里的本地 ID（`ARP\Machine\X64\{…}`、`MSIX\…`）只允许卸载，不能以 `-` 开头、不能含引号和控制字符。源名必须匹配 `^[A-Za-z0-9][A-Za-z0-9._-]*$`。不合法的请求返回失败 Toast，不产生任务。

## 5. 权限提升（UAC）

- winget 本身不需要管理员权限。按机器范围安装的安装程序会自己请求提升，Windows 弹出 UAC 确认框；我们的进程没有控制台窗口，但 UAC 框照常出现在安全桌面上。用户拒绝时安装失败，任务显示退出码，通知里有最后一行输出。
- 少数软件包要求 winget 自己以管理员身份运行（清单里的 `ElevationRequirement: elevationRequired`），winget 会报错退出。为此提供次要动作 **Install / Upgrade as Administrator**（`Ctrl+Shift+A`）：`ActionOpen` 打开 `winget.exe`，`Verb: runas`，参数同上再加 `--wait`。这时会出现 UAC 和一个可见的控制台窗口，输出在那个窗口里，不进后台任务（提升后的进程不能被非提升的启动器接管输出和 Job Object）。
- 启动器不以管理员身份运行，也不会自动提权。

## 6. 偏好项

| 名称 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `showInRoot` | bool | true | 根搜索里用 `winget` / `wg` 前缀直达 |
| `maxResults` | number | 50 | 搜索结果的最大条数（最多 500） |
| `installMode` | select | `default` | `default` / `silent` / `interactive` |

## 7. 测试

- **单元测试**（hostfake，假的 `Runner` 和内存中的 SQLite）：
  - 没有 winget 时插件不可用；
  - 索引读取：用和真实 schema 2.0 相同的建表语句在内存里建一个小索引（虚构的软件包）；不认识的 schema 版本；
  - 搜索打分和排序（含中文名、多词、`maxResults`）；
  - 表格解析：英文表头、虚构的本地化表头（含多词表头、中文名的显示宽度）、只有 4 列、汇总行、前面有进度输出、没有表格；
  - `winget show` 解析（多行值、列表、URL），`source export` 解析，`pinning.db` 读取；
  - 每个动作生成的命令行（假任务执行器只记录）、任务的标题/队列/通知方式、非法 ID 不产生任务、`OnDone` 后刷新；
  - 已安装缓存：先显示缓存、过期后后台刷新并 `Invalidate`、同时只有一个 `winget list`；
  - 根搜索前缀。
- **`glctl`**：`builtin.Fake` 用 `<fixtures>/winget`：`index.sql`（建表和虚构数据，启动时导入内存 SQLite，走和真实环境相同的读取代码）、`pinning.sql`、`list.txt`（虚构的 `winget list` 输出）、`show/<id>.txt`、`sources.jsonl`。只读的 winget 命令由假的 `Runner` 从这些文件回答；操作照常进宿主的假任务执行器。golden：搜索、已安装、可升级、源列表、详情视图、安装生成的命令；卸载先确认。
- **真实环境测试**：只有设置了 `GOLAUNCHER_WINGET_TESTS=1` 才运行，并且**只读**：读真实索引、搜索、`winget list`、`winget show`、`winget source export`、读 `pinning.db`，打印数量和耗时；断言没有产生任何后台任务。**绝不在测试里安装、卸载、升级、修改源或固定。**

## 8. 已知限制 / 以后

- **Microsoft Store 源（`msstore`）不能离线搜索**：它是 REST 源，没有本地索引。以后可以在搜索结果末尾加一条"在 Microsoft Store 中搜索 …"，按需运行 `winget search --source msstore`（同样的表格解析）。
- 详情要运行 `winget show`，所以放在单独的视图里，不在列表的详情面板里实时显示。
- `winget list` 慢（数秒），可升级状态最多晚 5 分钟（可以在视图里手动刷新）。
- COM API 可以同时解决本地化和速度问题，但需要 WinRT 绑定，留到以后评估。
- 只支持索引 schema 2.x；winget 改了索引格式时，搜索会在视图里提示不可用，其余功能不受影响。

## 9. 实现记录（R39）

见下方，实现后补充。
