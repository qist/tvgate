# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。
> 本次为服务端功能更新：新增动态代理池、探测 UA 配置，并修复 H5 播放器解析入口误走代理。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.4.6** |
| 发布日期 | 2026-11-21 |
| 发布提交 | `8d6d96a`（功能提交）+ 发版提交，tag `v3.4.6` |
| 距上一版 | v3.4.5（2026-11-17） |
| 平台 | Linux / Windows / macOS / Android 共 33 个平台包 |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512），共 66 个 |
| Docker | `docker.io/juestnow/tvgate:v3.4.6`、`ghcr.io/qist/tvgate:v3.4.6`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.4.6 — 代理组支持动态端口池与探测 UA，修复 H5 解析入口误走代理
```

## 二、GitHub Releases 正文 · 精简版（用户可读）

```
### v3.4.6 更新内容
1. 动态代理池 — proxygroups 新增 pool，按「网关 + 端口段」展开候选端口，后台定时探测、
   排序与自动故障切换；proxies 可省略，也可与静态节点混用；后台可编辑池参数并查看
   探测成功的端口、协议、延迟与状态
2. 探测 UA 可配置 — 组级 probe_ua 用于探测/测速，为空回退 player.ua；修复只认特定 UA
   （如 QTP / okhttp）的源站探测恒 403、节点被误判进冷却的问题
3. 探测逻辑统一 — 池探测与选路共用同一套直播检测（HTTP Range / RTSP），支持单轮重试；
   异步结果判定与主循环一致（206 计为成功），失败时清理过期状态，减少误判
4. H5 播放器修复 — 频道原始入口不再沿用分片代理出口，302 目标改走代理；避免入口不可达
   时把代理节点打成连续失败并进入长时间冷却
5. DNS — 代理组 resolve 支持 remote（默认）/ local，文档见 doc/PROXYGROUPS.md

升级：下载对应平台压缩包解压覆盖后重启；Docker 用户重新拉取镜像；旧配置文件可直接使用。
Android 用户：App（APK）由独立仓库 qist/tvgate-android 按同一版本号发布（内嵌本版本服务端），
覆盖安装即可；直接用二进制则在 Releases 页下载 TVGate-android-*.zip。
```

## 三、完整版（用于发布公告 / 论坛 / 通知，用户可读）

```
## TVGate v3.4.6（2026-11-21）

本次为服务端功能更新，新增动态代理池与探测 UA 配置，并修复 H5 播放器在解析型源上的
代理选择问题。协议入口与既有配置保持兼容，可直接升级。

### 一、动态代理池

- proxygroups 新增可选字段 pool，按「网关 + 端口段」展开候选节点（如 192.0.2.10:20000-20100）
- 后台按 probe_interval 定时探测，按响应时间排序，保留前 top_n 个可用节点
- 失败节点进入 fail_cooldown，下一轮仍会重测，成功后自动恢复
- probe_retries 支持单轮内立即重试，避免动态端口偶发抖动被直接判死
- proxies 可以省略，也可以与静态节点混用（静态节点始终保留在候选前面）
- 后台「代理组」页面可新增/编辑池，并展示探测成功的端口、协议、延迟、HTTP 状态与失败次数

### 二、探测 UA

- 新增组级 probe_ua，仅用于探测/测速：组 probe_ua → 全局 player.ua → 不发送
- 部分直播源会校验 UA（例如只接受 QTP 或 okhttp），探测用错 UA 会一直 403，虽然播放
  仍能成功，但后台状态会显示失败并可能触发冷却
- 播放请求不受该配置影响，仍按频道 ua= / player.ua 透传

### 三、探测与选路

- 池探测复用 lb 的直播检测：HTTP 发 Range 请求，RTSP 走 OPTIONS/DESCRIBE/SETUP/PLAY
- 池探测要求 HTTP 状态码 < 400；lb 选路保持 < 500 视为可连
- 异步消费结果与主循环判定统一，206 分片响应计为成功
- 测速失败时清空 StatusCode / ResponseTime，避免日志出现「上次 206 + 本次失败」

### 四、H5 播放器修复

- 解析型源（php 302）的原始入口不再沿用分片代理出口，交给域名规则或直连
- 302 指向的播放地址仍交给代理组拉取，CDN 属地校验不受影响
- 避免入口地址不可达时反复用入口地址测速，把代理节点打成连续失败并进入长时间冷却

### 五、DNS

- 代理组 resolve 支持 remote（默认，代理端解析）/ local（TVGate 用 dns.servers 解析成 IP）
- 代理服务器自身的域名同样走 dns.servers（socks4/socks4a 由底层库解析）
- 详细说明见 doc/PROXYGROUPS.md

### 兼容性与升级

- 配置文件向后兼容：旧 config.yaml 可直接使用，不配置 pool / probe_ua 时行为与之前一致
- 二进制：下载对应平台压缩包，解压覆盖后重启服务（systemd：systemctl restart tvgate）
- Docker：docker pull juestnow/tvgate:v3.4.6（或 ghcr.io/qist/tvgate:v3.4.6），重建容器
- Android App（APK）：独立仓库 qist/tvgate-android 发布，版本号与服务端对齐，内嵌同版本服务端，
  覆盖安装即可升级
- Android 二进制：Releases 页 TVGate-android-arm64-v8a.zip / TVGate-android-arm32-v7a.zip

### 发布校验

- 每个压缩包附 .dgst 校验文件（MD5/SHA1/SHA256/SHA512），下载后可比对
- 版本核对：./TVGate-linux-64 -version → v3.4.6
- 完整改动清单：doc/CHANGELOG.md
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.4.6` / `ghcr.io/qist/tvgate:v3.4.6` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android 二进制 | Releases 页 `TVGate-android-{arm64-v8a,arm32-v7a}.zip` | 解压覆盖二进制后重启 |
| Android App | 独立仓库 `qist/tvgate-android` 的 `TVGate-v3.4.6-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.4.6` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单

- [ ] 服务端 Release：tag `v3.4.6` 已推送，66 个资产（33 平台包 + 33 `.dgst`）上传完成
- [ ] Docker 镜像：`juestnow/tvgate:v3.4.6` 已推送，`latest` 随 tag 更新
- [ ] Android 二进制：`android-arm64-v8a` / `android-arm32-v7a` 构建成功并上传
- [ ] Android APK：`tvgate-android` 仓库 v3.4.6 已发布（arm64 / arm / x86_64，内嵌同版本服务端）
- [ ] 发布后抽查：Release 二进制（linux-64 包解压）实测 `-version` 输出 v3.4.6
- [ ] GitHub Release 标题与正文已填写（取自「一」「二」精简版）
