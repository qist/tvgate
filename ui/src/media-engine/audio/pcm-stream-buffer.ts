/**
 * 软解 PCM 的流时间轴缓冲（本项目自研，clean-room）。
 *
 * worker 交出来的 PCM 块带的是**媒体时间**（MSE 时间轴，与 video.currentTime 同域，
 * 由 remuxer 的 dts 基点归一化）。本类维护一条「按时间排序、无重叠、间隙吸附」的
 * 连续时间轴，服务于三个场景：
 *   1. seek / 回前台 / 硬重同步时，从目标时间点重建排程队列（已解码内容不丢）；
 *   2. 按窗口给排程队列补货；
 *   3. 以播放头为基准回收过旧数据，避免长时间播放内存无界增长。
 *
 * 归一化规则（对应「流时间戳会抖动、断流重连会重发」的现实）：
 *   - 与前一块重叠 → 裁掉已被覆盖的头部；
 *   - 与前一块的空隙 ≤ PCM_GAP_SNAP_SEC → 视为抖动，直接接到前一块末尾（不制造静音数据）；
 *   - 与后一块重叠 → 只保留到后一块起点为止；
 *   - 与已有块起点几乎重合（< 1ms）→ 覆盖旧块（重连后的重复数据）。
 */

/** 吸收时间戳抖动的容差（秒）：小于它的空隙/重叠按 0 处理。 */
export const PCM_GAP_SNAP_SEC = 0.005;

/** 时间轴上的一个 PCM 片段（samples 为交错 Float32；startSec/endSec 为媒体时间）。 */
export interface BufferedPcm {
  samples: Float32Array;
  channels: number;
  sampleRate: number;
  startSec: number;
  endSec: number;
}

/** 块时长（秒）。 */
export function pcmDurationSec(chunk: BufferedPcm): number {
  return chunk.endSec - chunk.startSec;
}

/** 裁掉 [chunk.startSec, fromSec) 的头部；整块被覆盖时返回 null。 */
function trimHead(chunk: BufferedPcm, fromSec: number): BufferedPcm | null {
  if (fromSec <= chunk.startSec + PCM_GAP_SNAP_SEC) {
    return chunk;
  }
  const cutFrames = Math.round((fromSec - chunk.startSec) * chunk.sampleRate);
  const totalFrames = Math.floor(chunk.samples.length / chunk.channels);
  if (cutFrames >= totalFrames) {
    return null;
  }
  return {
    samples: chunk.samples.subarray(cutFrames * chunk.channels),
    channels: chunk.channels,
    sampleRate: chunk.sampleRate,
    startSec: chunk.startSec + cutFrames / chunk.sampleRate,
    endSec: chunk.endSec,
  };
}

export class PcmStreamBuffer {
  private chunks: BufferedPcm[] = [];

  get size(): number {
    return this.chunks.length;
  }

  get empty(): boolean {
    return this.chunks.length === 0;
  }

  first(): BufferedPcm | undefined {
    return this.chunks[0];
  }

  last(): BufferedPcm | undefined {
    return this.chunks[this.chunks.length - 1];
  }

  clear(): void {
    this.chunks = [];
  }

  /** 插入一块解码 PCM（归一化规则见文件头）。 */
  insert(chunk: BufferedPcm): void {
    const at = this.lowerBound(chunk.startSec);
    let normalized = chunk;

    const prev = at > 0 ? this.chunks[at - 1] : undefined;
    if (prev) {
      if (normalized.startSec > prev.endSec) {
        // 空隙：把本块整体前移接上前一块（时间戳抖动或丢块后的对齐）。
        const duration = pcmDurationSec(normalized);
        normalized = { ...normalized, startSec: prev.endSec, endSec: prev.endSec + duration };
      } else if (normalized.startSec < prev.endSec) {
        const trimmed = trimHead(normalized, prev.endSec);
        if (!trimmed) {
          return;
        }
        normalized = trimmed;
      }
    }

    const next = this.chunks[at];
    if (next && Math.abs(next.startSec - normalized.startSec) < 0.001) {
      // 同一位置重复到达（重连重发）：新数据覆盖旧数据。
      this.chunks[at] = normalized;
      return;
    }

    if (next && normalized.endSec > next.startSec) {
      // 与后一块重叠：只保留到后一块起点。
      const keepFrames = Math.round((next.startSec - normalized.startSec) * normalized.sampleRate);
      if (keepFrames <= 0) {
        return;
      }
      const totalFrames = Math.floor(normalized.samples.length / normalized.channels);
      const frames = Math.min(keepFrames, totalFrames);
      this.chunks.splice(at, 0, {
        ...normalized,
        samples: normalized.samples.subarray(0, frames * normalized.channels),
        endSec: normalized.startSec + frames / normalized.sampleRate,
      });
      return;
    }

    this.chunks.splice(at, 0, normalized);
  }

  /**
   * 返回 targetSec 所在块的下标；targetSec 落在块之间的空隙时，返回其前一块
   * （早于所有块则返回第一块，晚于所有块则返回最后一块）；空时间轴返回 -1。
   * 注意：返回值 ≥ 0 **不代表** targetSec 被覆盖，需要 `covers()` 复核。
   */
  indexAround(targetSec: number): number {
    if (this.chunks.length === 0) {
      return -1;
    }
    let low = 0;
    let high = this.chunks.length - 1;
    while (low <= high) {
      const mid = (low + high) >>> 1;
      const chunk = this.chunks[mid];
      if (targetSec >= chunk.startSec && targetSec < chunk.endSec) {
        return mid;
      }
      if (targetSec < chunk.startSec) {
        high = mid - 1;
      } else {
        low = mid + 1;
      }
    }
    return low > 0 ? low - 1 : Math.min(low, this.chunks.length - 1);
  }

  /** targetSec 是否被覆盖（endMarginSec：距块尾保留的余量，避免贴着边界取数据）。 */
  covers(targetSec: number, endMarginSec = 0): boolean {
    const at = this.indexAround(targetSec);
    if (at < 0) {
      return false;
    }
    const chunk = this.chunks[at];
    return targetSec >= chunk.startSec && targetSec < chunk.endSec - endMarginSec;
  }

  /**
   * 取 [fromSec, fromSec + windowSec) 内的片段（块头按 fromSec 裁齐），最多 limit 块。
   * 用于把已解码缓冲重新灌进排程队列。
   */
  slice(fromSec: number, windowSec: number, limit: number): BufferedPcm[] {
    const out: BufferedPcm[] = [];
    const startIndex = this.indexAround(fromSec);
    if (startIndex < 0) {
      return out;
    }
    const untilSec = fromSec + windowSec;
    let cursor = fromSec;
    for (let i = startIndex; i < this.chunks.length && out.length < limit; i++) {
      const source = this.chunks[i];
      if (source.endSec <= cursor + PCM_GAP_SNAP_SEC) {
        continue; // 已被前面的块覆盖
      }
      if (source.startSec >= untilSec) {
        break; // 超出本次窗口
      }
      const piece = trimHead(source, cursor);
      if (!piece) {
        continue;
      }
      out.push(piece);
      cursor = piece.endSec;
    }
    return out;
  }

  /**
   * 回收过旧数据：只有最早块落后 referenceSec 超过 maxBackwardSec 才动手，
   * 且保留 referenceSec 之前 keepBehindSec 秒内的内容（seek / 重连还要用）。
   */
  recycle(referenceSec: number, keepBehindSec: number, maxBackwardSec: number): void {
    const oldest = this.chunks[0];
    if (!oldest || referenceSec - oldest.startSec < maxBackwardSec) {
      return;
    }
    const cutoffSec = referenceSec - keepBehindSec;
    let drop = 0;
    while (drop < this.chunks.length && this.chunks[drop].endSec < cutoffSec) {
      drop++;
    }
    if (drop > 0) {
      this.chunks.splice(0, drop);
    }
  }

  /** 第一个 startSec >= targetSec 的下标（升序数组的二分下界）。 */
  private lowerBound(targetSec: number): number {
    let low = 0;
    let high = this.chunks.length;
    while (low < high) {
      const mid = (low + high) >>> 1;
      if (this.chunks[mid].startSec < targetSec) {
        low = mid + 1;
      } else {
        high = mid;
      }
    }
    return low;
  }
}
