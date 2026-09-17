import { describe, it, expect } from "vitest";
import { Fmp4Remuxer, type InitSegmentPayload, type MediaSegmentPayload } from "./fmp4-remuxer";

const VIDEO = {
  id: 1,
  kind: "video" as const,
  codec: "avc1.64001e",
  timescale: 90000,
  width: 1280,
  height: 720,
  codecPrivate: new Uint8Array([0x01, 0x64, 0x00, 0x1e, 0xff]), // 伪 avcC
};
const AUDIO = {
  id: 2,
  kind: "audio" as const,
  codec: "mp4a.40.5",
  timescale: 48000,
  channels: 2,
  sampleRate: 48000,
  codecPrivate: new Uint8Array([0x00, 0x00, 0x00, 0x1d, 0x65, 0x73, 0x64, 0x73]), // 伪 esds
};

function makeRemuxer(opts?: { startupGraceMs?: number }) {
  const inits: InitSegmentPayload[] = [];
  const medias: MediaSegmentPayload[] = [];
  const remuxer = new Fmp4Remuxer(
    {
      onInitSegment: (s) => inits.push(s),
      onMediaSegment: (s) => medias.push(s),
    },
    { startupGraceMs: opts?.startupGraceMs },
  );
  return { remuxer, inits, medias };
}

function pushVideoSamples(remuxer: Fmp4Remuxer, count = 120) {
  for (let i = 0; i < count; i++) {
    remuxer.addSample({
      trackId: 1,
      data: new Uint8Array([0, 0, 0, 2, 0x65]),
      dts: i * 3600,
      cts: 0,
      // 周期 IDR：成段发生在「下一个关键帧」处（GOP 对齐），单一关键帧永远无法切段
      isKeyframe: i % 30 === 0,
    });
  }
}

describe("Fmp4Remuxer 起播门控", () => {
  it("视频 init 已发但音频轨未注册时，不提前发 media（避免引擎初始化后再 addSourceBuffer 抛错）", () => {
    const { remuxer, inits, medias } = makeRemuxer();
    remuxer.addTrack(VIDEO);

    // 视频 init 立即发射
    expect(inits.map((i) => i.kind)).toEqual(["video"]);

    // 攒够一批样本触发内部 flush，但音频轨尚未注册 → 门控应拦住 media
    pushVideoSamples(remuxer);
    expect(medias).toHaveLength(0);

    // 音频轨注册并发完 init 后，两轨 init 齐备 → 放行
    remuxer.addTrack(AUDIO);
    expect(inits.map((i) => i.kind)).toEqual(["video", "audio"]);
    remuxer.flush();
    expect(medias.length).toBeGreaterThan(0);
    expect(medias[0].kind).toBe("video");
  });

  it("纯视频流（音频永不到来）在宽限期到后放行，不会永久卡住", () => {
    const { remuxer, medias } = makeRemuxer({ startupGraceMs: 0 });
    remuxer.addTrack(VIDEO);
    pushVideoSamples(remuxer);
    expect(medias.length).toBeGreaterThan(0);
  });

  it("音频 init 的 container 为 audio/mp4（此前两轨都写 video/mp4）", () => {
    const { remuxer, inits } = makeRemuxer();
    remuxer.addTrack(VIDEO);
    remuxer.addTrack(AUDIO);
    const audio = inits.find((i) => i.kind === "audio")!;
    const video = inits.find((i) => i.kind === "video")!;
    expect(audio.container).toBe("audio/mp4");
    expect(video.container).toBe("video/mp4");
  });

  it("force flush 跳过门控（收尾不丢数据）", () => {
    const { remuxer, medias } = makeRemuxer();
    remuxer.addTrack(VIDEO);
    pushVideoSamples(remuxer, 5);
    expect(medias).toHaveLength(0); // 门控拦住
    remuxer.flush(true);
    expect(medias.length).toBeGreaterThan(0); // 强制放行
  });
});

describe("Fmp4Remuxer 静音 AAC 假音轨（C2：软解音轨给 MSE 挂占位音轨，防后台被 UA 冻结）", () => {
  it("假音轨 init 立即发射且 container 为 audio/mp4", () => {
    const { remuxer, inits } = makeRemuxer({ startupGraceMs: 0 });
    remuxer.addTrack(VIDEO);
    remuxer.setSilentAudioTrack({ id: 2, sampleRate: 48000, channels: 2 });

    const audioInit = inits.find((i) => i.kind === "audio");
    expect(audioInit).toBeDefined();
    expect(audioInit!.container).toBe("audio/mp4");
    expect(audioInit!.codec).toBe("mp4a.40.2");
  });

  it("视频段带出等长静音音频段：段窗对齐、残差累积不漂移", () => {
    const { remuxer, medias } = makeRemuxer({ startupGraceMs: 0 });
    remuxer.addTrack(VIDEO);
    remuxer.setSilentAudioTrack({ id: 2, sampleRate: 48000, channels: 2 });

    pushVideoSamples(remuxer, 90); // 90 帧 × 40ms = 3.6s；周期 IDR 触 GOP 对齐成段

    const videos = medias.filter((m) => m.kind === "video");
    const audios = medias.filter((m) => m.kind === "audio");
    expect(videos.length).toBeGreaterThan(1);
    expect(audios.length).toBeGreaterThan(1);

    // 首段：静音音频起点（ms）= 视频段起点（90kHz → 秒），长度与视频段相等（±50ms）
    const v0 = videos[0];
    const a0 = audios[0];
    expect(a0.startDts / 1000).toBeCloseTo(v0.startDts / 90000, 2);
    expect(Math.abs(a0.duration - v0.duration)).toBeLessThan(0.05);

    // 跨段续接：音频段起点严格递增（无重叠/回退）
    for (let i = 1; i < audios.length; i++) {
      expect(audios[i].startDts).toBeGreaterThan(audios[i - 1].startDts);
    }

    // 无累积漂移：末段静音音频的结束时刻与对应视频段的结束时刻相差 < 1 个 AAC 帧长（21.3ms）
    // （逐段起点续接 + 时长取整残差累积保证不随时间漂移；末帧允许最多一帧的过冲）
    const lastA = audios[audios.length - 1];
    const lastV = videos[videos.length - 1];
    const aEndMs = lastA.startDts + lastA.duration * 1000;
    const vEndMs = ((lastV.startDts + lastV.duration * 90000) / 90000) * 1000;
    expect(Math.abs(aEndMs - vEndMs)).toBeLessThan(25);
  });
});
