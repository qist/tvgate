/**
 * Fmp4Remuxer init 扣住：PMT 声明了视频但视频轨未注册时，音频 init/media 必须扣住
 * （音频先 append 会初始化媒体引擎，视频 SourceBuffer 从此建不出来 → 起播死循环）。
 * 实测源：江苏移动系 CDN 把 SPS/PPS/IDR 放在无 PTS 注入 PES 里，视频轨可比音频晚数秒。
 */
import { describe, it, expect, vi, afterEach } from "vitest";
import { Fmp4Remuxer, type RemuxTrackConfig } from "./fmp4-remuxer";
import { buildEsds } from "../formats/aac";

afterEach(() => {
  vi.useRealTimers();
});

function audioTrack(id: number): RemuxTrackConfig {
  return {
    id,
    kind: "audio",
    codec: "mp4a.40.2",
    timescale: 44100,
    channels: 2,
    sampleRate: 44100,
    codecPrivate: buildEsds(new Uint8Array([0x12, 0x10])),
  };
}

function videoTrack(id: number): RemuxTrackConfig {
  return {
    id,
    kind: "video",
    codec: "avc1.64001e",
    timescale: 90000,
    width: 64,
    height: 64,
    codecPrivate: new Uint8Array([0, 0, 0, 8, 0x61, 0x76, 0x63, 0x43]), // 伪 avcC（box 头形即可）
  };
}

function audioSample(id: number, dts: number) {
  return { trackId: id, data: new Uint8Array(4), dts, cts: 0, isKeyframe: true };
}

describe("Fmp4Remuxer 视频轨等待窗口（init 扣住）", () => {
  it("视频轨未注册时音频 init 不发；视频轨注册后两轨 init 一并发出", () => {
    const inits: string[] = [];
    const remuxer = new Fmp4Remuxer(
      { onInitSegment: (s) => inits.push(s.kind) },
      { startupGraceMs: 60_000 }, // 宽限拉长：窗口内绝不放行
    );
    remuxer.setVideoExpected(true);
    const audio = audioTrack(1);
    remuxer.addTrack(audio);
    remuxer.addSample(audioSample(1, 0));
    remuxer.addSample(audioSample(1, 1024));
    expect(inits).toEqual([]); // 音频 init 被扣住

    remuxer.addTrack(videoTrack(2));
    // addTrack → emitInitIfNeeded：视频轨已注册，音频 init 放行；视频 init 视 codecPrivate 就绪同批发出
    expect(inits).toContain("audio");
    expect(inits).toContain("video");
  });

  it("宽限到期仍无视频轨：放行音频 init，退化为纯音频，不卡死", () => {
    vi.useFakeTimers();
    const inits: string[] = [];
    const remuxer = new Fmp4Remuxer({ onInitSegment: (s) => inits.push(s.kind) }, { startupGraceMs: 1_000 });
    remuxer.setVideoExpected(true);
    remuxer.addTrack(audioTrack(1));
    remuxer.addSample(audioSample(1, 0));
    expect(inits).toEqual([]);
    vi.advanceTimersByTime(1_100);
    remuxer.addSample(audioSample(1, 1024)); // 再触发一次成段检查
    expect(inits).toContain("audio");
  });

  it("videoExpected=false（纯音频流）：音频 init 立即发出，不受等待窗口影响", () => {
    const inits: string[] = [];
    const remuxer = new Fmp4Remuxer({ onInitSegment: (s) => inits.push(s.kind) }, { startupGraceMs: 60_000 });
    remuxer.setVideoExpected(false);
    remuxer.addTrack(audioTrack(1));
    expect(inits).toEqual(["audio"]);
  });
});
