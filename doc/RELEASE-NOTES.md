# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.3.7** |
| 发布日期 | 2026-10-02 |
| 发布提交 | `5616a4a` · tag `v3.3.7` |
| 距上一版 | v3.3.6（2026-09-27）以来 10 个提交 |
| 平台 | Linux / Windows / macOS / Android 共 32 个平台包 |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512） |
| Docker | `docker.io/juestnow/tvgate:v3.3.7`、`ghcr.io/qist/tvgate:v3.3.7`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.3.7 — phpgo 解释器系列修复（参数解析/GCM加密/字符串语义/PATH_INFO路由）；播放页状态栏沉浸修复
```

## 二、GitHub Releases 正文 · 精简版（用户可读）

```
### v3.3.7 更新内容
1. 内嵌 PHP（phpgo）解释器多处修复 — 修复部分 PHP 直播源脚本在本机直接执行时报错：
   「函数参数须为变量」「openssl_encrypt 后 GCM 解密失败」「字符串内容被误改」以及
   「/PHP脚本.php/子路径」路由 403 等问题；常见源脚本（4gtv.php/ysptp.php 等）
   现在可正常请求与执行
2. 安卓播放页状态栏沉浸修复 — 弹窗/切后台后状态栏常驻不再自动收回，导致播放页布局
   被顶偏；现改为瞬态行为 + 焦点恢复时主动压回，布局不再被推偏
3. /debug/pprof 端点改为编译标签控制 — 常规构建（Docker / 二进制）不再包含 pprof，
   彻底消除内存/性能 profile 泄露面；调试时加 `-tags pprof` 编译即可恢复

升级：下载对应平台压缩包，解压覆盖后重启服务即可；Docker 用户重新拉取镜像；
安卓盒子/电视直接覆盖安装新版 APK（App 内也支持在线更新）。旧配置文件可直接使用，无需修改。
```

## 三、完整版（用于发布公告 / 论坛 / 通知，用户可读）

```
## TVGate v3.3.7（2026-10-02）

本次主要修复内嵌 PHP 解释器导致部分直播源脚本本机执行失败的问题，
以及安卓播放页状态栏沉浸行为导致的布局推偏。

### 一、内嵌 PHP（phpgo）解释器修复

此前使用「内嵌执行」方式（本机直接跑 PHP 脚本、不走 HTTP 回环）时，
下列源脚本会直接报错或得到错误输出：

- 函数参数「类型提示 + 引用」解析失败（如 array &$id）：报错「函数参数须为变量」
- 字符串下标赋值语义错误：$s[$i] = 'x' 字节修改被误作数组操作，
  整串数据损坏成字面量 "Array"（加密 nonce 等字段直接不可用）
- openssl_encrypt GCM 模式未写回 $tag：认证标签丢失，
  服务端回传 403 或解密失败
- PATH_INFO 路由（/ysptp.php/cctv1.m3u8）返回 403：此前找不到文件直接拒绝，
  现前缀探测脚本文件 + 剩余路径作 PATH_INFO 传入

以上修复覆盖 4gtv.php / ysptp.php 等常见 PHP 直播源脚本，
本类源现可请求即回 M3U8，与外网 PHP 环境执行结果一致。

### 二、安卓播放页状态栏沉浸修复（随新版 APK 发布）

- 现象：安卓播放页弹窗（权限/更新）或切到后台后再回来，状态栏常驻不消失，
  H5 顶部安全区随之持续存在，把播放页布局（含底部控制条）反复顶偏
- 原因：进入沉浸模式时未声明系统栏行为；状态栏被带出后无法自动收回
- 修复：Android 11+（API 30+）声明 BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
  （边缘下滑系统栏只短暂悬浮、几秒后自动收回）；同时补回
  onWindowFocusChanged 重新进入沉浸，焦点恢复时主动把状态栏压回

### 三、/debug/pprof 安全收敛

- /debug/pprof/ 端点此前注册在所有端口（含公网），可无鉴权访问进程堆、
  goroutine 栈、CPU profile 等，存在信息泄露风险
- 现改为编译期控制：常规 `go build` 完全不编译 pprof 相关代码进 binary
  （符号表中 0 个 pprof 符号），发行版无任何暴露面
- 需要性能诊断时用 `go build -tags pprof` 编译即可恢复原有能力

### 兼容性与升级
- 配置文件向后兼容：旧 config.yaml 可直接使用，无需改动
- 二进制：下载对应平台压缩包，解压覆盖后重启服务（systemd：systemctl restart tvgate）
- Docker：docker pull juestnow/tvgate:v3.3.7（或 ghcr.io/qist/tvgate:v3.3.7），重建容器
- Android：安装 v3.3.7 版 APK 覆盖即可（App 内也支持在线更新）

### 发布校验
- 每个压缩包附 .dgst 校验文件（MD5/SHA1/SHA256/SHA512），下载后可比对
- 版本核对：./TVGate-linux-64 -version → v3.3.7
- 完整改动清单：doc/CHANGELOG.md（含 Android 段落）
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.3.7` / `ghcr.io/qist/tvgate:v3.3.7` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android App | Releases 页 `TVGate-v3.3.7-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.3.7` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单

- [ ] 服务端 Release：tag `v3.3.7` 已推送（指向 `5616a4a`），64 个资产（32 平台包 + 32 `.dgst`）上传完成
- [ ] Docker 镜像：`juestnow/tvgate:v3.3.7` 与 `ghcr.io/qist/tvgate:v3.3.7` 推送完成且 `latest` 已更新
- [ ] Android Release：`releases/latest` 为 v3.3.7，`arm64` / `arm` / `x86_64` 三个 APK 齐全
- [ ] 发布后抽查：Release 二进制（linux-64 包解压）实测 `-version` 输出 v3.3.7
- [ ] 发布后抽查：发行版 binary 用 `go tool nm` 确认 0 个 pprof 符号
- [ ] GitHub Release 正文已填写（取自「二、精简版」）
- [ ] 设备抽查：安卓播放页弹窗/切后台后状态栏浸入正常；PHP 直播源脚本可本机执行
- [ ] 商店：`doc/STORE-RELEASE-NOTES.md` 文案已在应用市场提交
