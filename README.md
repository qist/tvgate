# TVGate — IPTV 转发 / 代理工具

> 高性能的本地内网流/网页资源转发与代理工具，将内部可访问的 `http`/`rtsp`/`rtp` 等资源安全地发布到外网，并支持通过多种上游代理跨区域访问受限资源。
---

## 目录

- [TVGate — IPTV 转发 / 代理工具](#tvgate--iptv-转发--代理工具)
  - [目录](#目录)
  - [功能](#功能)
    - [转发](#转发)
    - [代理](#代理)
  - [快速开始](#快速开始)
    - [安装](#安装)
    - [运行示例](#运行示例)
  - [📦 使用 Docker 启动](#-使用-docker-启动)
    - [方式一：使用 ghcr.io 镜像](#方式一使用-ghcrio-镜像)
    - [方式二：使用 Docker Hub 镜像](#方式二使用-docker-hub-镜像)
    - [udp转发：](#udp转发)
    - [docker-compose 示例](#docker-compose-示例)
  - [服务管理 / 启动脚本](#服务管理--启动脚本)
    - [systemd (Linux)](#systemd-linux)
    - [OpenWrt 安装](#openwrt-安装)
    - [代理规则格式](#代理规则格式)
  - [使用示例（外网访问路径）](#使用示例外网访问路径)
  - [🔹 jx 视频解析接口](#-jx-视频解析接口)
  - [配置（config.yaml）示例](#配置configyaml示例)
  - [Nginx 反向代理配置参考](#nginx-反向代理配置参考)
  - [注意事项 / 常见问题](#注意事项--常见问题)
    - [Star](#star)

---
changelog
v2.1.13

```
1、RTSP 优化。
2、组播优化。
3、组播异常客户端无法连接监听不释放bug修复。
4、更新依赖。
```

v2.1.12

```
1、fcc 优化。
2、依赖更新。
```

v2.1.11

```
使用fcc 不正常释放bug 修复。
```

v2.1.10

```
1、ts 缓存添加开关支持。 小设备建议关闭缓存。默认关闭缓存。
2、fcc 优化。
```

v2.1.9

```
1、fcc 优化。
2、hls 转发 优化，增加ts 文件缓存 web 界面可配置。
3、删除web页面特征码。
4、更新一下依赖。
5、代理 dns 解析遵循ipv6 开关设置。
```

v2.1.8

```
1、修复转发html页面时打开一直加载问题。
2、修复域名映射只能单客户端播放bug。
3、修复web 界面优先项yaml没配置添加失败bug。
4、优化rtsp 转发性能。
5、HTTP HUB 基于路径跟状态码设计，当状态是200 跟206 的时候数据实现不一致不播放bug。200 全部资源 普通请求 206 分段下载、断点续传、视频流分片。
```

v2.1.7

```
1、http转发hub模式验证。
2、开启yaml没配置web界面都显示相关项。
3、一些其它优化。
4、HTTP配置编辑器 添加 跳过TLS证书验证
```

v2.1.6

```
1、修复组播断流。添加参数 mcast_rejoin_interval 组播重连间隔时间 10s
2、优化fcc web添加fcc 配置。 
访问 http://server:port/rtp/239.253.64.120:5140?fcc=10.255.14.152:15970&operator=huawei operator 后端配置可以不携带operator 参数。FCC类型: telecom, huawei
访问 http://server:port/rtp/239.253.64.120:5140?fcc=10.255.14.152:15970
```

v2.1.5

```
1、删除首页特征改成404页面，防止被抓取。
2、更新一些依赖。
3、组播添加fcc支持。
http://127.0.0.1:8888/udp/239.49.1.68:8000?fcc=180.100.72.185:15970
```

v2.1.4

全局认证配置（用于所有转发）
```
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

1、修复了一些bug
```

v2.1.3

```
1、组播优化，解决卡顿，播放器兼容问题。
```

v2.1.2

```
1、重写多播转发
2、添加dns配置当远程dns不能解析退回本地dns解析，代理默认代理服务器dns解析如果不能解析配置dns进行解析没配置使用本地dns解析。 
3、流畅性优化
```

v2.1.1
```
1、端口分离 支持 http https 管理端口独立配置 详细配置参考web 页面 服务器编辑.
2、更新了一些依赖。 
3、修复win闪退问题
```

v2.1.0
```
1、多播优化。
```
v2.0.9
```
1、添加Linux 版本的更新支持,win 版本需要手动更新。
2、用systemctl 启动的脚本需要添加 Restart=always 配置。 
3、openwrt 启动脚本需要添加  procd_set_param respawn 配置。
4、添加github接口加速配置。
```

v2.0.8
```
1、不在需要自己创建配置文件，启动程序会自动生成配置文件。启动方式支持 ./TVGate-linux-arm64 -config=/usr/local/TVGate/config.yaml 也支持目录 ./TVGate-linux-arm64 -config=/usr/local/ 直接启动 ./TVGate-linux-arm64 当前目录生成配置文件。
2、添加域名映射支持：
当然一样支持代理后的映射 www.bing.com 不能直连配置代理了一样可以映射访问
配置格式:
domainmap:
    - name: localhost-to-test
      source: test.test.cc # 自己的域名或IP地址 如果是ip 映射 别人就不能用ip 做代理了 打开是映射的网页 可以解析自己的域名 使用原始代理 不配置映射
      target: www.bing.com # 需要代理的域名 映射 80 http 443 https 其它端口记得携带上完整端口  www.bing.com:8080
      client_headers: # 前端验证头 头验证
        X-Forwarded-For: 192.168.100.1
      server_headers: # 后端发送头
        X-Forwarded-Proto: http
      protocol: http # 可选 默认http 支持 https http rtsp
    - name: 34444
      source: rtsp.test.cc
      target: 123.147.112.17:8089
      client_headers:
        User-Agent: okhttp/3.12.0 # 前端设置头 必须一致 okhttp 就不能访问
      protocol: rtsp
    - name: 99999
      source: https.test.cc
      target: 96336.ww.com
      protocol: https
    - name: other # 默认后端是http
      source: other.test.cc
      target: 96336.ww.com
访问：
http: http://test.test.cc:8888/PLTV/88888888/224/3221236260/index.m3u8
https: http://https.test.cc:8888/PLTV/88888888/224/3221236260/index.m3u8
rtsp: http://rtsp.test.cc:8888/04000001/01000000004000000000000000000231?
3、配置可以完全web 编辑可见所得 配置保存后等待后端重新加载后在点击前端的重新加载配置。
4、配置文件没有对应的主节点 打开 YAML编辑器 添加主节点 web 就会自动显示跟相关配置
5、添加在线配置还原删除备份，每次修改配置会自动备份。文件名字 config.yaml.backup.20250917171446 config.yaml 这个名字是你指定的配置文件名字 不一定是config.yaml aaa.yaml 等
6、还有一些影藏技能自己去发现了。
```
## 功能

### 转发
将内网可访问的资源（如 `http`, `https`, `rtsp`, `rtp`）通过 HTTP 对外发布，外网用户访问 Go 程序所在主机的端口（默认 `8888`）即可获取流或请求代理的资源。

支持的常见场景：
- 将内网 RTP / 组播 转为可通过 HTTP 访问（类似 udpxy）
- 将运营商提供的 RTSP / HTTP 单播转发并通过外网访问
- 将局域网内的 PHP 动态脚本通过外网访问（如 `huya.php`）

---

### 代理
支持上游代理（`socks5`、`socks4`、`http`），可为不同域名 / IP / 子网 指定不同上游代理，实现跨区域、跨运营商访问受限内容。

- **动态重载配置**：修改 `config.yaml` 后程序会自动重载配置（无需重启）。
- **规则类型**：单 IP、CIDR 子网、域名通配符、IPv6 等。

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
    image: ghcr.io/qist/tvgate:latest   # 或 juestnow/tvgate:latest  #不能下载 可以换成 67686372.boown.com/qist/tvgate:latest
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
下载 `https://github.com/qist/luci-app-tvgate`

1. Install the generated ipk package:
   ```bash
   opkg update
   opkg install curl ca-certificates unzip luci-compat luci luci-base
   opkg install /tmp/luci-app-tvgate_1.0.0_all.ipk
   opkg install /tmp/luci-i18n-tvgate-zh-cn_1.0.0-1_all.ipk
   opkg install /tmp/luci-i18n-tvgate-en_1.0.0-1_all.ipk
   ```

2. Uninstall package:
   ```bash
   opkg remove luci-app-tvgate
   opkg remove luci-i18n-tvgate-en
   opkg remove luci-i18n-tvgate-zh-cn
   ```
3. openwrt 25 Install the generated apk package:
 ```bash
apk update
apk add curl ca-certificates unzip luci-compat luci luci-base
apk add --allow-untrusted luci-app-tvgate-1.0.0-r1.apk
apk add --allow-untrusted luci-i18n-tvgate-en-1.0.0-r1.apk
apk add --allow-untrusted luci-i18n-tvgate-zh-cn-1.0.0-r1.apk
```
4. openwrt 25 Uninstall package:
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
- 需要代理的ip 域名尽量都添加，如果日志出现  dial tcp 210.13.7.109:80: i/o timeout 那就把 210.13.7.109/24 添加到代理规则中
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
# 10 万并发参考
#  http:
#   timeout: 0s                       # 整体请求超时，不限制（由上层逻辑控制超时）
#   connect_timeout: 3s               # 建立连接的超时时间（越短越好，失败快速切换）
#   keepalive: 30s                    # 长连接保活时间，保证高并发时连接复用
#   response_header_timeout: 5s       # 响应头超时，避免服务端卡死
#   idle_conn_timeout: 90s            # 空闲连接保留时间，过短会频繁建连，过长会浪费 FD
#   tls_handshake_timeout: 5s         # TLS 握手超时，CDN/直播源一般很快
#   expect_continue_timeout: 1s       # 基本不用，保持默认

#   max_idle_conns: 200000            # 全局最大空闲连接数（10 万并发需要翻倍冗余）
#   max_idle_conns_per_host: 10000    # 单 host 的空闲连接上限，保证热点源站可复用
#   max_conns_per_host: 20000         # 单 host 总连接数上限（活跃+空闲），防止热点源阻塞

#   disable_keepalives: false         # 必须启用长连接，否则 10 万并发会把源站打爆

# 配置文件重新加载时间(秒)

# jx 视频解析接口配置 支持 某奇 某果 某讯 某尤 某咕
jx:
    path: "/jx" # jx 接口路径，可自定义，例如 /jx
    default_id: "1" # 默认集数，如果请求未传 id，则使用此值
    # 多个视频 API 组配置，可以配置不同的视频源
    api_groups:
        other_api:
            endpoints:
                - "http://23.224.101.30" # 主 API 地址
                - "https://mozhuazy.com" # 备用 API 地址
            timeout: 10s # 请求超时
            query_template: "%s/api.php/provide/vod/?ac=detail&wd=%s" # 查询 URL 模板，%s 会被替换为 endpoint 和搜索关键词
            primary: true # 是否主 API
            weight: 2 # 权重，用于负载均衡
            fallback: true # 是否可以作为备用 API
            max_retries: 3 # 请求失败重试次数
            filters:
                exclude: "电影解说,完美世界剧场版" # 排除包含指定关键字的视频

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
        # headers:
        #   Host: "1.3.236.22:443"
        #   X-T5-Auth: "887766543"
        #   User-Agent: "baiduboxapp"
    #     - name: test1
    #       type: socks5
    #       server: 192.168.0.151
    #       port: 7890
    #       # username: "qist" # 账号
    #       # password: "123456789" # 密码
    #     - name: test2
    #       type: socks4  #认证没实现
    #       server: 192.168.0.151
    #       port: 7891
    #     - name: test2
    #       type: socks4a  #认证没实现
    #       server: 192.168.0.151
    #       port: 7891
    #     - name: test3
    #       type: http
    #       server: 192.168.0.151
    #       port: 7890
    #       # username: "qist" # 账号
    #       # password: "123456789"  # 密码
    #       # headers: # 代理服务器验证headers 配置
    #       #   Host: "1.3.236.22:443"
    #       #   X-T5-Auth: "887766543"
    #       #   User-Agent: "baiduboxapp"
    #     # - name: test4
    #       # type: https
    #       # server: 78.141.193.27
    #       # port: 8888
    #       # username: "qist"
    #       # password: "123456789"
    #       # headers: # 代理服务器验证headers 配置
    #       #   Host: "1.3.236.22:443"
    #       #   X-T5-Auth: "887766543"
    #       #   User-Agent: "baiduboxapp"
    domains: # 支持通配符号*
      - live2.rxip.sc96655.com
    interval: 180s # 秒 默认60s 健康检测时间
    ipv6: false # IPv6开关 true 开启
    loadbalance: round-robin # 负载均衡方案：round-robin 轮询 fastest 最快的优先
    max_retries: 3 # 最大重试3次
    retry_delay: 1s # 重试延迟1秒
    max_rt: 100ms # 最大响应时间 默认800ms 大于800ms 不参与轮询 如果所有测速大于800ms 参数轮询
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
    domains: # 支持通配符号*
      - "*.rrs.169ol.com" # 规则支持ip 192.168.1.1 子网 192.168.1.0/24 域名 *.rrs.169ol.com live2.rxip.sc96655.com ipv6：1234:5678::abcd:ef01/128
    interval: 180s # 秒 默认60s 健康检测时间
    ipv6: false # IPv6开关 true 开启
    loadbalance: round-robin # 负载均衡方案：round-robin 轮询 fastest 最快的优先
    max_retries: 3 # 最大重试3次
    retry_delay: 1s # 重试延迟1秒
    max_rt: 100ms # 最大响应时间 默认800ms 大于800ms 不参与轮询 如果所有测速大于800ms 参数轮询
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
      - 39.134.179.185/24
      - 120.199.224.178/24
    interval: 180s
    loadbalance: round-robin
    max_retries: 3 # 最大重试3次
    retry_delay: 1s # 重试延迟1秒
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
      - 2409:8087:1::/48
      - 2409:8087:8::/48
      - 2409:8087:9::/48
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

### Star

[![Star History Chart](https://api.star-history.com/svg?repos=qist/tvgate&type=Date)](https://www.star-history.com/#qist/tvgate&Date)
