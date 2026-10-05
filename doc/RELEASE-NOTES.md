# 服务端（二进制）发布说明

> 命名与用法：与 `doc/STORE-RELEASE-NOTES.md`（Android 应用市场）配套；本文件按**最新一次服务端发布**维护，
> 每次发版替换正文与顶部版本表。三档文案分别对应 **GitHub Releases 一键摘要 / Release 正文 / 完整说明**。

## 本次发布

| 项 | 值 |
|---|---|
| 版本 | **v3.3.9** |
| 发布日期 | 2026-10-05 |
| 发布提交 | `6bbf817` · tag `v3.3.9` |
| 距上一版 | v3.3.8（2026-10-03）以来 3 个提交 |
| 平台 | Linux / Windows / macOS / Android 共 32 个平台包 |
| 资产 | `TVGate-<平台>-<架构>.zip` + 同名 `.dgst`（MD5/SHA1/SHA256/SHA512） |
| Docker | `docker.io/juestnow/tvgate:v3.3.9`、`ghcr.io/qist/tvgate:v3.3.9`（同时打 `latest`） |

---

## 一、GitHub Release 标题 / 一句话摘要

```
v3.3.9 — 全局授权（global_auth）全链路修复：/pp 播放页纳入令牌校验、m3u8 分片与回看地址自带授权、令牌参数名前后端对齐
```

## 二、GitHub Releases 正文 · 精简版（用户可读）

```
### v3.3.9 更新内容
1. 全局授权（global_auth）覆盖 /pp 独立播放页 — 页面本体此前不校验令牌，开启授权后
   表现为「页面能打开但一片空白」（接口 403）；现统一走令牌校验，未启用授权时行为不变
2. m3u8 与回看地址自带授权 — 播放列表分片行、EXT-X-KEY / EXT-X-MAP / EXT-X-MEDIA
   内嵌地址、回看 play 地址在服务端签发时就带上令牌：hls.js 与第三方播放器按播放列表
   原样请求也能直接出画（此前表现为歌单能拉到、画面出不来）
3. 令牌参数名前后端对齐 — 播放页注入 meta 指明令牌参数名，前端按 global_auth.token_param_name
   读取并补签，不再写死 my_token；服务端配置名与历史默认名两个名字都认，老的
   ?my_token= 分享链接继续可用
4. 修 token_param_name 未配置时的全量 403 — 参数名为空导致所有播放请求被拒；
   PHP 采集入口同时剥离配置名与默认名，避免令牌进入脚本 $_GET
5. 后台外链自动补签 — 「复制外链 / EPG 接口 / player 入口」与发布页 /pp?live= 预览、
   回放地址按授权配置自动追加令牌，复制即用；未启用授权或没有静态令牌时原样返回
6. 前端对服务端已签名地址跳过补签，不再产生重复的同名令牌参数

升级：下载对应平台压缩包，解压覆盖后重启服务即可；Docker 用户重新拉取镜像；
安卓盒子/电视直接覆盖安装新版 APK（App 内也支持在线更新）。旧配置文件可直接使用，无需修改。
```

## 三、完整版（用于发布公告 / 论坛 / 通知，用户可读）

```
## TVGate v3.3.9（2026-10-05）

本次重点修复全局授权（global_auth）的全链路问题：/pp 独立播放页纳入令牌校验、
服务端签发的 m3u8 分片与回看地址自带授权、令牌参数名前后端对齐。

### 一、/pp 独立播放页纳入全局授权

- 现象：开启 global_auth 后，播放页接口早已由 requireToken 挡住（403），
  但页面本体一直是裸放行，于是「页面能打开但一片空白」
- 修复：/pp 与 /pp/ 深链统一走 globalAuth 中间件，校验失败返回 403 并记日志
- 未启用 global_auth 时中间件直接透传，行为与从前完全一致；
  /pp/assets/ 静态资源不受影响，页面可正常加载

### 二、m3u8 与回看地址自带授权

- 播放列表重写产出的分片行、EXT-X-KEY / EXT-X-MAP / EXT-X-MEDIA 内嵌 URI、
  回看签发的 play 地址，统一由服务端补上授权参数
- 原因：hls.js 与第三方播放器按播放列表原样请求，不会像页面那样补令牌，
  不签就一律 403（表现为歌单能拉到、画面出不来）
- 新增 auth.TokenManager.SignURL：静态令牌复用配置值，只开动态令牌时按目标路径现场签发；
  未启用授权时原样返回

### 三、令牌参数名两端对齐

- 播放页 <head> 注入 <meta name="tvgate-token-param">，取自 global_auth.token_param_name
  （按属性值转义后再拼进标签），前端按注入值读取并补签所有出站请求
- 服务端新增 auth.ExtractToken：按配置名取值，取不到回落 my_token，两个名字互为兼容
- 效果：配置了自定义参数名（如 ouyyt）的部署不再全部 403；老的 ?my_token= 分享链接继续可用

### 四、修 token_param_name 未配置时的全量 403

- 原实现直接按配置名取查询参数，参数名为空时恒取到空串 → 所有播放请求被拒
- 现改为按配置名取值、回落默认名；PHP 采集入口也同时剥离两个名字，
  避免令牌进入脚本 $_GET

### 五、后台外链自动补签

- 「复制外链 / EPG 接口 / player 入口」与发布页 /pp?live= 预览、回放地址，
  按 global_auth 配置自动追加令牌，复制出去即可直接播放
- 未启用授权、或只开了动态令牌（前端拿不到静态值）时原样返回，不改变地址语义
- 前端对服务端已签名地址跳过补签，避免重复追加同名参数

### 兼容性与升级
- 配置文件向后兼容：旧 config.yaml 可直接使用，无需改动
- 未启用 global_auth 的部署行为完全不变
- 二进制：下载对应平台压缩包，解压覆盖后重启服务（systemd：systemctl restart tvgate）
- Docker：docker pull juestnow/tvgate:v3.3.9（或 ghcr.io/qist/tvgate:v3.3.9），重建容器
- Android：安装 v3.3.9 版 APK 覆盖即可（App 内也支持在线更新）

### 发布校验
- 每个压缩包附 .dgst 校验文件（MD5/SHA1/SHA256/SHA512），下载后可比对
- 版本核对：./TVGate-linux-64 -version → v3.3.9
- 完整改动清单：doc/CHANGELOG.md（含 Android 段落）
```

---

## 四、下载与升级（发布核对用）

| 部署方式 | 获取方式 | 升级步骤 |
|---|---|---|
| 裸机二进制 | Releases 页 `TVGate-<平台>-<架构>.zip` | 解压覆盖 → 重启服务（`systemctl restart tvgate`） |
| Docker | `juestnow/tvgate:v3.3.9` / `ghcr.io/qist/tvgate:v3.3.9` | 拉取新镜像 → 重建容器（config 挂载不变） |
| Android App | Releases 页 `TVGate-v3.3.9-{arm64,arm,x86_64}.apk` | 覆盖安装，或 App 内在线更新 |
| 源码 | tag `v3.3.9` | `make linux-64`（自动构建 Web UI） |

## 五、发布核对清单

- [x] 服务端 Release：tag `v3.3.9` 已推送（指向 `6bbf817`），64 个资产（32 平台包 + 32 `.dgst`）上传完成
- [x] Docker 镜像：`juestnow/tvgate:v3.3.9` 已推送（Docker Hub 已可见），`latest` 随 tag 更新
- [x] Android Release：`releases/latest` 为 v3.3.9，`arm64` / `arm` / `x86_64` 三个 APK 齐全
- [ ] 发布后抽查：Release 二进制（linux-64 包解压）实测 `-version` 输出 v3.3.9
- [ ] GitHub Release 正文已填写（取自「二、精简版」）
- [ ] 设备抽查：开启 global_auth 后 /pp 页面可正常播放，m3u8 分片与回看地址免手工补令牌
- [ ] 设备抽查：配置自定义 token_param_name 的部署全链路可用，老 ?my_token= 链接仍可打开
- [ ] 商店：`doc/STORE-RELEASE-NOTES.md` 文案已在应用市场提交
