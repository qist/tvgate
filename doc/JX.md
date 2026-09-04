# 视频解析接口（jx）

`jx` 模块对接第三方视频 API，为影视类链接或片名提供统一的搜索/解析入口，支持常见的视频解析站点（如某奇、某果、某讯、某尤、某咕等）。可配置多个 API 组，在组间按主备/权重负载均衡，单组内多 endpoint 失败重试，并支持按关键字过滤结果。

接口路径、默认集数、API 组均可通过 `jx` 配置段自定义，修改后随配置热加载生效。

## 配置段

```yaml
jx:
  path: "/jx"                      # jx 接口路径，可自定义，例如 /jx
  default_id: "1"                  # 默认集数，如果请求未传 id，则使用此值
  api_groups:                      # 多个视频 API 组配置，可以配置不同的视频源
    other_api:
      endpoints:                   # API 接口列表（第一个为主，其余为同组备用）
        - "http://192.0.2.10"
        - "https://api.example.com"
      timeout: 5s                  # 请求超时
      query_template: "%s/api.php/provide/vod/?ac=detail&wd=%s"   # 查询 URL 模板
      primary: true                # 是否主 API
      weight: 2                    # 权重，用于负载均衡
      fallback: true               # 是否可以作为备用 API
      max_retries: 2               # 请求失败重试次数
      filters:
        exclude: "电影解说,完美世界剧场版"   # 排除包含指定关键字的视频（逗号分隔）
```

## 字段说明

`jx` 段：

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `path` | string | — | 接口访问路径前缀，可自定义（如 `/jx`） |
| `default_id` | string | — | 请求未传 `id` 参数时使用的默认集数 |
| `api_groups` | map | — | 视频 API 组配置，键为组名（如 `other_api`），值为该组配置 |

`api_groups` 每组字段：

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `endpoints` | []string | — | API 接口列表，同组多个地址依次作为备用 |
| `timeout` | duration | — | 单次请求超时（如 `5s`） |
| `query_template` | string | — | 查询 URL 模板，两个 `%s` 分别替换为 endpoint 与搜索关键词 |
| `primary` | bool | `false` | 是否主 API（优先使用） |
| `weight` | int | — | 权重，用于负载均衡 |
| `fallback` | bool | `false` | 是否可作为备用 API（主 API 不可用时启用） |
| `max_retries` | int | — | 请求失败最大重试次数 |
| `filters` | map | — | 过滤条件；`exclude` 为逗号分隔关键字，命中任一关键字的视频被排除 |

## 访问示例

URL 参数：`jx=<影视链接或名称>`（必填）、`id=<集数>`（可选，缺省用 `default_id`）、`full=1`（可选，返回完整信息）。

```bash
# 按影视链接解析
http://192.0.2.10:8888/jx?jx=https://v.example.com/x/cover/mcv8hkc8zk8lnov/z0040syxb9c.html&full=1

# 按片名搜索并指定集数
http://192.0.2.10:8888/jx?jx=爱情公寓3&id=11&full=1
```

## tvbox 对接示例

在 TVBox 配置文件的解析（parse）列表中填入本服务地址即可：

```bash
http://192.0.2.10:8888/jx?jx=https://v.example.com/x/cover/mcv8hkc8zk8lnov/z0040syxb9c.html
http://192.0.2.10:8888/jx?jx=爱情公寓3&id=11
```

## 注意事项

- `query_template` 中的两个 `%s` 依次被替换为 `endpoints` 中的地址与搜索关键词，模板需与目标站点的 API 规格匹配。
- `jx` 参数既支持完整影视页面链接，也支持直接传片名，服务端统一走配置的 API 组检索。
- 启用全局鉴权（`global_auth.tokens_enabled: true`）后，访问 `/jx` 同样需携带有效 token，否则返回 `403 Forbidden`；注意当前实现读取 token 时固定使用 `my_token` 参数名（见 `doc/GLOBALAUTH.md`）。
- `filters.exclude` 为精确子串匹配，多个关键字用英文逗号分隔。
- 上游 API 站点可用性不可控，建议同时配置多个 API 组（`primary` + `fallback`）并合理设置 `timeout` / `max_retries`。
