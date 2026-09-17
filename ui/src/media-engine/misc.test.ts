import { describe, it, expect } from "vitest";
import {
  liveEdgeMse,
  createLiveSessionAnchor,
  lagBehindLiveEdge,
  goLiveTargetMse,
  mseToWallClock,
  isNearLiveWallClock,
  NEAR_LIVE_EDGE_MS,
} from "./timeline";
import { identifyVideoCodec, identifyAudioCodec } from "./media-codecs";
import { PlayerErrors } from "./errors";
import { defaultConfig, createDefaultConfig } from "./config";

describe("timeline", () => {
  it("liveEdgeMse 按会话锚点外推直播边", () => {
    const anchor = createLiveSessionAnchor(10, 1000);
    // 过去 500ms → 直播边 = 10 + 0.5
    expect(liveEdgeMse(anchor, 1500)).toBeCloseTo(10.5, 6);
    expect(lagBehindLiveEdge(anchor, 10, 1500)).toBeCloseTo(0.5, 6);
    expect(goLiveTargetMse(anchor, 2, 1500)).toBeCloseTo(8.5, 6);
  });
  it("mseToWallClock / isNearLiveWallClock", () => {
    const origin = new Date("2026-01-01T00:00:00Z");
    expect(mseToWallClock(10, origin).getTime()).toBe(origin.getTime() + 10000);
    const now = origin.getTime() + 20000;
    // seek 目标距直播边 500ms < 10s → 视为接近直播
    expect(isNearLiveWallClock(new Date(origin.getTime() + 19500), null, origin, now)).toBe(true);
    expect(isNearLiveWallClock(new Date(origin.getTime() + 1000), null, origin, now)).toBe(false);
    expect(NEAR_LIVE_EDGE_MS).toBe(10000);
  });
});

describe("media-codecs", () => {
  it("视频 codec 识别", () => {
    expect(identifyVideoCodec("avc1.4d401f")).toBe("h264");
    expect(identifyVideoCodec("hvc1.1.6.L93.B0")).toBe("hevc");
    expect(identifyVideoCodec("vp09.00.10.08")).toBe("vp9");
    expect(identifyVideoCodec("av01.0.04M.08")).toBe("av1");
    expect(identifyVideoCodec("mp4a.40.2")).toBeUndefined();
  });
  it("音频 codec 识别", () => {
    expect(identifyAudioCodec("mp4a.40.2")).toBe("aac");
    expect(identifyAudioCodec("ac-3")).toBe("ac3");
    expect(identifyAudioCodec("ec-3")).toBe("eac3");
    expect(identifyAudioCodec("mp2")).toBe("mp2");
    expect(identifyAudioCodec("mp4a.40.34")).toBe("aac");
  });
});

describe("errors / config", () => {
  it("错误码取值稳定", () => {
    expect(PlayerErrors.REQUEST_FAILED).toBe("RequestFailed");
    expect(PlayerErrors.CODEC_UNSUPPORTED).toBe("CodecUnsupported");
  });
  it("defaultConfig 字段齐全且 createDefaultConfig 返回副本", () => {
    const c = createDefaultConfig();
    expect(c).toEqual(defaultConfig);
    c.liveSync = false;
    expect(defaultConfig.liveSync).toBe(true);
  });
});
