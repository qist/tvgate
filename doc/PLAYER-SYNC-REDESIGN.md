# H5 播放引擎 · 音频同步层重构设计

> 状态：Draft v2 | 日期：2026-09-13
> 范围：`ui/src/playback-engine/`（前端播放引擎）
> 取代：`doc/audio-sync-fix-plan.md`（v1 草案，其结论部分已被证伪，见 §1.3）

---

## 0. 决策摘要

| 议题 | 结论 |
|---|---|
| 是否整体重写 17.7k 行引擎 | **否**。重写边界划在 `audio/`（约 1200 行，已被补丁堆满，整体推掉重写为 5 个小模块）与 `mse/` + `timeline/`；`demux/`(3.5k) `remux/`(2.4k) `render/`(2.5k) `io/`+`hls/`(1.3k) 一行不动 |
| 硬解设备是否强制软解 | **否**。改为「先硬解，无声再降级」，探测用**内置探测片段实测**而非 `isTypeSupported`，见 §3.2 |
| 是否支持 WSOLA | **是**，且 `avcodec_audio.wasm` 已导出 `wsola_*`、`audio/wasm-stretcher.ts` 已可用，只需正确接线（§3.4） |
| 追延迟是否允许丢内容 | **不允许作为常态**。常态用 WSOLA 无感微调；硬重锚只作兜底与事件响应（§3.5） |
| 音频两条输出路径 | P1 软解 PCM（WSOLA 可变速）；P2 原生硬解（天然同步，不解码、不纠偏） |
| P2 是否做「保持音高」 | **不做**（P2-a）。接 `createMediaElementSource` + WSOLA 等于又回到软解的输出结构，性价比为负；先看追速时间占比，超阈值应调 live-sync 参数而非上音高补偿 |
| 推进顺序 | `0 清算 → 3' 硬解路由 → 1 同步内核 → 2 WSOLA`；阶段 4（时间轴单一权威）本次**不做** |
| 恢复漂移重锚的时机 | **与阶段 1 一起**，不单独恢复 —— 缺少不变量 1/2 时会重演 re-anchor 风暴与大跳变清队静音 |

---

## 1. 现状与问题

### 1.1 实际数据流（含分叉点）

```
拉流 io/fetch-loader.ts（由 worker/segment-source.ts 或 hls/hls-source.ts 驱动）
  └ worker/pipeline.ts _run
      ├─[分叉A] pipeline.ts:516  hls / continuous-live-ts / static-ts-list
      ├─[分叉B] pipeline.ts:835  TSDemuxer / FLVDemuxer / fMP4 直通
      └─ audio ─┬─[分叉C] AAC/MP3         → MP4Remuxer → MSE audio SourceBuffer
                └─        MP2/AC-3/E-AC-3 → demuxer 按帧头切帧 + 逐帧 PTS 外推
                          → onRawAudioData（每帧带自己的 PTS）
                          → worker-audio-decoder → avcodec-audio-decoder(WASM)
                          → pcm-audio-data → playback-controller.ts:169
                          → audio/pcm-audio-player.ts (WebAudio)
  视频解码：始终由 UA 硬件/引擎解码（<video>），引擎内无软解视频
  渲染 ─┬─[分叉D] render/index.ts:162  MSE + renderCanvas → WebGL 后处理叠加
        └─        其余 → 裸 <video>
```

### 1.2 重复实现（都在同步链路上）

| 职责 | 实现处 | 份数 |
|---|---|---|
| buffered 区间判定 | `mse/playback-controller.ts:12` `:191`、`backends/native-playback-backend.ts:14`、`mse/live-sync.ts:27` | 4 |
| source mode 推断 | `mse/playback-controller.ts:348`、`worker/pipeline.ts:516` | 2 |
| 直播边缘/锚点数学 | `mse/playback-controller.ts:237`、`timeline/wall-clock.ts:24`、`native-playback-backend.ts:252` | 3 |
| 时间戳基准 | `pipeline.ts:167,732` → `mp4-remuxer.ts:126,577,619,686` → `media-source.ts:207` → `pipeline.ts:1176` | 跨 4 层 |
| 音频重锚/时钟 | `pipeline.ts:1292`（自由时钟，仅诊断）、`pipeline.ts:1243`（重钉）、`pcm-audio-player.ts:576`（硬重锚） | 3 |

### 1.3 自相矛盾的结论 —— 同步策略被整体关上

同一个被测对象（ac3-lab）在代码里存着两条互相矛盾的记录：

- `worker/pipeline.ts:1281-1287`：`ac3-lab 508s 实测 drift p50=-0.2ms`
- `audio/pcm-audio-player.ts:612`：`lab 实测 drift 长期稳定在 -190ms（永不 re-anchor）照样完美同步`

`-190ms` 那条已被证伪：它是 lab 锚定实现把「队列头已落后画面」的那段 PCM **整段重放**（`start = max(desired, now+ε)`）造成的**真实滞后**，被永久写进链，且 190ms < 250ms 重锚阈值所以永不纠正。修复后 lab 实测 -10ms、重锚后 -5ms（§5.1）。

受此错误结论影响，player 里：

- `pcm-audio-player.ts:615-617` 漂移触发重锚 **整段被注释**
- `pcm-audio-player.ts:625` autoCalib 被字面量 `if (false && ...)` **永久关闭**

=> 当前 player 实际只剩「rate 跟随 + 排不出去时的阻塞兜底」，**没有任何基于漂移的自愈**。

### 1.4 死代码

| 位置 | 说明 |
|---|---|
| `audio/wasm-stretcher.ts`（166 行） | WSOLA 封装，**零引用**。能力是资产，接线是债务 |
| `remux/aac-silent.ts` + `mp4-remuxer.ts:421-521` `_generateSilentAudio` + `_silentAudioMode` | 无任何生产者置 true；demuxer 反而发 `softwareDecodeOnly` 把它关掉 |
| `native-playback-backend.ts:264-267` | 4 个空实现（平台分支，保留） |

### 1.5 变速方式会变调

`pcm-audio-player.ts:519` 用 `source.playbackRate.value = rate` 跟随视频速率。`AudioBufferSourceNode.playbackRate` 是**重采样**，变速必然变调（live-sync 追延迟到 1.2x 时音频升高约 3 个半音）。这是引入 WSOLA 的直接理由：变速但保持音高。

---

## 2. 目标与非目标

**目标**
1. 硬解设备（MSE 原生 AC-3/E-AC-3）走原生路径：不占 CPU/电量、天然同步。
2. 软解设备保持有声且同步；live-sync 追延迟**不变调**（WSOLA）。
3. 同步只有一个内核、一套时间基准，可被 `ac3-lab` 逐条验证。

**非目标**
- 不改 `demux/` `remux/` `render/` `io/` `hls/` 的内部实现。
- 不引入服务端改动。
- 不做视频软解。

---

## 3. 设计

### 3.1 单一时间基准

新增 `timeline/media-clock.ts`，全引擎唯一的时间换算入口：

```ts
// 屏幕帧时间：对齐必须用它，而不是 video.currentTime（后者比画面早一个显示延迟）
visibleVideoTime(video: HTMLVideoElement): number;   // = currentTime − displayLead(rVFC)
// 音频图时间 ↔ 墙钟 ↔ 媒体时间
ctxTimeForMediaTime(ctx, mediaTime, calib): number;
// 直播边缘（唯一实现，替换 playback-controller / wall-clock / native 三份）
liveEdgeSeconds(...): number;
bufferContains(sb: TimeRanges, t: number): boolean;  // 唯一实现，替换 4 份
```

### 3.2 音频输出后端与能力探测（硬解优先）

```
                       ┌─ P2 原生硬解（MSE 音轨 / native <video src>）→ 天然同步，不纠偏
音频编解码 codec ──────┤
                       └─ P1 软解 PCM（WASM → WebAudio）→ 需同步内核 + WSOLA
```

**探测方案**（替代当前 `mse-playback-backend.ts:20-34` 的「一律软解」）：

| 级 | 手段 | 作用 |
|---|---|---|
| L1 | `MediaSource.isTypeSupported('video/mp4;codecs="ec-3"')` | 快速排除。**不可单独采信** |
| L2 | 内置一段 **1 秒 AC-3 fMP4 探测片段**（约 5~10KB，随构建内嵌），用一次性 `MediaSource` 真实解码，800ms 内查已解码音频字节数（Chromium `webkitAudioDecodedByteCount`；不支持该属性时退化为 `audioTracks.length > 0`） | **唯一可信信号**：L1 会在平台无 Dolby 解码器时假 true |
| L3 | `navigator.mediaCapabilities.decodingInfo({ type: "media-source" })` | 交叉验证（有效性待实测） |
| L4 | URL 逃生开关 `?audioPath=sw` / `?audioPath=hw` | 现场兜底与回归对比 |

**为什么不用「在正式 MediaSource 上真实 `addSourceBuffer`」做 L2**：Blink 里 `addSourceBuffer` 与 `isTypeSupported` 走同一套 codec 注册表检查，L1 假 true 的地方它同样会假通过，等于自欺。

**为什么不用「起播后 2s 发现无声再热切」**：那需要 session 重建并让用户听到 2s 静音。探测片段方案把判定前置到 load 阶段，**不需要热切换、不影响真实播放启动、结果是确定的**。

**缓存**：判定结果按 `codec` 缓存在会话内（设备能力在一次会话中视为稳定）。跨会话持久化（localStorage）**未做** —— 缓存过期会让"本来能硬解的设备"在平台变化后失去声音，收益不值得这个风险。

**实现**：`audio/audio-router.ts`（新增），`backends/mse-playback-backend.ts` 接线。

**fail-safe 三条硬规则**（违反任意一条都可能让设备失去声音）：
1. 任何异常、超时、拿不到可信信号 → 判定为"不支持原生"，回落软解（= 历史行为）。
2. `decodedAudioBytes === null`（非 Chromium，没有这个可信信号）→ 同样回落软解。
3. **必须两个 codec 都通过**才放开开关。worker 侧只有一个 `wasmDecoders.ac3` 同时控制 AC-3 与 E-AC-3 的软解（`ts-demuxer.ts:1681/1795`），只过一半就放开会让另一种 codec 的频道彻底没声音。

**时序**：
- backend 创建时即并行预热探测（隐藏元素里跑，不在关键路径）。
- `loadSegments` 首次装载时最多等 `AUDIO_ROUTE_WAIT_MS = 400ms`；超时就本次走软解并在日志里告警。
- **已知取舍**：若首个频道在 player 挂载后 400ms 内就开始装载，本会话会走软解（决策随控制器固定）。探测片段窗口 `PROBE_WATCH_MS = 400ms`。

**实测过的坑（必须保留在实现里）**：`sourceopen` 监听**必须先挂、再设 `element.src`**。先 `await fetch(...)` 再挂监听会丢事件 —— 表现为 `sourceopen timeout`，而真机上"支持硬解的设备被误判为不支持"，功能静默失效（本地已修）。

> 当前 `?preferNativeAc3=1` 等价于 `?audioPath=hw`，保留兼容。

### 3.3 P1 同步内核（提炼自 `ac3-lab/audio-sync.ts`）

✅ **已实现**：`audio/audio-sync-core.ts`（580 行），`ac3-lab` 与 `PCMAudioPlayer` 共用同一实例化入口。
对外接口：`enqueue / pump / controlTick / reanchor / resetChain / driftSec /
heardStreamTime / visibleVideoTime / getOutputLatencySec / setCalibrationMs / stats / start / destroy`，
以及注入钩子 `getRate / getScheduleAheadSec / maxAudioLeadSec / maxQueueChunks / reanchorDriftSec /
onSchedulingBlocked / onSchedulingResumed`。

- **lab 侧**：`ac3-lab/main.ts` 直接使用内核，删除了本地重复的 rVFC 探针与"漂移过大就重锚"策略。
- **player 侧**：`PCMAudioPlayer` 只保留设备接线/生命周期/兜底，排程与纠偏全部委托内核。
- **纠偏策略集中在内核的 `controlTick()`**：`pump()` + `|drift| > reanchorDriftSec` → 硬重锚；
  `controlTick(false)` 用于页面隐藏等不该重锚的场景。

**五条不变量**（前三条由本次修复确立，禁止回退）

1. **锚定只能「跳」，不能「重放」**
   链重启时若算出的起点已落在过去，必须用 `AudioBufferSourceNode.start(when, offset)` 跳过本段中已过期的部分；整段过期则丢弃。
   反例（原实现）：`start = max(desired, now+ε)` → 把滞后量写进链并永久保留（实测 -190ms / -101~-116ms）。

2. **锚定前视频时钟必须确实在推进**
   要求「距上次时钟变化 < 150ms」且「自上次锚定起 `currentTime` 已前进 ≥ 0.1s」。
   反例：启动期 `paused` 已为 false 但 `currentTime` 冻结在起点，据此锚定会把随后冻结的时长记成「音频超前」（实测 +63ms）。

3. **ctx 时间 ↔ 媒体时间必须按「实际排出去的内容」记账**
   每一段的 `ctxEnd − ctxStart` 与 `streamEnd − streamStart` 必须同源推导（见 §3.4），不允许一边用输入时长、一边用输出时长。

4. **纠偏手段分级**：WSOLA 微调（无感）优先，硬重锚（有接缝）兜底（§3.5）。

5. **校准常量只标定、不收敛**：`audioSyncOffsetMs` 是每设备标定值，不参与任何闭环求解。

### 3.4 WSOLA 变速（重新接线）

**为什么不能只用 `source.playbackRate`**：变调（§1.5）。
**为什么不能"输入 chunk → 输出 chunk"简单记账**：WSOLA 有内部重叠相加缓冲，`process()` 可能返回空（`wasm-stretcher.ts:23`），输入输出**非 1:1**；旧实现按输入 chunk 的 `streamEnd` 记账，于是「环量到的标签」与「喇叭里真实播放的内容」逐秒分离（`pcm-audio-player.ts:22-26` 记载的坑）。

**正确结构：把"伸缩"与"排程"解耦成两级，记账只发生在输出级**

```
[WASM 解码] → 输入队列(媒体时间 T, 内容游标 c)
                   ↓  (ratio r = 目标速率)
              WasmStretcher（连续状态，跨 chunk 不重置）
                   ↓  输出 PCM 切片（定长，如 30ms）
         sync-core 排程：每切片记录 (ctxStart, ctxEnd, contentStart, contentEnd)
                   ↓
              AudioBufferSourceNode（playbackRate 恒 1.0！）
```

记账公式（**唯一真源**）：不要用名义 `ratio` 推算，直接取 `wsola_position()` 的增量。
设某输出切片占 `n` 帧（ctx 时长 `s = n / sampleRate`），该次 `process()` 返回后
`position` 从 `P0` 推进到 `P1`（单位：输入帧）：

```
streamStart = contentBase + P0 / sampleRate   // 权威：已输出内容在输入流中的绝对位置
streamEnd   = contentBase + P1 / sampleRate
span = { ctxStart: X, ctxEnd: X + s, streamStart, streamEnd }
nextStartTime = X + s
```

`contentBase` 是"`position == 0` 对应的媒体时间"，只在 `wsola_reset()`（重锚/seek）后重新种入。
这样记账**与 ratio 变化无关**，也天然免疫"环量到 ≈0 但口型对不上"的内容-标签分离。

**实测（真实 Chrome + 白噪声 + 0.5s 处 8 倍幅度标记，48000Hz/1ch，wsola.c 的 SEQ=30ms / OVERLAP=12ms / SEEK=10ms）**：

| ratio | out/in 实测 | 理论 1/ratio | 结束时 `position`(s) | 输入(s) | 首次有输出前需喂入 |
|---|---|---|---|---|---|
| 1.00 | 1.0000 | 1.0000 | 3.000 | 3.0 | 40ms（= 首次投喂粒度） |
| 0.80 | 1.2300 | 1.2500 | 2.952 | 3.0 | 60~100ms |
| 1.20 | 0.8200 | 0.8333 | 2.952 | 3.0 | 60~100ms |
| 1.50 | 0.6600 | 0.6667 | 2.970 | 3.0 | 80ms |
| 2.00 | 0.5000 | 0.5000 | 3.000 | 3.0 | 80ms |
| 0.50 | 1.9700 | 2.0000 | 2.955 | 3.0 | 80ms |

由此确定四件事：

1. **`position` 语义成立**：`position = out_frames × ratio` 精确成立（0.8 档：3.69s 输出 × 0.8 = 2.952s ✓）→ 可直接作为内容游标，**不存在需要标定的 `L_w`**（原 §7 遗留项 1 就此关闭）。
2. **没有系统性内容偏移**：标记落在 `markerIn / ratio` 处，误差 −20 ~ +23ms 且不随 ratio 单调变化 —— 量级不超过一个合成周期（30ms），是 WSOLA 窗函数平滑 20ms 标记造成的测量弥散，不是对齐偏移。
3. **两处固有代价**：
   - **预填 52ms**：一个合成周期需要 `seek + seq + overlap = 10 + 30 + 12 = 52ms` 输入在内，再加一次投喂粒度。投喂是瞬时的，不产生可听延迟；但 ratio≠1 时首次输出最早出现在喂入 52ms 之后。
   - **尾部丢弃 ~48ms**：**没有 flush 接口**，结束时仍有约 48ms 输入留在内部缓冲未输出（实测 `position` 停在 2.952 而非 3.000）。重锚/换台时这段会丢，与"硬重锚丢 ≤300ms"的既定取舍一致。
4. **ratio == 1 时是逐位直通**（`wsola.c:234` 的 bypass 分支）：预填与尾部丢弃都不存在 —— 即**稳态（不需要追延迟）时 WSOLA 零代价**，只有追赶期才真正参与。

**约束**

| 项 | 取值 | 理由 |
|---|---|---|
| 目标 ratio | `r = R_video`（跟随 live-sync 的 `video.playbackRate`） | 音频内容必须与画面以同一速率推进 |
| 微调带宽 | `r ∈ [R_video×0.92, R_video×1.08]` | 留余量给残余漂移修正 |
| ratio 变化率 | ≤ 2%/s | WSOLA 需要时间做交叠淡入，跳变会有金属声 |
| ratio 硬限幅 | `[0.5, 2.0]` | 与 wasm 侧输出缓冲上限一致 |
| 重置时机 | 硬重锚、seek、切台、codec 变化 | 状态不连续，继续用旧状态会产生错误交叠；`wsola_reset()` 会清零 `position`，必须同时重新种入 `contentBase` |
| 预填 / 尾部 | 52ms 预填、~48ms 尾部无法 flush | 已实测；ratio==1 时均不存在 |

**与两套速率环路的关系（避免振荡）**

- 慢环 = live-sync：决定 `video.playbackRate`（画面追延迟），时间常数秒级。**必须立刻跟随**——音频不跟，内容就会以 `r − R` 持续跑偏。
- 快环 = WSOLA 残余漂移修正：作用于 `r` 的微调带宽内（±8%），并限幅变化率。
- 两环必须分离时间尺度；同一尺度会让「画面加速」与「音频加速」互相追逐。

**环路整定（实测教训，务必保留）**：快环是「积分对象 + 死时间」——死时间 θ ≈ 排程前瞻 0.6s + 一个合成周期 52ms + 控制环采样 0.25s ≈ **0.9s**。这类对象必须满足 `K × θ < 1` 才不发散：

| 增益 K | K×θ | 实测表现 |
|---|---|---|
| 1.5 | ≈1.35 | **±8% 满幅极限环**：ratio 在 1.104~1.296 间摆动、drift 在 ±150ms 间振荡（周期约 8s） |
| 0.6 | ≈0.54 | 收敛：ratio 稳定在 1.200、drift 8~16ms |

另外必须加 **20ms 死区**：否则零点附近会持续"追"，把测量噪声积分进 ratio，稳态也停不在视频速率上。加了死区后实测 ratio 精确等于 1.000/1.200。

### 3.5 纠偏策略分层

| 条件 | 手段 | 可感性 |
|---|---|---|
| `abs(drift) < 15ms` | 不动作 | 无 |
| `15ms ≤ abs(drift) < 250ms` | WSOLA 在微调带宽内修正，修正速率上限 5%/s | 无感 |
| 上述修正持续 3s 无进展 | 硬重锚（跳过期段） | 有接缝（丢 ≤300ms） |
| `abs(drift) ≥ 250ms` | 硬重锚 | 有接缝 |
| `ratechange` / `seeked` / PTS 跳变 / 时间轴不连续 | **立即**硬重锚 | 有接缝 |
| 音频排不出去（`desired − now > 2s`）持续 3s | 请求 worker 重钉 + 硬重锚 | 有接缝 |

关键修订：`pcm-audio-player.ts:615-617` 的那段注释逻辑必须**恢复为活代码**，阈值取 `REANCHOR_DRIFT_SEC = 0.25`。

### 3.6 P2 原生硬解路径

原生音轨与视频在同一个媒体元素流水线里，**共用同一时钟**，因此：

- 无需任何 PCM 层纠偏；`PCMAudioPlayer` 不实例化。
- live-sync 改 `video.playbackRate` 会让音频同速变调。两条子策略：
  - **P2-a（推荐，默认）**：接受变调（追延迟通常只维持数秒），换来实现简单与低功耗。
  - **P2-b（备选）**：`createMediaElementSource` 接入 WebAudio + WSOLA 保持音高。代价：引入输出延迟（需重新标定）、MSE blob 同源限制、以及"音频必须经 WebAudio 输出"带来的额外风险。仅当实测变调不可接受时启用。

### 3.7 模块划分

| 模块 | 职责 | 状态 |
|---|---|---|
| `audio/audio-sync-core.ts` | 同步内核：队列、span 记账、锚定、重锚、漂移纠偏策略、rVFC 显示延迟探针 | ✅ 已实现（580 行）；**lab 与 player 共用同一份** |
| `audio/pcm-audio-player.ts` | P1 输出后端：设备接线 / 生命周期 / 源 PTS 跳变 / 排不出去的兜底 | ✅ 已收敛（957 → 508 行，排程已交给内核） |
| `audio/audio-router.ts` | codec + 设备能力 → 选 P1/P2 | ✅ 已实现（见 §3.2） |
| `audio/wasm-stretcher.ts` | 伸缩器：连续状态、ratio、输出缓冲（原 WSOLA 封装，**已重新接线**） | ✅ 已接入内核（阶段 2），lab 与 player 均已启用 |
| `audio/native-output.ts` | P2 输出后端：状态上报（不做 PCM） | 阶段 3' 已完成"路由"部分；P2 本身不需要输出代码 |
| `timeline/media-clock.ts` | 唯一时间基准/直播边缘/buffered 工具 | 阶段 4（本次不做） |
| 删除 | `ac3-lab/audio-sync.ts`、`remux/aac-silent.ts`、静音轨分支、autoCalib 残骸 | ✅ 已删 |

---

## 4. 迁移计划

> 顺序：`0 → 3' → 1 → 2`。理由：当前所有同步问题只存在于软解路径上，**先让不需要软解的设备脱离软解**（阶段 3'）等于直接绕开待修的同步层；后再回头修同步层。

### 阶段 0 · 清算 —— ✅ 已完成（2026-09-13）
- 删除静音 AAC 假音轨死代码：`remux/aac-silent.ts`（整文件）、`mp4-remuxer.ts` 的 `_generateSilentAudio`、`_silentAudioMode` / `_silentAudioLastDts` / `_silentAudioDurationResidual` 及相关调用点（1084 → 955 行）。
- 删除 `PCMAudioPlayer` 里的 autoCalib 残骸：`if (false && ...)` 整块、其常量组、字段组、`desired` 公式中的 `+ autoCalibSec`、日志里的 `[autoCalib-OFF]`（1043 → 957 行）。
- 被注释掉的漂移重锚块替换为指向本文件的说明注释。
- **附带修复**：`tsc --noEmit` 由「1 个错误」变为 **0 错误**。原错误正是 `if (false && drift !== null && ...)` 阻止类型收窄导致 `driftWindow.push(number | null)`。
- **验收结果**：`tsc --noEmit` exit 0；`vite build` 通过，player 包体 425.45 → 421.94 kB；ac3-lab 回归正常（AC-3 解码 1203 帧、MSE 错误 0、underrun 0、锚定跳过期段与重锚均生效）。
- 说明：headless Chrome 的音频/视频时钟存在 0.2%/s 级相对漂移，`drift` 绝对值在无头环境仅供参考；最终判定须在真机（真机实测长期稳定在 ±5ms）。

### 阶段 3' · 硬解路径恢复 + 路由 —— ✅ 已完成（实现；正向路径待真机复验）
- 新增内置探测片段 `audio/assets/audio-probe-ac3.mp4` / `audio-probe-eac3.mp4`（各 13.3 KB，ffmpeg 生成，48kHz 立体声 1s，fMP4）。
- 新增 `audio/audio-router.ts`：L1 `isTypeSupported` → L2 内置片段真实解码 + 已解码音频字节数 → L4 `?audioPath=sw|hw`；带 session 级缓存、预热、超时等待、fail-safe。
- `mse-playback-backend.ts` 接线：backend 创建时预热；`loadSegments` 首次装载时取一次结论（≤400ms），确认原生可用则去掉 `wasmDecoders.ac3`；另加一层 `.catch` 保证探测异常也照常装载。
- **验收结果（本机 Chrome/Linux 无 Dolby）**：L1 正确判否（0ms），资产 HTTP 200，fail-safe 生效；用 AAC 片段验证了**正向路径机制**（`sourceopen` → `appendBuffer` → 播放 → `webkitAudioDecodedByteCount > 0`，200ms 内即出现解码字节）。
- **待办**：正向路径需在真有 Dolby 解码器的机型上复验；`navigator.mediaCapabilities.decodingInfo`（L3）尚未纳入。
- `tsc --noEmit` exit 0；构建通过。

### 阶段 1 · 同步内核收敛 —— ✅ 已完成（lab 已复验；player 待真机复验）
1. 抽出 `audio/audio-sync-core.ts`（580 行）：队列 / span 记账 / 锚定 / 重锚 / 纠偏策略 / rVFC 探针。
2. ac3-lab 改为直接使用内核，删除 `ac3-lab/audio-sync.ts`（约 390 行）与本地重复的 rVFC 探针。
3. `PCMAudioPlayer` 用内核替换排程/锚定/漂移（**957 → 508 行**），补齐不变量 2（时钟推进闸）与 3（输出级记账，含 `rate` 参与）。
4. 漂移重锚恢复为活代码，作为内核 `controlTick()` 内的策略（阈值 250ms），**与时钟闸/跳过语义一起上线**。
5. 顺带修正：`resetChain()` 重置时钟闸基准 —— 否则向后 seek 后 `videoClock − lastAnchorClockSec` 为负，锚定会被挡住直到播放头重新越过旧锚点（等于静音）。
6. `feed()` 的源 PTS 跳变检测改为向内核取"队尾结束时间"，保留"队列空即放行"的自愈特性。

**lab 验收结果**（真实 Chrome，同一节目源，25s 稳态）：`drift` min=0.0 / p50=2.6 / max=4.1 ms；underrun 0；reanchor 0（稳态）；drop 5（启动期过期帧）；MSE 错误 0；解码 1210 帧；手动重锚后 drift −9.2ms。无页面异常。
**player 待办**：需真机复验（软解 AC-3 频道跑 ≥30min，观察 drift p50/p95、underrun 与重锚次数）。

### 阶段 2 · WSOLA 上线 —— 🔶 lab 已验证通过；player 待启用 + 真机复验

**第一步实测（✅ 已完成）**：见 §3.4 —— 不存在需要标定的 `L_w`，改用 `wsola_position()` 做权威内容游标；固有代价为 52ms 预填与 ~48ms 尾部丢弃（无 flush 接口）；ratio==1 逐位直通。

**第二步接线（✅ 已完成，内核）**：`audio-sync-core.ts` 增加**可注入**的伸缩级（`stretcherFactory`，默认不传＝完全保持"链式 1:1"行为）：

- 排程统一改成"段"模型（`Segment`）——直通与伸缩后排程共用同一套锚定/跳过/记账逻辑，只有来源不同；
- 记账取 `position` 增量（`streamStart/End`），**不用名义 ratio 推算**，因此与 ratio 变化无关；
- 喂入时**为时间轴缺口补静音**，使「position ↔ 媒体时间」映射保持线性（否则缺口被伸缩器压掉、标签全歪）；
- 小重叠（≤20ms）裁掉前缀、大倒退则重置伸缩器并重新种 `contentBase`；
- 两环速率策略 + 死区 + 变化率限幅（整定见 §3.4 表）。

**第三步 lab 验收（✅ 已完成）**：`ac3-lab` 挂上伸缩器并加了「模拟 live-sync 追速」按钮（直接改 `video.playbackRate`）：

| 阶段 | drift | ratio |
|---|---|---|
| 1.0x 基线 | 7.4 ~ 9.0 ms | **1.000**（死区使其精确停在视频速率） |
| 切 1.2x（24s） | 瞬态 −51.8ms（一个采样）后稳定 **8.3 ~ 16.2 ms** | 收敛到 **1.200** |
| 切回 1.0x | 瞬态 71.6ms 后 **−1.4 / 0.5 ms** | 回到 **1.000** |

计数全程 `underrun 0 / reanchor 0 / drop 6`（仅启动期）——**变速期间没有新增丢帧，也没触发一次硬重锚**。

**第四步播放器启用（✅ 已完成）**：

- `PCMAudioPlayer` 注入 `stretcherFactory`（复用 `config.wasmDecoders` 里的统一 wasm URL）；`source.playbackRate` 跟随自动失效 —— 内核在伸缩级可用时把节点速率固定为 1。
- **回退路径必须保留**：wasm 拿不到 / 创建失败 → 内核退回"链式 1:1 + playbackRate 跟随"（变调但**同步**，不会没声音）。为此 `takeSegment` 与 `nodeRate` 都改为判断 `stretchEnabled`（**工厂存在且未创建失败**）：
  > 只看"有没有传工厂"是个陷阱 —— 创建失败时会既不产出输出、也不走直通，等于**永久静音**。
- 变速时**不再硬重锚**（`onVideoRateChange` 在伸缩级可用时直接返回）：否则每次追速开始都会留下一次接缝（丢 ~52ms 预填 + ~48ms 尾部）。已排好的 span 最多 0.6s 播完，残余漂移交给微调环吸收。

**待办**：
1. **真机听感验证**：无变调、无金属声/接缝（无头环境测不了）；
2. 真机长跑（含 live-sync 追速期），观察 drift 与 reanchor/drop 计数；
3. 若日志出现 `stretcher unavailable`，确认只是变调、同步仍正常（即回退生效）。

### 阶段 4 · 时间轴单一权威（本次不做）
`_pendingDtsOffsetMs` / `_dtsBase` / `timestampOffset` / `mapPcmTimestamp` 收敛进 Timeline 模块。
**建议**：阶段 1~3 稳定运行两周无同步类问题后再评估。

---

## 5. 验证

### 5.1 基线（已实测）

| 场景 | drift | 说明 |
|---|---|---|
| ac3-lab 修复前 | **−190ms 恒定** | 锚定重放缺陷，`reanchors=0` 永不纠正 |
| ac3-lab 修复后 | 启动 −10ms，重锚后 −4~−7ms | 真实 Chrome + Playwright，`/web/ac3-lab.html` |

### 5.2 判定阈值

| 指标 | 目标 |
|---|---|
| `drift` p50 | ≤ 20ms |
| `drift` p95 | ≤ 40ms |
| `underrun` | 长时间播放不增长（≤ 1 次/10min） |
| `reanchor` | 稳态 ≤ 2 次/10min（仅事件触发） |
| `appendError` | 0 |
| 长时间（≥30min） | drift 不单调累积 |
| 变速期 | 无变调（频谱基频不变），ratio 无振荡 |

### 5.3 方法

`ac3-lab` 作为 golden reference（同一份 `audio-sync-core.ts`），固定同一节目源、同一台设备，比对 CSV 指标；player 侧用 `?audioPath=`、`?avOffsetMs=` 做对照实验。

---

## 6. 风险与回退

| 风险 | 缓解 |
|---|---|
| WSOLA 记账再出错（历史坑） | 记账只发生在输出切片级（§3.4）；ac3-lab 提供逐帧可对比的 CSV |
| 探测误判继续走软解 | 探测只影响"是否省 CPU"，不影响"有没有声音"——降级方向永远是软解 |
| L3 热切造成 2s 静音 | 仅在 L1+L2 都通过但 L3 无声时发生；用 `?audioPath=hw` 可复现排查 |
| 两环振荡 | 时间尺度强制分离 + ratio 变化率限幅 |
| 硬重锚接缝感 | 提高 WSOLA 生效区间、重锚阈值可配置；只对事件类立即重锚 |
| 回退 | 全部经 `audio-router` 与 URL 开关可逐条关闭；`defaultConfig` 保持向后兼容 |

---

## 7. 已决策事项与遗留问题

### 已决策

| 问题 | 结论 |
|---|---|
| 是否整体重写 | 否；但 `audio/` 目录整体推掉重写为 5 个小模块 |
| 推进顺序 | `0 → 3' → 1 → 2`，阶段 4 不做 |
| P2 是否做保持音高（P2-b） | **不做**。先看追速时间占比；若超阈值应调 live-sync 参数（`maxLatency: 3` / `target: 1.5` 偏紧）而非上音高补偿 |
| 硬解探测手段 | 内置 1s 探测片段实测；不用 `isTypeSupported` / `addSourceBuffer` 单独判定 |
| 恢复漂移重锚 | 与阶段 1 一起，不单独恢复 |

### 遗留（实测后回填）

1. ~~**WSOLA 固有延迟 `L_w` 是常量还是随 ratio 变化**~~ —— ✅ 已实测关闭：**不存在需要标定的 `L_w`**，改用 `wsola_position()` 作为权威内容游标（见 §3.4）。代价是 52ms 预填与 ~48ms 尾部丢弃。
2. **原生 AC-3 在目标机型（Android TV / 老 WebView）的判定结果** —— 机制已实现，正向路径需在有 Dolby 解码器的机型上复验；若出现"首个频道没拿到硬解"，优先调 `AUDIO_ROUTE_WAIT_MS`，或把决策改为按次装载而非按会话。
3. **`mediaCapabilities.decodingInfo({type:"media-source"})` 是否可信** —— 待与探测片段结论对照（L3 尚未纳入）。
4. **拆分 `wasmDecoders.ac3` 为按 codec 的独立开关** —— 现在两个 codec 共用一个开关，导致"只支持 ac-3、不支持 ec-3"的设备拿不到硬解收益。拆开后可放宽 §3.2 的第 3 条硬规则。
5. ~~**WSOLA 接线方式（阶段 2 待做）**~~ —— ✅ 内核接线、lab 验收、**播放器启用**均已完成（见 §3.4 与 §4 阶段 2）。剩余：**真机听感验证**（无变调/无金属声，无头环境测不了）+ 真机长跑。`source.playbackRate` 路径保留为伸缩器不可用时的回退。
6. **环路增益/死区是否要在真机上重标** —— `RATIO_DRIFT_GAIN=0.6` 是按"死时间 ≈ 0.9s"推的；真机上排程前瞻与设备输出延迟不同，若观察到 drift 小幅周期振荡，先降增益（0.6 → 0.4）再看。
7. **"音频落后画面"方向没有自愈（待定方案）** —— 内核只有 `maybeRebaseAxis`（音频**超前** → 轴平移）；音频**落后**时 `enqueue()`/`reanchor()` 的 stale floor 与 `pump()` 的 `skipFrames >= seg.frames` 会持续把块丢成 `droppedStale`（现场日志 `anchor: dropped N fully-stale PCM chunk(s)`）。而 PCM 轴（`MP4Remuxer._pcmTiming`）是"内容连续 bridging"语义：**上流任何丢音频都只让轴变短、不留缺口**，重钉只发生在音频轨不连续或重建 remuxer 时 —— 于是"视频轴跑到 PCM 轴前面"（视频侧 TS 不连续、seek、分片跳过）可能永久静音。待定方案：① 主线程→worker 新增"重钉 PCM 轴"指令（等价 `_resetPcmOnAudioDiscontinuity`；**不能**用 `rebaseAxis` 反向平移——那会把过期内容按新标签播出去）；② 上流丢音频的闸门（等视频关键帧、`Unknown pts`、坏帧跳过）显式记账并在恢复时请求重钉。
8. **`PCMAudioPlayer.awaitingNewTimeline` 是无超时的粘滞闸（待加兜底）** —— 只有收到 `time <= reanchorAtSec + 2.5s` 的块才解除；视频 seek/时间轴重锚后若新 PCM 标签整体落在窗口之外，`feed()` 会**永久静默丢弃**（连 `anchor:` 日志都不会出现，现场特征就是"日志停在 `reanchor(video-seeked): queue=0` 之后"）。建议加超时 + 丢弃计数告警，并让 `onVideoSeeked` 不再无条件 `force` 重锚（落点相近时保留在播的链）。~~部分兜底~~：2026-09-16 已加静音看门狗（§9 第 3 层），粘滞期间超过 5s 无音频链会被强制重锚；但 `feed()` 层的丢弃计数告警仍未做。
9. **弱网/网络切换的端到端验证** —— §9 的四层自愈需要真机复验：WiFi ↔ 4G/5G 来回切换（含隧道/电梯等真实断网），观察 `Transient stream failure ... reconnect` 日志、`rebase axis`/`silent-stall` 计数与 drift 是否回到 ±20ms；理想情况是切换全程不触发第 4 层（会话重建）。

---

## 9. 网络异常自愈（2026-09-16）

**故障现象**：WiFi 切 4G/5G 后音画永久错位、或画面在走声音不回来、或永久转圈。根因是链路上所有环节都假设"网络不会断"：`fetch-loader` 单次请求失败即判死整条流水线 → 上层整会话重建（上限 3 次、无退避），重建期间无任何时间轴重钉，源 PTS 纪元变化后 PCM 与视频轴分离且无自愈。

分四层加固（由下到上，正常情况下上层永不触发）：

| 层 | 位置 | 触发条件 | 动作 | 关键常量 |
|---|---|---|---|---|
| 1 传输层 | `io/fetch-loader.ts` | 直播流（`resumeMode==="restart"`）请求中途失败 | 指数退避重连（`_internalRestart` → `onRestarted`，解封装/Rmuxer 走 TS 边界桥接） | `RETRY_BASE_DELAY_MS=500`、上限 5 次、`RETRY_MAX_DELAY_MS=8s`；4xx（除 429）不重试 |
| 1' 传输层 | 同上 | 直播流 20s 内一个字节都没到（TCP 静默挂死，网络切换典型表现） | 掐断连接并走同一重连路径 | `LIVE_DATA_TIMEOUT_MS=20s` |
| 2 时间轴 | `worker/pipeline.ts` | 连续直播 TS 重连成功 | `_finishTsInputBoundary()` + **`_resetPcmOnAudioDiscontinuity()`**：源 PTS 纪元不可预知，重钉 PCM 轴到当前播放头 | 复用既有的音频轨不连续路径 |
| 3 音频链 | `audio/pcm-audio-player.ts` | 曾成功排程过 + 5s 无可听内容（队列空、无前瞻）+ 视频时钟在推进 + `readyState≥HAVE_FUTURE_DATA` | `reanchor("silent-stall", true)`：按屏上帧时间重建链，后续 PCM 直接锚定播放头 | `SILENT_STALL_MS=5s`、`SILENT_STALL_MIN_UPTIME_MS=3s` |
| 4 播放层 | `components/player/video-player.tsx` | 播放时钟 12s 无推进（可见、未暂停、非 PiP） | 有前向缓冲且解码计数在动 → 补播一次；否则按直播边缘/当前位置重建会话 | `STALL_DETECT_MS=12s`、冷却 30→60→120→180s |

**为什么第 1 层是主路径**：只要重连在 12s 内成功，元素、MSE 缓冲、音频链全部保持，用户只会看到一次短暂卡顿而不是重建；第 3、4 层只是"永不永久卡死"的保险。

**不做的事**（避免回退既有结论）：不碰 `AudioSyncCore` 的锚定/记账不变量；重连不使用 `rebaseAxis` 反向平移（会把过期内容按新标签播出去）；VOD/回看不做字节流重连（重复内容），仍由上层按位置重建。

### 9.1 音频"卡顿一下"的根因修正（同日后续）

**现象**：播放中偶发"丢帧 + 声音卡顿一下"，但音画保持同步。

**根因**：`_resetPcmOnAudioDiscontinuity`（5b0d973 引入的"断流恢复自愈"）是**无条件**的
——TS 流里任何一次"audio discontinuity"（连续性计数器抖动、丢一个包、PES 重整、重连）
都会：`resetPcmTiming()` + 解码器 carry 清零 + 把 PCM 轴重钉到播放头。这等于**每次都
丢弃当前已解码的 PCM 缓冲**，听感正是"卡顿一下"（音画仍同步，因为重钉本身是对齐的）。

**修正**（`pipeline._reanchorPcmIfNeeded` + `MP4Remuxer.probeNextPcmOutput`）：

| 判据（PCM 游标 − 播放头） | 动作 |
|---|---|
| −3s ≤ Δ ≤ +36s（正常预解码余量 / 小幅抖动） | **保留时间轴**，交给 `mapPcmTimestamp` 的"内容连续 bridging"吸收（与 AC-3 周期 PTS 相位跳变同款），无可听接缝 |
| Δ > +36s（超过 `LEAD_BUFFER_AHEAD_MS` 的合法上限）或 Δ < −3s（PCM 已跟不上播放头） | 真脱轴：`resetPcmTiming` + 钉到播放头（原 5b0d973 语义） |
| 播放头未知（首帧前） | 保守重置 |

同时修正两处会**放大**卡顿的判据缺陷：

1. `PCMAudioPlayer.audioStallWatchdog` 的"推进标记"原用 `nextAudibleStreamSec()`（队头/链尾）
   —— 自动推进时它可能长时间不变，会把正常播放误判成停摆并强制重锚（重锚本身即一次卡顿）。
   改为内核新增的 `contentCursorSec()`（内容游标：已排程链尾 → 已产出尾 → 队头），
   只在**真实前进**时刷新进度时间戳。
2. 视频轨不连续分支原本**不会**清软解音频解码器 carry（`else` 挂在 `if (track === "audio")`
   上导致永假）—— 修正为视频/音频两条分支各自正确处理。

**诊断**：`PCMAudioPlayer` 每 ~60s 的诊断日志新增各丢弃环节的**增量** WARN
（`Audio drops in last ~60s: worker[remux/trim/ovf/gen/carry] player[stale/underrun/reanchor/stall]`）
—— 全零即说明卡顿不来自软解 PCM 链路（应查 MSE / bridging），非零则直接定位环节。

---

## 附：关键常量

```ts
// audio-sync-core
SCHEDULE_AHEAD_SEC              = 0.6    // 已排程音频的前瞻窗口
MAX_SCHEDULE_LEAD_SEC           = 2      // 音频远在播放头之前则不排程
REANCHOR_DRIFT_SEC              = 0.25   // 硬重锚阈值（恢复为活代码）
REANCHOR_MIN_INTERVAL_MS        = 1500
STALE_KEEP_SEC                  = 0.05
MIN_ANCHOR_CLOCK_ADVANCE_SEC    = 0.1    // 锚定前视频时钟必须前进的量
CLOCK_STALE_MS                  = 150
WSOLA_CORRECT_MIN_DRIFT_SEC     = 0.015  // 进入微调区间
WSOLA_RATIO_TRIM                = 0.08   // 微调带宽 ±8%
WSOLA_RATIO_SLEW_PER_SEC        = 0.02   // ratio 变化率上限
```

> 注：`REANCHOR_DRIFT_SEC` 与 autoCalib 常量组已在阶段 0 随死代码一并移除，阶段 1 恢复漂移重锚时重新引入。

---

## 8. 变更记录

| 日期 | 版本 | 内容 |
|---|---|---|
| 2026-09-13 | v1 | 首版草案（`doc/audio-sync-fix-plan.md`）：基于「lab 的 -190ms 是正常偏置」这一错误前提，方案方向有误 |
| 2026-09-13 | v2 | 本文档。修正前提（-190ms 是锚定重放缺陷造成的真实滞后，已修复并实测）；确定不整体重写；补齐 WSOLA 接线设计与硬解探测方案 |
| 2026-09-13 | v2 +阶段0 | 完成阶段 0 清算（死代码删除、`tsc` 归零、ac3-lab 回归通过）；§3.2 探测方案改为内置探测片段；推进顺序定为 `0 → 3' → 1 → 2` |
| 2026-09-13 | v2 +阶段3' | 完成阶段 3' 实现：内置探测片段 + `audio/audio-router.ts` + 后端接线；实测修正 `sourceopen` 监听顺序缺陷（否则真机硬解设备会被误判为不支持）；fail-safe 三条硬规则固化为设计要求 |
| 2026-09-13 | v2 +阶段1 | 完成阶段 1：抽出 `audio/audio-sync-core.ts` 供 lab/player 共用；删除 `ac3-lab/audio-sync.ts`；`PCMAudioPlayer` 957→508 行；恢复漂移重锚（含时钟闸）；修正 `resetChain()` 时钟闸基准。lab 实测 drift p50=2.6ms、underrun 0、MSE 错误 0 |
| 2026-09-13 | v2 +阶段2测 | 完成阶段 2 第一步实测：`wsola_position()` 语义成立（`position = out×ratio`），**无 `L_w` 需标定**，改用它做权威内容游标；实测固有代价 = 52ms 预填 / ~48ms 尾部丢弃（无 flush 接口）/ ratio==1 逐位直通。§3.4 记账公式与约束表据此改写，§7 遗留项 1 关闭 |
| 2026-09-13 | v2 +阶段2线 | 内核加入可注入伸缩级（`stretcherFactory`，默认关闭）；排程改为统一"段"模型；缺口补静音保证 position↔媒体时间线性。lab 挂上伸缩器并通过验收（1.2x 追速 drift 收敛到 8~16ms、ratio→1.200、0 丢帧 0 重锚）。实测发现并修正环路极限环：`K×θ<1` + 20ms 死区（K 1.5→0.6）。§3.4 增环路整定表 |
| 2026-09-13 | v2 +阶段2启 | 播放器启用 WSOLA：注入 `stretcherFactory`，`source.playbackRate` 跟随退居回退路径。修正"创建失败即永久静音"的实现缺陷（`stretchEnabled` 同时判断"已注入"与"未失败"）；伸缩级可用时变速不再硬重锚。lab 回归复验通过（ratio→1.200、0 丢帧 0 重锚）。待真机听感 + 长跑 |
| 2026-09-15 | v2 +软解统一 | MP2 由"整段转发 PES payload"（WASM parser 切帧 + PES PTS 打标签）改为与 AC-3/E-AC-3 **同模型**：demuxer 按 MPEG 帧头切帧（`demux/mp3.ts` 新增 `MP3Parser`，覆盖 MPEG-1/2/2.5 × Layer I/II/III）+ 逐帧 PTS 外推 + 跨 PES carry + 坏帧跳过并推进 PTS。逐帧标签精度由 `samplesBeforeInput` 回退保证（WASM parser 的一帧延迟不改变帧起点）。三条软解路径在 demuxer → WASM → PCM 时间轴上行为一致 |
| 2026-09-16 | v2 +网络自愈 | **网络异常自愈加固**（背景：WiFi → 4G/5G 切换后音画再也对不齐/无声/永久卡转圈）。四层：① `io/fetch-loader.ts` 直播流传输层自动重连（指数退避 0.5→8s、上限 5 次、4xx 不重试）+ 20s 无数据看门狗（静默挂死主动掐断重试）；② `worker/pipeline.ts` 连续直播 TS 重连后 `_reanchorPcmIfNeeded()` 按需重钉 PCM 轴（源 PTS 纪元不可预知时避免与新视频轴分离）；③ `audio/pcm-audio-player.ts` 静音看门狗（曾出过声 + 5s 无可听内容 + 视频时钟在推进 → `reanchor("silent-stall")`）；④ `components/player/video-player.tsx` 播放停摆看门狗（12s 时钟无推进 → 有缓冲补播 / 无缓冲按直播边缘重建，30→180s 退避）。参见 §9 |
| 2026-09-16 | v2 +卡顿修正 | **修复"播放中声音卡顿一下"**：`_resetPcmOnAudioDiscontinuity` 原为无条件重钉，任何 audio discontinuity（计数抖动/丢包/重连）都丢弃已解码 PCM 缓冲 → 可听卡顿。改为**有界判定**（`_reanchorPcmIfNeeded` 比较 PCM 游标与播放头：±(-3s,36s] 内保留时间轴由 bridging 吸收，越界才重钉）；修正 `audioStallWatchdog` 的推进标记（改用内核新增 `contentCursorSec()`，避免误判正常播放为停摆）；修正视频轨不连续分支不清音频解码器 carry 的死分支；新增每 60s 各环节丢弃增量 WARN 诊断（定位卡顿来自 worker 还是主线程）。参见 §9.1 |
