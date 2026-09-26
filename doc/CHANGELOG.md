# Changelog

---

## Android (tvgate-android)

### v3.3.5

```
1、内嵌服务端同步至 v3.3.5 — 管理后台手机端排版修复（页头按钮换行不再被裁、列表页操作区
   独立成行让文件名完整显示、文件编辑器工具栏不再撑破页面导致整页横滑）；任务页点「添加」
   导致页面崩溃修复；详见服务端 v3.3.5 条目
2、App 侧本次无独立代码改动 — 仅随服务端同步发版：内嵌同版本服务端二进制（含 /pp H5
   播放器与管理后台），覆盖安装即可升级
```

### v3.3.4

```
1、内嵌服务端同步至 v3.3.4 — 代理组「改规则仍走旧规则」修复（缓存按组名失效清理，
   整组删除/改名同样清理）、代理组每组新增「清理缓存」按钮（一键清缓存+重置测速）；
   EPG 节目单每日 0 点强制过期换新（跨天不再读到旧节目单）；带 4K/高清后缀频道名的
   模板 EPG 错配修复（CCTV1-4K 不再串到 CCTV14）；台标死链探测移出重载路径+结论
   落盘（APK 启动更快、死链 404 不再随重启复活）；详见服务端 v3.3.4 条目
2、App 侧本次无独立代码改动 — 仅随服务端同步发版：内嵌同版本服务端二进制（含 /pp H5
   播放器与管理后台），覆盖安装即可升级
```

### v3.3.3

```
1、内嵌服务端同步至 v3.3.3 — iPhone Safari 播放一直转圈修复（ManagedMediaSource
   sourceopen 不触发）、iOS 15 打开播放页整页白屏修复、原生播放失败给出明确原因提示
   （编码不支持等）、播放器诊断日志收口到调试开关（未开启调试控制台零输出）；
   详见服务端 v3.3.3 条目
2、App 侧本次无独立代码改动 — 仅随服务端同步发版：内嵌同版本服务端二进制（含 /pp H5
   播放器与管理后台），覆盖安装即可升级
```

### v3.3.2

```
1、内嵌服务端同步至 v3.3.2 — 旧 CDN TLS 握手失败修复（部分频道「一直打不开」，日志报
   remote error: tls: handshake failure）；台标死链自动清理（无台标频道直接显示首字徽章、
   不再空等图床 404）；手机端原生播放音量面板改为向下弹出可触控（不再被下方栏目遮挡）；
   详见服务端 v3.3.2 条目
2、App 侧本次无独立代码改动 — 仅随服务端同步发版：内嵌同版本服务端二进制（含 /pp H5
   播放器与管理后台），覆盖安装即可升级
```

### v3.3.1

```
1、内嵌服务端同步至 v3.3.1 — 播放页两处「画面全黑」（切台后需点一下才恢复、小米等浏览器
   无媒体徽标/部分台黑屏）修复；上游定时踢连接导致的断流续播与时间轴重锚（重连后画面
   正常接续）、解析直链加绝对有效期与失效自愈；TS 缓存开启时 Hub 的 FLV 头信息按 tag
   边界捕获（晚加入的观众不再黑屏）；手机端原生解码时控制条外置到画面下方、换线路改为
   置顶下拉（选中即收回）；详见服务端 v3.3.1 条目
2、App 侧本次无独立代码改动 — 仅随服务端同步发版：内嵌同版本服务端二进制（含 /pp H5
   播放器与管理后台），覆盖安装即可升级
```

### v3.3.0

```
1、内嵌服务端同步至 v3.3.0 — 移动系（魔百盒）频道不再「一直加载中」打不开：
   修复 CDN 在 GOP 边界注入的无 PTS 访问单元导致视频轨拿不到参数集与关键帧、以及视频
   init 顺序竞态导致 SourceBuffer 建不出来；切台卡顿与持续丢帧修复。整点跨小时断流治理
   —— 分片目录纠偏 + 缓冲缺口瞬间跳过（不再卡 30 秒），断档后「声音不回来」一并修复；
   上游抖动导致的长时间停拉与跳片修复。多伴音流（6 音频 + 1 视频）不再被拒收、AC-3 主轨
   与媒体徽标正确；广播/纯音频频道恢复有声并显示广播占位。EPG 多来源合并与主源回退、
   组内同名频道可换线路并自动故障转移；频道列表美化（首字徽章 / LIVE 标识 / 分组计数 /
   搜索号位）、播放页改版（媒体信息徽标、界面风格）；起播与播放列表轮询更省流量；播放器
   稳定性（服务重启窗口内不再弹错误、worker 崩溃自动重建）；详见服务端 v3.3.0 条目
2、App 侧本次无独立代码改动 — 仅随服务端同步发版：APK 版本号取自 tvgate 源码
   config/version，内嵌同版本服务端二进制（含 /pp H5 播放器与管理后台），覆盖安装即可升级
```

### v3.2.2

```
1、内嵌服务端同步至 v3.2.2 — 网络切换/断流自愈（WiFi ↔ 4G/5G 不再永久错位、无声或
   卡转圈）、播放中"声音卡顿一下"修复（PCM 重钉改为有界判定）、音频软解统一为
   demuxer 逐帧切分 + 逐帧 PTS 外推、播放停摆兜底与音频链静音看门狗、缓冲领先上限
   10s → 30s、 seek 不再清空音频队列；详见服务端 v3.2.2 条目
2、android_autoplay 默认改为关闭 — 未配置（null）与 false 一致：启动停留在信息界面，
   仅显式 true 才自动进入直播页（此前未配置即自动进入）
3、播放期间屏幕常亮 — 前台服务/播放页保持常亮，支持后台持续播放不熄屏
4、WebView 渲染进程崩溃自动恢复 — 渲染进程被杀后自动重建并回到播放页；
   H5 全屏时手机自动横屏
5、修复主题预置 key — 修正为 tvgate.theme（此前键名不一致导致预置主题偶发不生效）
6、更新检查健壮性 — 全链路日志与弹窗生命周期保护（避免升级弹窗在 Activity
   重建后泄漏/重复弹），本地版本号显示统一去 v 前缀、资源名匹配兼容带/不带 v 前缀
```

### v3.2.1

```
1、内嵌服务端同步至 v3.2.1 — E-AC-3/AC-3 软解长播音画不同步修复（源 PTS 周期
   跳变不再触发 bridging）、中断恢复超时误报导致切台无声修复、phpgo date() 时区
   按原生 PHP 语义解析、LiveSync 自愈日志静默与 drift 诊断降频
2、支持 android_autoplay 标记并修复返回键无法退出播放页 — 显式 true 时启动自动
   进入直播页（未配置默认不进入）；返回统一走 onBackPressed：HTML5 全屏 → 退播放页
   → WebView 历史 → 退后台，退出播放页清空历史防止再次导回
3、CI 恢复上传 GitHub Release — APK 作为资产上传，App 在线更新（releases/latest）
   可检测新版本并在线升级；README 同步在线更新说明
```

### v3.2.0

```
1、内嵌服务端同步至 v3.2.0 — AC-3/E-AC-3 杜比音频 WASM 软解设计（浏览器 MSE
   不支持 ac-3 时不再无声、修复 DVB 0x6A 描述符识别）、配置保存后立即生效（不再
   等 fsnotify 5s 防抖）、配置备份内容比对+手动备份/一键清理、Code 大文件编辑
   优化（中文输入法不再卡顿）、静态资源 gzip 加速、playback 拉取挂起超时防护等
```

### v3.1.1

```
1、player.android_autoplay 默认值落地 — 未配置（null）与 false 一致：启动停留在
   信息界面；显式 true 才自动进入直播页（此前版本未配置即自动进入）
2、内嵌服务端同步至 v3.1.1（解析型源 Referer/重试修复、UA 分组作用域、配置热加载
   即时生效、深链编码标识、播放页黑屏 legacy 兼容等）
```

### v3.1.0

```
1、直播接口启动自动打开 — config.yaml player.enabled: true 时，服务就绪后自动经 /pp 独立
   播放页打开直播（信息卡片淡出 + 播放页淡入过渡、沉浸式全屏、自动起播、支持 H5 网页全屏）；
   返回键退回信息卡片且本次会话不再自动弹出；ConfigParser 新增 player.enabled 解析
2、消除手机/电视白色元素 — 窗口背景/状态栏/导航栏统一深色 #0D1117；WebView 底色压黑且每次
   导航重新压黑；document-start 脚本预置播放器 dark 主题（H5 侧 localStorage 为裸字符串
   比较，不能带 JSON 引号），浅色系统下不再出现白色顶栏
3、CI 上传 artifact 并创建 GitHub Release — 构建产物（APK 命名 TVGate-<版本>-<abi>.apk）
   发布到对应版本 Release，App 在线更新（依赖 releases/latest）可检测新版本并在线升级；
   新增 setup-node（Node 20）
4、build-android.sh 补齐 Web 前端构建 — web/dist 不进 git（.gitignore 只留 .gitkeep），
   go:embed 缺产物时二进制内为占位页（管理后台/直播播放器均不可用）；现编译 Go 前自动
   npm 构建（ui 源码比 .built 标记新才重建；npm 缺失直接报错退出）
5、支持 player.android_autoplay 标记位 — 显式 true 时启动自动进入直播页，未配置默认
   不进入（Web 后台播放器页可开关）；修复返回键无法退出播放页（返回统一走
   onBackPressed：HTML5 全屏 → 退播放页 → WebView 历史 → 退后台，退出播放页清空
   历史防止再次导回）
```

### v3.0.10

```
1、停用自动注入 DNS，默认走系统/本地 DNS — 服务端移除 PreferGo + CGO 链接，系统解析经
   getaddrinfo→netd 取设备本地 DNS，公网/内网域名无需再注入；TVGateService.kt 相关注入代码
   整块注释保留（稳定后可清理），网络变化不再改 config 并重启
2、新增在线 APK 更新 — 启动/前台对比 GitHub Latest release，发现新版本可在线下载升级；无网络时跳过
3、CI 支持手动触发构建时覆盖版本号（workflow_dispatch 输入 VERSION，用于在线更新测试）
4、兼容修复 — 本地版本号显示前统一去掉 v 前缀；匹配 release 资源名时兼容带/不带 v 前缀
5、CI 恢复上传 GitHub Release（移除测试期 artifact 步骤），构建产物合并到对应版本 release
```

### v3.0.9

```
1、修复代码文件管理 / 备份中心中文文件名乱码 — decodeFilename 原先先尝试 GBK 解码，UTF-8 中文文件名按 GBK 解码后也含 CJK 字符，导致从仓库同步的 UTF-8 中文文件名（如"可可影院.json"）显示为乱码；改为优先判定原始字节为合法 UTF-8 时直接返回，仅非 UTF-8 字节才走 GBK/GB18030 转码（兼容旧 Windows 上传的 GBK 文件名）
```

### v3.0.8

```
1、新增仓库同步模块（sync）— 将 GitHub/GitLab 仓库内容单向同步到本地 docroot 子目录；支持多仓库（sync 为条目列表，每项独立同步循环/独立 manifest）；基于 git blob sha 增量对比；.bak.<时间戳> 备份；protect 保护清单（设备私有文件永不覆盖/永不删除）；孤立文件（本地有远端无）日志报告；Web 新增"仓库同步配置"编辑器（多仓库添加/删除）
2、整仓归档同步 — 公开仓库走 codeload 直连下载（不占 api.github.com 未认证 60 次/小时限额）+ 本地计算 git blob sha 对比；首次同步或增量树 API 限流时自动降级整仓归档，避免大仓库逐文件拉取触发 429/403；归档下载使用独立 10 分钟超时
3、修复配置热加载 php docroot 失效 — 热加载时 php.Init 先于 SetDefaults 执行，相对 docroot 拿到未解析路径导致脚本 404；调整顺序后相对/绝对 docroot 及 php.path 修改均即时生效
4、修复 Web 配置保存 YAML 标签错误 — 手工构造 YAML 节点未设 Tag，repo 等字符串被序列化成 `!!null xxx` 导致配置重新加载失败；显式设置 !!str/!!bool/!!int/!!seq/!!map
5、凭据显示安全 — sync GitHub token 后端掩码返回（保存后不可回显，掩码占位保存保留原值、填新值才覆盖）；global_auth 密钥/token 默认打码 + 点击眼睛按钮按需显示
```

### v3.0.7

```
1、修复 PHP 链接校验短超时误判 — phpgo HTTP 栈比原生 PHP 慢，0.1s 短超时会把好链接误判为失效导致缓存被反复清掉；get_http_response_code 改为"0.1s 快速校验 + 加几秒兜底重试"，校验超时按"无法判断"处理
2、PHP 缓存校验逻辑增强 — 链接校验超时（拿不到明确状态码）不再清缓存、不覆盖，用旧缓存兜底；只有明确非 200 才重新生成
3、文档完善 — phpgo 实际实现函数全量清单（300+ 函数/12 别名/no-op 标注）、README 补充函数覆盖与超时注意事项
```

### v3.0.6

```
1、PHP docroot 默认相对路径 www — 以配置文件所在目录为基准，安卓无需改绝对路径即可用 PHP 脚本；启动兜底创建 files/www 目录
2、修复代码编辑器无法编辑/保存 — Android /data/user/0 → /data/data 符号链接被误判越权，归一化 root 后正常
3、开机自启 — 新增 BootReceiver 监听 BOOT_COMPLETED，设备重启后自动启动服务（部分 ROM 需在自启动管理放行）
4、前台服务改用 specialUse 类型 — Android 15 禁止从开机广播启动 dataSync，specialUse 无类型时限、允许开机自启
5、体积优化 — 交叉编译叠加 -gcflags=all=-l 关闭内联，APK 每架构约省 1.1MB
6、兼容性确认 — minSdk=21（Android 5.0+），代码全部按版本判断，arm64/arm/x86_64 三架构覆盖
```

### v3.0.5

```
1、网络切换自动更新DNS — 网络环境切换时自动检测DNS变化并更新config.yaml重启进程
2、手动重启内核 — 界面新增重启内核按钮，支持遥控器焦点导航
3、TV分辨率检测与UI自适应缩放 — 重写布局为三段式weightSum结构，兼容海信等未声明TV uiMode的电视
4、修复首次手动重启报错(退出码141) — 新增killedByUs标记区分主动杀进程和异常退出
5、修复重启按钮文字不显示 — 用代码设置背景和padding避免Material3主题覆盖
6、重启按钮改为垂直布局 — 避免水平溢出遮挡，遥控器提示卡片改为vertical布局
7、启动界面美化、后台运行、局域网信息展示、遥控器支持
8、修复process.destroy()导致consume线程InterruptedIOException崩溃
9、APK分架构打包 — arm64-v8a / armeabi-v7a / x86_64
10、迁移完整构建链 — 交叉编译/分架构打包脚本 + GitHub Actions CI
```

---

## 服务端 (tvgate)

### v3.3.5

```
1、管理后台手机端排版修复 — 窄屏（≤430px）下多处页面横向溢出、按钮被裁出屏幕：
   页头按钮行改为可换行（YAML 编辑器「保存」、配置备份「批量删除」、备份中心、域名
   映射此前均被裁在屏外）；列表页行内操作区窄屏独立成行右对齐，文件名拿回整行宽度
   （配置备份此前只能显示成一个「config.yaml…」，实测列宽 78px → 256px）；文件列表
   操作按钮移动端常显、桌面端仍悬停显示
2、配置内容编辑器工具栏修复 — 原先工具栏被 shrink-0 锁死在 627px，导致 flex-wrap 失效、
   整页横向滚动（实测 scrollWidth 652 而屏宽 390），末尾「检测/批量替换/下载/关闭」
   全部被裁；现窄屏上下两行（文件名一行、按钮组一行），lg 起恢复同行
3、卡片横向溢出根因修复 — 全局 Card 组件补 min-w-0：Card 作为 flex/grid 子项时默认
   min-width:auto，会被内部宽表格/代码块撑开，导致整页横滑、内部滚动容器同时失效
4、任务页「添加」崩溃修复 — 点添加后页面报 Cannot read properties of undefined
   (reading 'cron')：新增任务时以旧数组长度取元素越界，改为直接进入编辑态
5、后台入口 HTML 缓存策略调整 — Cache-Control 由 no-cache 改 no-store，避免 WebView
   拿着旧 index.html 引用已不存在的旧 assets（此前表现为升级后设备上仍是旧界面）
```

### v3.3.4

```
1、代理组「改规则仍走旧规则」修复 — 访问缓存（域名→代理组映射）的失效清理从域名交集
   改为按组名全量清理：此前组内域名删光、域名挪到其它组时旧缓存条目会残留（交集为空
   被跳过），请求继续命中旧组不走新规则；整组删除/改名此前完全无人清理，现同样补清
2、代理组每组新增「清理缓存」按钮 — 管理后台代理组页每组卡片一键清空该组全部访问
   缓存并重置测速统计（下次请求强制重新测速），修改规则后立即生效，无需等热重载或
   定时清理；原有的定时清理（10 分钟清 30 分钟闲置条目）保持不变
3、EPG 缓存每日 0 点强制过期 — XMLTV 节目单按天组织，服务端每日本地 0 点强制重拉
   全部来源（绕过 1 分钟节流，保留进行中去重），跨天后不再读到旧节目单；周期刷新
   频率与幂等保护不变
4、模板 EPG 画质后缀错配修复 — 频道名带画质后缀先剥离再查模板 EPG（CCTV1-4K→CCTV1、
   北京卫视4K→北京卫视），修复 CCTV1-4K 被外源模糊匹配到 CCTV14 少儿节目单；仅后缀前
   是分隔符（-/_/空格/·）或非 ASCII 才剥，CCTV4K 等本名频道不受影响，查不到回落原名
5、logo 死链探测异步化+落盘 — 死链探测移出订阅重载关键路径（后台 goroutine，30 秒
   预算），频道表先行下发不再被阻塞；探测结论持久化 logo_probe.json（配置目录，原子写），
   APK/进程重启不丢结论、死链不随重启「复活」闪 404，稳态零探测启动更快；本地台标
   存在性判断从逐频道 os.Stat 改为一次 ReadDir 目录索引
6、存量 data race 修复（-race 实证）— 全局配置发布点（load_file.go）补写锁；删除测试中
   与存活后台 goroutine 竞争的冗余 config.Cfg.HTTP 字段写入；全量测试 -race 通过
```

### v3.3.3

```
1、iPhone Safari 播放一直转圈修复 — iOS 17+ 走 ManagedMediaSource（MMS），而 MMS 的
   激活条件是必须在挂 src 之前于 video 元素上设 disableRemotePlayback=true（此前误设在
   MediaSource 对象上是无声的无效赋值，AirPlay 预期抑制 sourceopen，管线永不启动）；
   同时管线改为 open 后立即启动、init/media 段暂存待 sourceopen 补发（不再被事件
   门控饿死）；sourceopen 看门狗修复重挂后静默卡死的洞（video.load() 会 detach 且
   不可重挂，看门狗不再被 opening 状态放行）；诊断面板新增「MS源」状态行
2、iOS 15 打开播放页整页白屏修复 — iPhone Safari 16.4 之前没有 screen.orientation
   API，直接解构 undefined 抛 TypeError 导致 React 整树无法挂载；改为 API 守卫 +
   innerWidth<innerHeight 降级判断横竖屏（监听注册/清理同样守卫）
3、原生播放失败诊断与误报治理 — error 事件上报 MediaError code/message/src（此前只有
   一句「原生播放失败」无细节）；code=1（换台 load() 中断）为良性事件不再上报，避免
   误触错误恢复切线路；code=3/4 追加明确提示：常见于 MP2/MP3/AC-3 音轨源不被系统原生
   HLS 支持（iOS 15 无 MSE 只能原生，引擎 MSE 路径有 WASM 软解故 iOS 17+/安卓可播），
   属系统级限制
4、启动错误上屏 — 两个入口 HTML（index/player）加 ES5 启动错误陷阱：脚本异常/
   Promise 拒绝在页面底部红条显示、6 秒 root 仍空给出资源未加载提示——旧 WebView/
   低版本系统上白屏不再无声无息（本次即靠它定位 iOS 15 解构崩溃）
5、播放器诊断日志收口 — 全部诊断性 console.warn 统一收口到调试开关：?dbg=1 /
   localStorage['tvgate-player-debug']='1' / __tvdbg() 三种开启方式，主线程与 worker
   线程（开关随 load 命令下发）全覆盖；未开启调试控制台零输出；错误级日志（真故障）
   不受开关影响始终显示
6、构建依赖修复 — Makefile UI 重建依赖补齐入口 HTML 与 vite.config.ts：此前改入口
   文件（如启动错误陷阱）不触发前端重建，产物与源码不一致
```

### v3.3.2

```
1、旧 CDN TLS 握手失败修复 — 部分 CDN 仅支持 TLS 1.2 + 静态 RSA 密钥交换，而 Go 新版
   默认套件列表已剔除静态 RSA：客户端握手直接被拒（remote error: tls: handshake failure），
   频道打不开（openssl/浏览器正常，Go 失败）。所有出站 TLS 客户端显式配置套件列表（静态
   RSA + 现代默认套件，utils/tlsutil 统一实现）：正常 CDN 仍优先协商 ECDHE、前向保密不受
   影响，仅旧 CDN 落到 RSA 兜底；覆盖播放器直连 / 代理组 / php 源 / DNS DoT、DoH / 域名
   映射 / 全局 DefaultTransport。不用 GODEBUG tlsrsakex（已于 Go 1.27 移除），全版本 Go
   行为一致
2、台标死链自动清理 — 模板补齐的台标地址在订阅刷新时轻量并发探测（Range 探测、限并发
   16、单请求 5 秒 / 单轮 10 秒预算，不拖重载、未探完的下轮续探），确认 404/410 的置空
   台标——前端直接走首字徽章，不再整页空等图床失败（首轮清理 350+ 频道）；403/5xx/
   网络错误视为「不确定」保留原地址且不写缓存（防误杀防盗链源，宁可多留不可误杀）；
   探测结论按 URL 缓存（命中 24h / 缺失 2h），稳态刷新零探测开销；组内聚合自动落到
   存活的线路台标；本地台标目录补 Cache-Control（4 小时 + 条件校验，与外部图床一致）
3、手机端原生播放音量面板方向修复 — 控制条外置（挂视频区下方）后音量滑杆仍向上弹出、
   被播放画布拦截无法触控；改为外置条时向下弹出（与换线路下拉一致），桌面浮层模式不变；
   弹层伸出控制条后不再被下方页面栏目遮挡（外置条包裹层建立独立层叠上下文 z-30，
   音量/换线路弹层统一抬升，高于页头低于抽屉）
4、TVFusion 设计基准入库 — 全平台客户端（TVBox 接口兼容）设计文档 doc/tvbox-universal-
   client-design.md：协议原则（JSON 是协议/site.jar 优先级/七方法 ABI/playerType 透传）、
   兼容矩阵（输入/输出两维，jar/py/js/json）、jar 三路执行（Android ART 委托 / 桌面
   sidecar JVM / iOS 服务器桥）、出站链路四能力（去广告/域名映射/DNS/代理，FongMi 字段
   实证）、双通路播放（转发/直通 + advised 适合度 + 单向回落）、monorepo 工程结构与
   分期路线 A–F；项目定名 TVFusion（MediaFusion 与 Stremio 生态知名插件同赛道撞名弃用）
```

### v3.3.1

```
1、播放页两处「画面全黑」修复 — 切台后画面全黑、点一下才恢复：双槽切换不再用 opacity
   隐藏 video（部分内核下隐藏即丢帧）；小米等浏览器「没有媒体徽标 / 部分台黑屏」：放宽
   MSE 探测 + 原生化明示（媒体徽标处直接标注原生模式）+ 视频轨不支持时的占位提示
2、直播断流续播（上游定时踢连接）— 连续流 EOF 不再当「播放结束」：固定短延迟重连、
   不占错误重试预算；重连续接按播放头重锚时间轴并重建解复用器（修「重连成功但画面不动」）；
   重锚目标限制在播放头 +2 秒内（避免每次连接把整段缓冲灌回来、直播延迟越拖越长，实测
   1 分钟落后 45 秒）；停摆看门狗对原生播放路径不再误判换线路（接管型内核会暂停我们的
   <video>，currentTime 停推不等于流坏）；HLS 探测提前退出——首个非空白字节不是 # 就
   不再等 2MB/4 秒超时，FLV 直播起播不再白等 4 秒并少拉一路数据
3、302 解析型源直链生命周期 — 解析出的直播直链不再无限复用：缓存加绝对有效期（90 秒，
   到点即使频道一直在播也重新解析），直播流过早结束（<20 秒）时立即清缓存、下次请求
   重新解析；修「一直重连同一条失效直链」（在上游看来就是持续断开重连，可能触发风控）
4、Hub 的 FLV 头信息（TS 缓存开启时）— 旧实现按「整个读块」匹配，而第一个读块几乎必然
   是「文件头 + 配置 tag + 数据 tag」连在一起：要么把整块当成文件头重放给晚加入的客户端
   （在流前面插出第二个 FLV 头，H5/MSE 解复用乱套），要么配置 tag 永远抓不到（拿不到
   SPS/PPS、AAC 配置，黑屏花屏）。现按 tag 边界跨块增量捕获 13 字节文件头 + 首个
   AVC/AAC sequence header 完整 tag，捕获齐即短路（稳态零开销）；头信息重放跳过源客户端
   （它自己的连接本来就带头信息）；捕获写入补锁，消除数据竞争；AAC 判定放宽到全部采样率
5、手机端原生解码控制条外置 — 紧凑布局下播放/音量/换线路移到播放画面之外（经 portal 挂到
   视频区下方的常规文档流）：接管型内核会把叠在视频上的浮层连同点击一起吞掉；换线路改
   「点击开合 + 置顶下拉」（fixed 定位、不受 overflow 裁剪，选中即自动收回）
6、播放诊断浮层 — 播放地址加 ?dbg=1 打开：后端模式、MSE 逐串探测结果、两槽出帧状态、
   视口/布局/控件状态，报障时一眼定位
```

### v3.3.0

```
1、去 GPL 重写与合规收尾 — 播放引擎整体迁移为 ui/src/media-engine（解封装/软解/管线/
   wasm 重组；clean-room 自研实现，MPL-2.0），UI 外壳（播放页、管理界面组件、i18n、
   播放页入口脚本、样式表）完成 clean-room 重写，内部契约去同源化（/api/player/* v2）；
   新增 THIRD-PARTY-NOTICES 并在 README 标注许可分界；与 GPL 上游逐文件指纹复测：引擎
   k=8 零成块重合、全量 k=6 仅余接口字段与常量字面量，依赖侧（Go/npm）无 GPL/LGPL 组件
2、修复移动系（Huawei CDN）流全部不开播 — 三处根因：① CDN 在 GOP 边界注入
   [PPS][AUD][SPS][PPS][IDR] 访问单元且不携带 PTS/DTS（ISO 13818-1 中 PTS 为可选），
   旧逻辑整包丢弃 → 视频轨永远拿不到参数集与关键帧、轨无法发布、MSE 永远等不到视频
   init；② 视频轨晚于音频数秒发布时的 init 发射顺序竞态，视频 SourceBuffer 永远建不出
   来（一直加载中）；③ 视频成段未回关键帧边界，切台后卡顿/反复缓冲与持续丢帧。现无 PTS
   视频 PES 照常解析并出帧（注入帧 DTS 按源预留帧槽推算），init 顺序与分段起点一并修正；
   同类流（广场舞等）同步受益
3、整点断流与跳片治理 — 跨小时分片目录纠偏（整点分片 404 回退上一小时目录）；MSE 缓冲
   缺口跳过（gap skip）把整点缺片的「卡 30 秒」变成「瞬间跳过」；软解 PCM 时间轴保留大
   跳变，修整点断档后「画面恢复、声音不回来」；HLS 分片失效按批次刷新播放列表快速恢复
   （不再死啃旧分片）；上游抖动/整点导致的长时间停拉与跳片修复；非解析型上游请求不再
   重试（消除整点分片的成倍重试日志）
4、起播与轮询性能 — 起播路径播放列表只请求一次（省一次完整往返）；HLS 空闲轮询按目标
   时长自适应节流（一个分片窗口只开一次）；缓冲见底时改回 1 秒密集问保护临界缓冲
5、多音轨与声画分流 — 多伴音流（6 音频 + 1 视频）多音轨撞 SourceBuffer 被拒收修复；
   AC-3 主轨丢失与媒体徽标被 MP2 备轨覆盖修复；独立音轨（声画分流）走 MSE 直通；音频
   时间轴锚定视频基准做音画对齐；切台旧音轨残留（在途 PCM 消息与 sourceopen 时序缺口）
   修复；无缝切台过渡期音量状态保护（撤销过渡静音 + 隔离内部动作）
6、广播/纯音频频道 — 支持裸 ADTS AAC 分片（广播频道不再无声）与 ADTS 跨 PES 接帧；
   纯音频频道显示广播占位，不再只剩一片黑
7、频道列表与 EPG — 组内同名频道聚合多线路（前端换线路 + 自动故障转移）；EPG 多来源
   合并与主源回退、多订阅源解析、统一入口（ch 认 tvg-id/频道名/key，date 可选）与对外
   ch 查询；订阅/EPG 跟随重定向；打开侧边栏把在播频道定到列表正中；频道列表美化（首字
   徽章/LIVE 标识/分组计数/搜索号位）；台标前置与自愈、手机端视频区紧凑；分组筛选跟随
   正在播放的频道；播放页与后台界面改版（媒体信息徽标、界面风格、后台美化、EPG 与频道
   侧栏）；切台即上 loading 遮罩、深链按线路 key、EPG 失败退避
8、播放器稳定性与其他 — 手机侧栏频道列表/节目单不渲染（Activity 误从 lucide-react
   导入）修复；搜索框与浮层标题浅色下对比度提升；源初始化零重试导致服务重启窗口内耗尽
   重试预算弹错误、worker 崩溃后不重建导致服务重启立即断开 —— 均修复；phpgo PCRE 内核
   完整化（RE2 换 regexp2 回溯引擎，PHP 语义全对齐）；崩溃转储与标准库日志写入日志文件
   （不再依赖启动脚本 2>&1）；全局配置先补默认值再发布（修热重载后进程被空指针带走）；
   后台订阅源/定时任务示例与测试夹具脱敏（去除真实脚本名/内网 IP/源站域名）
```

### v3.2.2

```
1、音频软解统一 FFmpeg avcodec WASM — 删除 minimp3(MP2)+ac3 双模块，改用单一
   avcodec_audio.wasm（libavcodec，me_* ABI），一套覆盖 MP2/MP3/AC-3/E-AC-3/AAC，
   内置 WSOLA 供主线程变速、info 上报 samplesBeforeInput 供 PTS 外推；AC-3/E-AC-3
   软解路径与 MP2 方案一致整段转发 PES pts（不做帧级 PTS 纠正；该"整段转发"模型已在
   本版本条目 18 统一为「demuxer 逐帧切分 + 逐帧 PTS 外推」）
2、AC-3/E-AC-3 软解稳定性对齐 ac3-lab — 接通分离音轨（EXT-X-MEDIA）软解通路（此前
   onRawAudioData 未接线，AC-3 帧绕过软解导致 MSE 无声）；PCM 时间轴基座固定
   （setPcmSourceBase(0)），解除 WebKit 首帧 init gate 死锁；音频轴 blocked 自愈改为
   主线程 rebase 时间轴平移，替代 worker 绝对钉扎协议（深管线在途时会钉出恒定落后
   的轴导致永久静音）；one-shot 重锚确认丢失后立即可重发（不再 60s 冷却）；resync
   失败判定要求视频时钟确实推进，视频缓冲停摆不再误触发整场重建
3、桌面后台播放自愈 — 回前台 AudioContext suspended 自动恢复、resume 失败必上报一次、
   hidden 时不置 needsUserInteraction（避免毒化回前台恢复）；AC-3 与 AAC 后台播放
   行为一致；新增全链路 PCM 丢弃计数（pcm-audio-stats，3s 一报）便于定位丢帧环节
4、修复首次打开播放页点台无反应 — pending 槽首次装载时 backend 需异步等音频探测
   （≤500ms）才建 controller/MSE，首次 play() 被随后 MSE src 重置以 AbortError 打断
   且被静默吞掉，表现为"第一次点台无反应、再点一次才切"；现 pending 槽 canplay 且
   仍处于当前切换代际时补播兜底
5、修复 PTS 重叠纠正空指针与拉流泵崩溃 — ts-demuxer 对 AC-3/E-AC-3/AAC 的 PTS 重叠
   纠正补 audio_metadata 空值保护；fetch-loader 对 onDataArrival 异常兜底，不再打挂
   拉流泵循环导致播放暂停
6、Web 首页应用连接数读取真实活跃连接 — ActiveConnections/TotalConnections 此前从未
   被写入恒为 0；现 connections 改读活跃客户端数（5s 刷新），累计总数在新连接注册
   时累加（进程重启归零）
7、修复文件上传成功/失败计数恒为 0 — FileList 为 live 对象，发起上传后清空 input 使
   同一引用被原地清空；改为快照计数并采用服务端 uploaded/failed 名单（部分失败此前
   被吞，现逐个列出）
8、组播配置页拆分 — 「组播配置」（网卡、IGMP 刷新重连间隔）与「FCC 配置」（上游接口、
   FCC 上游接口、FCC 类型、缓存大小、监听端口范围）分为两张独立卡片
9、频道号展示 — 频道按订阅序位编号（number），频道列表/切台弹窗/信息条均展示频道号；
   手机端频道列表改双列网格，PC 侧栏收窄至 18rem
10、phpgo 修复 — 请求自带 Accept-Encoding（CURLOPT_ENCODING/HTTPHEADER）时按
    Content-Encoding 手动解压并支持 br（此前 Go Transport 不自动解压）；& | ^ 补齐
    PHP 字符串逐位运算语义；新增 stream_set_timeout/stream_set_blocking 防 socket
    读不到数据时请求永久挂起
11、部署脚本对齐 — start.sh / TVGate.service 二进制名与本地路径统一为
    build/TVGate-linux-64，编译产物路径与启动路径一致（无需手动搬移）
12、依赖更新 — golang.org/x/crypto 0.56.0、golang.org/x/sync 0.23.0、gortsplib 5.6.5
13、EPG 频道名归一化模糊匹配 — 订阅频道名与 XMLTV 频道名存在质量后缀变体
   （"北京卫视4K" vs "北京卫视"）时自动匹配节目单：小写、去常见分隔符
   （-/_/./空格/·）并剥掉质量后缀（4k/uhd/fhd/hd/高清/超清/标清，可叠加）；
   多个 display-name 归一化冲突的键不建（宁缺毋滥，避免误匹配无关频道）
14、软解音频声道模式 — 播放器设置新增"声道"下拉（立体声 / 单声道合成）：
   分离声道源（如 L=对白 R=音乐）在手机端只能听到单边时，单声道合成 (L+R)/2
   让两边内容都能听到；对 MP2/AC-3/E-AC-3 软解生效，AAC 浏览器原生解码不受
   影响；选择 localStorage 持久化，运行时可随时切换
15、恢复软解源静音 AAC 假音轨 — 主 muxed TS 软解（MP2/AC-3/E-AC-3）时 MSE 附带
   一条按视频时间戳同步生成的静音 AAC 音轨，让 video 元素"有音轨"——后台标签页
   不再被判"无音频播放"而冻结，根治后台音画不同步（真实声音仍走 WebAudio 软解，
   静音轨仅用于维持 video 播放）
16、回前台音视频对齐重构 — 后台 free-run 期间音频实时推进、video 被 UA 节流时，
   回前台改为"视频追音频"：等待视频时钟确认恢复推进后把 video seek 到"正在听到
   的位置"，且保留音频链不清不重锚（消除切前台静音窗口、音频内容不重播、
   live-sync 不再 1.2x 加速追赶）；轴平移改用"即将播出的内容"（伸缩输出头→链尾
   内容游标→队头）精确测量，消除队头测量导致的过冲丢块
17、音频同步内核清理 — 删除无调用方的死代码（PCMAudioPlayer.stop、
   onStartupSyncFailed、AudioSyncCore.flushQueue/stopChain/lastQueuedEndSec/
   hasChain/AUDIO_SYNC_TAG、PlayerErrors.AUDIO_STARTUP_SYNC_FAILED），
   PLAYER-SYNC-REDESIGN 接口清单同步更新
18、音频软解统一为「demuxer 逐帧切分 + 逐帧 PTS 外推」— MP2 原先整段转发 PES payload
   （交给 WASM 侧 parser 切帧、用 PES PTS 打标签），现改为与 AC-3/E-AC-3 完全同模型：
   帧长取自 MPEG 帧头（覆盖 MPEG-1/2/2.5 × Layer I/II/III），跨 PES 的半帧由 demuxer
   carry 兜底，每帧带自己的 PTS 送 WASM；坏帧只跳该帧并照常推进 PTS，无 PTS 且无历史
   时报错丢弃（不再用 0 污染音频时间轴）；元数据/audio init 段改由首个完整帧派发。
   逐帧标签仍精确：WASM parser 的一帧延迟由 samplesBeforeInput 回退到帧起点。至此
   MP2/AC-3/E-AC-3 三条软解路径在 demuxer → WASM → PCM 时间轴上行为一致
19、修复源中断恢复后音频永久静音 — 上游分片 404/502 后视频 remuxer 会把源 PTS 跳变
   bridge 到连续输出时间轴，而软解 PCM 标签直映射源 PTS，两边不同源导致 PCM 领先视频
   数十秒 → 排程门压死 → queue=0 永久静音；音频轨不连续时把下一帧 PCM 重钉到当前播放头，
   之后按源 PTS 连续 bridging，与视频同轴（该"重钉"已在条目 23 改为**有界判定**）
20、回前台对齐 seek 后立即重建音频链 — 对齐 seek 处理延迟期间音频仍会继续超前，残留大
   drift 会被下一 controlTick 的漂移重锚清链（queue=0 静音）；改为 seek 到"正在听到的
   位置"后立即 trimFutureBeyond + reanchor，一次到位
21、直播缓冲领先上限 10s → 30s — 分片到达慢（整点 404/502 后每 10s 一片）时 10s 只够
   1 片，播放头追到缓冲末尾即卡；30s 可撑约 3 片，卡顿频率降约 3 倍（代价是内存与
   断流恢复后的追延迟时间）
22、网络切换/断流自愈（WiFi ↔ 4G/5G 不再永久错位、无声或卡转圈）— ①拉流层新增传输级
   自动重连：直播流中途断流按指数退避重连（0.5→8s、上限 5 次），成功后切一次 TS 输入
   边界（remuxer 把源 PTS 空洞 bridge 到连续输出时间轴），画面连续、音画不脱轴，无需
   重建会话；②新增 20s 无数据看门狗：网络切换时旧连接常静默挂死（系统级超时可达 30s+），
   主动掐断走同一重连路径；③4xx（除 429）不重试、VOD/回看不做字节流重连（会内容重复），
   仍由上层按位置重建
23、修复播放中"声音卡顿一下"（音画仍同步）— 原先音频轨不连续（TS 连续性计数器抖动、
   丢包、PES 重整、重连）时无条件重置 PCM 时间轴并把首帧钉到播放头，每次都丢弃当前已
   解码的 PCM 缓冲 → 可听卡顿；改为**有界判定**：先预测新 PCM 的落点，与播放头偏差在
   (−3s, +36s] 内保留时间轴、由内容连续 bridging 吸收（与 AC-3 周期 PTS 相位跳变同款），
   越界（超 30s 缓冲上限的轴分离，或落后播放头）才重钉；顺带修复视频轨不连续分支恒假
   导致软解音频解码器 carry 从不清零的问题
24、播放停摆兜底（MSE / native 两后端通用）— 可见且未暂停时播放时钟 12s 无推进：有前向
   缓冲且解码计数在动则补播一次，否则按直播边缘（回看按当前位置）重建会话；连续失败按
   30→60→120→180s 退避，弱网下不反复重建；保证"永不永久卡死"
25、音频链静音看门狗 — 曾出过声 + 5s 无可听内容 + 视频时钟确实在推进 → 按屏上帧时间
   强制重建排程链（后台 free-run / 回前台待对齐 / 暂停均不介入）；推进判据改用内核新增的
   **内容游标**（原用队头/链尾做标记，在自动推进时可能长时间不变，会把正常播放误判成
   停摆并强制重锚——重锚本身即一次可听卡顿）
26、新增软解音频丢弃**增量**诊断日志 — 每 ~60s 汇总各环节丢弃数（worker：remux/trim/
   溢出/gen/carry；player：过期/欠载/重锚/停摆），出现即打印 WARN 定位"卡顿"来自哪个
   环节；长期全零则排除软解 PCM 链路（问题在 MSE / bridging 侧）
```

### v3.2.1

```
1、修复 E-AC-3/AC-3 软解长播后音画不同步 — 部分源（如广东 4K E-AC-3）的复用器
   每到 ~10s 边界把音频 PTS 相位整体提前固定量（实测 256ms=8 帧、亦见 1024ms 等，
   常为 AC-3 帧长 32ms 的整数倍），而音频内容本身连续。旧代码把这类周期跳变当
   "不连续"触发 re-锚 + bridging，每 10s 把音频时间轴硬拧一次、周期性压缩，长播
   累积成"声音慢慢跑画面前面"。现把音频 PTS 重新锚定阈值由 100ms 提高到 10s，
   只对真正的切台/节目级不连续（>数秒）才重锚
2、修复中断恢复超时误报导致切台无声 — 重负载频道（1080i bwdif 软件反交错、
   MP2/AC-3 wasm 起播慢）切台后视频 4s 内未起播即被误判"时钟死亡"上报
   AudioResyncFailed，反复重试耗尽后无声；现区分"数据未到"（重新排队定时器
   继续等）与"时钟真死"（readyState 良好却无 timeupdate 才上报）
3、修复 phpgo date() 时区 — 原实现硬编码 UTC，未显式 date_default_timezone_set
   的脚本 date()/strtotime()/checkdate() 等全部按 UTC 输出（差 8 小时）。现按
   原生 PHP 优先级解析：显式 set（请求级隔离）> ini date.timezone > 系统本地
   时区；gmdate 恒 UTC
4、日志治理 — LiveSync 分片缺口自愈（静默常态操作）不再按警告级刷屏；AC-3
   软解 A/V drift 诊断由 10s 一条降为 60s 一条
```

### v3.2.0

```
1、AC-3/E-AC-3 杜比音频 WASM 软解设计 — 浏览器 MSE 不支持 ac-3 时不再无声：
   控制逻辑重做（不再把视频降速到 0.0625x 等音频就绪、视频 waiting/stalled 不再
   立即暂停 PCM、改宽限期调度；PCM 缓冲加大；LiveSync 加追赶迟滞防 rate 1↔1.2
   抖动）；修复 DVB 私有 PES 音频描述符识别（ETSI 0x6A→AC3、0x7D→EAC3，移除
   误映射的 0x7A 实为 DTS）——上海联通组 4K（江苏/北京/东方卫视4K 等）此前
   有画面没声音；ac3 wasm 复用工作缓冲消除每包 malloc/free
2、静态资源预压缩 gzip — wasm 传输 520KB→218KB（约 -57%），/pp/assets 与
   /web/assets 统一生效，弱网电视盒加载提速
3、配置备份系统重构 — 各配置编辑接口保存前统一走内容比对公共函数（内容未变/
   与最近快照同态不重复备份，不做全量历史扫描）；备份文件名精确到毫秒防同秒
   互相覆盖；配置备份管理新增「手动备份」与「一键清理」（按文件保留最近 N 份，
   默认 5，可手动输入）
4、配置保存后立即生效 — 全部配置编辑接口写文件后立即重载内存配置（不等
   fsnotify 5s 防抖），GET 立即返回新值；YAML 编辑器保存后自动重新读取落盘
   内容，去掉「点击重新加载生效」手动步骤，消除误以为未保存成功而重复编辑
5、Code 文件编辑大文件优化 — textarea 改非受控（打字不再逐键把整串回写 DOM）
   + 大文件（>1MB）wrap=off（规避软换行全量重排）：tv.txt 级大文件中文输入法
   逐字输入不再卡顿
6、player 拉取挂起防护 — 订阅/EPG/模板 EPG 拉取绑定请求上下文并加 30s 总超时：
   源在响应头后半挂不再无限阻塞（此前会永久卡死订阅刷新循环/EPG 刷新/请求
   goroutine，热加载失效）
```

### v3.1.1

```
1、修复解析型源 403/垃圾内容 — Go 跟随重定向会自动把上一跳 URL 设为 Referer，
   防盗链 CDN（腾讯云直播等）见外站 Referer 直接 403；播放器与代理拉流客户端
   跟随重定向时剥离 Referer。上游响应可用性判定（非 2xx 或内容非 m3u8/TS/
   MP4/FLV）失败自动清解析缓存重新解析，最多 3 次，对付解析脚本随机死链
2、transport 强制 ForceAttemptHTTP2 — 自定义 DialContext/TLSClientConfig 会关闭
   Go 自动 h2，ALPN 仅 http/1.1 的 TLS 指纹易被 CDN 风控拦截
3、订阅 ua= 作用域修正 — 只作用于所在分组，组边界（#genre#）重置；修复文件头
   ua= 跨组泄漏导致未配置分组被动继承、修改全局 player.ua 不生效的问题；
   未配置 UA 的分组回落全局 player.ua；频道行尾 ,ua= 优先级不变
4、player 配置热加载即时生效 — 改 ua/update_interval/订阅源立即重载订阅并重置
   刷新计时（此前要等当前周期计时器到期，最长延迟一个周期）；
   /api/player/channels、/api/player/epg 补 Cache-Control: no-cache
5、播放器深链标识改为组名+频道名编码（12 位 hex，不暴露明文），比频道 key 更
   稳定（换源不改链接）；历史链接（明文名字/频道 key）全部兼容
6、配置页按钮统一"重新加载"+ 加载状态（转圈/禁用防连点），定时任务页残留
   "重置"一并处理
7、Android/Windows 版本升级入口隐藏、YAML 编辑器查找条等前端体验修复
8、Docker 构建修复 — UI 阶段固定构建平台（node:20-alpine 无 riscv64 manifest）、
   ui 产物路径修正
```

### v3.1.0

```
1、新增 H5 播放器模块 — 服务端解析 IPTV 订阅（M3U / 逗号TXT，支持多文件目录合并加载），
   频道生成不透明 key（源地址哈希）对外发布，订阅即白名单（非白名单 key 返回 403），
   真实源地址与抓流 UA 全程不出服务器；受控拉流 /player/<key>，分片走 /player/<key>/<token> 短路径
2、播放页双入口 — 管理后台 /web/player 与独立入口 /pp（不跳转后台路径，避免 Location 头
   泄露隐藏的 web.path）；/pp/<key> 转为 /pp#<key> 深链（透传 my_token）；
   仪表盘活跃连接表中的直播地址可点击，新标签直接观看
3、自研 playback-engine — MSE + wasm 转封装随 SPA 构建；三级智能播放策略 + 后台持续播放保活；
   修复 Chrome/Edge 后台标签断流；直播/回看徽标按播放模式即时切换；遥控器（方向键/ChannelUp/
   Down/数字换台）与触摸操作，分组栏可折叠；EXT-X-MEDIA 分离音轨播放，音画同步（A/V drift ~6ms）；
   直播分片加载失败（上游 CDN 短窗口驱逐分片 404 / 瞬时 EOF）自动跳过并刷新列表追直播边缘，
   连续 8 次失败才报错（点播/回看保持立即报错）；移动 OTT 源回看 PLTV 路径替换为 TVOD
4、频道源支持 php:// — docroot 脚本由内嵌 phpgo 解释器内部执行（不走 HTTP 回环、不依赖 IP、
   不经鉴权），302 Location 解析出的真实源自动接入代理拉流链路，m3u8 输出重写为受控短地址
5、播放器 http/https 上游接入代理组 — 与原生 /https:// 转发同链路（规则匹配 → 节点选择 → 健康
   标记）；频道级代理组亲和记忆（IPTV 分片 CDN 常为 IP 主机，域名规则匹配不到）；301/302 跳转
   回写域名→IP 映射，后续 IP 分片命中同组
6、修复重定向链两处 bug — cache/redirect.go 链记录条件写反（跳过新 IP、追加重复）且无长度上限
   （补 maxChainLen=32）；domainmap doWithRedirect 重建请求丢失原始 UA/Referer（改为 Clone/恢复）；
   移除 httpclient NewHTTPClient 中 ErrUseLastResponse 下的无效 req.URL 重写
7、新增定时任务模块 — 标准 5 段 cron 调度执行命令（支持 */n 步长），command 经系统 shell 执行，
   或 php://xxx.php 由内嵌 phpgo 执行（安卓无原生 php 环境可用，GET 语义注入 $_GET，脚本输出为
   任务输出，脚本缺失/HTTP≥400 判失败）；Web 可视化配置（每分钟/每小时/每天/每周/每月，cron
   回显解析）、立即执行、状态展示（上次结果/耗时/输出摘要/下次时间）
8、Web 管理后台全量迁移 React SPA（单二进制嵌入，双入口构建）— 登录页迁移（公开访问零鉴权
   请求、去品牌特征、noindex）、监控/仪表盘迁入并清理旧 /status；仪表盘重构（顶部 6 卡 + 资源/
   网络/应用三卡 + 启动时间，CPU 温度 -0.9 修复：哨兵值 -1 经 round1 截断变形，改 tempOrNull
   显示"不支持"）；代理组节点状态展示、编辑布局修复（grid 防塌缩）、协议仅保留 http/https/
   socks5/socks4、新增组置顶插入；定时任务卡片状态增强、编辑行防轮询覆盖
9、新增二次授权（elevated session）— 配置查看/保存、备份下载/恢复需重输登录密码；独立短 TTL
   Cookie（10 分钟，HttpOnly+SameSite=Strict），常量时间比较；403 由前端弹窗引导解锁；
   ApiError 统一使 YAML/备份模块 403 正确触发弹窗
10、代码文件管理重构为左右分栏（文件树 + 浏览/编辑双模式），恢复语法检测（.php 工具栏按钮）、
    目录递归批量替换、查找替换弹窗、zip 解压、代码文件↔备份中心互跳；修复 GBK 残留文件名重复
    显示（列表跳过非 UTF-8 名，上传/解压统一 normalizeFilename → UTF-8 落盘）
11、「GitHub 升级」更名「GitHub 加速配置」— 同一份 config.github 双用途（仓库同步拉取 + 版本
    升级加速）
12、phpgo 兼容性增强 — 支持 UTF-8 标识符 / or-and-xor 运算符 / 命名空间；修复带键追加赋值
    $arr[$key][] = $val 丢失前置下标
13、Makefile 构建依赖修复 — 前端源码（ui/src）与 Go 源码变化自动触发 dist 重建/二进制重编；
    此前二进制目标无依赖，改代码后 make 判定"无需重建"，部署到过期产物
14、修复组播配置页渲染崩溃 — 配置接口数组字段 null 兜底
15、文档 — README 补充 H5 播放器（订阅地址形式/订阅格式规范/频道源协议/访问入口）、定时任务、
    Web 管理后台与二次授权章节；新增 README 订阅示例与解析器一致性守卫测试
16、播放器界面风格与面板透明度 — 新增深海/翡翠/落日三套配色（CSS hue-rotate 实现，播放画面/台标
    反向抵消保持原色）；节目单/侧栏/分组栏面板透明度 4 档（不透明/85%/70%/55%），非默认档启用
    Win11 亚克力毛玻璃（backdrop-filter），面板光晕随主题配色
17、直播卡顿修复 — MSE 缓冲发丝缺口（DTS 不连续 0.06-0.3s 空洞）gap-heal 自动跳过（≤1.5s + 8 次
    重试）；播放列表仅剩 1 分片时后台预取补充、缓冲饥饿 500ms 立即刷新，消除周期性缓冲耗尽
18、解析型源 302 缓存 — php:// 等解析脚本最终拉流地址缓存 30 分钟，m3u8 刷新不再重复执行解析脚本，
    源失效自动刷新
19、解析缓存改活跃会话语义 — 连续轮询间隔（45s 内）复用，换台/回看/返回直播必然重新请求上游；
    回看（playseek）会话不写直播缓存，杜绝返回直播误播回看片段
20、php/rtsp 直连源支持回看 — 回看 token 拉流按协议分派；phpgo 内置 DateTime 类，修复回看脚本
    时移参数为空
21、频道分组选择记忆 — 上次分组 localStorage 持久化，重开自动恢复（分组不存在回退全部）
22、YAML 编辑器修复 — 修复容器高度塌陷（绝对定位无高度压缩至 1px，固定 70vh）；内置查找条
    （Ctrl+F）、查找体验修复、点击遮罩不误关弹窗
23、配置页交互优化 — 保存/重置按钮统一移至标题行右上角、代理组编辑卡片增加保存按钮；
    github/sync/publisher 接口数组字段 null 兜底；卡片删除统一确认弹窗；播放器配置页展示外链与
    一键复制；后台补常驻直播入口
24、player 节点新增 android_autoplay 标记位 — 安卓客户端启动是否进入播放页（纯标记位，
    未配置默认不进入、显式 true 才自动进入；客户端 App 读取自行控制，服务端不做行为控制）
25、推流发布（publisher）增强 — 播放器支持同源 FLV 直播直连与发布页播放入口；主备切换稳定性修复；
    HLS 每日归档 MP4 + 监控录制模板，归档间隔/MP4 保留期可配置；本地播放地址跟随 Publisher Path；
    本地 HLS 回放时间段选择，主动关闭推流不再报错
26、Android/Windows 平台隐藏版本升级入口 — 前端 UA + 后端平台双判断
27、低版本安卓电视/盒子黑屏兼容 — MSE 不支持时回退 native 播放后端（<video src> 直播 HLS/HTTP
    流），native 起播 8s 超时检测（无法解码/格式不支持给错误而非无限加载）；构建转译 es2017 +
    入口 polyfill（globalThis/AbortController/findLast/ResizeObserver 等）；ES Module 不支持的
    WebView（Chromium 57-60）自动加载 SystemJS legacy 降级包（Babel + core-js 补到 chrome 49），
    根治页面全黑
```

### v3.0.10

```
1、DNS 解析统一兜底链 — 配置 dns.servers → 系统解析 → 内置公共DNS(223.5.5.5/119.29.29.29)；
   gethostbyname/gethostbynamel/dns_get_record/curl/各 dialer 全部一致；配置的 DNS 强制优先，失败才回落系统
2、安卓系统解析走本地 DNS — systemResolver 移除 PreferGo，CGO 链接下经 getaddrinfo→netd 取设备本地 DNS，
   公网/内网域名无需再注入
3、curl 暴露 CURLINFO_PRIMARY_IP — 回显实际连接对端 IP，便于诊断
4、清理每查询 [dns] 调试日志 — 避免多次解析疯狂刷屏（保留配置错误级 WARNING）
5、修复 TTL 查询空结果直接返回 — RcodeSuccess 但无匹配记录（如 CNAME 别名）时继续尝试下一个 NS
6、phpgo 新增函数 — sys_get_temp_dir + bcmath(bcadd/bcsub/bcmul/bcdiv/bcmod/bcpow/bcpowmod/bccomp，
   math/big 实现、默认 scale=0)，供 wxty 等直播解析脚本 RSA 解密
7、修复 phpgo 双引号字符串转义 — 补 \xHH/八进制\0..\777/\v/\f/\e；此前 "\x02" 被解析成 4 字符，
   使 rsaAsn1Integers 扫不出 N/E（返回0），直播解析脚本报"获取直播地址失败"
8、代码文件管理：二进制文件点击不打开编辑器（避免读入大二进制卡顿），仅选中并提示可下载/删除；
   @media 480px 移动端适配
9、代码管理中 ZIP 上传自动解压 — xxx.zip + 配套 xxx.zip.md5（MD5 一致即自动解压，覆盖模式）+
   手动解压接口，文档同步
10、README 补备份机制章节与 ZIP 解压说明；新增 DNS 路径 / phpgo bcmath/RSA 单元测试
```

### v3.0.9

```
1、修复代码文件管理 / 备份中心中文文件名乱码 — decodeFilename 原先先尝试 GBK 解码，UTF-8 中文文件名按 GBK 解码后也含 CJK 字符，导致从仓库同步的 UTF-8 中文文件名（如"可可影院.json"）显示为乱码；改为优先判定原始字节为合法 UTF-8 时直接返回，仅非 UTF-8 字节才走 GBK/GB18030 转码（兼容旧 Windows 上传的 GBK 文件名）
```

### v3.0.8

```
1、新增仓库同步模块（sync）— 将 GitHub/GitLab 仓库内容单向同步到本地 docroot 子目录；支持多仓库（sync 为条目列表，每项独立同步循环/独立 manifest）；基于 git blob sha 增量对比；.bak.<时间戳> 备份；protect 保护清单（设备私有文件永不覆盖/永不删除）；孤立文件（本地有远端无）日志报告；Web 新增"仓库同步配置"编辑器（多仓库添加/删除）
2、整仓归档同步 — 公开仓库走 codeload 直连下载（不占 api.github.com 未认证 60 次/小时限额）+ 本地计算 git blob sha 对比；首次同步或增量树 API 限流时自动降级整仓归档，避免大仓库逐文件拉取触发 429/403；归档下载使用独立 10 分钟超时
3、修复配置热加载 php docroot 失效 — 热加载时 php.Init 先于 SetDefaults 执行，相对 docroot 拿到未解析路径导致脚本 404；调整顺序后相对/绝对 docroot 及 php.path 修改均即时生效
4、修复 Web 配置保存 YAML 标签错误 — 手工构造 YAML 节点未设 Tag，repo 等字符串被序列化成 `!!null xxx` 导致配置重新加载失败；显式设置 !!str/!!bool/!!int/!!seq/!!map
5、凭据显示安全 — sync GitHub token 后端掩码返回（保存后不可回显，掩码占位保存保留原值、填新值才覆盖）；global_auth 密钥/token 默认打码 + 点击眼睛按钮按需显示
```

### v3.0.7

```
1、修复 PHP 链接校验短超时误判 — phpgo HTTP 栈比原生 PHP 慢，0.1s 短超时会把好链接误判为失效导致缓存被反复清掉；get_http_response_code 改为"0.1s 快速校验 + 加几秒兜底重试"，校验超时按"无法判断"处理
2、PHP 缓存校验逻辑增强 — 链接校验超时（拿不到明确状态码）不再清缓存、不覆盖，用旧缓存兜底；只有明确非 200 才重新生成
3、文档完善 — phpgo 实际实现函数全量清单（300+ 函数/12 别名/no-op 标注）、README 补充函数覆盖与超时注意事项
```

### v3.0.6

```
1、修复 PHP 时区 — 内嵌 time/tzdata，安卓/精简镜像无系统时区也能正确 LoadLocation，避免 UTC 时间错乱
2、修复 PHP 中文乱码 — 脚本未声明 charset 时自动补 ;charset=UTF-8，已声明的 gbk 等保持不变
3、移除 PHP 无引用死函数（phpDate/phpGmDate）
4、Web 代码编辑器修复与增强 — 补回丢失的代码注释按钮；注释快捷键支持 Ctrl+Q（兼容 Ctrl+/）
5、代理转发路径精简 — 移除 executeCopyWithPool 的 goroutine/通道/任务池开销；删除 proxy/client 死代码
6、普通响应保留 Content-Length 并放开 HTTP/1.1 keep-alive — 消除 chunked 开销，支持连接复用
7、无 Content-Length 的普通响应改走连接关闭帧 — 修复部分播放器"普通页面一直不返回"（chunked 兼容问题）
8、统一 RTSP 写出路径 — 抽离 writeRTSPToClient 批量写 helper，H.264/AAC 也吃到 TLS 批量写优化
9、TS 缓存从首个关键帧起缓存并前置 PAT/PMT — 修复首客户端花屏/关键帧问题，缓存不含 P 帧垃圾前缀
10、放宽 HTTP 连接数默认值 — MaxIdleConns/MaxIdleConnsPerHost/MaxConnsPerHost/IdleConnTimeout 提升，修复同一源站并发第 N 路流断开重连
11、流媒体(.ts/.flv)免疫 http.timeout 整体超时 — 修复长连接被超时掐断反复重连；普通响应超时仍生效
12、Web 代码编辑器新增文件/目录重命名 — 区分文件与目录重命名按钮，避免与打开文件重命名混淆
13、Web 代码编辑器新增持久化批量替换 — 纯前端编排递归处理目录文本文件，跳过 .bak/隐藏/超大(>5MB)/二进制，每文件读改存并自动备份
14、Web 代码编辑器查找/替换弹窗化 — 小白友好弹窗(区分大小写/正则/计数/上一个下一个/替换/全部替换)，快捷键 Ctrl+F/Ctrl+H/F3；记忆上次输入并优先带入选中文本，Esc 关闭
15、多文件上传增强 — 单文件失败不再中断整体，响应返回 uploaded/failed 明细，前端显示"X成功+Y失败"
16、EnsureConfigFile 支持 ~ / ~/ 家目录展开 — 修复配置文件备份等全局路径在安卓/移动端(如 Termux)的路径问题
17、tzdata 内嵌策略调整 — 仅安卓内嵌 time/tzdata，其余平台用系统时区，减小非安卓二进制体积
18、安卓日志输出本地时区 — 读取 persist.sys.timezone 设置 time.Local，服务日志时间与设备本地一致
19、安卓/服务器平台隐藏版本升级卡片 — 前端 UA + 后端平台双判断，修正浏览器 UA 盲区
20、保持 /debug/pprof 调试接口禁用 — 生产环境不开放（加回后回退）
21、修复 PHP docroot 尾斜杠误判越权 — filepath.Clean 归一化，/apps/www 与 /apps/www/ 两种写法均正常（此前带尾斜杠会触发 403/非法路径）
22、修复 Docker buildx 多平台构建 — final 镜像 debian:bookworm-slim 换 alpine（manifest 解析失败）
23、PHP 新增 DNS 系列函数 — gethostbyname/gethostbynamel/dns_get_record（A/AAAA 位掩码、真实 TTL，复用项目 DNS 解析器，支持 dnscrypt/DoH 配置）
24、curl 新增 CURLOPT_IPRESOLVE 支持 — 指定 IPv4/IPv6 解析（复用项目 DNS 解析器按族拨号）
25、真正实现 curl_multi 并发 — 多句柄 goroutine 并行拉取，标准 do/while 循环一次完成，$still_running 引用回写，getcontent/getinfo 正常读取
26、dns_get_record TTL 查询优先使用 YAML 配置的 dns.servers — 安卓/内网可指向内网 DNS，外部公共 DNS 仅作最后兜底，避免内网域名解析失败
27、修复 usleep 为真正睡眠 — 此前为 no-op 直接返回，现按微秒挂起当前请求（time.Sleep 按 goroutine 阻塞，不影响其他并发请求）
28、PHP 内置函数大规模补全 — 新增约 70 个常用函数（详见下方分组）：
   - 字符串：strcmp/strcasecmp/strncmp/strncasecmp/strnatcmp/strnatcasecmp、strrev/str_shuffle/str_rot13、substr_count/substr_replace、strip_tags/str_word_count、printf/vprintf/vsprintf、htmlentities/html_entity_decode/utf8_decode
   - 数组：array_diff/array_intersect/array_diff_key/array_intersect_key/array_merge_recursive、array_chunk/array_splice/range/shuffle/array_fill_keys/array_pad/array_count_values/array_product/array_reduce、array_key_first/array_key_last
   - 排序：arsort/krsort/usort/uasort/uksort；并修复既有 sort/asort/ksort/rsort 无法修改原数组的问题（补充按引用传参）
   - 类型/调用：is_object/is_scalar/is_iterable/is_countable/is_callable/is_resource/settype/var_export/get_debug_type、call_user_func/call_user_func_array/function_exists/defined/constant/extract
   - 文件：rename/copy/touch/readfile/fseek/ftell/rewind/fileatime/filectime
   - HTTP：get_headers（ProxyResult 增加响应头字段）
   - 数学/日期/正则：exp/log/log10/log2/log1p/fmod/deg2rad/rad2deg/三角函数/decoct/octdec/srand/is_finite/is_infinite/is_nan、mktime/gmmktime/checkdate/getdate/gettimeofday、preg_quote/preg_grep
29、修复 json_encode 关联数组键序 — 重写为自定义保序编码器，按 PHP 插入顺序输出对象键（此前经 Go map 编码被按键排序打乱），删除废弃的 phpToGo；连续数字键仍输出 JSON 数组，JSON_UNESCAPED_UNICODE/JSON_PRETTY_PRINT 等 flags 行为不变
```

### v3.0.5

```
1、修复 bufio 缓冲层数据乱序问题 — FLV 头部改用 sendToClientViaWriter 通过 bufio 写入，避免绕过缓冲层
2、修复 bufio 缓冲层数据丢失与积压问题 — ctx.Done() 路径补 bw.Flush 避免丢末尾数据；flusher=nil 时仍刷 bw 避免积压
3、修复 ts_cache waitCh 关闭后空转问题 — 刷出剩余数据后退出，避免 busy-loop
4、修复 udp_rtp hub 关闭时数据未 flush 问题 — h.ctx.Done() 路径补 flush
5、修复 mpegts flusher=nil 时 panic — 补 nil 检查
6、修复 web 登录会话 1 小时失效问题 — validateAuthCookie 时间戳校验与 Cookie MaxAge 30 天一致
7、Makefile 支持按平台单独编译 — make linux-64 / make windows-64 等，新增 make list
8、注释掉 pprof 调试接口 — 生产环境禁用 /debug/pprof
9、README 添加 Linux 内核优化建议和 CPU 性能模式章节
10、Android 平台获取不到系统信息时用 runtime.GOOS/GOARCH 兜底
11、添加下载脚本 — download-apk.sh / download-release.sh，支持依赖检查
12、更新依赖 — golang.org/x/net 0.58.0, go-astits 1.16.0, gortsplib 5.6.3, quic-go 0.61.0, pion/rtp 1.10.4 等
```

## v3.0.0

```
fix(lb): Interval 参数生效，过期测速缓存不再复用
- fastest/round-robin 选择缓存代理时增加 LastCheck > Interval 过滤
- 超过 Interval 自动触发原有重测流程，避免一直使用过期测速结果
```

## v2.1.20

```
1、全面性能优化锁优化。
2、组播、TS 缓存，独立节点配置。
3、代理页面编辑优化。
4、登陆有效时间改成30天。
```

## v2.1.19

```
1、修复rtsp崩溃问题。
2、添加前端查看时时日志功能。
```

## v2.1.18

```
1、优化ts缓存修复图像卡顿问题。
2、修复web 编辑代理组返回主页后端数据覆盖编辑数据问题。
3、更新了一些依赖。
```

## v2.1.17

```
修复ts开启缓存 卡死问题。
```

## v2.1.16

```
1、小设备内存增大崩溃修复。
2、备份配置文件批量删除。
3、升级一下依赖。
```

## v2.1.15

```
组播优化。
```

## v2.1.14

```
1、修复组播转发内存暴涨，cpu 占用率过高等。
2、修复centos7二进制升级，启动报端口被占用。
```

## v2.1.13

```
1、RTSP 优化。
2、组播优化。
3、更新依赖。
```

## v2.1.12

```
1、fcc 优化。
2、依赖更新。
```

## v2.1.11

```
使用fcc 不正常释放bug 修复。
```

## v2.1.10

```
1、ts 缓存添加开关支持。小设备建议关闭缓存。默认关闭缓存。
2、fcc 优化。
```

## v2.1.9

```
1、fcc 优化。
2、hls 转发优化，增加ts 文件缓存 web 界面可配置。
3、删除web页面特征码。
4、更新一下依赖。
5、代理 dns 解析遵循ipv6 开关设置。
```

## v2.1.8

```
1、修复转发html页面时打开一直加载问题。
2、修复域名映射只能单客户端播放bug。
```
