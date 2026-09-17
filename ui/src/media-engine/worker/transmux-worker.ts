/**
 * 转封装 Worker 入口。
 * 在 worker 线程内运行「HLS 拉流 → demux → remux → 软解」，把 init/media 段、PCM、媒体信息以
 * transferable 方式回传主线程；主线程只做 MSE append 与 WebAudio 排程，避免重活阻塞 UI。
 */

import { HlsSource } from "../hls/hls-source";
import { TransmuxPipeline, type SoftAudioChunk } from "../pipeline/transmux-pipeline";
import { DecoderRegistry, WasmAudioDecoder } from "../decoder/wasm-audio-decoder";
import { createFfmpegBridgeFactory } from "../decoder/ffmpeg-bridge";
import { builtinWasmDecoders } from "../decoder/builtin-wasm";
import type { WasmDecoderConfig } from "../decoder/types";
import type { SegmentSource } from "../hls/segment-source";
import type { WorkerCommand, WorkerEvent } from "./messages";
import type { PcmWorkerStats, PlayerMediaInfo } from "../backends/types";
import { PcmTimeline } from "./pcm-timeline";

let pipeline: TransmuxPipeline | null = null;
let audioPipeline: TransmuxPipeline | null = null;
let soft: WorkerSoftDecoder | null = null;
/** 载入世代：每次 load/stop 递增。音频 rendition 的异步加载结果只对当代有效——
 *  旧世代迟到时直接丢弃，防止在新流上重建已被销毁的音频流水线。 */
let loadGen = 0;



function post(event: WorkerEvent, transfer?: Transferable[]): void {
  (self as unknown as Worker).postMessage(event, transfer ?? []);
}

function toTransferable(data: Uint8Array): ArrayBuffer {
  return data.slice().buffer as ArrayBuffer;
}

/**
 * 零延迟 macrotask 让出。
 * `setTimeout(fn, 0)` 在浏览器有 ~4ms clamp —— 解码循环若每块都让出，产出吞吐会被
 * 钳死在 ~250 块/秒，直接导致「音频产出跟不上视频 → 抖动缓冲耗尽 → 静音」。
 * MessageChannel 走的是真正的 macrotask 队列且无 clamp，让出成本接近 0，
 * 同时保留「不饿死 worker 内视频 remux/append」的原语义。
 */
const yieldChannel = new MessageChannel();
function yieldToMacrotask(): Promise<void> {
  return new Promise<void>((resolve) => {
    yieldChannel.port1.onmessage = () => resolve();
    yieldChannel.port2.postMessage(0);
  });
}

/** 源初始化重试参数：服务重启/上游抖动窗口内 fetch 必然短暂失败，需静默退避重试
 *  （UI 侧只有 3 次重载预算，不能被暂时性失败消耗掉）。 */
const SOURCE_INIT_MAX_ATTEMPTS = 6;
const SOURCE_INIT_BASE_DELAY_MS = 500;
const SOURCE_INIT_MAX_DELAY_MS = 5000;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/** 响应前几 KB 判断是否 HLS（#EXTM3U）。
 *  返回 null 表示探测本身失败（网络不可用）——必须与「确定不是 HLS」区分开：
 *  前者应退避重试（服务重启窗口内 fetch 必然短暂失败），后者才降级为直连 URL。 */
async function sniffHls(url: string): Promise<boolean | null> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 4000);
  try {
    const res = await fetch(url, { signal: ctrl.signal });
    if (!res.ok || !res.body) return false;
    const reader = res.body.getReader();
    const dec = new TextDecoder();
    let acc = "";
    try {
      for (let i = 0; i !== 32; i++) {
        const c = await reader.read();
        if (c.done) break;
        if (c.value) acc += dec.decode(c.value, { stream: true });
        if (acc.indexOf("#EXTM3U") !== -1) break;
      }
    } finally {
      clearTimeout(timer);
      try {
        await reader.cancel();
      } catch {
        /* ignore */
      }
    }
    return acc.indexOf("#EXTM3U") !== -1;
  } catch {
    clearTimeout(timer);
    // 探测失败（网络不可用 / 超时）：返回 null，由调用方退避重试而非降级直连
    return null;
  }
}

/**
 * worker 内解析动态源（HLS）。
 *
 * 初始化必须带退避重试：服务重启 / 上游抖动窗口内 fetch 必然短暂失败，而 UI 侧只有
 * MAX_RETRIES=3 次重载预算。若把一次性失败直接上报，几轮即耗尽预算并弹错误面板
 * （实测服务重启窗口内出现多条 Failed to fetch）。此处静默退避重试，仅当连续失败到
 * 上限才上报一次，交由上层按位置重载恢复。
 */
async function resolveSources(
  urls: string[],
  onError: (msg: string) => void,
): Promise<{
  source: SegmentSource | null;
  /** 播放列表是否声明了独立音频 rendition（同步可得：决定主流水线是否抑制音频）。 */
  hasAudioRendition: boolean;
  /** 独立音频 rendition：异步加载（不阻塞主视频起播），完成后交付（失败为 null）。 */
  audioSourcePromise: Promise<SegmentSource | null>;
  urls: string[];
}> {
  const first = urls[0];
  if (!first) return { source: null, hasAudioRendition: false, audioSourcePromise: Promise.resolve(null), urls };

  for (let attempt = 1; attempt <= SOURCE_INIT_MAX_ATTEMPTS; attempt++) {
    const sniff = await sniffHls(first);
    if (sniff === false) {
      // 确定不是 HLS（如直连 TS/FLV）：交给 pipeline 按直连流处理，无需重试
      return { source: null, hasAudioRendition: false, audioSourcePromise: Promise.resolve(null), urls };
    }
    if (sniff === true) {
      // 初始化期间 onError 静默：拉取失败由本函数的退避重试消化，不消耗 UI 重试预算
      // liveEdgeSegments: 2 —— 起播点取 edge-1：比 3 片少下 1 片（更快），
      // 又不像 1 片那样直接贴最新片（最新片可能尚未在 CDN 完全就绪 → 拉取失败/重试
      // → 起播反而变慢）。续片由 IO 循环接续。
      const hls = new HlsSource(first, { onError: () => {} }, { liveEdgeSegments: 2 });
      const info = await hls.load().catch(() => null);
      if (info) {
        // 独立音频 rendition（声画分流）：**异步加载、不阻塞主视频流水线** ——
        // 音频链与视频链并行，起播总耗时 = max(两链) 而非两者之和
        // （实测串行时视频要等音频 playlist + 预拉完成后才启动，多等 ~0.5~1s）。
        // 拉流错误不冒泡到主播放器（音频 playlist 抖动不得误触发整路重载）。
        let resolveAudioSource: (s: SegmentSource | null) => void = () => {};
        const audioSourcePromise = new Promise<SegmentSource | null>((resolve) => {
          resolveAudioSource = resolve;
        });
        const hasAudioRendition = Boolean(info.audioRendition?.uri);
        if (info.audioRendition?.uri) {
          const uri = info.audioRendition.uri;
          void (async () => {
            try {
              // liveEdgeSegments: 1 —— 同主源：贴直播边起播，减少预拉等待
              const ah = new HlsSource(uri, { onError: () => {} }, { liveEdgeSegments: 1 });
              const ainfo = await ah.load().catch(() => null);
              resolveAudioSource(ainfo ? ah : null);
            } catch (e) {
              // eslint-disable-next-line no-console
              console.warn(`[HLS] 音频 rendition 解析失败，仅播视频: ${e instanceof Error ? e.message : String(e)}`);
              resolveAudioSource(null);
            }
          })();
        } else {
          resolveAudioSource(null);
        }
        return { source: hls, hasAudioRendition, audioSourcePromise, urls: [] };
      }
      hls.destroy();
    }
    // sniff === null（探测失败）或播放列表加载失败 → 退避后重试
    if (attempt < SOURCE_INIT_MAX_ATTEMPTS) {
      const delay = Math.min(SOURCE_INIT_BASE_DELAY_MS * 2 ** (attempt - 1), SOURCE_INIT_MAX_DELAY_MS);
      // eslint-disable-next-line no-console
      console.warn(`[TransmuxWorker] 源初始化失败，${delay}ms 后重试 ${attempt}/${SOURCE_INIT_MAX_ATTEMPTS}`);
      await sleep(delay);
    }
  }
  onError(`播放列表连续 ${SOURCE_INIT_MAX_ATTEMPTS} 次加载失败`);
  return { source: null, hasAudioRendition: false, audioSourcePromise: Promise.resolve(null), urls };
}

/** worker 内软解：原始样本 → WASM 解码 → PCM（time 已由 pipeline 归一化）。 */
interface SoftDecoderState {
  chunks: SoftAudioChunk[];
  decoder: WasmAudioDecoder | null;
  draining: boolean;
  /** 连续解码失败次数（用于坏状态下自愈）。 */
  decodeErrors: number;
  /** 解码器初始化失败次数（acquire 返回 null，未配置或初始化失败）。 */
  decodeInitFailed: number;
  /** 软解 PCM 时间轴映射：丢弃/裁剪锚点前音频，保证输出 time 恒 ≥ 0。 */
  pcm: PcmTimeline;
  /**
   * 时间外推状态（基线 PTS 外推的等价实现）：
   * PCM 时间 = anchorTime + samplesSinceAnchor / sampleRate。
   * 不用每块 chunk.time 做首帧时间——解码输出首帧可能是跨 PES 的 carry 帧，
   * 其真实时间早于该块 PES 的 dts；且 wasm 内部 carry 导致每块输出的首帧
   * 相对块起点偏移不定。按累计样本数推进时间轴则与 PES 边界完全无关，
   * 时间轴绝对连续（重叠/空洞只在真不连续时出现）。
   */
  anchorTime: number | null;
  samplesSinceAnchor: number;
  anchorSampleRate: number;
}

/** 外推时间与块时间偏差超过此阈值才 re-锚（基线 10s：只认切台/节目级不连续，
 *  不响应 PES 周期相位抖动，否则会把时间轴硬拧造成声音加速/毛刺）。 */
const AUDIO_REANCHOR_THRESHOLD_SEC = 10;

/**
 * 解码时间片预算（毫秒）：连续解码到预算耗尽才让出一次 macrotask。
 * 太小 → 让出开销占比高、吞吐低；太大 → 饿死视频 remux/append（起播卡顿）。
 */
const DECODE_SLICE_BUDGET_MS = 4;

/** 连续解码失败达此次数即判定解码器坏状态并重建（避免静默永久停供）。 */
const DECODER_RESET_ERROR_THRESHOLD = 8;

class WorkerSoftDecoder {
  private readonly registry: DecoderRegistry;
  private readonly state = new Map<string, SoftDecoderState>();
  private gen = 0;

  constructor(wasmDecoders: WasmDecoderConfig["wasmDecoders"], pcmOutputChannels = 0) {
    // pcmOutputChannels：设备只能 2.0 时让 WASM 直接解成 2 声道（5.1 → 2.0 由解码器内部降混）
    this.registry = new DecoderRegistry(createFfmpegBridgeFactory(pcmOutputChannels), { wasmDecoders });
  }

  handle(chunk: SoftAudioChunk): void {
    if (!this.registry.supports(chunk.codec as never)) return;
    let st = this.state.get(chunk.codec);
    if (!st) {
      st = {
        chunks: [],
        decoder: null,
        draining: false,
        decodeErrors: 0,
        decodeInitFailed: 0,
        pcm: new PcmTimeline(),
        anchorTime: null,
        samplesSinceAnchor: 0,
        anchorSampleRate: 0,
      };
      this.state.set(chunk.codec, st);
    }
    st.chunks.push(chunk);
    void this.drain(st, chunk.codec, this.gen);
  }



  private async drain(
    st: SoftDecoderState,
    codec: string,
    gen: number,
  ): Promise<void> {
    if (st.draining) return;
    st.draining = true;
    try {
      if (!st.decoder) {
        st.decoder = await this.registry.acquire(codec as never);
        if (gen !== this.gen) return;
      }
      const decoder = st.decoder;
      if (!decoder) {
        // 解码器不可用（未配置或初始化失败）：本 codec 永久静默，但视频继续。
        // 仅在首次失败时计数，避免每块重复累加。
        if (st.decodeInitFailed === 0) {
          st.decodeInitFailed++;
          // eslint-disable-next-line no-console
          console.warn(`[DEC-INIT-FAIL] ${codec}: 软解 WASM 不可用（未配置或初始化失败）`);
        }
        return;
      }
      let sliceStart = performance.now();
      while (st.chunks.length > 0 && decoder) {
        if (gen !== this.gen) return;
        const chunk = st.chunks.shift()!;
        try {
          const audios = decoder.decode(chunk.data);
          if (gen !== this.gen) return;
          // 一块码流可能解出多帧。时间用「锚点 + 累计解码样本数外推」，而非
          // 每块 chunk.time——解码输出首帧可能是跨 PES 的 carry 帧（真实时间早于
          // 该块 dts），且 wasm 内部 carry 使首帧相对块起点偏移不定。按样本数推进
          // 时间轴与 PES 边界无关，绝对连续（基线 PTS 外推的等价实现）。
          for (const a of audios) {
            const sr = a.sampleRate;
            if (st.anchorTime === null || st.anchorSampleRate !== sr) {
              // 本批输出的开头可能起始于本次输入之前（WASM parser 压住上一帧再交出）：
              // 按 samplesBeforeInput 把锚点退回帧起点，否则逐帧标签整体前移一帧。
              const before = a.samplesBeforeInput ?? 0;
              st.anchorTime = chunk.time - before / sr;
              st.samplesSinceAnchor = 0;
              st.anchorSampleRate = sr;
            } else {
              // 外推时间与块时间偏差超阈值 → 真不连续（切台/大跳变）→ re-锚。
              // 阈值 10s：不响应 PES 周期相位抖动（否则时间轴被硬拧 → 声音变速/毛刺）。
              const extrapolated = st.anchorTime + st.samplesSinceAnchor / sr;
              if (Math.abs(chunk.time - extrapolated) > AUDIO_REANCHOR_THRESHOLD_SEC) {
                st.anchorTime = chunk.time;
                st.samplesSinceAnchor = 0;
              }
            }
            const time = st.anchorTime + st.samplesSinceAnchor / sr;
            st.samplesSinceAnchor += a.samplesPerChannel;
            const ch = Math.max(1, a.channels);
            const mapped = st.pcm.map(time, a.pcm, sr, ch);
            if (mapped) {
              post(
                { type: "pcm", pcm: mapped.pcm, channels: ch, sampleRate: sr, time: mapped.time },
                [mapped.pcm.buffer],
              );
            }
          }
        } catch (e) {
          // 单块失败跳过，避免队列卡死；但连续失败说明解码器已进入坏状态，
          // 若不处理会「静默停供」（音频永久无声且无任何报错）——故累计到阈值即重建解码器自愈。
          st.decodeErrors++;
          if (st.decodeErrors <= 3) {
            // eslint-disable-next-line no-console
            console.warn(`[DEC-ERR] ${codec} #${st.decodeErrors}: ${e instanceof Error ? e.message : String(e)}`);
          }
          if (st.decodeErrors >= DECODER_RESET_ERROR_THRESHOLD) {
            // eslint-disable-next-line no-console
            console.warn(`[DEC-RESET] ${codec}: ${st.decodeErrors} 次连续失败，重建解码器`);
            try {
              st.decoder?.flush();
            } catch {
              /* ignore */
            }
            st.decoder = null; // 下一轮 drain 会重新 acquire
            st.decodeErrors = 0;
            st.anchorTime = null;
            st.samplesSinceAnchor = 0;
          }
        }
        // 时间片让出：首个视频样本定基准后，预锚点音频会成批灌入解码循环，
        // 若同步跑完会饿死 worker 内的视频 remux/append → 起播视频缓冲枯竭→卡顿。
        // 但「每块都让出」会因 setTimeout 的 ~4ms clamp 把吞吐钳死，故改为：
        // 连续处理到时间片预算耗尽才让出一次，且用 MessageChannel 零延迟让出。
        // 稳态（队列常空）不触发让出，几乎零开销。
        if (st.chunks.length > 0 && performance.now() - sliceStart >= DECODE_SLICE_BUDGET_MS) {
          await yieldToMacrotask();
          if (gen !== this.gen) return;
          sliceStart = performance.now();
        }
      }
    } finally {
      if (gen === this.gen) st.draining = false;
    }
  }

  reset(): void {
    this.gen++;
    for (const st of this.state.values()) {
      st.chunks.length = 0;
      st.draining = false;
      if (st.decoder) {
        try {
          st.decoder.flush();
        } catch {
          /* ignore */
        }
      }
    }
    this.state.clear();
  }

  /** 汇总所有 codec 的软解诊断计数（对齐 playback-engine PcmWorkerStats）。 */
  getStats(): PcmWorkerStats {
    const acc: PcmWorkerStats = {
      remuxDropChunks: 0,
      trimSamples: 0,
      decodeInitFailed: 0,
      decodeErrors: 0,
      pendingOverflowDrops: 0,
    };
    for (const st of this.state.values()) {
      const t = st.pcm.getStats();
      acc.remuxDropChunks += t.remuxDropChunks;
      acc.trimSamples += t.trimSamples;
      acc.decodeInitFailed += st.decodeInitFailed;
      acc.decodeErrors += st.decodeErrors;
    }
    return acc;
  }

  destroy(): void {
    this.reset();
    this.registry.destroy();
  }
}

self.onmessage = (ev: MessageEvent<WorkerCommand>) => {
  const cmd = ev.data;
  switch (cmd.type) {
    case "load": {
      loadGen++;
      const gen = loadGen;
      pipeline?.destroy();
      audioPipeline?.destroy();
      audioPipeline = null;
      soft?.destroy();
      soft = null;

      const wasmDecoders = cmd.wasmDecoders ?? builtinWasmDecoders;
      const softEnabled = cmd.softDecodeAudio ?? true;

      void (async () => {
        const resolved = await resolveSources(cmd.urls, (msg) =>
          post({ type: "error", error: { category: "io", info: msg } }),
        );
        if (softEnabled) soft = new WorkerSoftDecoder(wasmDecoders, cmd.pcmOutputChannels ?? 0);

        // 声画分流（独立音轨）时：
        // - 主视频流水线抑制其 TS 内音频：声音统一由第二路音频流水线输出，避免两路
        //   撞进同一个 audio SourceBuffer（MSE 拒收）或叠音；
        // - 音频期待**乐观预置**为 true：独立音轨若是 passthrough（AAC 等）会走 MSE
        //   直通，其 audio SB 必须在第一路 init append 前就位（Chromium 首个 init
        //   append 即锁死 SB 数量，后到的 addSourceBuffer 必抛）；若音频流水线拉流
        //   demux 后确认是软解编码（ac3/mp2 等），会发 mseAudio=false 的 layout 撤回预置。
        const hasSeparateAudio = resolved.hasAudioRendition;
        let mainLayout: { video: boolean; mseAudio: boolean } | null = null;
        let audioLayout: { video: boolean; mseAudio: boolean } | null = hasSeparateAudio
          ? { video: false, mseAudio: true }
          : null;
        const postMergedLayout = (): void => {
          // 主流水线 layout 未到前**不可发布**：它承载视频期待 —— 若音频流水线 layout
          // 先到就发出 video:false，hold 只等音频即放行，audio SB 先建立并 append
          // （引擎初始化）后，video init 到达时 addSourceBuffer 必抛"已达上限"。
          if (!mainLayout) return;
          const video = mainLayout.video || (audioLayout?.video ?? false);
          const mseAudio = mainLayout.mseAudio || (audioLayout?.mseAudio ?? false);
          post({ type: "stream-layout", layout: { video, mseAudio } });
        };
        // 声画双方各自发布 mediaInfo，而 UI 按 slot 整体替换 —— 任一方单独上报都会
        // 顶掉另一方的行（实测视频行被音频顶掉）；缓存合并后统一上报。
        let mainMediaInfo: PlayerMediaInfo | null = null;
        let audioMediaInfo: PlayerMediaInfo | null = null;
        const postMergedMediaInfo = (): void => {
          if (!mainMediaInfo && !audioMediaInfo) return;
          const merged: PlayerMediaInfo = { ...(mainMediaInfo ?? {}) };
          if (audioMediaInfo?.audio) merged.audio = audioMediaInfo.audio;
          post({ type: "media-info", info: merged });
        };

        pipeline = new TransmuxPipeline(
          {
            urls: resolved.urls,
            source: resolved.source ?? undefined,
            sourceMode: cmd.sourceMode,
            resumeMode: cmd.resumeMode,
            targetDuration: cmd.targetDuration,
            maxBytes: cmd.maxBytes,
            bufferThreshold: cmd.bufferThreshold,
            softDecodeCodecs: cmd.softDecodeCodecs,
            suppressAudio: hasSeparateAudio,
          },
          {
            onInitSegment: (seg) => {
              const data = toTransferable(seg.data);
              post({ type: "init-segment", codec: seg.codec, container: seg.container, data, kind: seg.kind }, [data]);
            },
            onMediaSegment: (seg) => {
              const data = toTransferable(seg.data);
              post(
                {
                  type: "media-segment",
                  data,
                  timestampOffset: seg.timestampOffset,
                  startDts: seg.startDts,
                  duration: seg.duration,
                  kind: seg.kind,
                  trackIds: seg.trackIds,
                },
                [data],
              );
            },
            onMediaInfo: (info) => {
              mainMediaInfo = info;
              postMergedMediaInfo();
            },
            onStreamLayout: (layout) => {
              mainLayout = layout;
              postMergedLayout();
            },
            onSoftAudioData: (chunk) => soft?.handle(chunk),
            onLoadingComplete: () => post({ type: "loading-complete" }),
            onIOError: (info) =>
              post({ type: "error", error: { category: "io", code: info.code, info: info.msg, url: info.url } }),
            onDemuxError: (msg) => post({ type: "error", error: { category: "demux", info: msg } }),
          },
        );
        void pipeline.start().catch((e: unknown) => {
          post({
            type: "error",
            error: { category: "io", info: e instanceof Error ? e.message : String(e) },
          });
        });

        // 独立音频 rendition（分离音轨）：第二路发布者。
        // - passthrough 编码（AAC 等）走 MSE 直通：产出的 audio init/media 段由主线程
        //   append 到 audio SourceBuffer（与主视频流水线的 video SB 各占其一，按 kind 分流）；
        // - 软解编码（ac3/mp2 等）继续走独立软解（separateAudio：base 钉音频首样本）。
        if (softEnabled) {
          void resolved.audioSourcePromise.then((audioSource) => {
            // 世代校验：stop / 重新 load 后迟到的交付直接丢弃（不得在新流上重建旧流水线）
            if (gen !== loadGen || !audioSource) return;
            audioPipeline = new TransmuxPipeline(
              {
                urls: [],
                source: audioSource,
                sourceMode: cmd.sourceMode,
                resumeMode: cmd.resumeMode,
                targetDuration: cmd.targetDuration,
                maxBytes: cmd.maxBytes,
                bufferThreshold: cmd.bufferThreshold,
                softDecodeCodecs: cmd.softDecodeCodecs,
                separateAudio: true,
              },
              {
                onInitSegment: (seg) => {
                  const data = toTransferable(seg.data);
                  post({ type: "init-segment", codec: seg.codec, container: seg.container, data, kind: seg.kind }, [
                    data,
                  ]);
                },
                onMediaSegment: (seg) => {
                  const data = toTransferable(seg.data);
                  post(
                    {
                      type: "media-segment",
                      data,
                      timestampOffset: seg.timestampOffset,
                      startDts: seg.startDts,
                      duration: seg.duration,
                      kind: seg.kind,
                      trackIds: seg.trackIds,
                    },
                    [data],
                  );
                },
                onMediaInfo: (info) => {
                  audioMediaInfo = info;
                  postMergedMediaInfo();
                },
                onStreamLayout: (layout) => {
                  // 音频流水线的实际布局：软解编码时 mseAudio=false，即撤回"预置的音频期待"
                  audioLayout = layout;
                  postMergedLayout();
                },
                onSoftAudioData: (chunk) => soft?.handle(chunk),
              },
            );
            // 声画时间轴对齐（旧实现同语义：音频映射到输出/视频时间轴）：音频基准锚到视频首样本。
            // **不阻塞启动**：标记"基准待定"——音频链立即拉流并发射 init（hold/起播门立即满足），
            // 仅首个 media 段推迟到基准到达（remuxer 侧超时 1.5s 自行放行），避免为等基准拖慢起播。
            audioPipeline.awaitExternalBase();
            void audioPipeline.start().catch((e: unknown) => {
              // eslint-disable-next-line no-console
              console.warn(`[HLS] 音频 rendition 流水线启动失败: ${e instanceof Error ? e.message : String(e)}`);
            });
            // 后台轮询视频基准就绪后锚定（视频链通常先就绪；最多等 ~2s）
            void (async () => {
              for (let i = 0; i < 40; i++) {
                if (gen !== loadGen) return;
                const videoBase = pipeline?.getFirstVideoSampleSec() ?? null;
                if (videoBase !== null) {
                  audioPipeline?.setExternalBase(videoBase);
                  return;
                }
                await sleep(50);
              }
            })();
          });
        }
      })();
      break;
    }
    case "clock":
      // 缓冲领先门：主流水线与音频流水线都需要。音频链片小、处理快，若不设门会一路追到
      // 远超视频的位置（元素缓冲被虚高、内存徒增、进度差越拉越大）；用同一播放头与
      // 视频缓冲末端限流，把两链进度差约束在领先阈值内，保证音画时间轴贴近。
      pipeline?.setClock(cmd.currentTimeMs, cmd.bufferedEndMs, cmd.hidden);
      audioPipeline?.setClock(cmd.currentTimeMs, cmd.bufferedEndMs, cmd.hidden);
      break;
    case "pause":
      pipeline?.pause();
      audioPipeline?.pause();
      break;
    case "resume":
      pipeline?.resume();
      audioPipeline?.resume();
      break;
    case "stop":
      loadGen++; // 使迟到的音频 rendition 交付失效
      pipeline?.stop();
      audioPipeline?.stop();
      soft?.reset();
      break;
    case "destroy":
      pipeline?.destroy();
      pipeline = null;
      audioPipeline?.destroy();
      audioPipeline = null;
      soft?.destroy();
      soft = null;
      break;
  }
};

post({ type: "ready" });

/** 周期上报软解诊断计数（变更即发），供 UI/后端定位静音、丢帧。 */
let lastStatsJson = "";
setInterval(() => {
  if (!soft) return;
  const stats = soft.getStats();
  const json = JSON.stringify(stats);
  if (json !== lastStatsJson) {
    lastStatsJson = json;
    post({ type: "pcm-audio-stats", stats });
  }
}, 1000);
