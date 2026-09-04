# 全局鉴权（global_auth）

`global_auth` 是作用于全局各入口的 token 鉴权模块：开启后，HTTP 代理、UDP 组播转 HTTP、RTSP、PHP 模块（`/php/` 下所有脚本）、jx 视频解析、H5 播放器（频道列表 / EPG / 拉流 / 回看）、域名映射与推流等入口均要求 URL 中携带有效 token，未携带或无效一律返回 `403 Forbidden`。

支持两类 token：**动态 token**（基于 `secret` + `salt` 生成、带 TTL 自动过期）与**静态 token**（固定字符串，可设有效期）。配置修改后由配置热加载自动刷新，无需重启。

## 配置段

```yaml
global_auth:
  tokens_enabled: false        # 是否启用全局 token 验证
  token_param_name: t2         # token 参数名（URL 中 ?t2=<token>）
  dynamic_tokens:              # 动态 token 配置
    enable_dynamic: true       # 是否启用动态 token
    dynamic_ttl: 2h            # 动态 token 有效期，如 1h / 2h（0 表示永不过期）
    secret: mysecretkey12345   # AES 加密密钥（务必更换为自己的强密钥）
    salt: mysalt123            # 盐值（参与 token 内容校验）
  static_tokens:               # 静态 token 配置
    enable_static: true        # 是否启用静态 token
    token: mytoken12345        # 静态 token 值
    expire_hours: 48h          # 静态 token 有效时长，如 24h（0 表示永不过期）
```

## 字段说明

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `tokens_enabled` | bool | `false` | 是否启用全局 token 验证 |
| `token_param_name` | string | — | token 在 URL 中的参数名，如 `?t2=<token>` |
| `dynamic_tokens.enable_dynamic` | bool | `false` | 是否启用动态 token |
| `dynamic_tokens.dynamic_ttl` | duration | — | 动态 token 有效期（如 `1h`），超时即失效；0 表示永不过期 |
| `dynamic_tokens.secret` | string | — | 动态 token 的 AES 加密密钥 |
| `dynamic_tokens.salt` | string | — | 动态 token 的盐值，校验时必须匹配 |
| `static_tokens.enable_static` | bool | `false` | 是否启用静态 token |
| `static_tokens.token` | string | — | 静态 token 值（固定字符串） |
| `static_tokens.expire_hours` | duration | — | 静态 token 有效时长（如 `30s` / `24h` / `48h`），0 表示永不过期 |

## 动态 token 生成原理

动态 token 并非随机串，而是**可自校验、自带时间戳的加密串**：

1. **生成**：服务端将明文 `salt|路径|时间戳` 用 `secret` 作 AES 加密后 Base64 编码，得到动态 token；
2. **验证**：用 `secret` 解密 → 比对解密出的 `salt` 与配置一致 → 解析时间戳，距生成时刻超过 `dynamic_ttl` 判为过期（`dynamic_ttl` 为 0 则不限）；
3. **内部改写**：对 301 跳转、HLS TS 分片等需要二次请求的场景，服务端会自动生成携带动态 token 的新 URL；token 中记录首次生成路径，重定向与分片沿用首次路径，保证链路不断；
4. **过期清理**：后台定时清理过期会话，防止状态膨胀。

客户端无需自行计算动态 token，只需使用服务端下发/改写后的完整 URL 即可。

## 静态 token

静态 token 即配置中固定的字符串，适合自用或分发给可信任设备：请求时带上 `?<token_param_name>=<token值>` 即可通过验证。`expire_hours` 设置其有效时长，过期后需更换配置中的 token 值；设为 0 表示永不过期。长期使用期间会话自动保活（活跃状态持续刷新）。

## token 参数自动删除

验证通过后，token 参数会从 URL 中**自动删除**：

- 不会传入 PHP 脚本的 `$_GET` / `$_POST` / `QUERY_STRING`，避免 token 泄露给脚本逻辑；
- 转发到上游/后端前同样剥离 token 参数，保持原始 URL。

未携带 token 或 token 无效时，所有受控入口统一返回 `403 Forbidden`。

## 作用入口

| 入口 | 说明 |
|---|---|
| HTTP 代理 | 代理拉流请求需携带 token |
| UDP 组播转 HTTP | `udp://` 频道播放请求需携带 token |
| RTSP | `rtsp://` 频道播放请求需携带 token |
| PHP 模块 | `/php/` 下任何脚本均需携带 token（与 HTTP / UDP / RTSP handler 行为一致） |
| jx 视频解析 | `/jx` 请求需携带 token |
| H5 播放器 | 频道列表 / EPG / 拉流 / 回看等接口需携带 token |
| 域名映射 / 推流 | 同样接入全局 token 校验（域名映射未配置独立 `auth` 时回落全局） |

## 与播放器鉴权的关系

播放器自身有独立的**源白名单**机制（订阅内频道才可播放，key 不在白名单返回 403），与 `global_auth` 相互独立、可叠加使用：开启 `global_auth` 后，播放器各接口与 `/player/<key>` 拉流在白名单校验之外还需携带有效 token（参数名取 `token_param_name`）。两类校验都通过才能正常播放。

## 示例

```yaml
global_auth:
  tokens_enabled: true
  token_param_name: my_token
  dynamic_tokens:
    enable_dynamic: true
    dynamic_ttl: 2h
    secret: mysecretkey12345     # 示例值，部署时务必更换
    salt: mysalt123              # 示例值，部署时务必更换
  static_tokens:
    enable_static: true
    token: mytoken12345          # 示例值，部署时务必更换
    expire_hours: 48h
```

静态 token 访问示例（假设 `token_param_name: my_token`、`static_tokens.token: mytoken12345`）：

```bash
http://192.0.2.10:8888/php/huya.php?id=12345&my_token=mytoken12345
```

## 注意事项

- `tokens_enabled: false` 时全局管理器不创建，所有入口不做 token 校验。
- `secret` / `salt` / `token` 属于敏感凭据，务必使用足够长的随机强值并妥善保管；泄露即等同开放全部受控入口。
- 静态 token 与动态 token 可同时启用，验证时任一通过即可。
- 配置热加载会自动重载全局 token 管理器，静态 token 的活跃状态在重载后保留，不因改配置而掉线。
- 采集类客户端若依赖 `QUERY_STRING` 透传，无需关心 token：验证后 token 参数已被删除，脚本收到的即原始业务参数。
