# 服务器与传输配置（server / http / reload）

`server` 段决定 TVGate 对外监听的端口与 TLS（HTTPS）行为，是转发、代理、Web 管理后台、H5 播放器等所有功能对外的统一入口。将服务暴露到公网时，可在此直接配置证书启用 HTTPS，或在前端放置 Nginx 做 TLS 终结。

`http` 段控制 TVGate 作为 **HTTP 客户端**访问上游（转发源站、上游代理、订阅、DNS-over-HTTPS 等）时的传输参数：各类超时、TCP 保活、连接池上限。流媒体转发是长连接、高并发场景，这些参数直接影响稳定性与源站压力。

`reload` 段（单字段）控制配置热重载的防抖间隔：程序通过文件监听（fsnotify）监测 `config.yaml` 变更，检测到修改后等待 `reload` 秒再执行重载，避免编辑器多次写盘触发频繁重载。

> **关于 `monitor` 段**：旧版本曾提供 `monitor.path`（状态页，默认 `/status`）。新版已将监控能力全量迁入 Web 管理后台（仪表盘，接口 `/web/api/v1/status`），`/status` 路由与 `monitor` 配置段已移除。旧配置文件中保留 `monitor:` 段不会报错（未知字段被忽略），但不再生效。

## 配置段

```yaml
server:
  port: 8888                 # 监听端口（主端口，绑定所有网卡）
  http_port: 0               # 独立 HTTP 端口（>0 时启用端口分离，见注意事项）
  certfile: ""               # TLS 证书文件路径（与 keyfile 同时配置后主端口启用 HTTPS）
  keyfile: ""                # TLS 私钥文件路径
  ssl_protocols: "TLSv1.2 TLSv1.3"   # TLS 协议版本（空 = 默认 TLSv1.2~1.3）
  ssl_ciphers: ""            # TLS 加密套件，冒号分隔（空 = 默认安全套件）
  ssl_ecdh_curve: "X25519MLKEM768:X25519:P-384:P-256"  # ECDH 曲线（支持 ML-KEM 后量子）
  http_to_https: false       # HTTP 访问是否跳转 HTTPS
  tls:                       # 独立 HTTPS 端口的 TLS 配置（可选）
    https_port: 0            # HTTPS 监听端口（>0 时启用）
    certfile: ""             # 证书路径
    keyfile: ""              # 私钥路径
    ssl_protocols: ""        # 协议版本
    ssl_ciphers: ""          # 加密套件
    ssl_ecdh_curve: ""       # ECDH 曲线
    enable_h3: false         # 是否启用 HTTP/3（QUIC）

http:
  timeout: 0s                # 整个请求超时时间（0 = 不限制，适合长连接流）
  connect_timeout: 10s       # TCP 连接建立超时
  keepalive: 10s             # TCP 长连接保活时间
  response_header_timeout: 10s  # 等待响应头超时
  idle_conn_timeout: 30s     # 空闲连接在连接池中的保留时间
  tls_handshake_timeout: 10s # TLS 握手超时
  expect_continue_timeout: 1s   # Expect: 100-continue 等待超时
  max_idle_conns: 1000       # 全局最大空闲连接数
  max_idle_conns_per_host: 32   # 每个主机最大空闲连接数
  max_conns_per_host: 64     # 每个主机最大连接数（含空闲与活跃）
  disable_keepalives: false  # 是否禁用长连接复用（false = 启用 KeepAlive）
  insecure_skip_verify: false   # 是否跳过上游 TLS 证书校验

reload: 5                    # 配置热重载防抖间隔（秒），0 = 立即重载
```

组播接口相关字段已移至独立的 `multicast` 段（旧版曾写在 `server` 段下）：

```yaml
multicast:
  multicast_ifaces: []       # 组播监听网卡，留空 = 默认接口，如 [ "eth0", "eth1" ]
  mcast_rejoin_interval: 0s  # 组播重加入间隔，0 = 禁用；多播流中断时建议 30s~120s
```

## 字段说明

### server 段

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| port | int | 8888 | 主监听端口，绑定 `0.0.0.0` |
| http_port | int | 0 | 独立 HTTP 端口；`>0` 时进入端口分离模式（见注意事项） |
| certfile | string | "" | TLS 证书文件（PEM）；与 keyfile 均非空时主端口以 HTTPS 提供服务 |
| keyfile | string | "" | TLS 私钥文件（PEM） |
| ssl_protocols | string | ""（TLSv1.2~1.3） | 允许的 TLS 协议版本，空格分隔 |
| ssl_ciphers | string | ""（默认安全套件） | TLS 加密套件，冒号分隔 |
| ssl_ecdh_curve | string | "" | ECDH 曲线，冒号分隔，支持 ML-KEM（如 `X25519MLKEM768`） |
| http_to_https | bool | false | HTTP 请求自动跳转 HTTPS |
| tls.https_port | int | 0 | 独立 HTTPS 监听端口 |
| tls.enable_h3 | bool | false | 是否启用 HTTP/3 |
| tls.certfile / keyfile 等 | string | "" | 独立 HTTPS 端口专属的证书与套件配置，不与主端口共用 |

### http 段

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| timeout | duration | 0（不限制） | 整个请求超时；流媒体长连接应保持 0 |
| connect_timeout | duration | 10s | TCP 连接建立超时 |
| keepalive | duration | 10s | TCP 保活探测间隔 |
| response_header_timeout | duration | 10s | 发出请求后等待响应头的超时 |
| idle_conn_timeout | duration | 30s | 空闲连接保留时间，超时后关闭 |
| tls_handshake_timeout | duration | 10s | TLS 握手超时 |
| expect_continue_timeout | duration | 1s | `Expect: 100-continue` 等待超时 |
| max_idle_conns | int | 1000 | 全局最大空闲连接数 |
| max_idle_conns_per_host | int | 32 | 单主机最大空闲连接数 |
| max_conns_per_host | int | 64 | 单主机最大连接总数（活跃 + 空闲）；默认值已放宽，过小会导致多路观看同一源站时断流重连 |
| disable_keepalives | bool | false | 禁用长连接复用 |
| insecure_skip_verify | bool | false | 跳过上游证书校验 |

### reload 段

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| reload | int | 0 | 配置文件变更后的防抖等待秒数；0 表示立即重载 |

### monitor 段（已移除）

| 字段 | 状态 | 说明 |
|---|---|---|
| monitor.path | 已移除 | 旧版状态页路径（默认 `/status`）；监控能力现由 Web 后台仪表盘（`/web/api/v1/status`）承载 |

## 示例

最小可用配置（HTTP 明文监听）：

```yaml
server:
  port: 8888

reload: 5
```

完整示例（主端口 HTTPS + 高并发 HTTP 客户端调优）：

```yaml
server:
  port: 8888
  certfile: /etc/tvgate/certs/example.com.crt
  keyfile: /etc/tvgate/certs/example.com.key
  ssl_protocols: "TLSv1.2 TLSv1.3"
  http_to_https: false

http:
  timeout: 0s
  connect_timeout: 3s
  keepalive: 30s
  response_header_timeout: 5s
  idle_conn_timeout: 90s
  tls_handshake_timeout: 5s
  expect_continue_timeout: 1s
  max_idle_conns: 200000
  max_idle_conns_per_host: 10000
  max_conns_per_host: 20000
  disable_keepalives: false
  insecure_skip_verify: false

reload: 5
```

## 注意事项

- **端口分离模式**：`http_port` 或 `tls.https_port` 大于 0 时，主端口（`port`）降级为只跑 Web 管理后台与 PHP 模块，转发、代理、jx、播放器等业务走新端口；未启用新端口时主端口跑全功能。
- **重启与热更新**：热重载时若检测到 `port` / `http_port` / `https_port` / 证书路径变更，会关闭并重建服务（短暂中断）；其余变更仅平滑替换路由，不中断现有连接。
- **`timeout: 0s` 不代表无超时保护**：仅整体请求不限制，连接、响应头、TLS 握手仍有各自超时兜底；不要为流媒体把 `response_header_timeout` 设得过大或禁用。
- **10 万并发参考**：`max_idle_conns: 200000`、`max_idle_conns_per_host: 10000`、`max_conns_per_host: 20000`、`keepalive: 30s`，并确保 `disable_keepalives: false`，同时配合系统级 `nofile` / conntrack 调优（见 README「Linux 内核优化建议」）。
- **安全**：`insecure_skip_verify: true` 会跳过上游证书校验，存在中间人风险，仅限内网自签证书等可信场景；服务绑定所有网卡，公网暴露请启用 TLS、放行防火墙端口并设置 Web 后台强密码。
- **reload 值在启动时读取**：运行中修改 `reload` 本身需重启进程才影响防抖间隔；其他配置仍按旧间隔正常热重载。
- **组播配置迁移**：`multicast_ifaces` / `mcast_rejoin_interval` 现属 `multicast` 段；写在 `server` 段下将被忽略。`mcast_rejoin_interval` 是兼容性方案（IGMP 侦听交换机缺查询等场景），推荐 30~120 秒（小于典型交换机超时 260 秒），仅在遇到多播流中断时启用。
