# 组播（multicast）

`multicast` 段配置 IPTV 组播（RTP/UDP）接收相关参数。TVGate 在内网网卡上加入运营商组播组，把组播流实时转成 HTTP 对外发布——外网用户直接访问 `http://<IP>:<端口>/udp/239.0.0.1:2000` 即可观看内网 `rtp://239.0.0.1:2000` 的频道，用法类似 udpxy。

该段同时控制 **FCC（Fast Channel Change，快速换台）**：部分运营商 IPTV 平台（`telecom` 电信 / `huawei` 华为两种类型）在换台时先经单播快速补流、再切回组播，开启后可显著缩短换台黑屏时间。FCC 依赖 `fcc_cache_size` 缓存、`fcc_listen_port_min/max` 端口范围接收单播补流，必要时用 `upstream_interface_fcc` 指定专用上游接口。

## 配置段

```yaml
multicast:
  multicast_ifaces: []            # 组播监听网卡列表，留空 [] 表示默认接口，多网卡填 ["eth0","eth1"]
  mcast_rejoin_interval: 60s      # 组播重加组间隔；0/不填 = 不周期重加组，推荐 30s~120s
  fcc_type: telecom               # FCC 类型：telecom（电信平台）/ huawei（华为平台）；不填默认 telecom
  fcc_cache_size: 16384           # FCC 缓存大小（字节），默认 16384
  fcc_listen_port_min: 40000      # FCC 单播补流监听端口范围最小值，默认 40000
  fcc_listen_port_max: 40100      # FCC 单播补流监听端口范围最大值，默认 40100
  upstream_interface: ""          # 默认上游接口：组播/上游流量从此口进出；留空走默认路由
  upstream_interface_fcc: ""      # FCC 专用上游接口：FCC 单播补流走指定网卡；留空跟随 upstream_interface
```

## 字段说明

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `multicast_ifaces` | 字符串列表 | `[]`（默认接口） | 加入组播组使用的网卡列表；留空用系统默认接口，多网卡/多运营商环境填多个口 |
| `mcast_rejoin_interval` | duration | `0`（不重加组） | 周期性重新加入组播组的间隔；仅在配置了具体时长（如 `60s`）时启用 |
| `fcc_type` | string | `telecom` | FCC 快速换台协议类型，支持 `telecom` / `huawei` 两种 |
| `fcc_cache_size` | int | `16384` | FCC 换台补流数据缓存大小（字节） |
| `fcc_listen_port_min` | int | `40000` | FCC 接收单播补流的监听端口范围下限 |
| `fcc_listen_port_max` | int | `40100` | FCC 接收单播补流的监听端口范围上限 |
| `upstream_interface` | string | 空（默认路由） | 默认上游接口，组播加入与上游流量绑定该网卡 |
| `upstream_interface_fcc` | string | 空（跟随上游接口） | FCC 专用上游接口，FCC 单播补流与组播走不同出口时使用 |

## 示例

```yaml
multicast:
  # 双网卡：eth0 接运营商组播，eth1 走默认路由出网
  multicast_ifaces:
    - eth0
  # 无 IGMP 查询器 / IGMP 侦听交换机环境，周期重加组防止组播流中断
  mcast_rejoin_interval: 60s
  # 电信 IPTV 开启 FCC 快速换台
  fcc_type: telecom
  fcc_cache_size: 16384
  fcc_listen_port_min: 40000
  fcc_listen_port_max: 40100
  upstream_interface: eth0
  upstream_interface_fcc: eth1
```

访问方式（假设服务监听 `8888`，组播地址 `239.0.0.1:2000`）：

```text
内网源：  rtp://239.0.0.1:2000
外网访问：http://example.com:8888/udp/239.0.0.1:2000
```

## 注意事项

- **mcast_rejoin_interval 兼容性**：在启用 IGMP Snooping 的交换机、或没有 IGMP 查询器（Querier）的网络里，组播组成员关系可能因无周期查询而老化，导致流中断。设置 `30s ~ 120s` 的重加组间隔可保持成员有效；普通家庭路由器环境通常不需要（保持 `0` 即可）。间隔过短会增加组播协议报文开销，不建议低于 30s。
- **FCC 适用运营商**：FCC 并非所有地区/运营商都提供。`fcc_type: telecom` 适配电信系 IPTV 平台，`huawei` 适配华为系 IPTV 平台；未提供 FCC 服务的源保持普通组播转发即可，不影响观看，只是换台稍慢。
- **防火墙放行**：开启 FCC 后需放行 `fcc_listen_port_min ~ fcc_listen_port_max` 的 UDP 端口（默认 `40000-40100`），并放行组播地址（如 `239.0.0.0/8`）的入站 UDP。
- **Docker 部署**：组播收流需 `--net=host`，桥接网络无法正确接收组播。
- **配置位置**：`multicast_ifaces`、`mcast_rejoin_interval` 等均写在顶层 `multicast:` 段下（旧文档示例写在 `server:` 段的写法已过时，当前版本以 `multicast` 段为准）。
- **热加载**：修改组播配置后由配置热加载应用到各流 Hub，无需重启；FCC 类型/缓存大小变更会平滑更新到已建立的流。
