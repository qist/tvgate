/**
 * 指数哥伦布（Exp-Golomb）位流读取器（本项目自研实现）。
 *
 * 语法依据（公开规范）：
 *   - ITU-T H.264 §9.1 / ITU-T H.265 §9.2：ue(v) 为「z 个 0 + 1 + z 位后缀」，
 *     数值 = 2^z - 1 + 后缀；se(v) 是 ue(v) 的 k 映射：
 *     码值 0→0、1→+1、2→-1、3→+2 …… 即奇数取 (k+1)/2，偶数取 -(k/2)。
 *
 * 实现要点（与「逐位读取」的常见写法不同，这里按字节做窗口）：
 *   - 字节按需装入 32 位窗口，窗口内始终 ≤ 31 位，规避 JS 位运算的符号位陷阱；
 *   - 前导数零用 256 项查表按字节推进（只在窗口尾部不足 8 位时才退化为逐位）；
 *   - 读到数据末尾之外（位流损坏）抛 EngineError，交由调用方按「该参数集无效」处理。
 */
import { EngineError } from "../utils/exception";

/** 单字节前导零个数查表（0x00 的 8 个零由调用方按整字节处理）。 */
const LEADING_ZEROS_IN_BYTE = (() => {
  const table = new Uint8Array(256);
  for (let byte = 0; byte < 256; byte++) {
    let zeros = 0;
    for (let bit = 7; bit >= 0 && (byte & (1 << bit)) === 0; bit--) {
      zeros++;
    }
    table[byte] = zeros;
  }
  return table;
})();

export default class ExpGolomb {
  private data: Uint8Array | null;
  /** 下一次 refill 的读取位置（字节）。 */
  private loadPos = 0;
  /** 位窗口：低 bitsHeld 位是尚未消费的比特，按「高位先出」排列。 */
  private window = 0;
  private bitsHeld = 0;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  /** 释放对底层数据的引用（解析完成后调用，避免长时间持有整个 RBSP）。 */
  destroy(): void {
    this.data = null;
  }

  /** 读取 n 位无符号整数（0 ≤ n ≤ 32）。 */
  readBits(n: number): number {
    if (n < 0 || n > 32) {
      throw new EngineError("argument", `ExpGolomb.readBits: 位宽 ${n} 超出 0..32`);
    }
    if (n === 0) {
      return 0;
    }

    this.refill();
    if (n <= this.bitsHeld) {
      this.bitsHeld -= n;
      return (this.window >>> this.bitsHeld) & ((1 << n) - 1);
    }

    // 跨越窗口边界：先取窗口内剩余位，再补读剩余部分。
    const highBits = this.bitsHeld;
    const high = highBits > 0 ? this.window & ((1 << highBits) - 1) : 0;
    this.bitsHeld = 0;
    this.refill();

    const lowBits = n - highBits;
    if (lowBits > this.bitsHeld) {
      throw new EngineError("bitstream", "ExpGolomb: 位流提前结束");
    }
    this.bitsHeld -= lowBits;
    const low = lowBits > 0 ? (this.window >>> this.bitsHeld) & ((1 << lowBits) - 1) : 0;
    // 用乘加而非 << ：32 位结果在 JS 里会变成负数。
    return (high * 2 ** lowBits + low) >>> 0;
  }

  readBool(): boolean {
    return this.readBits(1) === 1;
  }

  readByte(): number {
    return this.readBits(8);
  }

  /** ue(v)：先数前导零，再读等长后缀。 */
  readUEG(): number {
    let zeros = 0;
    for (;;) {
      this.refill();
      if (this.bitsHeld === 0) {
        throw new EngineError("bitstream", "ExpGolomb: 位流提前结束（ue 前导零扫描）");
      }

      if (this.bitsHeld >= 8) {
        // 一次看 8 位：整字节为零则跳 8 位，否则定位到分隔用的那个 1。
        const top = (this.window >>> (this.bitsHeld - 8)) & 0xff;
        const z = LEADING_ZEROS_IN_BYTE[top];
        if (z === 8) {
          this.bitsHeld -= 8;
          zeros += 8;
          continue;
        }
        this.bitsHeld -= z + 1;
        zeros += z;
        break;
      }

      const bit = (this.window >>> (this.bitsHeld - 1)) & 1;
      this.bitsHeld -= 1;
      if (bit === 1) {
        break;
      }
      zeros += 1;
    }

    return zeros === 0 ? 0 : 2 ** zeros - 1 + this.readBits(zeros);
  }

  /** se(v)：ue(v) 码值的符号映射（见文件头说明）。 */
  readSEG(): number {
    const code = this.readUEG();
    return (code & 1) === 1 ? (code + 1) / 2 : -(code / 2);
  }

  /** 把窗口补到「至少 24 位」或字节流耗尽为止（窗口最大 31 位，永不溢出）。 */
  private refill(): void {
    const data = this.data;
    if (!data) {
      throw new EngineError("state", "ExpGolomb: 位流已释放");
    }
    while (this.bitsHeld <= 23 && this.loadPos < data.length) {
      this.window = ((this.window << 8) | data[this.loadPos++]) >>> 0;
      this.bitsHeld += 8;
    }
  }
}
