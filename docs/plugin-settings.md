# 插件设置页的集成方式

对应问题：R9（见 [requirements.md](requirements.md)）。

> 第三方开发的插件，它的配置页面能够集成到 golauncher 的设置页面中吗？是通过什么方式集成的？

## 1. 结论

可以集成，方式是**声明式**的：插件不画设置界面，只在 manifest 里声明"有哪些设置项"，golauncher 的设置页按声明自动生成表单。内置插件、Go 写的插件，以及将来其他语言写的外部插件，都用这一套机制。

## 2. 当前机制（已实现）

插件在 manifest 的 `preferences` 里声明设置项：

```go
Preferences: []plugin.Preference{
    {Name: "maxResults", Type: plugin.PrefNumber, Title: "%pref.maxResults.title%", Default: ...},
    {Name: "apiKey",     Type: plugin.PrefPassword, Title: "%pref.apiKey.title%"},
    {Name: "mode",       Type: plugin.PrefSelect, Options: []plugin.Option{...}},
},
```

设置页"插件"里，每个插件有一个详情页，内容如下：

| 区块 | 来源 |
|------|------|
| 偏好表单 | 由 `preferences` 声明自动生成，支持的类型有 `text`、`password`、`number`、`bool`、`select`、`stringList`、`path`、`hotkey`。标题和说明来自插件自己的翻译文件，所以也能多语言 |
| 命令设置 | 每个命令的别名、全局快捷键、是否出现在根搜索里。由宿主统一提供，插件不需要写任何代码 |
| 基本信息 | 版本、权限。插件不可用时显示原因 |

数据流：

- 插件用 `Host.Preferences()` 读取设置，用户没设置的项回退到声明里的 `Default`。
- 用户修改设置后，插件会收到 `config-changed` 事件。
- `password` 类型的值存在 Windows 凭据管理器里，不写进 `config.json`。
- 外部插件（M5）在 `plugin.json` 里声明同样的 `preferences`，设置值通过 JSON-RPC 传给插件。

## 3. 为什么不让插件自己画设置界面

理由和插件结果只返回数据、不画 UI 一样：

- 外部插件跑在独立进程里，没办法往宿主的窗口里画东西；
- 所有插件的设置页外观和主题保持统一；
- `glctl` 和测试可以直接读取、检查设置项。

## 4. 局限和扩展方向（📋 已记录，计划和 M5 外部插件一起做）

现在的声明式表单表达不了这些场景：

- OAuth 这类需要"登录 / 授权"按钮的流程；
- 选项要动态加载的下拉框，比如先连接服务器，再列出项目；
- 表格类的编辑，以及"选了 A 才显示 B"这种联动。

计划在声明式框架内扩展，不开放自绘：

1. **按钮型设置项**：点击后回调插件的 `HandleAction`，用于"登录"、"测试连接"之类的操作。
2. **动态选项**：`select` 的选项由插件在运行时提供。
3. **分组和条件显示**：设置项可以分组，也可以按其他设置项的值决定是否显示。
4. **`Form` 视图**：插件返回一个表单描述，由宿主嵌进设置页渲染。这是最灵活的一种。
