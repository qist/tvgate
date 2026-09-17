/**
 * 时域拉伸（WSOLA，clean-room 实现）。
 * 依据标准 WSOLA（Waveform Similarity Overlap-Add）算法重新实现：
 * 保音高变速——按「自然位置 + 相似度搜索」选帧，汉宁窗重叠相加输出，
 * 合成步长随目标速度变化，从而在不改采样率的前提下压缩/拉伸时长。
 *
 * 说明：引擎设计 §5.1 的拉伸核心为 WASM（URL 由 config 提供，性能更好）；
 * 本模块为无 WASM 时的纯 TS 回退实现，接口与 WASM 版一致（见 AudioStretcher）。
 */

export interface AudioStretcher {
  /** speed > 1 表示加快（压缩时长）；按通道返回拉伸后 PCM。 */
  process(input: Float32Array[], speed: number): Float32Array[];
  reset(): void;
}

function buildHann(size: number): Float32Array {
  const w = new Float32Array(size);
  for (let i = 0; i < size; i++) w[i] = 0.5 - 0.5 * Math.cos((2 * Math.PI * i) / (size - 1));
  return w;
}

const SEARCH_STEP = 2; // 相似度搜索步长（跳样降低运算量；参照实现逐样本，纯 TS 折中取 2）
/** 旁路迟滞：进入旁路的偏离上限（对齐参照 wasm-stretcher 的 1%）。 */
const BYPASS_ENTER = 0.01;
/** 旁路迟滞：已旁路时退出旁路的偏离上限（2%）——避免 speed 在 1 附近抖动时
 *  反复在「透传/拉伸」间切换（每次切换有缓冲尾部丢失与算力抖动）。 */
const BYPASS_EXIT = 0.02;

export class WsolaStretcher implements AudioStretcher {
  private buf: Float32Array[] = [];
  private inLen = 0;
  private bufStart = 0; // buf[0] 对应的绝对样本序号
  private natural = 0; // 下一帧的「自然」位置（绝对序号）
  private outAcc: Float32Array[] = [];
  private outNorm = new Float32Array(0);
  private outPos = 0;
  /** 上一帧的单声道混合（L+R，幅度尺度不影响归一化评分），匹配基准。 */
  private lastFrame: Float32Array | null = null;
  private monoScratch: Float32Array = new Float32Array(0);
  private bypassActive = false;

  private readonly frameSize: number;
  private readonly hop: number;
  private readonly searchWin: number;
  private readonly hann: Float32Array;

  constructor(
    private readonly channels: number,
    sampleRate: number,
    frameMs = 20,
  ) {
    this.frameSize = Math.max(128, Math.round((sampleRate * frameMs) / 1000));
    this.hop = this.frameSize >> 1;
    // 搜索窗 ±hop（48kHz 下 ±10ms，对齐参照实现 SEEK_MS=10）：窗太小找不到
    // 最佳波形对齐点，拉伸期会产生相位断裂的金属声/毛刺。
    this.searchWin = this.hop;
    this.hann = buildHann(this.frameSize);
    this.reset();
  }

  reset(): void {
    this.buf = [];
    this.inLen = 0;
    this.bufStart = 0;
    this.natural = 0;
    this.outAcc = [];
    this.outNorm = new Float32Array(0);
    this.outPos = 0;
    this.lastFrame = null;
    this.bypassActive = false;
  }

  process(input: Float32Array[], speed: number): Float32Array[] {
    if (input.length === 0 || input[0].length === 0) return [];
    // 迟滞旁路：进入 1% / 退出 2%（对齐参照 wasm-stretcher），避免 speed 在 1
    // 附近抖动时反复切换透传/拉伸。
    const bypass = Math.abs(speed - 1) < (this.bypassActive ? BYPASS_EXIT : BYPASS_ENTER);
    this.bypassActive = bypass;
    // 从未拉伸过（无内部状态）：纯透传零开销。一旦有状态（拉伸期结束回落 1x），
    // 必须走下方 COLA 排干——直接透传会把缓冲里未输出的音频丢掉（每次切换丢
    // 最多 frameSize+searchWin ≈ 40ms，听感为周期性毛刺/丢字）。
    if (bypass && this.inLen === 0 && this.outPos === 0 && this.natural === 0) return input;

    this.append(input);

    // bypass：delta=0 + 合成步长=hop。Hann 窗 50% 重叠满足 COLA 条件，归一化后
    // 逐样本重建输入——把拉伸期残留的输入无损、连续地排干，切换零丢失。
    const synthesisHop = bypass
      ? this.hop
      : Math.max(1, Math.min(this.frameSize, Math.round(this.hop / speed)));

    for (;;) {
      const needEnd = bypass
        ? this.natural + this.frameSize - this.bufStart
        : this.natural + this.searchWin + this.frameSize - this.bufStart;
      if (needEnd > this.inLen) break;

      const delta = bypass ? 0 : this.findBestDelta();
      const start = this.natural + delta - this.bufStart;
      this.overlapAdd(start, synthesisHop);
      if (bypass) {
        this.lastFrame = null;
      } else {
        this.fillMonoMix(start);
        this.lastFrame = this.monoScratch;
      }
      this.natural += this.hop;
    }

    const emitted = this.emit();
    this.trimInput();
    return emitted;
  }

  private append(input: Float32Array[]): void {
    const n = input[0].length;
    if (this.buf.length === 0) {
      this.buf = input.map((ch) => new Float32Array(ch));
      this.inLen = n;
      return;
    }
    const grown = new Float32Array(this.inLen + n);
    for (let c = 0; c < this.channels; c++) {
      grown.set(this.buf[c].subarray(0, this.inLen), 0);
      grown.set(input[c], this.inLen);
      this.buf[c] = grown;
    }
    this.inLen += n;
  }

  /** 把 [start, start+frameSize) 混成单声道写入 monoScratch（匹配基准）。 */
  private fillMonoMix(start: number): void {
    if (this.monoScratch.length < this.frameSize) this.monoScratch = new Float32Array(this.frameSize);
    const dst = this.monoScratch;
    const c0 = this.buf[0];
    if (this.channels > 1 && this.buf.length > 1) {
      const c1 = this.buf[1];
      for (let i = 0; i < this.frameSize; i++) dst[i] = c0[start + i] + c1[start + i];
    } else {
      for (let i = 0; i < this.frameSize; i++) dst[i] = c0[start + i];
    }
  }

  /**
   * 在自然位置附近搜索与上一帧最相似的偏移。
   * 评分对齐参照实现：单声道混合归一化互相关 dot / sqrt(候选能量)——
   * 未归一化的裸点积会偏向高能量位置（如鼓点），导致对齐点漂移、拉伸期产生
   * 相位断裂的可闻毛刺。
   */
  private findBestDelta(): number {
    if (!this.lastFrame) return 0;
    let bestDelta = 0;
    let bestScore = -Infinity;
    const base = this.natural - this.bufStart;
    const c0 = this.buf[0];
    const c1 = this.channels > 1 && this.buf.length > 1 ? this.buf[1] : null;
    const last = this.lastFrame;
    for (let delta = -this.searchWin; delta <= this.searchWin; delta += SEARCH_STEP) {
      const s = base + delta;
      if (s < 0 || s + this.frameSize > this.inLen) continue;
      let dot = 0;
      let energy = 0;
      if (c1) {
        for (let i = 0; i < this.frameSize; i += 2) {
          const v = c0[s + i] + c1[s + i];
          dot += v * last[i];
          energy += v * v;
        }
      } else {
        for (let i = 0; i < this.frameSize; i += 2) {
          const v = c0[s + i];
          dot += v * last[i];
          energy += v * v;
        }
      }
      const score = dot / Math.sqrt(energy + 1e-9);
      if (score > bestScore) {
        bestScore = score;
        bestDelta = delta;
      }
    }
    return bestDelta;
  }

  private overlapAdd(start: number, synthesisHop: number): void {
    const need = this.outPos + this.frameSize;
    if (this.outNorm.length < need) this.growOutput(need);
    for (let c = 0; c < this.channels; c++) {
      const src = this.buf[c];
      const dst = this.outAcc[c];
      for (let i = 0; i < this.frameSize; i++) {
        dst[this.outPos + i] += src[start + i] * this.hann[i];
      }
    }
    for (let i = 0; i < this.frameSize; i++) this.outNorm[this.outPos + i] += this.hann[i];
    this.outPos += synthesisHop;
  }

  private growOutput(need: number): void {
    const size = Math.max(need, this.outNorm.length * 2, 4096);
    const norm = new Float32Array(size);
    norm.set(this.outNorm.subarray(0, Math.min(this.outNorm.length, size)), 0);
    this.outNorm = norm;
    if (this.outAcc.length === 0) {
      this.outAcc = Array.from({ length: this.channels }, () => new Float32Array(size));
    } else {
      for (let c = 0; c < this.channels; c++) {
        const grown = new Float32Array(size);
        grown.set(this.outAcc[c].subarray(0, Math.min(this.outAcc[c].length, size)), 0);
        this.outAcc[c] = grown;
      }
    }
  }

  /**
   * 输出已「完整」的区间 [0, outPos)：后续帧只会写入 >= outPos 的位置，
   * 因此该区间不会再被补充，可安全归一化输出。
   */
  private emit(): Float32Array[] {
    if (this.outPos === 0) return [];
    const n = this.outPos;
    const out: Float32Array[] = [];
    for (let c = 0; c < this.channels; c++) {
      const src = this.outAcc[c];
      const dst = new Float32Array(n);
      for (let i = 0; i < n; i++) {
        const g = this.outNorm[i];
        dst[i] = g > 1e-6 ? src[i] / g : 0;
      }
      out.push(dst);
    }
    // 保留 [outPos, ...) 的未完结重叠部分
    const remain = this.outNorm.length - n;
    for (let c = 0; c < this.channels; c++) {
      const tail = new Float32Array(remain);
      tail.set(this.outAcc[c].subarray(n, n + remain), 0);
      this.outAcc[c] = tail;
    }
    const normTail = new Float32Array(remain);
    normTail.set(this.outNorm.subarray(n, n + remain), 0);
    this.outNorm = normTail;
    this.outPos = 0;
    return out;
  }

  /** 丢弃已不可能再被引用的输入前缀，控制内存。 */
  private trimInput(): void {
    const keepFrom = this.natural - this.searchWin - this.bufStart;
    if (keepFrom < 8192) return;
    const newLen = this.inLen - keepFrom;
    for (let c = 0; c < this.channels; c++) {
      const trimmed = new Float32Array(newLen);
      trimmed.set(this.buf[c].subarray(keepFrom, keepFrom + newLen), 0);
      this.buf[c] = trimmed;
    }
    this.inLen = newLen;
    this.bufStart += keepFrom;
  }
}

/** 直通实现：不做拉伸（用于未配置 WASM 且不需要变速时的最省算力回退）。 */
export class PassthroughStretcher implements AudioStretcher {
  process(input: Float32Array[], _speed: number): Float32Array[] {
    return input;
  }
  reset(): void {
    /* 无状态 */
  }
}
