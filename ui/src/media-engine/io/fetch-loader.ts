/**
 * 自包含 fetch 加载器（clean-room 实现）。
 * 依据 Fetch / Streams 公开标准重新实现，合并 IOController（缓冲 + 限速采样 + 分发）
 * 与 FetchStreamLoader（fetch + ReadableStream 泵循环）与 RangeSeekHandler（Range 头构造）。
 * 行为（见引擎设计 §5.8）：断点续传（range / restart）、重定向后取最终 URL、错误上报。
 */

export interface DataSource {
  url: string;
  cors?: boolean;
  withCredentials?: boolean;
  referrerPolicy?: string;
}

export interface LoaderErrorInfo {
  code: number; // HTTP 状态码；网络异常为 -1
  msg: string;
  url: string;
}

export type ResumeMode = "range" | "restart";

export interface ByteRange {
  from: number;
  to?: number; // 省略表示到流末尾
}

export interface FetchLoaderCallbacks {
  onDataArrival?(chunk: Uint8Array, byteStart: number, receivedLength: number): void;
  onComplete?(rangeFrom: number, rangeTo: number): void;
  onError?(info: LoaderErrorInfo): void;
  onRedirect?(finalUrl: string): void;
  onSpeedSample?(bytesPerSecond: number): void;
}

export interface FetchLoaderConfig {
  /** 断点续传模式：range=从已收字节继续；restart=从头重来。 */
  resumeMode?: ResumeMode;
  /** 速度采样间隔（毫秒）。 */
  speedSampleInterval?: number;
  /** 限速（字节/秒）；0 或省略表示不限速。 */
  bandwidthLimit?: number;
  /** 直播无数据看门狗（毫秒）；0/省略 = 关闭（仅直播源开启）。 */
  dataWatchdogMs?: number;
  headers?: Record<string, string>;
}

const SPEED_SAMPLE_INTERVAL = 500;
/**
 * 直播流"无数据"看门狗默认值（毫秒；0 = 关闭）。移动网络切换（WiFi→4G/5G）不会让旧 TCP
 * 连接立刻报错：请求会静默挂住直到系统级超时（可达 30s+），期间拉流泵零推进、播放停在缓冲上。
 * 主动掐连接交上层重连，能在秒级内把新连接建到新网络上。由 pipeline 对直播源开启。
 */
export const LIVE_DATA_TIMEOUT_MS = 20_000;

/** 构造 Range 请求头值，如 "bytes=1024-" / "bytes=0-2047"。 */
export function buildRangeHeader(range: ByteRange): string {
  return range.to === undefined ? `bytes=${range.from}-` : `bytes=${range.from}-${range.to}`;
}

export class FetchStreamLoader {
  private controller: AbortController | null = null;
  private receivedLength = 0;
  private rangeFrom = 0;
  private aborted = false;
  private completed = false;
  private lastSampleTime = 0;
  private lastSampleBytes = 0;
  private startTime = 0;
  private readonly extraHeaders: Record<string, string>;

  private readonly resumeMode: ResumeMode;
  private readonly speedSampleInterval: number;
  private readonly bandwidthLimit: number;
  private readonly dataWatchdogMs: number;
  private dataWatchdogTimer: ReturnType<typeof setTimeout> | null = null;
  /** 是否已因"长时间无数据"掐断本次请求（静默挂死的连接）。 */
  private stalled = false;

  constructor(
    private readonly source: DataSource,
    private readonly callbacks: FetchLoaderCallbacks = {},
    config: FetchLoaderConfig = {},
  ) {
    this.resumeMode = config.resumeMode ?? "range";
    this.speedSampleInterval = config.speedSampleInterval ?? SPEED_SAMPLE_INTERVAL;
    this.bandwidthLimit = config.bandwidthLimit ?? 0;
    this.dataWatchdogMs = config.dataWatchdogMs ?? 0;
    this.extraHeaders = config.headers ?? {};
  }

  /** 是否已传输完成。 */
  get isCompleted(): boolean {
    return this.completed;
  }

  /** 已接收字节数。 */
  get bytesReceived(): number {
    return this.receivedLength;
  }

  /** 是否因"长时间无数据"被看门狗掐断（静默挂死的连接，交上层重连）。 */
  get dataStalled(): boolean {
    return this.stalled;
  }

  /** 开始加载；range 省略表示从 0 开始。 */
  async open(range?: ByteRange): Promise<void> {
    this.aborted = false;
    this.rangeFrom = range?.from ?? 0;
    this.receivedLength = 0;
    this.lastSampleTime = Date.now();
    this.lastSampleBytes = 0;
    this.startTime = Date.now();

    const headers: Record<string, string> = { ...this.extraHeaders };
    if (range && (range.from > 0 || range.to !== undefined)) {
      headers["Range"] = buildRangeHeader(range);
    }

    this.controller = new AbortController();
    let response: Response;
    try {
      response = await fetch(this.source.url, {
        method: "GET",
        headers,
        mode: this.source.cors === false ? "no-cors" : "cors",
        credentials: this.source.withCredentials ? "include" : "omit",
        referrerPolicy: (this.source.referrerPolicy as ReferrerPolicy) || "no-referrer",
        signal: this.controller.signal,
      });
    } catch (e) {
      this.emitError(-1, e instanceof Error ? e.message : String(e));
      return;
    }

    // 重定向后取最终 URL
    if (response.url && response.url !== this.source.url) {
      this.callbacks.onRedirect?.(response.url);
    }

    if (!response.ok && !(response.status === 206)) {
      this.emitError(response.status, `HTTP ${response.status}`);
      return;
    }
    if (!response.body) {
      this.emitError(response.status, "响应无 body（当前环境不支持 ReadableStream）");
      return;
    }

    this.stalled = false;
    this.armDataWatchdog();
    await this.pump(response.body);
  }

  /** 重新计时无数据看门狗（每收到一段数据即刷新）。 */
  private armDataWatchdog(): void {
    if (this.dataWatchdogMs <= 0) return;
    this.clearDataWatchdog();
    this.dataWatchdogTimer = setTimeout(() => {
      this.dataWatchdogTimer = null;
      if (this.aborted || this.completed) return;
      // 无数据超时：掐断连接交上层重连（不在此上报错误，避免与重试预算打架）
      this.stalled = true;
      this.abort();
    }, this.dataWatchdogMs);
  }

  private clearDataWatchdog(): void {
    if (this.dataWatchdogTimer !== null) {
      clearTimeout(this.dataWatchdogTimer);
      this.dataWatchdogTimer = null;
    }
  }

  private async pump(body: ReadableStream<Uint8Array>): Promise<void> {
    const reader = body.getReader();
    try {
      for (;;) {
        if (this.aborted) break;
        const { done, value } = await reader.read();
        if (done) break;
        if (!value || value.length === 0) continue;

        await this.applyBandwidthLimit();

        const byteStart = this.rangeFrom + this.receivedLength;
        this.receivedLength += value.length;
        this.callbacks.onDataArrival?.(value, byteStart, this.receivedLength);
        this.sampleSpeed();
        this.armDataWatchdog();
      }
      if (!this.aborted) {
        this.completed = true;
        this.callbacks.onComplete?.(this.rangeFrom, this.rangeFrom + this.receivedLength - 1);
      }
    } catch (e) {
      if (!this.aborted) this.emitError(-1, e instanceof Error ? e.message : String(e));
    } finally {
      try {
        reader.releaseLock();
      } catch {
        /* 已释放 */
      }
    }
  }

  /**
   * 限速：按「已收字节 vs 按限速应允许收到的字节」的超收量换算等待时间，
   * 单次最多等 1s，避免长阻塞。
   */
  private applyBandwidthLimit(): Promise<void> {
    if (this.bandwidthLimit <= 0) return Promise.resolve();
    const elapsedSec = (Date.now() - this.startTime) / 1000;
    const allowed = elapsedSec * this.bandwidthLimit;
    const excess = this.receivedLength - allowed;
    if (excess <= 0) return Promise.resolve();
    const waitMs = Math.min((excess / this.bandwidthLimit) * 1000, 1000);
    if (waitMs <= 0) return Promise.resolve();
    return new Promise((resolve) => setTimeout(resolve, waitMs));
  }

  private sampleSpeed(): void {
    const now = Date.now();
    const elapsed = now - this.lastSampleTime;
    if (elapsed < this.speedSampleInterval) return;
    const bytes = this.receivedLength - this.lastSampleBytes;
    this.callbacks.onSpeedSample?.(Math.round((bytes * 1000) / elapsed));
    this.lastSampleTime = now;
    this.lastSampleBytes = this.receivedLength;
  }

  private emitError(code: number, msg: string): void {
    this.callbacks.onError?.({ code, msg, url: this.source.url });
  }

  /** 中断加载。 */
  abort(): void {
    this.clearDataWatchdog();
    this.aborted = true;
    this.controller?.abort();
    this.controller = null;
  }

  /** 中断后从何处续传（取决于 resumeMode）。 */
  getResumeRange(): ByteRange | undefined {
    if (this.resumeMode === "restart") return undefined;
    return { from: this.rangeFrom + this.receivedLength };
  }

  destroy(): void {
    this.abort();
    this.completed = false;
    this.receivedLength = 0;
  }
}

/**
 * IO 控制器：把到达的数据按阈值聚合后分发给消费者，并维护缓冲量与速度估计。
 * 与加载器解耦——加载器只负责取字节，本控制器负责缓冲/节流/分发。
 */
export class IOController {
  private pending: Uint8Array[] = [];
  private pendingBytes = 0;
  private pendingStart = 0;
  private speed = 0;
  private lastReport = 0;

  constructor(
    private readonly dispatch: (chunk: Uint8Array, byteStart: number) => void,
    private readonly options: { bufferThreshold?: number; onSpeedSample?: (bps: number) => void } = {},
  ) {}

  private get threshold(): number {
    return this.options.bufferThreshold ?? 64 * 1024;
  }

  /** 收到一段数据。 */
  onDataArrival(chunk: Uint8Array, byteStart: number): void {
    const now = Date.now();
    if (this.lastReport > 0) {
      const dt = (now - this.lastReport) / 1000;
      if (dt > 0) {
        const instant = chunk.length / dt;
        // 简单平滑：新值权重 0.3，保留历史 0.7
        this.speed = this.speed === 0 ? instant : this.speed * 0.7 + instant * 0.3;
        this.options.onSpeedSample?.(Math.round(this.speed));
      }
    }
    this.lastReport = now;

    if (this.pending.length === 0) this.pendingStart = byteStart;
    this.pending.push(chunk);
    this.pendingBytes += chunk.length;
    if (this.pendingBytes >= this.threshold) this.flush();
  }

  /** 把缓冲的数据一次性交出。 */
  flush(): void {
    if (this.pending.length === 0) return;
    let total = 0;
    for (const p of this.pending) total += p.length;
    const out = new Uint8Array(total);
    let o = 0;
    for (const p of this.pending) {
      out.set(p, o);
      o += p.length;
    }
    const start = this.pendingStart;
    this.pending.length = 0;
    this.pendingBytes = 0;
    this.pendingStart = 0;
    this.dispatch(out, start);
  }

  get bufferedBytes(): number {
    return this.pendingBytes;
  }

  get estimatedSpeed(): number {
    return Math.round(this.speed);
  }

  reset(): void {
    this.pending.length = 0;
    this.pendingBytes = 0;
    this.speed = 0;
    this.lastReport = 0;
  }
}
