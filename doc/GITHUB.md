# GitHub 加速配置（github）

`github` 段是**双用途加速配置**，为两类需要访问 GitHub 的功能统一提供主/备加速地址与超时、重试控制：

1. **仓库同步（sync 模块）**：`sync` 中 `type: github` 的条目在拉取仓库目录树、文件内容与整仓归档（tar.gz）时，GitHub API 与下载请求经加速地址前缀转发，适用于 GitHub 访问受限或缓慢的网络环境。
2. **版本升级（updater）**：检查新版本（获取 Release 列表）与下载升级包时，同样经加速地址访问。

启用后的请求顺序为：**主加速地址（url）→ 备用加速地址（backup_urls，按序）→ 官方地址兜底**。任一地址成功即结束，全部失败才报错；即使 `enabled: false`，也会直接访问官方地址。加速原理是把原始 GitHub 地址拼接在加速站点之后（如 `https://gh-proxy.example.com/https://api.github.com/...`）。Web 管理后台「GitHub 加速」页提供可视化配置。

## 配置段

```yaml
github:
  enabled: false                            # 是否启用加速
  url: https://gh-proxy.example.com         # 主加速地址（写站点根地址即可，无需带路径）
  backup_urls:                              # 备用加速地址列表，按序尝试
    - https://gh-mirror1.example.com
    - https://gh-mirror2.example.com
  timeout: 10s                              # 单次请求超时时间
  retry: 3                                  # 最大重试次数
```

## 字段说明

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| enabled | bool | false | 是否启用加速；`false` 时直接访问官方 GitHub 地址 |
| url | string | "" | 主加速地址，写站点根地址（程序自动拼接原始 GitHub 地址） |
| backup_urls | []string | 空 | 备用加速地址列表，主地址失败后按序逐个尝试 |
| timeout | duration | 10s | 单次请求超时时间 |
| retry | int | 3 | 最大重试次数 |

## 示例

```yaml
github:
  enabled: true
  url: https://gh-proxy.example.com
  backup_urls:
    - https://gh-mirror1.example.com
    - https://gh-mirror2.example.com
  timeout: 10s
  retry: 3
```

不启用加速（直连官方 GitHub，显式声明便于阅读）：

```yaml
github:
  enabled: false
  url: ""
  backup_urls: []
  timeout: 10s
  retry: 3
```

## 注意事项

- **只对 GitHub 生效**：`sync` 中 `type: gitlab` / `type: gitee` 的条目走各自平台 API，不经本段加速；`host` 指定的自建实例同样不走加速。
- **加速地址写根地址即可**：程序自动去除多余斜杠后拼接原始地址，如 `https://gh-proxy.example.com` + `https://api.github.com/repos/...`、`https://github.com/qist/tvgate/releases/download/...`。
- **第三方加速站点的信任问题**：启用加速后，请求（包括 `sync` 条目携带的 `Authorization: Bearer <token>` 请求头）会经过第三方站点转发。同步公开仓库风险较低，但请勿把私有仓库 token 用于不可信的加速站点。
- **故障切换按地址列表顺序进行**：主地址 → 备用地址（按 `backup_urls` 顺序）→ 官方地址，每个地址失败后立即尝试下一个；`retry` 控制整体最大重试次数。
- **timeout 默认 10s**（配置缺省时自动补齐）；版本升级的 Release 检查在未显式配置超时时内部另有 30s 兜底。
- **热重载生效**：修改本段后由配置热重载自动加载，`sync` 模块会按新配置自动重启同步循环，无需重启进程。
- **`enabled: false` 不是"禁用访问"**：同步与升级功能仍会直连官方 GitHub；若需完全关闭同步，请将对应 `sync` 条目的 `enabled` 设为 `false`。
- **示例地址均为 example.com 占位**，实际部署请替换为你可用的加速服务地址。
