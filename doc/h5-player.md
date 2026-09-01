# TVGate H5 播放器 · 设计文档

> 状态：**待评审**
> 日期：2026-09-01
> 适用范围：`ui/`（现有 SPA 前端）下新增的电视直播 / 点播（JX/HLS）播放视图，以及对应的服务端路由与频道清单来源；**不**改后端代理/组播/推流/解析核心逻辑

---

## 0. 结论先行（TL;DR）

| 项 | 结论 |
|---|---|
| **核心诉求** | 把**直播**并进 Web 播放器：既能播**组播/RTSP 直播**，也能播**远程 HTTP(S) 直播（远程 http 直连优先、全部经受控 `player/<key>` 兜底）**，并渲染 EPG 节目单。**本期只做直播**，JX 点播列为后续项 |
| **承载形式** | 复用现有 SPA（Vue3 + Vite，`ui/` → `web/dist` -> `go:embed`）新增一个 **`/player` 视图**，**不**另起一套独立页面体系 |
| **播放引擎（三件套）** | **mpegts.js**（直播：`video/mp2t` 原始 TS + FLV）＋ **hls.js**（HLS）＋ **原生 `<video>`**（MP4） |
| **兜底机制** | 所有直播频道统一用**频道不透明键** `player/<key>` 播放（服务端内部分发到 /udp /rtp /rtsp /upstream）；**远程 http(s) 源可选「直连优先」省流量**，直连失败再落 `player/<key>`。受控端点**只拉订阅白名单内的源**，前端不得携带任意 URL（防开放代理/SSRF，§6）。TS 直播整流换地址；HLS(m3u8) 兜底由前端把分片重写为同源 `player/<key>/<子路径>` |
| **盒子本地遥控** | `keydown/keyup` 归一化箭头键/OK/返回/媒体键，做可见焦点态 |
| **服务端改动量** | 小。新增**一个订阅源配置 + 受控拉流端点**（服务端解析订阅当白名单、按白名单拉流），其余播放入口零改动。这是把「直播拉流收口到配置源」以堵开放代理所必需 |
| **风险** | 低。核心是校验「只拉配置的源、拒绝任意 URL」，并在 §6.4 说明直连/受控拉流的分界 |

> **本期只做直播**（订阅 + 受控 `player/<key>` + EPG 渲染 + 盒子遥控）；JX 点播（§4.3）为后续项，本期内不实现。

---

## 1. 现状：服务端已具备的可播入口（后端零改动即可复用）

| 入口 | 路径形态 | 输出 | 浏览器引擎 |
|---|---|---|---|
| UDP 组播直播 | `GET /udp/<ip:port>?iface=..&fcc=..` | `video/mp2t`（原始 TS 连流） | **mpegts.js** |
| RTP 直播 | `GET /rtp/<ip:port>..` | `video/mp2t` | mpegts.js |
| RTSP 直播 | `GET /rtsp/<host>[:port]/<path>` | `video/mp2t`（H264/AAC 或原生 TS remux 成 TS 连流） | mpegts.js |
| 通用转发（默认代理） | `GET /https://<完整远程url>`（含 query） | 原样透传（HLS/TS/MP4），**不 rewrite 分片** | 取决于内容类型 |
| 推流发布 HLS | `GET /play/<id>.m3u8` | m3u8 + 分片 | hls.js |
| 推流发布 FLV | `GET /play/<id>` | FLV | mpegts.js（继承 flv.js 能力） |
| 视频解析（点播） | `GET /jx?jx=<关键词>&id=<集数>&full=1` | `{"url":"<单集远端地址 m3u8/mp4>"}` | hls.js / 原生 video |

**已核实的关键点（设计据此展开）**：

1. UDP/RTP 直播：`UdpRtpHandler` → `hub.ServeHTTP(w,r,"video/mp2t")`（`handler/udp_handler.go#L146`）。
2. RTSP 直播：`RtspToHTTPHandler` 用 gortsplib 连上游，`HandleMpegtsStream`/`HandleH264AacStream` 统一输出 `video/mp2t`（`stream/mpegts.go#L124`、`stream/h264_aac.go#L385`）。TCP interleave + 代理组都内建。
3. 通用转发（`/https://...`，通用反代用，**播放器不使用它**）：`GetTargetURL` 把 `/https://...` 还原为原始 URL（`stream/handle.go#L242-L275`），`HandleProxyResponse` **纯字节流式拷贝、不重写 m3u8 分片**（`stream/handle.go#L31-L74`）。播放器的受控拉流改走 §6 的 `player/<key>`（强制白名单），因此这里的分片重写只发生在 `player/<key>/<子路径>` 场景（§4.4）。
4. 频道清单：**不设终端侧任意拉流**。订阅源（默认 `https://<your-domain>/sub.m3u`，或本地文件/`php` 路径）由 tvgate **服务端持有并解析**，同时充当「允许拉取的源」白名单，并为每频道分配不透明 key。前端展示频道、播放远程流都走服务端（`api/player/channels` + `player/<key>`），播放器不得携带任意 URL。订阅支持 **M3U** 与**逗号 txt**（`名称,URL` + `分类,#genre#`）两格式，见 §6。

---

## 2. 播放引擎选型

| 引擎 | 版本建议 | 用途 | 备注 |
|---|---|---|---|
| **mpegts.js** | 1.7.x（bilibili/xqq） | 直播 `video/mp2t` + FLV | 原 flv.js 后继，支持 HTTP fetch/XHR 拉 TS 连流低延迟播放；**直连需远端 CORS，兜底走同源代理即绕过** |
| **hls.js** | 1.x | HLS 直播/点播（m3u8） | 支持 live catchup/low-latency；兜底用自定义 loader 重写分片 |
| **原生 `<video>`** | — | MP4 点播 | 直连/转发都行，无需自建 loader |

**明确不支持**：HLS `EXT-X-KEY`（加密）、LL-HLS 超低延迟、DRM——遇到即给用户提示并尝试下一可用源。

> 参照：C 版 web-ui（rtp2httpd）用同一能力组合（原生/MSE/自定义 loader）但无 live 低延迟追帧；此处直播走 tvgate 的 TS 连流，天然低延迟，无需再做 live-sync。

---

## 3. 目标与非目标

### 目标
1. 统一直播入口：一次搞定「组播 / RTSP / 远程 HTTP 直播」，引擎按内容自动选。
2. **远程 http(s) 直播直连优先**：盒子能直连远程 `http(s)://` 直播就直连（省 tvgate 流量/跳数）；连不上自动落到受控 `player/<key>`。
3. 频道清单 + 分组，支持盒子遥控焦点切换。
4. 低延迟直观：直播起播即出画，无预缓冲等待。
5. EPG 节目单渲染：本期展示当前/今日节目。

### 非目标
- ❌ 不新增独立页面体系；播放器是 SPA 的一个视图。
- ❌ 不改后端 RTP/组播/推流/代理/解析核心（§1 入口全部原样复用）。
- ❌ 不做多语言 / 弹幕 / 倍速增强（播放器保持简洁）。
- ❌ 不引入 CDN（hls.js / mpegts.js 走 npm 依赖打进 `web/dist`，离线可用）。
- ❌ **本期不做 JX 点播/剧集列表**（§4.3 列为后续项）。

---

## 4. 关键流程

### 4.1 引擎选择规则（前端，按播放地址返回的内容类型判定）

订阅内的频道一律先拿**不透明播放地址** `<源站>/player/<key>`（key 由 `/api/player/channels` 下发），引擎按该地址返回的内容判定：

```
订阅频道 → <源站>/player/<key>
  /rtsp/ /udp/ /rtp/ 源   → 服务器输出 video/mp2t（原始 TS 连流）→ mpegts.js
  远程 http(s) .m3u8      → 返回 HLS → hls.js
  远程 .ts/.flv           → 返回 TS/FLV → mpegts.js

远程 http(s) 源若走「直连优先」：
  url 以 .m3u8 结尾 / 内容类型 mpegurl → hls.js
  url 以 .flv 结尾                      → mpegts.js
  url 以 .mp4 结尾                       → 原生 <video>
  其它 / 探测到 marker(0x47 TS)         → mpegts.js
```

> 组播/RTSP 除 `player/<key>` 外无「直连」选项（浏览器连不了组播/rtsp），天然走受控端点。

> 直播源地址与「直连 vs 转发」是本方案核心，细则见 §4.4。

### 4.2 直播数据流（组播 / RTSP / 远程 → 统一 `player/<key>` → 引擎）

```
频道列表某频道（key 由 /api/player/channels 下发）
 → 播放地址 = <源站>/player/<key>            （源不外露，服务端内部分发）
     源为 udp/rtp  → 服务端内部走 tvgate /udp//rtp/（maps 到组播）→ video/mp2t
     源为 rtsp     → 服务端内部走 tvgate /rtsp/ 连上游 → video/mp2t
     源为远程 http → 服务端 fetch/HLS 回源 → 按内容 TS/HLS
 → mpegts.js.open(<源站>/player/<key>) 或 hls.js（按内容类型） → <video> 出画
```
组播/RTSP 目标在 tvgate LAN 侧，盒子必须先经 tvgate，**无「直连绕过」选项**，天然走受控端点；远程 http(s) 才有「直连优先」可选项（§4.4）。

### 4.3 JX 点播/HLS（**本期不做，列为后续项**）

（后续实现时才启用本小节，覆盖 JX 返回的远端 m3u8/mp4 直连 → 受控 `player/<key>` 兜底）

### 4.4 远程 HTTP(S) 直播：直连优先 → 受控拉流端点兜底（核心）

对「远端 http(s):// 单条流」统一走两步，**兜底只经受控拉流端点、不可塞任意 URL**（§6.4 安全）。播放地址一律用**频道不透明键**（`player/<key>`），真实源地址不外露（对标 C 的 `player/<频道名>` 变换播表）：

> 说明：本节「直连优先」**只对远程 http(s)**；组播/RTSP 无直连、全程只走 `player/<key>`（§4.2）。

```
第一步 直连（需要远端 CORS）：
  m3u8   → hls.js.loadSource(远端Url)
  ts连流 → mpegts.js.open(远端Url)
  mp4    → <video>.src = 远端Url

第二步 兜底（触发条件见 §4.6）：
  播放地址换成 <源站>/player/<频道不透明键>
  该键由服务端为订阅里每个频道的真实源分配（稳定 hash，源不泄漏给前端）
  m3u8   → hls.js.loadSource(<源站>/player/<key>)
            + 自定义 loader：每个分片 url 也重写为同源 <源站>/player/<该源分片子路径>
  ts连流 → mpegts.js.open(<源站>/player/<key>)       ← 整流换地址，无分片
  mp4    → <video>.src = <源站>/player/<key>
```

**为什么 HLS 必须另做分片重写，而 TS 不用：**
- TS 直播是**一条 HTTP 长连接**，mpegts.js 一直读这一路流，兜底 = 改这一次地址。
- HLS 的 m3u8 里含**多个分片相对地址**；受控端点对上游 m3u8 **纯透传不改内容**（§1.3），浏览器按 m3u8 里地址取分片会绕开端点。故前端用 hls.js 自定义 loader，把每个分片 `.resolveUrl()` 结果重写为同源 `player/<key>/<子路径>`。**分片白名单** = 该频道键所对应源 m3u8 的相对子路径，非任意路径。

> 注：`player/` 受控端点与通用代理 `/https://` 是两个东西——`/https://` 保持现状（通用转发用），`player/` 专为播放器且强制白名单，互不影响。

### 4.5 起播失败判定（慢判，不预探）

- 不对远端做 HEAD 预探（CORS / 防盗链 / geo 只有真连才知道）。
- 判定时机：
  - hls.js：`Hls.Events.ERROR`，`fatal` 且 `NETWORK_ERROR` / `MEDIA_ERROR`；同一源重试 ≤ 2 次仍失败 → 切兜底。
  - mpegts.js：`ERROR` 事件 / `video` 进入 `waiting` 且超出起播超时（如 8s 无画面）→ 切兜底。
  - 原生 `<video>`：`error` / `stalled` 超时 → 切兜底。
- 兜底再失败 → 提示「无法播放」，给出换源/返回列表入口，不自动连环换。

### 4.6 剧集/频道切换与连播

- 切换即关闭旧引擎（hls.destroy / mpegts.pause+destroy / video.pause），重建新引擎，避免多实例并发拉流。
- 直播切换目标地址同样走 §4.4 两步兜底。

---

## 5. 盒子本地遥控

- 统一 `document.addEventListener('keydown')` + `keyup`：
  - `ArrowUp/Down/Left/Right`：移动焦点（`Element.focus()` + `.focused` 高亮），`preventDefault()` 防滚动/方向键改音，`key.repeat` 长按加速翻列表。
  - **OK 键在 `keyup` 收到**（多数遥控 OK 只在 keyup 触发）：选择剧集 / 播放 / 暂停。
  - `Backspace` / `Escape` / 部分盒子背键 `keyCode 108`：返回上级。
  - 媒体键 `MediaPlayPause` / `MediaPlay` / `MediaPause`：播放暂停，防误触（事件已 `isTrusted`）。
- 可见焦点态：列表项、返回按钮都要有明确 focus 描边；进入播放页时自动 focus 列表首项。
- 归一化：不同内核背键/OK 差异集中到一个 `useRemoteControl` composable，业务不散落。

---

## 6. 直播频道清单与受控拉流（服务端持有订阅当白名单，防开放代理）

**结论：tvgate 服务端持有订阅源并解析，既出频道列表，又当「允许拉取的源」白名单、给每频道分配不透明 key；播放器只走受控 `player/<key>` 端点，前端不得携带任意 URL（堵 SSRF/开放代理）。**

### 6.1 订阅源配置（新增，指向「配置的本地地址 + 远程 URL」）

配置一个 `player` / `stream` 订阅源（可为本地文件路径或远程 URL）：

| 配置 | 说明 |
|---|---|
| `player.subscription` | 本地文件路径 或 远程 URL，默认如 `https://<your-domain>/sub.m3u`；也支持 `php` docroot 相对路径 |
| `player.update_interval` | 定时刷新订阅（秒，默认 7200，附退避重试） |

- 服务端定时拉取/读取该订阅并解析成内存频道表。
- **解析器支持两种格式（两类源都在用，都要支持）**：
  - **M3U**：`#EXTINF` + 下一行 URL（如 `<your-domain>/sub.m3u`，RTSP `.smil`，带 `tvg-id`/`tvg-name`/`tvg-logo`/`group-title`）。
  - **逗号 TXT**：`名称,URL` 每行 + `分类,#genre#` 分组头（如 `<your-domain>/tv.txt`）。
- 每行可能含 `udp://`/`rtp://`（可带 `?fec=`）、`rtsp://`、`http(s)://….m3u8`、`.ts`、`.flv`。
- 每个远程源的 UA/Referer 等若需指定，作为**该条目的服务端属性**存下（不信任客户端传入）。
- EPG（本期渲染节目单，两种来源，均不收任意客户端输入）：
  - **M3U（原生 XMLTV）**：头 `x-tvg-url="..."` 指向 **XMLTV `epg.xml` 或 `epg.xml.gz`**（按 gzip 魔数 `0x1f 0x8b` 自动识别是否压缩，对标 C `epg.c`）。由**服务端定时下载并解析**全量 XMLTV（`<channel id>`/`<programme channel= start= stop=>`），提供按频道/日期查询接口 `GET /api/player/epg?ch=<tvg-id>&date=..`；播放器不直接连外部 EPG，节目单来自服务端解析结果（源一致、非客户端任意输入）。
  - **逗号 TXT**：用 **EPG 模板**（如 `https://epg.<your-domain>/?ch={name}&date={date}`），`{name}`=频道名、`{date}`=日期（YYYY-MM-DD）；播放器按频道名+日期填充模板拉取节目单。
  - 频道入口：`/api/player/channels` 下发每个频道的 `tvg-id`/`tvg-name`/EPG 来源类型，播放器据此决定走服务端 `/api/player/epg`（M3U）还是填充模板（TXT）。

### 6.2 白名单与受控拉流端点 `GET /player/<key>`

- **白名单来源** = §6.1 解析出的频道表（其 URL/源的集合）。服务端仅对「命中订阅的源」与「这些源产生的子分片（相对基址）」放行。
- **每个频道分配不透明键 `<key>`**：由真实源 URL 生成稳定 hash（对标 C：播放只见 `player/<频道名>`，源不外露、非加密只是间接寻址）。
- **`GET /player/<key>`**：服务端：
  1. 查 `<key>` 是否有记录；无 → `403`。
  2. 命中 → 用该条目的服务端属性（真实源地址 + UA/Referer/代理组）发起上游拉流/请求，把内容流式回给播放器。**真实源地址只存在于服务端，前端/浏览器不可见**。
  3. 对 HLS 的子分片请求：仅允许是「该 key 对应源 m3u8 的相对子路径」（`/player/<key>/<子路径>`），否则 `403`。
- **防滥用**：任意 URL 直接请求都能在第一步被拒（无该 key），播放器/浏览器无法拿 tvgate 当跳板拉任意站点。

### 6.3 播放器数据流

```
进入播放页
 → GET <源站>/api/player/channels（服务端已解析的频道列表 JSON，含分组/频道名/<key>）
 → 渲染分组/频道（遥控焦点）
 → 播放某频道（一律 <源站>/player/<key>，服务端内部分发）：
     源为 udp/rtp/rtsp → player/<key> → TS → mpegts.js（无直连，天然受控）
     源为远程 http(s)  → 先直连（可选省流量）；失败 → player/<key>（§4.4）
 → 进入频道时按 EPG 模板拉节目单并渲染（§6.1）
```

- **不信任前端拼 URL**：前端仅提交「该频道的 `<key>`」，真实上游地址与 UA 由服务端经 key 查表得到，杜绝客户端带任意 url。
- 频道列表由 `/api/player/channels` 下发（含分组/频道名/key/tvg 属性），「取自订阅、非任意输入」。

### 6.4 安全说明（为什么不再让前端带 `<源站>/https://<任意url>`）

- 早期版本让播放器把任意 `http(s)://` 改写成 `源站/https://<url>` 走通用代理——这会把 tvgate 变成**开放 HTTP 代理**（SSRF 探测内网 / 流量走私 / 当跳板），正是要避免的。
- **新方案收敛**：远程拉流一律经 `player/<key>`（不透明频道键）且强制订阅白名单；`/https://` 通用代理保持现状用于既有转发场景，播放器不再触碰它。
- UA 由服务端每个源配置，客户端无需传（顺手解决浏览器禁止自定义 UA 的问题）。

---

## 7. 部署 / 路由 / 鉴权

- **承载**：**独立自含播放页 `/pp/`**（`web/player/index.html` + 本地打包 hls.js/mpegts.js，经 `go:embed` 随单二进制发布，与后台同域）。注：原定的 SPA `/player` 视图因 `ui/` 前端工程不在当前工作树，改用独立页实现。
- **引擎依赖**：`hls.js` / `mpegts.js` 已下载放 `web/player/`，随二进制 embed，本地交付、离线可用（不引 CDN）。无需 npm 构建。
- **鉴权**：`/player`、`/api/player/*`、`/player/<key>` 走现有 `cookieAuth`（播放器会话）；`/player/<key>` 若被直接当流地址被播放器/第三方引用，需带全局 token（`my_token`）校验（现有后台的 `token_param_name`）。
- **服务端**：订阅解析（§6.1）+ 白名单校验 + `player/<key>` 拉流，作为播放器专属能力挂载到与 `/jx` 同端口（「新 HTTP/HTTPS 端口」，见 §1 表），保证播放页同源。
- **后台编辑与生效（全走热加载）**：`/web/player-editor` 改 `player` 段（启用/订阅/EPG模板/刷新间隔，走 `config/save-player`，沿用 `yaml.Node` 保留注释 + `.backup` 备份）。保存后 config watcher（`config/watch/watch.go`）会**重建 mux** 并用 `load.LoadConfig` 刷入新配置——因此 `subscription`/`epg`/`update_interval` 在下个刷新周期生效，**`enabled` 开关也会随 mux 重建立即挂载/卸载** `/api/player/*`、`/player/<key>`、`/pp/`（与 php 模块一致，无需重启）。Manager 的 `Reload` 每次读当前全局 `config.Cfg.Player`（避免 `config.Cfg= newCfg` 替换后的陈旧指针）。

---

## 8. 新增/改动清单（按当前实现）

| 类别 | 项 | 说明 |
|---|---|---|
| 前端（播放页） | `web/player/index.html` | 独立自含播放页 `/pp/`：分组/频道列表 + 三引擎（mpegts/hls/原生） + 遥控 + EPG |
| 前端（播放页） | `web/player/hls.min.js`、`mpegts.js` | 本地打包库，随二进制 embed |
| 后端（新增） | `player` 配置区段（enabled/subscription/epg/update_interval） | `config/config.go#PlayerConfig`；默认 2h |
| 后端（新增） | `player/` 包：订阅加载+解析（M3U/逗号TXT）→ 频道表=白名单 + 每频道不透明 key | 真实源/UA 仅服务端 |
| 后端（新增） | `GET /api/player/channels` + `GET /player/<key>[/子路径]` | 出列表（含 key）；受控拉流（内部分发 udp/rtp/rtsp/http），命中 key 才放行；HLS 分片服务端重写、相对分片经子路径回拉（§6.2） |
| 后端（新增） | M3U EPG：`x-tvg-url` → XMLTV `epg.xml(.gz)` 下载解压解析 + `GET /api/player/epg` | gzip 魔数 `0x1f 0x8b` 识别（对标 C `epg.c`），按 tvg-id+日期查询（§6.1） |
| 后端（新增） | 路由挂载 + token 鉴权 | `/api/player/*`、`/player/`、`/pp/` 与 `/jx` 同端口（`server/http.go`） |
| 后台管理（新增） | `web/handleplayer.go` + `templates/player_editor.html` | `/web/player-editor` 编辑 `player` 段 + `config/player`、`config/save-player` JSON 读写（§7） |

---

## 9. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| **CORS**：直连远端直播/点播被浏览器拦（无 `Access-Control-Allow-Origin`） | 直连失败 | 属设计内路径，自动落同源 `player/<key>` 兜底（§4.4） |
| **分片重写遗漏**：HLS 兜底时某个分片没走 `player/` | 偶发黑屏/卡分片 | 统一在 hls.js loader 的 `resolveUrl` 处集中重写为同源 `player/<key>/<子路径>`，单点收口 |
| **开放代理 / SSRF**：有人拿 tvgate 拉任意站点 | **安全风险** | 远程拉流仅在 `player/<key>` 且强制订阅白名单（§6.2），无 key 一律 403；`/https://` 通用代理不供播放器使用 |
| **解码兼容**：盒子解不了 H.265 / 杜比 | 有音无画 / 报错 | `player/<key>` 兜底后仍失败即给明确提示并尝试换源；浏览器受硬件能力限制 |
| **频道清单依赖订阅源** | 未配订阅则无频道 | 配置 `player.subscription`（默认 `https://<your-domain>/sub.m3u`，M3U/逗号 txt 均可），见 §6.1 |
| **直播多实例叠加**：切台未销毁旧引擎 | 带宽/内存浪费、串流 | 切台先 `destroy/pause` 旧引擎再建新（§4.6） |

---

## 10. 待拍板决策

| # | 决策项 | 结论 |
|---|---|---|
| 1 | 直播拉流安全边界 | ✅ **服务端持有订阅当白名单 + 每频道不透明 key**；播放地址 `player/<key>`，真实源不外露；前端不得携带任意 URL（堵 SSRF/开放代理）。不新增 `/playlist.m3u` 重写端点 |
| 2 | 组播/RTSP 是否收口 | ✅ **全部收口 `player/<key>`**（服务端内部分发到 /udp /rtp /rtsp /upstream），源完全不外露、对标 C |
| 3 | 订阅源与格式 | ✅ 默认 `player.subscription = https://<your-domain>/sub.m3u`（M3U，RTSP `.smil`）；解析器支持 **M3U** 与**逗号 TXT**（`名称,URL` + `分类,#genre#`）两格式，条目覆盖 udp/rtp/rtsp/http(s) |
| 4 | 本期范围 | ✅ **只做直播**（订阅 + `player/<key>` + 盒子遥控）；JX 点播列为后续项 |
| 5 | EPG | ✅ **本期渲染节目单**。M3U 走**原生 XMLTV `epg.xml(.gz)`**（服务端下载/解压/解析 + `/api/player/epg`，对标 C `epg.c`，gzip 魔数 `0x1f 0x8b` 识别）；TXT 用 `https://epg.<your-domain>/?ch={name}&date={date}` 模板 |
| 6 | 播放器入口 | ✅ SPA 路由 `/player`（与后台同域），不独立建站 |
| 7 | 引擎依赖 | ✅ npm 引入 `hls.js` + `mpegts.js`，本地打包不进 CDN |
| 8 | 兜底实现 | ✅ 远程 http 先直连、失败转 `player/<key>`；HLS 分片前端 loader 重写为同源 `player/<key>/<子路径>`；UA 为服务端每源属性 |
| 9 | 遥控范围 | ✅ 盒子本地按键（keydown/keyup），本轮不做手机远程遥控 |

---

## 附：关键文件索引

| 文件 | 作用 |
|---|---|
| `handler/udp_handler.go#L146` | UDP/RTP → `video/mp2t` |
| `handler/rtsp_handler.go` | RTSP → remux `video/mp2t`（gortsplib + 代理组） |
| `handler/http_handler.go#L138-L148` | `/udp /rtp /rtsp` 分流；默认代理入口 |
| `stream/handle.go#L242-L275` | `GetTargetURL`：`/https://...` 还原转发的核心 |
| `stream/handle.go#L31-L74` | `HandleProxyResponse`：纯透传、不重写 m3u8 |
| `publisher/handler.go#L26-L102` | `/play/{id}` HLS/FLV |
| `jx/jx_handler.go`、`jx/request.go#L17-L105` | JX 解析，返回远端可播地址 |
| `config/config.go#L112-L114` | `MulticastConfig`（现状无频道清单） |
| 新增：`player` 配置区段 / 订阅解析器 / `player/<key>` 受控拉流 / `/api/player/channels` | 服务端订阅白名单 + 每频道不透明 key（§6） |
| `ui/`（Vue3+Vite） | 播放器承载工程 |