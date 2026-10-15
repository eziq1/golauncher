# 单位换算设计方案（计算器插件扩展）

> 状态：已实现（实现记录见 §6）· 设计 v1 2026-10-15 · 对应需求 R28（见 [requirements.md](requirements.md)）
>
> 你提出的类别：货币、面积、体积、重量、容量。问题：还应该包括哪些？

## 1. 澄清

- **体积和容量是同一个量纲**（升和立方米可以互换），合并成一类。
- 如果"容量"指的是**存储容量**（GB、TB），就归入"数据大小"。

## 2. 建议的范围

### 第一批（日常高频）

| 类别 | 例子 | 说明 |
|------|------|------|
| 货币 | USD、CNY、EUR、JPY… | 需要联网获取汇率，见 §3.4 |
| 面积 | m²、km²、ft²、公顷、亩 | 你提出的 |
| 体积 / 容量 | L、mL、m³、gal（美制/英制）、fl oz | 你提出的，合并为一类 |
| 重量 | kg、g、lb、oz、t、斤、两 | 你提出的 |
| 长度 | km、m、mi、ft、in、海里、尺、寸、里 | |
| 温度 | ℃、℉、K | 不是简单的乘法换算，要单独处理 |
| 数据大小 | B、KB/KiB、MB/MiB、GB、TB | 区分 1000 和 1024 进制 |
| 数据速率 | bps、Mbps、MB/s | |
| 速度 | km/h、mph、m/s、节 | |
| 时间长度 | s、min、h、天、周、年 | |
| 时间戳 | Unix 秒、毫秒 ↔ 日期 | 和世界时钟插件联动 |

### 第二批（专业或偶尔用）

压力（Pa、bar、psi、atm、mmHg）、能量（J、cal、kcal、kWh）、功率（W、kW、hp、PS）、角度、频率（Hz、rpm）、油耗（L/100km ↔ mpg，倒数换算）、力与扭矩、密度。

### 第三批（开发者和设计师常用，不是严格意义的单位）

屏幕和排版（px、pt、em、rem、dp）、颜色格式（HEX、RGB、HSL）、烹饪（cup、tbsp、tsp）、尺码（鞋码、衣服尺码，需要查表）。进制转换计算器已经支持。

## 3. 设计方向（待确认）

1. **并进计算器插件 `builtin.calc`**，扩展它的表达式语法：
   - `10 km in mi`、`3 斤 to kg`、`5 GB / 100 Mbps`、`100 usd in cny`；
   - 只给单位、不写目标时（比如 `30 ℃`），自动列出常用换算。
2. **按量纲做类型检查**：支持复合单位（`km/h`、`kWh`、`N·m`）；量纲不一致时拒绝换算，不返回结果。
3. **别名**：英文缩写、全称、中文名，以及拼音缩写。
4. **货币**：
   - 插件声明 `network` 权限，汇率缓存在本地，离线时用缓存，并显示"汇率更新于何时"；
   - 汇率来源选免费、不需要 API Key 的公开接口，**这一点需要你确认**。

## 4. 已确认的决定（2026-10-15）

| 项 | 决定 |
|----|------|
| 范围 | 第一、二、三批**一次全部实现** |
| 形态 | 并进计算器插件 `builtin.calc` |
| 类型检查 | 按量纲检查，支持复合单位 |
| 别名 | 支持英文缩写、全称、中文名、拼音缩写 |
| 货币 | 用联网的免费公开接口，**不需要 API Key** |

## 5. 详细设计

### 5.1 结构（`plugins/calc`）

- `units/`：单位库与量纲运算。
  - 量纲用一个指数向量表示：长度、质量、时间、电流、温度、物质的量、发光强度，另加数据量（bit）、货币、角度。
  - 每个单位记录：系数、偏移（温度用）、是否为倒数换算（油耗用）、别名列表。
  - 支持 SI 前缀（k、M、G、m、µ…）和二进制前缀（Ki、Mi、Gi…）。
- **语法扩展**：在现有的 Pratt 解析器里，数字后面可以跟单位，单位之间可以组合，比如 `km/h`、`kW·h`、`N*m`。目标单位用 `in`、`to`、`as`、`=>`、`换成`、`转换为` 引出，原有的进制写法 `255 in hex` 保持不变。
- **带单位的运算**：`5 GB / 100 Mbps` 结果是时间，`3 kg * 9.8 m/s^2` 结果是力，`1 mi + 200 m` 自动换算成同一单位。量纲不一致时报错，不返回结果。
- **不写目标单位时**：比如 `30 ℃`、`10 km`、`100 usd`，列出同一量纲下的常用单位（每类最多 5 个）。按偏好 `unitSystem`（公制或英制）排序，中国市制可以通过开关加入。

### 5.2 各类别的特殊处理

| 类别 | 特殊点 |
|------|--------|
| 温度 | 带偏移，不是纯比例换算。`30 ℃ in ℉` 是换算温度值；`Δ` 前缀（如 `Δ5 ℃`）表示温差，按纯比例换算 |
| 油耗 | L/100km 和 mpg 是倒数关系，单独处理 |
| 数据 | KB 按 1000、KiB 按 1024，`b`（bit）和 `B`（byte）大小写敏感 |
| 时间戳 | `1700000000 to date`、`1700000000000 ms to date`、`now in unix`、`2026-10-15 10:00 to unix`；时区按本地，也可以写 `in utc` |
| 屏幕和排版 | px、pt、em、rem、dp、in、cm 互换。偏好 `dpi`（默认 96）和 `remBase`（默认 16px） |
| 颜色 | `#ff8800 to rgb`、`rgb(255,136,0) in hsl`、`hsl(32,100%,50%) to hex`；结果条目带色块预览（在详情面板里） |
| 尺码 | 鞋码（中/美/欧/英，男女分开）、衣服尺码（中/美/欧），查表换算，例如 `US 9 shoe in EU`、`鞋码 42 in us` |
| 烹饪 | cup、tbsp、tsp（美制和公制），按体积处理 |
| 中国市制 | 斤、两、钱、亩、分、尺、寸、丈、里、升、斗 |

### 5.3 货币

- **数据源**（不需要 API Key）：
  1. 主用 `https://open.er-api.com/v6/latest/USD`（ExchangeRate-API 的开放接口，160 多种货币，每天更新）。它的条款要求注明数据来源，所以详情面板和设置页写明"Rates by Exchange Rate API"。
  2. 备用 Frankfurter（`api.frankfurter.app`，开源，数据来自欧洲央行，约 30 种货币）。
  3. 实现时先核实两者当前可用，并在文档里记录核实的日期。
- **缓存**：汇率存在插件的 `CacheDir`，超过 6 小时才用 `Host.Go` 在后台刷新；刷新失败时继续用旧缓存。结果的副标题标出"汇率更新于 …"。从来没有获取过汇率时（首次使用又离线），显示"暂时没有汇率"，不报错。
- **权限**：manifest 声明 `network`。偏好 `currencyProvider` 可以选数据源，也可以关闭货币换算。
- **别名**：ISO 代码；`$`、`¥`、`€`、`£` 这类符号（`¥` 默认表示人民币，可以用偏好改为日元）；中文名（美元、人民币、元、日元、欧元、英镑、港币、新台币、韩元…）。
- **不写目标货币时**：列出偏好 `currencies` 里的货币，默认是 USD、CNY、EUR、JPY、GBP、HKD。

### 5.4 结果条目

- **标题**：结果，比如 `6.2137 mi`；有效数字默认 6 位，可以通过偏好调整。
- **副标题**：完整的算式，比如 `10 km = 6.2137 mi`；货币结果额外标出汇率的时间。
- **动作**：

  | 动作 | 快捷键 |
  |------|--------|
  | 复制数值 | `Enter` |
  | 复制"数值 + 单位" | `Ctrl+Enter` |
  | 粘贴数值 | `Ctrl+Shift+V` |

- **详情面板**：同一量纲下的其他单位换算结果；颜色结果显示色块。

### 5.5 测试

- **单位库**：每一类表驱动，覆盖往返换算的精度、前缀、别名（中英文、拼音）、量纲不一致时报错、复合单位。
- **特殊类别**：温度的温差、油耗的倒数、时间戳和时区、颜色往返、尺码查表、排版和 DPI。
- **货币**：用 `httptest` 模拟接口；覆盖缓存过期、刷新失败时用旧缓存、首次离线、主接口失败时切换到备用接口。连真实网络的测试要显式开启（`GOLAUNCHER_NETWORK_TESTS=1`）。
- **回归**：计算器原有的测试和 golden 全部保持通过；新增 `glctl` golden（固定时间，汇率用 fixture）。

## 6. 实现记录（2026-10-15）

### 6.1 结构

- `plugins/calc/units`：单位库。`Dim` 是 10 维指数向量（长度、质量、时间、电流、温度、物质的量、发光强度、数据量、货币、角度）；`Unit` 记录显示符号、类别、系数（`big.Float`，由有理数一次舍入得到）、温度偏移、动态系数（px、rem/em 依赖偏好，货币依赖汇率）、是否倒数换算（油耗）、是否市制。`Table` 有两张表：大小写敏感的符号表和大小写不敏感的名称表（含多词短语如 `fl oz`、`nautical miles` 和拼音）。单位表用 `sync.OnceValue` 只建一次，`Init` 里用 `Host.Go` 在后台预热。
- `plugins/calc`：
  - `lexer.go`：识别 `℃ ℉ ° ′ ″`、货币符号、`²` `³`、`Δ`、`·`、`=>` `→` 和中文关键词（`换成` `转换为` `转换成` `换算成` `转成` `转为`，可以不加空格：`3斤换成kg`），以及含运算符的单位写法 `L/100km`、`lb-ft`。
  - `parser.go`：在原 Pratt 解析器上扩展。数字后面的单位绑定力 27（介于一元负号 25 和乘方 30 之间），所以 `5 GB / 100 Mbps` 是两个量相除，`-5 ℃` 是负 5 摄氏度，`2 m^2` 是 2 平方米。单位表达式（`km/h`、`m/s^2`、`kW·h`、`N*m`）单独解析：`/` 或 `*` 后面跟单位才继续，跟数字就结束。`5 ft 10 in`、`1 h 30 min` 是隐式相加。
  - `value.go`：带量纲的运算。加减要求量纲一致；乘除合并单位并按系数找命名单位（`kg·m/s²` → N，`km/h`）；体积/长度归为油耗，力×长度归为扭矩。
  - `eval.go`：从右往左找最后一个转换关键词，后面能解析成单位才算转换；`255 in hex` 走原来的进制路径。
  - `result.go`：结果条目、详情、"全部换算"视图；`rates.go`：汇率；`color.go`、`timestamp.go`、`sizes.go`：非线性的特殊换算，在通用解析之前用字符串匹配处理。
- `internal/lint`：内置插件现在可以 import 自己的子包（`plugins/calc/units`），仍然不能 import 其他插件或宿主内部包。
- `builtin.Fake`：`<fixtures>/rates.json`（open.er-api.com 的响应格式）存在时，计算器用它作为固定汇率；不存在时没有汇率，且绝不联网。fake 模式下时间戳用 UTC 显示，结果不依赖机器时区。

### 6.2 与设计的差异和补充决定

1. **详情面板**：根搜索目前不显示条目的 `Detail`（只有视图设置 `ShowDetail` 时才显示）。所以结果条目照样带 `Detail`（glctl 输出里能看到，以后根搜索加上详情面板就能直接用），另外加了动作 **Show All Conversions**（`Ctrl+Shift+A`），推入一个带详情面板的列表视图，列出同类的全部换算。
2. **色块**：选了 PNG 方案。第一次用到某个颜色时在 `CacheDir/swatches/rrggbb.png` 写一张 160×96 的纯色图，之后复用；写失败就不带图片。
3. **动作**：复制数值（`Enter`）、复制"数值 + 单位"（第二个动作，即 `Ctrl+Enter`）、粘贴数值（`Ctrl+Shift+V`）、全部换算。不带单位的纯数学结果，条目和动作保持原样（原有 golden 不变）。
4. **数据单位的大小写**：`b`（bit）和 `B`（byte）、`Mb` 和 `MB` 精确匹配时区分。全小写的 `kb`、`mb`、`gb`、`tb` 按字节理解（最常见的写法），比特要写 `Kb`、`Mb`、`kbit`、`megabits`。`kbps`/`Mbps` 是比特每秒，`MB/s`、`MBps` 是字节每秒。
5. **防误触**：只有一个字母的写法必须大小写完全匹配（`k` 不是开尔文，`4k` 没有结果）。`d` 只用来显示、不接受输入（`3d` 留给 3D 应用），"天"要写 `day`、`days`、`天`。没有数字、也没有转换关键词时一律不出结果（`mi`、`pt`、`km/h`、`usd`）；有关键词时没有数字按 1 处理（`km in mi`、`usd to cny`）。
6. **`pt`** 是排版的点（1/72 英寸），品脱写 `pint`。`em` 和 `rem` 相同（没有父元素的字号），都等于偏好 `remBase` 像素。`dp` 是 1/160 英寸，不受 DPI 影响。
7. **货币符号** `$ € £ ¥ ₩ ₹ ₽…` 只能写在数字前（`$100`），写在数字后是语法错误（原有测试 `1+2 $` 要求如此）；ISO 代码和中文名可以写在后面（`100 usd`、`100 元`）。`¥`、`￥` 默认是人民币，偏好 `yenMeansJPY` 可改为日元；`円` 总是日元。ISO 代码不区分大小写，和其他单位同名时让给其他单位（`cup` 是量杯，`CUP` 是古巴比索）。
8. **"度"** 不作为单位（角度和温度都常说"度"），用 `°`、`摄氏度`、`角度`；`度电` 是千瓦时。**"分"** 定为市制面积单位（1/10 亩）。
9. **温度运算**：`30 ℃ + 5 ℃ = 35 ℃`（右边当温差），`30 ℃ − 20 ℃ = Δ10 ℃`，`Δ5 ℃ in ℉ = Δ9 ℉`。带偏移的温度不能乘除、乘方（开尔文可以）。
10. **时间戳**：没写单位的数字 ≥ 10¹² 当毫秒。目标词：`date`、`time`、`datetime`、`iso`、`日期`、`时间`；`unix`、`timestamp`、`epoch`、`ts`、`时间戳`；`ms`；`utc`；`local`。日期写法：`2026-10-15`、`2026-10-15 10:00[:00]`、`2026/10/15 …`、RFC 3339，后面可以加 `utc`。时区默认本地（`Options.Location`，默认 `time.Local`）。
11. **尺码**：必须有类别词（`shoe`、`鞋`、`鞋码`、`男鞋`、`clothing`、`衣服`、`女装`…），否则 `us 9` 之类保持普通搜索。鞋码的 CN 列是脚长毫米（GB/T 3293.1），JP 列是厘米；`鞋码 42`、`CN 42` 这类小于 100 的数按欧码理解。衣服尺码表：男装 INT/US（胸围英寸）/EU/CN（号型），女装 INT/US/UK/EU/CN。
12. **正在输入**：`10 km in`（还没写目标）显示 `10 km` 的换算，不会闪烁消失。
13. **数字格式**：有效数字默认 6 位（偏好 1–30），小于 10¹⁵ 的整数精确显示，10⁻⁶ 到 10¹⁵ 之外用科学计数法；货币固定两位小数（小于 0.01 时按有效数字）。
14. **建议列表**：按偏好 `unitSystem` 的顺序取同类单位，但数值超过 10,000 或小于 0.001 的排到后面；数据大小、数据速率、时间按"数值离 10 最近"挑选；货币用偏好 `currencies` 的顺序。源单位是市制时自动列出市制单位，否则要打开 `chineseUnits`（默认关）。
15. 汇率失败后 5 分钟内不重试；刷新只在用到货币时触发，纯单位换算从不联网。

### 6.3 汇率接口核实

2026-10-15 用只读 GET 实测（同日 `GOLAUNCHER_NETWORK_TESTS=1 go test ./plugins/calc -run TestRealProviders` 也通过）：

| 接口 | 结果 |
|------|------|
| `https://open.er-api.com/v6/latest/USD` | 200，`application/json`，不需要 Key。响应：`{"result":"success","provider":…,"documentation":…,"terms_of_use":…,"time_last_update_unix":1792022551,"time_last_update_utc":"Thu, 15 Oct 2026 00:02:31 +0000","time_next_update_unix":…,"time_eol_unix":0,"base_code":"USD","rates":{"USD":1,"CNY":6.706494,"EUR":0.894425,"JPY":158.305497,…}}`，166 种货币，每天更新一次 |
| `https://api.frankfurter.app/latest?from=USD` | 301，跳转到 `https://api.frankfurter.dev/v1/latest?from=USD`（接口已迁移） |
| `https://api.frankfurter.dev/v1/latest?base=USD` | 200，不需要 Key，`Cache-Control: public, max-age=86400`。响应：`{"amount":1.0,"base":"USD","date":"2026-10-14","rates":{"CNY":6.6927,"EUR":0.89326,…}}`，29 种货币（不含 USD 本身，代码里补上 1），工作日更新 |

实现：主用 er-api，失败换 Frankfurter（偏好选 Frankfurter 时顺序相反，选 off 时关闭货币换算）；请求带 `User-Agent: golauncher/<版本>`，超时 10 秒，响应最多读 1 MiB。缓存在 `CacheDir/rates.json`，超过 6 小时或第一次用到货币时用 `Host.Go` 后台刷新，完成后 `Host.Invalidate("")` 让根搜索重新拉取。`RootItems` 只读内存和缓存文件，从不等网络。从来没拿到过汇率时显示"Exchange rates are not available yet"，刷新失败时提示检查网络。详情面板和设置页的说明里写明"Rates by Exchange Rate API"（带链接），用 Frankfurter 时写"Rates by Frankfurter (European Central Bank)"。

### 6.4 测量（AMD `go test -bench`）

| 输入 | `RootItems` 耗时 |
|------|------|
| `10 km in mi` | 35 µs |
| `30 ℃`（列出换算） | 24 µs |
| `100 usd`（6 种货币） | 37 µs |
| `5 GB / 100 Mbps` | 45 µs |
| `#ff8800`（含检查色块文件） | 66 µs |
| `1700000000 to date` | 11 µs |
| `1234 * 5`（纯数学） | 7 µs |
| `notepad`（不是算式） | 3.4 µs |

单位表建表 1.1 ms，只在启动后的后台做一次；共 459 个大小写敏感符号、1480 个名称/别名/拼音键。测试覆盖率：`plugins/calc` 96%，`plugins/calc/units` 99%。

### 6.5 支持的单位

前缀：SI `n µ(u) m c d h k M G T P E`（各单位只开放常用的组合），二进制 `Ki Mi Gi Ti Pi Ei`（数据）。每个单位都能用符号、英文全称（单复数）、中文名找到；中文名自动生成全拼（`gongli`）和两个字以上的首字母（`gl`），但不会覆盖已有的符号或英文名（`mi` 是英里，不是"米"）。

| 类别 | 单位 |
|------|------|
| 长度 | m、km、cm、mm、µm、nm、dm、in、ft、yd、mi、nmi、au、ly；市制 丈、尺、寸、里 |
| 屏幕和排版 | px（偏好 `dpi`）、pt、rem、em（偏好 `remBase`）、dp（sp、dip）；可和长度互换 |
| 面积 | m²、km²、cm²、mm²、ha、acre、ft²、in²、yd²、mi²；市制 亩、分、顷；也可写 `m^2`、`m2`、`sq ft`、`平方米` |
| 体积/容量 | L、mL、cL、dL、µL、hL、m³（方）、cm³（cc）、gal（美制）、imp gal、qt、pint、imp pint、fl oz、imp fl oz、ft³、in³、bbl；市制 升、斗 |
| 烹饪 | cup、tbsp、tsp（美制），metric cup、metric tbsp、metric tsp（公制 250/15/5 mL） |
| 质量 | g、kg、mg、µg、t、lb、oz、st、ct、gr、ozt（金衡盎司）、ton（短吨）、long ton；市制 斤、两、钱 |
| 温度 | ℃（°C、C、celsius、摄氏度）、℉、K；`Δ` 表示温差 |
| 数据大小 | B、KB…EB、KiB…EiB；b、Kb…Eb、Kib…Eib、Kbit/Mbit/Gbit/Tbit |
| 数据速率 | bps、kbps、Mbps、Gbps、Tbps、B/s、KB/s、MB/s、GB/s、MiB/s；任意 数据/时间 组合 |
| 速度 | km/h（kph）、m/s、mph、kn（节）、ft/s；任意 长度/时间 组合 |
| 时间长度 | s、ms、µs、ns、min、h、d（输入写 day/天）、wk、mo（平均月）、yr（格里历平均年 365.2425 天） |
| 压力 | Pa、hPa、kPa、MPa、GPa、bar、mbar、psi、atm、mmHg、inHg、Torr |
| 能量 | J、kJ、MJ、GJ、cal、kcal（大卡）、Wh、kWh（度电）、MWh、GWh、TWh、BTU、eV |
| 功率 | W、mW、kW、MW、GW、hp（英制马力）、PS（公制马力，"马力"）、BTU/h |
| 角度 | °（deg）、rad、grad、turn（rev、圈）、arcmin、arcsec；`sin(90°)` 等三角函数接受角度 |
| 频率 | Hz、kHz、MHz、GHz、THz、rpm、bpm |
| 油耗（倒数换算） | L/100km、km/L、mpg（美制）、mpg (UK) |
| 力 | N、kN、mN、kgf、lbf、dyn |
| 扭矩 | N·m（Nm、N*m）、lbf·ft（lb-ft、ft-lb）、kgf·m |
| 密度 | kg/m³、g/cm³、kg/L（g/mL）、lb/ft³ |
| 货币 | open.er-api.com 的 166 个 ISO 代码；符号 `$ € £ ¥ ￥ ₩ ₹ ₽ ฿ ₫ ₱ ₺ 円`；中英文名称：美元/美金、人民币/元/块、欧元、日元、英镑、港币/港元、新台币、韩元、澳元、加元、瑞郎、新加坡元、卢布、泰铢、澳门元、越南盾等 |
| 时间戳 | Unix 秒/毫秒/微秒/纳秒 ↔ 日期（本地或 UTC），`now` |
| 颜色 | HEX（`#rgb`、`#rrggbb`）、`rgb()`/`rgba()`（0–255 或百分比）、`hsl()`/`hsla()` |
| 尺码 | 男/女鞋：US、UK、EU、CN（mm）、JP（cm）；男/女装：INT（XS–XXXL）、US、UK（女装）、EU、CN（号型） |

### 6.6 测试

- `units/units_test.go`：量纲运算、有理数系数、符号/名称/中文/拼音查找（含大小写敏感和"拼音不覆盖英文"）、多词短语、全部前缀的系数、动态系数（DPI、remBase、汇率缺失）、建议列表与类别的一致性、命名单位匹配、**全部同量纲单位两两往返换算**（1.2345 往返后 30 位有效数字一致）。
- `convert_test.go`：按类别的表驱动换算（约 150 条，含中文关键词、拼音、复合单位、单位运算、温差、油耗倒数、排版）、量纲不一致不出结果、**防误触负例**（`code`、`notepad`、`mi`、`pt`、`tea`、`3d`、`4k`、`python 3`、`office 365`、`word to pdf`、`c++` 等约 100 个）、裸量的建议列表（公制/英制、市制）、偏好（有效数字、DPI、remBase、¥、关闭货币）、条目动作和详情、"全部换算"视图、基准测试。
- `special_test.go`：颜色（含 RGB→HSL→RGB 往返、色块 PNG 的像素）、时间戳（固定时钟，Asia/Shanghai 和 UTC）、尺码查表。
- `rates_test.go`：`httptest` 模拟两个接口：首次离线、失败后 5 分钟内不重试、刷新后缓存写盘并被新实例复用、超过 6 小时用旧缓存并后台刷新、刷新失败继续用旧缓存、主接口失败切换 Frankfurter、偏好 Frankfurter/关闭、各种坏响应、坏缓存文件、`Close` 能打断挂起的下载、`User-Agent`；`TestRealProviders` 只在 `GOLAUNCHER_NETWORK_TESTS=1` 时连真实网络。
- `glctl` golden：`query_unit`、`query_unit_bare`、`query_unit_expr`、`query_unit_zh`、`query_currency`、`query_currency_bare`（fixture 汇率）、`query_currency_no_rates`、`query_timestamp`、`query_now_unix`（固定 `--now`）、`query_color`（色块路径归一化为 `<cache>/swatches/…`）、`query_size`、`query_not_unit`；`plugin_list` 随 manifest 更新。原有计算器测试和 golden 全部不变地通过。
