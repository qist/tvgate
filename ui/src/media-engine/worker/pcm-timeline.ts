/**
 * 软解 PCM 时间轴映射（PCM 时间戳与 MSE 时间轴同基准）。
 *
 * 软解音频（mp2/ac3…）不经 MSE，其 PCM time 由 pipeline 归一到 MSE 时间轴（减视频首样本基准）。
 * 但 MP2/ac3 的 PES 常**先于首个视频 IDR 到达**（间隔可达 ~1.7s），导致首批 PCM 的 time 为负。
 * 若带着负 time 进 PCM 播放器，控制环会测得巨大初始漂移 → 起播硬重同步（掐链 + 静音 + 淡入）
 * → 表现为「第一帧后卡一下」。
 *
 * 本类在 worker 软解层就地消化这个偏移，复刻原始的两道工序：
 *  1. 首块：以视频锚点(0)为地板，整块在锚点之前 → 丢弃；跨锚点 → 裁掉前面那段，time 夹到 0。
 *  2. 后续块：按原始时间连续性排列。**小空洞**（PES 拼接抖动）填 0 bridge，保持背靠背排程；
 *     **大跳变**（断流/分片跳号恢复后源时间轴整体前跳，如整点切换跳过数个分片）必须保留——
 *     抹掉跳跃会让 PCM 轴永远落后 MSE 视频轴（视频按源 PTS 连续推进），数据一到就被当过期
 *     丢弃 → 表现为「画面恢复、声音永不回来」。
 * 这样首个进 PCM 播放器的块起始就是 0，与 video.currentTime 同起点，起播无硬重同步。
 */

/**
 * 桥接上限（秒）：原始时间空洞超过它即视为「源时间轴真跳变」并保留跳跃。
 * 正常 PES 拼接抖动远小于 1s；断流/分片跳号恢复的跳变通常数秒到数十秒。
 */
const MAX_BRIDGE_GAP_SEC = 1.0;

export class PcmTimeline {
  /** 原始（未裁剪）端点时间（秒），用于跨块连续性。 */
  private lastOriginalEnd: number | undefined;
  /** 输出（已裁剪）端点时间（秒），用于背靠背排程与空洞 bridge。 */
  private lastOutputEnd: number | undefined;
  /** 诊断计数：整块落在视频锚点前被丢弃的块数。 */
  private droppedChunks = 0;
  /** 诊断计数：跨锚点块被裁掉的样本数。 */
  private trimmedSamples = 0;

  /**
   * 计算单块 PCM 的输出时间与裁剪。
   * @param originalStart 该块原始媒体时间（秒，已减视频基准，可为负）
   * @param pcm 交错 PCM（Float32，长度 = 样本数 × 声道数）
   * @param sampleRate 采样率
   * @param channels 声道数
   * @returns 映射后的 { time, pcm }；整块在视频锚点前则返 null（丢弃）
   */
  map(
    originalStart: number,
    pcm: Float32Array,
    sampleRate: number,
    channels: number,
  ): { time: number; pcm: Float32Array } | null {
    const totalSamples = pcm.length / channels;
    if (totalSamples <= 0) return null;
    const duration = totalSamples / sampleRate;

    let rawOutputStart: number;
    if (this.lastOriginalEnd === undefined || this.lastOutputEnd === undefined) {
      // 首块：以视频锚点 0 为地板，负值留待下方裁剪
      rawOutputStart = originalStart;
    } else {
      // 后续块：贴合上一块输出端点。小空洞（< MAX_BRIDGE_GAP_SEC）填 0 bridge 不回退；
      // 大跳变保留跳跃，让 PCM 轴跟随源/视频轴（见文件头说明）。
      const distance = originalStart - this.lastOriginalEnd;
      const advance = distance > MAX_BRIDGE_GAP_SEC ? distance : distance > 0 ? 0 : distance;
      rawOutputStart = this.lastOutputEnd + advance;
    }

    let outputStart = rawOutputStart;
    let trimSamples = 0;
    if (outputStart < 0) {
      // 仍落在视频锚点之前：裁掉前面那段，time 夹到 0
      trimSamples = Math.min(totalSamples, Math.floor(-outputStart * sampleRate));
      outputStart = 0;
    }
    if (trimSamples > 0) this.trimmedSamples += trimSamples;

    const emittedSamples = totalSamples - trimSamples;
    if (emittedSamples <= 0) {
      this.droppedChunks++;
      return null; // 整块在锚点前 → 丢弃
    }

    const out = trimSamples > 0 ? pcm.subarray(trimSamples * channels) : pcm;

    this.lastOriginalEnd = originalStart + duration;
    this.lastOutputEnd = outputStart + emittedSamples / sampleRate;
    return { time: outputStart, pcm: out };
  }

  /** 切源/seek：时间映射与端点历史全部作废（新流是全新时间轴）。 */
  reset(): void {
    this.lastOriginalEnd = undefined;
    this.lastOutputEnd = undefined;
    this.droppedChunks = 0;
    this.trimmedSamples = 0;
  }

  /** 诊断计数：整块丢弃与锚点前裁剪的样本量（供 PcmWorkerStats 汇总上报）。 */
  getStats(): { behindAnchorDrops: number; trimmedAtAnchor: number } {
    return { behindAnchorDrops: this.droppedChunks, trimmedAtAnchor: this.trimmedSamples };
  }
}
