# 仓库同步（sync）

TVGate 内置仓库同步模块：将 **GitHub / GitLab / Gitee 仓库**的内容**单向**同步到本地 `docroot` 子目录（如 `tvbox`），一处维护、多端（安卓 / Windows / Linux）自动拉取，无 git 依赖（Go HTTP 直连 API）。`sync` 为多仓库列表，每个 `enabled` 条目独立同步循环、独立 manifest；条目需使用互不相同的 `local_path`。

主要特性：基于 `git blob sha` 的增量对比（只拉变更）、变更多 / 首次同步时整仓归档降级、`protect` 本地保护清单（永不覆盖 / 删除）、覆盖前 PHP 语法校验与覆盖 / 删除前 `.bak` 备份、路径穿越防护、孤立文件报告，并可在 Web 编辑器（`/web/sync-editor`）可视化增删多仓库。

## 配置段

```yaml
sync:
  - name: tvbox            # 标识（用于日志区分多仓库，可空）
    enabled: false         # 是否启用
    type: github           # github | gitlab | gitee
    host: ""               # 自建实例地址（自建 GitLab https://gitlab.example.com 或 Gitee https://gitee.com），留空 = 平台默认
    repo: owner/repo       # 仓库标识（GitLab 可为 group/project）
    branch: main           # 同步分支
    token: ""              # PAT（GitHub: ghp_xxx；GitLab: glpat_xxx；Gitee: 私人令牌），公开仓库可留空
    interval: 60s          # 轮询间隔（最小 10s）
    repo_path: .           # 仓库内源子目录（"." = 仓库根）
    local_path: tvbox      # 本地目标：以 php docroot 为锚点；"." = docroot 根，"tvbox" = docroot/tvbox
    only_php: false        # 是否只同步 .php/.phtml/.php3/.php4/.inc（混合内容默认 false 全量）
    backup: true           # 覆盖/删除前备份为 .bak.<时间戳>
    delete: false          # 远端已删除的文件，本地是否也删除（false 则保留）
    protect: []            # 本地保护清单（相对 local_path，支持目录前缀）：永不覆盖、永不删除
    timeout: 15s           # 单次 API/下载请求超时
```

## 字段说明

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `name` | string | 空 | 标识，用于日志区分多仓库，可空 |
| `enabled` | bool | `false` | 是否启用该条目 |
| `type` | string | `github` | 仓库平台：`github` / `gitlab` / `gitee` |
| `host` | string | 空（平台默认） | 自建实例地址，如自建 GitLab `https://gitlab.example.com`；留空用平台默认（gitlab.com / gitee.com） |
| `repo` | string | 无 | 仓库标识 `owner/repo`（GitLab 可为 `group/project`） |
| `branch` | string | `main` | 同步分支 |
| `token` | string | 空 | 访问令牌 PAT，公开仓库可留空 |
| `interval` | duration | `60s` | 轮询间隔，最小 10s |
| `repo_path` | string | 空 | 仓库内源子目录，`.` 表示仓库根 |
| `local_path` | string | `tvbox` | 本地目标目录，以 php docroot 为锚点：`.` = docroot 根，`tvbox` = docroot/tvbox |
| `only_php` | bool | `false` | 是否只同步 PHP 文件（`.php` / `.phtml` / `.php3` / `.php4` / `.inc`） |
| `backup` | bool | `true` | 覆盖 / 删除本地文件前备份为 `.bak.<时间戳>` |
| `delete` | bool | `false` | 远端已删除的文件，本地是否也删除 |
| `protect` | string 列表 | 空 | 本地保护清单（相对 `local_path`，支持目录前缀），永不覆盖、永不删除 |
| `timeout` | duration | `15s` | 单次 API / 下载请求超时 |

## 示例

```yaml
sync:
  - name: tvbox
    enabled: true
    type: github
    host: ""
    repo: example/tvbox
    branch: main
    token: ghp_xxxxxxxxxxxx        # 公开仓库可留空；建议配置只读 PAT 提升限额
    interval: 30m
    repo_path: .
    local_path: tvbox
    only_php: false
    backup: true
    delete: false
    timeout: 30s
    protect:
      - tv.txt                     # 设备私有文件：永不覆盖、永不删除

  - name: scripts
    enabled: false
    type: gitlab
    host: https://gitlab.example.com   # 自建 GitLab（API v4 路径一致）
    repo: group/scripts
    branch: main
    token: glpat_xxxxxxxxxxxx
    interval: 1h
    repo_path: php
    local_path: scripts
    only_php: true
    backup: true
    delete: true
    timeout: 15s
    protect: []
```

## 注意事项

### 同步与安全特性

- **增量对比**：基于 `git blob sha` 对比，只拉变更（新 / 改），未变化跳过
- **整仓归档降级**：变更多 / 首次同步时下载整仓 tar.gz（公开仓库走 codeload 直连，不占 `api.github.com` 未认证 60 次/小时限额），本地计算 git blob sha 对比；增量树 API 限流时自动降级归档
- **安全**：覆盖前 `simplePHPCheck` 校验 PHP 语法；`.bak.<时间戳>` 备份；路径穿越防护；归档解压防穿越
- **孤立文件报告**：每次同步列出"本地有、远端无"的文件（跳过 protect / `.bak` / 隐藏文件），供核对设备私有文件
- **Web 编辑器**：登录后台 `/web/sync-editor` 可视化增删多仓库（含 protect 清单）

### token 掩码保存

Web 编辑器保存后令牌**不回显**（显示 `********`，掩码占位保存会保留原值、填新值才覆盖），避免凭据泄露。GitHub 未认证仅 60 次/小时，建议公开仓库也配置一个只读 PAT（Contents: Read）以提升到 5000 次/小时，稳定高频轮询。

### 访问同步内容（TVBox 订阅）

同步下来的文件落在 **`docroot + local_path`** 目录（例如 `docroot/tvbox`）。PHP 模块启用后（`php.enabled: true`），`/php/` 前缀下的静态文件按正确 MIME 直接返回（`.json` / `.txt` / `.m3u` / `.jar` / `.js` / `.py` 等），可直接作为 TVBox 订阅 / 直播源地址：

```
http://192.0.2.10:8888/php/tvbox/0707.json      # TVBox 订阅配置
http://192.0.2.10:8888/php/tvbox/listx.m3u      # 直播源列表
http://192.0.2.10:8888/php/tvbox/jar/spider.jar # 爬虫插件
```

- `local_path` 为其他子目录（如 `www/scripts`）时路径为 `/php/www/scripts/...`；为 `.`（同步到 docroot 根）时路径为 `/php/<文件>`
- 未启用 PHP 模块时无法通过 `/php/` 访问，可将这些文件放到其他静态目录
- 在 TVBox 的"配置订阅"里填上述地址，电视端即可自动拉取并更新订阅；多仓库各自同步到不同 `local_path` 即可管理多套配置

### 平台说明

- **自建 GitLab**：内网 IP / 端口填 `host` 即可（API v4 路径一致）
- **Gitee**：走 API v5，token 用 Gitee 私人令牌，归档为 zip
