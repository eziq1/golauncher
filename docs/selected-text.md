# 读取选中文字设计方案

> 状态：已实现并发布（实现记录见 §13）· 设计 v1 · 2026-10-16 · 对应需求 R64（来自 R63，见 [requirements.md](requirements.md)）
>
> 需求：在任意应用（微信、浏览器、编辑器……）里选中一段文字，再按 Alt+Space 打开启动器，golauncher 要知道选中了什么，并提供针对它的操作：翻译、问 AI（解释、总结、改写、回复建议）、网页搜索、是网址就打开、是算式就计算……
>
> 本文是 [context-awareness.md](context-awareness.md) 第二阶段“选中文字（复制到剪贴板再还原，需要单独评估）”的单独评估。下文的应用名、窗口内容都是虚构示例。

## 1. 总体思路

```
按下 Alt+Space（WM_HOTKEY，此时 Alt、Space 往往还按着）
  └─ ① 记下前台窗口（已有：prevForeground）
  └─ ② 【同步，≤50ms】UI Automation 取焦点元素：是不是本进程、是不是密码框、有没有 TextPattern
  │      （只有这一步必须在启动器抢走焦点之前做）
  └─ ③ 显示启动器（不等选中文字）
  └─ ④ 【异步，≤250ms】从②拿到的元素读选中范围和文字 → Selection
         └─ ⑤ 宿主把 Selection 交给有权限的插件（SelectionProvider）→ 各插件返回自己的动作
              └─ ⑥ 根列表最上方出现“选中的文字”分组（晚到时按 R26/R27 的规则刷新）

剪贴板方式（模拟 Ctrl+C）只在两种情况下用，且发生在 ③ 之前（§3.3）：
  · 专用快捷键“对选中文字打开启动器”（用户明确要处理选中文字，可以接受 ~100–250ms 的延迟）；
  · 前台应用在“使用剪贴板方式的应用”列表里（例如实测后确认 UIA 读不到的聊天软件）。
```

分工沿用环境感知：**宿主负责“拿到文字”**（`internal/winapi/selection`、`internal/core`），**插件负责“针对文字的操作”**（新接口 `SelectionProvider`）。插件之间不能互相调用（[m5-plugin-platform.md](m5-plugin-platform.md)“插件之间怎么交互”），所以翻译/问 AI 由 `builtin.ai` 提供，搜索/打开网址由 `builtin.quicklinks` 提供，计算由 `builtin.calc` 提供，各自读自己的设置（默认搜索引擎、AI 开关），宿主只负责汇总成一个分组。

## 2. 获取选中文字的方式

### 2.1 UI Automation（TextPattern）

做法（纯 Go COM vtable，和 `internal/winapi/context` 的资源管理器识别器同样的写法）：

1. `CoCreateInstance(CLSID_CUIAutomation8)` → `IUIAutomation`；有 `IUIAutomation2` 时把 `ConnectionTimeout` / `TransactionTimeout` 设成 200ms，目标程序卡住时不会把我们拖住。
2. `GetFocusedElement` → 元素。`CurrentProcessId` 是本进程时丢弃（启动器已经抢到焦点了，说明晚了）。
3. `CurrentIsPassword`（`UIA_IsPasswordPropertyId`）为真 → **结束，什么都不读**。
4. `GetCurrentPatternAs(UIA_TextPattern2Id)`，没有再试 `UIA_TextPatternId` → `IUIAutomationTextPattern::GetSelection` → `IUIAutomationTextRangeArray`，逐个 `GetText(maxChars)`，多段选区用换行连接。
5. 可编辑性：`ValuePattern.IsReadOnly` 或 `ControlType == Edit` → `Selection.Editable`，决定“替换选中的文字”是否出现（§6）。

线程：UIA 客户端调用放在**专用的、锁定的 OS 线程上，MTA**；绝不在 MyGo 主线程上调用（UIA 访问到本进程窗口时会死锁），这也是第 2 步检查进程号的原因之一。

| 优点 | 缺点 |
|------|------|
| 没有任何副作用：不碰剪贴板、不发按键 | 只对实现了 TextPattern 的控件有效 |
| 能分辨“支持但没选中”和“不支持”，前者可以直接下结论“没有选中文字”，不用再试 Ctrl+C | 自绘界面（很多 IM、游戏、部分 Qt/DirectUI 程序）读不到 |
| 能识别密码框（`IsPassword`） | 管理员权限的窗口读不到（UIPI，本进程是中完整性级别） |
| 能知道文字是否可编辑 | Chromium/Electron 第一次被 UIA 查询时才打开无障碍树，首次可能拿不到（需重试一次），并且之后该浏览器进程一直开着无障碍模式，有一定 CPU/内存开销（需实测） |

已知支持情况（**“需实测”的项目实现前要逐个测，结论只记“支持/不支持/耗时”，不记本机内容**）：

| 应用类型 | UIA TextPattern 选区 | 说明 |
|----------|----------------------|------|
| Win32 Edit / RichEdit（记事本、写字板、大多数对话框） | 支持 | 系统的 MSAA→UIA 代理提供 TextPattern |
| WPF、UWP/WinUI 文本控件 | 支持 | 框架自带 |
| Office（Word、Outlook 正文） | 支持 | Word 提供原生 UIA；Excel 单元格选区不是文字，见 §2.2 |
| Edge、Chrome（网页正文和输入框） | 支持（需实测首次延迟） | Chromium 原生 UIA；首次查询会触发无障碍树构建 |
| Firefox | 需实测 | 有 UIA 支持，TextPattern 覆盖程度不确定 |
| Electron 应用（VS Code、各种基于 Electron 的 IM/文档工具） | 多数不支持 | 默认不开无障碍；VS Code 编辑器只有在“屏幕阅读器模式”下才把选区同步到隐藏的 textarea |
| Windows Terminal、conhost | 支持 | 两者都实现了 TextPattern（给屏幕阅读器用），**这正是终端只能用 UIA、不能用 Ctrl+C 的原因** |
| Qt 程序 | 标准控件支持，自绘控件不支持 | Qt 5/6 自带 UIA 后端，`QLineEdit`/`QTextEdit`/QML 文本组件实现了文本接口；自绘的气泡、列表不会有 |
| 微信 4.x（`Weixin.exe`） | **需实测** | 公开信息显示 4.x 是基于 Qt 重写的跨平台界面。输入框可能可读；聊天气泡里的部分选中是否通过 UIA 暴露未知 |
| 微信 3.x（`WeChat.exe`） | **需实测**，预计不支持 | 自绘（DirectUI 类）界面。第三方自动化库能通过 UIA 读到消息列表项的名称，但这不等于能读到气泡里的选区 |
| PDF 阅读器 | 需实测 | 各家差异大 |
| 游戏、全屏程序 | 不支持 | |

### 2.2 模拟 Ctrl+C（剪贴板方式）

流程（全部在显示启动器之前，目标窗口还在前台）：

| 步骤 | 做法 | 失败或超限时 |
|------|------|--------------|
| 0. 先决条件 | 应用不在排除列表、不在“禁止 Ctrl+C”内置名单（§4.3）；目标进程完整性级别不高于本进程（`GetTokenInformation(TokenIntegrityLevel)`，否则 `SendInput` 会被 UIPI 静默拦下）；如果 §2.1 已拿到元素：不是密码框、也不是“支持 TextPattern 但没有选区” | 不用剪贴板方式 |
| 1. 保存剪贴板 | `EnumClipboardFormats` 按原顺序复制所有 `HGLOBAL` 格式的数据；跳过系统合成的格式（有 `CF_UNICODETEXT` 时的 `CF_TEXT`/`CF_OEMTEXT`/`CF_LOCALE`，有 DIB 时的 `CF_BITMAP`）和非 `HGLOBAL` 的句柄格式（`CF_BITMAP`、`CF_ENHMETAFILE`、`CF_METAFILEPICT`、`CF_PALETTE`、`CF_OWNERDISPLAY`）；总量上限 16MB，保存耗时上限 50ms | 超限、或剪贴板里有保存不了的格式（无法原样还原）→ **放弃剪贴板方式**，不发 Ctrl+C |
| 2. 标记 | 记下序号 `S0 = GetClipboardSequenceNumber()`，打开“抑制”：本进程的剪贴板监视器忽略从 `S0` 起到还原结束后 debounce 期满的所有变化（`clip.Watch` 的 `ownWrite` 过滤扩展为序号区间） | |
| 3. 发送按键 | 先等用户松开 Alt/Space/Shift/Win，最多 100ms；还按着就用 `PasteTo` 同样的顺序：**先按下 Ctrl，再注入仍按着的 Alt/Shift/Win 的抬起**（Ctrl 在先，松开 Alt 不会激活菜单栏；Shift 必须松开，否则 Chrome 里 Ctrl+Shift+C 是开发者工具），然后 C 按下/抬起、Ctrl 抬起 | `SendInput` 返回不足 → 补发 C、Ctrl 的抬起，放弃 |
| 4. 等待 | 剪贴板序号变化，最多 200ms（用一个临时的 `AddClipboardFormatListener` 消息窗口，不轮询）；变化后再等 20ms 内不再变化（有的程序一次复制写好几次） | 超时 → 当作“没有选中文字”；剪贴板没被动过，不需要还原 |
| 5. 读取 | 只读 `CF_UNICODETEXT`（`clip.ReadWith` 不请求图片，避免触发延迟渲染）；截到最大长度 | 没有文字（例如复制的是文件）→ 当作没有选中文字，照常还原 |
| 6. 还原 | 只有当序号仍等于第 4 步看到的值时才还原（中间有别的程序或用户写过剪贴板就不动它）；按原顺序写回所有格式，**并加上** `ExcludeClipboardContentFromMonitorProcessing`、`CanIncludeInClipboardHistory=0`、`CanUploadToCloudClipboard=0`（原内容在第一次复制时已经被记录过，还原不应再生成一条）；原来剪贴板为空就 `EmptyClipboard` | 还原失败 → 日志（不含内容）+ 一次通知“剪贴板未能还原” |
| 7. 结束 | debounce（50ms）期满后关闭抑制 | |

已知的坑：

| 情况 | 问题 | 处理 |
|------|------|------|
| 终端（Windows Terminal、conhost、各类终端模拟器） | 没有选区时 Ctrl+C 是中断（SIGINT），会打断正在运行的程序 | 内置禁止名单，**永远不发 Ctrl+C**；终端走 UIA（§2.1 表中两者都支持） |
| VS Code、JetBrains 等编辑器 | 没有选区时 Ctrl+C 复制整行 | 会把“当前行”误当成选中文字。UIA 能读时不走剪贴板；否则列入默认的“不要用剪贴板方式”名单，用户可自行移除 |
| Excel | 复制单元格会出现虚线框（复制状态），还原后虚线框消失，原来“在 Excel 内部粘贴公式/格式”的能力丢失；单元格数据还是延迟渲染的 | 内置禁止名单 |
| Office 等延迟渲染的数据 | 保存剪贴板时 `GetClipboardData` 会强制所有者渲染，可能很慢 | 第 1 步 50ms 上限；剪贴板所有者是已知的延迟渲染程序时直接放弃剪贴板方式 |
| 还原后的所有者变成了我们 | 依赖“所有者是自己”的程序（同上 Excel）粘贴行为会变 | 同上，接受；在设置说明里写明 |
| 第三方剪贴板管理器 | 目标程序写入的内容不带排除标记，它们会记录下来 | 无法阻止；设置说明里写明 |
| Windows 剪贴板历史（Win+V）和云剪贴板 | 同上：写入的是目标程序，不是我们，没法事先加标记 | 第二期：用 WinRT `Clipboard.GetHistoryItemsAsync` / `DeleteItemFromHistory` 删掉刚才那一条（需实测：桌面程序能否调用、时机、能否删掉已上传到云端的那一份）。设置页检测 `HKCU\Software\Microsoft\Clipboard\EnableClipboardHistory` 和云剪贴板开关，开着时在剪贴板方式旁边显示提醒 |
| 游戏、全屏程序 | Ctrl+C 可能是游戏按键 | 前台窗口是全屏独占或不在白名单时不用（只对列表里的应用和专用快捷键使用，本来就不会打到游戏） |
| 密码框 | 多数密码框禁止复制，但不能依赖 | UIA 能拿到元素时先查 `IsPassword`；拿不到时只靠应用排除名单 |
| 管理员权限窗口 | `SendInput` 被 UIPI 拦下，且不报错 | 第 0 步检查完整性级别，直接跳过 |
| 我们自己的剪贴板历史 | 会记下临时内容 | 第 2 步的序号区间抑制；还原内容带排除标记，`plugins/clipboard` 的 `c.Exclude` 已经会跳过 |

### 2.3 对比与推荐

| | UIA | 剪贴板方式 | 混合（推荐） |
|---|---|---|---|
| 覆盖面 | 标准控件、浏览器、Office、终端 | 几乎所有支持复制的程序 | 两者之和 |
| 副作用 | 无 | 改写并还原剪贴板；Win+V/云剪贴板/第三方管理器可能记下；可能误触应用的 Ctrl+C 含义 | 默认无，只在用户开启的场合有 |
| 对弹出速度的影响 | 同步部分 ≤50ms（目标 <15ms，需实测） | 弹出推迟 ~50–250ms | Alt+Space 不受影响；专用快捷键和名单内应用会推迟 |
| 能否识别密码框 | 能 | 不能 | 能（先 UIA） |

**推荐：混合方式，UIA 为主，剪贴板方式默认关闭、按需开启：**

1. Alt+Space：**只用 UIA**。没有副作用、不拖慢弹出，默认开启（随“情境感知”）。
2. 剪贴板方式默认关闭。打开后只用于：
   - 专用快捷键“对选中文字打开启动器”（默认不设，用户自己录一个）；
   - “使用剪贴板方式的应用”名单里的应用（默认空；如果实测证明微信读不到，在设置说明里举例建议加入 `Weixin.exe`，但不默认加入）。
3. 两种情况下都**先试 UIA**：UIA 明确说“没选中”或“是密码框”时不发 Ctrl+C。
4. 终端、Excel 等内置禁止名单永远不发 Ctrl+C。

不选“每次 Alt+Space 都先 Ctrl+C”：每次呼出都改写剪贴板、推迟弹出、在 VS Code 里误取整行、让 Win+V 多出条目，而大部分时候用户根本没打算处理选中文字（浏览器里残留的选区很常见）。

不选“后台持续监听焦点变化（UIA FocusChanged 事件）来提前拿到焦点元素”：违背环境感知“只在呼出时获取一次，不在后台持续监视其他窗口”的原则，并且会让所有 Chromium 程序一直开着无障碍模式。

## 3. 时机与延迟

### 3.1 为什么要分两段

- 焦点元素只能在启动器抢走焦点**之前**取（之后 `GetFocusedElement` 返回的是我们自己）。所以第一段在 `showLauncherThen` 里、`panel.Show/Focus` 之前同步完成，硬上限 50ms：在 UIA 线程上执行，热键回调用带超时的 channel 等它；超时就放弃这次（UIA 线程上迟到的结果按进程号丢弃）。
- 读选区和文字可以在之后做：Win32 Edit、Chromium、WPF 失去焦点后选区仍在（需实测确认各类型）。第二段和环境识别一样放进 `host.Go`，预算 250ms，到达后刷新根列表。
- 待测的替代方案：用 `GetGUIThreadInfo(目标线程).hwndFocus` + `ElementFromHandle` 代替同步的 `GetFocusedElement`，这样第一段只是一个本地调用（<1ms）。对 Win32 控件可行；对 Chromium/Qt 这类“一个 HWND 里很多元素”的程序拿不到真正的焦点元素。实测后决定是否作为 Win32 控件的快速路径。

### 3.2 顺序（`internal/shell/shell.go` 的 `showLauncherThen`）

```
fg := clip.ForegroundWindow(); s.prevForeground.Store(fg)
sel := selection.Begin(fg, opts)            // 第一段，≤50ms；关闭时立即返回
if 需要剪贴板方式 { sel.CopyNow(ctx) }        // 专用快捷键或名单内应用，≤250ms，在 Show 之前
s.env.Engine.DetectContext(…)               // 不变
s.env.Engine.DetectSelection(sel.Read)       // 第二段，异步；剪贴板方式已经有结果时直接 Set
… panel.Show(); panel.Focus()
```

剪贴板方式的等待不能放在 MyGo 主线程上空转：热键回调里启动后台流程，完成后 `mygo.RunOnMain` 再显示窗口；这期间再次按热键忽略。

### 3.3 晚到的分组（复用 R26/R27）

- 选区和目录环境共用 `core.contextState` 的代数：新的一次呼出使旧结果作废；两者分别到达，各触发一次 `contextChanged`。
- 选中文字分组插在“在 <目录> 中”之上。R26/R27 的规则原样适用：用户没有用方向键或点击移动过选中项时，到达后选中**第一行**并滚到顶部；悬停不算主动选择；行标识用“分组标题 + Key”。
- 第一行是“选中文字”预览行（§5.1），按 Enter 只是查看全文，**不会把文字发出去**。这样“Alt+Space、Enter”的肌肉记忆在残留选区的情况下也不会误触 AI 或网页搜索。
- 回归测试按 R27 的教训：列表比窗口高、鼠标停在列表上、选区在根列表显示后 100ms 才到达。

### 3.4 预算与测量

| 段 | 预算 | 目标（需实测） |
|----|------|----------------|
| 第一段（焦点元素 + IsPassword + 是否有 TextPattern） | 50ms，超时放弃 | 中位数 <15ms |
| 第二段（GetSelection + GetText） | 250ms，超时丢弃 | 中位数 <30ms；Chromium 首次除外 |
| 剪贴板方式（保存 + 按键 + 等待 + 读取 + 还原） | 保存 50ms，等待 200ms | 有选区时 <100ms；没选区时等满 200ms |

测量方法见 §9.4。如果第一段的中位数超过 15ms，就改用 §3.1 的 `hwndFocus` 快速路径或把第一段整体挪到异步（代价是部分程序读不到）。

## 4. 隐私与安全

### 4.1 原则

| 规则 | 做法 |
|------|------|
| 默认开还是关 | UIA 方式默认**开**（和“情境感知”一致：只在呼出时读一次、只在内存里）；剪贴板方式默认**关**（有副作用）；第三方插件默认**看不到**（§7.3）。“情境感知”关掉时选中文字也不读 |
| 密码框 | UIA `IsPassword` 为真时什么都不读；剪贴板方式在 UIA 拿不到元素时只能靠应用排除名单 |
| 按应用排除 | “从不读取选中文字的应用”名单，默认：`KeePassXC.exe`、`KeePass.exe`、`1Password.exe`、`Bitwarden.exe`（与剪贴板历史的 `ignoreApps` 默认值相同）。网银等网页应用无法按应用排除（是浏览器），第二期有了浏览器网址识别后再按域名排除 |
| 不落盘 | 选中文字只在本次呼出的内存里；“最近使用”不记录（条目 `Transient`，宿主同时把分组条目标成 `Entry.Context`）；不写日志（只记方式、应用名、长度、耗时、错误种类） |
| 隐藏即清空 | 启动器隐藏（`EventWindowHidden`）时清掉当前选区；下一次呼出重新读取。已经发给 AI 的那条消息属于对话记录（见下） |
| 不进剪贴板历史 | 剪贴板方式的临时内容由序号区间抑制，还原内容带排除标记（§2.2）；**不提供“复制选中的文字”这个动作**（用户在原程序里按 Ctrl+C 即可）。计算结果、AI 回答等“处理后的结果”照常复制或粘贴 |
| AI 只在用户选择时 | 只有用户执行了 AI 动作，才把**选中的文字原文**（截断后的）连同固定的提示词作为一条消息发出；消息在对话视图里完整显示，选中文字放在引用块里，截断时注明。发送前可以按 Enter 在预览行查看全文 |
| 对话记录 | 从选中文字开始的对话和其他对话一样存进 AI 对话历史（30 天，可删除）。是否要单独设置“不保存这类对话”见 §11 |
| AI 工具层 | 选区永远不出现在工具结果里：`Engine.Search`（AI 用）本来就不带本次呼出的环境；选中文字分组的条目另外标 `Item.Sensitive`，双保险。外部 AI 应用（MCP）完全看不到 |
| 长度上限 | 默认 10 000 个字符（设置可改，1 000–100 000）；超出部分截掉，`Truncated=true`，预览行和 AI 消息里注明“已截断” |

### 4.2 日志示例

```
level=DEBUG msg="selection captured" source=uia app=Notepad.exe chars=128 truncated=false took=12ms
level=INFO  msg="selection skipped" reason=password app=…
level=INFO  msg="selection clipboard restore skipped" reason=changed-by-other
```

## 5. 呈现与交互

### 5.1 根列表，输入为空

```
┌─────────────────────────────────────────────────────────────┐
│  搜索…                                                       │
├─────────────────────────────────────────────────────────────┤
│ 选中的文字                                                    │
│ ▸ “Let's move the meeting to Thursday af…”   Weixin · 52 字  │ ← 预览行，Enter 查看全文
│   翻译成中文                                   AI             │
│   问 AI…                                      AI             │
│   总结 / 解释 / 改写 / 回复建议                 AI             │
│   用 Google 搜索                                              │
│ 在 Downloads 中                                ← 有目录环境时  │
│   …                                                         │
│ 最近使用                                                      │
└─────────────────────────────────────────────────────────────┘
```

- **预览行**（`builtin.selection` 提供）：标题是选中文字的第一行，去掉多余空白，截到 60 个字符加“…”；副标题“<应用> · <字数>”，截断时加“· 已截断”；整行 `Sensitive`。Enter 推入“选中的文字”详情视图：全文（Markdown 代码块，保留换行）+ 元数据（应用、字数、字符数、行数、方式 UIA/剪贴板、是否截断、是否可编辑）。**字数统计就在这里**，不单独做动作。
- **动作行**：各插件 `SelectionItems` 的条目，按插件顺序再按插件自己的顺序；输入为空时最多显示 6 行动作（加预览行），其余在输入文字时才参与搜索。插件顺序为 `builtin.selection` → `builtin.calc` → `builtin.quicklinks` 的“打开链接” → `builtin.ai` → `builtin.quicklinks` 的搜索。为此插件可以给条目设一个可选的 `Item.Score`：空输入时宿主按 Score 从大到小排，“是网址”“是算式”这类明确命中的条目设 1，排到前面。
- **和目录环境共存**：两者都有时（例如在资源管理器的重命名框里选中了文字），选中文字分组在上，目录分组在下，互不影响。底栏的环境标签不变；有选区时标签后面加一个小标签“已选中 52 字”，点击等于 Enter 预览行。

### 5.2 输入文字时

- 分组里的条目和其他结果一起参与模糊匹配，计分方式和环境条目相同（0.7×匹配分 + 0.1）。输入“翻译”“总结”“搜索”“tr”就能找到对应动作。
- **输入内容作为给 AI 的指令**：AI 开着时，`builtin.ai` 在分组里额外返回一行“让 AI 处理选中的文字：<输入>”（预先匹配，`Score` 0.6，排名约 0.52：明确匹配的应用和命令仍在它前面，没有好结果时它在最前）。例：选中一段英文，输入“翻译成日语”→ 这一行在最前 → Enter → 打开对话，消息是“翻译成日语”+ 引用的选中文字。
- 选中文字常常是“残留”的（用户其实只想开个应用），所以普通搜索的排序和以前一样，选中文字不会挤掉明确的匹配。

### 5.3 Tab 和参数

- **根搜索里的 Tab 不变**：仍是“把输入的文字发给 AI”，不附带选中文字（防止残留选区被顺带发出）。
- **“问 AI…”行带一个可选参数**（`Item.Arguments`，占位符“要 AI 做什么？可留空”）：选中这一行按 Tab 进入参数输入，写指令，Enter 发送；不写指令直接 Enter，就是“请解释这段文字”的默认提示词。这复用了现有的“有参数的条目 Tab 进参数”规则，不用新的按键。
- 对话视图里：从选中文字开始的对话，消息上方显示引用块；“粘贴回答到前一个窗口”（`Ctrl+Shift+V`）在选区可编辑时标题改为“替换选中的文字”。

### 5.4 Enter 的含义汇总

| 选中的行 | Enter | Ctrl+K 里的其他操作 |
|----------|-------|----------------------|
| 预览行 | 查看全文和统计 | — |
| 翻译 / 解释 / 总结 / 改写 / 回复建议 | 打开 AI 对话并发送 | 在 Claude/ChatGPT 中继续（已有的“交给应用继续”） |
| 问 AI… | 有参数时 Tab 进参数；Enter 用默认提示词发送 | |
| 让 AI 处理选中的文字：<输入> | 发送输入 + 选中文字 | |
| 用 <引擎> 搜索 | 打开浏览器 | 第二个引擎（如果设置了） |
| 打开链接 | 用默认浏览器打开 | 复制链接 |
| = <计算结果> | 复制结果 | 粘贴替换（可编辑时）、复制算式 |

## 6. 内置动作（第一版）

| 动作 | 提供者 | 出现条件 | 行为 |
|------|--------|----------|------|
| 预览 / 全文 / 字数统计 | `builtin.selection`（新，`plugins/selection`） | 总是 | §5.1。字数：中文按字计、英文按词计（`unicode` 分类，空白分词），另给字符数和行数 |
| 打开链接 | `builtin.quicklinks` | 选区去掉首尾空白后整体是网址（复用 `DetectURL`，和根搜索里输入网址同一规则），只认单个网址 | `ActionOpen`；Score 1，排在前面 |
| = 计算结果 / 换算 | `builtin.calc` | 单行、≤ calc 的 `maxInput`，并且 `evaluate` 成功且结果和原文不同（纯数字“42”不算；时间戳、颜色、“10 km”这类特殊换算算） | 和根搜索的结果条目相同：复制、复制算式；可编辑时“粘贴替换”（`ActionPaste`，回到原窗口粘贴，正好替换仍处于选中状态的文字）。Score 1 |
| 翻译 | `builtin.ai`（AI 开着时）；`builtin.quicklinks`（总是，网页翻译） | AI：总是。网页：选区 ≤ 2 000 个字符（URL 长度） | AI：推入对话视图并发送“把下面的文字翻译成<目标语言>，只输出译文”+ 引用。目标语言：AI 插件新偏好“翻译成”，默认“自动”= 界面语言；原文主要已是界面语言时译成英文（按字符的 Unicode 区块粗判）。网页：用 quicklinks 已有的 `translate` 引擎（Google 翻译，界面为中文时 `tl=zh-CN`），原文是中文时用 `tl=en`；有道（`youdao` 引擎）可在 Ctrl+K 里选 |
| 问 AI… | `builtin.ai` | AI 开着 | §5.3，可选参数作为指令 |
| 解释 / 总结 / 改写 / 回复建议 | `builtin.ai` | AI 开着；总结只在 ≥ 200 字时出现 | 固定提示词（见下）+ 引用；发送后就是普通对话，可以追问。回答可用“粘贴回答到前一个窗口 / 替换选中的文字” |
| 让 AI 处理选中的文字：<输入> | `builtin.ai` | AI 开着且有输入 | §5.2 |
| 用 <默认引擎> 搜索 | `builtin.quicklinks` | 选区 ≤ 300 个字符；多行时换行替换成空格 | 和备选条目“用 Google 搜索…”同一套 `defaultEngines()`；第二引擎在 Ctrl+K |
| AI 关着时的提示 | `builtin.ai` | AI 没打开 | 一行“用 AI 翻译、总结选中的文字”，Enter 推入现有的“打开 AI”提示页，打开后回到根列表 |

提示词（发给模型，不翻译成界面语言，但要求“用<界面语言>回答”）：

| 动作 | 提示词要点 |
|------|------------|
| 解释 | Explain the quoted text concisely. |
| 总结 | Summarize the quoted text in a few bullet points. |
| 改写 | Rewrite the quoted text to be clearer and more natural, keeping its language and meaning. Output only the rewritten text. |
| 回复建议 | The quoted text is a message I received. Suggest 3 short replies in its language. |

AI 插件的 `chatPayload` 增加 `Quote`（选中文字）和 `Truncated`；对话标题取动作名（“翻译：Let's move…”），不是选中文字的全文。

第二期再做：大小写转换、繁简转换、去掉多余空格和换行、URL 编码/解码、Base64、JSON 格式化（都放进 `builtin.selection`，本地处理，结果可复制或粘贴替换）；多个网址时逐个打开；划词后直接朗读。

## 7. SDK 与宿主改动

### 7.1 SDK（`pkg/plugin/selection.go`）

```go
// PermSelection lets a plugin receive the text selected in the window that
// was active when the launcher opened (docs/selected-text.md). Built-in
// plugins declare it; for plugins in their own process the user must also
// allow it in Settings › Plugins.
const PermSelection Permission = "selection"

// Selection is the text selected in the foreground window when the launcher
// opened. It lives in memory for one show only and is never stored.
type Selection struct {
    Text      string  `json:"text"`
    App       AppInfo `json:"app"`
    Source    string  `json:"source"`              // SelectionUIA | SelectionClipboard
    Truncated bool    `json:"truncated,omitzero"`
    Editable  bool    `json:"editable,omitzero"`   // known to be in an editable control
}

const (
    SelectionUIA       = "uia"
    SelectionClipboard = "clipboard"
)

// SelectionQuery is passed to SelectionProvider.
type SelectionQuery struct {
    Selection Selection `json:"selection"`
    Text      string    `json:"text"` // what the user typed in the root search, "" when empty
}

// SelectionProvider contributes items for the selected text. They are shown
// in the "Selected Text" section at the top of the root search and take part
// in the search when text is typed. Only plugins with PermSelection (and, if
// external, allowed by the user) are called. Return nil when nothing applies.
type SelectionProvider interface {
    SelectionItems(ctx context.Context, q SelectionQuery) ([]Item, error)
}
```

- **不放进 `Context`**：`Context` 会传给所有 `ContextProvider` 和 `RootItems`（`Query.Context`），也包括没有权限的外部插件。选中文字只经过 `SelectionProvider` 这一条路，权限检查只有一处。
- 宿主把这些条目的 `Group` 设为“选中的文字”，标 `Entry.Context`（不进最近使用）、`Sensitive`；插件仍应把文字放进动作的 Payload，而不是指望以后再取。
- `Item.Score` 在空输入时作为选中文字分组内的排序依据（§5.1），只对这个分组生效，目录环境分组的顺序不变。

### 7.2 外部插件协议（`docs/plugin-protocol.md`）

| 方法 | 参数 | 结果 |
|------|------|------|
| `selectionItems` | `{selection: {text, app, source, truncated?, editable?}, text}` | `Item[]` |

- 能力位 `capabilities.selectionItems`；超时 150ms（与 `contextItems` 相同）。
- 只有清单里声明了 `"selection"` 权限、**并且**用户在 设置 › 插件 › <插件> 里打开了“允许读取选中的文字”（外部插件默认关）时才调用。权限没开时插件根本不会收到任何选区信息，也不知道有没有选区。
- `extproc.Proxy`、`rpcserve` 都要实现；`rpcserve` 侧提供 `SelectionProvider` 的适配。

### 7.3 权限

| 插件 | 默认 |
|------|------|
| 内置插件（`builtin.selection`、`builtin.ai`、`builtin.quicklinks`、`builtin.calc`） | 清单声明 `selection`，直接可用 |
| 外部插件 | 清单声明后，还需用户在设置里逐个允许；未声明的不显示这个开关 |

### 7.4 宿主

| 部分 | 位置 | 说明 |
|------|------|------|
| UIA 读取 | `internal/winapi/selection`（包名 `winselection`）：`uia_windows.go`、`selection.go`、`other.go` | `Begin(hwnd, opts) *Pending`（第一段）、`(*Pending).Read(ctx) (plugin.Selection, bool)`（第二段）；UIA 线程（锁定 OS 线程、MTA，进程内只建一个，空闲 30s 后释放 COM）；内置的禁止 Ctrl+C 名单；完整性级别检查 |
| 剪贴板方式 | `internal/winapi/clip` | `Snapshot() (*Saved, error)`（所有格式、上限）、`(*Saved).Restore(markers bool)`、`SequenceNumber()`、`Suppress(fromSeq) (end func())`（扩展 `ownWrite`）、`CopyFrom(hwnd, timeout) (string, error)`（与 `PasteTo` 共用按键代码，修饰键处理相同）。为测试把 Win32 调用收进一个小接口 `api`，测试换成内存假剪贴板 |
| 引擎 | `internal/core/selection.go` | `Engine.DetectSelection(read)`、`Engine.SetSelection`（glctl 注入）、隐藏时清空、和 `contextState` 共用代数；`selectionEntries` 汇总有权限的插件（并发、超时、稳定顺序，与 `contextEntries` 相同的写法） |
| 外壳 | `internal/shell/shell.go` | §3.2 的顺序；专用快捷键注册（和启动器热键一样走 `registerHotkeys`）；剪贴板方式期间忽略重复热键 |
| 界面 | `internal/ui` | 底栏“已选中 N 字”标签；无新控件 |
| 新插件 | `plugins/selection`（`builtin.selection`） | 预览行、全文详情视图；第二期的本地文字转换 |
| AI 插件 | `plugins/ai` | `SelectionItems`、`chatPayload.Quote`、“翻译成”偏好、引用块显示、“替换选中的文字”标题 |
| 快速链接 | `plugins/quicklinks` | `SelectionItems`：打开链接、搜索、网页翻译 |
| 计算器 | `plugins/calc` | `SelectionItems`：复用 `evaluate` |

## 8. 设置

放在 设置 › 通用 › “情境感知”开关下面，作为它的子项（情境感知关掉时整组变灰）：

| 设置项 | 配置字段（`config.Config.Selection`） | 默认 | 说明 |
|--------|----------------------------------------|------|------|
| 读取选中的文字 | `enabled` | 开 | 用 UI Automation 读取，不改剪贴板 |
| 读不到时用剪贴板方式 | `clipboard` | 关 | 说明：会临时改写剪贴板并立即还原；Windows 剪贴板历史、云剪贴板和其他剪贴板工具可能记下这段文字；终端、Excel 永不使用。Win+V 或云剪贴板开着时显示提醒 |
| 使用剪贴板方式的应用 | `clipboardApps` | 空 | 程序名列表（如 `Weixin.exe`）。Alt+Space 在这些应用里也会用剪贴板方式（弹出会慢一点） |
| 对选中文字打开启动器 | `hotkey` | 未设置 | 专用快捷键；按下时先读选中文字（必要时用剪贴板方式）再显示启动器。没有选中文字时照常打开，并提示“没有读取到选中的文字” |
| 从不读取的应用 | `excludeApps` | `KeePassXC.exe`、`KeePass.exe`、`1Password.exe`、`Bitwarden.exe` | 两种方式都不读 |
| 最多读取 | `maxChars` | 10 000 字符 | 1 000–100 000 |

“方式”不做成单独的下拉框：开关组合已经表达了“关闭 / 仅 UIA / UIA + 剪贴板”，少一个概念。

术语（补进 [i18n-zh-cn.md](i18n-zh-cn.md) §4）：Selected Text → 选中的文字；Read selected text → 读取选中的文字；Replace selection → 替换选中的文字；Clipboard method → 剪贴板方式。

## 9. 测试

### 9.1 单元测试（默认运行，不碰真实剪贴板和其他窗口）

- `internal/winapi/clip`（内存假剪贴板 + 假 `SendInput`）：
  - 保存/还原：多种格式按原顺序原样写回；合成格式和句柄格式跳过；超过 16MB、出现保存不了的格式 → 放弃且**没有发按键**；原来为空 → 还原为空；还原带三个排除标记、原有标记保留。
  - 等待：序号不变 → 超时、不还原；连续多次写入 → 等稳定后再读；读完后有第三方写入 → 不还原。
  - 按键序列：Alt/Shift/Win 按着时的事件顺序（Ctrl 先按下、再抬起修饰键、C、Ctrl 抬起）；`SendInput` 只插入一部分时补发抬起。
  - 抑制：区间内的变化不触发 `Watch`，区间结束后的正常复制照常触发。
- `internal/winapi/selection`：禁止名单匹配（大小写、路径）；完整性级别判断的分派；截断（按字符，不截断在代理对中间）；多段选区拼接。UIA 的 COM 部分抽成函数变量（同 `wincontext.explorerView`），测试替换。
- `internal/core`：选区异步到达后刷新根、先于根到达、超时丢弃、新呼出丢弃旧结果、隐藏时清空、情境感知关闭时 `read` 不被调用；只调用有权限的插件（外部插件未允许时不调用）；条目不进最近使用、标 `Sensitive`；AI 用的 `Engine.Search` 结果里没有选区条目；空输入时最多 6 行、按 Score 排序；和目录分组同时存在时的顺序；R26/R27 规则（列表比窗口高、鼠标在列表上、选区晚到 → 选中预览行、滚到顶）。
- 插件（hostfake）：`builtin.calc`（算式、单位、时间戳出现；纯数字、多行、超长不出现）；`builtin.quicklinks`（网址判定、搜索长度上限、换行替换、网页翻译的目标语言）；`builtin.ai`（AI 关时的提示行、指令行、各提示词、`Quote` 进对话、对话标题、“翻译成”自动规则、截断说明）；`builtin.selection`（预览截断、字数统计中英混排、详情元数据）。
- 协议：`rpcserve` 的 `selectionItems`；`extproc` 对没有能力位或用户未允许的插件不发请求。
- `internal/lint` 现有规则照常适用（界面文字走 i18n、插件用 `Host.Go`）。

### 9.2 glctl golden

- 新参数：`--selection TEXT`、`--selection-app NAME`（默认 `notepad.exe`）、`--selection-source uia|clipboard`、`--selection-truncated`、`--selection-editable`；`query`、`session new` 都支持（写进 session 文件）。注入时不做任何读取。
- golden：`query_selection`（空根：预览行 + 动作，AI 关）、`query_selection_url`、`query_selection_calc`、`query_selection_typed`（输入“翻译”）、`session_selection_ai`（假后端，回答“You asked: …”，能看到提示词和引用）、`session_selection_instruction`（输入指令后的指令行）、`query_selection_context`（同时注入目录）、`zh_query_selection`。

### 9.3 显式开启的真实测试

- 剪贴板方式：沿用 `GOLAUNCHER_CLIPBOARD_TESTS=1`。测试先保存用户的剪贴板，结束时用同一套还原代码恢复；目标窗口是测试自己创建的（子进程里的 Win32 `EDIT` 控件，内容是固定的假文字），绝不对用户的窗口发按键。
- UIA：新的 `GOLAUNCHER_SELECTION_TESTS=1`。测试用自身的 helper 子进程（`os.Args[0]` + 环境变量切换为 helper 模式）创建一个 `EDIT`、一个 `RICHEDIT50W`、一个 `ES_PASSWORD` 控件，选中一段文字后把它放到前台，测：读到的文字、密码框不读、无选区、截断。不打开浏览器或其他用户程序。

### 9.4 延迟测量

- 真实测试里记录每一步耗时（第一段、第二段、剪贴板方式的各步骤），每种情况跑 20 次，输出中位数、p95、最大值，写进本文的实现记录（只记控件类型和数字）。
- 手工测量清单（实现完成后，只记“支持/不支持/方式/耗时”）：记事本、Word、Edge、Chrome（首次和之后）、Firefox、VS Code、Windows Terminal、微信 3.x、微信 4.x、一个 Electron IM、一个 PDF 阅读器。结果更新到 §2.1 的表里，并据此决定是否在设置说明里建议把某些应用加入“使用剪贴板方式的应用”。
- 热键到窗口可见的整体时间：有选区功能和没有时各测一次，确认 Alt+Space 的弹出没有可感知的变慢（差值中位数 <15ms）。

## 10. 分期

| 阶段 | 内容 |
|------|------|
| **第一期（MVP）** | UIA 读取（两段式）；SDK `Selection`、`SelectionProvider`、`PermSelection`；宿主分组、R26/R27 规则、隐藏清空；`builtin.selection`（预览、全文、字数统计）；`builtin.ai`（翻译、问 AI 带参数、解释、总结、改写、回复建议、指令行、AI 关时提示）；`builtin.quicklinks`（打开链接、搜索、网页翻译）；`builtin.calc`（计算/换算、粘贴替换）；设置（开关、排除名单、最大长度）；glctl 注入和 golden；UIA 真实测试和延迟测量；手工兼容性清单 |
| **第一期之后马上做（视实测结果）** | 剪贴板方式（保存/还原、抑制、按键、禁止名单）、“使用剪贴板方式的应用”、专用快捷键；剪贴板真实测试 |
| **第二期** | Win+V 历史里删除临时条目（WinRT，需实测）；外部插件协议 `selectionItems` 和设置里的逐插件允许；本地文字转换（大小写、繁简、格式化）；浏览器网址识别后按域名排除；HTML 选区（保留链接和格式） |

协议部分放在第二期：第一期只有内置插件使用选区，外部插件在第一期完全看不到选区，不需要权限界面。

## 11. 需要你决定的问题

1. **默认开关**：UIA 方式随“情境感知”默认开启，可以吗？还是也要默认关、首次使用时询问？
2. **剪贴板方式**：同意“默认关、只用于专用快捷键和名单内应用”吗？还是希望 Alt+Space 在 UIA 读不到时就自动用剪贴板方式（每次呼出都可能改写剪贴板并推迟 ~200ms）？
3. **微信**：实测后如果 UIA 读不到，是否把 `Weixin.exe` / `WeChat.exe` 默认放进“使用剪贴板方式的应用”（前提是你打开了剪贴板方式）？
4. **根搜索的 Tab**：保持“只发输入的文字”（推荐，防止残留选区被发出），还是有选区时 Tab 自动带上选中文字？
5. **对话记录**：从选中文字开始的 AI 对话，和其他对话一样保存 30 天，还是不保存（或者单独一个开关）？
6. **空输入时的行数**：选中文字分组最多 6 行动作（加预览行）合适吗？AI 的六个动作（翻译、问 AI、解释、总结、改写、回复建议）是否要精简，比如只留翻译和问 AI，其余输入时才出现？
7. **翻译目标语言**：“自动”（界面语言，原文已是界面语言时译成英文）可以吗？
8. **剪贴板方式第一期就做**，还是等 UIA 的兼容性清单测完再决定？

## 12. 决定（2026-10-16，R65）

1. **跟随环境感知开关**：读取选中文字属于环境感知，跟随现有开关，不单独设总开关。
2. **模拟 Ctrl+C**：
   - 功能保留，**默认关闭**（设置项"读不到时改用复制的方式"）；
   - 打开后，Alt+Space 用 UI Automation 读不到（应用不支持）时，**自动改用模拟 Ctrl+C**；
   - 原来的"专门热键"和"按应用列表"可以保留为补充；
   - 因此第一版就包含 Ctrl+C 方式，§11 的问题 3（是否默认把微信加入列表）不再需要。
3. **主搜索框按 Tab 进 AI**：只发送输入的文字，不带选中的文字。
4. **从选中文字发起的 AI 对话**：和其他对话一样保留 30 天。
5. **没有输入文字时**：最多显示 6 个操作。
6. **翻译目标语言**：
   - 默认翻译成界面语言，设置里可以修改；
   - 原文已经是目标语言时，仍按 §11 的建议译成英文。
## 13. 实现记录（2026-10-16，R65）

### 13.1 做了什么

- **SDK**（`pkg/plugin/selection.go`）：`PermSelection`（`"selection"`）、`Selection`（`Text`、`App`、`Source`、`Truncated`、`Editable`）、`SelectionQuery`、`SelectionProvider`，与 §7.1 一致；选中的文字**不在** `plugin.Context` 里。
- **配置**（`config.Config.Selection`）：`clipboard`（读不到时改用复制的方式，默认关）、`clipboardApps`（总是用复制方式的应用）、`hotkey`（对选中的文字打开启动器，默认未设置）、`excludeApps`（默认 KeePassXC、KeePass、1Password、Bitwarden）、`maxChars`（默认 10 000，范围 1 000–100 000）。按 §12.1 **没有** `enabled` 总开关，跟随 `contextAware`。旧配置文件缺这一段时取默认值；清空的列表保持为空。
- **读取**（`internal/winapi/selection`，包名 `winselection`）：
  - `Begin(hwnd, opts)` 是第一段：在专用的 UIA 线程（锁定 OS 线程、MTA、空闲 30 秒后释放）上 `GetFocusedElement`，查进程号、`IsPassword`、是否支持 `TextPattern2`/`TextPattern`；热键回调在 MyGo 主线程上最多等 `FocusBudget`（50ms），超时视为“拿不到元素”。`IUIAutomation2` 的连接/事务超时设为 200ms。
  - `(*Pending).Read(ctx)` 是第二段：`GetSelection` → 逐段 `GetText`（多段用换行连接），按字符截断（不截断在字符中间）；第一次没有任何选区时 30ms 后重试一次（Chromium 首次构建无障碍树）。可编辑性：控件类型 Edit 且没有只读的 ValuePattern，或 Document 带非只读 ValuePattern。
  - 方式选择 `decide`（纯函数，全平台单测），依次：排除的应用 → 不读；本进程 → 不读；密码框 → 不读；在“总是用复制方式的应用”里且打开了复制方式且允许 Ctrl+C → 复制；支持 TextPattern → UIA（**支持但没选中时不会再发 Ctrl+C**）；没打开复制方式 → 不读；禁止 Ctrl+C 的应用（Windows Terminal、conhost、cmd、PowerShell、各类终端模拟器，窗口类 `ConsoleWindowClass` / `CASCADIA_HOSTING_WINDOW_CLASS` / `PseudoConsoleWindow`；Excel）→ 不读；目标进程完整性级别高于本进程（打不开也按“更高”处理）→ 不读；没选区时 Ctrl+C 会复制整行的编辑器（VS Code、Cursor、JetBrains 系列、Visual Studio、Sublime Text、Notepad++）→ 不读，除非用户把它加进了“总是用复制方式的应用”；其余 → 复制。
- **复制方式**（`internal/winapi/clip`，`CopySelection(hwnd, CopyOptions)`）：按 §2.2 实现。保存所有内存格式（按原顺序；有 `CF_UNICODETEXT` 时跳过 `CF_TEXT`/`CF_OEMTEXT`/`CF_LOCALE`，有 DIB 时跳过 `CF_BITMAP`/`CF_PALETTE`）；遇到元文件、`CF_OWNERDISPLAY`、`CF_DSP*`、私有句柄格式，或总量超过 16MB、保存超过 50ms、保存后剪贴板又变了 → `ErrCannotSave`，**不发任何按键**。按键与 `PasteTo` 共用 `shortcutKeys`（先按下 Ctrl，再抬起仍按着的 Alt/Shift/Win，再 C、抬起 Ctrl），先等最多 100ms 让用户松开热键。等待用临时的剪贴板监听窗口（事件驱动）+ 高精度定时器，变化后 20ms 不再变化才读；只读 `CF_UNICODETEXT`。还原前在打开剪贴板的状态下核对序号（期间有别人写过就不动），写回时附加三个排除标记（已有就不重复）；还原失败返回文字和包装了 `ErrNotRestored` 的错误，外壳据此弹出“剪贴板未能还原”。整个过程中本进程的剪贴板监视器按序号区间忽略所有变化（结束后再延长 50ms 的 debounce），所以 golauncher 自己的剪贴板历史既不记临时内容也不记还原内容。
- **引擎**（`internal/core/selection.go`）：`DetectSelection(read)`（在 `DetectContext` 之后调用，共用“每次呼出一代”，预算 250ms）、`SetSelection`（glctl）、`ClearSelection`（启动器隐藏时由外壳同步调用，同时让还在读的结果作废）、`Selection()`。到达时宿主再检查一遍：空白文字、排除的应用丢弃，超长截断。`selectionEntries` 只调用声明了 `PermSelection` 且在本进程运行的插件（外部插件一律不调用），条目统一设 `Group`、`Sensitive`、`Transient`、`Entry.Context`、`Entry.Selection`。空输入：按 Score 从高到低，再按插件的 fallback 顺序（快速链接的网页搜索排最后），再按插件顺序，最多 7 行（预览 + 6 个操作）。有输入：排名 = 0.7×匹配分 + 0.1，带 Score 的条目按 Score 作为匹配分，**不加** alias 加分，也**不计入**“是否有好结果”的判断（残留选区不会挤掉明确匹配，也不会让备选命令消失）。晚到刷新复用 `contextChanged`（R26/R27 规则）。`Engine.Search`（AI 用）不包含选区条目。
- **外壳**（`internal/shell/shell.go`）：热键回调里依次 `prevForeground` → `DetectContext` → `winselection.Begin`；需要复制时把复制放到后台 goroutine，完成后回主线程再显示（期间再按热键忽略）；然后 `DetectSelection(sel.Read)`、显示窗口。新的全局热键“对选中的文字打开启动器”（情境感知打开且设置了才注册）：没读到时在启动器底栏提示“没有读取到选中的文字”。设置页检测 `HKCU\Software\Microsoft\Clipboard` 的 `EnableClipboardHistory`/`EnableCloudClipboard`，打开复制方式且其中之一开着时显示提醒。
- **界面**：底栏在环境标签后面加“已选中 N 字”标签，点击等于在预览行按 Enter（`Controller.OpenSelection`）。设置 › 通用：在“语言”下面新增“选中的文字（属于情境感知）”一组，只在情境感知打开时显示。
- **插件**：
  - 新插件 `builtin.selection`（排在 apps 之后）：预览行（第一行、压缩空白、60 字符；副标题“应用 · N 字/words · 已截断”，敏感），Enter 推入全文视图（列表 + 详情：代码块里的全文，元数据：应用、字数、字符数、行数、方式、是否截断、是否可编辑）。字数：汉字/假名/谚文每个算一个，其他连续的字母数字算一个词。
  - `builtin.ai`：AI 关着时一行“用 AI 翻译、总结选中的文字”，Enter 进现有的“打开 AI”提示页，打开后回到根列表（`PopToRoot`），之前不发送任何内容；AI 开着时：翻译成<语言>、问 AI…（可选参数“要 AI 做什么？可留空”）、解释、总结（≥200 字符）、改写、回复建议；有输入时另加“让 AI 处理选中的文字：<输入>”（Score 0.6）。新偏好“翻译成”（`translateTo`，默认“自动（界面语言）”）。发出并显示的消息 = 指令 + 空行 + 引用块（每行加 `> `），截断时再加一行 `_(The quoted text was truncated to N characters.)_`；对话标题“<动作>：<选区前 30 字>”。从可编辑的选区开始的对话，“粘贴回答到前一个窗口”改名“替换选中的文字”（只在内存里记）。根搜索的 Tab 不变。
  - `builtin.quicklinks`：打开链接（整个选区是一个网址，Score 1）、用默认引擎搜索（≤300 字符，空白压成一个空格，第二引擎在 Ctrl+Enter）、网页翻译（≤2 000 字符；界面中文时译成中文、原文是中文时译成英文；界面英文时译成英文、原文是英文时译成中文；有道在 Ctrl+K）。
  - `builtin.calc`：单行、不超过计算器的最大长度、能算出且结果和原文不同才出现，条目和根搜索的结果相同（Score 1）；可编辑时把已有的“粘贴”动作改名为“粘贴替换选中的文字”，不可编辑时去掉这个动作。
- **AI 工具层**：`builtin.selection` 加入 `agent.Hidden`（永远不提供给 AI）；选区条目都是 `Sensitive`；`Engine.Search` 不带选区。
- **glctl**：`--selection TEXT`、`--selection-app`（默认 `notepad.exe`）、`--selection-source uia|clipboard`、`--selection-truncated`、`--selection-editable`，`query` 和 `session new` 都支持（写进 session 文件）。

### 13.2 与设计的出入

1. **复制方式的触发条件**（§12.2 覆盖 §2.3）：打开“读不到时改用复制的方式”后，Alt+Space 在第一段判定“读不到”（拿不到焦点元素、元素不支持 TextPattern、第一段超时）时自动改用 Ctrl+C。判定只看第一段：如果元素支持 TextPattern 但第二段读失败，已经来不及发 Ctrl+C（启动器已拿到焦点），这次就没有选区。专用热键和“总是用复制方式的应用”保留为补充；专用热键只在打开复制方式时才会复制。
2. **“使用剪贴板方式的应用”**改为“在这些应用中总是用复制的方式”：既然读不到时已会自动改用复制，这个列表的作用变成“跳过 UIA，直接复制”（例如 UIA 读到的内容不对的应用），并且能把“会复制整行的编辑器”放出来。
3. **编辑器**：§2.2 说的“默认的不要用剪贴板方式名单，用户可移除”实现为内置名单 + “总是用复制方式的应用”覆盖，少一个设置项。
4. **设置位置**：不是“情境感知开关下面变灰”，而是“语言”下面单独一组，情境感知关掉时整组隐藏（放在开关下面会把“语言”挤出首屏，界面测试也据此发现）。
5. **SDK 的 Score 规则**写进了 `SelectionProvider` 的注释：只对“明确命中”设 Score，且只在没有输入时设；有输入时带 Score 的条目按 Score 作为匹配分（用于“让 AI 处理…”）。排序里用了已有的 `FallbackOrderer`，让快速链接的网页搜索排在 AI 后面，没有新增接口。
6. **预览视图**用“列表 + 详情”而不是 `ViewDetail`：后者总是滚到末尾、元数据挤在一行，不适合长文本。
7. **计算器的“粘贴替换”**：根搜索的结果本来就有 Ctrl+Shift+V“粘贴”，可编辑时改名，不可编辑时去掉，避免两个同快捷键的动作。
8. **剪贴板 API 名称**：§7.4 写的 `Snapshot`/`Suppress`/`CopyFrom` 实际合并为一个导出函数 `clip.CopySelection`，内部步骤为未导出的 `saveClipboard`/`restoreClipboard`/`suppressor`，通过 `copyAPI` 接口测试。
9. **外部插件**：按 §10 放在第二期；第一期外部插件不会被调用，`plugin validate` 也不接受 `selection` 权限。
10. **Win+V 历史里删除临时条目**（WinRT）没有做，仍是第二期；设置说明和提醒里如实写明 Windows 剪贴板历史、云剪贴板和其他剪贴板工具可能记下复制的文字（目标程序写剪贴板时不带排除标记，我们无法事先标记，只能标记自己的还原）。

### 13.3 测量（只在测试自己创建的窗口上，各 20 次）

| 段 | 控件 | 中位数 | p95 | 最大 |
|----|------|--------|-----|------|
| 第一段（Begin） | Win32 EDIT | 2.1ms | 4.2–4.6ms | 4.7–5.8ms |
| 第一段 | RICHEDIT50W | 1.0ms | 1.6ms | 6.2–6.4ms |
| 第一段 | 密码 EDIT | 1.6ms | 2.1–2.6ms | 3.6–4.3ms |
| 第二段（Read） | Win32 EDIT | 0.5–0.6ms | 2.0ms | 2.6–3.6ms |
| 第二段 | RICHEDIT50W | 0.5ms | 1.0ms | 1.1ms |
| 冷启动（第一次，含建 UIA 线程和对象） | — | Begin 15–31ms，Read 1.6–3.7ms | | |
| 复制方式（EDIT，`GOLAUNCHER_CLIPBOARD_TESTS=1`） | 保存 / 等待 / 还原 / 合计 | ≈0 / ≈25ms / ≈0 / ≈27ms | 合计 31–65ms | |

第一段中位数远低于 15ms 的目标；冷启动在 50ms 预算内，但较慢的机器上可能超出（见 §13.5）。复制方式的等待里约 20ms 是“变化后再等 20ms 不再变化”。

### 13.4 已验证的与需要你试的

已验证（自动测试）：Win32 EDIT、RICHEDIT50W、密码框（不读）、无选区（不读）、截断、可编辑判断；复制方式在测试自己的 EDIT 窗口上读到文字并原样还原剪贴板（含排除标记、序号变化、监视器未触发）。

**请在真实应用里试**（结论只需告诉我“支持/不支持/方式/大概多快”，不要发内容）：

- 微信 4.x（`Weixin.exe`）和 3.x（`WeChat.exe`）：聊天输入框、聊天气泡里的部分选中。读不到时，打开“读不到时改用复制的方式”再试；
- 浏览器：Edge、Chrome（第一次和之后）、Firefox，网页正文和输入框；
- Office：Word 正文、Outlook 邮件正文；Excel（应当只在单元格编辑状态下用 UIA，永不发 Ctrl+C）；
- 终端：Windows Terminal、conhost（应当只用 UIA，永不发 Ctrl+C）；
- 记事本、VS Code（默认读不到；屏幕阅读器模式下也许可以）、一个 PDF 阅读器、一个 Electron 应用。
- 另外请留意：打开复制方式后在不支持 UIA 的应用里按 Alt+Space，弹出是否明显变慢；Win+V 历史里是否多出条目。

### 13.5 待定问题

1. 冷启动的第一段 15–31ms（UIA 线程空闲 30 秒后释放），慢机器上可能超过 50ms 预算导致这次读不到；可以改为启动时或热键注册后预热，或延长空闲时间。
2. 复制方式实测发现：剪贴板刚被别的程序改过、还有程序在读时，EDIT 控件打不开剪贴板就放弃复制，表现为等满 200ms 后“没有选中文字”。是否在超时后重试一次？
3. 空输入时最多 6 个操作，AI 开着且文字较长时 AI 的 6 个动作就占满了，网页搜索和网页翻译要输入才看得到。是否精简空输入时的 AI 动作（例如只留翻译、问 AI、解释、总结）？
4. “翻译成”的选项用界面语言显示语言名（“日语”），而不是各语言自己的名字（“日本語”）。
5. 界面是英文、原文也是英文时“自动”译成简体中文（因为应用只提供英文和简体中文两种界面），是否合适？
6. 网页翻译的标题是“网页翻译成英文/中文”，AI 翻译是“翻译成简体中文”，两者语言名不完全一致。
### 13.x 真机兼容性结果（你在 2026-10-16 测试，R66）

| 场景 | 复制方式关闭 | 复制方式打开 |
|------|------------|------------|
| Chrome：Google 搜索页、ChatGPT 输入框 | ✅ | ✅ |
| Chrome：ChatGPT 页面的其他内容 | ❌ | ✅ |
| 终端 | ✅ | ✅（直接读取，不发 Ctrl+C） |
| 微信：输入框 | ✅ | ✅ |
| 微信：聊天内容 | ❌ | ✅ |
| VS Code：输入框 | ✅ | ✅ |
| VS Code：代码编辑区 | ❌ | ❌ |

速度：没有感觉到延迟。

**VS Code 编辑区为什么读不到**：Monaco 编辑器在光标处放了一个隐藏的输入框（textarea）。它支持 TextPattern，但选区总是空的，所以按"支持读取但没有选中"处理，不会改用复制。这条规则同时保护了 VS Code 的内置终端（xterm.js 也是 textarea，发 Ctrl+C 会中断程序）。另外，VS Code 在没有选中时按 Ctrl+C 会复制整行。

**两个办法（待你选择）**：

1. 在 VS Code 里打开 `"editor.accessibilitySupport": "on"`，让 Monaco 把选中的文字同步到隐藏的输入框，直接读取可能就能读到。这需要实测。
2. 代码里特殊处理：
   - 只有焦点在 Monaco 编辑区时才允许改用复制，按 UIA 的类名区分 `inputarea` 和 xterm 的 `xterm-helper-textarea`；
   - 用 VS Code 写进剪贴板的 `vscode-editor-data` 元数据（`isFromEmptySelection`）丢弃整行复制。

   这两点都需要先用一个只读的诊断命令在真机上确认。