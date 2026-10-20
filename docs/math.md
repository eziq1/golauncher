# Markdown 里的 LaTeX 公式（R114）

AI 的回答（以及插件的 Markdown 详情）常带 LaTeX 公式，例如
`\text{CAGR} = \left(\frac{\text{期末值}}{\text{期初值}}\right)^{\frac{1}{n}} - 1`。
MyGo 的 Markdown 解析器认得公式，但不排版：独立公式只留了 `Options.Math`
接口，行内公式没有接口，原来都显示原始 TeX。R114 让它们正常显示。

代码在 `internal/ui/mathview`，不依赖 golauncher 的其他部分，以后可以原样提给 MyGo。

## 1. 总体设计

```
TeX ──treeblood──> MathML 字符串 ──encoding/xml──> Node 树 ──Check──> Layout ──> Box ──Paint──> 屏幕
                                                     │              │ 不支持
                                                     └──TreeUnicode─┴──────> 整条显示成 Unicode 文本
```

- **解析**：[treeblood](https://github.com/wyatt915/treeblood)（MIT，纯 Go，v0.1.16）把
  TeX 转成 MathML。它只给字符串（内部 AST 的构造函数没导出），所以我们用
  `encoding/xml` 读成一棵小树（`mathview.Node`）。读之前去掉 `<annotation>`：treeblood
  把 TeX 原样抄进去，`&` 不转义，不是合法 XML。`\operatorname` 通过 treeblood 的宏补上
  （`\mathrm{#1}`）。
- **排版**：自己写 MathML → 盒子的排版，算法照 W3C MathML Core，常数用 Latin Modern
  Math 的 MATH 表（系统字体没有 MATH 表）。每个元素排成一个盒子：宽度、基线以上高度
  （ascent）、基线以下深度（descent），以及要画的东西（字形、细线、路径），坐标相对盒子
  的基线左端。整个公式是**一个**自绘元素：`ui.Box(...).Size(w, h).Draw(...)`，在
  `Draw` 里用 `Painter.Glyphs`（字形）、`Painter.Fill`（分数线、上划线）、
  `Painter.StrokePath`/`FillPath`（根号、伸缩括号、重音）绘制。这样基线对齐完全由我们
  控制，不依赖 MyGo 元素能否报告基线。
- **字体与颜色**：用 Markdown 主题的字体（默认是系统字体，所以 `\text{期末值}` 这样的
  中文正常显示）、字号的 1.1 倍和正文颜色；`\color{red}` 等支持几个常用颜色名和 `#rrggbb`。
- **度量**：字宽来自 MyGo 的 `ui.Shape`（系统文字引擎，含字体回退）。MyGo 不提供单个字形
  的墨迹高度，所以字形的上下伸展按字符类别估算（`ink`：x 高字母 0.45em、大写/数字约
  0.7em、带降部字母 0.21em 深、中日韩字 0.82/0.12em 等）。`Measurer` 是接口，测试用
  确定的假度量，不需要窗口。
- **缓存**：排好的公式按 (TeX, 字体, 字号) 缓存（最多 512 条，满了清空），每帧只是查表和重放
  绘制命令；Unicode 文本按 TeX 缓存。

## 2. 支持的子集

| 元素 | 做法 |
|---|---|
| `math`、`semantics`、`mrow`、`mstyle`、`mpadded` | 横排；`mstyle` 的 `displaystyle`、`scriptlevel`、`mathsize`、`mathvariant`、`mathcolor` 生效；`annotation` 忽略 |
| `mi`、`mn`、`mo`、`mtext`、`mspace` | 单个拉丁字母和小写希腊字母用斜体（MathML Core 的 mathvariant 规则），多字母（`sin`、`lim`）正体；`mathvariant="normal"/"bold"` |
| 运算符间距 | 关系符两侧 5/18em，二元运算符两侧 4/18em（在开头、运算符或左括号后当正负号，不加），逗号后 3/18em，大运算符后 1/6em，函数名（`\sin x`）后 1/6em；上下标里不加关系/二元间距 |
| `mfrac` | 以数学轴（0.25em）为中心，按 MathML Core 3.3.2 的 shift/gap 常数；显示样式下分子分母保持字号，文本样式下缩到下一级；`linethickness="0"`（`\binom`）不画线 |
| `msup`、`msub`、`msubsup` | MathML Core 3.4.1：上标抬高 0.363em（cramped 0.289em），下标下移 0.247em，上下标之间至少 0.16em；单个字形的底座不按墨迹下落，大运算符和复合底座会；斜体字母后的上标右移一点 |
| `munder`、`mover`、`munderover` | 显示样式下 ∑ ∏ ∫ lim 的上下限放在上下（UpperLimit/LowerLimit 常数）；文本样式下 `movablelimits` 的改成上下标；`\hat` `\check` `\tilde` `\bar` `\vec` `\dot` `\ddot` `\acute` `\grave` `\breve` `\overline` `\underline` `\overrightarrow` `\overleftarrow` `\overbrace` `\underbrace` 用路径或细线画（单个字母上的重音按字母宽度，多个字母的跨整个底座） |
| `msqrt`、`mroot` | 根号用折线画，高度随被开方式增长；根指数在第二级字号，按 RadicalKernBefore/After 和 DegreeBottomRaise 60% 放置 |
| `mtable`/`mtr`/`mtd` | `pmatrix`、`bmatrix`、`vmatrix`、`cases`、`aligned` 等：列宽取最大，单元格按 `columnalign`/`text-align` 对齐，用 treeblood 给的 padding 作列距（表格外侧的去掉），否则列距 0.8em；行有 0.8/0.3em 的支柱高度；整个表格以数学轴居中 |
| 伸缩括号 | `\left…\right` 和矩阵两边的 `( ) [ ] { } | ‖ ⟨ ⟩ ⌊ ⌋ ⌈ ⌉ /`：同一行里其他内容的高度决定括号高度，关于数学轴对称（同 TeX）；不到 1.2em 时用字体的字形，更高时画成路径。`\big(`、`\Big(` 等是固定高度 |
| 大运算符 | 显示样式下 ∑ ∏ 等放大 1.45 倍、∫ 放大 1.8 倍，墨迹以数学轴居中 |
| `mphantom` | 占位不画 |
| `menclose` | box、circle（画成框）、各种删除线、上下左右线 |

字号：第一级上下标 71%，第二级及以下 50%（MathML Core 的 scriptlevel，比例取 Latin
Modern 的 ScriptPercentScaleDown/ScriptScriptPercentScaleDown）。

## 3. 退回 Unicode 文本

treeblood 解析失败（括号不配对等），或者 MathML 里有排版不支持的元素（`merror`——即
treeblood 不认识的命令，如 `\boxed`、`\tag`、`\xrightarrow`；`mmultiscripts`、
`mlabeledtr`、换行的 `mspace` 等），**整条公式**显示成可读的 Unicode 文本，不会出现
一半排好一半原样的情况：

- 有 MathML 树时从树生成（`TreeUnicode`）：`\left(\frac{\text{期末值}}{\text{期初值}}\right)^{\frac{1}{n}} - 1`
  → `(期末值 / 期初值)^(1/n) − 1`；希腊字母、运算符是对应字符；上下标的每个字符都有上标/下标
  字符时用它们（`x²`、`aₙ₊₁`、`e⁻ˣ`），否则写成 `^(…)`/`_(…)`；`√(a² + b²)`、`∛x`；
  矩阵 `(1, 2; 3, 4)`；重音用组合字符（`x̂`）。
- treeblood 失败时从 TeX 生成（`TeXUnicode`）：一个小命令表加上 `\frac`、`\sqrt`、上下标
  和环境的简单处理，尽力而为。这是唯一一处我们自己读 TeX 的地方。

这段 Unicode 文本也是排好的公式给读屏软件的名字（`Label`）。

## 4. 行内公式

MyGo 对行内公式没有接口，且不改 MyGo。做法（`mathview.Markdown`）：交给
`markdown.View` 之前，用 **MyGo 自己的解析器**（`parser.Document`）找出行内的
`parser.Math`，把源文本里这一段换成它的 Unicode 文本；ASCII 标点加反斜杠转义，前后加
不可见标记 U+2063/U+2064（复制时据此找回 TeX）。代码块里的 `$`、转义的 `\$`、被判断为
金额的 `$5` 不是公式，解析器不会给出，保持原样。段落里写在一行中间的 `$$…$$` 也按行内处理。

golauncher 打开了 `parser.ExtDollarMath`（单个 `$` 的行内公式，语言模型常这样写；MyGo
默认只认 `\(…\)`），解析器对金额有判断（`$` 后是空格或数字的不算）。

**开销**：`parser.Document` 只重新解析改动的部分（流式回答只在末尾追加），每条公式的
Unicode 文本有缓存，源文本不变时直接返回上一帧的结果。基准测试
（`BenchmarkInlineStreaming`，40 段、每段一条公式、每帧追加几个字符）在一台台式机上约
4 µs/帧，源文本不变时约 2 ns。所以流式回答时每帧都做，不需要等回答结束。

## 5. 复制

- **复制整条回答**（AI 聊天的“复制”动作）复制的是原始回答，本来就是 TeX，不受影响。
- **选中复制**：
  - 排好的独立公式里放了一段看不见的文本 `$$TeX$$`（透明、1 DIP 字号、绝对定位），所以
    选中跨过公式时复制出 `$$…$$`。
  - 没有行内公式时，选择和复制完全交给 MyGo 的 Markdown（文本、HTML、Markdown 三种格式）。
  - 有行内公式时，替换后的源文本会让 MyGo 的复制得到 Unicode 文本，所以
    `mathview.Markdown` 让 Markdown 共享选择（`ShareSelection`），自己在外面包一层可选择的
    容器：Ctrl+C 把选中的文本按标记换回原来的 `$…$`（选区从公式中间开始或结束时也换回整条），
    以纯文本和 Markdown 两种格式放到剪贴板；右键“复制为 Markdown”和 Shift+Ctrl+C（选中
    时）同样处理，没有选中时复制完整的原始 Markdown。

## 6. 已知不足

- 字形高度是按字符类别估算的，不是字体的真实墨迹，个别字体下上下标、分数线间距会和
  TeX 略有差别；系统字体没有 MATH 表，∑ ∫ 只是放大的普通字形，不是专门的大号字形。
- 伸缩括号画成等粗的线条，不像 TeX 字体那样有粗细变化。
- 不支持：带编号的公式（`\tag`）、`\boxed`、`\xrightarrow`、`\stackrel`、
  `mmultiscripts`/`\sideset`、公式内换行（`\\` 在环境外）——这些整条退回 Unicode 文本。
- treeblood 自身的问题会照搬：`\tfrac12` 会被读成 `12` 加下一项；`\color{red}{x}` 会把后面整段都染色。
- 有行内公式时，选中复制只提供纯文本和 Markdown（没有 HTML），段落之间用一个换行连接。
- 公式不能在中间折行；比视图宽时可以横向滚动。
- 行内公式只是 Unicode 文本，分数、矩阵等在行内读起来不如排版好；想要排版效果需要
  MyGo 提供行内公式接口（R114 决定暂不提）。
- 不可见标记 U+2063/U+2064 依赖文字引擎把“默认可忽略字符”画成空白（DirectWrite、Core
  Text 都是这样）。

## 7. 测试

- `unicode_test.go`：Unicode 退回文本（用户的例子、希腊字母、上下标、根号、求和、矩阵、
  未知命令），以及 TeX 直接转换。
- `tree_test.go`：treeblood 输出 → 树；不支持的元素被 `Check` 拒绝；annotation 里的 `&`。
- `layout_test.go`：假度量下分数、嵌套分数缩小、上下标、根号、伸缩括号、表格居中、
  上下限、运算符间距、重音，以及不支持时整条退回。
- `markdown_test.go`：行内替换（代码块、转义、金额不动）、标记还原（含部分选中）、在
  `ui.Tester` 里全选复制得到 TeX，以及基准测试。
- `internal/ui/math_test.go`：AI 聊天里回答带独立公式和行内公式，排好、不显示原始 TeX，
  “复制”动作得到 TeX。
- `png_test.go`：设置 `MATHVIEW_PNG=文件路径` 时把示例公式画成 PNG 方便目测（图片不入库）。
