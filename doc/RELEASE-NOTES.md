# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.3.2** |
| 发布日期 | 2026-09-21 |
| 发布提交 | （见 tag `v3.3.2` 指向）· tag `v3.3.2` |
| 距上一版 | v3.3.1（2026-09-19）以来 15 个提交 |
| 平台 | Linux / Windows / macOS / Android 共 30+ 目标（新旧两套命名并存） |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512） |
| Docker | `docker.io/juestnow/tvgate:v3.3.2`、`ghcr.io/qist/tvgate:v3.3.2`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.3.2 — 部分旧 CDN 频道打不开修复（TLS 握手兼容）；台标失效自动清理；手机端音量面板可触控
```

## 二、GitHub Releases 正文 · 精简版（用户可读）

```
### v3.3.2 更新内容
1. 部分频道打不开修复 — 个别旧式 CDN 仅支持旧版加密握手，新版客户端会被直接拒绝
   （日志显示 remote error: tls: handshake failure）；已恢复兼容，正常站点不受影响
2. 台标死链自动清理 — 订阅刷新时自动检测外部台标图是否有效：失效的直接改用首字徽章，
   列表加载更快、不再整页空等；带防盗链的台标自动保留、不误杀
3. 手机端音量面板修复 — 原生播放时音量滑杆改为向下弹出（与换线路菜单一致），
   不再被播放画面拦截、可正常触控

升级：下载对应平台压缩包，解压覆盖后重启服务即可；Docker 用户重新拉取镜像。旧配置文件可直接使用，无需修改。
```

## 三、完整版（用于发布公告 / 论坛 / 通知，用户可读）

```
## TVGate v3.3.2（2026-09-21）

本次更新集中解决三件事：部分频道打不开、台标失效白等、手机端音量不好调。

### 一、部分频道打不开修复（TLS 握手兼容）
- 个别 CDN 仅支持旧式加密握手（TLS 1.2 + 静态 RSA 密钥交换），新版客户端的握手会被
  对端直接拒绝，表现为频道"一直打不开"，服务端日志报 remote error: tls: handshake failure
- 已启用兼容开关恢复旧式握手能力：正常站点仍优先协商更安全的方式，仅这类旧 CDN
  落到兼容模式；覆盖播放器直连、代理组、php 源、域名映射全部出站 HTTPS 链路

### 二、台标死链自动清理
- 订阅刷新时对补齐的台标地址做后台轻量并发探测（限流 + 时间预算，不拖慢刷新），
  确认失效的直接改用首字徽章显示，频道列表加载更快、不再整页空等图床失败
- 防盗链等"不确定"结果自动保留原样，宁可多留不误杀；探测结论按地址缓存
  （命中 24 小时 / 缺失 2 小时），日常刷新零探测开销
- 本地台标目录响应补缓存头（4 小时 + 条件校验），与外部图床行为一致

### 三、手机端音量面板修复
- 原生播放时音量滑杆改为向下弹出（与换线路菜单方向一致），不再被播放画面拦截，
  可正常触控调节；桌面端浮层模式不变

### 兼容性与升级
- 配置文件向后兼容：旧 config.yaml 可直接使用，无需改动
- 二进制：下载对应平台压缩包，解压覆盖后重启服务（systemd：systemctl restart tvgate）
- Docker：docker pull juestnow/tvgate:v3.3.2（或 ghcr.io/qist/tvgate:v3.3.2），重建容器
- Android：安装 v3.3.2 版 APK 覆盖即可（App 内也支持在线更新）

### 发布校验
- 每个压缩包附 .dgst 校验文件（MD5/SHA1/SHA256/SHA512），下载后可比对
- 版本核对：./TVGate-linux-64 -version → v3.3.2
- 完整改动清单：doc/CHANGELOG.md（含 Android 段落）
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.3.2` / `ghcr.io/qist/tvgate:v3.3.2` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android App | Releases 页 `TVGate-v3.3.2-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.3.2` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单

- [ ] 服务端 Release：tag `v3.3.2` 已推送，30+ 平台二进制 + `.dgst` 全部上传完成
- [ ] Docker 镜像：`juestnow/tvgate:v3.3.2` 与 `ghcr.io/qist/tvgate:v3.3.2` 推送完成且 `latest` 已更新
- [ ] Android Release：`releases/latest` 为 v3.3.2 且 3 个 APK 齐全
- [ ] 商店：`doc/STORE-RELEASE-NOTES.md` 文案已在应用市场提交
- [ ] 发布后抽查：任一平台二进制 `-version` 输出 v3.3.2；服务能正常启动并加载 config
