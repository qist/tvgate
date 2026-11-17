# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。
> 本次为 Android 专属行为调整：Linux / Windows / macOS 的配置与运行行为与 v3.4.4 完全一致。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.4.5** |
| 发布日期 | 2026-11-17 |
| 发布提交 | `f5f087c`（tag `v3.4.5`） |
| 距上一版 | v3.4.4（2026-11-17，同日补丁） |
| 平台 | Linux / Windows / macOS / Android 共 33 个平台包 |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512），共 66 个 |
| Docker | `docker.io/juestnow/tvgate:v3.4.5`、`ghcr.io/qist/tvgate:v3.4.5`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.4.5 — Android 端保持默认后台凭据，修复内嵌 APK 无法登录后台
```

## 二、GitHub Releases 正文 · 精简版（用户可读）

```
### v3.4.5 更新内容
1. Android 运行环境不做后台凭据随机化 — APK 内嵌 .so 或独立 Android 二进制保持默认
   admin / admin 与 /web/，三方 App 没有凭据展示界面也能登录后台；Linux / Windows /
   macOS 行为不变，仍按原规则随机化
2. 兼容已被随机化的 Android 旧安装 — 凭据文件仍在且用户名 / 口令 / 路径均未被改动时，
   启动自动回退默认值并删除凭据文件；任一项被改过则完全不动
3. 安全兜底 — 默认口令下写配置、代码管理、定时任务、升级、备份等高危操作仍被锁定，
   首次登录后请立即在「服务 → Web 设置」修改密码

升级：下载对应平台压缩包解压覆盖后重启；Docker 用户重新拉取镜像；旧配置文件可直接使用。
Android 用户请同步更新 APK（tvgate-android v3.4.7），新内核才会生效。
```

## 三、完整版（用于发布公告 / 论坛 / 通知，用户可读）

```
## TVGate v3.4.5（2026-11-17）

本次为 Android 端专属补丁：只调整 Android 运行环境的后台凭据策略，协议、配置格式与其他
平台行为均无变化，可直接升级。

### 一、背景

TVGate 自 v3.4.4 起对公开默认的后台凭据做随机化（用户名 / 口令 / 路径），桌面与服务器
场景更安全。但 Android 上 TVGate 常以 .so 形式内嵌在三方 APK 中，宿主 App 未必有展示
web-initial-password.txt 的界面，随机化会让用户既不知道账号也拿不到口令，后台等于永久
锁死。

### 二、Android 凭据策略

- GOOS=android（APK 内嵌 .so 或独立二进制）不做随机化，保持默认 admin / admin 与 /web/
- 已被随机化的旧安装：凭据文件仍在且用户名 / 口令 / 路径均未被改动（用户从未登录过）时，
  启动自动回退默认值并删除凭据文件
- 只要任一项被改过，说明用户拿得到当前凭据，完全不动，避免把已知口令改掉
- Linux / Windows / macOS 仍按原规则随机化，不受影响

### 三、安全兜底

- 默认口令下「弱口令锁写」继续生效：写配置、代码管理、定时任务、升级、备份等高危操作
  全部拒绝
- 登录页会提示修改密码；首次登录后请在「服务 → Web 设置」改密解锁
- 提醒：默认口令是公开信息，同一局域网内任何人先登录都能改密接管，请尽快修改

### 兼容性与升级

- 配置文件向后兼容：旧 config.yaml 可直接使用，无需修改
- 二进制：下载对应平台压缩包，解压覆盖后重启服务（systemd：systemctl restart tvgate）
- Docker：docker pull juestnow/tvgate:v3.4.5（或 ghcr.io/qist/tvgate:v3.4.5），重建容器
- Android：需更新 APK（tvgate-android v3.4.7）才会内嵌 v3.4.5 内核，App 内支持在线更新

### 发布校验

- 每个压缩包附 .dgst 校验文件（MD5/SHA1/SHA256/SHA512），下载后可比对
- 版本核对：./TVGate-linux-64 -version → v3.4.5
- 完整改动清单：doc/CHANGELOG.md
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.4.5` / `ghcr.io/qist/tvgate:v3.4.5` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android App | tvgate-android Releases 页 `TVGate-v3.4.7-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.4.5` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单

- [x] 服务端 Release：tag `v3.4.5` 已推送，66 个资产（33 平台包 + 33 `.dgst`）上传完成
- [x] Docker 镜像：`juestnow/tvgate:v3.4.5` 已推送，`latest` 随 tag 更新
- [x] Android Release：`tvgate-android` v3.4.7 已发布，`arm64` / `arm` / `x86_64` APK 齐全（versionCode 12）
- [x] 发布后抽查：Release 二进制（linux-64 包解压）实测 `-version` 输出 v3.4.5；APK 内嵌 `libtvgate.so` 含 v3.4.5 版本串
- [x] GitHub Release 标题与正文已填写（取自「一」「二」精简版）
- [ ] 商店：`doc/STORE-RELEASE-NOTES.md` 文案已在应用市场提交
