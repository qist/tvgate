# 应用商店发布说明（Android App）

> 命名与用法：本文件按**最新一次商店提交**维护；每次发版替换正文并更新顶部版本号与日期。
> 各商店对更新说明（What's New / Release Notes）有字数上限，下面按长度分档给出可直接粘贴的版本。
> 配套：服务端二进制的发布说明见 [`doc/RELEASE-NOTES.md`](RELEASE-NOTES.md)。

## 本次提交

| 项 | 值 |
|---|---|
| 应用 | TVGate（Android） |
| 版本 | v3.4.3（versionName 与内嵌服务端一致） |
| versionCode | 8 |
| 日期 | 2026-11-14 |
| 包名 | com.tvgate.app |
| 适配 | Android 5.0 (API 21) 及以上；arm64-v8a / armeabi-v7a / x86_64 |
| 产物 | TVGate-v3.4.3-arm64.apk、TVGate-v3.4.3-arm.apk、TVGate-v3.4.3-x86_64.apk（已签名） |

---

## 一、商店更新说明 · 精简版（≤ 500 字符，推荐）

```
【本次更新】
1. 内嵌服务端同步至 v3.4.3，新增 Android ARM32 v7 服务端包，老 32 位设备同样原生运行
2. 修复 Web 管理后台「编辑文档」查找结果不高亮：匹配项实时高亮并随滚动同步
3. 文档补充播放地址内联 FCC 参数说明，按频道单独开关更灵活
4. App 前端无改动，仅随内嵌服务端重新打包，覆盖安装即可升级
```

## 二、商店更新说明 · 标准版（≤ 800 字符）

```
【v3.4.3 更新内容】
1. 内嵌服务端同步至 v3.4.3 — 新增 Android ARM32 v7（armeabi-v7a）服务端包，32 位老
   盒子/电视可原生运行，与 arm64 包同源同版本；同时把 Android 交叉编译独立为脚本，
   NDK clang + CGO 外部链接生成 PIE，构建更稳定
2. 后台编辑器查找高亮修复 — Web 管理后台编辑文档时，查找面板打开后全部匹配实时高亮，
   当前项用更醒目的颜色区分，编辑与滚动时高亮同步跟随，不再"搜得到却看不见"
3. 文档补齐 — 播放地址新增内联 FCC 参数（fcc=<IP>:<端口>、operator=telecom|huawei、
   fcc=false），可按频道单独开关，未带参频道继续沿用全局配置
4. App 前端无改动 — 本次仅随内嵌服务端重新打包，覆盖安装即可升级
```

## 三、商店更新说明 · 详细版（无字数限制，用于官网 / 论坛 / Release Notes）

```
## v3.4.3（2026-11-14）

### 本次亮点
- 内嵌服务端同步至 v3.4.3：新增 Android ARM32 v7 服务端包，Android 构建独立脚本化
- 修复 Web 管理后台「编辑文档」查找结果不高亮
- App 前端无改动，覆盖安装即可升级

### 功能与改进
- Android ARM32 v7（armeabi-v7a）服务端包：面向仍在服役的 32 位安卓盒子/电视，
  与 arm64 包同源同版本，产物为 PIE，最低 API 21
- Android 构建独立化：新增 scripts/build-android.sh，内部固定 CGO_ENABLED=1 并调用
  NDK clang 生成 PIE，不再套用 CGO_ENABLED=0 + 静态链接的通用规则；NDK 路径多级自动
  探测，Android 也不再参与 make all
- 后台编辑器查找高亮：新增与编辑器同字体/行高/换行的镜像层，匹配区间实时以背景色
  高亮，当前项颜色更醒目，滚动与编辑时同步刷新
- 文档补齐：播放地址支持内联 FCC 参数（fcc=<IP>:<端口>、operator=telecom|huawei、
  fcc=false），按频道单独开关

### 说明
- 支持 Android 5.0 及以上；arm64-v8a / armeabi-v7a / x86_64
- 内嵌服务端同步至 v3.4.3（versionCode 8，可覆盖安装升级）
- 建议 App 与 Web 管理后台使用同一版本
- 完整改动见 doc/CHANGELOG.md（Android 与 服务端 两段）
```

---

## 四、关键词（用于商店搜索优化，按需填写）

- 中文：网关、代理、内网穿透、直播、电视、播放器、IPTV、组播、家庭影院、机顶盒
- 英文：gateway, proxy, live tv, player, streaming, multicast, set-top box

## 五、敏感权限说明（如商店审核问询时使用）

| 权限 | 用途 |
|---|---|
| 网络访问 / 前台服务 | 在本机启动网关与播放服务，供局域网内设备访问 |
| 开机自启 | 设备（机顶盒/电视）重启后自动恢复服务，达到"开机即用" |
| 通知 | 前台服务常驻通知，展示局域网访问地址与停止服务按钮 |

> 说明：服务仅在本机与局域网内运行，不采集、不上传用户数据。
> 使用播放能力时，请确保内容来源的合法授权。

---

## 六、当前 CI 状态（发布核对用，发布完成后勾选）

- [ ] 服务端 Release（qist/tvgate · tag v3.4.3）：33 个平台包 + `.dgst` 校验文件（共 66 个资产）
- [ ] 安卓 Release（qist/tvgate-android · releases/latest）：`TVGate-v3.4.3-arm64/arm/x86_64.apk` 三个 APK 已上传
- [ ] Docker 镜像：`juestnow/tvgate:v3.4.3` 已推送（Docker Hub 可见）
- [ ] 实机验证：`./TVGate-linux-64 -version` 输出 v3.4.3；Android 包 `file` 显示 PIE + `for Android 21`
- [ ] 商店后台提交后回填提交时间与审核结果
- [ ] 应用商店文案与本文件"一、二档"一致（版本号已改为 v3.4.3）
