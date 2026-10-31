# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.4.2** |
| 发布日期 | 2026-10-31 |
| 发布提交 | 见 tag `v3.4.2` |
| 距上一版 | v3.4.1（2026-10-18）以来 16 个提交 |
| 平台 | Linux / Windows / macOS / Android 共 32 个平台包 |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512） |
| Docker | `docker.io/juestnow/tvgate:v3.4.2`、`ghcr.io/qist/tvgate:v3.4.2`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.4.2 — 安全修复 CVE-2026-98432：TCP 端口监听默认禁用 MPTCP，一律回落普通 TCP
```

## 二、GitHub Releases 正文 · 精简版（用户可读）

```
### v3.4.2 更新内容
1. 安全修复 CVE-2026-98432 — 新版 Go 标准库把「监听器默认走 MPTCP」打开了：凡是零值
   ListenConfig 创建的 TCP 监听，在支持 MPTCP 的内核上会静默改用 Multipath TCP 协议监听
   端口，与预期「仅普通 TCP」不符，且在无 MPTCP 的内核上完全无法察觉
2. 现已三层收紧：go.mod 全局 godebug multipathtcp=0（覆盖标准库与第三方库的全部监听/
   拨号）、HTTP 监听显式 SetMultipathTCP(false)（优先级高于 GODEBUG 环境变量）、
   热升级（tableflip）重新监听路径同样显式禁用
3. 行为兼容 — 监听仍保留 SO_REUSEPORT 复用语义，无 MPTCP 内核上行为完全不变；
   旧配置文件可直接使用，客户端无需任何改动
4. 新增回归测试 — 即使运行环境强制 GODEBUG=multipathtcp=1，监听也保持普通 TCP

升级：下载对应平台压缩包，解压覆盖后重启服务即可；Docker 用户重新拉取镜像；
安卓盒子/电视直接覆盖安装新版 APK（App 内也支持在线更新）。旧配置文件可直接使用，无需修改。
```

## 三、完整版（用于发布公告 / 论坛 / 通知，用户可读）

```
## TVGate v3.4.2（2026-10-31）

本次为安全版本：修复 CVE-2026-98432 —— TCP 端口监听在新版 Go 工具链下会默认改用
Multipath TCP（MPTCP）协议，现已在所有监听路径上显式禁用，一律回落普通 TCP。
不影响任何功能与配置，建议所有用户升级。

### 一、CVE-2026-98432：端口监听默认禁用 MPTCP（本次核心修复）

背景：Go 标准库从某版本起，将监听器的 MPTCP 默认值改为开启（defaultMPTCPEnabledListen =
true）。凡是使用「零值 net.ListenConfig」创建 TCP 监听的代码——包括大量第三方库——
只要内核支持 MPTCP，端口就会以 IPPROTO_MPTCP 协议监听，而不是普通 TCP。

影响：
- 监听协议与预期不符：安全设备、防火墙、抓包与连接复用等链路对 MPTCP 的处理
  未必与普通 TCP 一致，扩大了排查面与攻击面
- 隐蔽性强：在无 MPTCP 的内核上（多数旧内核、WSL2 等）完全无法复现，
  一旦换到支持 MPTCP 的内核（较新 Linux、OpenWrt 等）行为即发生改变

根因（本项目两处命中）：
- HTTP 监听回退路径原先经 go-reuseport 创建，其内部使用零值 ListenConfig（仅挂
  SO_REUSEPORT Control，未显式声明 MPTCP 偏好）
- 热升级器 tableflip 在未显式传入 ListenConfig 时同样使用零值配置

修复（三层防护，任一层单独即可生效）：
1. go.mod 增加 godebug multipathtcp=0 —— Go 官方的全局默认值机制，覆盖标准库与
   全部第三方库创建的监听与拨号
2. HTTP 监听改为自建 ListenConfig，显式 SetMultipathTCP(false)。该标记为三态，
   优先级高于 GODEBUG=multipathtcp 环境变量，即使运行环境被强制开启也不会回潮；
   Control 保留原 SO_REUSEADDR/SO_REUSEPORT 复用语义，并移除 go-reuseport 依赖
3. 热升级（tableflip）两处 Upgrader 创建点注入 MPTCP 禁用的 ListenConfig，
   覆盖热升级后重新监听的路径

### 二、回归测试

- 新增 server/mptcp_regression_test.go：断言监听配置 MultipathTCP() 恒为 false、
  socket 协议为普通 TCP（SO_PROTOCOL）、SO_REUSEPORT 语义保留
- 在 GODEBUG=multipathtcp=1 强制开启的环境下同样通过，实证零值配置确实默认启用
  MPTCP（漏洞根源）、显式禁用可完全覆盖环境变量

### 兼容性与升级

- 配置文件向后兼容：旧 config.yaml 可直接使用，无需改动
- 对无 MPTCP 的内核，监听行为与 v3.4.1 完全一致（字节级相同的 socket 选项）
- 客户端（浏览器、播放器、拨号侧）无需任何改动
- 二进制：下载对应平台压缩包，解压覆盖后重启服务（systemd：systemctl restart tvgate）
- Docker：docker pull juestnow/tvgate:v3.4.2（或 ghcr.io/qist/tvgate:v3.4.2），重建容器
- Android：安装 v3.4.2 版 APK 覆盖即可（App 内也支持在线更新）

### 发布校验

- 每个压缩包附 .dgst 校验文件（MD5/SHA1/SHA256/SHA512），下载后可比对
- 版本核对：./TVGate-linux-64 -version → v3.4.2
- 完整改动清单：doc/CHANGELOG.md（含 Android 段落）
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.4.2` / `ghcr.io/qist/tvgate:v3.4.2` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android App | Releases 页 `TVGate-v3.4.2-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.4.2` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单

- [ ] 服务端 Release：tag `v3.4.2` 已推送，64 个资产（32 平台包 + 32 `.dgst`）上传完成
- [ ] Docker 镜像：`juestnow/tvgate:v3.4.2` 已推送（Docker Hub 已可见），`latest` 随 tag 更新
- [ ] Android Release：`releases/latest` 为 v3.4.2，`arm64` / `arm` / `x86_64` 三个 APK 齐全
- [ ] 发布后抽查：Release 二进制（linux-64 包解压）实测 `-version` 输出 v3.4.2
- [ ] 发布后抽查：`ss -tlnp` 确认监听 socket 为普通 TCP（不出现在 `ss -M` 中）
- [ ] GitHub Release 正文已填写（取自「二、精简版」）
- [ ] 商店：`doc/STORE-RELEASE-NOTES.md` 文案已在应用市场提交
