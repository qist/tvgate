# 性能调优（Linux 内核 / CPU）

TVGate 作为流媒体转发代理，高并发场景下需要对 Linux 内核参数做适当调优。以下配置写入 `/etc/sysctl.d/99-tvgate.conf` 后执行 `sysctl -p` 生效。

## 文件描述符

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

systemd 用户在 `TVGate.service` 中配置 `LimitNOFILE=100000`，可根据需要调大。

## 网络缓冲区

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

## 连接与保活

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

## conntrack（连接跟踪）

```ini
# 连接跟踪表大小（高并发必须调大，否则日志出现 nf_conntrack: table full）
net.netfilter.nf_conntrack_max = 1048576
net.netfilter.nf_conntrack_tcp_timeout_established = 3600
net.netfilter.nf_conntrack_tcp_timeout_time_wait = 30
```

## 交换与内存

```ini
# 降低 swap 倾向（流媒体服务优先用物理内存）
vm.swappiness = 1

# 内存过量提交策略（避免 OOM killer 误杀）
vm.overcommit_memory = 1
```

## 网卡队列与多核

```ini
# 网卡接收队列长度
net.core.netdev_max_backlog = 65535

# 开启 RPS/RFS（多核负载均衡，小设备可不配）
net.core.rps_sock_flow_entries = 32768
```

## OpenWrt / 小设备精简版

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

## HTTP 客户端高并发参数（10 万并发参考）

配合 `http` 段调整上游连接池（详见 [SERVER.md](SERVER.md)）：

```yaml
http:
  timeout: 0s                       # 整体请求超时，不限制（由上层逻辑控制超时）
  connect_timeout: 3s               # 建立连接的超时时间（越短越好，失败快速切换）
  keepalive: 30s                    # 长连接保活时间，保证高并发时连接复用
  response_header_timeout: 5s       # 响应头超时，避免服务端卡死
  idle_conn_timeout: 90s            # 空闲连接保留时间，过短会频繁建连，过长会浪费 FD
  tls_handshake_timeout: 5s         # TLS 握手超时，CDN/直播源一般很快
  expect_continue_timeout: 1s       # 基本不用，保持默认

  max_idle_conns: 200000            # 全局最大空闲连接数（10 万并发需要翻倍冗余）
  max_idle_conns_per_host: 10000    # 单 host 的空闲连接上限，保证热点源站可复用
  max_conns_per_host: 20000         # 单 host 总连接数上限（活跃+空闲），防止热点源阻塞

  disable_keepalives: false         # 必须启用长连接，否则 10 万并发会把源站打爆
```

## CPU 性能模式（关闭节能 / 最高性能）

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

# 查看当前驱动
cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_driver

# 通过 intel_pstate 驱动状态切换
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
