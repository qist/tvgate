/**
 * 分段源抽象（clean-room 实现）。
 * 行为见引擎设计 §5.2：ContinuousLiveSegmentSource（直播连续）与 StaticSegmentSource（点播/回看）。
 * 直播连续源只有一条不结束的 URL；点播源为有限分段列表；HLS 源另见 hls-source.ts。
 */

export interface SegmentSource {
  /** 是否为直播（持续产生新分段）。 */
  readonly live: boolean;
  /**
   * 取下一个待拉分段 URL。
   * - 返回 null 表示不再有分段（点播结束 / 直播已停止）。
   * - 直播场景下允许阻塞等待新分段产生。
   */
  next(): Promise<string | null>;
  destroy(): void;
  /**
   * 分段批次失效通知（可选实现）：分片连续不可用（如整点切换后旧序列分片被删、新分片未就绪）时，
   * 调用方丢弃尚未消费的旧分段，促使下一次 next() 去刷新播放列表、改用新序列续播。
   * 未实现则退化为「按原列表逐个重试」。
   */
  invalidatePending?(): void;
}

/** 静态分段列表（点播/回看）。 */
export class StaticSegmentSource implements SegmentSource {
  readonly live = false;
  private index = 0;

  constructor(private readonly urls: string[]) {}

  async next(): Promise<string | null> {
    if (this.index >= this.urls.length) return null;
    return this.urls[this.index++];
  }

  destroy(): void {
    this.index = this.urls.length;
  }
}

/**
 * 持续直播源：一条不结束的流 URL。
 * 只交付一次；URL 本身由下层（fetch + ReadableStream）持续读取。
 */
export class ContinuousLiveSegmentSource implements SegmentSource {
  readonly live = true;
  private served = false;

  constructor(private readonly url: string) {}

  async next(): Promise<string | null> {
    if (this.served) return null;
    this.served = true;
    return this.url;
  }

  destroy(): void {
    this.served = true;
  }
}
