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
import type { PcmWorkerStats } from "../backends/types";
import { PcmTimeline } from "./pcm-timeline";

let pipeline: TransmuxPipeline | null = null;
let audioPipeline: TransmuxPipeline | null = null;
let soft: WorkerSoftDecoder | null = null;



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

/** 响应前几 KB 判断是否 HLS（#EXTM3U）。 */
async function sniffHls(url: string): Promise<boolean> {
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
    return false;
  }
}

/** worker 内解析动态源（HLS）。 */
async function resolveSources(
  urls: string[],
  onError: (msg: string) => void,
): Promise<{ source: SegmentSource | null; audioSource: SegmentSource | null; urls: string[] }> {
  const first = urls[0];
  if (!first) return { source: null, audioSource: null, urls };
  if (await sniffHls(first)) {
    const hls = new HlsSource(first, { onError }, { liveEdgeSegments: 3 });
    const info = await hls.load();
    if (info) {
      // 解析独立音频 rendition（EXT-X-MEDIA;TYPE=AUDIO）：重庆有线等「声画分流」流靠它出声。
      // 解析失败不致命——仅播视频（该流静音），视频继续。
      let audioSource: SegmentSource | null = null;
      if (info.audioRendition?.uri) {
        try {
          // 音频 rendition 的拉流错误不冒泡到主播放器（否则音频 playlist 抖动会误触发整路重载）；
          // 仅解析失败整体降级为「仅播视频」。播放期错误由 audioSource 自身重试逻辑消化。
          const ah = new HlsSource(info.audioRendition.uri, { onError: () => {} }, { liveEdgeSegments: 3 });
          const ainfo = await ah.load();
          if (ainfo) audioSource = ah;
        } catch (e) {
          // eslint-disable-next-line no-console
          console.warn(`[HLS] 音频 rendition 解析失败，仅播视频: ${e instanceof Error ? e.message : String(e)}`);
        }
      }
      return { source: hls, audioSource, urls: [] };
    }
    hls.destroy();
  }
  return { source: null, audioSource: null, urls };
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
            onMediaInfo: (info) => post({ type: "media-info", info }),
            onStreamLayout: (layout) => post({ type: "stream-layout", layout }),
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

        // 独立音频 rendition（分离音轨）：第二路软解流水线。base 钉音频首样本、只产 PCM，
        // 不向 MSE 发 init/media/stream-layout，避免干扰主视频流水线的缓冲门控。
        if (softEnabled && resolved.audioSource) {
          audioPipeline = new TransmuxPipeline(
            {
              urls: [],
              source: resolved.audioSource,
              sourceMode: cmd.sourceMode,
              resumeMode: cmd.resumeMode,
              targetDuration: cmd.targetDuration,
              maxBytes: cmd.maxBytes,
              bufferThreshold: cmd.bufferThreshold,
              softDecodeCodecs: cmd.softDecodeCodecs,
              separateAudio: true,
              audioOnly: true,
            },
            {
              onSoftAudioData: (chunk) => soft?.handle(chunk),
            },
          );
          void audioPipeline.start().catch((e: unknown) => {
            // eslint-disable-next-line no-console
            console.warn(`[HLS] 音频 rendition 流水线启动失败: ${e instanceof Error ? e.message : String(e)}`);
          });
        }
      })();
      break;
    }
    case "clock":
      // 缓冲领先门：仅主流水线需要；分离音频流水线不设此门。
      pipeline?.setClock(cmd.currentTimeMs, cmd.bufferedEndMs, cmd.hidden);
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
