# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。
> 本次为功能更新：重做代理池吞吐探测、简化探测节奏，并修复后台代理组页编辑卡顿。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.4.8** |
| 发布日期 | 2026-11-21 |
| 发布提交 | `chore(release): v3.4.8`，tag `v3.4.8` |
| 距上一版 | v3.4.7（2026-11-21，同日补丁） |
| 平台 | Linux / Windows / macOS / Android 共 33 个平台包 |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512），共 66 个 |
| Docker | `docker.io/juestnow/tvgate:v3.4.8`、`ghcr.io/qist/tvgate:v3.4.8`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.4.8 — 代理池吞吐探测重做：按完整分片后段速率选路，解决「测速很高但播放一直卡」
```

## 二、GitHub Releases 正文 · 精简版（用户可读）

```
### v3.4.8 更新内容
1. 吞吐探测重做 — 代理池改为读取完整直播分片，吞吐只看最后 3 秒的速率，
   能识别「前 3 秒跑满 7Mbps、随后限速到 2Mbps 甚至直接停住」的出口；
   此前只读 2MB，正好停在突发段，会把实际跑不动的节点测成最快并持续选中
2. 探测超时 — HTTP 探测实际下限提高到 8s（默认 probe_timeout 调整为 10s），
   确保能观察到突发之后的限速/停顿；RTSP 组补充 2s RTP 码率采样
3. 选路更稳 — 无有效样本时保留上一次结果，不再因源站抖动把正常节点判死；
   探测失败一次也会在下一轮自动重试（连续失败 3 次才放弃）
4. 探测节奏简化 — 移除快速检测档；深度探测仅在组内有播放时运行，
   全端口扫描按 probe_full_interval 定时执行（默认 1h），空闲时几乎不产生探测流量
5. 后台代理组页 — 节点表增加「吞吐」「HTTP」列，负载均衡改为下拉；
   修复编辑输入和定时刷新时的卡顿
6. 其他 — 修复 RTSP→HTTP 流结束时的重复 WriteHeader 告警，减少缓存状态日志刷屏

升级：下载对应平台压缩包解压覆盖后重启；Docker 用户重新拉取镜像；旧配置文件可直接使用。
Android 用户：App（APK）由独立仓库 qist/tvgate-android 按同一版本号发布；直接用二进制则下载
TVGate-android-*.zip。
```

## 三、完整版（用于发布公告 / 论坛 / 通知，用户可读）

```
## TVGate v3.4.8（2026-11-21）

本次重点重做代理池的吞吐探测与选路，解决部分出口「测速很快、实际播放一直卡」的问题；
同时简化探测节奏、优化后台代理组页。配置格式向后兼容，可直接覆盖升级。

### 问题现象

- 代理池里某个节点跑测速时能到 6~7Mbps，被选为最快节点
- 实际播放 5MB 左右的直播分片却要十几秒，播放器反复超时断开、画面卡顿
- 手工用同一个代理直接下载分片，平均只有 1~2Mbps

### 原因

- 这类出口采用「单连接前段突发、随后限速」策略：前 3 秒能跑 6~7Mbps，
  4 秒后降到 2~3Mbps，6 秒左右直接停止传输但连接不断开
- 旧探测只读 2MB 正文就结束，采样窗口正好落在突发段内，测出的值远高于持续带宽
- 用这个值排序，自然会把跑不动的节点排到最前面

### 修复

- **读取完整分片**：HTTP 探测最多读取 8MB（或读到分片结束），不再只看前 2MB
- **只统计后段速率**：吞吐按最后 3 秒的读取速率计算，前段突发不计入；
  后段完全没有数据时记 0，节点会被排到最后
- **探测超时下限 8s**：默认 `probe_timeout` 调整为 10s，确保能观察到突发结束之后的表现；
  RTSP 组在握手后继续采样 2s RTP 载荷，也参与吞吐排序
- **区分「跑不动」与「没测到」**：后段确实没数据记 0；分片没读到或采样窗口不足视为
  无样本，保留上一次结果，避免一次源站抖动把正常节点永久判死
- **失败节点自动重试**：深度探测会重测连续失败少于 3 次的候选，成功即恢复；
  不会再出现一次超时被移出列表、要等一小时全端口扫描才回来的情况

### 探测节奏

- **深度探测**：按 `probe_interval`（默认 60s）运行，仅在组内有播放时执行；
  播放中的节点只做 1 字节连通性检查，吞吐沿用上次完整采样，不和播放抢带宽
- **全端口扫描**：按 `probe_full_interval`（默认 1h）重新确认整个端口段；
  无播放时这是唯一会执行的定时任务
- 原 `probe_alive_interval` 快速检测档已移除，减少重复采样与流量

### 后台与体验

- 「代理组」页节点表增加「吞吐」「HTTP」列，负载均衡改为下拉选择
- 池参数增加「全端口扫描间隔」，保存时可正确写入 YAML
- 列表行按 memo 渲染、轮询按界面签名复用对象，修复编辑输入与定时刷新时的卡顿
- 修复 RTSP→HTTP 流结束后重复写响应头的告警；缓存状态日志只在触发测速时打印

### 兼容性与升级

- 配置文件向后兼容：旧 config.yaml 可直接使用；旧配置里的 `probe_alive_interval` 会被忽略
- 建议 HTTP 代理池的 `probe_timeout` 不小于 8s（代码内已有下限，写更小也按 8s 执行）
- 二进制：下载对应平台压缩包，解压覆盖后重启服务（systemd：systemctl restart tvgate）
- Docker：docker pull juestnow/tvgate:v3.4.8（或 ghcr.io/qist/tvgate:v3.4.8），重建容器
- Android App（APK）：独立仓库 qist/tvgate-android 发布，版本号与服务端对齐，内嵌同版本服务端
- Android 二进制：Releases 页 TVGate-android-arm64-v8a.zip / TVGate-android-arm32-v7a.zip

### 发布校验

- 每个压缩包附 .dgst 校验文件（MD5/SHA1/SHA256/SHA512），下载后可比对
- 版本核对：./TVGate-linux-64 -version → v3.4.8
- 完整改动清单：doc/CHANGELOG.md
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.4.8` / `ghcr.io/qist/tvgate:v3.4.8` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android 二进制 | Releases 页 `TVGate-android-{arm64-v8a,arm32-v7a}.zip` | 解压覆盖二进制后重启 |
| Android App | 独立仓库 `qist/tvgate-android` 的 `TVGate-v3.4.8-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.4.8` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单

- [x] 服务端 Release：tag `v3.4.8` 已推送，66 个资产（33 平台包 + 33 `.dgst`）上传完成
- [ ] Docker 镜像：`juestnow/tvgate:v3.4.8` 已推送，`latest` 随 tag 更新
- [x] Android 二进制：`android-arm64-v8a` / `android-arm32-v7a` 构建成功并上传
- [x] Android APK：`tvgate-android` 仓库 v3.4.8 已发布（arm64 / arm / x86_64，内嵌同版本服务端，versionCode 16）
- [x] 发布后抽查：Release 二进制（linux-64 包解压）实测 `-version` 输出 v3.4.8
- [x] GitHub Release 标题与正文已填写（取自「一」「二」精简版）
- [x] 本地归档：`download/TVGate-3.4.8.7z`（22 平台二进制）、`download/tvgate-android-3.4.8.zip`（3 个 APK）
