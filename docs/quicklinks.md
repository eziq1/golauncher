# 快速链接与网页搜索插件设计方案

> 状态：已实现并发布，见 §13 · 2026-10-15 · 对应需求 R49（见 [requirements.md](requirements.md)）
>
> 需求：新增插件：快速链接与网页搜索。

## 1. 定位

一个内置插件 `builtin.quicklinks`（`plugins/quicklinks`），纯 Go，只用 SDK（`pkg/plugin`）和 `internal/winapi/shell`（找已安装的浏览器）。包含四块功能：

| 功能 | 怎么用 |
|------|--------|
| 关键词网页搜索 | `g golang generics` → "用 Google 搜索“golang generics”"，`Enter` 在默认浏览器里打开 |
| 默认搜索引擎兜底 | 主搜索没有好结果时，列出"用 Google 搜索“…”"（宿主的 `FallbackProvider`） |
| 输入网址直接打开 | `github.com/foo`、`https://…`、`localhost:3000`、`192.168.1.1` → 第一条"打开网址" |
| 快速链接 | 用户自己定义的、有名字的链接：网址、文件、文件夹或程序（命令行），支持 `{query}`、`{clipboard}`、`{date}` 等占位符 |

**为什么是一个插件而不是两个**：网页搜索引擎和带 `{query}` 的快速链接本质上是同一种东西（"名字 + 关键词 + 带占位符的模板"），共用模板展开和编码、关键词解析、"用指定浏览器打开"、存储、导入导出；更重要的是**关键词是同一个命名空间**：`jira 123` 是快速链接还是搜索引擎，必须在一个地方判断和查重（创建快速链接时就能提示"关键词已被 Google 使用"）。拆成两个插件就要跨插件读彼此的数据，SDK 不支持。设置页里也只有一个插件页，偏好设置不多。

## 2. 关键词网页搜索

### 2.1 内置搜索引擎

| ID | 名称 | 默认关键词 | 网址模板 |
|----|------|------------|----------|
| `google` | Google | `g` `谷歌` | `https://www.google.com/search?q={query}` |
| `bing` | Bing / 必应 | `b` `必应` | `https://www.bing.com/search?q={query}` |
| `baidu` | 百度 | `bd` `百度` | `https://www.baidu.com/s?ie=utf-8&wd={query}` |
| `duckduckgo` | DuckDuckGo | `ddg` | `https://duckduckgo.com/?q={query}` |
| `github` | GitHub | `gh` | `https://github.com/search?q={query}` |
| `stackoverflow` | Stack Overflow | `so` | `https://stackoverflow.com/search?q={query}` |
| `youtube` | YouTube | `yt` | `https://www.youtube.com/results?search_query={query}` |
| `bilibili` | 哔哩哔哩 | `bili` `b站` | `https://search.bilibili.com/all?keyword={query}` |
| `wikipedia` | 维基百科 | `wiki` `维基` | `https://en.wikipedia.org/wiki/Special:Search?search={query}`（中文界面用 `zh.wikipedia.org`） |
| `zhihu` | 知乎 | `zh` `zhihu` `知乎` | `https://www.zhihu.com/search?type=content&q={query}` |
| `douban` | 豆瓣 | `db` `douban` `豆瓣` | `https://www.douban.com/search?q={query}` |
| `taobao` | 淘宝 | `tb` `淘宝` | `https://s.taobao.com/search?q={query}` |
| `jd` | 京东 | `jd` `京东` | `https://search.jd.com/Search?enc=utf-8&keyword={query}` |
| `npm` | npm | `npm` | `https://www.npmjs.com/search?q={query}` |
| `gopkg` | Go 包（pkg.go.dev） | `go` `gopkg` | `https://pkg.go.dev/search?q={query}` |
| `mdn` | MDN | `mdn` | `https://developer.mozilla.org/search?q={query}`（中文界面 `/zh-CN/search`） |
| `translate` | Google 翻译 | `gt` `translate`（R99 之前是 `tr`，现在 `tr` 归翻译插件） | `https://translate.google.com/?sl=auto&tl=en&text={query}&op=translate`（中文界面 `tl=zh-CN`） |
| `youdao` | 有道翻译 | `yd` `有道`（`翻译` 自 R99 起归翻译插件） | `https://www.youdao.com/result?word={query}&lang=en` |
| `maps` | Google 地图 | `map` `maps` | `https://www.google.com/maps/search/{query}` |
| `amap` | 高德地图 | `amap` `gd` `高德` `地图` | `https://www.amap.com/search?query={query}` |

- 全部默认启用（只在输入关键词时才出现，不打扰普通搜索）。可以逐个停用、改关键词、恢复默认；内置引擎的名称和网址不能改（想改就新建一个自定义引擎）。
- **字符集**：所有引擎都用 UTF-8 加百分号编码（百度加 `ie=utf-8`，京东加 `enc=utf-8`），不需要 GBK。自定义引擎的模板也一律按 UTF-8 编码。
- **和其他插件的前缀不冲突**：`time` `tz` `timer` `计时` `倒计时` `remind` `提醒` `winget` `wg` `scoop` `sc` `ip` `ping` `dns` `port` `wifi` `tracert` `ps` `kill` `进程`，以及翻译插件的 `tr` `fy` `翻译`（R99，[translator.md](translator.md) §6）都不是默认关键词（有测试检查）。

### 2.2 关键词解析

- 大小写不敏感；关键词后面必须是空格（含全角空格）或结尾：`gh` 不会触发 `g`，`good` 不会触发 `go`。**中文关键词也一样**（和计时器的前缀不同）：`百度网盘`、`有道云笔记`、`高德地图`、`谷歌浏览器` 都是应用名，不能被当成"用百度搜索“网盘”"；要搜索就写 `百度 天气`、`b站 猫`。多个关键词都匹配时取最长的。
- 不用英文品牌全名当默认关键词（`google`、`bing`、`baidu`）：`google chrome`、`bing wallpaper` 是应用名。输入 `goo`、`wik` 这种名称的开头（至少 3 个字）时，仍然会列出"用 Google 搜索"这条（带参数、不预打分），所以按名称也能找到。
- 优先级：快速链接的关键词 > 自定义引擎 > 内置引擎（创建时已经查重，正常不会同时匹配）。
- 关键词后面有内容：返回一条预打分的条目（Score 1，排第一）：
  - 标题 `用 Google 搜索“golang generics”` / `Search Google for “golang generics”`，副标题是展开后的网址；
  - 操作：打开（`Enter`，`ActionOpen` 网址）、复制网址（`Ctrl+Shift+C`）、用某个浏览器打开（每个检测到的浏览器一个，见 §6）。
- 只输入关键词（宿主会去掉首尾空格，所以 `g` 和 `g ` 一样），或者输入引擎名称的开头（至少 3 个字）：返回这个引擎的普通条目"用 Google 搜索"（不预打分，由宿主按关键词匹配），带一个可选的内联参数"搜索内容"。`Tab` 进入参数输入，`Enter` 搜索；参数为空时打开引擎首页。
- **已知的取舍**：预打分的条目总是排在最前面，所以如果有应用名正好是"关键词 + 空格 + 其他词"，会先出搜索条目。默认关键词都选了很少出现在应用名开头的短词；碰到冲突可以停用或改关键词。

### 2.3 不记入"最近使用"

关键词搜索、搜索建议和"打开网址"这几种条目带着用户输入的文字。宿主默认会把执行过的主搜索条目（含标题）存进"最近使用"，这样搜索词就会留在数据库里，空搜索时还会显示出来。为此 SDK 新增一个字段 **`Item.Transient`**（JSON `transient`）：这样的条目和兜底条目一样**不记入最近使用和排序记录**。改动只在 `internal/core` 的 `Engine.Run` 一行。快速链接和"用 Google 搜索"（只有关键词、带参数的那条）不加这个标记，它们记入最近使用是有用的。

## 3. 默认搜索引擎兜底

- 实现 `plugin.FallbackProvider`：主搜索没有好结果（没有结果，或最好的匹配低于 0.5）时，宿主在最后的"兜底命令"一节列出：
  - `用 Google 搜索“…”`（默认引擎）；
  - 偏好设置里选了"第二个兜底搜索引擎"时，再加一条。
- **默认引擎**：偏好 `defaultEngine`，默认值"自动"：界面语言是简体中文时用 **Bing**，其他语言用 **Google**。理由：Google 在中国大陆通常打不开；Bing 在大陆可以访问（`www.bing.com` 会自动转到 `cn.bing.com`），结果里广告比百度少，中英文都好用；想用百度的人在设置里选一下即可。第二个兜底引擎默认不设。
- 停用的引擎不能作为默认引擎；如果选中的默认引擎被停用了，退回"自动"的选择。
- **和 AI 的"问 AI"的顺序**：~~兜底条目按插件注册顺序排列，`builtin.quicklinks` 注册在 `builtin.ai` 前面，所以固定是：网页搜索在前，"问 AI：…"在后。~~ **修订（2026-10-16，R56）**：改为 **"问 AI：…"在前，网页搜索在后**。不再靠注册顺序：插件实现 `plugin.FallbackOrderer` 声明自己的备选条目排在哪，quicklinks 返回 `plugin.FallbackOrderLast`，总是排在最后（见 §14）。打开了 AI 时，没有结果按 `Enter` 运行"问 AI"；`Tab` 仍然是 AI。AI 关闭时（默认）只有网页搜索。
- 偏好 `fallback`（默认开）可以关掉兜底。
- 兜底条目本来就不记入最近使用；载荷里带着完整网址。

## 4. 输入网址直接打开

偏好 `openURLs`（默认开）。输入（去掉首尾空格后）**整个**是一个网址时，第一条是"打开 github.com/foo"（预打分，排在最前，不记入最近使用），副标题是要打开的完整网址。

识别规则（全部在 `urls.go`，表格测试覆盖）：

| 输入 | 结果 |
|------|------|
| `http://` `https://` 开头 | 原样打开（必须有主机名） |
| `www.` 开头的域名 | 前面加 `https://` |
| 域名：`标签.标签…顶级域`，顶级域在内置的常用列表里（com、org、net、io、dev、cn、uk、de、jp…约 80 个），后面可以有 `:端口`、`/路径`、`?查询`、`#片段` | 加 `https://` |
| `localhost`（可以带端口和路径） | 加 `http://` |
| IPv4（4 段，每段 0–255）可以带端口和路径；`[IPv6]` 带方括号 | 加 `http://` |

不识别（避免误判）：

- 有空格的文字；
- 数字和版本号：`1.5`、`3.14`、`v1.2.3`、`1.2.3`（只有 3 段）；
- 文件名：`file.txt`、`main.go`、`config.json`、`node.js`、`index.html`——顶级域不在列表里；**同时是顶级域的常见扩展名**（`.py` `.md` `.sh` `.rs` `.pl` `.zip` `.mov` `.ps` `.so` 等）也不在列表里，除非前面有 `www.`、后面有路径或者带协议（`setup.py` 不识别，`https://setup.py` 识别）；
- 邮箱 `a@b.com`、Windows 路径 `C:\…`、`\\server`、只有一个单词（`localhost` 除外）；
- `..` 连续的点、以 `-` 开头或结尾的标签、端口不是 1–65535。

## 5. 快速链接

### 5.1 数据

| 字段 | 说明 |
|------|------|
| 名称 | 必填，显示在主搜索里；宿主负责模糊匹配和拼音匹配 |
| 链接 | 必填：网址（任何协议：`https:`、`mailto:`、`ms-settings:`…；`github.com` 这种裸域名按 §4 补成 https）、文件或文件夹路径（支持 `%USERPROFILE%` 这类环境变量）、或者程序加参数（见下） |
| 关键词 | 可选，不能有空格；输入 `关键词 内容` 时把内容填进 `{query}` |
| 打开方式 | 可选：程序名或路径（如 `firefox.exe`）。为空时用系统默认的程序打开 |

**程序加参数**：链接以 `"` 开头时，引号里是程序，后面是参数（`"C:\Tools\app.exe" --open {query}`）；或者第一个空格前的部分以 `.exe` `.com` `.bat` `.cmd` 结尾（`notepad.exe C:\notes.txt`）。其余情况整个链接是一个目标（可以是带空格的路径）。

### 5.2 占位符

| 占位符 | 内容 |
|--------|------|
| `{query}`（也可以写 `{argument}`） | 输入的内容：关键词后面的文字，或者主搜索里内联参数的值 |
| `{clipboard}` | 剪贴板里的文字 |
| `{date}` `{time}` `{datetime}` | 当前日期 `2026-10-15`、时间 `14:30`、两者 `2026-10-15 14:30` |
| `{date:格式}`（`time`、`datetime` 同样） | 自定义格式，`yyyy` `yy` `MM` `M` `dd` `d` `HH` `H` `mm` `ss`，例如 `{date:yyyyMMdd}` |
| 加 `:raw`（`{query:raw}`、`{clipboard:raw}`） | 不编码 |

- **编码**：链接是网址时，`{query}` 和 `{clipboard}` 的值按 UTF-8 百分号编码，空格写成 `%20`（`&` `#` `+` `/` `?` `=` 都会被编码；中文、emoji 按 UTF-8 字节编码）。是路径或命令行时原样插入。
- 不认识的 `{…}` 原样保留（网址里本来就可能有花括号）。
- 时间用 `Host.Now()`（测试可以固定），剪贴板用 `Host.Clipboard()`。剪贴板只在执行时读取，不在每次按键时读。

### 5.3 在主搜索里

- 每个快速链接是主搜索里的一个普通条目（分组"快速链接"），标题是名称，关键词是用户设置的关键词，图标按链接类型（网址 `globe`、文件夹 `folder`、文件 `file`、程序 `app`；不联网取网站图标）。
- 有 `{query}` 的快速链接带一个必填的内联参数（宿主现有的命令参数机制）；或者直接输入 `关键词 内容`，这时出现预打分的"打开 Jira：123"。
- 操作：打开（`Enter`）、复制链接、编辑（`Ctrl+E`）、复制一份（Duplicate）、删除（`Ctrl+X`，红色，宿主先确认）。
- **执行方式**：没有动态占位符的快速链接直接是 `ActionOpen`（AI 看到的风险等级是"打开"）。有 `{query}`、`{clipboard}`、`{date}` 等占位符的，必须在执行的那一刻展开（"最近使用"里存的是条目快照，提前展开的日期会过期，剪贴板内容也会被存进数据库），所以主操作是一个回调，回调里展开后返回 `Result.Do = ActionOpen`。这类回调对 AI 来说是"改动"级别（每次询问），比"打开"更保守；见 §10。
- **别名和全局快捷键**：宿主的别名、快捷键只能设给清单里声明的命令，插件运行时不能新增命令。所以快速链接没有宿主的别名和全局快捷键，用自己的"关键词"代替别名；全局快捷键这一期不支持（记为待定）。

### 5.4 管理

- 命令 **"创建快速链接"**（`create`）：表单：名称、链接（说明里列出占位符）、关键词、打开方式。提交时检查：名称和链接不能为空；关键词不能有空格，不能和其他快速链接或已启用的搜索引擎的关键词重复。
- 命令 **"搜索快速链接"**（`quicklinks`）：列出全部快速链接（本地筛选、右侧详情），每条有上面的操作；列表最后是"创建快速链接""导入""导出""复制为 JSON"。
- **不预装**快速链接（预装的链接多半不是用户要的，而且删掉也麻烦）；文档 §5.6 给几个例子。

### 5.5 导入导出

JSON 格式（快速链接和自定义搜索引擎在同一个文件里）：

```json
{
  "golauncher": "quicklinks",
  "version": 1,
  "quicklinks": [
    {"name": "Go 标准库", "link": "https://pkg.go.dev/std"},
    {"name": "Jira 工单", "link": "https://jira.example.com/browse/{query}", "keyword": "jira"}
  ],
  "searchEngines": [
    {"name": "Go 源码", "keywords": ["gocs"], "url": "https://cs.opensource.google/search?q={query}&ss=go"}
  ],
  "builtinEngines": [
    {"id": "taobao", "enabled": false},
    {"id": "google", "keywords": ["g", "gg"]}
  ]
}
```

- **导出**：表单里填一个文件路径，写入；或者"复制为 JSON"到剪贴板。导出的是全部快速链接、自定义引擎，以及改过的内置引擎设置。
- **导入**：表单里填文件路径，或者直接粘贴 JSON。合并而不是覆盖：名称和链接都相同的快速链接跳过；关键词和已有的冲突时去掉这个关键词（导入后的提示里写明）；同名同网址的自定义引擎更新关键词。也接受 Raycast 导出的快速链接格式（顶层是数组，字段 `name`、`link`、`openWith`）。
- 不认识的字段忽略；格式错误时提示，不改动已有数据。

### 5.6 例子（不预装）

| 名称 | 链接 | 关键词 |
|------|------|--------|
| 翻译剪贴板 | `https://translate.google.com/?sl=auto&tl=zh-CN&text={clipboard}` | |
| 今天的日记 | `%USERPROFILE%\Documents\日记\{date}.md` | |
| 在 GitHub 打开仓库 | `https://github.com/{query:raw}` | `repo` |
| 下载文件夹 | `%USERPROFILE%\Downloads` | |
| 用 VS Code 打开项目 | `"C:\Program Files\Microsoft VS Code\Code.exe" "D:\src\{query:raw}"` | `code` |

## 6. 用指定浏览器打开

- 从注册表 `HKCU`/`HKLM\SOFTWARE\Clients\StartMenuInternet` 读已注册的浏览器（名称和 `shell\open\command` 里的程序），新增 `internal/winapi/shell.Browsers()`，几次注册表读取，启动后在后台读一次并缓存，窗口每次显示时重新读（很便宜）。
- 搜索、网址、快速链接（网址类）的条目上，每个浏览器一个"用 Firefox 打开"操作（最多 5 个），是 `ActionOpen`，目标是浏览器程序，参数是加了引号的网址。
- 不判断哪个是默认浏览器（需要解析 UserChoice，不便宜也不稳定）；默认浏览器就是 `Enter`。

## 7. 搜索建议（默认关闭）

- 偏好 `suggestions`（默认**关**）。说明里写清楚：打开后，输入了搜索关键词时，关键词后面的文字会发送给对应的搜索引擎。
- 只在**输入了关键词**（`g golang gen`）并且该引擎支持建议时才请求；不带关键词的文字（包括兜底）永远不发送。支持的引擎：Google、YouTube（Google 的建议接口）、Bing、DuckDuckGo、百度、维基百科，都是公开的 OpenSearch 格式（`["查询", ["建议1", …]]`）。百度的回复如果不是 UTF-8，按 GBK 解码（`golang.org/x/text`）。自定义引擎可以填一个建议网址（同样的 OpenSearch 格式）。
- **防抖**：停止输入 250ms 后才请求（计时在按键的那一刻开始）；新的输入取消还没完成的请求（`context` 取消）；超时 2 秒；窗口隐藏时取消。请求在 `Host.Go` 里做，结果回来后放进一个小缓存（最近 32 个查询）并 `Host.Invalidate`，宿主重新拉取主搜索时从缓存里取。`RootItems` 本身从不等待网络。
- 建议条目排在关键词搜索条目下面（预打分 0.99 递减，最多 5 条），标题是建议的文字，副标题"用 Google 搜索"，不记入最近使用。
- 失败（网络错误、超时、格式不对）只记日志，不提示。
- glctl 的假环境里，建议的 HTTP 客户端一律失败，不会联网。

## 8. 偏好设置

| 名称 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `defaultEngine` | select（动态选项） | `""`（自动：中文 Bing，其他 Google） | 兜底用的搜索引擎 |
| `secondEngine` | select（动态选项） | `""`（无） | 第二个兜底搜索引擎 |
| `fallback` | bool | true | 没有好结果时提供网页搜索 |
| `openURLs` | bool | true | 输入网址时直接打开 |
| `suggestions` | bool | false | 搜索建议 |

搜索引擎的启用、关键词和自定义引擎不是偏好设置（偏好设置没有表格），在命令 **"搜索引擎"**（`engines`）里管理：列出所有引擎（名称、关键词、"默认""已停用"），操作有：用它搜索（推入参数）、启用/停用、编辑（表单：关键词，自定义引擎还有名称、网址模板、建议网址）、设为默认（写偏好 `defaultEngine`，用 `Result.Preferences`）、恢复默认（内置）、删除（自定义，红色，确认）、新建自定义搜索引擎、导入、导出。

## 9. 存储

插件 KV 里两个键，都是带 `version` 的 JSON，每次修改整体写回：

- `quicklinks`：`{"version": 1, "next": 3, "items": [{"id": "1", "name": …, "link": …, "keyword": …, "openWith": …, "created": …, "updated": …}]}`
- `engines`：`{"version": 1, "builtin": {"google": {"disabled": false, "keywords": ["g"]}}, "custom": [{"id": "c1", "name": …, "keywords": […], "url": …, "suggest": …}]}`

数据损坏时（JSON 解析失败）：记日志，当作空的，**不覆盖**原数据，直到用户做了修改。以后的格式升级按 `version` 迁移（[storage.md](storage.md)）。

## 10. AI

按 [ai.md §4.2、§16](ai.md) 的风险等级：

| 操作 | 种类 | 风险 |
|------|------|------|
| 搜索、打开网址、打开快速链接（无动态占位符）、用某浏览器打开 | `Open` | 打开（默认直接执行，可设成每次询问） |
| 复制网址、复制链接、复制为 JSON | `CopyText` | 打开 |
| 打开带占位符的快速链接、带参数的"用 Google 搜索" | `Callback` | 改动（每次询问） |
| 创建、编辑、复制一份快速链接，启用/停用、编辑引擎，设为默认，导入，导出 | 表单提交 / `Callback` | 改动 |
| 删除快速链接、删除自定义引擎 | `Callback` + `StyleDestructive` | 破坏性（总是询问） |
| 打开管理视图、表单 | `Push` | 只读 |

没有标 `ReadOnly` 的回调（没有只读的回调）。快速链接的链接、剪贴板内容不出现在任何条目里，所以不会给 AI 看到剪贴板。

## 11. 测试

- **模板展开和编码**：空格、`&`、`#`、`+`、`%`、`/`、中文、emoji、`:raw`、不认识的占位符、多个占位符、日期格式（固定时钟）、`{clipboard}`（假剪贴板）、路径和命令行里不编码、环境变量。
- **网址识别**：表格测试，正例和反例（§4 的每一条）。
- **关键词**：解析（大小写、空格、汉字关键词直接接内容、最长匹配、优先级）、和其他插件前缀不冲突、内置引擎之间不重复、创建时的查重。
- **快速链接**：增删改、复制一份、存储往返（新实例读回）、损坏的数据、导入导出往返、Raycast 格式、合并规则。
- **兜底**：默认引擎按语言、偏好、停用的引擎、第二个引擎；在 glctl 会话里和 AI 的"问 AI"一起出现，顺序固定。
- **搜索建议**：`httptest` 服务器（替换客户端的 Transport，不联网）：默认关闭时一个请求都不发；不带关键词不发；防抖（连续输入只发最后一个）；取消；超时；GBK；缓存后 `Invalidate`。
- **glctl golden**（假环境）：`g golang generics`、`bd 天气`、`github.com/foo`、只输入关键词、兜底（中英文）、会话：搜索后 `Enter` 打开的网址、创建快速链接再搜到它、"搜索引擎"视图。
- **i18n**：`en.json` 和 `zh-CN.json`，`glctl i18n check` 通过。
- 测试里不打开浏览器、不联网；真实网络的测试只在设置 `GOLAUNCHER_NET_TESTS=1` 时运行。

## 12. 以后可以做

- 快速链接的全局快捷键（需要宿主支持运行时的"动态命令"）；
- 离线可用时取网站图标（缓存在本地，默认不联网）；
- 从浏览器书签导入；
- 多个 `{argument name="…"}`。

## 13. 实现记录（2026-10-15）

代码在 `plugins/quicklinks`：`quicklinks.go`（插件主体、偏好、根搜索、兜底）、`engines.go`（内置引擎、关键词解析）、`template.go`（占位符、编码、链接类型、打开动作）、`urls.go`（网址识别）、`items.go`（条目和操作）、`views.go`（视图、表单、回调）、`store.go`（存储、导入导出）、`suggest.go`（搜索建议）、`locales/en.json`、`locales/zh-CN.json`。

### 宿主侧改动

- SDK：`plugin.Item` 新增 `Transient`（JSON `transient`）；`internal/core` 的 `Engine.Run` 不记录这样的条目（和兜底、情境条目一样）。测试 `TestTransientItemsNotRecorded`。
- `internal/winapi/shell`：新增 `Browsers()`（读 `StartMenuInternet`，HKCU 和 HKLM，按名称排序、按程序去重）和 `Browser` 类型；非 Windows 返回 nil。
- `internal/builtin`：`All` 和 `Fake` 各加一行，**注册在 `ai` 前面**（兜底顺序）。`Fake` 用 `fakeQuicklinks()`：一个假的浏览器（Firefox，标准安装路径）、建议的 HTTP 客户端一律失败（不联网）、同步读取浏览器。
- 文档：`plugin-guide.md`（示例表、`Item.Transient`、兜底的顺序）。

### 与设计的差异和补充

1. **中文关键词后面也必须有空格**（设计初稿里写的是可以直接接内容）：实现时发现 `百度网盘`、`有道云笔记`、`高德地图`、`谷歌浏览器` 这些常见应用名会被抢走，所以统一要求空格。`b站 猫` 可以，`b站猫` 不行。
2. **去掉了 `google`、`bing`、`baidu` 这三个英文全名关键词**（`google chrome`、`bing wallpaper` 是应用）；改为输入引擎名称的开头（至少 3 个字）时列出"用 Google 搜索"这条（不预打分，由宿主匹配）。
3. **网址识别**的细节（由并行的子代理实现，`urls_test.go` 约 130 个用例加模糊测试）：`cc` 也算"像扩展名的顶级域"（`main.cc` 不识别）；带用户名密码的网址一律不识别（`https://google.com@evil.com` 这种），包括写了协议的；顶级域列表比设计里的多一些（ly、gl、fm、im、gg、mx 等），故意不含 `id`、`to`（`user.id`、`path.to` 像代码）；保留输入的大小写（`GitHub.com` → `https://GitHub.com`）。
4. **自定义引擎的网址**：没有写协议时，只有顶级域在已知列表里才能补成 https（`cs.opensource.google/…` 要写 `https://`），否则表单提示"请输入带 {query} 的网址"。
5. **快速链接的 `{query}`**：在主搜索里输入"关键词 内容"时，如果链接只有 `{query}`，预打分的条目直接是 `ActionOpen`（风险等级"打开"）；从名称找到、在内联参数里填内容的，是回调（"改动"）。有 `{clipboard}`、`{date}` 的总是回调。
6. **"复制链接"**：没有占位符的复制展开后的网址或路径，有占位符的复制模板本身（展开需要执行时的值）。
7. **表单提交后回到主搜索**（和计时器的重命名一样，SDK 没有"返回上一层"）。
8. **导出**需要在表单里写文件路径（SDK 的 `file` 字段没有"另存为"对话框）；也可以用"复制为 JSON"。路径支持 `%USERPROFILE%` 这类环境变量。
9. **搜索建议**：维基百科的建议接口总是英文维基（中文界面搜索用中文维基）；建议失败不缓存，下次输入时重试。
10. **快速链接的全局快捷键和宿主别名**：宿主的别名和快捷键只能给清单里的命令，插件不能在运行时新增命令，这一期不支持；快速链接用自己的"关键词"代替别名。
11. **不预装快速链接**；不取网站图标（要联网），按链接类型用 `globe`、`folder`、`file`、`app`。

### 测试

- `template_test.go`：编码（空格、`&`、`#`、`+`、`%`、`/`、中文、emoji，且能解码回原文）、占位符展开（`{argument}`、大写、`:raw`、剪贴板、日期格式、路径里不编码、不认识的保留、没有 `{clipboard}` 时不读剪贴板）、链接类型、环境变量、打开方式、首页、关键词解析（含全角空格、`百度网盘`）、最长匹配和优先级、内置关键词互不重复且不占其他插件的前缀。
- `urls_test.go`：网址识别的正例（含返回值）和反例，`FuzzDetectURL`。
- `quicklinks_test.go`（hostfake，固定时钟、假剪贴板）：关键词搜索（含浏览器操作、百度的中文编码、只输入关键词时的参数、按名称、中文界面）、输入网址、兜底（按语言、选择的默认和第二个引擎、停用、关闭）、偏好的动态选项、快速链接增删改和复制一份（含查重、存储往返、ID 不复用）、占位符在执行时展开、引擎的停用/改关键词/自定义/设为默认/恢复默认/删除（含存储往返）、导出到文件再导入（往返一致、重复导入跳过、关键词冲突、Raycast 格式、错误时不改动）、损坏的存储（不覆盖、修改后写回）、视图和表单、图标。
- `suggest_test.go`（`httptest`，客户端的 Transport 把所有请求转到测试服务器）：默认关闭时一个请求都不发；不带关键词、不支持建议的引擎、兜底都不发；回答后 `Invalidate`、缓存、去掉和输入相同的建议；百度的 GBK 回答；防抖（连续输入只请求最后一个）；离开关键词、窗口隐藏时取消；超时；解析。真实网络的 `TestRealSuggestions` 只在 `GOLAUNCHER_NET_TESTS=1` 时运行。
- `internal/core`：`TestTransientItemsNotRecorded`。`internal/winapi/shell`：`TestCommandProgram`、`TestBrowsers`。
- `glctl` golden：`query_web_search`、`query_web_search_baidu`、`query_web_keyword`、`query_web_url`、`query_web_fallback`、`run_web_engines`、`zh_query_web_fallback`、`zh_query_web_search`，会话 `session_web_search`（`Enter` 打开的网址，且没有记入最近使用）、`session_quicklink_create`、`session_quicklink_open`。`session_ai_fallback` 里"用 Google 搜索"排在"问 AI"前面。新增的插件和命令改变了 `plugin_list`、`query_empty`、`zh_query_empty`、`query_context`、`run_external_command`、`session_confirm_done`、`session_ai_fallback`，已核对，全部只是新增的内容。
- `glctl i18n check`：0 个错误、0 个警告。

### 合并注意

- `internal/builtin/builtin.go` 的 `All` 和 `Fake` 各加了一行（在 `network` 和 `ai` 之间），和其他新插件分支可能有相邻改动的冲突，两边都保留。（R56 起兜底顺序由 `FallbackOrderer` 决定，与注册顺序无关。）
- 上面列出的 golden 会随其他新插件、命令变化，合并后运行 `go test ./internal/cli -update` 重新生成并检查。兜底条目会出现在任何"没有好结果"的查询的 golden 里。

### 待定

- 快速链接的全局快捷键（需要宿主支持动态命令）。
- 兜底条目的顺序（R56 起"问 AI"在前、网页搜索在后）由插件的 `FallbackOrderer` 决定；如果以后需要让用户调整兜底顺序，要宿主提供一个通用设置。
- "用某浏览器打开"只列出注册的浏览器，不区分哪个是默认浏览器。
## 14. 修订（2026-10-16，R56）

- **兜底顺序改为"问 AI"在前、网页搜索在后**（R56 的决定）。实现：
  - SDK（`pkg/plugin`）新增可选接口 `FallbackOrderer`（`FallbackOrder() int`）和常量 `FallbackOrderLast`（100）。宿主（`internal/core` 的 `fallbackEntries`）按 `FallbackOrder` 从小到大、相同时按注册顺序排列各插件的备选条目；没实现这个接口的插件是 0。
  - quicklinks 实现它，返回 `FallbackOrderLast`：网页搜索是什么都能接的兜底，排在所有其他备选条目（问 AI、以后其他插件的）之后。
  - 选这个做法而不是调换 `internal/builtin` 里的注册顺序：顺序是明确声明的，不会因为以后有人调整插件列表而悄悄改变；外部插件协议目前不支持备选条目（plugin-guide），以后支持时可以照用。
- `Tab` 不受影响（quicklinks 没有 `TabProvider`）。
- 测试：`internal/core` 的 `TestFallbackOrder`；`session_ai_fallback` golden 里只有两条兜底条目的顺序变了。