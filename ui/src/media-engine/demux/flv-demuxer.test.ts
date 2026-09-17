/**
 * HTTP-FLV 解封装回归（真实 ffmpeg 素材，自产）。
 *
 * fixtures（testdata/）：testsrc2 320x180@15fps 1.5s + sine 48kHz 单声道 AAC
 *   - test-h264.flv：H.264（main profile / B 帧）+ AAC（legacy codecId 7）
 *   - test-h265.flv：H.265（Enhanced-RTMP：tag 头 bit7 置位 + fourcc hvc1，packetType 0/1）+ AAC
 *
 * 回归点：FLV 魔数探测、avcC/hvcC/esds 作为 codecPrivate（须含 box 头）、
 * 首个视频样本必为关键帧、dts 单调、H.264 B 帧 cts、分块喂入与整块等量。
 */
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { FlvDemuxer } from "./flv-demuxer";
import type { DemuxedSample, TrackInfo } from "./ts-demuxer";

interface RunResult {
  tracks: TrackInfo[];
  samples: DemuxedSample[];
  layouts: { video: boolean; mseAudio: boolean; softAudio?: boolean }[];
}

function fixtureBytes(name: string): Uint8Array {
  return new Uint8Array(readFileSync(new URL(`./testdata/${name}`, import.meta.url)));
}

function run(data: Uint8Array, chunkSize = data.byteLength): RunResult {
  const tracks: TrackInfo[] = [];
  const samples: DemuxedSample[] = [];
  const layouts: RunResult["layouts"] = [];
  const demuxer = new FlvDemuxer({
    onTracks: (t) => tracks.push(...t),
    onSamples: (s) => samples.push(...s),
    onStreamLayout: (l) => layouts.push(l),
  });
  for (let off = 0; off < data.byteLength; off += chunkSize) {
    demuxer.push(data.subarray(off, Math.min(off + chunkSize, data.byteLength)));
  }
  return { tracks, samples, layouts };
}

/** codecPrivate 的 box 类型（第 4~8 字节）。 */
function boxType(data?: Uint8Array): string {
  return data && data.byteLength >= 8 ? String.fromCharCode(data[4], data[5], data[6], data[7]) : "";
}

function videoSamples(result: RunResult): DemuxedSample[] {
  return result.samples.filter((s) => s.kind === "video");
}

function audioSamples(result: RunResult): DemuxedSample[] {
  return result.samples.filter((s) => s.kind === "audio");
}

describe("FlvDemuxer.probe", () => {
  it("识别 FLV 魔数；不足 3 字节要求更多数据", () => {
    expect(FlvDemuxer.probe(new Uint8Array([0x46, 0x4c])).needMoreData).toBe(true);
    expect(FlvDemuxer.probe(new Uint8Array([0x46, 0x4c, 0x56, 0x01])).match).toBe(true);
    expect(FlvDemuxer.probe(new Uint8Array([0x47, 0x40, 0x11])).match).toBe(false); // TS 同步字节
  });
});

describe("FLV 解复用（真实素材）", () => {
  it("H.264 + AAC：轨道元数据 / codecPrivate box / 关键帧门控 / B 帧 cts", () => {
    const result = run(fixtureBytes("test-h264.flv"));
    const video = result.tracks.find((t) => t.kind === "video");
    const audio = result.tracks.find((t) => t.kind === "audio");

    expect(video).toBeDefined();
    expect(video?.codec).toMatch(/^avc1\.[0-9a-f]{6}$/);
    expect(boxType(video?.codecPrivate)).toBe("avcC");
    expect(video?.timescale).toBe(90000);
    expect(video?.width).toBe(320);
    expect(video?.height).toBe(180);
    expect(video?.scanType).toBe("progressive"); // 素材 SPS frame_mbs_only_flag=1

    expect(audio).toBeDefined();
    expect(audio?.codec).toMatch(/^mp4a\.40\./);
    expect(boxType(audio?.codecPrivate)).toBe("esds");
    expect(audio?.timescale).toBe(48000);
    expect(audio?.sampleRate).toBe(48000);

    const vs = videoSamples(result);
    const as = audioSamples(result);
    expect(vs.length).toBeGreaterThanOrEqual(10); // 1.5s@15fps ≈ 22 帧（首关键帧前会有丢弃）
    expect(vs[0].isKeyframe).toBe(true);
    expect(as.length).toBeGreaterThanOrEqual(30); // ≈1.5s / 1024 采样

    // dts 单调不减（直播时间轴不可倒退）
    for (let i = 1; i < vs.length; i++) expect(vs[i].dts).toBeGreaterThanOrEqual(vs[i - 1].dts);
    // B 帧 → 存在 cts > 0 的样本
    expect(vs.some((s) => s.cts > 0)).toBe(true);
    // layout：video + 进 MSE 的 AAC
    expect(result.layouts.at(-1)).toEqual({ video: true, mseAudio: true, softAudio: false });
  });

  it("H.265（Enhanced-RTMP）+ AAC：hvcC 与 hvc1 codec 串", () => {
    const result = run(fixtureBytes("test-h265.flv"));
    const video = result.tracks.find((t) => t.kind === "video");

    expect(video).toBeDefined();
    expect(video?.codec).toMatch(/^hvc1\./);
    expect(boxType(video?.codecPrivate)).toBe("hvcC");
    expect(video?.timescale).toBe(90000);
    expect(video?.width).toBe(320);
    expect(video?.height).toBe(180);
    expect(video?.scanType).toBe("progressive"); // 素材 VUI field_seq_flag=0

    const vs = videoSamples(result);
    expect(vs.length).toBeGreaterThanOrEqual(10);
    expect(vs[0].isKeyframe).toBe(true);
    for (let i = 1; i < vs.length; i++) expect(vs[i].dts).toBeGreaterThanOrEqual(vs[i - 1].dts);
    // 样本为 length-prefixed NALU（首 4 字节长度 + HEVC NALU 头）
    expect(vs[0].data.byteLength).toBeGreaterThan(4);
  });

  it("分块喂入（1KB / 7 字节）与整块喂入等量（tag 边界无损）", () => {
    const data = fixtureBytes("test-h264.flv");
    const whole = run(data);
    for (const chunkSize of [1024, 7]) {
      const chunked = run(data, chunkSize);
      expect(chunked.tracks.length).toBe(whole.tracks.length);
      expect(videoSamples(chunked).length).toBe(videoSamples(whole).length);
      expect(audioSamples(chunked).length).toBe(audioSamples(whole).length);
      expect(videoSamples(chunked)[0].dts).toBe(videoSamples(whole)[0].dts);
    }
  });
});
