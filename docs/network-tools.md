# 网络工具插件设计方案

> 状态：设计 v1 · 2026-10-15 · 对应需求 R31（见 [requirements.md](requirements.md)）

内置插件 `builtin.network`（`plugins/network`），manifest 声明 `network` 权限。各个工具既是命令（可以带参数），也可以在根搜索里用前缀直接调用。

## 1. 工具

| 工具 | 根搜索写法 | 内容 | 实现 |
|------|------------|------|------|
| 本机 IP | `ip` | 每个网卡的 IPv4 和 IPv6、MAC、网关、DNS，以及当前连接的 Wi-Fi 名称和信号强度。动作：复制地址 | `GetAdaptersAddresses`（x/sys/windows）；Wi-Fi 信息用 `WlanQueryInterface` |
| 公网 IP | `ip`（同一个视图里） | 公网 IPv4（和 IPv6，如果有）；可选显示地区 | `https://api.ipify.org?format=json`，备用 `https://ifconfig.me/ip`。都免费、不需要 Key，结果缓存 5 分钟。在后台获取，到达后再刷新列表 |
| Ping | `ping <host>` | 实时逐条显示每次回复的延迟，最后给出统计（丢包率、最小/平均/最大）。可以选择持续 ping，直到关闭视图 | `IcmpSendEcho2`（iphlpapi，不需要管理员权限）。每次结果用 `Host.Render` 推送到视图 |
| DNS 查询 | `dns <domain> [@server]` | A、AAAA、CNAME、MX、TXT、NS 记录，可以指定 DNS 服务器 | Go 的 `net.Resolver`；指定服务器时自定义 `Dial` |
| 端口检测 | `port <host>:<port>` | 远程端口能否连通，以及连接耗时 | TCP 连接，超时 3 秒 |
| 本地端口占用 | `port <port>` | 哪个进程在监听这个端口（进程名、PID、路径）；可以结束进程（需要确认） | `GetExtendedTcpTable` / `GetExtendedUdpTable`，结果包含进程 ID |
| Wi-Fi 密码 | `wifi` | 已保存的 Wi-Fi 列表；动作"复制密码"。**密码默认不显示在界面上** | `WlanGetProfile` 加 `WLAN_PROFILE_GET_PLAINTEXT_KEY`。实现时要先核实非管理员能否取到明文密码；取不到就改用 `netsh wlan show profile name=… key=clear`，仍然取不到就提示"需要以管理员身份运行" |
| 路由追踪 | `tracert <host>` | 作为后台任务执行（R24），在任务视图里看输出 | `RunProcess(tracert)` |
| 刷新 DNS 缓存、续租 IP | 命令 | `ipconfig /flushdns`、`ipconfig /release` 加 `/renew`（续租需要确认），作为后台任务执行 | `RunProcess` |

- **前缀**：`ip`、`ping`、`dns`、`port`、`wifi`、`tracert`，可以通过偏好修改。不带前缀的输入不返回结果，不干扰普通搜索。
- **输入校验**：主机名、IP、端口号格式不对时，显示提示条目，不发起任何网络请求。

## 2. 隐私与安全

- **公网 IP** 要访问外部服务，只在用户输入 `ip` 时才获取，不在后台定期请求。
- **Wi-Fi 密码**：只在用户执行"复制密码"动作时才读取明文，复制完弹提示，界面上不直接显示密码。
- **结束进程**需要确认。

## 3. 测试

- **网卡信息**：解析逻辑和系统调用分开，系统调用放在接口后面；用 fixture 测试解析。
- **Ping**：`IcmpSendEcho2` 放在接口后面，测试用假实现，覆盖超时、目标不可达、统计计算。真实的 ping 只 ping `127.0.0.1`。
- **DNS 和端口检测**：用本地的 DNS 服务器和本地监听端口模拟，不依赖外网。
- **本地端口占用**：测试自己在本地开一个端口，然后验证能查到是本进程在监听。
- **公网 IP**：用 `httptest` 模拟接口；真实请求只在设置 `GOLAUNCHER_NETWORK_TESTS=1` 时运行。
- **Wi-Fi**：只读真实测试只在设置 `GOLAUNCHER_NETWORK_TESTS=1` 时运行，**不在日志或测试输出里打印任何密码**。
- `glctl` golden：`ip`（fixture）、`dns example.com`（假的解析器）、输入不合法的情况。
