/**
 * Annex-B 起始码扫描（本项目自研）。
 *
 * 规范（ISO/IEC 14496-10 Annex B / ITU-T H.264 Annex B、H.265 Annex B）：
 * NAL 单元以 3 字节起始码 `00 00 01` 或 4 字节起始码 `00 00 00 01` 分隔。
 *
 * 边界语义（保持调用方既有约定）：
 *   - 4 字节起始码允许**恰好结束于缓冲区末尾**（随后没有 NAL 数据，属畸形流，
 *     但仍按起始码报告，交给上层按空 NAL 处理）；
 *   - 3 字节起始码要求其后**至少还有一个字节**（3 字节码后面必需至少 1 字节头）。
 */

/**
 * 返回 `startOffset` 起（含）下一个起始码的偏移；找不到时返回 `data.byteLength`。
 * 返回的偏移对 4 字节码指向第一个 0x00，对 3 字节码指向 `00 00 01` 的第一个 0x00。
 */
export function findAnnexBStartCodeOffset(data: Uint8Array, startOffset: number): number {
  const length = data.byteLength;
  // 起始码的最小可能位置：3 字节码的 0x01 落在 startOffset + 2 处。
  for (let cursor = Math.max(startOffset, 0) + 2; cursor < length; cursor++) {
    if (data[cursor] !== 0x01 || data[cursor - 1] !== 0x00 || data[cursor - 2] !== 0x00) {
      continue;
    }

    // 优先识别 4 字节码：多出的那个 0x00 属于起始码本身，不应算进上一段 NAL。
    const fourByteOffset = cursor - 3;
    if (fourByteOffset >= startOffset && data[fourByteOffset] === 0x00) {
      return fourByteOffset;
    }

    // 3 字节码：必须留得下 NAL 头字节。
    if (cursor + 1 < length) {
      return cursor - 2;
    }
  }

  return length;
}
