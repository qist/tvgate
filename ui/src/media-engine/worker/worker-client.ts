/**
 * Worker 客户端（主线程侧。
 * 用标准 `new Worker(new URL(...), { type: "module" })` 实例化（Vite 会内联打包该模块），
 * 把 PipelineCallbacks 语义桥接为跨线程消息；大数据经 transferable 零拷贝回传。
 */

import type { MediaSegmentPayload, InitSegmentPayload } from "../remux/fmp4-remuxer";
import type { PlayerError, PlayerMediaInfo } from "../backends/types";
import type { SourceMode } from "../types";
import type { WasmDecoderConfig } from "../decoder/types";
import type { WorkerCommand, WorkerEvent } from "./messages";
import type { PcmWorkerStats } from "../backends/types";

export interface WorkerClientCallbacks {
  onInitSegment?(segment: InitSegmentPayload): void;
  onMediaSegment?(segment: MediaSegmentPayload): void;
  onMediaInfo?(info: PlayerMediaInfo): void;
  /** PMT 轨道布局（主线程据此在 append 前建齐 SourceBuffer）。 */
  onStreamLayout?(layout: { video: boolean; mseAudio: boolean }): void;
  /** 软解后的交错 PCM（time 已在 worker 内归一化到 MSE 时间轴）。 */
  onPCMAudioData?(pcm: Float32Array, channels: number, sampleRate: number, time: number): void;
  /** 软解 PCM 全链路丢弃计数（诊断用）。 */
  onPcmAudioStats?(stats: PcmWorkerStats): void;
  onLoadingComplete?(): void;
  onError?(error: PlayerError): void;
}

export interface WorkerClientLoadOptions {
  urls: string[];
  sourceMode?: SourceMode;
  resumeMode?: "range" | "restart";
  targetDuration?: number;
  maxBytes?: number;
  bufferThreshold?: number;
  softDecodeCodecs?: string[];
  wasmDecoders?: WasmDecoderConfig["wasmDecoders"];
  softDecodeAudio?: boolean;
  /** 设备可用的 PCM 输出声道数（0/缺省 = 直通源声道；2 = 强制 2.0；5.1 设备兼容）。 */
  pcmOutputChannels?: number;
}

export class TransmuxWorkerClient {
  private worker: Worker | null = null;
  /**
   * 已销毁：destroy() 后置位，此后收到的任何 worker 消息一律丢弃。
   * 切台时旧 worker 被 terminate 前可能已 post 出若干消息（尤其软解 PCM 批量下发），
   * 这些"在途消息"会在新流启动后才抵达主线程 —— 若继续转发，会灌进音频播放器，
   * 表现为切台后**旧音轨残留**（慢台尤其明显：新流迟迟无数据，旧音占满空窗）。
   */
  private disposed = false;

  constructor(private readonly callbacks: WorkerClientCallbacks = {}) {}

  private ensureWorker(): Worker {
    if (this.worker) return this.worker;
    let worker: Worker;
    try {
      worker = new Worker(new URL("./transmux-worker.ts", import.meta.url), { type: "module" });
    } catch (e) {
      // 极少数环境不支持 module worker：上报而非崩溃
      this.callbacks.onError?.({ category: "io", info: e instanceof Error ? e.message : "无法创建 worker" });
      throw e;
    }
    worker.onmessage = (ev: MessageEvent<WorkerEvent>) => {
      // 仅处理当前活跃 worker 的消息：崩溃/重建替换后，旧 worker 在途的残留消息
      // （尤其软解 PCM 批量下发）一律丢弃 —— 否则会在新流上继续灌入旧音轨。
      if (this.worker !== worker) return;
      this.handleEvent(ev.data);
    };
    worker.onerror = (e: ErrorEvent) => {
      // 旧 worker 的迟到 error：忽略，不得误伤已替换的新 worker
      if (this.worker !== worker) return;
      // **关键**：崩溃/加载失败的 worker 必须立即作废，否则 ensureWorker 会一直返回这具
      // "尸体"——上层（直播边重载 / 用户切台）会照常调用 load()，消息却全部石沉大海，
      // 表现为「服务重启后立即断开、且重试完全不生效」。作废后下一次 load() 会自动重建
      // worker 并重新拉流，服务恢复即自愈（与旧实现"错误交给上层 retry/reload"同语义）。
      e.preventDefault();
      this.dropWorker();
      this.callbacks.onError?.({ category: "io", info: e.message || "worker 异常" });
    };
    this.worker = worker;
    return worker;
  }

  /** 丢弃当前 worker（崩溃/退出/销毁时调用）：terminate 并置空，下次 send 按需重建。 */
  private dropWorker(): void {
    const worker = this.worker;
    this.worker = null;
    try {
      worker?.terminate();
    } catch {
      // worker 已退出：terminate 可能抛，忽略
    }
  }

  private handleEvent(event: WorkerEvent): void {
    // 已销毁（切台/重建/停止）：丢弃一切后续消息 —— 含 terminate 前已 post 的在途消息
    if (this.disposed) return;
    // init 段**同任务批量**下发（避免多次跨线程往返）：
    // 每个 worker 消息是独立任务；若 video init 先 append，UA 会在任务间隙解析它并**锁死
    // SourceBuffer 集合**，随后 audio 的 addSourceBuffer 直接抛
    // "This MediaSource has reached the limit of SourceBuffer objects" → **有画无声**。
    // 把相邻 init 攒到同一任务里一次性下发，两条 addSourceBuffer 在同一个任务内先后执行，
    // 此时任何 init 都还没被解析，源缓冲集合不会被锁。
    // media-info 可能紧跟 init 之前，不能作为 flush 触发（否则又把它们拆成两个任务）。
    if (event.type !== "init-segment" && event.type !== "media-info") {
      this.flushPendingInits();
    }
    switch (event.type) {
      case "ready":
        break;
      case "init-segment":
        this.pendingInits.push({
          codec: event.codec,
          container: event.container,
          data: new Uint8Array(event.data),
          kind: event.kind,
        });
        break;
      case "media-segment":
        this.callbacks.onMediaSegment?.({
          data: new Uint8Array(event.data),
          timestampOffset: event.timestampOffset,
          startDts: event.startDts,
          duration: event.duration,
          kind: event.kind,
          trackIds: event.trackIds,
        });
        break;
      case "media-info":
        this.callbacks.onMediaInfo?.(event.info);
        break;
      case "stream-layout":
        this.callbacks.onStreamLayout?.(event.layout);
        break;
      case "pcm":
        this.callbacks.onPCMAudioData?.(event.pcm, event.channels, event.sampleRate, event.time);
        break;
      case "pcm-audio-stats":
        this.callbacks.onPcmAudioStats?.(event.stats);
        break;
      case "loading-complete":
        this.callbacks.onLoadingComplete?.();
        break;
      case "error":
        this.callbacks.onError?.(event.error);
        break;
    }
  }

  /** 待下发的 init 段（见 handleEvent 注释：必须同任务批量 flush）。 */
  private pendingInits: InitSegmentPayload[] = [];

  private flushPendingInits(): void {
    if (this.pendingInits.length === 0) return;
    const inits = this.pendingInits;
    this.pendingInits = [];
    for (const init of inits) this.callbacks.onInitSegment?.(init);
  }

  private send(cmd: WorkerCommand): void {
    try {
      this.ensureWorker().postMessage(cmd);
    } catch (e) {
      // 重建失败（服务重启窗口内 worker 脚本暂时拉不到）：丢弃以便下次重试重建，
      // 不向外抛 —— 抛出会沿调用栈变成未捕获异常，直接打断上层播放流程。
      this.dropWorker();
      // eslint-disable-next-line no-console
      console.warn(`[WorkerClient] postMessage 失败：${e instanceof Error ? e.message : String(e)}`);
    }
  }

  load(options: WorkerClientLoadOptions): void {
    this.send({ type: "load", ...options });
  }

  /** 上报播放头 + MSE 缓冲末端（worker 缓冲领先门用；hidden 时门无条件放行）。 */
  clock(currentTimeMs: number, bufferedEndMs: number, hidden: boolean): void {
    this.send({ type: "clock", currentTimeMs, bufferedEndMs, hidden });
  }

  pause(): void {
    this.send({ type: "pause" });
  }

  resume(): void {
    this.send({ type: "resume" });
  }

  stop(): void {
    this.send({ type: "stop" });
  }

  destroy(): void {
    // 先置失效闸门（即使 worker 已崩溃为空也要置位），再通知/停掉底层 worker
    this.disposed = true;
    if (!this.worker) return;
    this.send({ type: "destroy" });
    this.dropWorker();
  }
}
