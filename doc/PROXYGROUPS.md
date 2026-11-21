# 代理组（proxygroups）

`proxygroups` 是 TVGate 的上游代理模块：为不同的域名 / IP / 子网指定不同的上游代理（`http`、`https`、`socks5`、`socks4`），命中规则的请求经对应组的代理节点转发出去，实现跨区域、跨运营商访问受限的 IPTV 源。

每个代理组独立运作：组内节点按 `interval` 周期独立测速，节点故障时自动切换到组内其他可用节点；`loadbalance` 决定组内多节点的选择策略。典型场景是"不同运营商源走不同上游"——例如四川联通的频道源走联通出口代理、浙江移动的源走移动出口代理，各源互不影响，单一出口故障只影响对应运营商的频道。

节点来源有两种，可以单独使用，也可以同时配置：

- `proxies`：静态节点列表，地址固定；
- `pool`：动态端口池，按「网关 + 端口段」展开候选端口（每个端口对应一个出口），由后台定时探测、排序和故障切换。

## 配置段

```yaml
proxygroups:                 # map 形式：组名 → 组配置，组名自定义（如运营商名）
  组名:
    proxies:                 # 静态上游代理节点列表（与 pool 至少填一个）
      - name: 节点1          # 节点名称（日志/统计中标识）
        type: socks5         # 类型：http / https / socks5 / socks4a
        server: 192.0.2.10   # 代理服务器地址（IP 或域名）
        port: 1080           # 代理端口
        udp: true            # 是否支持 UDP 转发（仅 socks5 生效）
    domains:                 # 走本组的规则列表（域名 / IP / CIDR / IPv6）
      - "*.live.example.com" # 域名通配符
      - 192.0.2.0/24         # IPv4 CIDR 子网
    interval: 180s           # 组内节点测速间隔（也是测速结果缓存有效期）
    ipv6: false              # IPv6 开关（true 时本组支持 IPv6 目标）
    loadbalance: round-robin # 负载均衡策略：round-robin（轮询）/ fastest（最快）
    max_retries: 3           # 请求失败最大重试次数
    retry_delay: 1s          # 每次重试的间隔
    max_rt: 100ms            # 最大响应时间，超过视为节点不理想/不可用
    resolve: remote          # 目标域名解析方式：remote（代理端解析）/ local（TVGate 解析）
    probe_ua: ""             # 探测/测速用 UA；空则回退 player.ua，再空则不发送

    pool:                    # 可选：动态端口池
      servers:
        - 192.0.2.10:20000-20100
      protocol: auto
      probe_enabled: true
      probe_interval: 60s
      probe_url: "http://example.com/main.m3u8"
      probe_timeout: 2s
      probe_retries: 1
      probe_concurrency: 16
      top_n: 30
      fail_cooldown: 60s
      max_ports: 2048
```

## 字段说明

### 组级字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| 组名（map key） | string | 必填 | 自定义组名，同一组名下的规则共用一组节点 |
| `proxies` | 列表 | — | 静态上游代理节点列表；与 `pool` 至少填一个 |
| `domains` | 列表 | 必填 | 命中即走本组的规则列表，支持域名 / IP / CIDR / IPv6 |
| `interval` | duration | — | 组内节点测速间隔，如 `180s`、`3m`；同时是测速结果缓存有效期 |
| `ipv6` | bool | `false` | 是否启用 IPv6 目标支持 |
| `loadbalance` | string | `round-robin` | `round-robin` 轮询均摊；`fastest` 每次选响应最快的节点 |
| `max_retries` | int | — | 请求失败后的最大重试次数 |
| `retry_delay` | duration | — | 两次重试之间的等待时间 |
| `max_rt` | duration | — | 最大响应时间阈值，测速/选用时高于该值的节点视为不可用 |
| `resolve` | string | `remote` | `remote` 目标域名交给代理端解析；`local` 用 TVGate 的 `dns.servers` 解析成 IP 后再交给代理 |
| `probe_ua` | string | 空 | 探测/测速用 `User-Agent`；为空回退 `player.ua`，仍为空则不发送。只影响探测，播放请求仍按频道配置透传 |
| `pool` | object | — | 动态端口池配置，见下节 |

### proxies 节点字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `name` | string | 必填 | 节点名称，用于日志与统计展示 |
| `type` | string | 必填 | 代理类型，支持 `http` / `https` / `socks5` / `socks4` / `socks4a` |
| `server` | string | 必填 | 代理服务器地址（IP 或域名） |
| `port` | int | 必填 | 代理端口 |
| `udp` | bool | `false` | 是否启用 UDP 支持（仅 `socks5` 有效，组播/UDP 转发场景需要） |
| `username` | string | — | 代理认证用户名（可选） |
| `password` | string | — | 代理认证密码（可选） |
| `headers` | map | — | 经该节点请求时附加的自定义请求头（可选） |
| `resolve` | string | 继承组 | 节点级 DNS 模式覆盖，通常不需要 |

### pool 字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `servers` | []string | 必填 | 网关列表，支持 `host:start-end`、`host:port`、`scheme://[user:pass@]host:start-end` |
| `protocol` | string | `auto` | `socks5` / `http` / `https` / `socks4a` / `auto`；`auto` 对每个端口同时探测 socks5 与 http |
| `probe_enabled` | bool | `true` | 是否启用池主动探测；`false` 时只把前 `top_n` 个候选交给 `lb`，由请求时按需测速 |
| `probe_interval` | duration | `60s` | 定时探测间隔，最小 10s |
| `probe_url` | string | 空 | 探测目标，支持 `http` / `https` / `rtsp`；为空时不启动定时探测 |
| `probe_timeout` | duration | `2s` | 单次探测超时 |
| `probe_retries` | int | `0` | 单轮内失败后的立即重试次数，最大 5；每次重试同样受 `probe_timeout` 限制，全部失败才标记失败并进入冷却 |
| `probe_concurrency` | int | `16` | 并发探测数，最大 64 |
| `top_n` | int | `30` | 进入可用列表的最大节点数，最大 200 |
| `fail_cooldown` | duration | `60s` | 失败节点冷却时间 |
| `max_ports` | int | `2048` | 端口段展开后的总端口上限，防止误配 |

### domains 规则格式

| 格式 | 示例 | 说明 |
|------|------|------|
| 单 IP | `192.0.2.55` | 精确匹配单个 IPv4 地址 |
| IPv4 子网 | `192.0.2.0/24` | CIDR 子网，整段命中 |
| 域名 | `www.example.com` | 精确匹配域名 |
| 域名通配符 | `*.live.example.com` | 匹配任意子域；也支持 `hki*-edge*.edgeware.example.com` 这类部分通配 |
| IPv6 地址 | `2001:db8::abcd:ef01` | 精确匹配单个 IPv6 地址 |
| IPv6 子网 | `2001:db8::/32` | IPv6 CIDR 子网 |

## 动态代理池

代理池把同一网关的多个端口（每个端口一个出口）聚合成一个代理组，由后台定时探测、排序和故障切换。前端播放地址保持不变，不需要在 URL 上加任何代理标记。

### 节点来源与优先级

- 只有 `pool`：完全由池提供节点，`proxies` 可以省略；
- 只有 `proxies`：保持原有静态代理行为；
- 两者都有：静态代理始终保留在候选列表前面，池节点追加在后。

校验规则为「`proxies` 非空 或 `pool.servers` 非空」，两者都为空时配置无效。

### 探测与选路

1. **加载/热重载**：展开 `servers` 为候选节点，先放入一批候选，保证首个请求就有节点可用；随后后台并发探测。
2. **定时探测**：`probe_enabled: true` 且 `probe_url` 非空时，每 `probe_interval` 巡检一轮。
3. **探测判定**：复用 `lb` 的直播检测——HTTP 发 `Range` 请求，RTSP 走 OPTIONS/DESCRIBE/SETUP/PLAY；HTTP 状态码 `< 400` 视为成功，记录响应时间。`probe_retries` 用于单轮内的立即重试。
4. **排序**：成功节点按响应时间升序，保留前 `top_n` 个。
5. **冷却**：失败节点进入 `fail_cooldown`；下一轮探测仍会重测，成功后自动恢复。
6. **选路**：继续使用现有 `lb` 逻辑，`fastest` 从可用列表里选响应时间最小的节点。
7. **故障切换**：请求失败由 `lb` 标记并按 `max_retries` / `retry_delay` 重试，池管理器下一轮探测补充节点。

`pool.probe_interval` 是后台巡检，代理组的 `interval` 是请求时的测速缓存有效期，两者不是同一个东西。建议 `interval >= probe_interval`，避免 `lb` 在两次池探测之间又对整组节点做一次全量测速。

### 探测 UA

一些直播源会校验 `User-Agent`（例如只接受 `QTP` 或 `okhttp`），探测若用错 UA 会一直拿到 403，虽然播放时频道级 `ua=` 仍能成功，但后台状态会显示失败并可能触发冷却。

解析顺序：

1. 组级 `probe_ua`；
2. 全局 `player.ua`；
3. 都不配置 → 不发送 `User-Agent`。

该配置只影响探测/测速，播放请求仍按频道 `ua=` 或 `player.ua` 透传，两者互不影响。

### DNS 模式

`remote`（默认）目标域名原样交给代理，由代理端解析：SOCKS5 按域名类型发送，HTTP/HTTPS 用 `CONNECT host:port` 带域名。这是浏览器、Clash、v2rayN 等主流客户端的行为，Android 上最稳。

`local` 由 TVGate 先解析成 IP 再交给代理：使用全局 `dns.servers`，未配置时走系统解析 → 内置公共 DNS 兜底；解析出多个 IP 时依次尝试；`ipv6: false` 只取 A 记录并使用 `tcp4`。

标准 `http.Transport` 的 `Proxy` 路径无法在 `DialContext` 中改写目标地址，因此 `local` 模式统一走自定义 CONNECT dialer：`socks5`、`socks4a`、`http`、`https` 代理均支持，`https` 代理先与代理建立 TLS 再发 `CONNECT`。

Android 约束：不新建 `net.Resolver{PreferGo: true}`，不读取 `/etc/resolv.conf` 判断 DNS，复用 `dns.GetInstance()` 的解析接口；代理服务器自身的域名也走同一解析链。Android 默认使用 `remote`。

### 后台展示

后台「代理组」页面可以新增/编辑动态池，并展示探测成功的端口、协议、延迟、HTTP 状态与失败次数。页面空闲时每 15 秒自动刷新；编辑中暂停刷新，避免覆盖未保存的修改。

### 前端访问

前端仍按原地址访问：

```
/http://tvgslb.hn.chinamobile.com:8089/.../main.m3u8
```

通过 `domains` 规则命中对应组，TVGate 内部选池节点。m3u8 重写和 302 跳转不写入任何代理参数，外部播放器拿不到代理地址。

本阶段不支持 URL 携带代理：涉及凭据泄漏、开放代理和 SSRF 风险。后续如需要，应只允许引用后台代理组名，并做鉴权、脱敏和私网地址拦截。

### 限制

- 池节点只在内存中维护，不写回配置文件；
- 多实例部署时每个实例独立探测，节点顺序可能不同，但都走同一份域名规则；
- `probe_url` 为空时没有定时巡检，只能依赖请求时测速；
- `protocol: auto` 会为每个端口生成 socks5 和 http 两个候选，端口段较大时探测量翻倍，请配合 `max_ports` 和 `probe_concurrency` 使用；
- 后台保存代理组时按组名合并更新已知字段，保留 YAML 中未识别的字段；`proxies` 为空且存在 `pool` 时不写空的 `proxies`。

## 示例

### 静态节点组

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

### 动态端口池

```yaml
proxygroups:
  hn-pool:
    # 只配置 pool 时 proxies 可以省略
    domains:
      - tvgslb.hn.chinamobile.com
      - "*.hn.chinamobile.com"
    loadbalance: fastest
    interval: 3m                        # 测速缓存有效期，建议 >= probe_interval
    max_retries: 2
    retry_delay: 1s
    resolve: remote
    probe_ua: "okhttp/3.14.9"           # 源站有 UA 白名单时按需设置
    pool:
      servers:
        - 221.229.214.145:20237-20337
        - 221.229.214.146:20237-20337
      protocol: auto
      probe_enabled: true
      probe_interval: 60s
      probe_url: "http://tvgslb.hn.chinamobile.com:8089/1800000001001/000000010000000000007000000001243/main.m3u8"
      probe_timeout: 2s
      probe_retries: 1
      probe_concurrency: 16
      top_n: 30
      fail_cooldown: 60s
      max_ports: 2048
```

### 静态与池混合

```yaml
proxygroups:
  mixed:
    proxies:                            # 固定出口始终保留
      - name: backup
        type: socks5
        server: 192.0.2.30
        port: 1080
    domains:
      - live.example.com
    loadbalance: fastest
    interval: 3m
    pool:
      servers:
        - 192.0.2.31:30000-30050
      protocol: http
      probe_url: "http://live.example.com/main.m3u8"
```

## 注意事项

- **按组独立测速与故障切换**：每组按 `interval` 独立测速并维护节点存活/响应时间统计；`fastest` 依据测速结果选最快节点，当前节点请求失败时按 `max_retries` / `retry_delay` 重试并在组内切换，单节点故障不影响其他组。
- **规则宁多勿少**：需要代理的 IP、域名尽量都添加。若日志出现 `dial tcp x.x.x.x:80: i/o timeout`，说明该地址直连不通，把对应 `x.x.x.x/24` 加入所属组的 `domains` 即可。
- **域名与 IP 都要覆盖**：部分源先请求调度域名再跳转源站 IP，调度域名与源站 IP 段都要加入 `domains`，否则跳转后的请求会直连失败。
- **UA 白名单源站**：探测与播放是两套 UA。源站校验 UA 时，除频道订阅里的 `ua=` 外，组级 `probe_ua` 也要配对，否则后台会一直显示 403 并可能把节点打进冷却。
- **UDP 支持**：仅 `socks5` 节点支持 `udp: true`；需要经代理转发组播/UDP 流时必须开启，`http`/`https`/`socks4` 节点忽略该字段。
- **协议类型**：节点类型支持 `http` / `https` / `socks5` / `socks4` / `socks4a`，填写其他值该节点不可用。
- **热加载**：修改 `config.yaml` 后程序自动重载（默认 `reload: 5` 秒检测），代理组增删改无需重启；池配置未变化的组保留已有节点与测速状态。
- **测速状态字段**：测速失败时会清空 `StatusCode` 与 `ResponseTime`，避免日志里出现「上次的 206 + 本次失败」这种误读组合。
