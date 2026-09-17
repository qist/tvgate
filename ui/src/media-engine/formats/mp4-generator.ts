/**
 * 片段化 MP4（fMP4）生成器。
 * 依据 ISO/IEC 14496-12 / 14496-15 公开规范重新实现，把 demux 出的音视频 sample
 * 封装为 MSE 可用的 init segment（ftyp+moov）与 media segment（moof+mdat）。
 * 支持 H.264(avc1) 与 AAC(mp4a) 两条常见轨；纯字节构造，无外部依赖。
 */

export interface Fmp4TrackInit {
  id: number;
  kind: "video" | "audio";
  codec: string; // avc1.XXXXXX / hev1 / hvc1 / mp4a.40.x
  timescale: number;
  width?: number;
  height?: number;
  channels?: number;
  sampleRate?: number;
  codecPrivate: Uint8Array; // 完整 avcC / hvcC / esds box（含 box 头）
}

export interface Fmp4Sample {
  duration: number;
  data: Uint8Array;
  isKeyframe: boolean;
  ctsOffset: number; // pts - dts
}

export interface Fmp4TrackRun {
  trackId: number;
  baseMediaDecodeTime: number;
  samples: Fmp4Sample[];
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

function u16(v: number): Uint8Array {
  return new Uint8Array([(v >>> 8) & 0xff, v & 0xff]);
}
function u32(v: number): Uint8Array {
  return new Uint8Array([(v >>> 24) & 0xff, (v >>> 16) & 0xff, (v >>> 8) & 0xff, v & 0xff]);
}
function u64(v: number): Uint8Array {
  const hi = Math.floor(v / 2 ** 32) >>> 0;
  const lo = (v % 2 ** 32) >>> 0;
  return concat([u32(hi), u32(lo)]);
}
function fourcc(s: string): Uint8Array {
  const b = new Uint8Array(4);
  for (let i = 0; i < 4; i++) b[i] = s.charCodeAt(i);
  return b;
}
function box(type: string, payload: Uint8Array): Uint8Array {
  return concat([u32(payload.length + 8), fourcc(type), payload]);
}

/**
 * 按 codec 串选择视频 sample entry 的 fourcc。
 * 此前恒写 `avc1`：HEVC 流会生成 codec 与 box 类型不符的非法 init → 视频永不进 buffered。
 */
function videoEntryType(codec: string): string {
  const c = (codec ?? "").toLowerCase();
  if (c.startsWith("avc1") || c.startsWith("avc3")) return "avc1";
  if (c.startsWith("hev1") || c.startsWith("hvc1") || c.startsWith("hvc2")) return "hvc1";
  if (c.startsWith("av01")) return "av01";
  if (c.startsWith("vp09") || c.startsWith("vp9")) return "vp09";
  if (c.startsWith("dvh1") || c.startsWith("dvhe")) return "dvh1";
  return "avc1"; // 未知按 H.264 处理
}

function buildVideoSampleEntry(t: Fmp4TrackInit): Uint8Array {
  const head = concat([
    new Uint8Array(6), // reserved (SampleEntry)
    u16(1), // data_reference_index
    new Uint8Array(16), // pre_defined / reserved (VisualSampleEntry)
    u16(t.width ?? 0),
    u16(t.height ?? 0),
    u32(0x00480000), // horizresolution 72dpi
    u32(0x00480000), // vertresolution 72dpi
    new Uint8Array(4), // reserved
    u16(1), // frame_count
    new Uint8Array(32), // compressorname
    u16(0x0018), // depth
    u16(0xffff), // pre_defined
  ]);
  return box(videoEntryType(t.codec), concat([head, t.codecPrivate]));
}

function buildAudioSampleEntry(t: Fmp4TrackInit): Uint8Array {
  const channels = t.channels ?? 2;
  const sampleRate = t.sampleRate ?? 48000;
  const head = concat([
    new Uint8Array(6), // reserved (SampleEntry)
    u16(1), // data_reference_index
    new Uint8Array(8), // reserved[2]
    // 注意：MP4/Chrome 解析器在 reserved[2] 之后直接读 channelcount，
    // 中间没有 version/revision/vendor 字段（否则 channelcount/samplerate 会整体偏移 8 字节）
    u16(channels), // channelcount
    u16(16), // samplesize
    u16(0), // pre_defined / compressionId
    u16(0), // reserved / packetSize
    u32(Math.round(sampleRate * 65536)), // samplerate（16.16 定点）
  ]);
  return box("mp4a", concat([head, t.codecPrivate]));
}

function buildStsd(t: Fmp4TrackInit): Uint8Array {
  const entry = t.kind === "video" ? buildVideoSampleEntry(t) : buildAudioSampleEntry(t);
  return box("stsd", concat([u32(0), u32(1), entry]));
}

/** 构造 moov（init segment 主体）。 */
function buildMoov(tracks: Fmp4TrackInit[]): Uint8Array {
  const mvhd = box(
    "mvhd",
    concat([
      u32(0), // version + flags
      u32(0),
      u32(0), // creation + modification
      u32(1000), // timescale
      u32(0), // duration
      u32(0x00010000), // rate
      u16(0x0100),
      u16(0), // volume + reserved
      new Uint8Array(8), // reserved
      u32(0x00010000),
      u32(0),
      u32(0),
      u32(0),
      u32(0x00010000),
      u32(0),
      u32(0),
      u32(0),
      u32(0x40000000), // unity matrix
      new Uint8Array(24), // pre_defined
      u32(tracks.length + 1), // next_track_ID
    ]),
  );

  const traks = tracks.map((t) => {
    const tkhd = box(
      "tkhd",
      concat([
        u32(0x00000007), // version 0 + flags(track_enabled|in_movie|in_preview)
        u32(0),
        u32(0), // creation + modification
        u32(t.id),
        u32(0), // reserved
        u32(0), // duration
        new Uint8Array(8), // reserved
        u16(0), // layer
        u16(0), // alternate_group
        u16(t.kind === "audio" ? 0x0100 : 0), // volume
        u16(0), // reserved
        u32(0x00010000),
        u32(0),
        u32(0),
        u32(0),
        u32(0x00010000),
        u32(0),
        u32(0),
        u32(0),
        u32(0x40000000), // unity matrix
        u32(t.kind === "video" ? Math.round((t.width ?? 0) * 0x10000) : 0),
        u32(t.kind === "video" ? Math.round((t.height ?? 0) * 0x10000) : 0),
      ]),
    );

    const mdhd = box(
      "mdhd",
      concat([
        u32(0),
        u32(0),
        u32(0), // creation + modification
        u32(t.timescale),
        u32(0), // duration
        u16(0x55c4), // language "und"
        u16(0),
      ]),
    );

    const handlerType = t.kind === "video" ? "vide" : "soun";
    const hdlrName = t.kind === "video" ? "VideoHandler" : "SoundHandler";
    const hdlr = box(
      "hdlr",
      concat([
        u32(0),
        u32(0), // pre_defined
        fourcc(handlerType),
        new Uint8Array(12), // reserved
        strToBytes(hdlrName + "\0"),
      ]),
    );

    const vmhdOrSmhd =
      t.kind === "video"
        ? box("vmhd", concat([u32(1), u16(0), new Uint8Array(6)])) // graphicsmode + opcolor
        : box("smhd", concat([u32(0), u16(0), u16(0)])); // balance + reserved

    const dref = box("dref", concat([u32(0), u32(1), box("url ", concat([u32(0x00000001)]))]));
    const dinf = box("dinf", concat([dref]));

    const stbl = box(
      "stbl",
      concat([
        buildStsd(t),
        box("stts", concat([u32(0), u32(0)])),
        box("stsc", concat([u32(0), u32(0)])),
        box("stsz", concat([u32(0), u32(0), u32(0)])),
        box("stco", concat([u32(0), u32(0)])),
      ]),
    );

    const minf = box("minf", concat([vmhdOrSmhd, dinf, stbl]));
    const mdia = box("mdia", concat([mdhd, hdlr, minf]));
    return box("trak", concat([tkhd, mdia]));
  });

  const trexList = tracks.map((t) =>
    box("trex", concat([u32(0), u32(t.id), u32(1), u32(0), u32(0), u32(0)])),
  );
  const mvex = box("mvex", concat(trexList));

  return box("moov", concat([mvhd, ...traks, mvex]));
}

function strToBytes(s: string): Uint8Array {
  const b = new Uint8Array(s.length);
  for (let i = 0; i < s.length; i++) b[i] = s.charCodeAt(i);
  return b;
}

/** 生成 init segment（ftyp + moov）。 */
export function generateInitSegment(tracks: Fmp4TrackInit[]): Uint8Array {
  const ftyp = box("ftyp", concat([fourcc("iso5"), u32(0), fourcc("iso5"), fourcc("iso6"), fourcc("mp41"), fourcc("avc1"), fourcc("dash")]));
  return concat([ftyp, buildMoov(tracks)]);
}

function buildFragment(run: Fmp4TrackRun): Uint8Array {
  const sampleCount = run.samples.length;
  // trun 标志位（ISO/IEC 14496-12，勿与 tfhd 的 0x8/0x10/0x20 混淆）：
  //   0x000001 data-offset | 0x000100 sample-duration | 0x000200 sample-size
  //   | 0x000400 sample-flags | 0x000800 sample-composition-time-offset
  // 缺 sample-size 时 SourceBuffer 无法把 mdat 切成样本 → 一个样本都进不了 buffered（且不报错）。
  const trunFlags = 0x000001 | 0x000100 | 0x000200 | 0x000400 | 0x000800;

  // 第一遍：用占位 data-offset 算 moof 大小
  const moofFirst = buildMoof(run, sampleCount, trunFlags, 0);
  const moofSize = moofFirst.length;
  const dataOffset = moofSize + 8; // 紧跟 moof 的 mdat 头(8) 之后
  const moof = buildMoof(run, sampleCount, trunFlags, dataOffset);

  const sampleData = concat(run.samples.map((s) => s.data));
  const mdat = box("mdat", sampleData);

  return concat([moof, mdat]);
}

/** 每轨递增的 moof 序号（mfhd.sequence_number）。写死 trackId 会让
 *  分片序号恒定，部分 UA 的 coded frame group 判定异常。 */
const fragmentSequences = new Map<number, number>();

function buildMoof(run: Fmp4TrackRun, sampleCount: number, trunFlags: number, dataOffset: number): Uint8Array {
  const seq = (fragmentSequences.get(run.trackId) ?? 0) + 1;
  fragmentSequences.set(run.trackId, seq);
  const mfhd = box("mfhd", concat([u32(0), u32(seq)])); // version+flags + sequence_number

  const tfhd = box("tfhd", concat([u32(0x00020000), u32(run.trackId)])); // default-base-is-moof

  // tfdt version 1：首字节为 1（version），后三字节 flags=0
  const tfdt = box("tfdt", concat([u32(0x01000000), u64(run.baseMediaDecodeTime)]));

  // version=1：sample_composition_time_offset 为有符号值（负 cts 的 B 帧需要）
  const trunHeader = concat([u32(0x01000000 | trunFlags), u32(sampleCount), u32(dataOffset)]);
  const trunSamples: Uint8Array[] = [];
  for (const s of run.samples) {
    // 关键帧：dependsOn=2、isDependedOn=1 → 0x02400000；非关键帧：dependsOn=1、isNonSync=1 → 0x01010000
    const flags = s.isKeyframe ? 0x02400000 : 0x01010000;
    trunSamples.push(u32(s.duration));
    trunSamples.push(u32(s.data.length)); // sample size（缺此字段 SourceBuffer 无法切分 mdat → 报错）
    trunSamples.push(u32(flags));
    trunSamples.push(u32(s.ctsOffset));
  }
  const trun = box("trun", concat([trunHeader, ...trunSamples]));

  const traf = box("traf", concat([tfhd, tfdt, trun]));
  return box("moof", concat([mfhd, traf]));
}

/** 生成 media segment：每个轨道一个 moof+mdat 片段，依次拼接。 */
export function generateMediaSegment(runs: Fmp4TrackRun[]): Uint8Array {
  return concat(runs.map((run) => buildFragment(run)));
}
