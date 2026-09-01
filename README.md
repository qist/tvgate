# TVGate — IPTV 转发 / 代理工具

> 高性能的本地内网流/网页资源转发与代理工具，将内部可访问的 `http`/`rtsp`/`rtp` 等资源安全地发布到外网，并支持通过多种上游代理跨区域访问受限资源。

## 目录

- [功能](#功能)
- [下载](#下载)
- [快速开始](#快速开始)
- [Docker 启动](#-使用-docker-启动)
- [服务管理](#服务管理--启动脚本)
- [代理规则格式](#代理规则格式)
- [使用示例](#使用示例外网访问路径)
- [jx 视频解析接口](#-jx-视频解析接口)
- [PHP 模块（纯 Go phpgo runtime）](#php-模块纯-go-phpgo-runtime)
- [配置示例](#配置configyaml示例)
- [Nginx 反向代理](#nginx-反向代理配置参考)
- [Linux 内核优化](#linux-内核优化建议)
- [Web 代码文件管理（编辑/上传/下载/语法检测）](#web-代码文件管理编辑上传下载语法检测)
- [仓库同步（sync）](#仓库同步sync)
- [注意事项](#注意事项--常见问题)

---

## 功能

### 转发
将内网可访问的资源（如 `http`, `https`, `rtsp`, `rtp`）通过 HTTP 对外发布，外网用户访问 Go 程序所在主机的端口（默认 `8888`）即可获取流或请求代理的资源。

支持的常见场景：
- 将内网 RTP / 组播 转为可通过 HTTP 访问（类似 udpxy）
- 将运营商提供的 RTSP / HTTP 单播转发并通过外网访问
- 将局域网内的 PHP 动态脚本通过外网访问（如 `huya.php`）

### 代理
支持上游代理（`socks5`、`socks4`、`http`），可为不同域名 / IP / 子网 指定不同上游代理，实现跨区域、跨运营商访问受限内容。

- **动态重载配置**：修改 `config.yaml` 后程序会自动重载配置（无需重启）
- **规则类型**：单 IP、CIDR 子网、域名通配符、IPv6 等

---

## 下载

### 服务端（本仓库）

前往 [Releases](https://github.com/qist/tvgate/releases) 下载对应平台的二进制文件。

支持的平台：

| 平台 | 文件名示例 |
|------|-----------|
| Linux 64-bit | `TVGate-linux-64.zip` |
| Linux ARM64 | `TVGate-linux-arm64-v8a.zip` |
| Linux ARM32 v7 | `TVGate-linux-arm32-v7a.zip` |
| Linux 32-bit | `TVGate-linux-32.zip` |
| Linux MIPS | `TVGate-linux-mips32.zip` |
| Linux LoongArch | `TVGate-linux-loong64.zip` |
| Linux RISC-V | `TVGate-linux-riscv64.zip` |
| Windows 64-bit | `TVGate-windows-64.zip` |
| Windows ARM64 | `TVGate-windows-arm64-v8a.zip` |
| macOS 64-bit | `TVGate-macos-64.zip` |
| macOS ARM64 | `TVGate-macos-arm64-v8a.zip` |
| Android ARM64 | `TVGate-android-arm64-v8a.zip` |

<details>
<summary>查看全部支持的平台</summary>

- linux-64 / linux-amd64
- linux-arm64-v8a / linux-arm64
- linux-arm32-v7a / linux-armv7
- linux-arm32-v6 / linux-armv6
- linux-arm32-v5
- linux-32 / linux-386
- linux-loong64
- linux-mips32 / linux-mips32le
- linux-mips64 / linux-mips64le
- linux-ppc64 / linux-ppc64le
- linux-riscv64
- linux-s390x
- windows-64 / windows-amd64
- windows-32 / windows-386
- windows-arm64-v8a / windows-arm64
- macos-64 / darwin-amd64
- macos-arm64-v8a / darwin-arm64
- android-arm64-v8a / android-arm64

</details>

### 安卓客户端

> [tvgate-android](https://github.com/qist/tvgate-android) — Android 平台 IPTV 播放客户端，配合 TVGate 服务端使用。

前往 [tvgate-android Releases](https://github.com/qist/tvgate-android/releases) 下载 APK。

### OpenWrt 插件

> [luci-app-tvgate](https://github.com/qist/luci-app-tvgate) — OpenWrt LuCI 管理界面插件，可在路由器上直接管理 TVGate。

前往 [luci-app-tvgate Releases](https://github.com/qist/luci-app-tvgate/releases) 下载 ipk/apk 安装包。

---

## 快速开始

### 安装
1. 下载对应平台二进制（示例）并放到 `/usr/local/TVGate/`（或你的目录）。
2. 准备配置文件 `/usr/local/TVGate/config.yaml`（见下文示例）。
3. 启动：
```bash
nohup /usr/local/TVGate/TVGate-linux-amd64 -config=/usr/local/TVGate/config.yaml > /var/log/tvgate.log 2>&1 &
```

### 运行示例
假设你的公网 IP 为 `111.222.111.222`，程序监听端口 `8888`，则外网可以按下面示例访问转发后的地址（见下文「使用示例」）。

---

## 📦 使用 Docker 启动

你可以直接通过 Docker 拉取镜像运行：

映射端口要根据yaml配置端口一致，例如：8888

### 方式一：使用 ghcr.io 镜像
```bash
docker run -d   --name=tvgate   -p 8888:8888  --restart=unless-stopped  -v /usr/local/TVGate/:/etc/tvgate/   ghcr.io/qist/tvgate:latest
```

### 方式二：使用 Docker Hub 镜像
```bash
docker run -d   --name=tvgate   -p 8888:8888 --restart=unless-stopped  -v /usr/local/TVGate/:/etc/tvgate/   juestnow/tvgate:latest
```

### udp转发：
```bash
docker run -d  --net=host  --name=tvgate --restart=unless-stopped -v /usr/local/TVGate/:/etc/tvgate/   ghcr.io/qist/tvgate:latest
```

### docker-compose 示例
```yaml
version: "3"
services:
  tvgate:
    image: ghcr.io/qist/tvgate:latest   # 或 juestnow/tvgate:latest
    container_name: tvgate
    restart: always
    ports:
      - "8888:8888"
    volumes:
      - /usr/local/TVGate/:/etc/tvgate/
```

运行后可通过 `http://宿主机IP:8888/` 访问。

---

## 服务管理 / 启动脚本

### systemd (Linux)
把以下文件保存为 `/etc/systemd/system/TVGate.service`：

```ini
[Unit]
Description=TVGate - IPTV 转发 / 代理工具
After=network.target

[Service]
Type=simple
LimitCORE=infinity
LimitNOFILE=100000
LimitNPROC=100000
ExecStart=/usr/local/TVGate/TVGate-linux-amd64 -config=/usr/local/TVGate/config.yaml
Restart=on-failure
PrivateTmp=true
ExecReload=/bin/kill -SIGHUP $MAINPID

[Install]
WantedBy=multi-user.target
```

启用并启动：
```bash
systemctl daemon-reload
systemctl enable --now TVGate
```

---

### OpenWrt 安装
下载地址：[luci-app-tvgate Releases](https://github.com/qist/luci-app-tvgate/releases)

1. 安装 ipk 包（OpenWrt 24 及以下）：
   ```bash
   opkg update
   opkg install curl ca-certificates unzip luci-compat luci luci-base
   opkg install /tmp/luci-app-tvgate_1.0.0_all.ipk
   opkg install /tmp/luci-i18n-tvgate-zh-cn_1.0.0-1_all.ipk
   opkg install /tmp/luci-i18n-tvgate-en_1.0.0-1_all.ipk
   ```

2. 卸载 ipk 包：
   ```bash
   opkg remove luci-app-tvgate
   opkg remove luci-i18n-tvgate-en
   opkg remove luci-i18n-tvgate-zh-cn
   ```

3. 安装 apk 包（OpenWrt 25+）：
   ```bash
   apk update
   apk add curl ca-certificates unzip luci-compat luci luci-base
   apk add --allow-untrusted luci-app-tvgate-1.0.0-r1.apk
   apk add --allow-untrusted luci-i18n-tvgate-en-1.0.0-r1.apk
   apk add --allow-untrusted luci-i18n-tvgate-zh-cn-1.0.0-r1.apk
   ```

4. 卸载 apk 包：
   ```bash
   apk del luci-app-tvgate
   apk del luci-i18n-tvgate-en
   apk del luci-i18n-tvgate-zh-cn
   ```

---

### 代理规则格式
- 支持 IP（例如 `192.168.1.1`）
- 支持子网（例如 `192.168.1.0/24`）
- 支持域名通配符（例如 `*.rrs.169ol.com`、`hki*-edge*.edgeware.tvb.com`、`www.tvb.com`）
- 支持 IPv6（例如 `1234:5678::abcd:ef01`）
- 支持 IPv6 子网（例如 `1234:5678::abcd:ef01/128`）
- 需要代理的ip 域名尽量都添加，如果日志出现  `dial tcp 210.13.7.109:80: i/o timeout` 那就把 `210.13.7.109/24` 添加到代理规则中

---

## 使用示例（外网访问路径）

以下示例假设 TVGate 运行在公网 IP `111.222.111.222`，端口 `8888`。

1. **组播 RTP（内网）**
   - 内网地址：`rtp://239.0.0.1:2000`
   - 外网访问：  
     `http://111.222.111.222:8888/udp/239.0.0.1:2000`

2. **RTSP（运营商/内网单播）**
   - 内网地址：  
     `rtsp://10.254.192.94/PLTV/.../index.smil`
   - 外网访问：  
     `http://111.222.111.222:8888/rtsp/10.254.192.94/PLTV/.../index.smil`

3. **HTTP / M3U8（运营商单播）**
   - 内网地址：  
     `http://sc.rrs.169ol.com/PLTV/.../index.m3u8`
   - 外网访问：  
     `http://111.222.111.222:8888/sc.rrs.169ol.com/PLTV/.../index.m3u8`

4. **HTTPS 转发**
   - 外网访问（转发 https）：  
     `http://111.222.111.222:8888/https://sc.rrs.169ol.com/PLTV/.../index.m3u8`

5. **局域网 PHP 动态页面代理**
   - 内网地址：`http://192.168.1.10/huya.php?id=11342412`
   - 外网访问：  
     `http://111.222.111.222:8888/192.168.1.10/huya.php?id=11342412`

---

## 🔹 jx 视频解析接口

用于对接第三方视频 API，支持常见的视频解析站点（如某奇、某果、某讯、某尤、某咕等）。

访问示例：

```bash
http://111.222.111.222:8888/jx?jx=https://v.xx.com/x/cover/mcv8hkc8zk8lnov/z0040syxb9c.html&full=1
http://127.0.0.1:8888/jx?jx=爱情公寓3&id=11&full=1
```

tvbox 配置文件：
```bash
http://111.222.111.222:8888/jx?jx=https://v.xx.com/x/cover/mcv8hkc8zk8lnov/z0040syxb9c.html
http://127.0.0.1:8888/jx?jx=爱情公寓3&id=11
```

---

## PHP 模块（纯 Go phpgo runtime）

Tvgate 内置一个**纯 Go 实现的 PHP 运行时（phpgo）**，无需 PHP-FPM、无需 CGO、无外部 `.so`/`.dll`，编译进单一静态二进制。启用后，Go HTTP Server 可通过指定路径前缀解释执行磁盘上的 PHP 脚本。

### 配置段

```yaml
php:
  enabled: false          # 是否启用 PHP 模块（独立模块，可单独开关）
  path: /php/             # 访问路径前缀。对外访问 URL 为 http://<IP>:<port>/php/<脚本>
  docroot: www            # PHP 脚本根目录（从磁盘读取，不打包进二进制）。默认相对路径 www，相对配置文件所在目录解析（安卓/移动端友好）
  index:                  # 目录索引文件列表（访问 /php/ 时按序尝试）
    - index.php
    - index.html
  worker_mode: false      # 是否启用 Worker 常驻模式（复用解释器实例，降低冷启动开销）
  workers: 4              # Worker 进程数（worker_mode 为 true 时生效）
```

> **docroot 说明**：脚本一律从 `docroot` 指定的磁盘目录读取（默认相对路径 `www`，即 `<配置文件所在目录>/www`）。部署时把 PHP 代码放到该目录即可，例如 `<配置文件所在目录>/www/index.php`。该路径可在配置中自由修改，无需重新编译。
>
> **路径写法（跨平台）**：
> - 绝对路径：`/www`、`C:/www`、`/data/data/com.termux/files/home/www` 直接使用。
> - 相对路径（如 `www`、`php`）：基准为**配置文件所在目录**（不是进程 cwd），跨平台一致。例如配置在 `/etc/tvgate/config.yaml`、写 `docroot: www`，实际解析为 `/etc/tvgate/www`。
>
> **安卓 / 移动端部署**：安卓上 `/www` 这类绝对路径通常不存在或不可写，所以默认值就是相对路径 `www`——将 `config.yaml` 与脚本目录放在一起即可使用：
> ```yaml
> php:
>   docroot: www     # 实际 = <config.yaml 所在目录>/www
> ```
> 例如 Termux 中配置在 `~/tvgate/config.yaml`、脚本在 `~/tvgate/www/`，即可正常访问（默认配置即满足，无需再改）。
>
> **两个注意点（安卓建议遵循）**：
> - **启动时用绝对路径传 `-config`**：安卓进程 cwd 不可靠（常为 `/`），若用相对 `-config config.yaml` 启动，相对 docroot 会以 cwd 为基准，解析不可控。建议 `tvgate -config /data/data/com.termux/files/home/tvgate/config.yaml`（或 shell 会先展开 `~/tvgate/config.yaml`）。
> - **支持 `~` / `~/` 家目录写法**：`docroot` 也支持 `~/www`、`~` 等写法，程序会自动展开为用户家目录，无需手动拼接绝对路径。
>
> 不依赖任何 PHP 扩展（如 `iconv` / `session` / `xml` 等），常见字符串、数组、日期、curl、文件、加解密等内置函数均以 Go 原生实现。

> **内置函数覆盖**：phpgo 内置 300+ 个常用 PHP 函数（字符串 / 数组 / 数学 / 日期 / 文件 / JSON / URL / cURL / 正则 / 加解密 / 类型等），并含 12 个别名（如 `join`→`implode`、`mt_rand`→`rand`）。完整清单与兼容性说明见 [phpgo 函数实现清单](phpgo/php_basic_functions_go_implementation.md)。
>
> ⚠️ **超时注意**：phpgo 的 HTTP 栈比原生 PHP + libcurl 慢，脚本里用很短超时（如 `CURLOPT_TIMEOUT=0.1`）做链接可用性校验时容易被误判超时。建议把超时调大（如 `3s/5s`），或采用"0.1s 快速校验 + 加几秒兜底重试"；校验超时应按"无法判断"处理，用缓存兜底而不是清缓存。

### 访问方式

假设服务监听 `8888`，机器 IP 为 `192.168.1.10`，脚本放在默认的 `www/huya.php`（即 `<配置文件所在目录>/www/huya.php`）：

- 本机：`http://127.0.0.1:8888/php/huya.php?id=11342412`
- 局域网/外网：`http://192.168.1.10:8888/php/huya.php?id=11342412`

服务监听地址为 `:端口`（绑定所有网卡 `0.0.0.0`），外部访问需确保防火墙/安全组放行该端口。

> **静态文件支持**：`/php/` 前缀下既支持 PHP 解释执行，也直接服务静态资源。判断规则：扩展名为 `.php/.php3/.php4/.phtml/.inc`，或内容含 `<?php` / `<?=` / `<?` 标签的文件由 phpgo 解释；其余（`.html` / `.css` / `.js` / 无扩展名等）按原文件以正确的 MIME 类型直接返回，无需 PHP 标签。例如 `http://<IP>:<port>/php/index.html` 会直接返回静态 HTML（phpgo 不会丢弃标签外内容）。

### 全局 Token 验证

PHP 模块已集成 `global_auth` 全局 token 验证，与 HTTP / UDP / RTSP handler 行为一致。当 `config.yaml` 中 `global_auth.tokens_enabled: true` 时，访问 `/php/` 下的任何脚本都需要在 URL 参数中携带有效的 token。

示例（假设 `token_param_name: juieieiri`，`static_tokens.token: tertwertw`）：

```
http://<IP>:<port>/php/huya.php?id=12345&juieieiri=tertwertw
```

- 验证通过后，token 参数会从 URL 中**自动删除**，不会传到 PHP 脚本的 `$_GET` / `$_POST` / `QUERY_STRING` 中，避免 token 泄露给脚本逻辑。
- 未携带 token 或 token 无效时返回 `403 Forbidden`。
- `global_auth` 配置修改后由配置热加载自动刷新，无需重启。

### 备份文件管理

每次在 Web 编辑器中保存 PHP 文件时，系统会自动将旧内容备份为 `.bak.<时间戳>` 文件。Web 后台提供**备份文件管理中心**（入口在代码编辑页面），支持：

| 操作 | 说明 |
|---|---|
| 列表 | 列出 `docroot` 下所有 `.bak` 备份文件 |
| 回滚 | 将备份文件内容恢复为当前文件 |
| 下载 | 下载备份文件 |
| 删除 | 删除单个或批量删除备份文件 |
| 自动清理 | 按设定保留天数自动清理过期备份 |

---

## Web 代码文件管理（编辑/上传/下载/语法检测）

Web 管理后台内置**代码文件管理器**，可直接在浏览器中对 `docroot`（默认相对路径 `www`，即 PHP 脚本目录）下的文件进行可视化操作，无需 SSH 登录服务器。

### 入口

登录 Web 后台后访问：

```
http://<IP>:<port>/web/code
```

### 支持的操作

| 操作 | 说明 |
|---|---|
| 列目录 | 递归列出 `docroot` 下所有文件与子目录 |
| 新建 | 新建文件或目录（相对路径，如 `sub/test.php`） |
| 编辑/保存 | 在线编辑并保存到磁盘（保存前自动备份为 `.bak.<时间戳>`） |
| 上传 | 多选上传文件到指定子目录，自动防路径穿越 |
| 上传解压 | 上传 `.zip` 并按配套 `.zip.md5` 自动解压，或手动 `/api/code/unzip` 解压 |
| 下载 | 以附件形式下载文件 |
| 删除 | 删除文件或整个目录 |
| 语法检测 | 对 PHP 源码做纯文本级简单检测（无需 PHP 运行时） |

### 简单语法检测说明

后端 `simplePHPCheck` 在**纯 Go、无 PHP 二进制**的前提下做文本级检查，可识别：

- 未以 `<?php` / `<?` 开始标签开头（warning）
- 括号 `()` `{}` `[]` 不匹配或未闭合（error）
- 单/双引号字符串未闭合、块注释 `/*` 未闭合（error）

> 该检测为轻量级静态检查，不能替代完整 PHP 解释器；真正执行仍由 phpgo 运行时完成。所有写操作限制在 `docroot` 内，防止 `../` 目录穿越。

### ZIP 上传解压（自动 / 手动）

代码管理支持 **ZIP 上传 + 自动解压**，方便整包部署 PHP/js 等代码。

- **上传入口**：`POST <webPath>api/code/upload`（需登录），`multipart` 字段 `file`（可多选）+ 可选 `dir`（目标子目录，默认 `docroot` 根）。
- **自动解压触发条件**：上传完成后，后端自动扫描同目录下是否存在配对文件 `xxx.zip` 与 `xxx.zip.md5`。若存在且 `.zip.md5` 内容里第一个字段与 `xxx.zip` 的**实际 MD5** 一致，则立即将该 zip 解压到同目录（**覆盖模式**）。
  - `xxx.zip.md5` 格式：首字段为期望 MD5（`md5sum` 输出如 `d41d8cd98f00b204e9800998ecf8427e`，后续字段如文件名会忽略）。示例：
    ```
    echo -n "$(md5sum xxx.zip | awk '{print $1}')" > xxx.zip.md5
    ```
  - **没有 `.zip.md5` 文件不算错**，此时该 `.zip` 仅作为普通文件上传，不会自动解压。
  - MD5 不匹配（`mismatch`）或解压失败会逐项在响应 `unzip` 数组里回报，不会中断整体上传。
- **手动解压**：`POST <webPath>api/code/unzip`（需登录），两种模式：
  1. 指定磁盘已有 zip：`?path=xxx.zip&dir=目标目录`（`dir` 省略则解压到 zip 所在目录）。
  2. 上传并解压：`multipart file=xxx.zip&dir=目标目录`，可选 `flatten=true` 展平子目录。
- **安全**：解压统一走 `extractZip`，含**路径穿越防护**（归档内 `../` 或绝对路径条目被丢弃）+ `assertInside` 约束在 `docroot` 内；所有代码接口均需 `cookieAuth` 登录。

---

## 备份机制

TVGate 在**配置、代码中心、同步中心**三处各自有备份规则，都是"改前备份旧内容"，可回滚。

### 1. config.yaml 配置备份（规则）

**触发**：通过 Web 后台**保存任意配置**（server / http / web / monitor / php / sync / github / global-auth / log / proxygroups / ts / multicast / reload 等）时，各保存处理器都会在**写回新配置前**，先把当前 `config.yaml` 的旧内容完整复制一份。

**命名 / 位置 / 保留**：
- 备份文件名：`config.yaml.backup.<时间戳>`，时间戳格式 `20060102150405`，如 `config.yaml.backup.20260827150405`。
- 位置：与 `config.yaml` **同目录**。
- **每次保存都会新增一条**备份，不会覆盖旧备份（便于多步回退）。
- 备份文件内容为**明文配置**（与 config.yaml 一致）。

**配置备份中心**（`/web/config/backup`）：对同目录下所有 `*.backup.*` 文件提供 `列表 / 删除 / 批量删除 / 还原 / 下载`，按时间从新到旧排序。**无自动清理**，需手动删除或按需定期清理；还原会把选中的备份写回 `config.yaml`。

### 2. 代码中心（代码文件管理）保存/同步逻辑

**保存落盘**：在代码编辑器里保存或批量替换时，遵循"**先备份旧文件 → 再写新内容**"：
- 保存前先把当前旧文件复制为 **`原文件.bak.<时间戳>`**（时间戳格式 `YYYY-MM-DD_HH-MM-SS`，如 `test.php.bak.2026-08-27_22-28-00`，与原文件同目录），再写入新内容；保存后刷新文件列表。
- 批量替换（`🔁 批量替换`）：递归遍历当前目录及子目录的文本文件，**跳过** `.bak` 文件、隐藏文件、超大文件（>5MB）、二进制文件（内容含 `\0`）；每个被替换文件都会同上面的方式先备份 `.bak` 再写回。

**上传与解压**：
- 上传 `api/code/upload`：multipart 落盘；上传后若同目录存在 `xxx.zip` 与配套 `xxx.zip.md5` 且 MD5 一致，**自动解压**（覆盖模式）。
- 手动解压 `api/code/unzip`：按磁盘已有 zip（`?path=&dir=`）或 multipart 上传，可选 `flatten=true` 展平子目录；均含路径穿越防护。

**备份文件管理中心**（入口在代码编辑页）：统一管理 docroot 下所有 `.bak.<时间戳>` 文件，支持：
- `列表`（递归扫描 docroot 下含 `.bak.` 的文件）
- `回滚`（把备份写回原文件；**回滚前也会先备份当前文件**，避免不可逆覆盖）
- `下载`
- `单个 / 批量删除`
- `自动清理`（按原文件分组，每组保留最新 N 个，`keep` 可设 0=全删）

解压 / 批量替换 / 回滚所写的 `.bak` 都归此管理中心管理，可用同一套列表/回滚/清理。

### 3. 同步中心（sync）备份

仓库**同步**在**覆盖或删除**本地文件前，若该同步条目开启了 **`backup: true`**（YAML 里默认 `true`），会把将被覆盖/删除的本地文件备份为 **`.bak.<时间戳>`**（时间戳格式 `20060102-150405`，如 `20260827-150405`）。

- `backup: false` 则同步覆盖/删除前**不做**备份。
- **保护清单 `protect`** 中的文件不受影响（永不覆盖、永不删除）。
- 孤立文件报告会**跳过** `.bak` 备份文件，避免误报。

> 三处备份的 `.bak` / `.backup` 文件都不参与同步路径穿越判定之外的清理逻辑，需在各自管理中心或手动清理。

## 仓库同步（sync）

将 **GitHub / GitLab 仓库** 的内容**单向**同步到本地 `docroot` 子目录（如 `tvbox`），一处维护、多端（安卓 / Windows / Linux）自动拉取，无 git 依赖（Go HTTP 直连 API）。

### 特性

- **多仓库**：`sync` 为条目列表，每个 `enabled` 条目独立同步循环、独立 manifest；条目需使用互不相同的 `local_path`
- **增量对比**：基于 `git blob sha` 对比，只拉变更（新/改），未变化跳过
- **整仓归档**：变更多 / 首次同步时下载整仓 tar.gz（公开仓库走 codeload 直连，不占 `api.github.com` 未认证 60 次/小时限额），本地计算 git blob sha 对比；增量树 API 限流时自动降级归档
- **protect 保护清单**：相对 `local_path` 的路径（支持目录前缀），**永不覆盖、永不删除**（设备私有文件如 `tv.txt`，`delete: true` 时也跳过）
- **安全**：覆盖前 `simplePHPCheck` 校验 PHP 语法；`.bak.<时间戳>` 备份；路径穿越防护；归档解压防穿越
- **孤立文件报告**：每次同步列出"本地有、远端无"的文件（跳过 protect / `.bak` / 隐藏），供核对设备私有文件
- **Web 编辑器**：登录 `/web/sync-editor` 可视化增删多仓库（含 protect 清单）

### 配置段

```yaml
# 仓库同步（支持多仓库，每项独立同步到各自 local_path）
sync:
  - name: tvbox               # 标识（用于日志区分多仓库，可空）
    enabled: false            # 是否启用
    type: github              # github | gitlab | gitee
    host: ""                  # 自建实例地址（自建 GitLab https://git.内网 或 Gitee https://gitee.com），留空 = 平台默认
    repo: owner/repo          # 仓库标识（GitLab 可为 group/project）
    branch: main              # 同步分支
    token: ""                 # PAT（GitHub: ghp_xxx；GitLab: glpat_xxx；Gitee: 私人令牌），公开仓库可留空
    interval: 60s             # 轮询间隔（最小 10s）
    repo_path: .              # 仓库内源子目录（"." = 仓库根）
    local_path: tvbox         # 本地目标：以 php docroot 为锚点；"." = docroot 根，"tvbox" = docroot/tvbox
    only_php: false           # 是否只同步 .php/.phtml/.php3/.php4/.inc（tvbox 混合内容默认 false 全量）
    backup: true              # 覆盖/删除前备份为 .bak.<时间戳>
    delete: false             # 远端已删除的文件，本地是否也删除（false 则保留）
    protect: []               # 本地保护清单（相对 local_path，支持目录前缀）：永不覆盖、永不删除（如设备私有 tv.txt）
    timeout: 15s              # 单次 API/下载请求超时
```

> **平台支持**：`type` 支持 `github` / `gitlab` / `gitee`。`host` 留空用平台默认（gitlab.com / gitee.com）；**自建 GitLab**（内网 IP/端口）填 `host` 即可（API v4 路径一致）；**Gitee** 走 API v5，token 用 Gitee 私人令牌，归档为 zip。

> **访问令牌（token）**：Web 编辑器保存后令牌**不回显**（显示 `********`，掩码占位保存会保留原值、填新值才覆盖），避免凭据泄露。GitHub 未认证仅 60 次/小时，建议公开仓库也配置一个只读 PAT（Contents: Read）以提升到 5000 次/小时，稳定高频轮询。
>
> **详细设计**：见 [doc/sync-dev.md](doc/sync-dev.md)（同步算法 / 归档降级 / 孤立文件 / 生命周期 / 测试计划）。

### 访问同步内容

同步下来的文件落在 **`docroot + local_path`** 目录（例如 `docroot/tvbox`），PHP 模块对 `/php/` 前缀下的**静态文件**按正确 MIME 直接返回（`.json` / `.txt` / `.m3u` / `.jar` / `.js` / `.py` 等），因此可直接通过 HTTP 访问，作为 TVBox 订阅 / 直播源地址：

```
http://<IP>:<port>/php/tvbox/0707.json      # TVBox 订阅配置
http://<IP>:<port>/php/tvbox/listx.m3u      # 直播源列表
http://<IP>:<port>/php/tvbox/jar/spider.jar # 爬虫插件
```

- 前提：PHP 模块已启用（`php.enabled: true`），`local_path` 以 `docroot` 为锚点。
- 若 `local_path` 为其他子目录（如 `www/scripts`），则路径为 `/php/www/scripts/...`。
- 若 `local_path` 为 `.`（同步到 docroot 根），则路径为 `/php/<文件>`。
- 未启用 PHP 模块时，无法通过 `/php/` 访问；可将这些文件放到其他静态目录或直接经代理端口访问。

> **典型用法**：在 TVBox 的"配置订阅"里填 `http://<IP>:<port>/php/tvbox/0707.json`，电视端即可自动拉取并更新订阅；多仓库各自同步到不同 `local_path` 即可管理多套配置。

---

## 配置（config.yaml）示例

> 下例为示意配置，实际字段名以程序版本为准，请将此片段改成你需要的字段结构。

```yaml
server:
  #监听端口
  port: 8888
  # 证书路径
  certfile: ""
  # 密钥路径
  keyfile: ""
  # SSL 协议版本 (空为默认 TLSv1.2~1.3)
  ssl_protocols: "TLSv1.2 TLSv1.3"
  # SSL 加密套件 (空为默认安全套件)
  ssl_ciphers: "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256:TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256:TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384:TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384:TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305:TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305:TLS_AES_128_GCM_SHA256:TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256"
  # SSL ECDH 曲线 (支持 ML-KEM)
  ssl_ecdh_curve: "X25519MLKEM768:X25519:P-384:P-256"

  # 组播监听地址
  multicast_ifaces: [] # 可留空表示默认接口 [ "eth0", "eth1" ]

# github 加速配置 更新可以用到
github:
    enabled: false
    url: https://hk.gh-proxy.com
    timeout: 10s
    retry: 3
    backup_urls:
        - https://github.dpik.top
        - https://gitproxy.127731.xyz

# 监控配置
monitor:
  path: "/status"   # 状态信息 

# 配置文件编辑接口
web:
    enabled: true
    username: admin
    password: admin
    path: /web/ # 自定义路径

# 日志输出配置
log:
  # 是否输出日志
  enabled: true
  # 日志输出文件地址 "" 表示标准输出，否则输出到指定文件 ./access.log
  file: ""
  # 日志大小M单位
  maxsize: 10
  # 压缩文件备份个数
  maxbackups: 10
  # 日志保留天数
  maxage: 28
  # 是否压缩
  compress: true

http:
  timeout: 0s # 整个请求超时时间 (0 表示不限制)
  connect_timeout: 10s # 建立连接的超时时间
  keepalive: 10s # 长连接的保活时间
  response_header_timeout: 10s # 接收响应头的超时时间
  idle_conn_timeout: 5s # 空闲连接在连接池中的保留时间
  tls_handshake_timeout: 10s # TLS 握手超时时间
  expect_continue_timeout: 1s # Expect: 100-continue 的等待超时时间
  max_idle_conns: 100 # 最大空闲连接数（全局）
  max_idle_conns_per_host: 4 # 每个主机最大空闲连接数
  max_conns_per_host: 8 # 每个主机最大连接数（总数，含空闲和活跃）
  disable_keepalives: false # 是否禁用长连接复用 (false 表示启用 KeepAlive)

# jx 视频解析接口配置 支持 某奇 某果 某讯 某尤 某咕
jx:
    path: "/jx" # jx 接口路径，可自定义，例如 /jx
    default_id: "1" # 默认集数，如果请求未传 id，则使用此值
    api_groups:
        other_api:
            endpoints:
                - "http://23.224.101.30"
                - "https://mozhuazy.com"
            timeout: 10s
            query_template: "%s/api.php/provide/vod/?ac=detail&wd=%s"
            primary: true
            weight: 2
            fallback: true
            max_retries: 3
            filters:
                exclude: "电影解说,完美世界剧场版"

domainmap:
    - name: localhost-to-test
      source: test.test.cc
      target: www.bing.cn
      client_headers:
        X-Forwarded-For: 192.168.100.1
      server_headers:
        X-Forwarded-Proto: http
      protocol: http
    - name: 34444
      source: www.baidu.com
      target: 96336.ww.com
      client_headers:
        ua: 1236545
      protocol: rtsp                

reload: 5

global_auth:
    tokens_enabled: false
    token_param_name: my_token
    dynamic_tokens:
        enable_dynamic: false
        dynamic_ttl: 1h
        secret: mysecretkey12345
        salt: staticSaltValue
    static_tokens:
        enable_static: false
        token: token123
        expire_hours: 1h

# 仓库同步（将 GitHub/GitLab 仓库单向同步到本地 docroot 子目录，支持多仓库）
sync:
    - name: tvbox               # 标识（用于日志区分多仓库，可空）
      enabled: false            # 是否启用
      type: github              # github | gitlab | gitee
      host: ""                  # 自建实例地址（自建 GitLab https://git.内网 或 Gitee https://gitee.com），留空 = 平台默认
      repo: owner/repo          # 仓库标识 owner/repo（GitLab 可为 group/project）
      branch: main              # 同步分支
      token: ""                 # PAT（GitHub: ghp_xxx；GitLab: glpat_xxx；Gitee: 私人令牌），公开仓库可留空
      interval: 60s             # 轮询间隔（最小 10s）
      repo_path: .              # 仓库内源子目录（"." = 仓库根）
      local_path: tvbox         # 本地目标：以 php docroot 为锚点；"." = docroot 根，"tvbox" = docroot/tvbox
      only_php: false           # 是否只同步 .php/.phtml/.php3/.php4/.inc（混合内容默认 false 全量）
      backup: true              # 覆盖/删除前备份为 .bak.<时间戳>
      delete: false             # 远端已删除的文件，本地是否也删除（false 则保留）
      protect: []               # 本地保护清单（相对 local_path，支持目录前缀）：永不覆盖、永不删除（如设备私有 tv.txt）
      timeout: 15s              # 单次 API/下载请求超时

proxygroups:
  蜀小果:
    proxies:
      - name: 服务器1
        type: socks5
        server: 1.1.1.1
        port: 1080
        udp: true
      - name: 服务器2
        type: https
        server: 8.8.8.8
        port: 1234
    domains:
      - live2.rxip.sc96655.com
    interval: 180s
    ipv6: false
    loadbalance: round-robin
    max_retries: 3
    retry_delay: 1s
    max_rt: 100ms
  四川联通:
    proxies:
      - name: sclt1
        type: socks5
        server: 1.2.3.4
        port: 1080
        udp: true
      - name: sclt2
        type: socks5
        server: 4.3.2.1
        port: 1080
        udp: true
    domains:
      - "*.rrs.169ol.com"
    interval: 180s
    ipv6: false
    loadbalance: round-robin
    max_retries: 3
    retry_delay: 1s
    max_rt: 100ms
  浙江移动:
    proxies:
      - name: 浙江移动1
        type: socks5
        server: 192.168.100.1
        port: 8080
        udp: true
    domains:
      - hwltc.tv.cdn.zj.chinamobile.com
      - 39.134.179.0/24
    interval: 180s
    loadbalance: round-robin
    max_retries: 3
    retry_delay: 1s
    max_rt: 200ms
  mpd:
    proxies:
      - name: mpd1
        type: socks5
        server: 192.168.100.1
        port: 8888
    domains:
      - 1.1.1.1
      - "edgeware-live.edgeware.tvb.com"
      - "*.edgeware.tvb.com"
      - "hki*-edge*.edgeware.tvb.com"
      - 2001:::1
      - 2409:8087:0::/48
    interval: 180s
    ipv6: true
    loadbalance: fastest
```

---

## Nginx 反向代理配置参考

当你在前端放置 Nginx 做 TLS 终端或域名路由时，建议如下配置把请求反代到本地 TVGate：

```nginx
server {
    listen 80;
    listen 443 ssl http2;
    server_name dl.test.com;

    ssl_certificate     /etc/nginx/ssl/dl.test.com.crt;
    ssl_certificate_key /etc/nginx/ssl/dl.test.com.key;

    proxy_http_version 1.1;
    proxy_set_header   Host $host;
    proxy_set_header   X-Real-IP $remote_addr;
    proxy_set_header   X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header   X-Forwarded-Proto $scheme;

    # 特殊情况: 路径以 /http:// 或 /https:// 开头，直接交给后端处理
    location ~ ^/http(s)?:// {
        proxy_pass http://127.0.0.1:8888;
        proxy_set_header Host $host;
    }

    location / {
        proxy_pass http://127.0.0.1:8888;
        proxy_set_header Host $host;
        proxy_buffering off;
        proxy_cache off;
    }
}
```

---

## 注意事项 / 常见问题

- **安全性**：如果将 TVGate 暴露到公网，请务必在前端使用 TLS（NGINX/证书）并限制访问（IP 白名单、HTTP 认证、VPN 等）。
- **带宽与性能**：流媒体转发占用大量上行带宽，请确认宿主机带宽足够。
- **版权合规**：请确保你有权限分发和访问被转发的内容。
- **端口冲突**：如果 `8888` 被占用，请在配置或启动参数中修改监听端口。
- **自动重载配置**：修改 `config.yaml` 后观察日志，确认程序已加载新配置。

---

## Linux 内核优化建议

TVGate 作为流媒体转发代理，高并发场景下需要对 Linux 内核参数做适当调优。以下配置写入 `/etc/sysctl.d/99-tvgate.conf` 后执行 `sysctl -p` 生效。

### 文件描述符

```bash
# 查看当前值
ulimit -n
cat /proc/sys/fs/file-max

# 临时生效
ulimit -n 1048576

# 永久生效 /etc/security/limits.conf
*  soft  nofile  1048576
*  hard  nofile  1048576
```

systemd 用户在 `TVGate.service` 中已配置 `LimitNOFILE=100000`，可根据需要调大。

### 网络缓冲区

```ini
# /etc/sysctl.d/99-tvgate.conf

# TCP 读写缓冲区（流媒体大流量场景）
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.core.rmem_default = 262144
net.core.wmem_default = 262144

# TCP 缓冲区自动调节下限/上限
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216

# UDP 缓冲区（组播/RTP 转发关键）
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
```

### 连接与保活

```ini
# SYN 队列与积压
net.ipv4.tcp_max_syn_backlog = 65535
net.core.somaxconn = 65535

# TCP KeepAlive（快速回收死连接）
net.ipv4.tcp_keepalive_time = 600
net.ipv4.tcp_keepalive_intvl = 30
net.ipv4.tcp_keepalive_probes = 3

# FIN 超时与回收
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_tw_reuse = 1

# 本地端口范围（高并发连接数）
net.ipv4.ip_local_port_range = 1024 65535
```

### conntrack（连接跟踪）

```ini
# 连接跟踪表大小（高并发必须调大，否则日志出现 nf_conntrack: table full）
net.netfilter.nf_conntrack_max = 1048576
net.netfilter.nf_conntrack_tcp_timeout_established = 3600
net.netfilter.nf_conntrack_tcp_timeout_time_wait = 30
```

### 交换与内存

```ini
# 降低 swap 倾向（流媒体服务优先用物理内存）
vm.swappiness = 1

# 内存过量提交策略（避免 OOM killer 误杀）
vm.overcommit_memory = 1
```

### 网卡队列与多核

```ini
# 网卡接收队列长度
net.core.netdev_max_backlog = 65535

# 开启 RPS/RFS（多核负载均衡，小设备可不配）
net.core.rps_sock_flow_entries = 32768
```

### OpenWrt / 小设备精简版

资源有限的设备（路由器等）建议只调以下几项：

```ini
net.core.rmem_max = 4194304
net.core.wmem_max = 4194304
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_tw_reuse = 1
vm.swappiness = 0
```

同时确保服务启动脚本中配置了合理的 ulimit：

```bash
# /etc/init.d/tvgate 或 procd 脚本中
ulimit -n 65535
```

### CPU 性能模式（关闭节能 / 最高性能）

流媒体转发对实时性要求高，CPU 进入节能模式会导致突发负载时延迟升高（C-state 唤醒延迟可达数十微秒）。建议关闭节能、锁定最高性能：

```bash
# ==================== 1. CPU 调速器设为 performance ====================

# 查看当前调速器
cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor

# 临时生效：全部核心设为 performance
for gov in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do
    echo performance > "$gov"
done

# 永久生效（推荐）：
# 安装 cpupower 工具
apt install linux-tools-common    # Debian/Ubuntu
yum install kernel-tools          # CentOS/RHEL

# 设置全局调速器
cpupower frequency-set -g performance

# ==================== 2. 关闭 C-states（CPU 睡眠状态） ====================

# 通过内核启动参数关闭（编辑 /etc/default/grub）
# 在 GRUB_CMDLINE_LINUX_DEFAULT 中追加：
#   processor.max_cstate=1 intel_idle.max_cstate=0 idle=poll
# 例如：
#   GRUB_CMDLINE_LINUX_DEFAULT="quiet processor.max_cstate=1 intel_idle.max_cstate=0 idle=poll"
# 更新 GRUB 并重启：
update-grub         # Debian/Ubuntu
grub2-mkconfig -o /boot/grub2/grub.cfg   # CentOS/RHEL
reboot

# ==================== 3. 关闭 Intel Turbo Boost 降频 ====================

# 查看当前 Turbo Boost 状态（Intel）
cat /sys/devices/system/cpu/intel_pstate/no_turbo
# 0 = Turbo Boost 开启（正常），1 = 关闭

# 确保 Turbo Boost 开启（充分发挥 CPU 性能）
echo 0 > /sys/devices/system/cpu/intel_pstate/no_turbo

# ==================== 4. 切换 Intel P-State 驱动为 performance 模式 ====================

# 如果使用 intel_pstate 驱动
echo performance > /sys/devices/system/cpu/intel_pstate/status

# ==================== 5. BIOS 设置（手动操作） ====================

# 在 BIOS 中关闭以下选项（不同主板名称略有差异）：
#   - C-States Control / CPU C-States → Disable
#   - Enhanced Halt State (C1E) → Disable
#   - Intel Speed Shift Technology (HWP) → Enable（硬件快速调频）
#   - Intel Turbo Boost Technology → Enable
#   - Power Management → Maximum Performance
```

> **OpenWrt / ARM 设备**：ARM64 平台同样支持 cpufreq 调速。OpenWrt 没有 `cpufrequtils` 包，直接写 sysfs 即可，加入 `/etc/rc.local` 开机自动生效：
> ```bash
> # /etc/rc.local
> for cpu in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do
>     echo performance > "$cpu"
> done
> ```
> MIPS 路由器一般不支持 cpufreq，无需此步骤。

---

## 相关项目

| 项目 | 说明 | 下载 |
|------|------|------|
| [tvgate](https://github.com/qist/tvgate) | 服务端核心（本仓库） | [Releases](https://github.com/qist/tvgate/releases) |
| [tvgate-android](https://github.com/qist/tvgate-android) | Android IPTV 播放客户端 | [Releases](https://github.com/qist/tvgate-android/releases) |
| [luci-app-tvgate](https://github.com/qist/luci-app-tvgate) | OpenWrt LuCI 管理插件 | [Releases](https://github.com/qist/luci-app-tvgate/releases) |

---

### Star

[![Star History Chart](https://star-history.dera.page/svg?repos=qist/tvgate&type=Date)](https://star-history.dera.page/#qist/tvgate&Date)
