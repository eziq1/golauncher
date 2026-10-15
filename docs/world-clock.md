# 世界时钟插件设计方案

> 状态：设计 v1 · 2026-10-15 · 对应需求 R23（见 [requirements.md](requirements.md)）
>
> 需求：输入一个前缀（比如 `time`），列出全球时间：
> 1. 显示我当前的时间；
> 2. 列出几个主要城市的时间；
> 3. 支持搜索，比如 `time beijing`、`time bj`（模糊搜索），列出北京当前的时间。
>
> 重要的一点：时间要是动态的，利用插件的动态特性。

## 1. 定位

内置插件 `builtin.clock`（`plugins/clock`），纯 Go 实现，不依赖外部软件。它同时演示 M5 的两种动态能力：

- **根搜索**用 `Host.Invalidate`（通知后再拉取）：每秒通知一次，宿主重新拉取，时间就会跳动；
- **"World Clock"视图**用 `Host.Render`（推送快照）：每秒推送一份新视图。

## 2. 交互

### 2.1 根搜索（主要入口）

| 输入 | 结果 |
|------|------|
| `time` | 第一条是**本地时间**（时间、日期、时区名、UTC 偏移）；接着是**收藏的城市**，没有收藏时显示默认的主要城市列表 |
| `time beijing`、`time bj`、`time 北京`、`time bei` | 模糊匹配城市：英文名、中文名、拼音及其首字母、常用缩写（`nyc`、`sf`、`la`、`hk`）、国家名、IANA 时区 ID（`Asia/Shanghai`） |
| `time utc+8`、`time pst`、`time cet` | 按 UTC 偏移或时区缩写匹配，列出对应的城市 |

- **前缀**：偏好项 `prefixes`（stringList），默认 `["time", "tz"]`，也可以加中文的 `时间`。不带前缀的输入，插件一律不返回结果，不会干扰普通搜索。
- **排序**：插件自己做匹配并给出 `Score`，根搜索的模糊匹配只在宿主侧生效，而这里的输入带着前缀，宿主无法直接匹配。带前缀时，时钟结果排在根搜索最前面，分组标题为"World Clock"。
- **默认城市**：北京、东京、新加坡、孟买（新德里）、迪拜、莫斯科、伦敦、巴黎、柏林、纽约、旧金山（洛杉矶）、悉尼。通过偏好项 `cities` 可以改，收藏的城市排在前面。

### 2.2 每一条的内容

- **标题**：城市名加时间，例如 `Beijing  14:32:05`。显示秒，好让用户看出它在走。
- **副标题**：`Thursday, Oct 15 · UTC+8 · +15h`。最后一项是和本地时间的差；跨日时在附注里标出"Tomorrow"或"Yesterday"。
- **附注**：白天显示 ☀，夜里（当地 20:00–6:00）显示 ☾，同时写出相对日期。
- **图标**：内置的 `clock` 图标，界面那边要新增这个 SVG。
- **详情面板**：完整日期、IANA 时区、UTC 偏移、是否处于夏令时、下一次夏令时切换的时间（如果有）。

### 2.3 动作

| 动作 | 快捷键 | 说明 |
|------|--------|------|
| 复制时间 | `Enter` | 例如 `14:32 Beijing (UTC+8)` |
| 复制 ISO 8601 | `Ctrl+Enter` | 例如 `2026-10-15T14:32:05+08:00` |
| 收藏 / 取消收藏 | `Ctrl+Shift+P` | 存进插件的 KV。收藏的城市排在默认列表前面 |
| 打开世界时钟视图 | — | 打开 §2.4 的视图 |

### 2.4 "World Clock"视图（命令）

- 列表显示本地时间、收藏的城市和默认城市，搜索框可以过滤。
- 用 `Host.Render` 每秒推送一份新快照，选中项保持不变。

## 3. 动态刷新的实现

**根搜索（Invalidate）**：

- `RootItems` 被调用、并且输入带前缀时，插件记下"正在显示"，然后通过 `Host.Go` 启动一个对齐到整秒的计时器，每秒调用 `Host.Invalidate("")`。宿主重新拉取时，又会调用 `RootItems`，于是刷新就持续下去。
- **停止条件**（避免空转）：
  - 下一次 `RootItems` 收到的输入不带前缀；
  - 收到新增的宿主事件 `window-hidden`：SDK 新增常量 `EventWindowHidden`，shell 在隐藏启动器时发布；
  - 超过 2 秒没有被重新拉取，作为兜底。
- 宿主侧会合并 50ms 内的通知，并且刷新后保持选中项，所以每秒刷新一次既不闪烁，选中的条目也不会跳动。

**视图（Render）**：

- `View` 返回的视图带着实例 ID。插件为每个打开的视图实例启动一个每秒推送的计时器；视图关闭、或者 `Close` 时停止。进程内插件收不到视图关闭的通知，所以推送如果连续 3 秒没有被宿主重新拉取，也作为停止条件。实现时可以先查一下：引擎能否把 `OnViewClosed` 也通知给进程内插件（通过可选接口 `ViewCloser`）。如果改动很小，就一并加上。

**时钟来源**：所有时间都通过 `Host.Now()` 取得。这样测试和 `glctl --now` 可以固定时间，golden 测试保持确定。

## 4. 时区数据

- 用 Go 的 `time.LoadLocation`。**必须导入 `time/tzdata`**，把时区数据库编进二进制（约 450KB）。Windows 上没装 Go 的用户机器里没有 zoneinfo，不嵌入的话这个插件在那里会完全无法使用。
- 城市数据 `cities.json` 嵌入二进制，约 150 个主要城市。每个城市有：英文名、中文名、国家（英文和中文）、IANA 时区、别名和缩写。拼音在启动时用 `go-pinyin` 生成，同时生成全拼和首字母。
- 时区缩写（`PST`、`CET` 等）有歧义，只支持一份常用的固定映射表，并在文档中说明。

## 5. 偏好项

| 名称 | 类型 | 默认值 |
|------|------|--------|
| `prefixes` | stringList | `["time", "tz"]` |
| `cities` | stringList | §2.1 的默认城市 |
| `hour24` | bool | true（24 小时制） |
| `showSeconds` | bool | true |

## 6. 测试

- **城市匹配**：`bj`、`beijing`、`北京`、`bei`、`nyc`、`sf`、`utc+8`、`pst`、`Asia/Tokyo`。不带前缀时不返回结果。
- **时间格式**：12/24 小时制、跨日（Tomorrow / Yesterday）、夏令时（以纽约在三月切换前后为例）、半小时时区（印度 +5:30）、45 分钟时区（尼泊尔 +5:45）。
- **动态刷新**：
  - 用 hostfake 推进时钟，检查 `Invalidated()` 每秒增长；
  - 输入不带前缀、收到 window-hidden、超时无人拉取，这三种情况下停止刷新；
  - 视图的 `Renders()` 每秒增长，`Close` 后停止。
- **`glctl` golden**：在固定时间 `2026-10-15T10:00:00Z` 下，测试 `time`、`time bj`、`time 北京`。
- **Lint**：不允许直接使用 `go` 语句，所有后台任务必须走 `Host.Go`。

## 7. 以后可以做

- 时间换算：`time 3pm beijing in ny`、`time 15:00 tokyo`。
- 在根搜索里直接显示"会议时间建议"：多个城市都处在工作时间的时段。

## 8. 实现记录（2026-10-15）

代码在 `plugins/clock`：`clock.go`（插件主体、列表、视图、动作）、`cities.go`（城市数据、索引、匹配、时区缩写表）、`format.go`（时间格式化）、`refresh.go`（刷新循环）、`cities.json`（185 个城市，含 UTC）、`locales/en.json`（星期、月份名和日期格式也是消息，以后可以翻译）。

### 宿主侧改动

- SDK：新增常量 `plugin.EventWindowHidden = "window-hidden"`；新增可选接口 `plugin.ViewCloser { ViewClosed(instance string) }`。
- `internal/shell`：`panel.OnHide` 里发布 `window-hidden`。MyGo 的 `Window.Hide` 只在窗口确实从可见变为隐藏时触发 `OnHide`，所以 Esc、失焦、热键切换、动作执行后隐藏、打开设置等所有途径，每次隐藏只发布一次。
- `internal/core/engine.go`：原来对 `interface{ ViewClosed(string) }` 的断言改成 `plugin.ViewCloser`，行为不变。核实过：这条转发本来就覆盖进程内插件，不需要别的改动。
- `pkg/plugin/rpcserve`：收到 `viewClosed` 通知时转发给实现了 `ViewCloser` 的插件（之前直接忽略）。外部插件订阅 `window-hidden` 走 `host/subscribe` 的通用事件转发，不需要改。
- `internal/ui/icons.go`：新增 `clock` 图标；`internal/builtin`：`All` 和 `Fake` 注册插件。

### 与设计的差异

1. **视图的兜底停止条件**：设计里写的"推送连续 3 秒没有被重新拉取"不可行。视图只在用户输入时才会被拉取，按这个条件，用户不打字时视图 3 秒后就停了。既然 `OnViewClosed` 能通知进程内插件，就改为三个停止条件：`ViewClosed`、`window-hidden`、`Close`。启动器再次显示时会 `Reset`，关闭所有视图帧（同时触发 `ViewClosed`）；某个视图如果又被拉取，拉取时会重新启动推送。
2. **根搜索"2 秒无拉取"按 tick 计数**：连续两个 tick 之间都没有被拉取就停止，不依赖 `Host.Now` 是否在走，所以 `glctl --now` 固定时钟时也能停。实际停止时刻是最后一次拉取之后 2～3 秒。清空输入后宿主不再调用 `RootItems`（空查询不问插件），就靠这一条停止。
3. **复制动作用 Callback**，而不是内置的 `CopyText`，执行时按 `Host.Now()` 重新计算时间。原因：执行过的条目会被记进"最近使用"（存的是条目快照），用 `CopyText` 的话，从"最近使用"里执行会复制当时冻结的旧时间。代价是复制由插件通过 `Host.Clipboard` 完成（声明了 `clipboard` 权限），`glctl` 的 effects 里没有 `CopyText` 记录，Toast 由插件给出。
4. **视图不用 `FilterLocally`**：视图里的搜索要覆盖全部城市，而不只是正在显示的收藏和默认城市，所以 `View` 每次按输入自己匹配，推送循环记住每个实例最近一次的搜索文字。插件在同一把锁下更新搜索文字和渲染，拉取之后发出的推送一定用新的文字。
5. **默认城市**：设计里的"孟买（新德里）""旧金山（洛杉矶）"分别取孟买和旧金山。收藏的城市排在默认城市之前，两者都显示；视图里收藏单独一个分组。
6. **时区缩写表**（固定）：美国时区的 `pt`/`pst`/`pdt` 三种写法等价（`mt`、`ct`、`et`、`at`、`akt`、`nt` 同理）；`cst` 同时列出美国中部时间和中国标准时间（北京、台北、澳门、香港）；`ist` 指印度；`gmt` 指 UTC 加上英国、爱尔兰、西非、冰岛；另有 `bst wet west cet cest eet eest msk trt gst pkt npt ict wib sgt myt pht hkt jst kst aest aedt acst acdt awst nzst nzdt hst sast eat wat brt art`。
7. **匹配细节**：
   - 拼音首字母只算完全匹配（`bj` → 北京），不算前缀和子串。外国城市音译名的首字母（班加罗尔 `bjle`）会让 `bj` 匹配到一堆不相关的城市。
   - 子序列模糊匹配要求至少 4 个字符并且首字母相同，避免 `pst` 匹配到 Budapest。
   - UTC 偏移按城市**当前**的偏移匹配（10 月的 `utc-3` 包括夏令时中的哈利法克斯）。
   - 同分时，收藏和偏好里的城市排在前面，其次按 `cities.json` 的顺序（大城市在前），所以 `sz` 先深圳后苏州。
8. **打分**：不带搜索词时，本地时间 `Score = 1`，其余从 0.55 开始每条减 0.001。"最近使用"加分最多是 0.3，而匹配分的权重是 0.7，这样任何城市都排不到本地时间前面。带搜索词时按名次给 `1 − 0.005 × 名次`。时钟结果是预打分的（排名 +1），所以带前缀时总在其他结果之前。
9. **显示**：附注只在跨日时写 Tomorrow / Yesterday，收藏的城市前面加 ★。本地时间的副标题里，时区名和偏移相同（比如 UTC）时省略时区名。Windows 上 `time.Local` 没有 IANA 名，显示缩写。详情的"下一次切换"写的是切换前的当地时间，比如 `Sunday, November 1, 2026 02:00 → UTC-5`。
10. **偏好 `cities`** 接受英文名、中文名、别名和 IANA 时区。城市表里没有、但合法的 IANA 时区会生成一个城市，以时区 ID 的最后一段命名，比如 `America/Boise` 显示为 Boise。
11. **前缀**：前缀以非 ASCII 字符结尾（比如 `时间`）时，后面可以直接接城市名（`时间北京`）；ASCII 前缀后面必须是空格或结尾，`timer` 不会触发。
12. 城市索引（拼音、时区加载）在第一次使用时构建，`Init` 里用 `Host.Go` 预热。

### 测量

- **二进制大小**（`go build -ldflags "-s -w"`，windows/amd64）：21,153,280 → 21,894,656 字节，增加约 741 KB。其中 `time/tzdata` 约 412 KB（不导入时为 21,483,008 字节），插件代码和城市数据约 330 KB。
- **`RootItems` 延迟**（`go test -bench RootItems ./plugins/clock`）：`time` 0.24ms（13 条），`time b` 0.53ms（30 条），`time bj` 0.04ms，`time utc+8` 0.40ms。主要耗时在逐条格式化（模板消息）。`glctl bench` 测的端到端根搜索 p95 约 1ms。

### 测试

- `clock_test.go`：manifest、城市数据（≥150、默认城市都能找到）、无前缀不返回、列表顺序与分数约束、收藏、`cities` 偏好、自定义前缀、约 45 个匹配用例、`parseOffset`、复制动作（执行时取时间、12 小时制）。
- `format_test.go`：12/24 小时制、+5:30 / +5:45 / −2:30 / +3:30 偏移、时差、纽约 2026-03-08 夏令时切换前后（含下一次切换）、详情、日期变更线（Kiritimati 明天、Pago Pago 昨天、从 Pago Pago 看 Apia +24h）、跨年。
- `refresh_test.go`（hostfake + 注入的计时器，推进假时钟，没有真实等待）：每秒一次 `Invalidate`、对齐到整秒；三种停止条件（无前缀、`window-hidden`、无拉取）和 `Close`；视图每秒 `Render`、搜索文字跟随、`ViewClosed` / `window-hidden` / `Close` 后停止、重新拉取后恢复。
- `host_test.go`：接真实的 Engine 和 Controller，验证根列表和视图在界面快照里跳动，按 Esc 关闭视图后（经 `OnViewClosed` → `ViewCloser`）推送停止，清空输入后根刷新停止。
- `glctl` golden：`query_clock`、`query_clock_bj`、`query_clock_zh`（`--now 2026-10-15T10:00:00Z`）。

### 合并注意

- `internal/builtin/builtin.go` 的 `All` 和 `Fake` 各加了一行，和 Scoop 分支会有相邻改动的冲突，两边都保留即可。
- 新增的 World Clock 命令改变了 golden `plugin_list`、`query_empty`、`session_confirm_done`、`run_external_command`。合并后运行 `go test ./internal/cli -update` 重新生成并检查。
- 遗留问题（宿主层面）："最近使用"保存的是条目快照，时钟条目在那里的标题是记录时的时间。动作本身不受影响（见差异 3）。以后可以让插件重新解析最近使用的条目，或者允许条目声明不记入最近使用。
