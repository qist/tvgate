/**
 * 直播重连的时间轴重锚：上游断开重连会带来**全新 PTS 纪元**（新连接从自己的 0 开始）。
 * 若不重锚，新内容按旧基准归一化会落回已播区间 → 浏览器不会把播放头往回走，表现为
 * "重连了但画面不动 / 播放几帧就卡"（实测斗鱼 CDN 每条连接只送 ~400KB 就断，靠重连续播）。
 */
import { describe, expect, it } from "vitest";
import { buildEsds } from "../formats/aac";
import { Fmp4Remuxer, type RemuxTrackConfig } from "./fmp4-remuxer";

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
    codecPrivate: new Uint8Array([0, 0, 0, 8, 0x61, 0x76, 0x63, 0x43]),
  };
}

describe("Fmp4Remuxer 直播重连重锚", () => {
  it("新纪元的样本接到 reanchorTo 指定的起点，而不是回到 0", () => {
    const videoSegs: Array<{ startSec: number; off: number }> = [];
    const remuxer = new Fmp4Remuxer(
      {
        onMediaSegment: (s) => {
          if (s.kind === "video") {
            videoSegs.push({ startSec: s.startDts / 90000, off: s.timestampOffset });
          }
        },
      },
      {},
    );
    remuxer.setVideoExpected(true);
    remuxer.addTrack(videoTrack(1));
    remuxer.addTrack(audioTrack(2));

    const push = (dts: number, key = true) => {
      remuxer.addSample({ trackId: 1, data: new Uint8Array(8), dts, cts: 0, isKeyframe: key });
      remuxer.addSample({ trackId: 2, data: new Uint8Array(4), dts, cts: 0, isKeyframe: true });
    };

    // 第一纪元：25fps（3000 ticks/帧）
    for (let i = 0; i < 8; i += 1) push(i * 3000);
    remuxer.flush(true);
    expect(videoSegs.length).toBeGreaterThan(0);

    // 重连：重锚到 10s（模拟播放头 / 已发内容末端）
    remuxer.reanchorTo(10);
    const before = videoSegs.length;

    // 第二纪元：全新 PTS 起点（模拟新连接）
    for (let i = 0; i < 8; i += 1) push(5_000_000 + i * 3000);
    remuxer.flush(true);

    const epoch2 = videoSegs.slice(before);
    expect(epoch2.length).toBeGreaterThan(0);
    const firstAt = epoch2[0].startSec + epoch2[0].off;
    expect(firstAt).toBeGreaterThan(9.9);
    expect(firstAt).toBeLessThan(10.4);
    // 后续段按同一基准连续推进（不跳回 0）
    const secondAt = epoch2[1] ? epoch2[1].startSec + epoch2[1].off : firstAt;
    expect(secondAt).toBeGreaterThan(firstAt);
  });
});
