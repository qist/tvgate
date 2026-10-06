import { describe, expect, it } from "vitest";
import {
  MatroskaDemuxer,
  matroskaProbe,
  parseMatroskaCues,
  type MatroskaDemuxerCallbacks,
} from "./matroska-demuxer";
import type { DemuxedSample, TrackInfo } from "./ts-demuxer";

// ==================== EBML 合成工具 ====================

/** 元素 ID（保留标记位；按规范取最短编码）。 */
function eid(id: number): Uint8Array {
  if (id <= 0xff) return new Uint8Array([id]);
  if (id <= 0xffff) return new Uint8Array([id >> 8, id & 0xff]);
  if (id <= 0xffffff) return new Uint8Array([(id >> 16) & 0xff, (id >> 8) & 0xff, id & 0xff]);
  return new Uint8Array([(id >>> 24) & 0xff, (id >> 16) & 0xff, (id >> 8) & 0xff, id & 0xff]);
}

/** 尺寸 VINT（剥标记位，最短编码）。 */
function esize(n: number): Uint8Array {
  if (n < 0x7f) return new Uint8Array([0x80 | n]);
  if (n < 0x3fff) return new Uint8Array([0x40 | (n >> 8), n & 0xff]);
  return new Uint8Array([0x20 | ((n >> 16) & 0xff), (n >> 8) & 0xff, n & 0xff]);
}

/** unknown-size 标记（全 1）。 */
function esizeUnknown(byteLen = 1): Uint8Array {
  return new Uint8Array(byteLen).fill(0xff);
}

function el(id: number, payload: Uint8Array, unknownSize = false): Uint8Array {
  const head = concat([eid(id), unknownSize ? esizeUnknown() : esize(payload.length)]);
  return concat([head, payload]);
}

function concat(parts: Uint8Array[]): Uint8Array {
  let len = 0;
  for (const p of parts) len += p.length;
  const out = new Uint8Array(len);
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return out;
}

function u(v: number, byteLen = 0): Uint8Array {
  const bytes: number[] = [];
  let x = v;
  do {
    bytes.unshift(x & 0xff);
    x = Math.floor(x / 256);
  } while (x > 0);
  while (byteLen > bytes.length) bytes.unshift(0);
  return new Uint8Array(bytes);
}

function f64(v: number): Uint8Array {
  const b = new DataView(new ArrayBuffer(8));
  b.setFloat64(0, v);
  return new Uint8Array(b.buffer);
}

/** 块轨号 VINT（保留标记位；轨 1 → 0x81）。 */
function trackVint(n: number): Uint8Array {
  return new Uint8Array([0x80 | n]);
}

/** 最小 hvcC：configurationVersion=1 + profile_idc + level_idc + lengthSizeMinusOne=3。 */
function makeHvcC(profileIdc = 1, levelIdc = 120): Uint8Array {
  const cp = new Uint8Array(23);
  cp[0] = 1; // configurationVersion
  cp[1] = 0x80 | profileIdc; // profile_space=0 + tier + profile_idc
  cp[12] = levelIdc; // general_level_idc
  cp[21] = 0x03; // constantFrameRate/numTempLayers/temporalIdNested + lengthSizeMinusOne=3
  cp[22] = 0; // numOfArrays
  return cp;
}

/** AAC-LC 48kHz 2ch AudioSpecificConfig（aot=2, freqIdx=3, chCfg=2）。 */
function makeAsc(): Uint8Array {
  return new Uint8Array([0x11, 0x90]);
}

function ebmlHeader(): Uint8Array {
  return el(0x1a45dfa3, concat([el(0x4282, new Uint8Array([0x6d, 0x61, 0x74, 0x6f, 0x73, 0x6b, 0x61]))]));
}

/** Info：Duration 为缩放后时间码单位（1ms scale 下 62500 = 62.5s）。 */
function makeInfo(scale = 1e6, durationRaw = 62500): Uint8Array {
  return el(
    0x1549a966,
    concat([el(0x2ad7b1, u(scale)), el(0x4489, f64(durationRaw))]),
  );
}

/** 视频轨（V_MPEGH/ISO/HEVC + hvcC）与音频轨（A_EAC3）的 Tracks 元素。 */
function makeTracks(): Uint8Array {
  const video = el(
    0xae,
    concat([
      el(0xd7, u(1)),
      el(0x83, u(1)),
      el(0x86, new Uint8Array([0x56, 0x5f, 0x4d, 0x50, 0x45, 0x47, 0x48, 0x2f, 0x49, 0x53, 0x4f, 0x2f, 0x48, 0x45, 0x56, 0x43])), // "V_MPEGH/ISO/HEVC"
      el(0x63a2, makeHvcC()),
      el(0x23e383, u(1e9 / 30)), // DefaultDuration: 30fps
      el(0xe0, concat([el(0xb0, u(3840)), el(0xba, u(2160))])),
    ]),
  );
  const audio = el(
    0xae,
    concat([
      el(0xd7, u(2)),
      el(0x83, u(2)),
      el(0x86, new Uint8Array([0x41, 0x5f, 0x45, 0x41, 0x43, 0x33])), // "A_EAC3"
      el(0xe1, concat([el(0xb5, f64(48000)), el(0x9f, u(6))])),
    ]),
  );
  return el(0x1654ae6b, concat([video, audio]));
}

/** SimpleBlock：track + relTs(ms) + flags + data。 */
function simpleBlock(track: number, relTs: number, keyframe: boolean, data: Uint8Array): Uint8Array {
  const flags = keyframe ? 0x80 : 0x00;
  return el(
    0xa3,
    concat([trackVint(track), new Uint8Array([(relTs >> 8) & 0xff, relTs & 0xff, flags]), data]),
  );
}

function cluster(timecodeMs: number, blocks: Uint8Array[], unknownSize = false): Uint8Array {
  return el(0x1f43b675, concat([el(0xe7, u(timecodeMs)), ...blocks]), unknownSize);
}

interface Captured {
  tracks: TrackInfo[][];
  samples: DemuxedSample[];
  durations: number[];
  layouts: { video: boolean; mseAudio: boolean; softAudio?: boolean }[];
  errors: string[];
}

function capture(): Captured & { callbacks: MatroskaDemuxerCallbacks } {
  const c: Captured = { tracks: [], samples: [], durations: [], layouts: [], errors: [] };
  return {
    ...c,
    callbacks: {
      onTracks: (t) => c.tracks.push(t),
      onSamples: (s) => c.samples.push(...s),
      onDuration: (sec) => c.durations.push(sec),
      onStreamLayout: (l) => c.layouts.push(l),
      onError: (m) => c.errors.push(m),
    },
  };
}

// ==================== 探测 ====================

describe("matroskaProbe", () => {
  it("识别 EBML 魔数与不足 4 字节续等", () => {
    expect(matroskaProbe(new Uint8Array([0x1a, 0x45, 0xdf, 0xa3, 0x01]))).toEqual({ match: true, needMoreData: false });
    expect(matroskaProbe(new Uint8Array([0x1a, 0x45]))).toEqual({ match: false, needMoreData: true });
    expect(matroskaProbe(new Uint8Array([0x1a, 0x45, 0xdf, 0xa2]))).toEqual({ match: false, needMoreData: false });
    expect(matroskaProbe(new Uint8Array([0x47]))).toEqual({ match: false, needMoreData: false }); // TS 同步字节
  });
});

// ==================== 主流程 ====================

describe("MatroskaDemuxer", () => {
  it("解析 Info/Tracks/Cluster：轨道、时长、布局、样本", () => {
    const { callbacks, tracks, samples, durations, layouts, errors } = capture();
    const d = new MatroskaDemuxer(callbacks);
    const v1 = new Uint8Array(64).fill(0xaa);
    const v2 = new Uint8Array(32).fill(0xbb);
    const a1 = new Uint8Array(1536).fill(0xcc);
    d.push(
      concat([
        ebmlHeader(),
        el(0x18538067, new Uint8Array(0), true), // Segment unknown size
        makeInfo(1e6, 62500),
        makeTracks(),
        cluster(1000, [
          simpleBlock(1, 0, true, v1), // 视频帧 pts=1000ms
          simpleBlock(1, 33, false, v2), // pts=1033ms
          simpleBlock(2, 0, false, a1),
        ]),
      ]),
    );
    expect(errors).toEqual([]);
    // 轨道：hvc1（完整 codec 串）+ eac3
    expect(tracks.length).toBe(1);
    const infos = tracks[0];
    expect(infos.map((t) => t.codec)).toEqual(["hvc1.1.1.L120.B0", "eac3"]);
    expect(infos[0].width).toBe(3840);
    expect(infos[0].height).toBe(2160);
    expect(infos[0].codecPrivate?.length).toBe(31); // 裸 hvcC 记录(23) + box 头(8)
    expect(infos[1].channels).toBe(6);
    expect(infos[1].sampleRate).toBe(48000);
    // 布局：视频 + 软解音频
    expect(layouts).toEqual([{ video: true, mseAudio: false, softAudio: true }]);
    // 时长
    expect(durations).toEqual([62.5]);
    // 样本：视频 DTS 合成（30fps → 3000 ticks 步长），音频 dts=pts
    const vs = samples.filter((s) => s.kind === "video");
    expect(vs.length).toBe(2);
    expect(vs[0].dts).toBe(0);
    expect(vs[0].pts).toBe(1000 * 90);
    expect(vs[0].cts).toBe(1000 * 90 - 0);
    expect(vs[0].isKeyframe).toBe(true);
    expect(vs[1].dts).toBe(3000);
    expect(vs[1].pts).toBe(1033 * 90);
    expect(vs[1].isKeyframe).toBe(false);
    expect(vs[0].data).toEqual(v1); // hvcC 轨样本原样透传（长度前缀 NAL）
    const as = samples.filter((s) => s.kind === "audio");
    expect(as.length).toBe(1);
    expect(as[0].dts).toBe(1000 * 90);
    expect(as[0].pts).toBe(as[0].dts);
    expect(as[0].cts).toBe(0);
  });

  it("B 帧：pts 非单调而 dts 单调，cts 承载重排", () => {
    const { callbacks, samples } = capture();
    const d = new MatroskaDemuxer(callbacks);
    const mk = (rel: number) => simpleBlock(1, rel, rel === 33, new Uint8Array(16));
    d.push(
      concat([
        ebmlHeader(),
        el(0x18538067, new Uint8Array(0), true),
        makeInfo(),
        makeTracks(),
        cluster(1000, [mk(33), mk(0), mk(16)]), // 解码序 P B B → pts 1033/1000/1016
      ]),
    );
    const vs = samples.filter((s) => s.kind === "video");
    expect(vs.map((s) => s.pts)).toEqual([1033 * 90, 1000 * 90, 1016 * 90]);
    expect(vs.map((s) => s.dts)).toEqual([0, 3000, 6000]);
    expect(vs.every((s) => s.dts > 0 || s === vs[0])).toBe(true);
  });

  it("分片喂入（跨 push 的元素续读）与 unknown-size Cluster 兄弟退出", () => {
    const { callbacks, tracks, samples, errors } = capture();
    const d = new MatroskaDemuxer(callbacks);
    const stream = concat([
      ebmlHeader(),
      el(0x18538067, new Uint8Array(0), true),
      makeInfo(1e6, 10),
      makeTracks(),
      cluster(0, [simpleBlock(1, 0, true, new Uint8Array(16).fill(1))]),
      cluster(1000, [simpleBlock(1, 0, false, new Uint8Array(16).fill(2))]), // 下一 Cluster（兄弟）
    ]);
    // 按小分片喂入
    for (let i = 0; i < stream.length; i += 7) {
      d.push(stream.subarray(i, Math.min(i + 7, stream.length)));
    }
    expect(errors).toEqual([]);
    expect(tracks.length).toBe(1);
    const vs = samples.filter((s) => s.kind === "video");
    expect(vs.length).toBe(2);
    expect(vs[1].pts).toBe(1000 * 90);
  });

  it("BlockGroup：ReferenceBlock 缺席 = 关键帧", () => {
    const { callbacks, samples } = capture();
    const d = new MatroskaDemuxer(callbacks);
    const block = (track: number, rel: number) =>
      el(0xa1, concat([trackVint(track), new Uint8Array([(rel >> 8) & 0xff, rel & 0xff, 0x00]), new Uint8Array(8)]));
    const groupWithRef = el(0xa0, concat([block(1, 16), el(0xfb, u(-1, 8))])); // 引用 P 帧
    const groupNoRef = el(0xa0, block(1, 0)); // 无引用 = 关键帧
    d.push(
      concat([
        ebmlHeader(),
        el(0x18538067, new Uint8Array(0), true),
        makeInfo(),
        makeTracks(),
        cluster(0, [groupNoRef, groupWithRef]),
      ]),
    );
    const vs = samples.filter((s) => s.kind === "video");
    expect(vs.length).toBe(2);
    expect(vs[0].isKeyframe).toBe(true);
    expect(vs[1].isKeyframe).toBe(false);
  });

  it("AAC：ASC → esds 与 mp4a.40.2；不支持编码（A_OPUS）整轨剔除", () => {
    const { callbacks, tracks } = capture();
    const d = new MatroskaDemuxer(callbacks);
    const aac = el(
      0xae,
      concat([
        el(0xd7, u(3)),
        el(0x83, u(2)),
        el(0x86, new Uint8Array([0x41, 0x5f, 0x41, 0x41, 0x43])), // "A_AAC"
        el(0x63a2, makeAsc()),
      ]),
    );
    const opus = el(
      0xae,
      concat([
        el(0xd7, u(4)),
        el(0x83, u(2)),
        el(0x86, new Uint8Array([0x41, 0x5f, 0x4f, 0x50, 0x55, 0x53])), // "A_OPUS"
      ]),
    );
    d.push(
      concat([
        ebmlHeader(),
        el(0x18538067, new Uint8Array(0), true),
        makeInfo(),
        el(0x1654ae6b, concat([aac, opus])),
      ]),
    );
    expect(tracks.length).toBe(1);
    const aacInfo = tracks[0].find((t) => t.codec.startsWith("mp4a"));
    expect(aacInfo).toBeDefined();
    expect(aacInfo!.codec).toBe("mp4a.40.2");
    expect(aacInfo!.timescale).toBe(48000);
    expect(aacInfo!.codecPrivate!.length).toBeGreaterThan(0);
    expect(tracks[0].some((t) => t.id === 4)).toBe(false); // OPUS 剔除
  });

  it("seek 门限 + 预载轨道：低于门限的块丢弃，其后正常发射", () => {
    const { callbacks, tracks, samples } = capture();
    const preloaded: TrackInfo[] = [
      {
        id: 1,
        kind: "video",
        codec: "hvc1.1.1.L120.B0",
        timescale: 90000,
        width: 3840,
        height: 2160,
        codecPrivate: makeHvcC(),
      },
    ];
    const d = new MatroskaDemuxer(callbacks, { preloadedTracks: preloaded, seekGateMs: 5000 });
    // 流直接从 Cluster 字节偏移开始（重载路径，无容器头）
    d.push(
      concat([
        cluster(4000, [simpleBlock(1, 0, true, new Uint8Array(16).fill(1))]), // 整块低于门限 → 丢弃
        cluster(6000, [simpleBlock(1, 0, true, new Uint8Array(16).fill(2))]),
      ]),
    );
    expect(tracks.length).toBe(1); // 预载轨道首个 push 即发布
    expect(tracks[0][0].id).toBe(1);
    const vs = samples.filter((s) => s.kind === "video");
    expect(vs.length).toBe(1);
    expect(vs[0].pts).toBe(6000 * 90); // 绝对时间码（由管线 startAnchorSec 锚定到 MSE 时间轴）
    expect(vs[0].dts).toBe(0);
  });

  it("parseMatroskaCues：从中部开始的尾窗扫描 + 严格校验", () => {
    // CueTime ID：0xB3 = RFC 8796 定案值（ffmpeg/mkvmerge 产出）；0xE2 = 旧草案值，兼容
    const cue = (time: number, pos: number, cueTimeId = 0xb3) =>
      el(
        0xbb,
        concat([
          el(cueTimeId, u(time)),
          el(0xb7, concat([el(0xf7, u(1)), el(0xf1, u(pos))])),
        ]),
      );
    const cues = el(
      0x1c53bb6b,
      concat([el(0xbf, u(0xdeadbeef, 4)), cue(0, 512), cue(5000, 10240, 0xe2), cue(10000, 20480)]),
    );
    // 尾窗从 Cues 元素中间开始：前面垫垃圾字节（含 ffmpeg 风格 CRC32 前缀）
    const garbage = new Uint8Array(37).fill(0x5a);
    const points = parseMatroskaCues(concat([garbage, cues]));
    expect(points).toEqual([
      { timeMs: 0, clusterPos: 512 },
      { timeMs: 5000, clusterPos: 10240 },
      { timeMs: 10000, clusterPos: 20480 },
    ]);
    // 空窗 / 无 Cues：返回空
    expect(parseMatroskaCues(new Uint8Array(64).fill(0))).toEqual([]);
  });
});
