# 翻译插件方案（GitHub issue #3）

> 状态：方案 v1 · 已确认，按三期顺序开发（R99；§9 的问题按建议执行）· 三期都已实现并合并到 main，尚未发布（§10）· 2026-10-17 · 对应需求 R94（见 [requirements.md](requirements.md)）
>
> 来源：外部用户在 [issue #3](https://github.com/eziq1/golauncher/issues/3) 提出"Feature request: Translator plugin"，正文没有更多细节。本方案按启动器现有的能力补全需求。

## 1. 现状：已经有哪些"翻译"

| 已有功能 | 入口 | 局限 |
|---|---|---|
| 网页翻译（快速链接与网页搜索，R49） | `tr 文字`（Google 翻译网页）、`yd 文字`（有道） | 只是打开浏览器，结果不在启动器里 |
| AI 翻译（选中文字，R64、R65） | 选中文字后 Alt+Space →"翻译" | 要开 AI 对话；依赖 Claude/Codex 订阅；每次几秒 |

**缺的是**：在启动器里**直接看到译文**，像计算器一样边输边出结果，可以一键复制、粘贴或替换选中的文字，而且不依赖 AI 订阅。这就是本插件要做的。

## 2. 用起来的样子

| 输入 | 结果 |
|---|---|
| `tr hello world` | 自动识别源语言，译成界面语言；如果原文已经是界面语言，就译成英文（和 R65 的规则一致） |
| `tr ja 你好` / `tr >ja 你好` / `tr 日 你好` | 指定目标语言：语言代码、`>` 前缀、中文简称（中、英、日、韩、法、德、西、俄……）都可以 |
| `tr en>zh apple` | 同时指定源语言和目标语言 |
| `翻译 …`、`fy …` | 中文前缀，和 `tr` 等价 |
| 单个词，例如 `tr serendipity` | 引擎支持时，额外显示**词典**：音标、词性、几条释义 |
| 选中文字后 Alt+Space | "选中的文字"分组里多一行**即时译文**（不需要 AI），按 Enter 复制，或者粘贴替换原文 |

**结果列表**：每个启用的引擎一行，显示译文、引擎名和检测到的源语言；输入停顿约 300 毫秒后才发请求，继续输入会取消上一次请求，结果会缓存。

**动作**：

| 动作 | 快捷键 |
|---|---|
| 复制译文 | Enter |
| 粘贴到前一个窗口 | Ctrl+Enter |
| 替换选中的文字（从选中文字进入时） | — |
| 朗读译文或原文（见 §5） | — |
| 交换源语言和目标语言 | — |
| 在网页中打开（Google、DeepL、有道） | — |
| 用 AI 翻译（更准确，交给 AI 对话，见 §6） | — |

## 3. 翻译引擎

插件内置一个"翻译引擎"接口：输入文字、源语言、目标语言，返回译文、检测到的语言和可选的词典信息。默认引擎和备用引擎都可以在设置里调整，可以同时启用多个，分行显示结果对比。

| 引擎 | 需要 Key | 免费额度 | 优点 | 风险 |
|---|---|---|---|---|
| **Google 翻译（网页接口）** | 否 | 无官方额度（非公开接口） | 质量好，语言多，带词典 | **非官方接口**，随时可能变化或被限流；**中国大陆通常无法访问** |
| **MyMemory** | 否（可选填邮箱提高额度） | 匿名约每天 5,000 字符，带邮箱约每天 50,000 字符；单次请求约 500 字节 | 官方公开的免费接口，全球可访问 | 质量一般；额度小；长文要分段 |
| **DeepL API Free** | 是（免费注册） | 每月 500,000 字符（官方文档） | 质量很好 | 需要注册；支持的语言比 Google 少 |
| **Azure AI Translator（F0 免费层）** | 是（Azure 账号） | 第三方资料称每月 2,000,000 字符，**以官方为准** | 额度大，中国大陆可用 | 注册 Azure 稍麻烦 |
| **有道 / 百度 / 腾讯翻译** | 是（各自开放平台） | 各有免费额度，第二期再核实 | 中国大陆访问快，中文质量好，有道带词典 | 各家签名算法不同，要逐个实现 |
| **自建 LibreTranslate** | 视实例而定 | — | 完全自托管、离线 | 要用户自己部署，翻译质量一般 |

**默认配置（建议）**：开箱只启用不需要 Key 的引擎，Google 网页接口优先，访问失败时自动改用 MyMemory。在设置里填入 DeepL 或 Azure 的 Key 后，它们自动排到前面。中文界面下，可以提示用户"在中国大陆建议配置 Azure 或有道"。

**Key 的保存**：用已有的密码类偏好，存进 Windows 凭据管理器（macOS 用钥匙串），不写进 `config.json`，也不写日志。

## 4. 隐私

- **只有输入了前缀才发送**（`tr`、`fy`、`翻译`），和网页搜索建议的规则一致，普通输入永远不会发出去。
- **选中文字的即时译文**：默认关闭，在设置里打开后才会把选中的文字发给翻译引擎（因为它会在你没有主动选择"翻译"时就发送）。
- **只记长度和耗时**：不写日志，不进"最近使用"（`Item.Transient`）；翻译历史默认不保存（第三期可选）。
- **设置里写明每个引擎由谁提供**，以及文字会发到哪里。

## 5. 朗读（第二期）

- **Windows**：WinRT 的 `Windows.Media.SpeechSynthesis`，或者 SAPI，使用系统自带的语音包。
- **macOS**：`AVSpeechSynthesizer`，或者 `say` 命令。
- 只朗读你点了"朗读"的文字，不联网。

## 6. 和已有功能的关系

1. **前缀冲突**：网页搜索插件现在占用了 `tr`（Google 翻译网页）和 `翻译`（有道）。建议：
   - 翻译插件接管 `tr`、`fy`、`翻译`；
   - 网页搜索的 Google 翻译关键词改成 `gt`，`yd` 保留给有道网页；
   - 把这几个词加进保留关键词列表，由测试防止再次冲突。
2. **AI 翻译**：插件之间不能直接调用（见 m5-plugin-platform.md"插件之间怎么交互"）。"用 AI 翻译"需要一个宿主级的通用动作，例如 `ActionAskAI(指令, 文字)`，由宿主转交给 AI 插件，打开对话。这正是 R83 里提到的"宿主级通用动作"的第一个用例，放第二期。
3. **AI 工具层**：翻译动作是"只读"的（不改动本机，只是把文字发给翻译服务），AI 可以直接调用它，例如在对话里"把这段翻成日语"。

## 7. 实现要点

- **插件**：`builtin.translator`（`plugins/translator`），引擎实现放在插件内的 `engines/` 子包，每个引擎一个文件，接口统一。
- **网络**：统一的 HTTP 客户端（系统代理、超时 3 秒、取消），复用网页搜索建议里的防抖和取消逻辑。
- **语言**：语言代码表加中英文名称和别名；自动检测以引擎返回的结果为准；Unicode 码段只作为兜底。
- **长文本**：超过引擎单次上限时按句子分段，并发请求后再拼接（MyMemory 必须这样做）。
- **测试**：每个引擎都用 `httptest` 假服务器测请求格式、签名和解析；测语言解析（`tr en>zh`、`tr 日 …`）；测防抖和取消；测"没有前缀不发送"；glctl golden 用假引擎。真实网络测试只在设置 `GOLAUNCHER_NET_TESTS=1` 时运行。

## 8. 分期

| 期 | 内容 |
|---|---|
| **一** | 插件骨架；`tr`/`fy`/`翻译` 前缀和语言语法；Google 网页接口 + MyMemory（无 Key）+ DeepL（填 Key）；复制、粘贴、替换选中文字、在网页中打开；选中文字的即时译文（默认关闭）；前缀从网页搜索插件迁移 |
| **二** | Azure、有道、百度引擎；单词词典；朗读；宿主通用动作 `ActionAskAI`，实现"用 AI 翻译" |
| **三** | 翻译历史（可选，默认关闭）；自定义引擎（填一个 HTTP 模板）；本地模型（例如 Ollama）离线翻译 |

## 9. 需要你决定

1. 默认用不需要 Key 的 Google 网页接口（非官方）作为首选，可以接受吗？还是只用官方免费接口（MyMemory），让用户自己填 DeepL 或 Azure 的 Key？
2. 前缀迁移：翻译插件接管 `tr` 和 `翻译`，网页搜索里的 Google 翻译改用 `gt`，可以吗？
3. 中国大陆的用户比较多的话，有道、百度要不要提前到第一期？
4. 要不要在 issue #3 回复方案摘要，问一下提需求的人最在意什么（引擎、词典、朗读还是选中即译）？

## 参考

- DeepL API 用量与额度：<https://developers.deepl.com/docs/resources/usage-limits>
- Azure AI Translator 价格：<https://azure.microsoft.com/en-us/pricing/details/translator/>（F0 免费额度以官方页面为准）
- MyMemory 用量限制：<https://mymemory.translated.net/doc/usagelimits.php>

## 10. 实现记录

### 10.1 第一期（2026-10-17）

**做了什么**

- **插件**：`builtin.translator`（`plugins/translator`），引擎在 `plugins/translator/engines`，每个引擎一个文件，统一的 `Engine` 接口（`Translate(ctx, Request) (Result, error)`）；语言代码统一用 Google 的写法（`en`、`zh-CN`、`zh-TW`、`ja`……），每个引擎自己换算（DeepL 的 `ZH-HANS`、`EN-US`）。
- **前缀和语法**：`tr`、`fy`、`翻译` 后面必须是空格（含全角空格）或结尾，和网页搜索关键词的规则一样，所以“翻译器”“trello”不会触发。语法：`tr ja 你好`（代码或中文名称作第一个词）、`tr >ja 你好`（也认 `→`、`＞`、`->`）、`tr en>zh apple`、`tr 英>中 …`、`tr auto>fr …`。和英文单词重名的代码（`it`、`id`、`hi`）只在 `>` 或语言对里认，`tr it is raining` 仍然是翻译“it is raining”。只输入代码（`tr ja`）显示“输入要译成日语的文字”，不发请求；只输入中文名称（`tr 日语`）当作要翻译的词。
- **自动目标语言**：偏好设置“译成”，默认自动 = 界面语言；原文已经是它时译成英文，英文界面下的英文译成简体中文——和 builtin.ai 的“翻译”（R65）同一条规则，代码按 `plugins/ai/selection.go` 的 `translateTarget` 对齐复制了一份（插件之间不能互相 import）。**比 R65 多一步**：引擎检测到的源语言和字形猜测不同时，按检测结果再请求一次。例如英文界面下的 `tr bonjour`：拉丁字母先按英文处理要译成中文，Google 检测出是法语，就改为译成英文。
- **引擎**：
  - Google 网页接口：`translate.googleapis.com/translate_a/single?client=gtx&dj=1&dt=t`，短文本用 GET，超过 1,500 字节改用表单 POST，超过 4,500 字节分段；设置说明里写明“非官方接口，随时可能失效，中国大陆通常无法访问”。
  - MyMemory：`api.mymemory.translated.net/get`，可选邮箱（`de` 参数）；每段最多 500 字节，按句子、空格、字符边界依次切分，用 `Host.Go` 并发请求后按顺序拼接；源语言未知时先按字形猜（汉字、假名、谚文、西里尔字母），猜不出就用 `Autodetect`；额度用完（`quotaFinished`、429、“YOU USED ALL AVAILABLE FREE TRANSLATIONS”）、语言不支持（403 + LANGUAGE）分别报错。
  - DeepL：Key 以 `:fx` 结尾走 `api-free.deepl.com`，否则 `api.deepl.com`；`Authorization: DeepL-Auth-Key …`，JSON 请求体；403 → Key 无效，456/429 → 额度，400 且提到语言 → 不支持。
  - 偏好“翻译引擎”是一个列表，默认 `deepl, google`；需要 Key 的引擎没填 Key 时跳过，所以填了 DeepL Key 就自动排在第一。“其他引擎都失败时改用 MyMemory”默认开：Google 连不上时只显示 MyMemory 的那一行，副标题注明“Google 翻译不可用”。
- **防抖、取消、缓存**：停顿 300 毫秒后才发；新的文字取消上一次请求（未完成的结果丢弃）；每个引擎超时 5 秒；结果缓存在内存里（最多 64 条），窗口隐藏时取消进行中的请求，选中文字的缓存清空。输入和选中文字各用一个 runner，互不取消。
- **结果和操作**：每个引擎一行，标题是译文（单行，最多 200 字），副标题“引擎 · 源语言 → 目标语言”，详情里有全文和原文。操作：复制译文（Enter）、粘贴到前一个窗口（Ctrl+Enter）、交换源语言和目标语言（打开翻译视图，把译文反向翻译）、复制原文（Ctrl+Shift+C）、在 Google 翻译 / DeepL / 有道网页中打开。连不上、超时、额度、Key 无效等错误各有一行提示。
- **“翻译”命令**：打开同样的视图，搜索框就是输入框（支持同样的语法）。视图第一次渲染（带着文字打开，比如“交换语言”或 AI 的 `run_command` 带 query）**不防抖、等结果**，这样打开就是完整的；之后的输入照常防抖。这是为了让 AI 工具层（它对视图只等 300 毫秒的 Invalidate）拿到译文。
- **选中文字**：偏好“选中文字后立即翻译”默认关闭；打开后“选中的文字”分组里多一行即时译文（第一个答出来的引擎），Enter 复制，Ctrl+Enter 在可编辑时“替换选中的文字”、否则粘贴；还有“查看所有引擎的译文”。超过 2,000 字不翻译。关闭时 `SelectionItems` 直接返回空，不发送任何东西。
- **前缀迁移**：网页搜索的 Google 翻译关键词 `tr` → `gt`（保留 `translate`），有道的 `翻译` 去掉（保留 `yd`、`有道`），`tr`、`fy`、`翻译` 加进 `reservedPrefixes`，`TestBuiltinKeywords` 防止再冲突。用户自己改过的关键词保存在覆盖设置里，不会被改动。
- **隐私**：只有前缀后面的文字才发送；所有结果行 `Transient`（不进最近使用）；日志只记引擎、字数、耗时和错误类型，不记文字。
- **图标**：`internal/ui/icons.go` 加了 `languages`（Lucide）。

**测试**

- `engines_test.go`：每个引擎用 httptest 假服务器测请求格式（路径、参数、请求头、请求体）、解析、错误（额度、Key、语言、坏 JSON、空结果）、MyMemory 分段并发、DeepL Free/Pro 地址、超时；`Split`、`Canon`、`Guess`。
- `translator_test.go`：语法解析表、前缀、自动目标语言；“没有前缀不发请求”（包括选中文字在默认关闭时）；防抖和取消（假的计时器）、缓存；Key 和引擎顺序；回退到 MyMemory 和各种错误行；检测语言后改目标；中文界面；选中文字默认关闭、打开后的行和操作、隐藏后清空；视图第一次渲染不防抖；经过真实 HTTP（假 Google）的完整路径；离线；关闭后再 Init。
- glctl golden：`query_translate`、`query_translate_lang`、`zh_query_translate`、`run_translate`、`session_translate_copy`（假引擎回答 `[目标语言] 原文`，不联网）。
- 真实网络测试 `TestRealNoKeyEngines` 只在 `GOLAUNCHER_NET_TESTS=1` 时运行，只请求不需要 Key 的 Google 和 MyMemory。开发时各跑了一次：Google 的 GET 和表单 POST、MyMemory 的 `Autodetect` 都能正常解析，两者都返回了检测到的源语言。

**golden 的变化**（只有这些是预期的）：`run_web_engines`、`query_selection_typed` 里 Google 翻译的关键词变成 `gt, translate`、有道去掉了 `翻译`——这就是前缀迁移本身；另外新命令“翻译”出现在所有列出全部命令的 golden 里（`query_empty`、`zh_query_empty`、`query_selection*`、`query_context`、`query_processes_foreground`、`run_external_command`、`session_confirm_done`、`session_quicklink_create`），`query_selection_typed`（输入 `translate`）也多了这个命令，`plugin_list` 多了这个插件。

**出入**

- HTTP 客户端用 Go 默认的代理设置（环境变量），没有读取 Windows 的系统代理——仓库里还没有这样的工具函数，其他插件（汇率、网页搜索建议）也是这样。
- 默认引擎列表里没有 MyMemory 的独立一行，它是回退引擎（§3 的“访问失败时自动改用 MyMemory”）；想同时看两行就把 `mymemory` 加进列表。

### 10.2 第二期（2026-10-17）

**做了什么**

- **新引擎**（都需要在偏好设置里填 Key，没填就跳过；默认引擎列表变成 `deepl, azure, youdao, baidu, google`）：
  - Azure AI 翻译：`POST /translate?api-version=3.0&to=…[&from=…]`，请求头 `Ocp-Apim-Subscription-Key` 和（可选的）`Ocp-Apim-Subscription-Region`，中文用 `zh-Hans`/`zh-Hant`；错误码 401xxx → Key 无效，403001、429xxx → 额度，400035/400036 → 语言不支持。偏好：Key（密码）、区域（文本）。
  - 有道智云文本翻译：`POST /api` 表单，`signType=v3`，`sign = SHA-256(appKey + input + salt + curtime + 密钥)`，`input` 是原文（不超过 20 个字符时）或“前 10 个字 + 字数 + 后 10 个字”；`errorCode` 108/110/111/202 → Key，401/411/412 → 额度，102 → 语言。单个词返回的 `basic`（音标、`explains`）做成词典。偏好：应用 ID（文本）、应用密钥（密码）。
  - 百度翻译通用文本 API：`POST /api/trans/vip/translate` 表单，`sign = MD5(appid + q + salt + 密钥)`（用百度文档里的例子做了已知答案测试）；百度自己的语言代码（`jp`、`kor`、`fra`、`cht`……）双向换算；多行原文按行返回再拼接；`error_code` 52003/54001/58002/90107 → Key，54003/54004/54005 → 额度，58001 → 语言。偏好：APP ID（文本）、密钥（密码）。
  - 密钥只用来签名，不随请求发送（测试检查）。中文界面的“翻译引擎”说明里加了“在中国大陆建议配置 Azure 或有道”。
- **单词词典**：只对“一个词”查（没有空格、最多 40 个字符；中日韩文字最多 4 个字）。Google 网页接口加 `dt=bd`（按词性的译词）、`dt=md`（释义）、`dt=rm`（音标，取 `src_translit`）；有道用 `basic`。结果列表在各引擎译文之后多一行“词典”：标题“单词 /音标/”，副标题“词典 · 引擎 · 词性 释义”，详情里列出词性、译词和最多几条释义（每类最多 4 个词性、5 条），可以复制词条、朗读单词。真实请求确认过一次 Google 的字段（`definitions[].entry[].gloss`、`sentences[].src_translit`）。
- **朗读**：新包 `internal/winapi/tts`（`Speaker` 接口）。
  - **Windows 选 SAPI 5**（`ISpVoice`，COM 调用沿用 `internal/winapi/install` 的写法），不选 WinRT `SpeechSynthesizer`：WinRT 需要激活工厂、`IAsyncOperation` 回调和把音频流交给 `MediaPlayer` 播放，纯 syscall 写下来代码量大得多也更脆弱；SAPI 一个 COM 对象就能直接出声，不引入新依赖。用 SAPI XML 的 `<voice required="Language=411">` 按语言选语音，没有对应语音时用默认语音；异步朗读，每 50 毫秒 `WaitUntilDone` 检查一次是否取消，取消时 `Speak(NULL, SPF_PURGEBEFORESPEAK)` 立即停。**局限**：SAPI 只认经典 SAPI 注册表里的语音，Windows 设置里装的部分 OneCore 语音包它看不到，这时会用默认语音读。
  - **macOS 用 `say`**：文字从标准输入传（`say -f -`），不放在命令行参数里（其他进程能看到参数）；按 `say -v ?` 的列表挑对应语言的语音（zh-TW 依次找 zh_TW、zh_HK、任何中文语音），列表只读一次。
  - 插件里“朗读译文”“朗读原文”是 `ReadOnly` 的回调（AI 可以直接用，不改动任何东西），只在用户选择时朗读；新的朗读会停掉正在读的，插件关闭时也停。系统不支持时提示“这个系统不支持朗读”。测试用假的 Speaker，从不出声；glctl 的假环境用一个不出声的 Speaker。开发时在本机 Windows 上实际朗读过一次很短的词，确认能出声，也确认取消能在约 0.5 秒内停下来。
- **宿主通用动作 `ActionAskAI`**（R83 说的第一个用例）：
  - SDK（`pkg/plugin/askai.go`，新文件）：`ActionAskAI` 动作类型、负载 `AskAI{Instruction, Text, Title}`、接口 `AIAsker`。
  - 核心（`internal/core/askai.go`，新文件；`engine.go` 的 `Run` 只加了一个 case）：交给第一个启用的 `AIAsker`，它的 `Result` 按动作结果处理；AI 插件被关掉时提示“AI 不可用”。
  - AI 插件（`plugins/ai/askai.go`，新文件）：打开新对话，消息是指令加引用的原文（和选中文字的操作一样）；AI 总开关关着时先显示“打开 AI”的提示，打开后再发送。
  - 翻译插件的“用 AI 翻译”：指令 `Translate the quoted text into <语言>. Output only the translation.`，标题“翻译：原文开头”。
  - 对 AI 工具层来说它是“改动”类的动作（会把文字发给 AI），需要用户确认。
  - 文档：plugin-guide 新增“Asking AI: ActionAskAI”一节。

**测试**：Azure/有道/百度各自的 httptest 假服务器（请求格式、签名、语言代码、错误码、额度、没 Key 不发）、Google 词典解析、`IsWord`；插件里的 Key 组合（只填应用 ID 不算）、偏好传到真实引擎、词典行、朗读（只在选择时、取消、关闭、不支持）、“用 AI 翻译”的负载；核心的 `ActionAskAI` 路由（假的 AIAsker、AI 插件关掉、坏负载）；AI 插件收到后打开对话、AI 关着时不发送；glctl 会话 golden `session_translate_ai`（从翻译结果到 AI 对话的提示，再打开 AI 后发出的消息）。

**出入**

- 腾讯翻译没做（方案只写了“有道 / 百度 / 腾讯”是候选，R99 要求的是有道和百度）。
- 词典只用 Google 和有道；Azure 的词典接口（`/dictionary/lookup`）没接。
- 没有加“语音”偏好：按译文的语言自动选系统语音，暂时没有需要手动选的理由。

### 10.3 第三期（2026-10-17）

**做了什么**

- **翻译历史**（偏好“保存翻译历史”，默认关闭）：
  - 只在你**复制或粘贴**译文时记录（输入过程中的半截文字不记）：历史打开时，结果行的“复制译文”“粘贴”“替换选中的文字”变成插件回调，先记录再由 `Result.Do` 交给宿主复制或粘贴；历史关闭时它们仍是宿主的普通动作，什么都不经过插件。
  - 存在插件自己的本地存储（`Storage`）里，不进 `config.json`，不同步、不发送；最多 200 条，同一条译文再次使用时移到最上面。
  - 保留时间可选 1/7/30/90 天（默认 30 天），启动和设置变化时清理过期的；“清除历史”按钮（`PrefButton`）删除全部；**关闭历史会删除已保存的记录**。
  - “翻译历史”命令列出记录：复制、粘贴、重新翻译（打开翻译视图）、复制原文、从历史中删除（会先确认）；历史关闭时视图只显示“翻译历史已关闭”。
- **自定义引擎**（偏好“自定义引擎”，每行一个）：`名称 | GET 或 POST | 网址 | 译文的 JSON 路径 [| 请求体 [| 请求头]]`。`{text}`、`{from}`（未指定时是 `auto`）、`{to}` 在网址里按查询参数转义，在以 `{`/`[` 开头的请求体里按 JSON 字符串转义（并设 `application/json`），其他请求体按表单转义；JSON 路径支持 `data.translations.0.translatedText` 和 `[0].translations[0].text` 两种写法。默认排在内置引擎后面，也可以在“翻译引擎”列表里写 `custom:名称` 指定位置。写错的行跳过（日志只记行号和原因，不记内容，因为可能含 Key）。
- **本地模型（Ollama）**：引擎 `ollama`，用 Ollama 兼容 OpenAI 的 `/v1/chat/completions`（`stream: false`、`temperature: 0`，英文系统提示“只输出译文”）。偏好“Ollama 模型”是动态下拉框，打开设置页时向 `GET /api/tags` 要已安装的模型列表，连不上时在字段下面提示“…上没有运行 Ollama”；“Ollama 地址”默认 `http://localhost:11434`。没选模型时跳过；翻译前先检查服务器在运行且有这个模型（结果缓存 30 秒），不满足就显示“Ollama 不可用”这一行。本地模型可能要先加载，所以它的超时是 60 秒（其他引擎 5 秒）；完全离线，文字不离开电脑。默认引擎列表最后加上了 `ollama`（选了模型才会用）。

**测试**：`ParseCustom` 的正反例、GET/POST（JSON 和表单）的转义和请求头、`JSONPath`；Ollama 用 httptest 测模型检查（缓存、缺模型、没选模型、服务器没运行）、请求体和解析；插件里历史默认关闭（复制是普通动作、什么都不存）、打开后只在复制/粘贴时记录、排序、删除一条、保留期、清除按钮、关闭即删除；自定义引擎的位置；Ollama 的下拉选项和服务器没运行时的提示；glctl golden `run_translate_history_off`。

**golden 的变化**：新命令“翻译历史”出现在列出全部命令的 golden 里，`plugin_list` 多了新的偏好设置。

**出入**

- 自定义引擎的定义（包括写在请求头里的 Key）保存在配置文件里，不在凭据管理器中：偏好列表没有“密码”类型的列表；设置说明里提醒了这一点。
- 历史在“复制/粘贴时”记录，而不是“每次翻译完成时”：后者会把输入过程中的半截文字也记下来。
- Ollama 没有做流式输出：翻译一般很短，等完整结果更简单。

**需要你确认或在真机上试的**

- DeepL、Azure、有道、百度、Ollama 都只用假服务器测过（没有用真实 Key 请求，按 R99 的要求）。请各填一个 Key 试一次，特别是有道的 v3 签名和 Azure 的区域。
- 朗读：Windows 上看中文、日文是否选到了对应的语音（SAPI 只能看到部分系统语音）；macOS 上 `say` 的语音选择没有在真机上跑过。
- issue #3 的回复（R99 说“第一期完成后在 issue #3 回复进展”）没有发：子代理不对外发帖，需要你来发或者确认后再发。