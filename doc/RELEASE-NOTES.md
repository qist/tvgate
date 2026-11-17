# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.4.4** |
| 发布日期 | 2026-11-17 |
| 发布提交 | 见 tag `v3.4.4` |
| 距上一版 | v3.4.3（2026-11-14） |
| 平台 | Linux / Windows / macOS / Android 共 33 个平台包 |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512），共 66 个 |
| Docker | `docker.io/juestnow/tvgate:v3.4.4`、`ghcr.io/qist/tvgate:v3.4.4`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.4.4 — 后台安全加固、PHP 多目录与 curl 兼容性改进、代码管理器体验优化
```

## 二、GitHub Releases 正文 · 精简版（用户可读）

```
### v3.4.4 更新内容
1. 后台安全加固 — 完善访问控制与敏感操作保护，建议所有用户升级（技术细节不在发布说明中展开）
2. 升级注意 — 后台访问路径 / 用户名 / 口令若仍为示例值，首次启动会被替换为随机值；
   新值见启动日志或配置目录 web-initial-password.txt（登录后可在「服务 → Web 设置」修改）
3. PHP 模块 — docroot 增加业务目录校验；新增 php.allowed_roots 多目录支持，代码管理器可直接
   浏览额外目录；/php/ 不再返回备份文件（*.bak.*）与隐藏文件（.git / .env 等）
4. phpgo 兼容性 — curl 补齐 CURLINFO_HEADER_SIZE、CURLINFO_SIZE_DOWNLOAD、CURLINFO_TOTAL_TIME；
   按响应头长度拆包后再 json_decode 的脚本可正常运行
5. 代码文件管理器 — 支持多根目录切换；新建文件后自动打开并聚焦；同名文件先确认再覆盖，
   覆盖 / 上传 / 解压前自动备份为 .bak.<时间戳>
6. 定时任务 — 「立即执行」仅允许执行已保存任务；保存与启动前增加一致性校验
7. 文档与提示 — Web / PHP / 定时任务文档同步更新，错误提示更明确

升级：下载对应平台压缩包解压覆盖后重启；Docker 用户重新拉取镜像；旧配置文件可直接使用。
如仍在使用示例凭据，请按第 2 条获取新的后台凭据。
```

## 三、完整版（用于发布公告 / 论坛 / 通知，用户可读）

```
## TVGate v3.4.4（2026-11-17）

本次为安全加固与兼容性版本。配置文件向后兼容，协议与服务行为无破坏性变更，可直接升级。

### 一、后台安全加固

- 完善登录与敏感操作保护，降低后台在公网暴露场景下的风险
- 具体改动不在发布说明中展开；建议所有用户尽快升级到 v3.4.4

### 二、后台凭据初始化（升级注意）

- 后台访问路径 / 用户名 / 口令若仍为示例值，首次启动会被替换为随机值
- 新凭据输出到启动日志，并记录到配置目录 web-initial-password.txt（权限 0600）
- 登录后可在「服务 → Web 设置」修改为自己的密码；修改密码需要输入当前密码
- 也可用环境变量 TVGATE_ADMIN_PASSWORD 指定初始口令

### 三、PHP 模块

- docroot 增加业务目录校验，避免指向系统目录或敏感目录
- 新增 php.allowed_roots：代码管理器可直接浏览这些目录，docroot 内指向它们的软链接也可被
  PHP 服务正常访问
- /php/ 不再返回备份文件（*.bak.<时间戳>）与隐藏文件（.git、.env、.htaccess 等），
  .well-known 保留（ACME 等标准用途）

### 四、phpgo（内嵌 PHP 运行时）

- curl：补齐 CURLINFO_HEADER_SIZE、CURLINFO_SIZE_DOWNLOAD、CURLINFO_TOTAL_TIME 等常量与实现
- CURLOPT_HEADER=true 时可按 header_size 精确剥离响应头，依赖该写法的脚本可正常 json_decode
- curl_getinfo 的常量支持整数与名称两种传参

### 五、代码文件管理器

- 多根目录：php.docroot 与 php.allowed_roots，页面顶部下拉切换
- 新建文件后自动切到所在目录并在编辑器打开（含聚焦）
- 同名文件先提示确认，覆盖前自动备份；上传、解压覆盖同名文件同样先备份
- 备份文件仍从列表隐藏，可在备份中心查看与恢复

### 六、定时任务

- 「立即执行」只接受已保存任务（按任务名匹配），不再执行请求体里的任意命令
- 保存与启动前校验任务配置，未通过校验的任务不进入调度

### 兼容性与升级

- 配置文件向后兼容：旧 config.yaml 可直接使用，无需修改
- 二进制：下载对应平台压缩包，解压覆盖后重启服务（systemd：systemctl restart tvgate）
- Docker：docker pull juestnow/tvgate:v3.4.4（或 ghcr.io/qist/tvgate:v3.4.4），重建容器
- Android：内嵌服务端升级后覆盖安装 APK 即可（App 内也支持在线更新）

### 发布校验

- 每个压缩包附 .dgst 校验文件（MD5/SHA1/SHA256/SHA512），下载后可比对
- 版本核对：./TVGate-linux-64 -version → v3.4.4
- 完整改动清单：doc/CHANGELOG.md
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.4.4` / `ghcr.io/qist/tvgate:v3.4.4` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android App | Releases 页 `TVGate-v3.4.4-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.4.4` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单

- [ ] 服务端 Release：tag `v3.4.4` 已推送，66 个资产（33 平台包 + 33 `.dgst`）上传完成
- [ ] Docker 镜像：`juestnow/tvgate:v3.4.4` 已推送，`latest` 随 tag 更新
- [ ] Android Release：`releases/latest` 指向 v3.4.4，`arm64` / `arm` / `x86_64` APK 齐全
- [ ] 发布后抽查：Release 二进制（linux-64 包解压）实测 `-version` 输出 v3.4.4
- [ ] GitHub Release 正文已填写（取自「二、精简版」）
- [ ] 商店：`doc/STORE-RELEASE-NOTES.md` 文案已在应用市场提交
