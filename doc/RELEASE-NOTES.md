# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。
> 本次为 v3.4.6 的补丁版：只修复代理组状态显示，功能与配置行为与 v3.4.6 一致。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.4.7** |
| 发布日期 | 2026-11-21 |
| 发布提交 | `0bdb408`（修复提交）+ 发版提交，tag `v3.4.7` |
| 距上一版 | v3.4.6（2026-11-21，同日补丁） |
| 平台 | Linux / Windows / macOS / Android 共 33 个平台包 |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512），共 66 个 |
| Docker | `docker.io/juestnow/tvgate:v3.4.7`、`ghcr.io/qist/tvgate:v3.4.7`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.4.7 — 修复代理组未测速节点误显示「在线」，统一显示「未测」
```

## 二、GitHub Releases 正文 · 精简版（用户可读）

```
### v3.4.7 更新内容
1. 代理组状态修复 — 节点刚加载或测速尚未返回时，后台只拿到开始时间，之前会误显示
   「在线」而延迟为「—」；现在统一显示「未测」
2. 判定更严格 — 成功看延迟/状态码，失败看失败次数，只有开始时间时按未测处理
3. 静态节点与动态池节点使用同一套判定
4. 其余功能、配置与 v3.4.6 完全一致，可直接覆盖升级

升级：下载对应平台压缩包解压覆盖后重启；Docker 用户重新拉取镜像；旧配置文件可直接使用。
Android 用户：App（APK）由独立仓库 qist/tvgate-android 按同一版本号发布；直接用二进制则下载
TVGate-android-*.zip。
```

## 三、完整版（用于发布公告 / 论坛 / 通知，用户可读）

```
## TVGate v3.4.7（2026-11-21）

本次为 v3.4.6 的补丁版，只调整后台「代理组」页面的状态显示，功能、配置格式与运行行为
与 v3.4.6 完全一致，可直接覆盖升级。

### 问题现象

- 更新或重启后，代理组里的节点全部显示绿色「在线」，但延迟是「—」、连续失败为 0
- 实际这一轮测速还没返回，无法判断成功与否，后台不应该给出「在线」结论

### 原因

- 测速开始时会先写入 LastCheck，用于避免并发重复测速
- 请求成功标记也会把 Alive 置为 true，但不写延迟和状态码
- 旧前端只判断 LastCheck 是否非空，于是把「测速中」显示成了「在线」
- 从未测速的零值时间还会被序列化成 0001-01-01T00:00:00Z，进一步被当成有效时间

### 修复

- 前端改为「有测速结果才算已测」：延迟、状态码、失败次数任一非 0 才显示在线/离线
- 只有开始时间、三项均为 0 时显示「未测」
- 后端对 LastCheck / LastUsed / CooldownUntil 的零值统一输出 null
- 静态节点与动态池节点、顶部统计徽章使用同一套判定

### 兼容性与升级

- 配置文件向后兼容：旧 config.yaml 可直接使用
- 二进制：下载对应平台压缩包，解压覆盖后重启服务（systemd：systemctl restart tvgate）
- Docker：docker pull juestnow/tvgate:v3.4.7（或 ghcr.io/qist/tvgate:v3.4.7），重建容器
- Android App（APK）：独立仓库 qist/tvgate-android 发布，版本号与服务端对齐，内嵌同版本服务端
- Android 二进制：Releases 页 TVGate-android-arm64-v8a.zip / TVGate-android-arm32-v7a.zip

### 发布校验

- 每个压缩包附 .dgst 校验文件（MD5/SHA1/SHA256/SHA512），下载后可比对
- 版本核对：./TVGate-linux-64 -version → v3.4.7
- 完整改动清单：doc/CHANGELOG.md
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.4.7` / `ghcr.io/qist/tvgate:v3.4.7` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android 二进制 | Releases 页 `TVGate-android-{arm64-v8a,arm32-v7a}.zip` | 解压覆盖二进制后重启 |
| Android App | 独立仓库 `qist/tvgate-android` 的 `TVGate-v3.4.7-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.4.7` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单

- [ ] 服务端 Release：tag `v3.4.7` 已推送，66 个资产（33 平台包 + 33 `.dgst`）上传完成
- [ ] Docker 镜像：`juestnow/tvgate:v3.4.7` 已推送，`latest` 随 tag 更新
- [ ] Android 二进制：`android-arm64-v8a` / `android-arm32-v7a` 构建成功并上传
- [ ] Android APK：`tvgate-android` 仓库 v3.4.7 已发布（arm64 / arm / x86_64，内嵌同版本服务端）
- [ ] 发布后抽查：Release 二进制（linux-64 包解压）实测 `-version` 输出 v3.4.7
- [ ] GitHub Release 标题与正文已填写（取自「一」「二」精简版）
