import { describe, it, expect } from "vitest";
import { PlaybackController } from "./playback-controller";

/** 最小 video 替身：只需 currentTime / buffered / seeking / playbackRate / 事件登记。 */
function fakeVideo(ranges: [number, number][], currentTime: number): HTMLVideoElement {
  return {
    currentTime,
    seeking: false,
    playbackRate: 1,
    readyState: 4,
    buffered: {
      length: ranges.length,
      start: (i: number) => ranges[i][0],
      end: (i: number) => ranges[i][1],
    },
    addEventListener: () => {},
    removeEventListener: () => {},
  } as unknown as HTMLVideoElement;
}

describe("PlaybackController 缓冲缺口跳过（gap skip）", () => {
  it("播放头接近空洞前且空洞后已有数据 → 前跳跳过缺失段（对齐原生 HLS gapController）", () => {
    // 上游整点缺失 ~30s：缓冲区间 0~10 与 40~50，播放头 9.9（已接近空洞前）
    const video = fakeVideo(
      [
        [0, 10],
        [40, 50],
      ],
      9.9,
    );
    const skipped: number[] = [];
    const ctrl = new PlaybackController(video, { onGapSkip: (g) => skipped.push(g) });

    ctrl.tick();

    expect(video.currentTime).toBeCloseTo(40.02, 3);
    expect(skipped).toHaveLength(1);
    expect(skipped[0]).toBeCloseTo(30, 3);
  });

  it("距空洞末端还远 → 不跳（正常播放不被打断）", () => {
    const video = fakeVideo(
      [
        [0, 10],
        [40, 50],
      ],
      5,
    );
    const ctrl = new PlaybackController(video, {});
    ctrl.tick();
    expect(video.currentTime).toBe(5);
  });

  it("空洞过小（正常缓冲空隙 < 0.5s）→ 不跳", () => {
    const video = fakeVideo(
      [
        [0, 10],
        [10.2, 20],
      ],
      9.9,
    );
    const ctrl = new PlaybackController(video, {});
    ctrl.tick();
    expect(video.currentTime).toBe(9.9);
  });

  it("空洞后数据太少 → 不跳（跳过去也会立刻 stall）", () => {
    const video = fakeVideo(
      [
        [0, 10],
        [40, 40.1],
      ],
      9.9,
    );
    const ctrl = new PlaybackController(video, {});
    ctrl.tick();
    expect(video.currentTime).toBe(9.9);
  });

  it("只有一个缓冲区间（无空洞）→ 不动", () => {
    const video = fakeVideo([[0, 50]], 9.9);
    const ctrl = new PlaybackController(video, {});
    ctrl.tick();
    expect(video.currentTime).toBe(9.9);
  });
});
