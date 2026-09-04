# 代理组（proxygroups）

`proxygroups` 是 TVGate 的上游代理模块：为不同的域名 / IP / 子网指定不同的上游代理（`http`、`https`、`socks5`、`socks4`），命中规则的请求经对应组的代理节点转发出去，实现跨区域、跨运营商访问受限的 IPTV 源。

每个代理组独立运作：组内节点按 `interval` 周期独立测速，节点故障时自动切换到组内其他可用节点；`loadbalance` 决定组内多节点的选择策略。典型场景是"不同运营商源走不同上游"——例如四川联通的频道源走联通出口代理、浙江移动的源走移动出口代理，各源互不影响，单一出口故障只影响对应运营商的频道。

## 配置段

```yaml
proxygroups:                 # map 形式：组名 → 组配置，组名自定义（如运营商名）
  组名:
    proxies:                 # 该组的上游代理节点列表
      - name: 节点1          # 节点名称（日志/统计中标识）
        type: socks5         # 类型：http / https / socks5 / socks4
        server: 192.0.2.10   # 代理服务器地址（IP 或域名）
        port: 1080           # 代理端口
        udp: true            # 是否支持 UDP 转发（仅 socks5 生效）
    domains:                 # 走本组的规则列表（域名 / IP / CIDR / IPv6）
      - "*.live.example.com" # 域名通配符
      - 192.0.2.0/24         # IPv4 CIDR 子网
    interval: 180s           # 组内节点测速间隔
    ipv6: false              # IPv6 开关（true 时本组支持 IPv6 目标）
    loadbalance: round-robin # 负载均衡策略：round-robin（轮询）/ fastest（最快）
    max_retries: 3           # 请求失败最大重试次数
    retry_delay: 1s          # 每次重试的间隔
    max_rt: 100ms            # 最大响应时间，超过视为节点不理想/不可用
```

## 字段说明

### 组级字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| 组名（map key） | string | 必填 | 自定义组名，同一组名下的规则共用一组节点 |
| `proxies` | 列表 | 必填 | 上游代理节点列表，至少 1 个 |
| `domains` | 列表 | 必填 | 命中即走本组的规则列表，支持域名 / IP / CIDR / IPv6 |
| `interval` | duration | — | 组内节点测速间隔，如 `180s`、`3m` |
| `ipv6` | bool | `false` | 是否启用 IPv6 目标支持 |
| `loadbalance` | string | `round-robin` | `round-robin` 轮询均摊；`fastest` 每次选响应最快的节点 |
| `max_retries` | int | — | 请求失败后的最大重试次数 |
| `retry_delay` | duration | — | 两次重试之间的等待时间 |
| `max_rt` | duration | — | 最大响应时间阈值，测速/选用时高于该值的节点视为不可用 |

### proxies 节点字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `name` | string | 必填 | 节点名称，用于日志与统计展示 |
| `type` | string | 必填 | 代理类型，仅支持 `http` / `https` / `socks5` / `socks4` |
| `server` | string | 必填 | 代理服务器地址（IP 或域名） |
| `port` | int | 必填 | 代理端口 |
| `udp` | bool | `false` | 是否启用 UDP 支持（仅 `socks5` 有效，组播/UDP 转发场景需要） |
| `username` | string | — | 代理认证用户名（可选） |
| `password` | string | — | 代理认证密码（可选） |
| `headers` | map | — | 经该节点请求时附加的自定义请求头（可选） |

### domains 规则格式

| 格式 | 示例 | 说明 |
|------|------|------|
| 单 IP | `192.0.2.55` | 精确匹配单个 IPv4 地址 |
| IPv4 子网 | `192.0.2.0/24` | CIDR 子网，整段命中 |
| 域名 | `www.example.com` | 精确匹配域名 |
| 域名通配符 | `*.live.example.com` | 匹配任意子域；也支持 `hki*-edge*.edgeware.example.com` 这类部分通配 |
| IPv6 地址 | `2001:db8::abcd:ef01` | 精确匹配单个 IPv6 地址 |
| IPv6 子网 | `2001:db8::/32` | IPv6 CIDR 子网 |

## 示例

不同运营商源走不同上游（可直接拷贝修改）：

```yaml
proxygroups:
  example-unicom:                       # 联通源组
    proxies:
      - name: 联通出口1
        type: socks5
        server: 192.0.2.10
        port: 1080
        udp: true
      - name: 联通出口2
        type: socks5
        server: 192.0.2.11
        port: 1080
        udp: true
    domains:
      - "*.rrs.example.com"             # 联通 IPTV 调度/流媒体域名
      - 198.51.100.0/24                 # 源站 IP 段
    interval: 180s
    ipv6: false
    loadbalance: fastest                # 自动选响应最快的联通节点
    max_retries: 3
    retry_delay: 1s
    max_rt: 200ms

  example-mobile:                       # 移动源组
    proxies:
      - name: 移动出口1
        type: socks5
        server: 192.0.2.20
        port: 8080
        udp: true
    domains:
      - hwltc.tv.cdn.example.com
      - 203.0.113.0/24
      - 2001:db8::/32                   # IPv6 源段（ipv6: true 时生效）
    interval: 3m
    ipv6: true
    loadbalance: round-robin
    max_retries: 3
    retry_delay: 1s
    max_rt: 200ms
```

## 注意事项

- **按组独立测速与故障切换**：每组按 `interval` 独立测速并维护节点存活/响应时间统计；`fastest` 依据测速结果选最快节点，当前节点请求失败时按 `max_retries` / `retry_delay` 重试并在组内切换，单节点故障不影响其他组。
- **规则宁多勿少**：需要代理的 IP、域名尽量都添加。若日志出现 `dial tcp x.x.x.x:80: i/o timeout`，说明该地址直连不通，把对应 `x.x.x.x/24` 加入所属组的 `domains` 即可。
- **UDP 支持**：仅 `socks5` 节点支持 `udp: true`；需要经代理转发组播/UDP 流时必须开启，`http`/`https`/`socks4` 节点忽略该字段。
- **协议类型**：前端节点类型仅支持 `http` / `https` / `socks5` / `socks4`，填写其他值该节点不可用。
- **热加载**：修改 `config.yaml` 后程序自动重载（默认 `reload: 5` 秒检测），代理组增删改无需重启。
- **域名与 IP 都要覆盖**：部分源先请求调度域名再跳转源站 IP，调度域名与源站 IP 段都要加入 `domains`，否则跳转后的请求会直连失败。
