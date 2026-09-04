# 域名映射（domainmap）

`domainmap` 段把对源域名（`source`）的请求改写并转发到目标域名（`target`），并可在前端校验请求头、在后端附加/覆盖请求头。最典型的用途是"伪造请求过限制"：给后端请求附上运营商 IPTV 期望的 `User-Agent`、`X-Forwarded-For` 等头部，绕过源站对 UA 或来源地区的校验；也可用 `client_headers` 要求访问者必须携带指定头部，实现简易准入控制。

该段为列表形式，每条映射独立配置协议（`http` / `https` / `rtsp`）、独立 token 认证（`auth`，结构与全局 `global_auth` 完全一致）。认证的优先级是：**条目自身 `auth` 优先；条目未启用 token 时回落到全局 `global_auth`**——即未单独配置认证的映射自动受全局 token 保护，配置了则按条目自身的规则验证。

## 配置段

```yaml
domainmap:
  - name: example-map            # 配置名称（标识用途，可自定义）
    source: iptv.example.com     # 源域名：客户端实际访问的 Host
    target: real.example-src.com # 目标域名：请求实际被改写发往的后端
    protocol: http               # 协议：http / https / rtsp
    auth:                        # 本条目独立认证（结构与 global_auth 相同，可不配）
      tokens_enabled: false      # 是否启用 token 认证
      token_param_name: my_token # token 的 URL 参数名
      dynamic_tokens:            # 动态 token：按密钥+盐值生成，带有效期
        enable_dynamic: false
        dynamic_ttl: 2h
        secret: mysecret
        salt: mysalt
      static_tokens:             # 静态 token：固定值 + 过期时间
        enable_static: false
        token: token123
        expire_hours: 24h
    client_headers:              # 前端请求使用：校验客户端携带的请求头，不匹配返回 401
      User-Agent: okhttp/3.8.1
    server_headers:              # 后端请求使用：发往目标时附加/覆盖的请求头（伪造 UA / XFF）
      User-Agent: okhttp/3.8.1
      X-Forwarded-For: 192.0.2.1
```

## 字段说明

### 条目字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `name` | string | — | 配置名称，用于标识该条映射 |
| `source` | string | 必填 | 源域名，客户端请求的 Host 与之匹配时触发改写 |
| `target` | string | 必填 | 目标域名，请求被实际转发到的后端 |
| `protocol` | string | — | 后端协议：`http` / `https` / `rtsp` |
| `auth` | 对象 | — | 本条目独立 token 认证配置，结构同 `global_auth` |
| `client_headers` | map | — | 前端请求使用：客户端访问时必须携带的请求头（键值需完全一致），否则返回 401 |
| `server_headers` | map | — | 后端请求使用：TVGate 向目标发起请求时附加/覆盖的请求头，值留空则不覆盖 |

### auth 子字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `tokens_enabled` | bool | `false` | 是否启用本条目的 token 认证 |
| `token_param_name` | string | `token`（条目启用时）/ 全局参数名 | token 在 URL 中的参数名；验证通过后参数自动从 URL 移除 |
| `dynamic_tokens.enable_dynamic` | bool | `false` | 启用动态 token（按 `secret` + `salt` 生成） |
| `dynamic_tokens.dynamic_ttl` | duration | — | 动态 token 有效期，如 `2h` |
| `dynamic_tokens.secret` | string | — | 动态 token 的 AES 密钥 |
| `dynamic_tokens.salt` | string | — | 动态 token 的盐值 |
| `static_tokens.enable_static` | bool | `false` | 启用静态 token |
| `static_tokens.token` | string | — | 静态 token 值 |
| `static_tokens.expire_hours` | duration | — | 静态 token 过期时间，如 `24h` |

## 示例

```yaml
# 全局认证（条目未单独启用 auth 时回落到这里）
global_auth:
  tokens_enabled: false
  token_param_name: my_token

domainmap:
  # HTTP 映射：伪造 UA 与 X-Forwarded-For 过源站校验，并要求访问者带指定 UA
  - name: http-map
    source: iptv.example.com
    target: ott.example-src.com
    protocol: http
    client_headers:
      User-Agent: okhttp/3.8.1
    server_headers:
      User-Agent: okhttp/3.8.1
      X-Forwarded-For: 192.0.2.1

  # RTSP 映射：运营商 RTSP 源的域名改写
  - name: rtsp-map
    source: rtsp.example.com
    target: rtsp.example-src.com
    protocol: rtsp
    client_headers:
      ua: my-player

  # 带独立静态 token 认证的 HTTPS 映射
  - name: secure-map
    source: secure.example.com
    target: cdn.example-src.com
    protocol: https
    auth:
      tokens_enabled: true
      token_param_name: my_token
      static_tokens:
        enable_static: true
        token: token123
        expire_hours: 24h
```

## 注意事项

- **协议范围**：`protocol` 支持 `http` / `https` / `rtsp` 三种；`rtsp` 条目按 RTSP over TCP 处理并统一走 token 认证逻辑。
- **client_headers 是校验而非注入**：客户端请求头必须与配置完全一致才能通过（否则 `401`），常用于要求播放器带特定 UA 的准入控制；要"伪造"头部发给源站，应配置 `server_headers`。
- **与全局认证的配合**：条目 `auth.tokens_enabled` 开启且启用了动态/静态 token 时按条目配置验证；否则回落到全局 `global_auth` 的 token 管理器（参数名也取全局的）。全局 token 验证通过后，token 参数会自动从请求 URL 中删除，不透传给后端。
- **Host 头**：转发到目标时 Host 自动改写为 `target`，前端展示的地址始终是 `source`，真实源不外露。
- **热加载**：修改 `domainmap` 后由配置热加载生效，无需重启；Web 后台「域名映射」页可可视化配置。
