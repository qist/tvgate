# Changelog

---

## Android (tvgate-android)

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
