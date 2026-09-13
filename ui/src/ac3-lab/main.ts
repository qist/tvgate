/*
 * AC-3 Lab — 独立测试页（不改动 playback-engine 任何代码，只读复用其中的模块）
 *
 * 目标：在任意 AC-3 节目源上，验证一套"能正常出声、不卡顿、音画同步"的最小实现。
 *
 * 与正式播放器的差异（刻意为之）：
 *   - 视频：只把**视频**轨 remux 进 MSE（不塞静音 AAC 假音轨），播放时钟不再被假音轨影响。
 *   - 音频：TS 里解析出的**每一个 AC-3 完整帧**单独喂给 WASM 解码，因此每一帧都有
 *     确定的 PTS，不再需要"PCM 外推 / 跨 PES carry / 相位跳变吸收"那套推导。
 *   - 同步：见 audio-sync.ts —— 只有一个显式 calibration 常量 + 硬重锚，不做 WSOLA。
 *
 * 运行：npm run build 后打开 /web/ac3-lab.html
 *       dev: npm run dev 后打开 /ac3-lab.html
 * 换源：页面顶部「节目 key」输入框 + 切换（回车亦可）；或直接 ?src=/player/<key>
 */

import { AudioSyncCore } from "../playback-engine/audio/audio-sync-core";
import { WasmStretcher } from "../playback-engine/audio/wasm-stretcher";
import { AvcodecAudioDecoder } from "../playback-engine/decoder/avcodec-audio-decoder";
import TSDemuxer from "../playback-engine/demux/ts-demuxer";
import MP4Remuxer from "../playback-engine/remux/mp4-remuxer";
import avcodecWasmUrl from "../playback-engine/wasm/avcodec/avcodec_audio.wasm?url";

const el = <T extends HTMLElement>(id: string): T => {
  const node = document.getElementById(id);
  if (!node) throw new Error(`missing element #${id}`);
  return node as T;
};

const video = el<HTMLVideoElement>("video");
const statusEl = el<HTMLDivElement>("status");
const metricsEl = el<HTMLTableSectionElement>("metrics");
const logEl = el<HTMLPreElement>("log");
const playBtn = el<HTMLButtonElement>("play");
const calibInput = el<HTMLInputElement>("calib");
const calibValueEl = el<HTMLSpanElement>("calibVal");
const reanchorBtn = el<HTMLButtonElement>("reanchor");
const csvBtn = el<HTMLButtonElement>("csv");
const srcKeyInput = el<HTMLInputElement>("srcKey");
const switchSrcBtn = el<HTMLButtonElement>("switchSrc");
const srcHintEl = el<HTMLSpanElement>("srcHint");

const params = new URLSearchParams(location.search);
const SOURCE = params.get("src") ?? "/player/96cb616c72a2";
/** 预填当前 key（SOURCE 形如 /player/<12hex> 时提取）。 */
const currentKey = SOURCE.match(/\/player\/([0-9a-fA-F]{8,})/)?.[1] ?? "";
srcKeyInput.value = currentKey;

/** 已订阅频道缓存（供深链 token / 频道名反查真正的服务端 key）。 */
type ChannelInfo = { key: string; name: string; group?: string };
let channelList: ChannelInfo[] = [];

/**
 * 复刻 player-deep-link.ts 的深链编码（组名/频道名 → 12hex），用于把分享链接里的
 * token 反查回真正的服务端 key。lab 用 /player/<key> 拉流，而播放页分享的是这个 token。
 */
function encodeDeepLinkId(group: string, name: string): string {
  const input = `${group}/${name}`;
  let h1 = 0x811c9dc5;
  for (let i = 0; i < input.length; i++) {
    h1 ^= input.charCodeAt(i);
    h1 = Math.imul(h1, 0x01000193);
  }
  let h2 = 5381;
  for (let i = 0; i < input.length; i++) {
    h2 = (Math.imul(h2, 33) ^ input.charCodeAt(i)) >>> 0;
  }
  return (h1 >>> 0).toString(16).padStart(8, "0") + ((h2 >>> 0) & 0xffff).toString(16).padStart(4, "0");
}

/** 把用户输入（key / 深链 token / 频道名 / 组名/频道名）解析成真正的服务端 key；找不到返回 null。 */
function resolveChannelKey(token: string): string | null {
  const t = token.trim().toLowerCase();
  if (!t) return null;
  let ch = channelList.find((c) => c.key.toLowerCase() === t);
  if (ch) return ch.key;
  ch = channelList.find((c) => encodeDeepLinkId(c.group ?? "", c.name) === t);
  if (ch) return ch.key;
  const slash = t.indexOf("/");
  if (slash > 0) {
    const g = t.slice(0, slash);
    const n = t.slice(slash + 1);
    ch = channelList.find((c) => c.name.trim().toLowerCase() === n && (c.group ?? "").trim().toLowerCase() === g);
    if (ch) return ch.key;
  }
  ch = channelList.find((c) => c.name.trim().toLowerCase() === t);
  if (ch) return ch.key;
  return null;
}

/**
 * 从 /api/player/channels 拉取已订阅频道，填进 datalist，避免手敲不存在的 key 导致 403。
 * 403 = 该 key 未在当前订阅中注册（需先在后台订阅该频道，才会进 /player/<key>）。
 */
const channelKeysList = el<HTMLDataListElement>("channelKeys");
async function loadChannelList(): Promise<void> {
  try {
    const text = await fetchText("/api/player/channels");
    const data = JSON.parse(text) as { channels?: Array<{ key: string; name: string; group?: string }> };
    const channels = data.channels ?? [];
    channelList = channels;
    channelKeysList.innerHTML = channels
      .map((c) => `<option value="${c.key}">${c.name}${c.group ? ` · ${c.group}` : ""}</option>`)
      .join("");
    srcHintEl.textContent = `已订阅 ${channels.length} 个（403=未订阅）`;
    log(`channel list loaded: ${channels.length} channels`);
  } catch (error) {
    srcHintEl.textContent = "拉取频道列表失败";
    log(`load channel list FAILED: ${String(error)}`);
  }
}

const logLines: string[] = [];
function log(message: string): void {
  const line = `${new Date().toISOString().slice(11, 23)} ${message}`;
  logLines.push(line);
  if (logLines.length > 400) logLines.shift();
  logEl.textContent = logLines.join("\n");
  // eslint-disable-next-line no-console
  console.log("[ac3-lab]", message);
}

function setStatus(message: string): void {
  statusEl.textContent = message;
}

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

// ==================== 视频：MSE（仅视频轨） ====================

class VideoOnlyMse {
  private readonly mediaSource = new MediaSource();
  private sourceBuffer: SourceBuffer | null = null;
  private open = false;
  private codec = "";
  private container = "video/mp4";
  private readonly pending: ArrayBuffer[] = [];
  private errorCount = 0;

  get appendErrorCount(): number {
    return this.errorCount;
  }

  /** 当前已缓冲到的最后一个时间点（秒）；无缓冲返回 0。 */
  bufferedEnd(): number {
    const sb = this.sourceBuffer;
    if (!sb || sb.buffered.length === 0) return 0;
    return sb.buffered.end(sb.buffered.length - 1);
  }

  constructor(videoEl: HTMLVideoElement) {
    videoEl.src = URL.createObjectURL(this.mediaSource);
    this.mediaSource.addEventListener("sourceopen", () => {
      this.open = true;
      log("MSE sourceopen");
      this.ensureSourceBuffer();
    });
    this.mediaSource.addEventListener("sourceclose", () => log("MSE sourceclose"));
  }

  appendInit(data: ArrayBuffer, codec: string, container: string): void {
    this.codec = codec;
    this.container = container;
    this.pending.push(data);
    this.ensureSourceBuffer();
    this.flush();
  }

  append(data: ArrayBuffer): void {
    this.pending.push(data);
    this.flush();
  }

  private ensureSourceBuffer(): void {
    if (this.sourceBuffer || !this.open || !this.codec) return;
    const mime = `${this.container};codecs="${this.codec}"`;
    try {
      this.sourceBuffer = this.mediaSource.addSourceBuffer(mime);
    } catch (error) {
      log(`addSourceBuffer FAILED (${mime}): ${String(error)}`);
      setStatus(`浏览器不支持该视频轨: ${mime}`);
      return;
    }
    this.sourceBuffer.mode = "segments";
    this.sourceBuffer.addEventListener("updateend", () => this.flush());
    this.sourceBuffer.addEventListener("error", () => {
      this.errorCount++;
      log("SourceBuffer error");
    });
    log(`SourceBuffer ready: ${mime}`);
  }

  private flush(): void {
    const sb = this.sourceBuffer;
    if (!sb || sb.updating) return;
    const next = this.pending.shift();
    if (!next) return;
    try {
      sb.appendBuffer(next);
    } catch (error) {
      this.errorCount++;
      log(`appendBuffer FAILED: ${String(error)}`);
    }
  }
}

// ==================== HLS 拉流 ====================

async function fetchText(url: string): Promise<string> {
  const response = await fetch(url, { cache: "no-store" });
  if (!response.ok) throw new Error(`HTTP ${response.status} ${url}`);
  return response.text();
}

async function fetchBytes(url: string): Promise<Uint8Array> {
  const response = await fetch(url, { cache: "no-store" });
  if (!response.ok) throw new Error(`HTTP ${response.status} ${url}`);
  return new Uint8Array(await response.arrayBuffer());
}

/** 主播放列表里的第一个 variant URI（非 master 返回 null）。 */
function firstVariant(text: string): string | null {
  const lines = text.split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    if (!lines[i].startsWith("#EXT-X-STREAM-INF")) continue;
    for (let j = i + 1; j < lines.length; j++) {
      const line = lines[j].trim();
      if (!line || line.startsWith("#")) continue;
      return line;
    }
  }
  return null;
}

function mediaSegmentUris(text: string): string[] {
  const out: string[] = [];
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (line && !line.startsWith("#")) out.push(line);
  }
  return out;
}

// ==================== 主流程 ====================

async function main(): Promise<void> {
  log(`source = ${SOURCE}`);

  const ctx = new AudioContext();
  // 与播放引擎共用同一份同步内核（doc/PLAYER-SYNC-REDESIGN.md 阶段 1）。
  // 队列领先上限不设：lab 在 MSE 层已有 10s 限速（waitForBufferRoom），保持行为不变。
  //
  // 阶段 2：挂上 WSOLA 伸缩级（可注入、可关闭）。注入后：
  //   - 视频速率（video.playbackRate）由伸缩器以保音高方式跟随；
  //   - 节点 playbackRate 恒为 1，残余漂移在 ±8% 带内无感修正；
  //   - 注释掉 stretcherFactory 一行即可回到"链式 1:1"做 A/B。
  const audioSync = new AudioSyncCore({
    ctx,
    video,
    onLog: log,
    getRate: () => video.playbackRate || 1,
    stretcherFactory: async (sampleRate, channels) => {
      try {
        return await WasmStretcher.create(avcodecWasmUrl, sampleRate, channels);
      } catch (error) {
        log(`WSOLA 不可用，退回链式排程: ${String(error)}`);
        return null;
      }
    },
  });
  audioSync.start();

  const mse = new VideoOnlyMse(video);

  let demuxer: TSDemuxer | null = null;
  let remuxer: MP4Remuxer | null = null;
  let audioDecoder: AvcodecAudioDecoder | null = null;
  let decoderReady: Promise<void> | null = null;

  const ac3Frames: Array<{ unit: Uint8Array; pts: number }> = [];
  let ac3FrameCount = 0;

  function handleAc3Frames(): void {
    if (!remuxer || !audioDecoder) return;
    while (ac3Frames.length > 0) {
      const frame = ac3Frames.shift() as { unit: Uint8Array; pts: number };
      const decoded = audioDecoder.decode(frame.unit);
      if (!decoded) continue;
      const durationMs = (decoded.samplesPerChannel / decoded.sampleRate) * 1000;
      const mapping = remuxer.mapPcmTimestamp(frame.pts, durationMs);
      if (!mapping || mapping.action === "drop") continue;
      audioSync.enqueue({
        pcm: decoded.pcm,
        channels: decoded.channels,
        sampleRate: decoded.sampleRate,
        timeSec: mapping.time,
        durationSec: durationMs / 1000,
      });
      ac3FrameCount++;
      if (ac3FrameCount === 1) log(`first AC-3 frame: ${decoded.sampleRate}Hz/${decoded.channels}ch @ MSE ${mapping.time.toFixed(3)}s`);
    }
  }

  async function ensurePipeline(firstChunk: Uint8Array): Promise<void> {
    const probe = TSDemuxer.probe(firstChunk);
    if (!probe.match) throw new Error("首个分片不是 MPEG-TS");

    demuxer = new TSDemuxer(probe as ConstructorParameters<typeof TSDemuxer>[0], {
      waitForInitialVideoKeyframe: true,
    });
    demuxer.timestampBase = 0;
    demuxer.onError = (type, info) => log(`demux error ${type}: ${info}`);
    // 源在插播/切源时会出现时间轴不连续：不通知 remuxer 的话，输出时间戳可能
    // 后退/重叠，MSE 会停在最后一个有效点上不再前进（视频冻死）。
    demuxer.onTrackDiscontinuity = (track) => {
      log(`track discontinuity: ${track}`);
      if (track === "video") {
        remuxer?.flushStashedSamples();
        remuxer?.insertDiscontinuity();
      }
      if (track === "audio") {
        // audio discontinuity：源流时间轴断裂后，旧的 _pcmTiming 会把新帧
        // 的 PTS 桥接到旧时间轴上 → 音画脱节。必须重置 PCM timing，让下一帧
        // 重新按当前视频时间锚定。同时清空已捕获的 AC-3 帧缓冲和音频队列。
        remuxer?.resetPcmTiming();
        ac3Frames.length = 0;
        audioSync.reanchor("audio-discontinuity", true);
        log("audio discontinuity: reset PCM timing + re-anchored AudioSyncCore");
      }
    };

    remuxer = new MP4Remuxer();
    remuxer.onInitSegment = (type, segment) => {
      if (type === "video") mse.appendInit(segment.data as ArrayBuffer, segment.codec, segment.container);
    };
    remuxer.onMediaSegment = (type, segment) => {
      if (type === "video") mse.append(segment.data as ArrayBuffer);
    };
    remuxer.bindDataSource(demuxer as unknown as Parameters<MP4Remuxer["bindDataSource"]>[0]);

    // bindDataSource 把 onDataAvailable 设成了 remux；这里包一层，先把这一批 AC-3 帧
    // 的引用取出来（remux 之后会清空 track.samples），再交给 remux，最后才解码 ——
    // 保证解码时 mapPcmTimestamp 所需的 dts base 已经建立。
    const innerRemux = demuxer.onDataAvailable as (audioTrack: unknown, videoTrack: unknown) => void;
    demuxer.onDataAvailable = ((audioTrack: { samples?: Array<{ unit: Uint8Array; pts: number }> }, videoTrack: unknown) => {
      const captured = audioTrack?.samples?.length ? audioTrack.samples.slice() : [];
      innerRemux(audioTrack, videoTrack);
      for (const sample of captured) ac3Frames.push({ unit: sample.unit, pts: sample.pts });
      handleAc3Frames();
    }) as typeof demuxer.onDataAvailable;

    audioDecoder = new AvcodecAudioDecoder(avcodecWasmUrl, "ac3");
    decoderReady = audioDecoder.ready.then(() => log("AC-3 WASM decoder ready"));
    await decoderReady;
  }

  const seenSegments = new Set<string>();
  let bytesFed = 0;

  async function feedSegment(url: string): Promise<void> {
    const bytes = await fetchBytes(url);
    if (!demuxer) await ensurePipeline(bytes);
    demuxer?.parseChunks(bytes, bytesFed);
    bytesFed += bytes.byteLength;
    demuxer?.flushSegmentBoundary();
    remuxer?.flushStashedSamples();
  }

  let feedRunning = true;

  /**
   * 直播限速：不让解复用/解码跑到播放头前面太远。
   * 不限速时（实测）音频时间戳会跑到播放头前 ~50s，于是排程护栏拒绝一切样本、
   * 队列又被丢前端 → 彻底没声音。限速后队列自然被压在小范围内。
   */
  const MAX_BUFFER_AHEAD_SEC = 10;
  async function waitForBufferRoom(): Promise<void> {
    while (feedRunning) {
      const ahead = mse.bufferedEnd() - video.currentTime;
      if (ahead <= MAX_BUFFER_AHEAD_SEC) return;
      await sleep(250);
    }
  }

  async function feedLoop(): Promise<void> {
    let currentUrl = new URL(SOURCE, location.origin).href;
    let idleRounds = 0;
    while (feedRunning) {
      try {
        const text = await fetchText(currentUrl);
        const variant = firstVariant(text);
        if (variant) {
          currentUrl = new URL(variant, currentUrl).href;
          log(`master playlist → variant: ${currentUrl}`);
          continue;
        }
        if (text.includes("#EXT-X-DISCONTINUITY")) log("playlist contains #EXT-X-DISCONTINUITY");
        let consumed = 0;
        for (const uri of mediaSegmentUris(text)) {
          const absolute = new URL(uri, currentUrl).href;
          if (seenSegments.has(absolute)) continue;
          seenSegments.add(absolute);
          await waitForBufferRoom();
          if (!feedRunning) break;
          await feedSegment(absolute);
          consumed++;
          if (!feedRunning) break;
        }
        if (seenSegments.size > 4000) seenSegments.clear();
        if (consumed === 0) {
          idleRounds++;
          if (idleRounds % 5 === 0) log(`playlist has no new segment (idle ${idleRounds})`);
          await sleep(1000);
        } else {
          idleRounds = 0;
        }
      } catch (error) {
        log(`feed error: ${String(error)}`);
        await sleep(1500);
      }
    }
  }

  // ---- rVFC 显示延迟探针已移入同步内核（AudioSyncCore.start()），此处不再重复实现 ----

  // ---- 控制环：只做"漂移过大就硬重锚"，不做速率微调 ----
  const rows: string[] = [];
  const csvHeader =
    "wallMs,videoTime,visibleVideo,heard,driftMs,outputLatencyMs,displayLeadMs,scheduledAheadMs," +
    "underruns,reanchors,droppedStale,decodedFrames,readyState,paused,bufferedEnd,appendErrors";

  setInterval(() => {
    // 排程 + "漂移过大就硬重锚"的纠偏策略都在内核里（与播放引擎共用同一份）
    audioSync.controlTick();
    const drift = audioSync.driftSec();
    const stats = audioSync.stats();
    const heard = audioSync.heardStreamTime();
    const visible = audioSync.visibleVideoTime();
    const outputLatency = audioSync.getOutputLatencySec();
    metricsEl.innerHTML = [
      ["video.currentTime", video.currentTime.toFixed(3)],
      ["visibleVideoTime(屏幕帧)", visible.toFixed(3)],
      ["heardStreamTime(喇叭)", heard === null ? "—" : heard.toFixed(3)],
      ["drift (heard-visible)", drift === null ? "—" : `${(drift * 1000).toFixed(1)} ms`],
      ["calibration", `${audioSync.getCalibrationMs().toFixed(0)} ms`],
      ["outputLatency", `${(outputLatency * 1000).toFixed(0)} ms`],
      ["displayLead(实测)", `${(audioSync.getDisplayLeadSec() * 1000).toFixed(0)} ms`],
      ["scheduledAhead", `${(stats.scheduledAheadSec * 1000).toFixed(0)} ms`],
      ["queue", `${(stats.queueSec * 1000).toFixed(0)} ms`],
      [
        "WSOLA ratio",
        `${audioSync.getStretcherRatio().toFixed(3)}${audioSync.isStretching() ? "" : "（未启用）"}`,
      ],
      ["underrun / reanchor / drop", `${stats.underruns} / ${stats.reanchors} / ${stats.droppedStale}`],
      ["视频 readyState / paused", `${video.readyState} / ${video.paused}`],
      ["缓冲领先", `${(mse.bufferedEnd() - video.currentTime).toFixed(1)} s（上限 10s）`],
      ["MSE 错误次数", String(mse.appendErrorCount)],
      ["已解码 AC-3 帧", String(ac3FrameCount)],
      ["已喂字节", `${(bytesFed / 1024 / 1024).toFixed(1)} MB`],
    ]
      .map(([k, v]) => `<tr><th>${k}</th><td>${v}</td></tr>`)
      .join("");
    rows.push(
      [
        performance.now().toFixed(0),
        video.currentTime.toFixed(3),
        visible.toFixed(3),
        heard === null ? "" : heard.toFixed(3),
        drift === null ? "" : (drift * 1000).toFixed(1),
        (outputLatency * 1000).toFixed(0),
        (audioSync.getDisplayLeadSec() * 1000).toFixed(0),
        (stats.scheduledAheadSec * 1000).toFixed(0),
        String(stats.underruns),
        String(stats.reanchors),
        String(stats.droppedStale),
        String(ac3FrameCount),
        String(video.readyState),
        video.paused ? "1" : "0",
        mse.bufferedEnd().toFixed(2),
        String(mse.appendErrorCount),
      ].join(","),
    );
  }, 250);

  // ---- UI ----
  playBtn.addEventListener("click", async () => {
    try {
      await ctx.resume();
      await video.play();
      setStatus("播放中");
      log(`play: ctx=${ctx.state} video=${video.currentTime.toFixed(3)}`);
    } catch (error) {
      log(`play FAILED: ${String(error)}`);
    }
  });

  calibInput.addEventListener("input", () => {
    const ms = Number(calibInput.value);
    calibValueEl.textContent = `${ms} ms`;
    audioSync.setCalibrationMs(ms);
  });

  // 模拟 live-sync 追速：改 video.playbackRate，内核应立刻让 WSOLA ratio 跟上，
  // 从而在"画面变速"的情况下 drift 保持不漂（且音频不变调）。
  const rateHintEl = el<HTMLSpanElement>("rateHint");
  for (const btn of document.querySelectorAll<HTMLButtonElement>("[data-rate]")) {
    btn.addEventListener("click", () => {
      const rate = Number(btn.dataset.rate) || 1;
      video.playbackRate = rate;
      rateHintEl.textContent = `video.playbackRate = ${video.playbackRate.toFixed(2)}`;
      log(`set video.playbackRate = ${video.playbackRate}`);
    });
  }

  reanchorBtn.addEventListener("click", () => audioSync.reanchor("manual"));

  switchSrcBtn.addEventListener("click", () => {
    // 清掉用户可能多敲的 # / 空白 / 末尾片段，频道 key 是纯 hex。
    const raw = srcKeyInput.value.trim().replace(/^#/, "").replace(/\s+/g, "");
    if (!raw) return;
    // 兼容三种写法：纯 key（96cb616c72a2）、含 /player/ 的路径、或完整 http(s) URL。
    let normalized: string;
    let probeKey: string | null = null;
    if (raw.includes("/player/") || raw.startsWith("http")) {
      normalized = raw;
    } else {
      // 先按"key / 深链 token（组名+频道名编码）/ 频道名 / 组名/频道名"在频道列表里反查真正的服务端 key。
      const resolved = resolveChannelKey(raw);
      let key: string;
      if (resolved) {
        key = resolved;
        if (key.toLowerCase() !== raw.toLowerCase()) log(`resolved "${raw}" → key ${key}`);
      } else {
        // 列表里匹配不到：当作裸 key 直接试（可能未订阅 → 会被预检 403 拦下）。
        key = raw.replace(/[^0-9a-fA-F]/g, "");
        if (!key) {
          log(`invalid channel: ${raw}（既不是 key，也不在频道列表）`);
          srcHintEl.textContent = `⚠ ${raw} 无法识别`;
          return;
        }
        log(`"${raw}" 未在频道列表匹配，按裸 key 尝试: ${key}`);
      }
      probeKey = key;
      normalized = `/player/${key}`;
    }
    // 切换前预检：未注册的 key 服务端会 403，提前告诉用户，避免重载后进死循环拉流。
    if (probeKey) {
      void (async () => {
        try {
          const res = await fetch(`/player/${probeKey}`, { method: "GET", cache: "no-store" });
          if (res.status === 403) {
            log(`pre-check 403: ${probeKey} 未订阅（不会出现在 /api/player/channels）`);
            srcHintEl.textContent = `⚠ ${probeKey} 未订阅 → 403`;
            if (!confirm(`${probeKey} 在当前订阅中不存在（服务端返回 403）。\n仍要强制切换（会一直 403）？`)) {
              return;
            }
          } else if (!res.ok) {
            log(`pre-check ${res.status} for ${probeKey}`);
          } else {
            srcHintEl.textContent = "已订阅，可切换";
          }
        } catch (error) {
          log(`pre-check fetch FAILED: ${String(error)}`);
        }
        const url = new URL(location.href);
        url.searchParams.set("src", normalized);
        log(`switching source → ${normalized}`);
        location.href = url.toString();
      })();
      return;
    }
    const url = new URL(location.href);
    url.searchParams.set("src", normalized);
    log(`switching source → ${normalized}`);
    location.href = url.toString();
  });
  srcKeyInput.addEventListener("keydown", (event) => {
    if (event.key === "Enter") switchSrcBtn.click();
  });

  csvBtn.addEventListener("click", () => {
    const blob = new Blob([[csvHeader, ...rows].join("\n")], { type: "text/csv" });
    const link = document.createElement("a");
    link.href = URL.createObjectURL(blob);
    link.download = `ac3-lab-${Date.now()}.csv`;
    link.click();
  });

  video.addEventListener("error", () => log(`video element error: ${video.error?.message ?? "unknown"}`));
  video.addEventListener("waiting", () => log("video waiting"));
  video.addEventListener("playing", () => log("video playing"));
  video.addEventListener("stalled", () => log("video stalled"));
  video.addEventListener("pause", () => log("video pause"));
  video.addEventListener("seeked", () => log(`video seeked → ${video.currentTime.toFixed(3)}`));

  setStatus("拉流中…（点「播放」开始）");
  void loadChannelList();
  void feedLoop();
}

void main().catch((error) => {
  log(`fatal: ${String(error)}`);
  setStatus(`启动失败：${String(error)}`);
});
