import { describe, it, expect, vi } from "vitest";
import { PCMAudioPlayer } from "./pcm-audio-player";

/** 假 video：只提供本组测试用到的字段。 */
function fakeVideo(buffered: Array<[number, number]>, currentTime = 0): HTMLVideoElement {
  return {
    currentTime,
    paused: false,
    seeking: false,
    readyState: 4,
    buffered: {
      length: buffered.length,
      start: (i: number) => buffered[i][0],
      end: (i: number) => buffered[i][1],
    },
  } as unknown as HTMLVideoElement;
}

// biome-ignore lint/suspicious/noExplicitAny: 测试内访问私有成员
type AnyPlayer = any;

/** 排程链替身：只为 heardStreamTime() 提供"正在播的内容时间"。 */
function fakeChain(heardSec: number) {
  return {
    playedStreamSec: () => heardSec,
    hasScheduled: true,
    lastStreamEndSec: heardSec,
    scheduledEndSec: 0,
    append: () => {},
    stopAt: () => {},
    stopNow: () => {},
  };
}

/** 构造播放器并注入假 video；可选注入"正在播的内容时间"（heardStreamTime 的来源）。 */
function makePlayer(video: HTMLVideoElement, heardSec?: number): AnyPlayer {
  const player = new PCMAudioPlayer({ clock: () => video.currentTime });
  const p = player as AnyPlayer;
  p.video = video;
  if (heardSec !== undefined) {
    p.audioCtx = { currentTime: 100 };
    p.chain = fakeChain(heardSec);
  }
  return p;
}

describe("PCMAudioPlayer 回前台对齐（视频追音频）", () => {
  it("目标在缓冲内：把 video 跳到正在听到的位置，并抑制常规 seek 处理", () => {
    const video = fakeVideo([[0, 30]], 10);
    const p = makePlayer(video, 11);
    p.pendingForegroundAlign = true;

    p.alignToVideoOnReturn();

    expect(video.currentTime).toBeCloseTo(11, 3);
    expect(p.aligningSeek).toBe(true);
    expect(p.seeking).toBe(true);
    expect(p.pendingForegroundAlign).toBe(false);
  });

  it("领先很小（< 0.5s）不干预（链式延续自然同步）", () => {
    const video = fakeVideo([[0, 30]], 10);
    const p = makePlayer(video, 10.1);
    p.pendingForegroundAlign = true;

    p.alignToVideoOnReturn();

    expect(video.currentTime).toBe(10); // 未 seek
    expect(p.aligningSeek).toBe(false);
  });

  it("目标不在缓冲内：退回音频重锚（不 seek，至少不静音）", () => {
    const video = fakeVideo([[0, 10]], 10);
    const p = makePlayer(video, 21); // heard = 21，MSE 缓冲只到 10
    const spy = vi.spyOn(p, "resyncFromBuffer");
    p.pendingForegroundAlign = true;

    p.alignToVideoOnReturn();

    expect(video.currentTime).toBe(10); // 未 seek
    expect(spy).toHaveBeenCalledWith(10);
  });

  it("无正在播的音频链：按当前视频位置重建", () => {
    const video = fakeVideo([[0, 30]], 12);
    const p = makePlayer(video); // 无链 → heardStreamTime() === null
    const spy = vi.spyOn(p, "resyncFromBuffer");
    p.pendingForegroundAlign = true;

    p.alignToVideoOnReturn();

    expect(video.currentTime).toBe(12);
    expect(spy).toHaveBeenCalledWith(12);
  });

  it("缓冲判定留 100ms 余量（避免 seek 到边界卡 waiting）", () => {
    const video = fakeVideo([[0, 20]]);
    const p = makePlayer(video);
    expect(p.isSeekableTarget(video, 19.8)).toBe(true);
    expect(p.isSeekableTarget(video, 19.95)).toBe(false);
    expect(p.isSeekableTarget(video, -1)).toBe(false);
  });
});

describe("PCMAudioPlayer 静音看门狗（C9b：画面在走、声音不回来）", () => {
  function stalledPlayer() {
    // 测试运行在 node 环境：补最小 DOM 替身（isPageHidden 读 document，hasPlayableVideoData
    // 读 HTMLMediaElement.HAVE_FUTURE_DATA）
    vi.stubGlobal("document", { visibilityState: "visible" });
    vi.stubGlobal("HTMLMediaElement", { HAVE_FUTURE_DATA: 3 });
    const video = fakeVideo([[0, 30]], 12);
    const p = makePlayer(video);
    p.clockState = "active";
    p.chainStartedAtMs = performance.now() - 10_000; // 曾成功排程过
    p.lastAudioProgressMs = performance.now() - 10_000; // 10s 无内容推进
    p.lastClockChangeAtMs = performance.now(); // 视频时钟仍在推进
    return { p, video };
  }

  it("曾有内容、随后长时间无可听内容且时钟推进：按播放头强制重建链", () => {
    const { p, video } = stalledPlayer();
    const spy = vi.spyOn(p, "resyncFromBuffer");

    p.audioStallWatchdog();

    expect(p.stallTrips).toBe(1);
    expect(spy).toHaveBeenCalledWith(video.currentTime);
  });

  it("有可听内容时不触发（正常播放不得被误判成停摆）", () => {
    const { p } = stalledPlayer();
    p.pending = [{ samples: new Float32Array(2), channels: 1, sampleRate: 48000, startSec: 1, endSec: 1.5 }];
    const spy = vi.spyOn(p, "resyncFromBuffer");

    p.audioStallWatchdog();

    expect(p.stallTrips).toBe(0);
    expect(spy).not.toHaveBeenCalled();
  });

  it("视频时钟停滞（未推进）时不触发", () => {
    const { p } = stalledPlayer();
    p.lastClockChangeAtMs = performance.now() - 100_000; // 时钟早已不动
    const spy = vi.spyOn(p, "resyncFromBuffer");

    p.audioStallWatchdog();

    expect(p.stallTrips).toBe(0);
    expect(spy).not.toHaveBeenCalled();
  });

  it("从未成功排程过（起播阶段）不介入", () => {
    const { p } = stalledPlayer();
    p.chainStartedAtMs = null;
    const spy = vi.spyOn(p, "resyncFromBuffer");

    p.audioStallWatchdog();

    expect(p.stallTrips).toBe(0);
    expect(spy).not.toHaveBeenCalled();
  });
});
