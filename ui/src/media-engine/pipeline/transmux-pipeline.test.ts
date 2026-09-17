import { describe, it, expect, vi } from "vitest";
import { TransmuxPipeline, isRetryableIoError } from "./transmux-pipeline";
import type { SegmentSource } from "../hls/segment-source";

const VIDEO_TRACK = {
  id: 1,
  kind: "video" as const,
  codec: "avc1.64001e",
  timescale: 90000,
  width: 1920,
  height: 1080,
  codecPrivate: new Uint8Array([0x01, 0x64]),
};
const AUDIO_TRACK = {
  id: 2,
  kind: "audio" as const,
  codec: "mp4a.40.5",
  timescale: 48000,
  channels: 2,
  sampleRate: 48000,
  codecPrivate: new Uint8Array([0x00, 0x00]),
};

// biome-ignore lint/suspicious/noExplicitAny: 测试用弱类型
type MediaInfo = any;

describe("TransmuxPipeline 媒体信息聚合", () => {
  it("音视频分两批发布时，后一批不得覆盖前一批（音频编码/声道徽章不能消失）", () => {
    const infos: MediaInfo[] = [];
    const pipeline = new TransmuxPipeline({ urls: [] }, { onMediaInfo: (info) => infos.push(info) });
    // demuxer 分两批发布：视频先就绪，音频须等首帧 ADTS 构造 esds 后才发布
    // biome-ignore lint/suspicious/noExplicitAny: 私有方法仅测试内调用
    const handleTracks = (pipeline as any).handleTracks.bind(pipeline);

    handleTracks([VIDEO_TRACK]);
    const first = infos[infos.length - 1];
    expect(first.video?.codec).toBe("avc1.64001e");
    expect(first.audio).toBeUndefined(); // 音频尚未就绪

    handleTracks([AUDIO_TRACK]);
    const last = infos[infos.length - 1];
    // 关键：视频信息必须仍在，且音频信息已并入
    expect(last.video?.codec).toBe("avc1.64001e");
    expect(last.video?.height).toBe(1080);
    expect(last.audio?.codec).toBe("mp4a.40.5");
    expect(last.audio?.channelCount).toBe(2);
  });

  it("反向顺序（音频先到）同样两轨齐全", () => {
    const infos: MediaInfo[] = [];
    const pipeline = new TransmuxPipeline({ urls: [] }, { onMediaInfo: (info) => infos.push(info) });
    // biome-ignore lint/suspicious/noExplicitAny: 私有方法仅测试内调用
    const handleTracks = (pipeline as any).handleTracks.bind(pipeline);

    handleTracks([AUDIO_TRACK]);
    handleTracks([VIDEO_TRACK]);
    const last = infos[infos.length - 1];
    expect(last.audio?.codec).toBe("mp4a.40.5");
    expect(last.video?.codec).toBe("avc1.64001e");
  });

  it("内容未变化时不重复发布", () => {
    const infos: MediaInfo[] = [];
    const pipeline = new TransmuxPipeline({ urls: [] }, { onMediaInfo: (info) => infos.push(info) });
    // biome-ignore lint/suspicious/noExplicitAny: 私有方法仅测试内调用
    const handleTracks = (pipeline as any).handleTracks.bind(pipeline);

    handleTracks([VIDEO_TRACK]);
    const count = infos.length;
    handleTracks([VIDEO_TRACK]); // 完全相同的一批
    expect(infos.length).toBe(count); // 去重，不重复刷新
  });

  it("scanType 透传到媒体信息（隔行 → 徽标显示 1080i）", () => {
    const infos: MediaInfo[] = [];
    const pipeline = new TransmuxPipeline({ urls: [] }, { onMediaInfo: (info) => infos.push(info) });
    // biome-ignore lint/suspicious/noExplicitAny: 私有方法仅测试内调用
    const handleTracks = (pipeline as any).handleTracks.bind(pipeline);

    handleTracks([{ ...VIDEO_TRACK, scanType: "interlaced" as const }]);
    expect(infos[infos.length - 1].video?.scanType).toBe("interlaced");

    // 后续批次不带 scanType 时不得抹掉已知值（mergeDefined 语义）
    handleTracks([{ ...VIDEO_TRACK, scanType: undefined }]);
    expect(infos[infos.length - 1].video?.scanType).toBe("interlaced");
  });

  it("视频帧率由样本 DTS 增量估计（25fps → 25 FPS）", () => {
    const infos: MediaInfo[] = [];
    const pipeline = new TransmuxPipeline({ urls: [] }, { onMediaInfo: (info) => infos.push(info) });
    // biome-ignore lint/suspicious/noExplicitAny: 私有方法仅测试内调用
    const p = pipeline as any;
    p.handleTracks([VIDEO_TRACK]);

    // 25fps：相邻 DTS 差恒为 90000/25 = 3600（解码序，与 B 帧无关）
    p.handleSamples(
      Array.from({ length: 20 }, (_, i) => ({
        trackId: 1,
        kind: "video" as const,
        data: new Uint8Array([0]),
        dts: Math.round((i * 90000) / 25),
        cts: 0,
        isKeyframe: i === 0,
      })),
    );

    expect(infos[infos.length - 1].video?.frameRate).toBe(25);
  });

  it("软解音轨首帧确定声道数后重新发布，媒体信息补上 5.1（徽标不再缺声道）", () => {
    const infos: MediaInfo[] = [];
    const pipeline = new TransmuxPipeline({ urls: [] }, { onMediaInfo: (info) => infos.push(info) });
    // biome-ignore lint/suspicious/noExplicitAny: 私有方法仅测试内调用
    const handleTracks = (pipeline as any).handleTracks.bind(pipeline);

    // PMT 阶段先发布（PES/PMT 里没有声道数）→ 只有编码，没有声道
    handleTracks([VIDEO_TRACK, { ...AUDIO_TRACK, codec: "ac3", channels: undefined, sampleRate: undefined }]);
    expect(infos[infos.length - 1].audio?.codec).toBe("ac3");
    expect(infos[infos.length - 1].audio?.channelCount).toBeUndefined();

    // 首帧解析出 5.1（acmod=7 + LFE）后重发布 → 声道数并入，且视频信息不被覆盖
    handleTracks([{ ...AUDIO_TRACK, codec: "ac3", channels: 6, sampleRate: 48000 }]);
    expect(infos[infos.length - 1].audio?.channelCount).toBe(6);
    expect(infos[infos.length - 1].video?.codec).toBe("avc1.64001e");
  });
});

describe("TransmuxPipeline 缓冲领先门（对齐参照实现 waitForBufferRoom）", () => {
  function makeLive(): TransmuxPipeline {
    return new TransmuxPipeline({ urls: [], sourceMode: "continuous-live-ts" });
  }
  // biome-ignore lint/suspicious/noExplicitAny: 私有方法仅测试内调用
  const gate = (p: TransmuxPipeline) => (p as any).waitForBufferLead() as Promise<boolean>;

  it("点播/静态列表不受门限制（领先 600s 也放行）", async () => {
    const p = new TransmuxPipeline({ urls: [], sourceMode: "static-ts-list" });
    p.setClock(0, 600_000, false);
    await expect(gate(p)).resolves.toBe(true);
  });

  it("播放头未知（主线程未上报）时放行，绝不死锁", async () => {
    const p = makeLive();
    await expect(gate(p)).resolves.toBe(true);
  });

  it("领先恰为 30s（阈值内）放行", async () => {
    const p = makeLive();
    p.setClock(0, 30_000, false);
    await expect(gate(p)).resolves.toBe(true);
  });

  it("领先超过 30s 时保持等待，直到播放头追上", async () => {
    const p = makeLive();
    p.setClock(0, 40_000, false);
    const waiting = gate(p);
    let settled = false;
    void waiting.then(() => {
      settled = true;
    });
    await new Promise((r) => setTimeout(r, 30));
    expect(settled).toBe(false); // 门闭合：拉流/解码被限速
    p.setClock(20_000, 40_000, false); // 播放头追上来（领先 20s）
    await expect(waiting).resolves.toBe(true);
  });

  it("页面隐藏时无条件放行（后台音频 free-run）", async () => {
    const p = makeLive();
    p.setClock(0, 120_000, true);
    await expect(gate(p)).resolves.toBe(true);
  });

  it("门闭合等待中切后台 → 立即放行", async () => {
    const p = makeLive();
    p.setClock(0, 60_000, false);
    const waiting = gate(p);
    await new Promise((r) => setTimeout(r, 30));
    p.setClock(0, 60_000, true); // 切后台
    await expect(waiting).resolves.toBe(true);
  });
});

describe("TransmuxPipeline 软解 PCM 脱轴重钉（C9c：直播重连/断流恢复后重钉 PCM 轴）", () => {
  // biome-ignore lint/suspicious/noExplicitAny: 私有成员仅测试内调用
  function makeReanchorPipeline(): any {
    return new TransmuxPipeline({ urls: [], sourceMode: "continuous-live-ts" });
  }

  function silenceWarn(): void {
    vi.spyOn(console, "warn").mockImplementation(() => {});
  }

  it("偏离在界内（−3s ~ +36s）：保留 PCM 时间轴", () => {
    silenceWarn();
    const p = makeReanchorPipeline();
    p.playheadCurrentMs = 10_000;
    p.pcmTimeBaseSec = 5;
    p.lastSoftAudioTimeSec = 20; // 领先 10s → 界内

    p.reanchorPcmIfNeeded();

    expect(p.pcmTimeBaseSec).toBe(5);
  });

  it("领先超出上限（>36s）：重钉（清基准与暂存，下一帧按新基准锚定）", () => {
    silenceWarn();
    const p = makeReanchorPipeline();
    p.playheadCurrentMs = 10_000;
    p.pcmTimeBaseSec = 5;
    p.lastSoftAudioTimeSec = 60; // 领先 50s

    p.reanchorPcmIfNeeded();

    expect(p.pcmTimeBaseSec).toBeNull();
    expect(p.lastSoftAudioTimeSec).toBeNull();
  });

  it("落后超出下限（>3s）：重钉（否则会被当过期丢成静音）", () => {
    silenceWarn();
    const p = makeReanchorPipeline();
    p.playheadCurrentMs = 10_000;
    p.pcmTimeBaseSec = 5;
    p.lastSoftAudioTimeSec = 5; // 落后 5s

    p.reanchorPcmIfNeeded();

    expect(p.pcmTimeBaseSec).toBeNull();
  });

  it("播放头未知（首帧前）：保守重钉", () => {
    silenceWarn();
    const p = makeReanchorPipeline();
    p.pcmTimeBaseSec = 5;
    p.lastSoftAudioTimeSec = 5;

    p.reanchorPcmIfNeeded();

    expect(p.pcmTimeBaseSec).toBeNull();
  });
});

describe("直播重试判定（C9a）", () => {
  it("网络异常/5xx/429 可重试；4xx（除 429）为确定性失败不重试", () => {
    expect(isRetryableIoError(-1)).toBe(true); // 网络异常
    expect(isRetryableIoError(500)).toBe(true);
    expect(isRetryableIoError(503)).toBe(true);
    expect(isRetryableIoError(429)).toBe(true); // 限流：可重试
    expect(isRetryableIoError(404)).toBe(false);
    expect(isRetryableIoError(403)).toBe(false);
  });
});

// ===== 分段源分片失效恢复（整点切换）=====
// mock FetchStreamLoader（不依赖真实 fetch/Response）：URL 含 "old-" → 404；否则一小段 TS 后完成。
const harness = vi.hoisted(() => {
  const requests: string[] = [];
  class HarnessLoader {
    isCompleted = false;
    dataStalled = false;
    private readonly url: string;
    private readonly cbs: {
      onError?: (info: { code: number; msg: string; url: string }) => void;
      onDataArrival?: (chunk: Uint8Array, byteStart: number, received: number) => void;
      onComplete?: (from: number, to: number) => void;
    };
    // biome-ignore lint/suspicious/noExplicitAny: 测试用弱类型
    constructor(source: { url: string }, callbacks: any) {
      this.url = source.url;
      this.cbs = callbacks;
    }
    async open(): Promise<void> {
      requests.push(this.url);
      if (this.url.includes("old-")) {
        // 整点切换：旧序列分片已被上游删除（404）
        this.cbs.onError?.({ code: 404, msg: "HTTP 404", url: this.url });
        return;
      }
      this.cbs.onDataArrival?.(new Uint8Array([0x47, 0x40, 0x00, 0x10]), 0, 4);
      this.isCompleted = true;
      this.cbs.onComplete?.(0, 3);
    }
    getResumeRange(): undefined {
      return undefined;
    }
    abort(): void {
      /* noop */
    }
  }
  return { requests, HarnessLoader };
});

vi.mock("../io/fetch-loader", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../io/fetch-loader")>();
  return {
    ...actual,
    FetchStreamLoader: harness.HarnessLoader as unknown as typeof actual.FetchStreamLoader,
  };
});

describe("分段源分片失效恢复（整点切换）", () => {
  it("分片 404：丢弃整批旧分段并刷新播放列表续播，不逐个啃旧分片、不上报播放错误", async () => {
    harness.requests.length = 0;
    const errors: unknown[] = [];

    let pipelineRef: TransmuxPipeline | null = null;
    class FakeHlsSource implements SegmentSource {
      readonly live = true;
      invalidations = 0;
      private readonly oldQueue = ["https://x/old-1.ts", "https://x/old-2.ts"];
      private readonly newQueue = ["https://x/new-1.ts"];

      async next(): Promise<string | null> {
        if (this.oldQueue.length > 0) return this.oldQueue.shift()!;
        if (this.newQueue.length > 0) return this.newQueue.shift()!;
        pipelineRef?.destroy(); // 新序列已消费：结束测试中的无限直播循环
        return null;
      }
      /** 分片批次失效：丢弃旧序列、改用新播放列表序列（模拟上游整点切换后 m3u8 前进）。 */
      invalidatePending(): void {
        this.invalidations++;
        this.oldQueue.length = 0;
      }
      destroy(): void {
        /* noop */
      }
    }

    const source = new FakeHlsSource();
    const pipeline = new TransmuxPipeline(
      { urls: [], source },
      { onIOError: (info) => errors.push(info), onDemuxError: () => {} },
    );
    pipelineRef = pipeline;

    await pipeline.start();

    expect(source.invalidations).toBe(1); // 发生一次批次失效（丢弃旧序列）
    expect(harness.requests.some((u) => u.includes("new-1.ts"))).toBe(true); // 改用新序列续播
    expect(harness.requests.some((u) => u.includes("old-2.ts"))).toBe(false); // 旧批次其余分片不再被请求
    expect(errors).toEqual([]); // 分片失败不上报（不把整点切换当成播放错误）
  });
});
