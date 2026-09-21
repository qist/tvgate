# TVBox 兼容与全平台客户端设计（tvbox-universal-client）

> 状态：设计基准（未实施）。本文档是后续 TVBox 接口兼容与原生客户端开发的**协议与架构基准**，实施时以本文为准；如实现过程中决策变更，先改本文再动代码。

## 一、目标与定位

- **tvgate 不只是服务端，而是可打包进端内的"核心驱动框架"（Go 运行时）**：负责源解析、地址归一化、播放服务；端上只留 UI 壳 + 解码器两层。
- **做一个全平台 TVBox 替代品**：Android / Android TV / Windows / Linux / macOS / iOS，同一份 `jsm.json` 配置通吃。
- **现有 H5 保留不动**：继续作为浏览器入口对外提供（`/api/player/*` v2 契约不变），原生客户端是同一服务端的第一个"正经 API 客户端"。
- 生态原则：**不另发明配置格式**。用户自有 `qist/tvbox/jsm.json` 即统一配置协议，TVBox/FongMi/饭太硬等现有配置尽量直接可用。

## 二、协议原则（已确认的设计决策）

1. **JSON 是协议，平台只是实现**：配置中禁止出现平台分支字段（不搞 `android.jar` / `ios.jar` 之类），平台差异全部收敛在 Runtime 层。
2. **`sites[].jar` 必须保留**：site 级 jar 与全局 `spider` 可混用，加载优先级 **`site.jar > 顶层 spider`**。
3. **Spider 统一七方法 ABI**：`init / home / category / detail / search / play / proxy`，各执行体（js/py/jar）输出统一 JSON 语义（TVBox Result 结构）。
4. **Spider Runtime 独立化**：jar 执行环境以本地 JSON-RPC/HTTP 服务形态存在（参考 `fongmi-spider` 挂 `127.0.0.1` 的思路），与核心解耦，可独立运行。
5. **`playerType` 保持现有语义透传**（EXO/IJK/MPV 切换沿用 TVBox 惯例），不重新定义字段。
6. **JSON 只描述**：URL / Headers / Cookie / Referer / UA / DRM / 直播或点播；**不描述**播放引擎与解码器——引擎选择是端上 Runtime 的事。
7. **安全不变量**（沿袭 H5 既有红线）：源地址与抓流 UA 只存在于核心内部；对外只发不透明 key（`/player/<key>`）；TVBox 导出配置同样带 token，不暴露真实源 URL。

## 三、架构总览

```
┌────────────────── UI 壳（每端薄壳）──────────────────────────────┐
│   Android/TV: Compose · 桌面: Compose Desktop · iOS: Compose     │
│   频道单 / 点播分类 / EPG / 搜索 / 播放控制 / 遥控焦点              │
├──────────────────── 解码器适配层（按平台注入）─────────────────────┤
│   Android/TV: ExoPlayer ▸ ijkplayer        Win/Linux/mac: libmpv │
│   iOS: AVPlayer（可选嵌 mpv）                全平台兜底: libVLC    │
│        ▲ 只吃核心给出的本机归一化地址（127.0.0.1/player/<key>）     │
├──────────────────────────────────────────────────────────────────┤
│        tvgate Core（Go，服务器版与端内版同一份代码）                │
│  ├── 直播：lives → 订阅管线（M3U/TXT 解析、聚合、白名单、EPG）✓已有  │
│  ├── 点播：sites → VOD 模块（新建）                                │
│  │          └── SpiderHost 七方法 ABI（新建）                      │
│  │               ├── php: phpgo（✓已有，php:// 频道源同链路；       │
│  │               │        纯 Go 随核心全端生效，作 ABI 参考实现）    │
│  │               ├── js:  goja（纯 Go）→ 必要时 CGO QuickJS         │
│  │               ├── py:  CGO CPython 嵌入                         │
│  │               └── jar: 见 §五（按平台三路）                      │
│  ├── 播放服务：/player/<key> 归一化 HLS/FLV、catchup、m3u8 重写 ✓   │
│  └── 声明式源：type=4 VOD(苹果CMS) / XBPQ / XYQHiker 规则引擎（新建）│
└──────────────────────────────────────────────────────────────────┘
```

UI ↔ 核心通信走**本地 HTTP**：端内核心监听 `127.0.0.1` 随机端口 + token，UI 直接消费 `/api/player/*` v2 同构接口；gomobile 仅暴露 `Start/Stop/Config` 等生命周期方法。浏览器可直接访问端内 API 便于调试。

## 四、兼容性矩阵（输入侧 / 输出侧两个维度）

| 执行体 | 输入侧（核心自己执行，解析成频道/点播） | 输出侧（导出给 TVBox 族客户端） |
|---|---|---|
| JSON 声明式（type=4 VOD / 内联 lives / txt / XBPQ / XYQHiker） | ✅ 纯协议解析，Go 原生 | ✅ 透传 |
| PHP 源（phpgo 承接） | ✅ **已有**：phpgo 纯 Go 解释器随核心分发，五端零成本（`php://` 频道源同链路） | ✅ 透传 |
| JS spider（drpy/drpy2） | ✅ goja 嵌入核心，一次实现全端生效 | ✅ 透传（客户端自带运行时） |
| PY spider（dr_py/hipy） | ✅ CGO CPython；协议与 JS 同构 | ✅ 透传 |
| JAR（dex 字节码，csp_XXX） | △ 三路执行见 §五；iOS 走服务器桥 | ✅ 透传（客户端原生执行） |

**spider 只服务点播；直播 lives 全是纯 URL 协议**——直播兼容不依赖任何解释器。

## 五、JAR 三路执行设计

实测依据（`spider.jar` 116 个 `com.github.catvod.spider.*` 类）：spider 逻辑层依赖 okhttp/jsoup/gson/protobuf/org.json + `javax.crypto`，Android 触点仅薄层工具类（`android.util.Base64/Log/Pair`、`android.text.TextUtils`、`android.net.Uri`、`android.webkit.CookieManager`、接口签名所需的 `android.content.Context`）。

| 平台 | 路径 | 说明 |
|---|---|---|
| Android / TV | **宿主 ART 委托** | 核心经 gomobile 回调壳层，`DexClassLoader`/`InMemoryDexClassLoader` 加载 jar，反射实例化 `csp_XXX` 回灌核心——真 ART 自带全部 android.* 类，**零 shim 成本** |
| Windows / Linux / macOS | **JVM sidecar** | 独立小 JVM 进程（我们自写的桥，行为级实现）：d2j（dex→class）转换缓存 → `URLClassLoader` → 反射七方法 → 本地 JSON-RPC 回灌；需补 Android 桩库（Base64/TextUtils/Uri/Log/Pair/Context/CookieManager，每个 <100 行，按 API 文档实现桩行为）；JRE 用 jlink 精简（~40MB）随包 |
| iOS | **服务器 jarhost 桥** | iOS 无 JVM 且禁止动态代码加载；jar 走远程 tvgate 服务器的 sidecar 桥执行，端上只拿结果。备选评估：jvm.go（纯 Go JVM 解释器，教学级，仅可能跑通"纯 java.net+org.json"的简单源，okhttp/jsoup 满配源预期不通——待 spike 验证后决定是否作为轻量档） |

残余无解项（识别 → 降级提示）：依赖 WebView 过盾的源；quickjs 内嵌 JS 的 jar 源（`com.whl.quickjs` 需额外提供 libquickjs.so，后补）。

信任模型：`php://` 已在核心内执行用户脚本，jar/py/js 同级信任，不开新口子。

## 六、播放器适配层

| 平台 | 首选 | 备选 | 备注 |
|---|---|---|---|
| Android 手机/TV | ExoPlayer（Media3） | ijkplayer | HEVC 硬解；D-pad 焦点 |
| Windows / Linux / mac | libmpv | libVLC | FFmpeg 内核，4K HEVC/AC3 无忧 |
| iOS | AVPlayer | 嵌 mpv | AVPlayer 仅吃 HLS——核心归一化正好补齐 |
| 任意（容错） | libVLC | — | 组播/UDP 裸流最强 |

统一策略：
- 所有引擎优先吃核心清水 HLS（公约数）；FLV 低延迟仅 ExoPlayer/mpv 开启。
- catchup `from/to`（Unix 秒）→ 各引擎 seek 语义做一次薄适配。
- 独立模式可旁路直连原始流（mpv/VLC 直吃 ts/flv，性能最优；端内本来不设防）。

## 七、端内打包与运行形态

- **打包**：Android gomobile `.aar`（CGO 链路已验证，`CGO_ENABLED=1` 走 cgo DNS）；桌面 `.dll/.dylib/.so` + 壳；iOS xcframework（受限 CGO 子集，注意 CGO 禁令只针对纯 Go 构建的旧约束，iOS 打包单独处理）。
- **单机模式**：核心端内独立运行，自带订阅，全链路 127.0.0.1——"随身 TVBox"。
- **联网模式**：端内核心连远程 tvgate 服务器（订阅/白名单同步），播放走远程 `/player/<key>`。
- 两种模式核心代码同一份，仅配置来源不同。

## 八、分期路线

| 阶段 | 内容 | 备注 |
|---|---|---|
| A | `jsm.json` 输入：sites/lives 解析，lives 直接进现有订阅管线；Web 端先验证 | 协议解析为主，无解释器 |
| B | VOD 模块：type=4/声明式规则引擎（XBPQ/XYQHiker），分类/详情/搜索/播放 | 纯 Go |
| C | SpiderHost：**先以 phpgo 为参考实现打通七方法 ABI（php 解释器已有）**→ goja(js) → jar（Android ART 委托 + 桌面 sidecar）→ py(CPython)；d2j 转换器与 Android 桩库 | 唯一硬骨头，php 路径零新增 |
| D | Android Compose 壳 + gomobile 打包 + ExoPlayer 适配（M1 闭环：连服务器播 `/player/<key>`） | 先联网模式后单机 |
| E | 桌面 libmpv → iOS AVPlayer（jar 走服务器桥）；TV 焦点与遥控细节 | |
| F | TVBox 导出端点（`/tvbox/config.json`，供给 TVBox 族客户端，反向兼容） | 与 A 无依赖，可穿插 |

## 九、风险与已验证事实

- ✅ 已实测：`spider.jar`（116 类）Android 引用集中在壳工程，spider 逻辑层为纯 Java 依赖——端上 ART 委托可行。
- ✅ 已实测：外部台标 CDN 命中率、H5 引擎 HEVC/AC-3 教训（ts-demuxer layout.video 需认 0x1b/0x24；DVB 私有流音频靠 ES descriptor 识别）——原生端用系统解码器可规避 H5 软解坑。
- ⚠️ jvm.go 跑满配 jar 源预期不通（okhttp 反射/invokedynamic/动态代理），仅作 iOS 轻量档 spike 备选。
- ⚠️ iOS：无 JIT、禁动态加载字节码；AVPlayer 格式面窄。jar 与格式兼容均依赖核心归一化兜底。
- ⚠️ 硬约束：严禁从 GPL 上游（rtp2httpd / FongMi 等）逐行移植代码；jar 桥/桩库一律行为级实现（读行为 → 脱离源码重写）。

## 十、验收基准

- 用户自有 `qist/tvbox/jsm.json` 为第一个端到端用例：导入 → lives 频道即播 → `spider.jar` 的 sites 可分类/搜索/播放。
- 同一份 `jsm.json` 在 Android 壳与 H5 后台表现出一致的频道/EPG/回看语义。
- 全程抓包：真实源 URL 仅出现在核心日志与进程内存，UI 层与网络对面只见不透明地址。
