# TVGate iptv转发/代-理工具 v2.0.1
changelog
1、v2.0.1 组播转发支持指定网卡, 解决组播穿透问题
2、配置文件更新自动重载偶尔会失败, 修复

功能:
1. 转发
只要内网能访问的http rtsp rtp等资源,可以直接转发到外网.
例如:
你的公网ip是111.222.111.222,使用默认端口8888
①你的内网组播是rtp://239.0.0.1:2000,那么外网访问地址就是http://111.222.111.222:8888/udp/239.0.0.1:2000,和udpxy功能类似.
②你的运营商单播是rtsp://10.254.192.94/PLTV/88888888/224/3221225621/10000100000000060000000000009742_0.smil,
那么外网访问地址就是http://111.222.111.222:8888/rtsp/10.254.192.94/PLTV/88888888/224/3221225621/10000100000000060000000000009742_0.smil
③你的运营商单播是http://sc.rrs.169ol.com/PLTV/88888888/224/3221227984/index.m3u8,
那么外网访问地址就是http://111.222.111.222:8888/sc.rrs.169ol.com/PLTV/88888888/224/3221227984/index.m3u8
④https转发http://111.222.111.222:8888/https://sc.rrs.169ol.com/PLTV/88888888/224/3221227984/index.m3u8
⑤php代理转发http://111.222.111.222:8888/192.168.1.10/huya.php?id=11342412

1. 代-理
支持设置socks5 socks4 http代-理,通过代-理设置,可以跨地区访问有地区限制的资源.
例如:
运行本程序的路由器(服务器/电脑)ip是192.168.1.1,使用默认端口8888
设置好四川联通的代-理服务器,局域网中就可以访问http://192.168.1.1:8888/sc.rrs.169ol.com:80/PLTV/88888888/224/3221227984/index.m3u8
通过设置多个区域代-理服务器,全球可达!
(代-理服务器设置,请看配置文件config.yaml,程序有自动重载功能,修改配置文件不用重启!)
规则：支持ip 192.168.1.1 子网 192.168.1.0/24 支持通配符号 "*.rrs.169ol.com" "hki*-edge*.edgeware.tvb.com" live2.rxip.sc96655.com ipv6：1234:5678::abcd:ef01/128 

程序使用Golang开发
包含linux-amd64 linux-arm64 linux-armv7 android-arm64 win版本支持win10 及win server 2016以上等多个版本

nohub /usr/local/TVGate/TVGate-linux-amd64 -config=/usr/local/TVGate/config.yaml && 后台启动

openwrt启动脚本参考
TVGate
```
#!/bin/sh /etc/rc.common
# Copyright (C) 2006-2011 OpenWrt.org

START=99
STOP=15

USE_PROCD=1
PROG=/apps/TVGate/TVGate-linux-arm64

start_service() {
    echo "启动代理程序..."
    procd_open_instance
    procd_set_param command $PROG -config=/apps/TVGate/config.yaml
    procd_set_param respawn
    procd_close_instance
}

shutdown() {
    stop

    for pid in $(pidof TVGate-linux-arm64)
    do
        [ "$pid" = "$$" ] && continue
        [ -e "/proc/$pid/stat" ] && kill $pid
    done
}
```

Linux启动服务脚本参考
TVGate.service
```
[Unit]
Description=TVGate - high performance web server
After=network.target 
[Service]
LimitCORE=infinity
LimitNOFILE=100000
LimitNPROC=100000
ExecStart=/usr/local/TVGate/TVGate-linux-amd64 -config=/usr/local/TVGate/config.yaml
PrivateTmp=true
ExecReload=/bin/kill -SIGHUP $MAINPID
[Install]
WantedBy=multi-user.target
```

nginx转发配置参考

```nginx
server {
    listen 80;
    listen 443 ssl http2;
    server_name dl.jsp47.com;

    # SSL 证书（如果有）
    ssl_certificate     /etc/nginx/ssl/dl.jsp47.com.crt;
    ssl_certificate_key /etc/nginx/ssl/dl.jsp47.com.key;

    # 常见优化
    proxy_http_version 1.1;
    proxy_set_header   Host $host;
    proxy_set_header   X-Real-IP $remote_addr;
    proxy_set_header   X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header   X-Forwarded-Proto $scheme;

    # =============================
    # 特殊情况: /http:// 或 /https:// 开头的路径
    # =============================
    location ~ ^/http(s)?:// {
        # 不改写，直接丢给 Go 程序，让 Go 自己处理
        proxy_pass http://127.0.0.1:8888;
        proxy_set_header Host $host;
    }

    # =============================
    # 默认情况: 其他请求
    # =============================
    location / {
        proxy_pass http://127.0.0.1:8888;
        proxy_set_header Host $host;

        # 直播流避免缓存
        proxy_buffering off;
        proxy_cache off;
    }
}
```