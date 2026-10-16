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

## 4. 实现记录

> 2026-10-15 · 分支 `worktree-agent-a979000b80992d161`

### 4.1 代码位置

| 位置 | 内容 |
|------|------|
| `internal/winapi/network` | 系统调用：`GetAdaptersAddresses`（含网关）、wlanapi（`WlanQueryInterface` 当前连接、`WlanGetProfileList`、`WlanGetProfile`）、`IcmpSendEcho2` / `Icmp6SendEcho2`、`GetExtendedTcpTable` / `GetExtendedUdpTable`（监听中的 TCP 和已绑定的 UDP，IPv4 和 IPv6）、进程名和路径、结束进程。解析逻辑（端口表、Wi-Fi 配置 XML、MAC 格式）与系统调用分开，可以在任何平台上用 fixture 测试；非 Windows 平台返回 `errors.ErrUnsupported`。 |
| `plugins/network` | 插件。`Backend` 接口把系统和网络访问放在后面：`System` 是真实实现，`FixtureBackend` 读 `testdata/fixtures/network.json`，供 `glctl` 假环境和测试使用。 |
| `internal/builtin/builtin.go` | `All` 里 `network.New()`，`Fake` 里 `network.NewFake(fixture(dir, "network.json"))`。 |
| `internal/ui/icons.go` | 新图标 `network`、`wifi`、`globe`。 |

### 4.2 交互

- 根搜索前缀（可在偏好里改，留空即关闭）：`ip`、`ping`、`dns`、`port`、`wifi`、`tracert`。前缀后面必须是空格或结尾，所以 `ipconfig`、`portal` 不会触发。
- **只读本机信息的工具直接在根搜索里出结果**：`ip`（网卡、Wi-Fi、公网 IP）、`port 8080`（占用进程）、`wifi`（已保存网络）。`ip`、`wifi` 后面的文字用来过滤。
- **要发网络请求的工具在根搜索里只显示一条"要做什么"的条目，按 Enter 才执行**：`ping example.com` → 打开 Ping 视图；`dns example.com @1.1.1.1` → 打开 DNS 视图；`port example.com:443` → 打开端口检测视图；`tracert example.com` → 后台任务。这样逐字输入时不会对半截的主机名发请求，也满足"不带前缀不干扰普通搜索"。公网 IP 例外：设计要求输入 `ip` 时获取，所以输入 `ip` 时在后台查询，到达后 `Invalidate`。
- **Ping 视图**：每个视图实例一个会话，循环跑在 `Host.Go` 上：先解析主机名（优先 IPv4），然后每秒一次 `IcmpSendEcho2`（超时 2 秒），每次结果都 `Host.Render` 推送整个视图。第一行是汇总（已发送、已接收、丢包率、最小/平均/最大），下面是回复（最新的在上面，最多显示 100 条，统计算全部）。默认次数来自偏好 `pingCount`（默认 4）；"持续 Ping"（`Ctrl+Enter`）一直 ping 到停止、关闭视图（`ViewCloser`）、隐藏启动器或插件关闭。动作：停止 / 再 Ping 一次 / 持续 Ping / 复制结果。
- **DNS**：A、AAAA、CNAME、MX、TXT、NS 并行查询（每种类型一个 `Host.Go`），整体超时 5 秒；输入 IP 时做反向查询（PTR）。`@server` 支持 IP、`[IPv6]`、主机名，可带端口，默认 53；指定服务器时用 `net.Resolver{PreferGo: true, Dial: …}`。成功的结果缓存 1 分钟（视图被重新拉取时不重复查询），失败不缓存。视图用 `FilterLocally`，在视图里输入只过滤记录。
- **端口**：`port 8080` 按"进程 + 端口"分组显示（同一进程在 `0.0.0.0` 和 `[::]` 上监听只占一行）；`port`（不带参数）可以打开所有监听端口的列表。结束进程的动作是 `StyleDestructive`，由宿主确认；执行前再核对一次 PID 对应的进程名，PID 已被别的进程复用时不结束。PID 0、4 和启动器自己不提供结束动作。（R52 之后：进程名、路径和结束进程改由 `internal/winapi/process` 实现，动作数据带进程的启动时间，在结束用的同一个句柄上核对，并拒绝系统关键进程，见 [process-manager.md §5.4、§15](process-manager.md)。）远程检测用 TCP 连接，超时 3 秒，显示耗时和实际连接的地址。
- **后台任务**：`tracert <host>`（打开任务视图），"刷新 DNS 缓存"（`ipconfig /flushdns`），"续租 IP"（`cmd.exe /d /c "ipconfig /release && ipconfig /renew"`，命令标为 `Destructive`，宿主先确认）。两个 ipconfig 任务在同一个队列 `network.ipconfig` 里串行。
- **输入校验**：主机名按 RFC 1123（每段 1–63 个字母数字和连字符，总长 ≤ 253，不能全是数字，DNS 名称允许下划线）；IP 用 `netip.ParseAddr`；端口 1–65535。不合法时显示红色提示条目，不带任何会发请求的动作。以 `-` 开头的输入不是合法主机名，所以不会被当成 `tracert`/`ping` 的参数。

### 4.3 公网 IP 接口核实（2026-10-15）

| 接口 | 结果 |
|------|------|
| `https://api.ipify.org?format=json` | 200，`application/json`，`{"ip":"…"}`；免费、不需要 Key。只有 IPv4。 |
| `https://api64.ipify.org?format=json` | 200，`application/json`；双栈，用来取 IPv6。 |
| `https://ifconfig.me/ip` | 200，`text/plain`，只有地址；免费、不需要 Key。 |

- IPv4 查询强制走 `tcp4`，IPv6 查询强制走 `tcp6`，所以双栈接口会返回对应协议族的地址；返回的地址族不对就当作失败，换下一个接口。
- IPv4：先 ipify，失败再 ifconfig.me。IPv6：先 api64.ipify.org，失败再 ifconfig.me；没有 IPv6 是正常情况，界面显示"没有公网 IPv6 地址"。
- 缓存 5 分钟；失败结果缓存 30 秒（避免每次按键都重试）；"重新查询公网 IP"动作（`Ctrl+R`）立即重查。偏好 `publicIP` 可以完全关闭。

### 4.4 Win32 结构体核实

- `ICMP_ECHO_REPLY`：64 位进程收到的是原生布局（`Data` 是 8 字节指针），`IP_OPTION_INFORMATION.Ttl` 在偏移 24（32 位是 20，代码用 `16 + sizeof(uintptr)`）。实测 ping `127.0.0.1` 得到 TTL 128。
- `ICMPV6_ECHO_REPLY`：`IPV6_ADDRESS_EX` 是 1 字节对齐（26 字节），状态在偏移 26、RTT 在 30。实测 ping `::1` 成功。
- `WLAN_CONNECTION_ATTRIBUTES`、`WLAN_ASSOCIATION_ATTRIBUTES` 只有 4 字节字段和字节数组，Go 的自然对齐与 C 一致，amd64 和 arm64 布局相同；`WLAN_INTERFACE_INFO`（532 字节）和 `WLAN_PROFILE_INFO`（516 字节）按固定步长读取。`GOARCH=arm64 go vet` 通过。
- 端口表的行只含 DWORD 和字节数组，按字节解析，端口取 DWORD 低两字节（网络字节序）。

### 4.5 Wi-Fi 明文密码核实

在 Windows 11、**非管理员**（Medium 完整性级别）下用 `GOLAUNCHER_NETWORK_TESTS=1` 的只读测试验证（测试只记录"是/否"，不输出密码或网络名）：

- `WlanGetProfile` + `WLAN_PROFILE_GET_PLAINTEXT_KEY`：**能**取到明文密码（3 个 WPA2-PSK 配置都可以）。
- `netsh wlan export profile key=clear`：**也能**取到，内容与上面相同。

所以按设计保留三级回退：先 `WlanGetProfile`；`keyMaterial` 仍是加密的（`protected=true`）或调用被拒绝时，用 `netsh wlan export profile name="…" key=clear folder=<私有临时目录>`（导出的 XML 与系统语言无关，比解析 `netsh show profile` 的本地化输出可靠；读完立即删除临时目录）；两种都拿不到就提示"需要管理员权限"。开放网络提示"没有密码"，企业网络（802.1X，没有 `sharedKey`）提示"没有保存密码"。

密码只在执行"复制密码"时读取，写入剪贴板后弹出"已复制 X 的密码"，界面、提示、日志和测试输出里都不出现密码。

### 4.6 测试

- `internal/winapi/network`：端口表、配置 XML、MAC 解析（跨平台 fixture）；手工构造的 `IpAdapterAddresses` 链表转换；真实读取网卡（必须有 loopback）；ping `127.0.0.1` 和 `::1`；本进程开 TCP/UDP 端口后查到自己是占用者；只结束测试自己启动的子进程（`PING.EXE -n 30 127.0.0.1`）。Wi-Fi 和 ping 不可达地址只在 `GOLAUNCHER_NETWORK_TESTS=1` 时运行。
- `plugins/network`：一个"离线"后端在任何网络请求时让测试失败，用来验证不合法输入和不带前缀的输入都不发请求；fixture 后端测试 `ip`、`wifi`（复制密码、开放网络、需要管理员）、端口占用和结束进程（含 PID 复用保护）、后台任务的命令行；`httptest` 测试公网 IP 的回退、5 分钟缓存、失败重试；本地 UDP DNS 桩服务器测试全部记录类型、NXDOMAIN、PTR，以及服务器无响应时按类型报错且不缓存；本地监听端口测试远程端口检测；脚本化的假 ping 测试统计、超时、不可达、持续模式的停止、关闭视图和隐藏窗口；真实 ping 只 ping `127.0.0.1`。
- `glctl` golden：`query ip`、`query "port 8080"`（fixture）、`query "ping exa..mple"`（不合法输入）、会话里 `dns example.com` → Enter（假解析器）。

### 4.7 与设计的差异和遗留问题

- 发网络请求的工具（ping、DNS、远程端口、tracert）在根搜索里按 Enter 才执行，见 §4.2。
- 公网 IPv6 用 `api64.ipify.org`（设计只写了 `api.ipify.org`，它只有 IPv4）。没有显示地区：ipify 免费接口不提供地区，需要另一个服务，暂不做。
- 复制 Wi-Fi 密码：集成时（R37）给 SDK 加了 `Clipboard.WriteSecret`，这里改用它。除了文本，还写入 `ExcludeClipboardContentFromMonitorProcessing`、`CanIncludeInClipboardHistory = 0`、`CanUploadToCloudClipboard = 0` 三个格式，所以剪贴板历史插件、Windows 的 Win+V 历史和云剪贴板都不会记下这条密码。外部插件通过 `host/clipboard.writeText` 的 `secret: true` 参数使用。