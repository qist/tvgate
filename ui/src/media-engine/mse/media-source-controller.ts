/**
 * MediaSource 集成。
 * 依据 Media Source Extensions 公开规范重新实现。契约见引擎设计 §5.3：
 * open(onOpen) / appendInit(track,data,codec,container) / appendMedia(track,data,timestampOffset?) /
 * setDuration(sec) / endOfStream() / destroy()；回调 onBufferFull / onBufferAvailable /
 * onBufferUpdated / onStartStreaming / onEndStreaming（ManagedMediaSource 流控）。
 */

import { debugWarn } from "../../lib/debug-log";

interface ManagedMediaSource extends MediaSource {
  disableRemotePlayback: boolean;
}

type ManagedMediaSourceCtor = {
  new (): ManagedMediaSource;
  isTypeSupported(mime: string): boolean;
};

declare global {
  interface Window {
    ManagedMediaSource?: ManagedMediaSourceCtor;
    WebKitMediaSource?: { new (): MediaSource; isTypeSupported(mime: string): boolean };
  }
}

export interface BufferedRange {
  start: number;
  end: number;
}

export interface MediaSourceControllerCallbacks {
  onOpen?(): void;
  onBufferFull?(track: string): void;
  onBufferAvailable?(track: string): void;
  onBufferUpdated?(track: string, ranges: BufferedRange[]): void;
  /** ManagedMediaSource：UA 需要更多数据。 */
  onStartStreaming?(): void;
  /** ManagedMediaSource：UA 缓冲已够。 */
  onEndStreaming?(): void;
  onError?(message: string): void;
  /**
   * addSourceBuffer 抛"已达上限/引擎已初始化"时回调（返回 true 表示上层已接管自愈，
   * 控制器不再按降级上报错误）。见 mse-backend.healSourceBufferLimit。
   */
  onSourceBufferLimit?(track: string, message: string): boolean;
  /**
   * 某轨 MIME 不受当前环境支持（`MediaSource.isTypeSupported` 为假，典型：无 HEVC 时的 hvc1）。
   * 不提供时退回旧的 onError 行为（一律当错误）。
   */
  onTrackUnsupported?(track: string, mime: string): void;
}

export interface MediaSourceControllerOptions {
  /** 已播放区间保留秒数（超出即回收，控制低端设备内存）。 */
  keepBehind?: number;
}

interface TrackState {
  buffer: SourceBuffer;
  queue: { data: Uint8Array; timestampOffset?: number }[];
  updating: boolean;
}

/** 已播放区间默认保留秒数。 */
const DEFAULT_KEEP_BEHIND = 12;
/** QuotaExceeded 后的重试间隔（毫秒）：缓冲腾出后自动恢复拉流，避免永久暂停。 */
const FULL_RETRY_MS = 500;
/** sourceopen 前暂存 init/media 的队列上限（条）：溢出时优先丢最旧 media 段（直播语义下价值最低）。 */
const MAX_PRE_OPEN_QUEUE = 64;

/** open() 前到达的段载体：由后端在 sourceopen 后经原 onInitSegment/onMediaSegment 路径回放。 */
export interface PreOpenItem {
  kind: "init" | "media";
  track: string;
  data: Uint8Array;
  codec?: string;
  container?: string;
  timestampOffset?: number;
}

export class MediaSourceController {
  private mediaSource: MediaSource | null = null;
  private objectUrl: string | null = null;
  private readonly tracks = new Map<string, TrackState>();
  /** 判定不可用的轨（MIME 不支持 / addSourceBuffer 失败），不再重试写入。 */
  private readonly disabled = new Set<string>();
  private opened = false;
  private opening = false;
  private pendingOpens: (() => void)[] = [];
  /** sourceopen 前到达的 init/media 段暂存（iPhone Safari MMS：sourceopen 可能极晚，而管线已提前启动）。 */
  private preOpenQueue: PreOpenItem[] = [];
  /**
   * 启动 hold：Chromium 一旦 append 了任一 init/moov 就锁定 SourceBuffer 数量，
   * 之后 addSourceBuffer 必然抛 QuotaExceededError。故首个 init 必须先建好所有轨缓冲再统一放行。
   */
  private held = false;
  /** 缓冲真满（appendBuffer 抛 QuotaExceededError）时为 true，用于暂停上游拉流。 */
  private bufferFull = false;
  private fullRetryTimer: ReturnType<typeof setTimeout> | null = null;
  /** 摘除当前 MediaSource 上的监听器（destroy 时调用，避免旧 MS 的异步事件污染新会话）。 */
  private detachMediaSource: (() => void) | null = null;
  /** 是否实际使用 ManagedMediaSource（仅当标准 MediaSource 不可用时）。 */
  private usesManagedMSE = false;
  private readonly keepBehind: number;

  constructor(
    private readonly video: HTMLVideoElement,
    private readonly callbacks: MediaSourceControllerCallbacks = {},
    options: MediaSourceControllerOptions = {},
  ) {
    this.keepBehind = options.keepBehind ?? DEFAULT_KEEP_BEHIND;
  }

  get isOpen(): boolean {
    return this.opened;
  }

  /** open() 已发起但 sourceopen 尚未触发（看门狗/诊断用）。 */
  get isOpening(): boolean {
    return this.opening;
  }

  /** 是否实际使用 ManagedMediaSource（iOS Safari）。 */
  get usesManaged(): boolean {
    return this.usesManagedMSE;
  }

  /** 创建 MediaSource 并挂到 video；ManagedMediaSource 优先（支持 UA 流控）。 */
  open(onOpen?: () => void): void {
    if (this.opened) {
      onOpen?.();
      return;
    }
    // 加固：建新 MediaSource 前**必须**确保上一个已彻底拆除。
    // 否则 this.tracks 会残留已脱离旧 MS 的 SourceBuffer，新会话里 state 已存在 →
    // 不再调用 addSourceBuffer，而 append 又因 hasBuffer() 判否被丢弃 →
    // 「有流、无画面、且无任何报错」（一直加载中）。
    if (this.mediaSource !== null || this.tracks.size > 0 || this.detachMediaSource !== null) {
      this.destroy();
    }
    if (this.opening) {
      // 首次 open 的 sourceopen 尚未触发：把回调排队，待真正 open 后再执行，
      // 避免在未 open 的 MediaSource 上启动 pipeline（会导致 "MediaSource 尚未 open"）
      if (onOpen) this.pendingOpens.push(onOpen);
      return;
    }
    this.opening = true;
    // 只有**不存在**标准 MediaSource 时（iOS Safari）才用 ManagedMediaSource。
    // 把 MMS 排在最前很危险：MMS 有 UA 侧流控（streaming===false 期间不接受 append），
    // 而本控制器未实现该门控；再叠加 endstreaming 时暂停上游，极易造成 append 被丢弃/卡流。
    const hasStandardMSE = typeof window !== "undefined" && !!window.MediaSource;
    this.usesManagedMSE = !hasStandardMSE && typeof window !== "undefined" && !!window.ManagedMediaSource;
    const Ctor: { new (): MediaSource } | undefined =
      (typeof window !== "undefined" && window.MediaSource) ||
      (typeof window !== "undefined" && window.WebKitMediaSource) ||
      (typeof window !== "undefined" && window.ManagedMediaSource) ||
      undefined;
    if (!Ctor) {
      this.callbacks.onError?.("当前环境不支持 MediaSource");
      return;
    }

    const ms = new Ctor();
    this.mediaSource = ms;

    const managed = ms as ManagedMediaSource;
    // Apple 规范要求：Safari 只有在**媒体元素**上显式禁用远程播放（HTMLMediaElement.
    // disableRemotePlayback = true）后 MMS 才会激活，否则 sourceopen 永不触发
    // （iPhone 无限转圈的直接原因）。注意必须设在 video 元素上 —— 设在 MediaSource
    // 对象上只是无效果的普通属性赋值（不报错）；且必须在挂 src 之前完成。
    if (this.usesManagedMSE) {
      const el = this.video as HTMLVideoElement & { disableRemotePlayback?: boolean };
      try {
        el.disableRemotePlayback = true;
      } catch {
        /* 忽略 */
      }
    }

    // 监听器一律带「身份校验」：destroy() 里 video.load() 会让**旧** MediaSource 异步派发
    // sourceclose；不加校验就会把**新**会话的 opened/opening 置 false 并清空新轨队列
    // → 所有 append 静默 no-op（无画面、无任何报错，即「显示没有了」）。
    const onSourceOpen = () => {
      if (this.mediaSource !== ms) return;
      this.opened = true;
      this.opening = false;
      // open() 期间排队的回调必须在此补发，否则快速切台时第二个 pipeline 永不启动
      // （无画面且无任何报错）。
      const pendings = this.pendingOpens;
      this.pendingOpens = [];
      this.callbacks.onOpen?.();
      onOpen?.();
      for (const pending of pendings) pending();
    };
    const onSourceEnded = () => {
      /* 上层按需处理 */
    };
    const onSourceClose = () => {
      if (this.mediaSource !== ms) return; // 旧 MS 的 close 不得污染新会话
      this.opened = false;
      this.opening = false;
      // MediaSource 已脱离 video：丢弃所有待写缓冲，避免继续向已移除的 SourceBuffer 写数据
      for (const state of this.tracks.values()) state.queue.length = 0;
    };
    // ManagedMediaSource 流控事件
    const onStartStreaming = () => {
      if (this.mediaSource !== ms) return;
      this.callbacks.onStartStreaming?.();
    };
    const onEndStreaming = () => {
      if (this.mediaSource !== ms) return;
      this.callbacks.onEndStreaming?.();
    };

    ms.addEventListener("sourceopen", onSourceOpen);
    ms.addEventListener("sourceended", onSourceEnded);
    ms.addEventListener("sourceclose", onSourceClose);
    if (this.usesManagedMSE && typeof window !== "undefined" && window.ManagedMediaSource) {
      managed.addEventListener("startstreaming", onStartStreaming);
      managed.addEventListener("endstreaming", onEndStreaming);
    }
    this.detachMediaSource = () => {
      ms.removeEventListener("sourceopen", onSourceOpen);
      ms.removeEventListener("sourceended", onSourceEnded);
      ms.removeEventListener("sourceclose", onSourceClose);
      managed.removeEventListener("startstreaming", onStartStreaming);
      managed.removeEventListener("endstreaming", onEndStreaming);
    };

    this.objectUrl = URL.createObjectURL(ms);
    this.video.src = this.objectUrl;
  }

  /** 追加 init segment（内含 codec/container 描述，内部据此建 SourceBuffer）。 */
  appendInit(track: string, data: Uint8Array, codec: string, container: string): void {
    const ms = this.mediaSource;
    if (!ms) return;
    if (!this.opened) {
      // iPhone Safari（MMS）：管线已在 open 前启动 —— init 暂存，sourceopen 后按序回放
      // （直接丢弃会让该轨 SourceBuffer 永远建不出来 → 无限转圈）。
      // opening=false 说明 MediaSource 已拆除（撕裂竞态中的过期数据），维持丢弃不上报。
      if (this.opening) this.pushPreOpen({ kind: "init", track, data, codec, container });
      return;
    }
    if (this.disabled.has(track)) return; // 该轨已判定不可用，避免反复重试刷屏

    let state = this.tracks.get(track);
    if (!state) {
      const mime = `${container};codecs="${codec}"`;
      let supported = false;
      try {
        supported =
          (typeof window !== "undefined" && !!window.ManagedMediaSource?.isTypeSupported(mime)) ||
          (typeof MediaSource !== "undefined" && MediaSource.isTypeSupported(mime));
      } catch {
        supported = false;
      }
      if (!supported) {
        this.disabled.add(track);
        // 单轨不受支持不等于整条流不可播（可能只是视频轨 HEVC / 音频轨 AC-3）：
        // 交给上层决定是告警还是致命错误。
        if (this.callbacks.onTrackUnsupported) this.callbacks.onTrackUnsupported(track, mime);
        else this.callbacks.onError?.(`不支持的 MIME: ${mime}`);
        return;
      }

      let buffer: SourceBuffer;
      try {
        buffer = ms.addSourceBuffer(mime);
      } catch (e) {
        // Chromium：媒体引擎一旦初始化（首个 media 段已 append），再 addSourceBuffer 会抛
        // QuotaExceededError("has reached the limit of SourceBuffer objects")。
        // 此处优雅降级——禁用该轨让另一轨继续播，而不是让异常冒泡成致命错误中断播放。
        this.disabled.add(track);
        const message = e instanceof Error ? e.message : String(e);
        // 先给上层自愈机会（重建媒体源重放，让两轨 SourceBuffer 都在 append 之前建好）；
        // 上层不接管时保持原有降级：禁用该轨并上报（另一轨继续播）。
        if (this.callbacks.onSourceBufferLimit?.(track, message) !== true) {
          this.callbacks.onError?.(`无法创建 ${track} SourceBuffer（已达上限或引擎已初始化）：${message}`);
        }
        return;
      }

      state = { buffer, queue: [], updating: false };
      const sbState = state;
      this.tracks.set(track, state);
      buffer.addEventListener("updateend", () => this.onUpdateEnd(track));
      buffer.addEventListener("error", () => {
        // 关键修复：浏览器拒收某段会派发 error 事件，但 updating 不会自动复位 → pump 永久阻塞、
        // 后续所有段卡在队列、缓冲冻结在首个成功段（起播空洞/卡死）。必须复位并继续泵队列。
        sbState.updating = false;
        debugWarn(`VIDERR ${track} SB error event (updating reset) — 浏览器拒收该分片 mime=${mime} codec=${codec}`);
        this.callbacks.onError?.(`SourceBuffer error: ${track}`);
        this.pump(track);
      });
    }
    this.enqueue(track, { data });
  }

  /** 追加 media segment；timestampOffset 单位秒。 */
  appendMedia(track: string, data: Uint8Array, timestampOffset?: number): void {
    if (this.disabled.has(track)) return;
    if (!this.opened) {
      // 同 appendInit：opening 期间暂存（顺序在对应 init 之后，回放时天然保序）
      if (this.opening) this.pushPreOpen({ kind: "media", track, data, timestampOffset });
      return;
    }
    if (!this.tracks.has(track)) return; // init 尚未到达/已被丢弃时的竞态数据，直接忽略
    this.enqueue(track, { data, timestampOffset });
  }

  private pushPreOpen(item: PreOpenItem): void {
    if (this.preOpenQueue.length >= MAX_PRE_OPEN_QUEUE) {
      // 优先丢最旧 media 段；全是 init 的极端情况丢队首（init 只出现在流头部，实际到不了这）
      const mediaIdx = this.preOpenQueue.findIndex((q) => q.kind === "media");
      if (mediaIdx >= 0) this.preOpenQueue.splice(mediaIdx, 1);
      else this.preOpenQueue.shift();
    }
    this.preOpenQueue.push(item);
  }

  /** 取走暂存段：由后端在 sourceopen 后经原回调路径回放，复用 hold/armed 建齐逻辑。 */
  drainPreOpen(): PreOpenItem[] {
    const items = this.preOpenQueue;
    this.preOpenQueue = [];
    return items;
  }

  /** 看门狗专用：重跑 media load 算法促使 UA 重新派发 sourceopen（仅 opening 阶段有效）。 */
  retriggerLoad(): void {
    if (!this.opening || this.opened || !this.mediaSource) return;
    try {
      this.video.load();
    } catch {
      /* 忽略 */
    }
  }

  private enqueue(track: string, item: { data: Uint8Array; timestampOffset?: number }): void {
    const state = this.tracks.get(track);
    if (!state) return;
    state.queue.push(item);
    this.pump(track);
  }

  /** SourceBuffer 串行化：一次只能有一个 append，其余排队。 */
  private pump(track: string): void {
    const state = this.tracks.get(track);
    if (!state || state.updating || state.queue.length === 0) return;
    if (this.held) return; // 启动 hold：等所有轨缓冲建齐再 append（见 held 注释）
    const ms = this.mediaSource;
    if (!ms || ms.readyState !== "open" || !this.hasBuffer(state.buffer)) {
      // SourceBuffer 已被移除/关闭：丢弃该轨待写数据，避免 appendBuffer 抛 "removed from parent" 错
      state.queue.length = 0;
      state.updating = false;
      return;
    }
    const item = state.queue.shift()!;
    try {
      if (item.timestampOffset !== undefined && state.buffer.timestampOffset !== item.timestampOffset) {
        state.buffer.timestampOffset = item.timestampOffset;
      }
      state.updating = true;
      // TS 5.7 起 Uint8Array 带 buffer 泛型参数，DOM 的 BufferSource 要求 ArrayBuffer 后端，此处安全转换
      state.buffer.appendBuffer(item.data as unknown as BufferSource);
      // 曾经满、现在写入成功 → 通知上游恢复拉流
      if (this.bufferFull) {
        this.bufferFull = false;
        this.callbacks.onBufferAvailable?.(track);
      }
    } catch (e) {
      state.updating = false;
      const code = (e as DOMException | undefined)?.code;
      if (code === 22) {
        // QuotaExceededError：缓冲真满。段放回队首、暂停上游拉流，并在缓冲腾出后自动重试。
        // （只靠 updateend 触发恢复会死锁：暂停后没有新 append → 不再有 updateend）
        state.queue.unshift(item);
        if (!this.bufferFull) {
          this.bufferFull = true;
          this.callbacks.onBufferFull?.(track);
        }
        this.scheduleFullRetry();
      } else {
        this.callbacks.onError?.(e instanceof Error ? e.message : String(e));
      }
    }
  }

  /** 缓冲腾出后自动重试被 QuotaExceeded 挡回的段（避免暂停后死锁）。 */
  private scheduleFullRetry(): void {
    if (this.fullRetryTimer !== null) return;
    this.fullRetryTimer = setTimeout(() => {
      this.fullRetryTimer = null;
      for (const track of this.tracks.keys()) this.pump(track);
    }, FULL_RETRY_MS);
  }

  /** 该 SourceBuffer 是否仍归属于当前 MediaSource（未被 removeSourceBuffer / detach 移除）。 */
  private hasBuffer(buffer: SourceBuffer): boolean {
    const ms = this.mediaSource;
    if (!ms) return false;
    return Array.from(ms.sourceBuffers).indexOf(buffer) !== -1;
  }

  private onUpdateEnd(track: string): void {
    const state = this.tracks.get(track);
    if (!state) return;
    state.updating = false;

    const ranges = this.readRanges(state.buffer);
    this.callbacks.onBufferUpdated?.(track, ranges);

    // 回收已播放区间，控制内存（低端设备重要，也避免缓冲无界增长）
    this.maybeEvict();

    this.pump(track);
  }

  /** 回收已播放且超过保留窗口的区间。 */
  private maybeEvict(): void {
    const t = this.video.currentTime;
    for (const state of this.tracks.values()) {
      if (state.updating) continue;
      const b = state.buffer.buffered;
      if (b.length === 0) continue;
      if (t - b.start(0) > this.keepBehind + 5) {
        try {
          // remove() 同样会触发 updateend：先置 updating 让本 tick 的 pump 让位，
          // 否则 remove 与后续 appendBuffer 竞争 → InvalidStateError，
          // 且 catch 走非 code 22 分支「不回队」→ 该段被永久吞掉（缓冲空洞/卡顿）。
          state.updating = true;
          state.buffer.remove(0, t - this.keepBehind);
        } catch {
          state.updating = false;
          /* 忽略 */
        }
      }
    }
  }

  private readRanges(buffer: SourceBuffer): BufferedRange[] {
    const ranges: BufferedRange[] = [];
    for (let i = 0; i < buffer.buffered.length; i++) {
      ranges.push({ start: buffer.buffered.start(i), end: buffer.buffered.end(i) });
    }
    return ranges;
  }

  /** 启动 hold：先不 append 任何数据，等各轨 addSourceBuffer 建齐（Chromium 首个 init append 即锁数）。 */
  hold(): void {
    this.held = true;
  }

  /** 放行：泵出启动期排队的数据。 */
  release(): void {
    this.held = false;
    for (const track of this.tracks.keys()) this.pump(track);
  }

  setDuration(seconds: number): void {
    const ms = this.mediaSource;
    if (!ms || !this.opened) return;
    try {
      // 注意比较方向必须取反：ms.duration 初值为 NaN，`NaN < seconds` 恒为 false，
      // 会导致首次设置被跳过（Infinity 尤其明显）。
      if (!(ms.duration >= seconds)) ms.duration = seconds;
    } catch {
      /* duration 设置可能被 UA 拒绝，忽略 */
    }
  }

  endOfStream(): void {
    const ms = this.mediaSource;
    if (!ms || !this.opened) return;
    try {
      if (ms.readyState === "open") ms.endOfStream();
    } catch {
      /* 忽略 */
    }
  }

  /** 移除已播放区间，控制内存（低端设备重要）。 */
  evict(keepBehind = 10, upTo?: number): void {
    const t = upTo ?? this.video.currentTime;
    const removeTo = Math.max(0, t - keepBehind);
    for (const state of this.tracks.values()) {
      if (state.updating) continue;
      try {
        state.buffer.remove(0, removeTo);
      } catch {
        /* 忽略 */
      }
    }
  }

  destroy(): void {
    if (this.fullRetryTimer !== null) {
      clearTimeout(this.fullRetryTimer);
      this.fullRetryTimer = null;
    }
    this.bufferFull = false;
    this.held = false;

    const ms = this.mediaSource;
    for (const state of this.tracks.values()) {
      state.queue.length = 0;
      // 必须显式摘除 SourceBuffer：否则旧媒体管线与其缓冲区一直存活，
      // 低端设备的 SourceBuffer 数量会被耗尽 → 新流 addSourceBuffer 抛
      // "This MediaSource has reached the limit of SourceBuffer objects"。
      if (ms && ms.readyState !== "closed") {
        try {
          ms.removeSourceBuffer(state.buffer);
        } catch {
          /* 忽略 */
        }
      }
    }
    this.tracks.clear();
    this.disabled.clear();

    // 先摘监听器：video.load() 会触发旧 MS 的 sourceclose，若不摘除/不加身份校验，
    // 该异步事件会把随后新建的会话置为未打开（无画面且无报错）。
    this.detachMediaSource?.();
    this.detachMediaSource = null;

    if (this.objectUrl) {
      URL.revokeObjectURL(this.objectUrl);
      this.objectUrl = null;
    }
    // 仅 removeAttribute("src") **不会**重置 video 元素的资源选择算法，旧 MediaSource
    // 仍被挂载；必须再调 load() 才会真正释放旧管线。
    this.video.removeAttribute("src");
    this.video.load();
    // MMS 会话结束：恢复远程播放（AirPlay），避免后续 native 后端无法投屏
    const el = this.video as HTMLVideoElement & { disableRemotePlayback?: boolean };
    try {
      el.disableRemotePlayback = false;
    } catch {
      /* 忽略 */
    }
    this.mediaSource = null;
    this.opened = false;
    this.opening = false;
    this.pendingOpens = [];
    this.preOpenQueue = [];
  }
}
