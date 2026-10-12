# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.4.0** |
| 发布日期 | 2026-10-12 |
| 发布提交 | `57c77e7` · tag `v3.4.0` |
| 距上一版 | v3.3.9（2026-10-05）以来 15 个提交 |
| 平台 | Linux / Windows / macOS / Android 共 32 个平台包 |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512） |
| Docker | `docker.io/juestnow/tvgate:v3.4.0`、`ghcr.io/qist/tvgate:v3.4.0`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.4.0 — MKV 直链解复用、渐进式 MP4（ISO BMFF）自动改走原生后端、本地推流状态与新建推流名称
```

## 二、GitHub Releases 正文 · 精简版（用户可读）

```
### v3.4.0 更新内容
1. MKV 直链支持 — 转封装管线新增 EBML 探测与 MKV 解复用（网盘/直链常见的 4K HEVC +
   E-AC-3），seek 走 Range 重载、上报容器时长，进度条与回看可用
2. 渐进式 MP4（ISO BMFF）不再黑屏 — 转封装管线首块显式识别 ftyp/styp 盒并上报
   FormatUnsupported（此前落入 TS 兜底被静默丢弃，整条流黑屏且无任何报错）；播放器收到后
   自动改用原生 <video> 后端重灌该流，起播前先探首段容器形态，命中 MP4 直接选原生后端
3. 播放失败不再卡死 — 错误面板不再拦截画面点击，并新增「打开频道列表」按钮（桌面端），
   报错时也能随时唤出侧栏换台，不必刷新页面
4. 本地推流状态准确 — 后台「运行状态」对本地转发模式按最近 15s 是否有流数据判定
   「正在推流」，不再停在"等待启动"；新建推流补「推流名称」输入框并支持改名
5. 安全收口 — 备份删除接口强制 POST、需提权、只允许删 .backup. 文件；空口令 Cookie
   fail-closed
6. 升级包下载补自有 UA，去除 Go-http-client/1.1 默认标识，避免部分镜像/代理拒绝

升级：下载对应平台压缩包，解压覆盖后重启服务即可；Docker 用户重新拉取镜像；
安卓盒子/电视直接覆盖安装新版 APK（App 内也支持在线更新）。旧配置文件可直接使用，无需修改。
```

## 三、完整版（用于发布公告 / 论坛 / 通知，用户可读）

```
## TVGate v3.4.0（2026-10-12）

本次重点：新增 MKV 直链播放，修复渐进式 MP4 流「黑屏无报错」并让它自动改走原生解码，
播放失败时不再卡住换台。

### 一、MKV（Matroska）直链支持
- MSE 转封装管线新增 EBML 首块探测与 Matroska 解复用，网盘/直链常见的
  4K HEVC + E-AC-3 内容可直接播放
- seek 走 Range 重载，并上报容器时长（进度条 / 回看可用）

### 二、渐进式 MP4（ISO BMFF）不再黑屏
- 现象：渐进式单体 MP4（ftyp+moov+mdat）此前被"其余一律按 MPEG-TS"兜底交给 TS 解复用，
  首字节是盒子长度而非同步字 0x47，永远等不到同步，整条流被静默丢弃（黑屏且无任何错误）
- 修复：转封装管线首块显式识别 ftyp/styp 盒，判为不支持的容器并上报 FormatUnsupported
- 播放器收到即自动改用原生 <video> 后端重灌该流；起播前先探首段容器形态，
  命中 MP4 直接选原生后端，不再走"报错再切换"

### 三、播放失败不再卡死
- 错误面板不再阻断画面点击（点击面板等同点画面），面板内新增「打开频道列表」按钮（桌面端）
- 效果：任何流报错时都能随时唤出侧栏换台，不必刷新页面

### 四、本地推流状态与新建推流
- 后台「运行状态」对本地转发模式按最近 15 秒是否有流数据判定「正在推流」，
  不再停在"等待启动"（本地模式没有 FFmpeg 推流统计）
- 新建推流补「推流名称」输入框；支持改名，改名/新建都会拦下与既有节点重名覆盖

### 五、安全与其它
- 备份删除接口收口：四个删除路由挂 requireElevated、空口令 Cookie fail-closed、
  强制 POST 且只允许删 .backup. 文件，防止删掉 config.yaml 本体
- 升级包下载补自有 UA，去除 Go-http-client/1.1 默认标识
- 依赖锁文件与 package.json 对齐（tailwindcss 回退 v3.4.x）

### 兼容性与升级
- 配置文件向后兼容：旧 config.yaml 可直接使用，无需改动
- 二进制：下载对应平台压缩包，解压覆盖后重启服务（systemd：systemctl restart tvgate）
- Docker：docker pull juestnow/tvgate:v3.4.0（或 ghcr.io/qist/tvgate:v3.4.0），重建容器
- Android：安装 v3.4.0 版 APK 覆盖即可（App 内也支持在线更新）

### 发布校验
- 每个压缩包附 .dgst 校验文件（MD5/SHA1/SHA256/SHA512），下载后可比对
- 版本核对：./TVGate-linux-64 -version → v3.4.0
- 完整改动清单：doc/CHANGELOG.md（含 Android 段落）
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.4.0` / `ghcr.io/qist/tvgate:v3.4.0` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android App | Releases 页 `TVGate-v3.4.0-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.4.0` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单

- [ ] 服务端 Release：tag `v3.4.0` 已推送（指向 `57c77e7`），64 个资产（32 平台包 + 32 `.dgst`）上传完成
- [ ] Docker 镜像：`juestnow/tvgate:v3.4.0` 已推送（Docker Hub 已可见），`latest` 随 tag 更新
- [ ] Android Release：`releases/latest` 为 v3.4.0，`arm64` / `arm` / `x86_64` 三个 APK 齐全
- [ ] 发布后抽查：Release 二进制（linux-64 包解压）实测 `-version` 输出 v3.4.0
- [ ] GitHub Release 正文已填写（取自「二、精简版」）
- [ ] 设备抽查：MKV 直链可直接播放，进度条 / 回看可用
- [ ] 设备抽查：渐进式 MP4 源自动改走原生后端播放（无需刷新），报错时可从错误面板唤出侧栏换台
- [ ] 商店：`doc/STORE-RELEASE-NOTES.md` 文案已在应用市场提交
