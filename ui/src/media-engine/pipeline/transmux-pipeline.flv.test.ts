/**
 * 端到端：HTTP-FLV 直链走 TransmuxPipeline（校验"探测 → FLV 解复用 → fMP4 成段"整条接线）。
 * fetch 被打桩返回真实 FLV 素材（testdata/test-h264.flv），不依赖 MSE。
 */
import { readFileSync } from "node:fs";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { InitSegmentPayload, MediaSegmentPayload } from "../remux/fmp4-remuxer";
import { TransmuxPipeline, type PipelineConfig } from "./transmux-pipeline";

function fixtureBytes(name: string): Uint8Array {
  return new Uint8Array(readFileSync(new URL(`../demux/testdata/${name}`, import.meta.url)));
}

beforeEach(() => {
  vi.unstubAllGlobals();
});

function configFor(url: string): PipelineConfig {
  return {
    urls: [url],
    dataSource: {},
    bufferThreshold: 1 << 20,
    targetDuration: 0.5,
    maxBytes: 1 << 20,
    resumeMode: "restart",
  } as unknown as PipelineConfig;
}

describe("TransmuxPipeline（HTTP-FLV）", () => {
  it("首块探测 FLV → 产出 h264 视频 init/媒体段与 AAC 音频段", async () => {
    const flv = fixtureBytes("test-h264.flv");
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(new Uint8Array(flv), {
            status: 200,
            headers: { "content-type": "video/x-flv" },
          }),
      ),
    );

    const inits: InitSegmentPayload[] = [];
    const medias: MediaSegmentPayload[] = [];
    const layouts: { video: boolean; mseAudio: boolean }[] = [];
    const pipeline = new TransmuxPipeline(configFor("https://test.local/live.flv"), {
      onInitSegment: (seg) => inits.push(seg),
      onMediaSegment: (seg) => medias.push(seg),
      onStreamLayout: (layout) => layouts.push(layout),
    });

    await Promise.race([pipeline.start(), new Promise((r) => setTimeout(r, 10_000))]);
    pipeline.destroy();

    const videoInit = inits.find((s) => s.kind === "video");
    const audioInit = inits.find((s) => s.kind === "audio");
    expect(videoInit).toBeDefined();
    expect(String(videoInit?.codec)).toMatch(/^avc1\./);
    expect(audioInit).toBeDefined();
    expect(String(audioInit?.codec)).toMatch(/^mp4a\.40\./);

    expect(medias.some((s) => s.kind === "video")).toBe(true);
    expect(layouts.length).toBeGreaterThan(0);
    expect(layouts.at(-1)?.video).toBe(true);
  });
});
