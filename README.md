# TVGate — IPTV 转发 / 代理工具

> 高性能的本地内网流/网页资源转发与代理工具，将内部可访问的 `http`/`rtsp`/`rtp` 等资源安全地发布到外网，并支持通过多种上游代理跨区域访问受限资源。

- **转发**：内网 RTP 组播 / RTSP / HTTP / HTTPS / PHP 动态页 → HTTP 对外发布（类似 udpxy + 反代）
- **代理**：按域名 / IP / 子网 分组指定上游代理（socks5/socks4/http/https），跨区域访问受限源
- **H5 播放器**：IPTV 订阅直播 / EPG / 回看，真实源地址不外露
- **推流发布**：拉流转推 RTMP 多平台 + 本地 FLV/HLS 播放 + 录像归档
- **内置 PHP 运行时**：纯 Go phpgo，无需 PHP-FPM，安卓也能跑
- **Web 管理后台**：可视化配置全部模块，配置热重载无需重启

## 📖 详细文档

所有模块的详细配置说明、字段表与完整示例都在 `doc/` 目录：

| 模块 | 文档 | 说明 |
|------|------|------|
| 服务器与传输 | [doc/SERVER.md](doc/SERVER.md) | `server` 监听端口 / TLS / `http` 连接池 / `reload` 热重载 |
| Web 管理后台 | [doc/WEB.md](doc/WEB.md) | 登录 / 二次授权 / 代码文件管理 / 三处备份机制 |
| H5 播放器 | [doc/PLAYER.md](doc/PLAYER.md) | IPTV 订阅 / EPG / 回看 / 不透明 key / `/pp` 播放入口 |
| 推流发布 | [doc/PUBLISHER.md](doc/PUBLISHER.md) | RTMP 转推 / 本地 FLV·HLS / 录像回放 / MP4 归档 / 配置模板 |
| 代理组 | [doc/PROXYGROUPS.md](doc/PROXYGROUPS.md) | 按域名/IP 分组上游代理、测速切换、规则格式 |
| 组播配置 | [doc/MULTICAST.md](doc/MULTICAST.md) | RTP 组播转 HTTP、FCC 快速换台 |
| 域名映射 | [doc/DOMAINMAP.md](doc/DOMAINMAP.md) | 请求域名改写 + 自定义请求头 |
| 全局认证 | [doc/GLOBALAUTH.md](doc/GLOBALAUTH.md) | 动态/静态 token 校验，保护转发与播放入口 |
| 视频解析 | [doc/JX.md](doc/JX.md) | `jx` 影视解析接口，对接 TVBox |
| TS 缓存 | [doc/TS.md](doc/TS.md) | TS 分片内存缓存，多观众少回源 |
| PHP 模块 | [doc/PHP.md](doc/PHP.md) | 纯 Go phpgo runtime，`/php/` 执行脚本 |
| 定时任务 | [doc/TASKS.md](doc/TASKS.md) | cron 调度，支持系统命令与 `php://` 内部执行 |
| 仓库同步 | [doc/SYNC.md](doc/SYNC.md) | GitHub/GitLab/Gitee 单向同步到 docroot（TVBox 订阅托管） |
| GitHub 加速 | [doc/GITHUB.md](doc/GITHUB.md) | 仓库同步与版本升级共用的加速地址配置 |
| DNS | [doc/DNS.md](doc/DNS.md) | 自定义 DNS 与三级解析兜底链 |
| 日志 | [doc/LOG.md](doc/LOG.md) | 日志输出与轮转 |
| 梅林部署 | [doc/MERLIN.md](doc/MERLIN.md) | Asuswrt-Merlin 路由器 Entware 一键安装 |
| 性能调优 | [doc/TUNING.md](doc/TUNING.md) | 内核参数 / conntrack / CPU 性能模式 |
| 配置总示例 | [doc/config.yaml](doc/config.yaml) | 全模块配置样例（字段注释） |
| 更新日志 | [doc/CHANGELOG.md](doc/CHANGELOG.md) | 版本变更记录 |

## 目录

- [功能](#功能)
- [详细文档](#-详细文档)
- [下载](#下载)
- [快速开始](#快速开始)
- [Docker 启动](#-使用-docker-启动)
- [服务管理](#服务管理--启动脚本)
- [使用示例](#使用示例外网访问路径)
- [Nginx 反向代理](#nginx-反向代理配置参考)
- [注意事项](#注意事项--常见问题)

---

## 功能

### 转发
将内网可访问的资源（如 `http`, `https`, `rtsp`, `rtp`）通过 HTTP 对外发布，外网用户访问 Go 程序所在主机的端口（默认 `8888`）即可获取流或请求代理的资源。

支持的常见场景：
- 将内网 RTP / 组播 转为可通过 HTTP 访问（类似 udpxy）
- 将运营商提供的 RTSP / HTTP 单播转发并通过外网访问
- 将局域网内的 PHP 动态脚本通过外网访问（如 `huya.php`）
- H5 播放器：订阅 IPTV 频道清单，浏览器直接看直播/回看（真实源不外露）
- 定时任务：cron 调度执行系统命令或 docroot 内 PHP 脚本（安卓无原生 php 也可用）

### 代理
支持上游代理（`socks5`、`socks4`、`http`、`https`），可为不同域名 / IP / 子网指定不同上游代理，实现跨区域、跨运营商访问受限内容。

- **动态重载配置**：修改 `config.yaml` 后程序会自动重载配置（无需重启）
- **规则类型**：单 IP、CIDR 子网、域名通配符、IPv6 等，详见 [doc/PROXYGROUPS.md](doc/PROXYGROUPS.md)

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

### 梅林固件（Asuswrt-Merlin）

> 华硕路由器梅林固件用户可通过 Entware 一键部署：U 盘安装、开机自启、U 盘弹出自动停止。
> 教程见 [doc/MERLIN.md](doc/MERLIN.md)，一键脚本 [doc/scripts/merlin-install.sh](doc/scripts/merlin-install.sh)。

---

## 快速开始

### 安装
1. 下载对应平台二进制（示例）并放到 `/usr/local/TVGate/`（或你的目录）。
2. 第一次启动配置文件会自动创建，后续修改请直接编辑 `config.yaml` 或者web 界面编辑。
3. 启动：
```bash
nohup /usr/local/TVGate/TVGate-linux-amd64 -config=/usr/local/TVGate/config.yaml > /var/log/tvgate.log 2>&1 &
```

4. 浏览器打开 `http://<IP>:8888/web/` 进入管理后台（默认账号密码见配置 `web` 段），可视化配置所有模块。

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
- **带宽与性能**：流媒体转发占用大量上行带宽，请确认宿主机带宽足够；高并发内核调优见 [doc/TUNING.md](doc/TUNING.md)。
- **版权合规**：请确保你有权限分发和访问被转发的内容。
- **端口冲突**：如果 `8888` 被占用，请在配置或启动参数中修改监听端口。
- **自动重载配置**：修改 `config.yaml` 后观察日志，确认程序已加载新配置。

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