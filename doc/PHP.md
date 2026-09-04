# PHP 模块（php）

TVGate 内置一个**纯 Go 实现的 PHP 运行时（phpgo）**，无需 PHP-FPM、无需 CGO、无外部 `.so`/`.dll`，直接编译进单一静态二进制。启用后，Go HTTP Server 可通过 `path` 指定的路径前缀解释执行磁盘 `docroot` 目录下的 PHP 脚本，并同时直接服务该目录下的静态文件。脚本一律从磁盘读取（不打包进二进制），部署时把 PHP 代码放进 `docroot` 即可，路径可在配置中自由修改，无需重新编译。

phpgo 内置 300+ 个常用 PHP 函数（字符串 / 数组 / 数学 / 日期 / 文件 / JSON / URL / cURL / 正则 / 加解密 / 类型等），并含 12 个别名（如 `join`→`implode`、`mt_rand`→`rand`），不依赖 `iconv` / `session` / `xml` 等扩展，常见字符串、数组、日期、curl、文件、加解密等内置函数均以 Go 原生实现。除 HTTP 访问外，docroot 脚本还可被 H5 播放器的 `php://` 频道源与定时任务的 `php://` 命令**内部调用**，共用同一条内部执行链路。

## 配置段

```yaml
php:
  enabled: false          # 是否启用 PHP 模块（独立模块，可单独开关）
  path: /php/             # 访问路径前缀。对外访问 URL 为 http://<IP>:<port>/php/<脚本>
  docroot: www            # PHP 脚本根目录（从磁盘读取，不打包进二进制）。默认相对路径 www，相对配置文件所在目录解析（安卓/移动端友好）
  index:                  # 目录索引文件列表（访问 /php/ 目录时按序尝试）
    - index.php
    - index.html
  worker_mode: false      # 是否启用 Worker 常驻模式（复用解释器实例，降低冷启动开销）
  workers: 4              # Worker 进程数（worker_mode 为 true 时生效）
```

## 字段说明

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `enabled` | bool | `false` | 是否启用 PHP 模块 |
| `path` | string | `/php/` | 访问路径前缀 |
| `docroot` | string | `www` | PHP 脚本根目录；相对路径以**配置文件所在目录**为基准解析（不是进程 cwd），支持 `~` / `~/` 家目录写法 |
| `index` | string 列表 | `index.php`、`index.html` | 访问目录时按序尝试的索引文件列表 |
| `worker_mode` | bool | `false` | 是否启用 Worker 常驻模式 |
| `workers` | int | `0` | Worker 进程数，仅 `worker_mode: true` 时生效 |

## 示例

```yaml
php:
  enabled: true
  path: /php/
  docroot: www              # 实际解析为 <config.yaml 所在目录>/www
  index:
    - index.php
    - index.html
  worker_mode: false
  workers: 4
```

访问方式（假设服务监听 `8888`，机器 IP 为 `192.0.2.10`，脚本放在 `www/huya.php`，即 `<配置文件所在目录>/www/huya.php`）：

- 本机：`http://127.0.0.1:8888/php/huya.php?id=11342412`
- 局域网/外网：`http://192.0.2.10:8888/php/huya.php?id=11342412`

服务监听地址为 `:端口`（绑定所有网卡 `0.0.0.0`），外部访问需确保防火墙/安全组放行该端口。

## 注意事项

### docroot 路径解析规则

- 绝对路径（如 `/www`、`C:/www`、`/data/data/com.termux/files/home/www`）直接使用
- 相对路径（如 `www`、`php`）：基准为**配置文件所在目录**（不是进程 cwd），跨平台一致。例如配置在 `/etc/tvgate/config.yaml`、写 `docroot: www`，实际解析为 `/etc/tvgate/www`
- 支持 `~` / `~/` 家目录写法（如 `~/www`、`~`），程序自动展开为用户家目录，无需手动拼接绝对路径
- 路径会做归一化（清理尾斜杠与多余分隔符），避免 docroot 带尾斜杠时边界判断把正常脚本误判为越权（403/非法路径）

### 安卓 / 移动端部署

- **启动时用绝对路径传 `-config`**：安卓进程 cwd 不可靠（常为 `/`），若用相对 `-config config.yaml` 启动，相对 docroot 会以 cwd 为基准，解析不可控。建议 `tvgate -config /data/data/com.termux/files/home/tvgate/config.yaml`（或 shell 会先展开 `~/tvgate/config.yaml`）
- 默认值 `docroot: www` 即相对配置文件目录，将 `config.yaml` 与脚本目录放在一起即可使用：如 Termux 中配置在 `~/tvgate/config.yaml`、脚本在 `~/tvgate/www/`，默认配置即满足，无需再改

### 静态文件直返规则

`/php/` 前缀下既支持 PHP 解释执行，也直接服务静态资源。判断规则：扩展名为 `.php` / `.php3` / `.php4` / `.phtml` / `.inc`，或内容含 `<?php` / `<?=` / `<?` 标签的文件由 phpgo 解释执行；其余（`.html` / `.css` / `.js` / 无扩展名等）按原文件以正确的 MIME 类型直接返回，无需 PHP 标签。例如 `http://192.0.2.10:8888/php/index.html` 会直接返回静态 HTML。

### global_auth 集成

PHP 模块已集成 `global_auth` 全局 token 验证，与 HTTP / UDP / RTSP handler 行为一致。当 `global_auth.tokens_enabled: true` 时，访问 `/php/` 下的任何脚本都需要在 URL 参数中携带有效的 token（参数名取 `token_param_name`）：

- 验证通过后，token 参数会从 URL 中**自动删除**，不会传到 PHP 脚本的 `$_GET` / `$_POST` / `QUERY_STRING` 中，避免 token 泄露给脚本逻辑
- 未携带 token 或 token 无效时返回 `403 Forbidden`
- `global_auth` 配置修改后由配置热加载自动刷新，无需重启

### php:// 内部执行链路

除通过 `/php/` HTTP 访问外，docroot 脚本还可被其他模块内部调用——不走 HTTP 回环、不依赖 IP、不经过鉴权：

- H5 播放器的 `php://` 频道源：302 Location 解析为真实源后续走 http 链路，m3u8 输出自动重写分片
- 定时任务的 `php://` 命令：脚本输出体作为任务输出

写法均为 `php://<docroot 相对路径>?参数`，兼容 `php://php/xxx.php` 与 `php://xxx.php` 两种路径形式。

### 备份文件管理

每次在 Web 编辑器中保存 PHP 文件时，系统会自动将旧内容备份为 `.bak.<时间戳>` 文件。Web 后台提供**备份文件管理中心**（入口在代码编辑页面），支持列表（递归扫描 docroot 下 `.bak` 备份文件）、回滚、下载、单个/批量删除与自动清理。

### 超时建议

phpgo 的 HTTP 栈比原生 PHP + libcurl 慢，脚本里用很短超时（如 `CURLOPT_TIMEOUT=0.1`）做链接可用性校验时容易被误判超时。建议把超时调大（如 `3s` / `5s`），或采用"0.1s 快速校验 + 加几秒兜底重试"；校验超时应按"无法判断"处理，用缓存兜底而不是清缓存。

phpgo 内置函数的完整清单与兼容性说明见仓库内 `phpgo/php_basic_functions_go_implementation.md`。
