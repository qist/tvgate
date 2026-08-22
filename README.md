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
- [配置示例](#配置configyaml示例)
- [Nginx 反向代理](#nginx-反向代理配置参考)
- [Linux 内核优化](#linux-内核优化建议)
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

> **OpenWrt / ARM 设备**：ARM64 平台同样支持 cpufreq 调速，可按上述步骤 1 配置。MIPS 路由器一般不支持，无需此步骤。OpenWrt 可安装 `cpufrequtils` 包后执行 `cpufreq-set -g performance`。

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
