# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.4.3** |
| 发布日期 | 2026-11-14 |
| 发布提交 | 见 tag `v3.4.3` |
| 距上一版 | v3.4.2（2026-10-31）以来 19 个提交 |
| 平台 | Linux / Windows / macOS / Android 共 33 个平台包（新增 Android ARM32 v7） |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512），共 66 个 |
| Docker | `docker.io/juestnow/tvgate:v3.4.3`、`ghcr.io/qist/tvgate:v3.4.3`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.4.3 — 新增 Android ARM32 v7 服务端包、Android 构建独立脚本化、后台文档查找高亮修复
```

## 二、GitHub Releases 正文 · 精简版（用户可读）

```
### v3.4.3 更新内容
1. 新增 Android ARM32 v7（armeabi-v7a）服务端包 — 仍在服役的 32 位安卓盒子/电视可直接原生
   运行，下载 TVGate-android-arm32-v7a.zip 即可，与 64 位包同源同版本
2. Android 构建独立化 — 新增 scripts/build-android.sh，走 NDK clang + CGO 外部链接生成 PIE；
   make android / make android-arm32-v7a / make android-arm64-v8a 直接转发。Android 不再参与
   make all，没装 NDK 的机器也能正常编译其他平台
3. 后台「编辑文档」查找高亮修复 — 打开查找面板后全部匹配实时高亮，当前项用更醒目的颜色区分，
   编辑与滚动时高亮同步跟随，不再出现"搜得到却看不见"
4. 文档补齐 — doc/MULTICAST.md 新增「播放地址中启用 FCC」：支持 fcc=<IP>:<端口>、
   operator=telecom|huawei、fcc=false，可按频道单独开关
5. 依赖更新 — quic-go 0.65.0、golang.org/x/net 0.62.0、golang.org/x/text 0.44.0、brotli 1.3.0 等

升级：下载对应平台压缩包，解压覆盖后重启服务即可；Docker 用户重新拉取镜像；
安卓盒子/电视直接覆盖安装新版 APK（App 内也支持在线更新）。旧配置文件可直接使用，无需修改。
```

## 三、完整版（用于发布公告 / 论坛 / 通知，用户可读）

```
## TVGate v3.4.3（2026-11-14）

本次为构建与体验版本：新增 Android 32 位 ARM 服务端包，把 Android 交叉编译从主 Makefile
独立出来，并修复后台编辑器里查找结果不高亮的问题。协议与服务行为无破坏性变更，可直接升级。

### 一、新增 Android ARM32 v7（armeabi-v7a）服务端包

- 发布产物新增 TVGate-android-arm32-v7a.zip，面向仍在服役的 32 位安卓盒子 / 电视
- 与 arm64 包完全同源（同一提交、同一版本号），差异仅在 ABI
- 产物为 PIE 可执行文件，最低 API 21（Android 5.0），解释器 /system/bin/linker
- 说明：android/arm 必须外部链接（CGO）才能生成 PIE，因此它与 Linux/Windows 的
  静态链接产物构建方式不同，走 NDK clang 单独构建

### 二、Android 构建独立脚本化

- 新增 scripts/build-android.sh，支持 arm64-v8a / arm32-v7a / all 三种参数
- 脚本内部固定 CGO_ENABLED=1 并调用 NDK clang 生成 PIE，不再套用主 Makefile 的
  CGO_ENABLED=0 + -extldflags '-static' 通用规则（此前会直接报
  "android/arm requires external (cgo) linking, but cgo is not enabled"）
- NDK 探测顺序：ANDROID_NDK_HOME → ANDROID_NDK_ROOT → ANDROID_HOME → ANDROID_SDK_ROOT
  → ~/Android/Sdk/ndk/* → /opt/android-sdk/ndk/*；全部缺失时给出安装示例
- make android / make android-arm64-v8a / make android-arm32-v7a 仅作转发；Android 已从
  make all 移除，无 NDK 环境下 make all 不会再整体失败
- CI：release.yml 的 android-* 矩阵统一安装 NDK r25c，由脚本完成交叉编译后按平台名打包

### 三、后台「编辑文档内容」查找高亮修复

- 现象：在管理后台编辑文档时，查找面板能定位光标、显示匹配计数，但正文里看不到任何高亮
- 原因：编辑器使用原生 textarea，无法在文本内部渲染高亮区间
- 修复：新增与 textarea 完全同字体 / 内边距 / 行高 / 换行模式的镜像层，匹配区间以 <mark>
  渲染在 textarea 下方（textarea 背景透明），全部匹配实时高亮，当前项用琥珀色与其余
  紫色区分；textarea 滚动时镜像层同步滚动，编辑后高亮随内容即时刷新
- 查找遮罩背景由 bg-black/30 降为 bg-black/10，减少对正文的遮挡

### 四、文档补齐：播放地址内联 FCC

- doc/MULTICAST.md 新增「播放地址中启用 FCC」一节：
  - fcc=<IP>:<端口> 按频道指定 FCC 单播服务器
  - operator=telecom|huawei 覆盖全局 fcc_type
  - fcc=true/1 仅启用开关；fcc=false/0 对该频道显式关闭
- 参数只影响当次请求所属频道，未带参频道继续沿用 multicast 段配置

### 五、依赖更新

- github.com/quic-go/quic-go 0.64.1 → 0.65.0
- golang.org/x/net 0.61.0 → 0.62.0、golang.org/x/text 0.43.0 → 0.44.0
- golang.org/x/sync 0.24.0 → 0.25.0、golang.org/x/sys 0.49.0 → 0.50.0
- github.com/andybalholm/brotli 1.2.7 → 1.3.0
- github.com/asticode/go-astits 1.16.0 → 1.17.0

### 兼容性与升级

- 配置文件向后兼容：旧 config.yaml 可直接使用，无需改动
- 除 Web 管理后台编辑器查找高亮外，服务端行为与 v3.4.2 一致
- 二进制：下载对应平台压缩包，解压覆盖后重启服务（systemd：systemctl restart tvgate）
- Docker：docker pull juestnow/tvgate:v3.4.3（或 ghcr.io/qist/tvgate:v3.4.3），重建容器
- Android：安装 v3.4.3 版 APK 覆盖即可（App 内也支持在线更新）

### 发布校验

- 每个压缩包附 .dgst 校验文件（MD5/SHA1/SHA256/SHA512），下载后可比对
- 版本核对：./TVGate-linux-64 -version → v3.4.3
- 完整改动清单：doc/CHANGELOG.md（含 Android 段落）
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.4.3` / `ghcr.io/qist/tvgate:v3.4.3` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android App | Releases 页 `TVGate-v3.4.3-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.4.3` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单

- [ ] 服务端 Release：tag `v3.4.3` 已推送，66 个资产（33 平台包 + 33 `.dgst`）上传完成
- [ ] Android ARM32 v7 资产存在：`TVGate-android-arm32-v7a.zip` 与 `.dgst`
- [ ] Docker 镜像：`juestnow/tvgate:v3.4.3` 已推送（Docker Hub 已可见），`latest` 随 tag 更新
- [ ] Android Release：`releases/latest` 为 v3.4.3，`arm64` / `arm` / `x86_64` 三个 APK 齐全
- [ ] 发布后抽查：Release 二进制（linux-64 包解压）实测 `-version` 输出 v3.4.3
- [ ] 发布后抽查：Android 包 `file` 显示 PIE + `for Android 21`
- [ ] GitHub Release 正文已填写（取自「二、精简版」）
- [ ] 商店：`doc/STORE-RELEASE-NOTES.md` 文案已在应用市场提交
