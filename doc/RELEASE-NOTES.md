# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.2.2** |
| 发布日期 | 2026-09-16 |
| 发布提交 | `6e3eca5`（CHANGELOG 定稿）· tag `v3.2.2` |
| 距上一版 | v3.2.1（2026-09-08）以来 39 个提交 |
| 平台 | Linux / Windows / macOS / Android 共 30+ 目标（新旧两套命名并存） |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512） |
| Docker | `docker.io/juestnow/tvgate:v3.2.2`、`ghcr.io/qist/tvgate:v3.2.2`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.2.2 — 网络切换与断流自愈；修复播放中声音卡顿；音频软解统一逐帧 PTS
```

## 二、GitHub Releases 正文 · 精简版

```
### v3.2.2 更新内容
1. 网络切换/断流自愈 — WiFi ↔ 4G/5G、断网恢复后自动重连（含无数据检测），不再出现永久转圈、无声或音画错位
2. 播放中"声音卡顿一下"修复 — 音频时间轴的重钉改为有界判定，小幅抖动交给 bridging 吸收
3. 音画同步优化 — 音频软解统一为逐帧源 PTS 直标（MP2/AC-3/E-AC-3 三路径一致），长时间播放更稳
4. 直播体验 — 缓冲领先上限 10s→30s（抗分片稀疏/抖动）；播放停摆与音频链静音自动恢复
5. EPG 增强 — 频道名归一化匹配、支持 XMLTV xml.gz、主源失效自动回退
6. 其它 — seek 不再清空音频队列、软解音频声道模式（立体声/单声道合成）、新增丢弃增量诊断日志

完整改动见 doc/CHANGELOG.md（26 项）。升级：下载对应平台 zip 覆盖二进制后重启服务。
```

## 三、完整版（用于发布公告 / 论坛 / 通知）

```
## v3.2.2（2026-09-16）

相对 v3.2.1 共 39 个提交，服务端条目 26 项，重点如下。

### 一、网络异常自愈（本版最大改动）
- 直播流传输级自动重连：中途断流按指数退避重连（0.5→8s、上限 5 次），重连后自动衔接
  时间轴，画面连续、音画不脱轴，无需重建会话
- 无数据看门狗：网络切换时旧连接常静默挂死（系统级超时可达 30s+），20s 无数据即主动
  重建连接；VOD/回看不做字节流重连（避免内容重复），按位置重建
- 效果：WiFi ↔ 4G/5G 切换、隧道/电梯等真实断网后自动恢复，不再"永久错位/无声/转圈"

### 二、音画同步与音频质量
- 修复播放中"声音卡顿一下"：音频轨不连续时的 PCM 重钉改为有界判定（小幅抖动由
  bridging 吸收，只有真脱轴才重钉），杜绝每次抖动都丢一段音频
- 音频软解统一为「demuxer 逐帧切分 + 逐帧 PTS 外推」，MP2/AC-3/E-AC-3 三条路径
  在 demuxer → WASM → PCM 时间轴上行为一致；坏帧只跳该帧并保持 PTS 连续
- 修复源中断恢复后的音频永久静音；seek 后不再清空音频队列（从缓冲重锚）
- 播放停摆兜底 + 音频链静音看门狗：异常时自动按当前位置恢复，保证"永不永久卡死"
- 新增软解音频丢弃增量诊断日志（定位丢帧/卡顿环节）

### 三、直播与节目单（EPG）
- 缓冲领先上限 10s → 30s：分片稀疏/抖动（整点 404/502 后每 10s 一片）场景卡顿显著减少
- EPG 频道名归一化模糊匹配：自动处理"北京卫视4K" vs "北京卫视"等质量后缀差异
- 支持 XMLTV xml.gz（gzip 自动识别）；EPG 主源失效自动回退到备用源

### 四、其它修复与优化
- 后台播放音频 free-run 重构；回前台"视频追音频"对齐（消除切前台静音窗口）
- 软解音频新增声道模式：立体声 / 单声道合成（分离声道源手机端两边都听得到）
- 恢复软解源静音 AAC 假音轨，根治后台音画不同步
- 首次打开播放页点台无反应、返回键、主题预置等一批交互问题修复
- phpgo：curl 手动 Accept-Encoding 解压（含 br）、字符串位运算语义、socket 超时防挂起
- 依赖更新（x/crypto、x/sync、gortsplib、brotli 等）

### 兼容性与升级
- 配置文件向后兼容：本版无破坏性配置变更，旧 config.yaml 可直接使用；EPG/订阅新增能力
  均为可选字段
- 二进制升级：下载对应平台 zip，覆盖原二进制后重启服务（systemd：`systemctl restart tvgate`）
- Docker 升级：`docker pull juestnow/tvgate:v3.2.2`（或 ghcr.io/qist/tvgate:v3.2.2）
- Android 端：配套 App v3.2.2（内嵌同版本服务端），App 内"在线更新"可直接升级

### 发布校验
- 每个 zip 均附 `.dgst`（MD5/SHA1/SHA256/SHA512），下载后可比对
- 二进制内嵌版本号：`./TVGate-linux-64 -version` → v3.2.2
- 完整 CHANGELOG（含 Android 段落）：doc/CHANGELOG.md
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.2.2` / `ghcr.io/qist/tvgate:v3.2.2` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android App | Releases 页 `TVGate-v3.2.2-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.2.2` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单

- [ ] 服务端 Release：tag `v3.2.2` 已推送，30+ 平台二进制 + `.dgst` 全部上传完成
- [ ] Docker 镜像：`juestnow/tvgate:v3.2.2` 与 `ghcr.io/qist/tvgate:v3.2.2` 推送完成且 `latest` 已更新
- [ ] Android Release：`releases/latest` 为 v3.2.2 且 3 个 APK 齐全
- [ ] 商店：`doc/STORE-RELEASE-NOTES.md` 文案已在应用市场提交
- [ ] 发布后抽查：任一平台二进制 `-version` 输出 v3.2.2；服务能正常启动并加载 config
