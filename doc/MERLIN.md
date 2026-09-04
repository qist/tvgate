# 梅林固件（Asuswrt-Merlin）部署教程

在梅林固件路由器上以 Entware 服务方式运行 TVGate：单一静态二进制装在 U 盘
（Entware `/opt` 前缀），通过 Entware init 脚本开机自启，配置由 Web 后台管理。

> **适用环境**：梅林 384.x 及以上（RT-AX / GT-AX / RT-BE / GT-BE / ZenWiFi 系列
> aarch64，或 RT-AC86U 等 armv7 老机型）。需要一枚 U 盘 / 移动硬盘（≥1GB，
> 建议 ext4 格式）持续插在路由器上。

---

## 一、准备工作

1. **开启 SSH**：梅林后台「系统管理 → 系统设置 → SSH」设为 `LAN only`，
   端口默认 22，用 `ssh admin@<路由器IP>` 登录。
2. **开启自定义脚本**：同一页面勾选
   `Enable JFFS custom scripts and configs = Yes`，保存并重启路由器。
3. **插入 U 盘**并确认已挂载（`mount | grep mnt`）。

---

## 二、安装 Entware（已装可跳过）

### 方式 A：amtm（推荐）

```sh
amtm        # 进入菜单，选 entware 安装（i → entware），按提示完成
```

### 方式 B：手动安装

```sh
# armv7 老机型（RT-AC86U 等）
entware-setup.sh          # 部分固件内置；没有就用下面手动方式

# 手动：aarch64（RT-AX/BE/GT 新机型）
cd /tmp
curl -LO https://bin.entware.net/aarch64-k3.10/installer/generic.sh
sh generic.sh

# 手动：armv7（RT-AC86U 等）
cd /tmp
curl -LO https://bin.entware.net/armv7sf-k3.2/installer/generic.sh
sh generic.sh
```

安装完成后确认：

```sh
/opt/bin/opkg list-installed | head   # /opt 可用即成功
ls /jffs/scripts/services-start       # 应含 /opt/etc/init.d/rc.unslung start
```

> `services-start` 里没有 `rc.unslung start` 也没关系，安装脚本会自动补上。

---

## 三、安装 TVGate

### 一键脚本（推荐）

在路由器 SSH 中执行：

```sh
cd /tmp
# 官方源（网络通畅时）
curl -sLO https://raw.githubusercontent.com/qist/tvgate/master/doc/scripts/merlin-install.sh
sh merlin-install.sh

# GitHub 不通时走加速前缀（结尾带斜杠），可选指定版本
sh merlin-install.sh https://hk.gh-proxy.com/
sh merlin-install.sh https://hk.gh-proxy.com/ v3.1.0
```

脚本自动完成：架构检测（aarch64 → `linux-arm64-v8a`，armv7 → `linux-arm32-v7a`）→
下载 release → 安装到 `/opt/TVGate/` → 写入 Entware 自启脚本
`/opt/etc/init.d/S99tvgate` → 补齐 `/jffs/scripts/services-start`（开机自启）与
`/jffs/scripts/unmount`（U 盘弹出前安全停止）→ 启动。

`config.yaml` 无需手工创建——首次启动由程序自动生成默认配置，之后统一在
Web 后台修改（支持热重载，改完即生效）。

### 手动安装（不跑脚本时）

```sh
# 1. 下载（aarch64 机型为例，armv7 用 linux-arm32-v7a）
cd /tmp
curl -LO https://github.com/qist/tvgate/releases/latest/download/TVGate-linux-arm64-v8a.zip
unzip TVGate-linux-arm64-v8a.zip
mkdir -p /opt/TVGate
mv TVGate-linux-arm64-v8a /opt/TVGate/tvgate && chmod +x /opt/TVGate/tvgate

# 2. 首次手动启动一次，生成默认配置
cd /opt/TVGate && ./tvgate -config /opt/TVGate/config.yaml   # Ctrl+C 退出
```

然后按下方「服务脚本」手动创建 `/opt/etc/init.d/S99tvgate`（内容见安装脚本
内嵌模板），`chmod +x` 后启动。

---

## 四、首次启动与配置

```sh
/opt/etc/init.d/S99tvgate start
/opt/etc/init.d/S99tvgate status      # 应显示 运行中 (PID xxx)
```

1. 浏览器打开 `http://<路由器IP>:8888/web/` 进入管理后台
   （默认账号密码见自动生成配置的 `web` 段，**登录后立即修改**）。
2. 各模块（播放器 / 代理组 / 推流发布 / 定时任务等）都在后台可视化配置，
   详见 [README 文档索引](../README.md#-详细文档)。
3. **外网访问需放行端口**：梅林后台「WAN → 虚拟服务器 / 端口转发」把 8888
   （及你需要的端口）转发到路由器自身，或直接在外网场景用 Nginx 前置 TLS。

> **目录说明**（都在 U 盘上，重启不丢）：
>
> | 路径 | 内容 |
> |---|---|
> | `/opt/TVGate/tvgate` | 二进制 |
> | `/opt/TVGate/config.yaml` | 配置（Web 后台保存即热重载） |
> | `/opt/TVGate/log/` | 运行日志（程序内自动轮转） |
> | `/opt/etc/init.d/S99tvgate` | 服务脚本 |
> | `/jffs/scripts/services-start` | 开机自启入口 |
> | `/jffs/scripts/unmount` | U 盘弹出前停止服务 |

---

## 五、服务管理

```sh
/opt/etc/init.d/S99tvgate start     # 启动
/opt/etc/init.d/S99tvgate stop      # 停止
/opt/etc/init.d/S99tvgate restart   # 重启
/opt/etc/init.d/S99tvgate status    # 状态

tail -f /opt/TVGate/log/*.log       # 看日志
```

**开机自启链路**：路由器开机 → 梅林执行 `/jffs/scripts/services-start` →
`/opt/etc/init.d/rc.unslung start` → 依序调用 `/opt/etc/init.d/S99tvgate start`。

**U 盘安全弹出**：拔盘 / 卸载时梅林触发 `/jffs/scripts/unmount`，脚本会先
停止 TVGate 再卸载，避免写坏日志与配置。

---

## 六、升级与卸载

```sh
# 升级：重跑安装脚本即可（配置保留）
sh /tmp/merlin-install.sh <加速前缀>            # 或重新下载脚本执行

# 查看版本
/opt/TVGate/tvgate -v 2>/dev/null || grep -m1 version /opt/TVGate/config.yaml

# 卸载
/opt/etc/init.d/S99tvgate stop
rm -rf /opt/TVGate /opt/etc/init.d/S99tvgate
# 并手动移除 /jffs/scripts/services-start、unmount 中相关行
```

---

## 七、常见问题

- **启动失败 / 架构不符**：`uname -m` 必须是 `aarch64` 或 `armv7l/armv8l`。
  下载错了架构会报 `not found` 或 `Exec format error`，重跑脚本会自动选对。
- **GitHub 连不上 / 下载变小文件**：走加速前缀
  `sh merlin-install.sh https://hk.gh-proxy.com/`。
- **解压失败**：`opkg install unzip` 后重试。
- **U 盘休眠导致服务假死**：U 盘节能休眠后进程 IO 会卡住。可在
  「USB 相关应用」关掉 UAS/节能，或用带供电的 U 盘 / 硬盘。
- **重启后没自启**：确认「系统设置」里 JFFS custom scripts = Yes，且
  `services-start` 含 `/opt/etc/init.d/rc.unslung start`。
- **jffs 空间不足**：TVGate 全部文件都在 `/opt`（U 盘），jffs 只放两行脚本，
  无空间压力。
- **时间不准导致证书/令牌异常**：确认 NTP 已同步
  （后台「系统管理 → 基本设置」）。

---

## 相关文档

- [SERVER.md](SERVER.md) — 端口 / TLS / 连接池配置
- [PLAYER.md](PLAYER.md) — H5 播放器订阅直播
- [TUNING.md](TUNING.md) — 高并发内核调优（梅林内置参数已较优，一般无需改）
- [GITHUB.md](GITHUB.md) — GitHub 加速（同步 / 升级共用）
