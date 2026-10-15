# 需求台账

记录用户提出的每一项需求、问题和决定，以及对应的方案文档和状态。新需求一律先登记在这里，再动手。

状态说明：✅ 已完成 · 🚧 进行中 · ⏳ 待确认 · 📋 已记录，未排期

| # | 日期 | 需求 / 问题 | 方案 / 结论 | 状态 |
|---|------|-------------|-------------|------|
| R1 | 2026-10-14 | 用 Go + MyGo 做类 Raycast 的 Windows 启动器，目标有五个：类 Raycast 界面；完善的插件系统，自带功能也走插件；配置界面；供 Agent 做 UT 的 CLI；自带应用启动器、计算器、剪贴板管理器 | [design.md](design.md) | ✅ |
| R2 | 2026-10-14 | 方案统一放在 `docs/` 目录 | 本目录；R16 进一步要求每个需求都登记 | ✅ |
| R3 | 2026-10-14 | 四项决定：默认呼出键 `Alt+Space`；剪贴板历史不加密；外部插件不需要签名；第一版只做英文，但整体按可扩展的 I18N 设计 | [design.md §14](design.md)、§10 | ✅ |
| R4 | 2026-10-14 | 按第一版开始实现 | [design.md §12、§15](design.md) | ✅ |
| R5 | 2026-10-14 | 实现时拆分需求，用多个 Agent 并行加速 | 开发流程：先写好公共契约，再按互不重叠的目录分派子代理，最后统一集成和验证 | ✅ |
| R6 | 2026-10-15 | 启动器顶部有一块蓝色，像是 Windows 标题栏留下的 | 原因：DWM 在延伸到客户区的框架上画了标题栏强调色。修复：设置 `DWMWA_CAPTION_COLOR = DWMWA_COLOR_NONE`，见 [reliability.md §4](reliability.md) | ⏳ 已修复，等你在真机上确认 |
| R7 | 2026-10-15 | 好好整理，作为 0.1 版本发布到 GitHub：仓库 `eziq1/golauncher`，MIT 许可，英文 README 加中文 README，用 gh 创建 | [release.md](release.md) | 🚧 仓库已推送；v0.1.0 的发布被暂停，修完崩溃后重新发布 |
| R8 | 2026-10-15 | 集成 Everything 搜索电脑上的文件：作为 Windows 特有插件，依赖系统已安装的 Everything，没装就不启用 | [everything.md](everything.md) | ✅ |
| R9 | 2026-10-15 | 问：第三方插件的配置页能集成进 golauncher 的设置页吗？怎么集成？ | [plugin-settings.md](plugin-settings.md)：声明式集成，宿主生成表单；扩展方向列入路线图 | ✅ 已答复；扩展项 📋 |
| R10 | 2026-10-15 | 问：数据库 schema 支持迁移吗？以后怎么升级？ | [storage.md](storage.md)：三处存储都有版本化迁移；主数据库的迁移是这次补上的 | ✅ |
| R11 | 2026-10-15 | 本机运行时，文件搜索导致程序崩溃 | [reliability.md §1](reliability.md)：视图返回空分组时选中下标为 -1，底栏越界。已修复，并加了回归测试 | ✅ |
| R12 | 2026-10-15 | 问：怎么才能逮住这种崩溃？ | [reliability.md §2](reliability.md)：`debug.SetCrashOutput` 把崩溃调用栈写进日志 | ✅ |
| R13 | 2026-10-15 | 用子 Agent 调研 Raycast 扩展仓库（raycast/extensions），看能否直接集成它们的扩展 | [raycast-extensions-research.md](raycast-extensions-research.md) | 🚧 调研中 |
| R14 | 2026-10-15 | 在主程序和插件之间加一层防护：插件崩溃时主程序不能崩 | [reliability.md §3](reliability.md)：进程内防护（recover、`Host.Go`、事件分发、超时）；彻底隔离靠 M5 外部插件 | ✅ 进程内防护已完成；进程隔离随 M5 外部插件 📋 |
| R15 | 2026-10-15 | 自安装：第一次打开时弹出安装界面，默认装到用户程序目录；已安装时比较版本，不一致就提示升级或降级，一致就跳过 | [self-install.md](self-install.md) | 🚧 子代理实现中 |
| R16 | 2026-10-15 | 每个新需求和每个方案都要落到 docs 里 | 本台账，以及每项对应的方案文档 | ✅ 持续执行 |
