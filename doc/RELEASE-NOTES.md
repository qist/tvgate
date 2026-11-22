# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。
> 本次为修复 + 体验更新：后台列表删空可保存、H5 播放页更流畅、删除文件需确认、依赖安全升级。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.4.10** |
| 发布日期 | 2026-11-22 |
| 发布提交 | `chore(release): v3.4.10`，tag `v3.4.10` |
| 距上一版 | v3.4.9（2026-11-21，代理池空闲测速修复） |
| 平台 | Linux / Windows / macOS / Android 共 33 个平台包 |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512），共 66 个 |
| Docker | `docker.io/juestnow/tvgate:v3.4.10`、`ghcr.io/qist/tvgate:v3.4.10`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.4.10 — 后台列表删空可保存，H5 播放页更流畅，删除文件需确认
```

## 二、GitHub Releases 正文 · 精简版（用户可读）

```
### v3.4.10 更新内容
1. 后台列表删空可保存 — 定时任务、代理组、仓库同步删掉最后一个条目后，保存按钮不再消失、
   也不会被「至少填写一个」拦住；有未保存改动时暂停轮询，删掉的内容不会被自动拉回来
2. 删除文件需确认 — 代码文件管理改用应用内确认弹窗，不再依赖部分 WebView 会静默放行/忽略的
   window.confirm；目录会明确提示连同全部内容一起删除
3. H5 播放页更流畅 — 播放器不再随播放时钟每秒重渲染，节目单改为分钟级刷新，频道列表只计算
   可见频道的当前节目；手机端节目单滚动去掉逐帧模糊，滚动更跟手
4. 依赖安全升级 — zod 升级到 4.6.7（配套 @hookform/resolvers 7）、vitest 5.1.1、
   postcss-selector-parser 6.1.5，移除未使用的 fast-xml-parser；npm audit 归零
5. 兼容性不变 — 配置格式与数据目录无变化，旧 config.yaml 直接可用

升级：下载对应平台压缩包解压覆盖后重启；Docker 用户重新拉取镜像；旧配置文件可直接使用。
Android 用户：App（APK）由独立仓库 qist/tvgate-android 按同一版本号发布；直接用二进制则下载
TVGate-android-*.zip。
```

## 三、完整版（用于发布公告 / 论坛 / 通知，用户可读）

```
## TVGate v3.4.10（2026-11-22）

本次修复后台三类列表「删空后存不下去 / 删了又回来」的问题，改善 H5 播放页的渲染与滚动
流畅性，并把删除文件改为必须点确认；同时完成一轮依赖安全升级。配置格式向后兼容，可直接
覆盖升级。

### 修复一：后台列表删空后无法保存

问题：
- 定时任务：删掉最后一个任务后列表为空，底部「保存全部配置」按钮整个消失，删除无法提交
- 仓库同步：保存时校验「请至少填写一个仓库标识」，删掉最后一个仓库后保存直接被拦住
- 定时任务 / 代理组：5 秒 / 15 秒轮询只在「编辑中」暂停，删除不算编辑，刚删的条目
  几秒后又被后端配置拉回来

修复：
- 删除后仍可保存空列表（tasks: []、sync: []、无代理组），后端按空列表停止对应任务/同步
- 新增「有未保存改动」标记：改动期间暂停轮询覆盖本地列表，保存成功或重新加载后复位
- 列表为空时给出「暂无定时任务」提示，不再只剩一片空白

### 修复二：删除文件需要确认

问题：
- 代码文件管理的删除用的是浏览器 window.confirm，部分 App WebView 会静默放行或忽略，
  点了删除就直接落盘

修复：
- 改为应用内确认弹窗，只有点「删除」才真正调用后端；「取消」与点遮罩都不执行
- 文件与目录分文案，目录明确提示「及其全部内容」，与后端 RemoveAll 行为一致

### 改进：H5 播放页流畅性

- 播放器壳加 React.memo：播放时钟每秒更新不再连带重渲染双槽 video/canvas、门户与控制条，
  时间读数仍由 Context 直达叶子组件
- 节目单时钟改为分钟进位才提交：节目起止本就是分钟级数据，重算整张列表的频率降低约 60 倍
- 频道列表的「当前节目」映射只遍历可见/筛选后的频道，EPG 分片预取到达时不再全量扫描订阅
- 浮层滚动容器加 overscroll-behavior: contain 与旧 iOS 惯性兜底；手机端关闭节目单 sticky
  日期头的 backdrop-blur，避免滚动时逐帧重算背后内容

### 安全：依赖升级

- zod 3 → 4.6.7（配套 @hookform/resolvers 7.0.8），消除内存耗尽类校验漏洞
- vitest 5.0.0 → 5.1.1，修复测试工具链的 mock 注入 / 路径穿越问题
- postcss-selector-parser 通过 overrides 固定到 6.1.5，修复构建期选择器解析复杂度问题
- 移除已无任何引用的 fast-xml-parser
- 结果：npm audit 0 漏洞（原 1 高 / 3 中 / 1 低）

### 兼容性与升级

- 配置文件向后兼容：旧 config.yaml 可直接使用，无新增必填字段
- 二进制：下载对应平台压缩包，解压覆盖后重启服务（systemd：systemctl restart tvgate）
- Docker：docker pull juestnow/tvgate:v3.4.10（或 ghcr.io/qist/tvgate:v3.4.10），重建容器
- Android App（APK）：独立仓库 qist/tvgate-android 发布，版本号与服务端对齐，内嵌同版本服务端
- Android 二进制：Releases 页 TVGate-android-arm64-v8a.zip / TVGate-android-arm32-v7a.zip

### 发布校验

- 每个压缩包附 .dgst 校验文件（MD5/SHA1/SHA256/SHA512），下载后可比对
- 版本核对：./TVGate-linux-64 -version → v3.4.10
- 完整改动清单：doc/CHANGELOG.md
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.4.10` / `ghcr.io/qist/tvgate:v3.4.10` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android 二进制 | Releases 页 `TVGate-android-{arm64-v8a,arm32-v7a}.zip` | 解压覆盖二进制后重启 |
| Android App | 独立仓库 `qist/tvgate-android` 的 `TVGate-v3.4.10-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.4.10` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单（v3.4.10 已完成项）

- [x] 服务端 Release：tag `v3.4.10` 已推送，33 平台包 + 33 `.dgst` + 33 `.sig` 共 99 个资产上传完成
- [x] Docker 镜像：`juestnow/tvgate:v3.4.10` 已推送，`latest` 随 tag 更新
- [x] Android 二进制：`android-arm64-v8a` / `android-arm32-v7a` 构建成功并上传
- [x] Android APK：`tvgate-android` 仓库 v3.4.10 已发布（arm64 / arm / x86_64，内嵌同版本服务端，versionCode 18）
- [x] 发布后抽查：Release 二进制（linux-64 包解压）实测 `-version` 输出 v3.4.10；
      `.dgst` 中 SHA2-256 与本地 `sha256sum` 一致
- [x] GitHub Release 标题与正文已填写（取自「一」「二」精简版）
- [ ] 本地归档：`download/TVGate-3.4.10.7z`、`download/tvgate-android-3.4.10.zip`（可选，未归档）
