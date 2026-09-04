# DNS 解析配置（dns）

`dns` 段配置 TVGate 内置 DNS 解析器的上游服务器与查询参数。TVGate 访问上游域名（IPTV 源站、上游代理服务器、订阅地址、GitHub API 等）前都要先解析 IP；在运营商内网 DNS 污染、安卓设备系统 DNS 不可靠等场景下，显式指定上游 DNS 可以显著提升解析成功率与速度。

TVGate 的解析器为单例，采用**三级解析链**逐级兜底：

1. **已配置 DNS 服务器**：按 `dns.servers` 列表顺序逐个尝试，任一成功即返回结果。支持普通 UDP/TCP、DoT、DoH、DoH3、DoQ、DNSCrypt 多种协议形式。
2. **系统解析**：配置的服务器全部失败后，回退到系统解析器（Go `net.Resolver`，未强制纯 Go 模式）。在 Android 上走系统 netd / libc `getaddrinfo`（cgo 链路），可兼容 `/etc/resolv.conf` 为空的安卓环境——这也是不开启 `PreferGo` 的原因。
3. **内置公共 DNS 兜底**：系统解析也不可用时（如安卓纯 Go 环境下 resolv.conf 缺失），用内置公共 DNS `223.5.5.5`、`119.29.29.29` 发起 UDP 查询做最后尝试（返回 IPv4 地址），避免解析彻底失败。

配置文件热重载后，DNS 解析器会自动按新配置重建，无需重启进程。

## 配置段

```yaml
dns:
  servers:                                  # DNS 服务器列表，按序尝试；支持以下写法：
    - 192.0.2.1                             # 纯 IP = 普通 UDP 查询（等价 udp://192.0.2.1）
    - tcp://192.0.2.1                       # TCP 查询
    - tls://dns.example.com                 # DoT（DNS over TLS）
    - https://dns.example.com/dns-query     # DoH（DNS over HTTPS）
    - h3://dns.example.com                  # DoH3（HTTP/3 承载的 DoH）
    - quic://dns.example.com                # DoQ（DNS over QUIC）
    - sdns://AQcAAAAAAAAADT...              # DNSCrypt 服务器 stamp（以 sdns:// 开头）
  timeout: 5s                               # 单次 DNS 查询超时
  max_conns: 10                             # 最大连接数（作用于 DoH/DoH3/DoQ 客户端）
```

## 字段说明

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| servers | []string | 空 | 上游 DNS 服务器列表，按序尝试；留空表示不配置上游，直接走系统解析 → 内置公共 DNS 兜底的链路 |
| timeout | duration | 5s | 单次 DNS 查询超时时间 |
| max_conns | int | 10 | 最大连接数，仅对支持连接复用的加密 DNS 客户端（DoH / DoH3 / DoQ）生效 |

## 示例

内网 / 运营商环境（优先内网 DNS，按序兜底）：

```yaml
dns:
  servers:
    - 192.0.2.10
    - 192.0.2.11
    - udp://192.0.2.53
  timeout: 5s
  max_conns: 10
```

加密 DNS（防污染、防窃听）：

```yaml
dns:
  servers:
    - https://dns.example.com/dns-query
    - tls://dns.example.com
  timeout: 3s
  max_conns: 20
```

最简写法（仅调参数，不指定上游，走系统解析）：

```yaml
dns:
  timeout: 5s
  max_conns: 10
```

## 注意事项

- **servers 按序尝试、非并发**：第一个查询成功的服务器生效；把最稳定、最快的服务器放在最前面。
- **内置公共 DNS 兜底只返回 IPv4**：纯 IPv6 上游环境请显式配置支持 AAAA 记录解析的 DNS 服务器，不要依赖兜底链路。
- **Android 兼容性**：解析器刻意未启用纯 Go 模式，系统解析走 netd / `getaddrinfo`；请勿假设 `/etc/resolv.conf` 一定存在，安卓设备建议显式配置 `servers`。
- **`max_conns` 只对 DoH / DoH3 / DoQ 生效**：普通 UDP / TCP / DoT 客户端不使用连接池，配置该项不影响它们。
- **DNSCrypt 服务器**需使用 `sdns://` 开头的 DNSCrypt stamp 格式，程序自动识别协议类型；普通 URL 形式按 scheme（`https://` / `h3://` / `tls://` / `quic://` / `tcp://` / `udp://`）识别，无法识别时按普通 UDP 处理。
- **与代理模块的关系**：本段解析的是 TVGate 自身要访问的上游域名；上游走代理组时，代理服务器的域名解析同样使用本解析器。
- **示例中的 192.0.2.x 为 RFC 5737 文档专用 IP**，实际部署请替换为你的内网 DNS 或可信公共 DNS。
- 修改本段后通过配置热重载生效，日志中可见解析器重建；若配置了非法服务器地址，加载时会打印 WARNING 并跳过该服务器。
