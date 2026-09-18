import { describe, expect, it } from "vitest";
import { AdtsDemuxer, probeAdtsStream } from "./adts-demuxer";
import type { DemuxedSample, TrackInfo } from "./ts-demuxer";

/**
 * 合成一个 ADTS 帧（无 CRC）：7 字节头 + payload。
 * 头部字段按 ISO/IEC 13818-7：syncword(12) / ID(1) / layer(2) / protection(1) /
 * profile(2) / samplingFrequencyIndex(4) / private(1) / channelConfig(3) / ... / frameLength(13)。
 */
function adtsFrame(payloadLength: number, fill = 0x22): Uint8Array {
  const frameLength = 7 + payloadLength;
  const f = new Uint8Array(frameLength);
  f[0] = 0xff;
  f[1] = 0xf1; // MPEG-4, layer 0, no CRC
  f[2] = (1 << 6) | (4 << 2) | 0; // profile=LC(1), samplingFrequencyIndex=4 → 44100, chCfg 高位=0
  f[3] = (2 << 6) | ((frameLength >> 11) & 0x03); // channelConfig=2（立体声）
  f[4] = (frameLength >> 3) & 0xff;
  f[5] = ((frameLength & 0x07) << 5) | 0x1f;
  f[6] = 0xfc;
  f.fill(fill, 7);
  return f;
}

function concat(parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((n, p) => n + p.byteLength, 0);
  const out = new Uint8Array(total);
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.byteLength;
  }
  return out;
}

function collect(frames: number, payloadLength = 100, chunkSize = Number.POSITIVE_INFINITY) {
  const tracks: TrackInfo[] = [];
  const samples: DemuxedSample[] = [];
  const layouts: { video: boolean; mseAudio: boolean }[] = [];
  const demuxer = new AdtsDemuxer({
    onTracks: (t) => tracks.push(...t),
    onSamples: (s) => samples.push(...s),
    onStreamLayout: (l) => layouts.push(l),
  });
  const stream = concat(Array.from({ length: frames }, () => adtsFrame(payloadLength)));
  for (let at = 0; at < stream.length; at += chunkSize) {
    demuxer.push(stream.subarray(at, Math.min(at + chunkSize, stream.length)));
  }
  return { demuxer, tracks, samples, layouts };
}

describe("probeAdtsStream", () => {
  it("裸 ADTS 流：认", () => {
    expect(probeAdtsStream(concat([adtsFrame(200), adtsFrame(200), adtsFrame(200)]))).toBe(true);
  });

  it("第二个帧头对不上就不认（拒绝随机 0xFF 造成的误判）", () => {
    const stream = concat([adtsFrame(200), adtsFrame(200)]);
    // 把第二帧头首字节改成非同步字：只剩一个合法帧头
    stream[207] = 0x00;
    expect(probeAdtsStream(stream)).toBe(false);
  });

  it("MPEG-TS 流不认", () => {
    const ts = new Uint8Array(188 * 3);
    for (let i = 0; i < 3; i++) ts[i * 188] = 0x47; // 只有 sync byte，载荷全 0
    expect(probeAdtsStream(ts)).toBe(false);
  });

  it("数据不足两个帧但首帧合法：先认（真流马上补齐）", () => {
    expect(probeAdtsStream(adtsFrame(200))).toBe(true);
  });
});

describe("AdtsDemuxer", () => {
  it("首帧发一条音轨 + 纯音频布局，样本为裸 AAC 帧、时间轴按 1024 采样累加", () => {
    const { tracks, samples, layouts } = collect(4, 100);
    expect(tracks).toHaveLength(1);
    // codec 串由 aacCodecMimeType 依 ASC 的 AOT 给出（与 TS 路径的 AAC 一致）
    expect(tracks[0]).toMatchObject({ kind: "audio", timescale: 44100, channels: 2 });
    expect(tracks[0].codec).toMatch(/^mp4a\.40\.\d$/);
    expect(tracks[0].codecPrivate.length).toBeGreaterThan(2); // esds
    expect(layouts).toEqual([{ video: false, mseAudio: true, softAudio: false }]);

    expect(samples).toHaveLength(4);
    expect(samples.map((s) => s.dts)).toEqual([0, 1024, 2048, 3072]);
    for (const s of samples) {
      expect(s.kind).toBe("audio");
      expect(s.data.byteLength).toBe(100); // 已剥掉 7 字节 ADTS 头
      expect(s.isKeyframe).toBe(true);
      expect(s.cts).toBe(0);
    }
  });

  it("块边界切在帧中间也不丢帧（半帧留到下一块拼接）", () => {
    const whole = collect(5, 100).samples;
    for (const chunkSize of [1, 3, 7, 13, 51, 107, 188]) {
      const { samples } = collect(5, 100, chunkSize);
      expect(samples.map((s) => s.dts)).toEqual(whole.map((s) => s.dts));
      expect(samples.map((s) => s.data.byteLength)).toEqual(whole.map((s) => s.data.byteLength));
    }
  });

  it("跨 push（= 跨 HLS 分片）时间轴连续，不归零", () => {
    const { demuxer, samples } = collect(3, 100);
    demuxer.push(concat([adtsFrame(100), adtsFrame(100)]));
    expect(samples.map((s) => s.dts)).toEqual([0, 1024, 2048, 3072, 4096]);
  });

  it("reset() 后重新从 0 起算（切台/重载语义）", () => {
    const { demuxer, samples } = collect(2, 100);
    demuxer.reset();
    demuxer.push(concat([adtsFrame(100), adtsFrame(100)]));
    expect(samples.map((s) => s.dts)).toEqual([0, 1024, 0, 1024]);
  });

  it("整块垃圾不会无界缓存（只留 7 字节等下个帧头）", () => {
    const { demuxer, samples } = collect(0);
    demuxer.push(new Uint8Array(4096).fill(0x11));
    demuxer.push(concat([adtsFrame(50)]));
    expect(samples).toHaveLength(1);
  });
});
