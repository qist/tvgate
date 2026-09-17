/**
 * AudioContext 上的背靠背排程链（本项目自研，clean-room）。
 *
 * 软解 PCM 按块创建 `AudioBufferSourceNode`，每块的起点严格等于上一块的结束时刻
 * （由 AudioContext 时钟累加，sample-accurate、天然无缝）。**块的流时间戳不直接
 * 决定起点**——video.currentTime 的抖动若参与排程会产生可闻的咔哒声；
 * 流时间只用于把"当前在播的内容时间"映射回媒体时间轴（`playedStreamSec()`），
 * 供上层漂移环比较。
 *
 * 链（重新）启动时可按调用方给出的领先量整体推迟，使音频落回视频时钟之后；
 * 重启的首块做几毫秒淡入，避免从静默硬接到波形中间。
 */

/** 待排程的一块输出（交错 Float32；streamStart～streamEnd 是它对应的媒体时间区间）。 */
export interface ScheduleBlock {
  samples: Float32Array;
  channels: number;
  sampleRate: number;
  streamStartSec: number;
  streamEndSec: number;
}

/** 已排程但尚未播完的一段（上下文时钟区间 ↔ 媒体时间区间）。 */
interface ScheduledSpan {
  source: AudioBufferSourceNode;
  ctxStartSec: number;
  ctxEndSec: number;
  streamStartSec: number;
  streamEndSec: number;
}

/** 链（重新）启动时的最小延后（秒），确保 `source.start()` 的时刻仍在未来。 */
const MIN_RESTART_DELAY_SEC = 0.04;
/** 重启时首块的淡入时长（秒）。 */
const RESTART_FADE_SEC = 0.005;
/** 播放头起点未知时允许的最大延后（秒）——再大就是明显的音画脱节了。 */
const MAX_RESTART_LEAD_SEC = 2;
/** 认为"链还在运行"的容差：起点早于 now 超过它即视为已排空。 */
const CHAIN_LIVE_EPSILON_SEC = 0.005;
/** 已播完的 span 保留时长（秒），用于 playedStreamSec 的边界插值。 */
const SPAN_RETENTION_SEC = 0.5;

export class OutputChain {
  private nextStartSec = 0;
  private spans: ScheduledSpan[] = [];

  constructor(
    private readonly ctx: AudioContext,
    private readonly sink: AudioNode,
  ) {}

  /** 已排程内容的结束时刻（上下文时钟，秒）= 下一块的起点。 */
  get scheduledEndSec(): number {
    return this.nextStartSec;
  }

  /** 是否还有已排程的音频（含正在播的与已排未播的）。 */
  get hasScheduled(): boolean {
    return this.spans.length > 0;
  }

  /** 已排程音频的媒体时间终点；无排程时为 null。 */
  get lastStreamEndSec(): number | null {
    return this.spans.length > 0 ? this.spans[this.spans.length - 1].streamEndSec : null;
  }

  /** 排程一块；`leadSec` 为"本块内容领先视频时钟的量"（null = 无视频时钟参考）。 */
  append(block: ScheduleBlock, leadSec: number | null): void {
    const ctx = this.ctx;
    const ctxNow = ctx.currentTime;

    let restarted = false;
    if (this.nextStartSec < ctxNow + CHAIN_LIVE_EPSILON_SEC) {
      // 链（重新）启动：把起点整体推迟到领先量之后，让音频对齐视频时钟；
      // 无参考（后台 free-run / 恢复中）时只留最小延迟。
      const lead = leadSec === null ? 0 : Math.max(0, Math.min(leadSec, MAX_RESTART_LEAD_SEC));
      this.nextStartSec = ctxNow + Math.max(MIN_RESTART_DELAY_SEC, lead);
      restarted = true;
    }

    const frames = Math.floor(block.samples.length / block.channels);
    if (frames <= 0) {
      return;
    }

    const buffer = ctx.createBuffer(block.channels, frames, block.sampleRate);
    for (let ch = 0; ch < block.channels; ch++) {
      const channelData = buffer.getChannelData(ch);
      for (let i = 0; i < frames; i++) {
        channelData[i] = block.samples[i * block.channels + ch];
      }
    }
    if (restarted) {
      fadeIn(buffer, RESTART_FADE_SEC);
    }

    const source = ctx.createBufferSource();
    source.buffer = buffer;
    source.connect(this.sink);
    source.start(this.nextStartSec);

    this.spans.push({
      source,
      ctxStartSec: this.nextStartSec,
      ctxEndSec: this.nextStartSec + buffer.duration,
      streamStartSec: block.streamStartSec,
      streamEndSec: block.streamEndSec,
    });
    this.nextStartSec += buffer.duration;
    this.prune(ctxNow);
  }

  /**
   * 当前正在播放的内容时间（媒体时间轴，秒）：在已排程区间内线性插值。
   * 链空闲 / 尚未开播 / 已放完时返回 null。
   */
  playedStreamSec(ctxNowSec: number): number | null {
    for (const span of this.spans) {
      if (ctxNowSec >= span.ctxStartSec && ctxNowSec < span.ctxEndSec) {
        const ratio = (ctxNowSec - span.ctxStartSec) / (span.ctxEndSec - span.ctxStartSec);
        return span.streamStartSec + ratio * (span.streamEndSec - span.streamStartSec);
      }
    }
    return null;
  }

  /** 立即停止并断开所有排程（用于 seek/停止：不需要平滑）。 */
  stopNow(): void {
    for (const span of this.spans) {
      try {
        span.source.stop();
        span.source.disconnect();
      } catch {
        // 已停止/已断开的 source 再操作会抛错，忽略即可。
      }
    }
    this.spans = [];
    this.nextStartSec = 0;
  }

  /** 在 `whenSec`（上下文时钟）停止所有排程，供上层先做增益下潜再调用。 */
  stopAt(whenSec: number): void {
    for (const span of this.spans) {
      try {
        span.source.stop(whenSec);
      } catch {
        // 同上：忽略重复停止。
      }
    }
    this.spans = [];
    this.nextStartSec = 0;
  }

  /** 丢弃早已播完的 span（保留一小段用于播放头的边界插值）。 */
  private prune(ctxNowSec: number): void {
    while (this.spans.length > 0 && this.spans[0].ctxEndSec < ctxNowSec - SPAN_RETENTION_SEC) {
      try {
        this.spans[0].source.disconnect();
      } catch {
        // 忽略。
      }
      this.spans.shift();
    }
  }
}

/** 对缓冲区起始的几毫秒做线性淡入（消除链重启时的拼接爆音）。 */
function fadeIn(buffer: AudioBuffer, fadeSec: number): void {
  const fadeFrames = Math.min(Math.floor(fadeSec * buffer.sampleRate), buffer.length);
  if (fadeFrames <= 0) {
    return;
  }
  for (let ch = 0; ch < buffer.numberOfChannels; ch++) {
    const data = buffer.getChannelData(ch);
    for (let i = 0; i < fadeFrames; i++) {
      data[i] *= (i + 1) / fadeFrames;
    }
  }
}
