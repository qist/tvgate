# Changelog

---

## Android (tvgate-android)

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
