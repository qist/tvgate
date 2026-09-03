# TVGate H5 播放器 · 设计文档

> 状态：**已实现（v2 · SPA 双入口版）**
> 日期：2026-09-03
> 适用范围：H5 直播播放器页面（频道列表 / EPG / 回看 / 设置），以及所依赖的服务端播放器模块；**不**改后端代理/组播/推流/解析核心逻辑

---

## 0. 结论先行（TL;DR）

| 项 | 结论 |
|---|---|
| **承载形式** | SPA 双入口构建（`ui/index.html` 管理后台 + `ui/player.html` 播放器），路由 `{web.path}player`（旧地址 `/pp/` 自动 301 跳转并透传 `my_token`）；产物经 `go:embed` 随单二进制发布 |
| **播放引擎** | 自研 **playback-engine**（TS 源码 `ui/src/playback-engine/`，随 SPA 构建进产物）：TS 连流 + HLS（m3u8）+ WASM MP2 音频软解 + WebGL 渲染（自动反交错 / 画质增强）+ 双 video 槽无缝换台 + 直播追帧（live-sync） |
| **数据层** | 频道列表 / EPG / 回看全部走服务端 API（`/api/player/channels`、`/api/player/epg`、`/api/player/catchup`），**真实源地址不出服务端**；播放地址一律为不透明键 `player/<key>` |
| **鉴权** | 所有 API 与流请求透传全局 token（`my_token` 查询参数，页面 URL 携带） |
| **安全边界** | 订阅即白名单：服务端解析订阅生成频道表，前端仅见 `key`，无法携带任意 URL（堵 SSRF/开放代理） |

---

## 1. 页面结构（`ui/src/pages/player.tsx` + `ui/src/components/player/`）

- **视频区**：`VideoPlayer` 双槽（slot a/b 各一对 `<video>`+`<canvas>`）无缝换台；错误自动重试（≤3 次）→ 降级换源 → 错误 UI
- **侧栏**：频道列表（分组 / 搜索 / EPG 当前节目）与节目单（EPGView，过去节目可回看）两个 Tab
- **控制条**：播放暂停、进度（回看 VOD 可拖动）、音量、频道号输入、全屏、画中画、设置
- **设置菜单**：语言（en/简/繁）、主题（自动/浅/深）、界面风格（fancy/simple）、画中画模式、无缝换台、自动反交错、画质增强
- **遥控与手势**：方向键/OK/返回/媒体键导航；移动端手势（左半屏竖划换台、右半屏音量、横划 seek、双击暂停）
- **状态持久化**：localStorage（键 `tvgate-player-*`：上次频道、音量、外观、主题等）
- **深链**：URL hash 记忆当前频道（`#频道名` 或 `#key`），刷新/分享自动恢复

## 2. 播放引擎（`ui/src/playback-engine/`）

- **统一入口**：`createPlaybackBackend(video, config)`，MSE 后端（`createMSEPlaybackBackend`）+ 原生后端兜底（LG webOS 等无 MSE 平台）
- **内容自动嗅探**：`loadSegments([{url, duration: 0}])` 单段即进入 continuous-live 模式，worker 拉流后按内容自动切换——原始 TS（0x47）直接 demux，遇 `#EXTM3U` 自动切 HLS 源（含 VOD 判定）。因此 `/player/<key>`（无扩展名，服务端可能回 TS 或 m3u8）无需前端做 Content-Type 预探测
- **直播追帧**：live-sync（target 1.5s / max 3s），延迟超限自动变速追赶
- **渲染管线**：WebGL canvas 输出，1080p 及以下可开自动反交错（bwdif）与画质增强（FSR upscale）
- **音频软解**：TS 流中检测到 MP2 音频时惰性加载 `mp2_decoder.wasm`（Vite `?url` 导入，构建产物 `assets/mp2_decoder-<hash>.wasm`），解出 PCM 走 WebAudio 播放
- **Worker**：demux/remux 在 inline worker（Vite `?worker&inline`）中执行，不阻塞主线程

## 3. 数据流

```
进入 {web.path}player?my_token=<token>
 → GET /api/player/channels          频道列表（key/name/group/scheme/tvg_*/epg_type）
 → 渲染频道列表（分组/搜索/台标）
 → 选台：
     直播  → loadSegments([{url: "/player/<key>?my_token=..", duration: 0}])   引擎嗅探 TS/HLS
     回看  → GET /api/player/catchup?key=<key>&start=<YmdHis>&end=<YmdHis>
             → {url:"/player/<key>/<token>"}（服务端签发的受控回看地址，起始即 seek 目标）
             → loadSegments([{url, duration: 0}]) → 引擎判定 VOD，进度条可拖
     节目单 → GET /api/player/epg?date=YYYYMMDD&ch=<tvg-id>&name=<频道名>       按频道懒加载
```

- EPG 键匹配回退：`tvgId → tvgName → name`；无 EPG 数据时对可回看频道做 2h 占位节目填充（不阻塞首屏）
- 回看失败自动回到直播；换台/seek 竞态用递增序号丢弃过期响应

## 4. 服务端播放器模块（`player/` 包，保持不变）

- `player.subscription` 订阅（M3U / 逗号 TXT，本地文件或远程 URL，支持目录递归）→ 服务端解析为频道表 = **白名单**，每频道分配稳定不透明 key（md5+sha1 截断）
- `GET /api/player/channels`：频道列表 JSON（真实源 URL 与 UA 不外露，`json:"-"`）
- `GET /player/<key>[/<sub>]`：受控拉流。udp/rtp/rtsp 内部分发；http(s) 回源（跟随重定向、注入 UA）；m3u8 由服务端**重写**分片为 `/player/<key>/<sha1 前 10 位>` 短地址并登记 token（TTL 30 分钟），CDN 源站地址不外露
- `GET /api/player/epg`：M3U 订阅走服务端解析的 XMLTV EPGBank（`x-tvg-url`，gzip 魔数自动识别）；TXT 订阅由服务端填 `{name}/{date}` 模板代拉（规避 CORS）
- `GET /api/player/catchup`：http(s) 源按 `playseek=<start>-<end>` 回源拼装并签发受控短地址
- 挂载：`cfg.Player.Enabled` 时注册以上端点（`server/http.go`），`/pp/`→`{web.path}player` 301；SPA 入口 `registerSPARoutes`（`web/spa.go`）提供 `player.html`

## 5. 构建

- `make web-ui` → `ui/ npm run build` → Vite 双入口（`index.html` + `player.html`）输出 `web/dist/` → `go:embed all:dist`
- 播放器 bundle 独立 chunk（约 115KB gzip），不进管理后台首屏；`web/dist` 缺失时播放页返回构建提示
- 配置编辑仍在后台「播放器」页（`views/config/Player.tsx`），保存走 `config/save-player` 热加载

## 6. 安全要点

- 前端只见不透明 key 与受控短地址，真实源地址 / UA 仅存服务端
- 任意 URL 请求无 key 一律 403；HLS 分片仅放行该频道 m3u8 派生的短 token
- 播放器 API 与流均要求 `my_token`（全局 token，与 /jx、/udp、/rtsp 一致）
