/**
 * PcmStreamBuffer 回归：时间轴的归一化 / 补货窗口 / 回收阈值。
 *
 * 这些规则直接决定 seek 后能否"一个字节不丢地"重建排程链，以及长时间播放会不会
 * 内存无界增长，属于播放链路的关键不变量。
 */
import { describe, expect, it } from "vitest";
import { PcmStreamBuffer, PCM_GAP_SNAP_SEC, type BufferedPcm } from "./pcm-stream-buffer";

const SAMPLE_RATE = 48000;

/** 造一块 PCM：时长 durationSec，起点 startSec（样本按秒数填充满即可）。 */
function chunk(startSec: number, durationSec: number, sampleRate = SAMPLE_RATE): BufferedPcm {
  const frames = Math.round(durationSec * sampleRate);
  return {
    samples: new Float32Array(frames),
    channels: 1,
    sampleRate,
    startSec,
    endSec: startSec + frames / sampleRate,
  };
}

describe("PcmStreamBuffer 插入归一化", () => {
  it("小空隙（≤ GAP_SNAP）吸附为连续：后一块直接接到前一块末尾", () => {
    const buffer = new PcmStreamBuffer();
    buffer.insert(chunk(0, 1));
    buffer.insert(chunk(1.002, 1)); // 2ms 空隙

    expect(buffer.size).toBe(2);
    expect(buffer.last()?.startSec).toBeCloseTo(1, 6);
    expect(buffer.last()?.endSec).toBeCloseTo(2, 6);
  });

  it("重叠：裁掉已被覆盖的头部", () => {
    const buffer = new PcmStreamBuffer();
    buffer.insert(chunk(0, 1));
    buffer.insert(chunk(0.5, 1)); // 与第一块重叠 0.5s

    expect(buffer.size).toBe(2);
    const second = buffer.last();
    expect(second?.startSec).toBeCloseTo(1, 6);
    expect(second?.endSec).toBeCloseTo(1.5, 6);
    // 裁掉 0.5s 头部 = 24000 帧
    expect(second?.samples.length).toBe(24000);
  });

  it("完全被前一块覆盖的块被丢弃", () => {
    const buffer = new PcmStreamBuffer();
    buffer.insert(chunk(0, 2));
    buffer.insert(chunk(0.5, 1)); // 完全落在第一块内部

    expect(buffer.size).toBe(1);
  });

  it("同一起点重复到达（重连重发）时覆盖旧块", () => {
    const buffer = new PcmStreamBuffer();
    const first = chunk(0, 1);
    first.samples.fill(1);
    const again = chunk(0, 1);
    again.samples.fill(2);
    buffer.insert(first);
    buffer.insert(again);

    expect(buffer.size).toBe(1);
    expect(buffer.first()?.samples[0]).toBe(2);
  });

  it("与后一块重叠时只保留到后一块起点", () => {
    const buffer = new PcmStreamBuffer();
    buffer.insert(chunk(2, 1)); // 先有后一块
    buffer.insert(chunk(1, 2)); // 覆盖 1~3，应裁到 1~2

    expect(buffer.size).toBe(2);
    expect(buffer.first()?.startSec).toBeCloseTo(1, 6);
    expect(buffer.first()?.endSec).toBeCloseTo(2, 6);
  });
});

describe("PcmStreamBuffer 查询与补货", () => {
  it("indexAround 覆盖判定：块内返回自身，空隙返回前一块（但不该被 covers 认可）", () => {
    const buffer = new PcmStreamBuffer();
    buffer.insert(chunk(0, 1));
    buffer.insert(chunk(3, 1));

    expect(buffer.indexAround(0.5)).toBe(0);
    expect(buffer.indexAround(3.5)).toBe(1);
    expect(buffer.covers(0.5)).toBe(true);
    expect(buffer.covers(2)).toBe(false); // 处于 1~3 的空隙
  });

  it("covers 支持块尾余量（避免贴着边界取数据）", () => {
    const buffer = new PcmStreamBuffer();
    buffer.insert(chunk(0, 1));
    expect(buffer.covers(0.95, 0.1)).toBe(false);
    expect(buffer.covers(0.85, 0.1)).toBe(true);
  });

  it("slice 只取窗口内的块，并把窗口起点处的块头裁齐", () => {
    const buffer = new PcmStreamBuffer();
    buffer.insert(chunk(0, 1));
    buffer.insert(chunk(1, 1));
    buffer.insert(chunk(2, 1));

    const pieces = buffer.slice(0.5, 1.5, 100);

    expect(pieces.length).toBe(2);
    expect(pieces[0].startSec).toBeCloseTo(0.5, 6);
    expect(pieces[0].endSec).toBeCloseTo(1, 6);
    expect(pieces[1].startSec).toBeCloseTo(1, 6);
    // 第二块起点 2 已经超出窗口（0.5 + 1.5 = 2 不含），故不再取
  });

  it("slice 遵守 limit（待排程队列上限）", () => {
    const buffer = new PcmStreamBuffer();
    for (let i = 0; i < 10; i++) buffer.insert(chunk(i, 1));
    expect(buffer.slice(0, 100, 3).length).toBe(3);
  });
});

describe("PcmStreamBuffer 回收", () => {
  it("落后不足 maxBackward 时不动（避免频繁裁剪）", () => {
    const buffer = new PcmStreamBuffer();
    buffer.insert(chunk(0, 1));
    buffer.insert(chunk(1, 1));
    buffer.recycle(20, 8, 30);
    expect(buffer.size).toBe(2);
  });

  it("落后超过 maxBackward 时，回收 keepBehind 之前的内容", () => {
    // 注意：插入时空隙会被吸附成连续（时间轴恒为一条无洞的链），
    // 所以这里用 40 个 1s 块造出 0~40s 的连续时间轴。
    const buffer = new PcmStreamBuffer();
    for (let i = 0; i < 40; i++) {
      buffer.insert(chunk(i, 1));
    }

    buffer.recycle(41, 8, 30); // cutoff = 41 - 8 = 33

    expect(buffer.size).toBe(8); // 保留 32~40s
    expect(buffer.first()?.startSec).toBeCloseTo(32, 6);
  });

  it("GAP_SNAP 常量与导出保持一致（下游按同一容差吸附游标）", () => {
    expect(PCM_GAP_SNAP_SEC).toBeCloseTo(0.005, 9);
  });
});
