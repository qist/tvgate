import { readFileSync } from "node:fs";
import { describe, it, expect } from "vitest";
import { TsDemuxer, type TrackInfo, type DemuxedSample } from "./ts-demuxer";
import { TS_PACKET_SIZE } from "../formats/ts";

function tsPacket(pid: number, payload: Uint8Array, pus: boolean, cc: number): Uint8Array {
  const pkt = new Uint8Array(TS_PACKET_SIZE);
  pkt[0] = 0x47;
  pkt[1] = (pus ? 0x40 : 0) | ((pid >> 8) & 0x1f);
  pkt[2] = pid & 0xff;
  const content = Math.min(payload.length, TS_PACKET_SIZE - 4);
  // 内容未填满一包时，用 adaptation field stuffing 占位，避免零字节混入 PES 载荷
  const pad = TS_PACKET_SIZE - 4 - content;
  let start: number;
  if (pad > 0) {
    pkt[1] |= 0x20;
    pkt[3] = 0x30 | (cc & 0x0f);
    pkt[4] = pad - 1; // adaptation_field_length（不含本长度字节）
    start = 5 + (pad - 1);
  } else {
    pkt[3] = 0x10 | (cc & 0x0f);
    start = 4;
  }
  pkt.set(payload.subarray(0, content), start);
  return pkt;
}

function concat(parts: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(parts.reduce((a, p) => a + p.length, 0));
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return out;
}

function encodePts(pts: number): Uint8Array {
  const b = new Uint8Array(5);
  b[0] = 0x21 | (((pts >>> 30) & 0x07) << 1);
  b[1] = (pts >>> 22) & 0xff;
  b[2] = ((pts >>> 15) & 0x7f) << 1;
  b[3] = (pts >>> 7) & 0xff;
  b[4] = ((pts & 0x7f) << 1) | 0x01;
  return b;
}

/** 组装一段单节目 H264 TS 流。 */
function buildH264Ts(ptsValue: number): Uint8Array {
  // PAT
  const pat = new Uint8Array([0x00, 0x00, 0xb0, 0x09, 0x00, 0x01, 0xc1, 0x00, 0x00, 0x00, 0x01, 0xe1, 0x00]);
  // PMT：program 1, pcr pid 0x100, video pid 0x101 (stream_type 0x1b)
  const pmt = new Uint8Array([
    0x00, 0x02, 0xb0, 0x0e, 0x00, 0x01, 0xc1, 0x00, 0x00, 0xe1, 0x00, 0xf0, 0x00, 0x1b, 0xe1, 0x01, 0xf0, 0x00,
  ]);
  // PES 头（含 PTS）+ SPS；PES_packet_length=28（覆盖 6 字节后的标志+PTS+20 字节负载）
  const pesHead = new Uint8Array([0x00, 0x00, 0x01, 0xe0, 0x00, 0x1c, 0x80, 0x80, 0x05, ...encodePts(ptsValue)]);
  const sps = new Uint8Array([0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x1e]);
  const packetA = concat([pesHead, sps]);
  // PPS + IDR
  const pps = new Uint8Array([0x00, 0x00, 0x01, 0x68, 0xce, 0x3c, 0x80]);
  const idr = new Uint8Array([0x00, 0x00, 0x01, 0x65, 0x01, 0x02]);
  const packetB = concat([pps, idr]);
  // 下一帧起点：触发上一帧 finalize
  const packetC = new Uint8Array([0x00, 0x00, 0x01, 0x41, 0x03]);

  return concat([
    tsPacket(0, pat, true, 0),
    tsPacket(0x100, pmt, true, 0),
    tsPacket(0x101, packetA, true, 1),
    tsPacket(0x101, packetB, false, 2),
    tsPacket(0x101, packetC, true, 3),
  ]);
}

describe("TsDemuxer（H264 集成）", () => {
  it("PAT/PMT 建轨 → SPS/PPS 造 avcC → 出样本", () => {
    const tracks: TrackInfo[] = [];
    const samples: DemuxedSample[] = [];
    const demuxer = new TsDemuxer({
      onTracks: (t) => tracks.push(...t),
      onSamples: (s) => samples.push(...s),
    });
    demuxer.push(buildH264Ts(3600));

    expect(tracks).toHaveLength(1);
    const track = tracks[0];
    expect(track.kind).toBe("video");
    expect(track.codec).toBe("avc1.64001e"); // 由 SPS profile/constraint/level 生成完整 codec 串
    expect(track.codecPrivate.length).toBeGreaterThan(0);
    expect(String.fromCharCode(track.codecPrivate[4], track.codecPrivate[5], track.codecPrivate[6], track.codecPrivate[7])).toBe(
      "avcC",
    );

    expect(samples).toHaveLength(1);
    const s = samples[0];
    expect(s.isKeyframe).toBe(true);
    expect(s.pts).toBe(3600);
    expect(s.dts).toBe(3600);
    // 样本剥离参数集后仅剩 IDR（3 字节）+ 4 字节长度前缀
    expect(s.data.length).toBe(7);
    expect(s.data[0]).toBe(0);
    expect(s.data[3]).toBe(3); // 长度前缀 = 3
    expect(s.data[4]).toBe(0x65); // IDR
  });

  it("分块喂入不丢数据（任意切分）", () => {
    const samples: DemuxedSample[] = [];
    const demuxer = new TsDemuxer({ onSamples: (s) => samples.push(...s) });
    const stream = buildH264Ts(7200);
    for (let i = 0; i < stream.length; i += 250) {
      demuxer.push(stream.subarray(i, Math.min(i + 250, stream.length)));
    }
    expect(samples).toHaveLength(1);
    expect(samples[0].pts).toBe(7200);
  });

  it("无 PAT 的空流不报错", () => {
    const demuxer = new TsDemuxer({});
    demuxer.push(new Uint8Array(188 * 2)); // 全零，无 sync
    // 不应抛错
    expect(true).toBe(true);
  });
});

describe("TsDemuxer 扫描方式（scanType：徽标 1080p / 1080i）", () => {
  it("真实隔行素材（SPS frame_mbs_only_flag=0，field_order=tt）→ interlaced", () => {
    const data = new Uint8Array(readFileSync(new URL("./testdata/test-h264-interlaced.mpegts", import.meta.url)));
    let video: TrackInfo | undefined;
    const demuxer = new TsDemuxer({
      onTracks: (tracks) => {
        for (const t of tracks) {
          if (t.kind === "video") video ??= t;
        }
      },
      onSamples: () => {},
    });
    demuxer.push(data);

    expect(video).toBeDefined();
    expect(video?.scanType).toBe("interlaced");
    expect(video?.width).toBe(320);
    expect(video?.height).toBe(240);
  });

  it("解析出 SPS 的视频轨一定有扫描方式（逐行/隔行二选一，不留空）", () => {
    const tracks: TrackInfo[] = [];
    const demuxer = new TsDemuxer({ onTracks: (t) => tracks.push(...t) });
    demuxer.push(buildH264Ts(3600));
    expect(["progressive", "interlaced"]).toContain(tracks[0].scanType);
  });
});

describe("TsDemuxer 音频（AAC）：PES 边界与 ADTS 帧边界无关", () => {
  /** 28 字节 ADTS 帧：48kHz 立体声 AAC-LC，7 字节头 + 21 字节负载（负载填 fill 便于校验）。 */
  function adtsFrame(fill: number): Uint8Array {
    const f = new Uint8Array(28);
    f.set([0xff, 0xf1, 0x4c, 0x80, 0x03, 0x80, 0x00]);
    f.fill(fill, 7);
    return f;
  }

  /** 造一段 TS：PMT 声明 AAC(0x0f) 在 PID 0x102，payloads 依次作为各音频 PES 的载荷。 */
  function buildAacTs(payloads: Uint8Array[]): Uint8Array {
    const parts: Uint8Array[] = [];
    // PAT → PMT PID 0x100
    parts.push(tsPacket(0x0000, new Uint8Array([0x00, 0x00, 0xb0, 0x09, 0x00, 0x01, 0xc1, 0x00, 0x00, 0x00, 0x01, 0xe1, 0x00]), true, 0));
    // PMT：program 1, PCR PID 0x100, 流 0x0f(AAC) on PID 0x102
    parts.push(
      tsPacket(0x0100, new Uint8Array([0x00, 0x02, 0xb0, 0x0e, 0x00, 0x01, 0xc1, 0x00, 0x00, 0xe1, 0x00, 0xf0, 0x00, 0x0f, 0xe1, 0x02, 0xf0, 0x00]), true, 0),
    );
    payloads.forEach((payload, i) => {
      const len = 3 + 5 + payload.length; // 标志(3) + PTS(5) + 负载
      const head = new Uint8Array([
        0x00, 0x00, 0x01, 0xc0, (len >> 8) & 0xff, len & 0xff, 0x80, 0x80, 0x05, ...encodePts(36000 + i * 2048),
      ]);
      parts.push(tsPacket(0x0102, concat([head, payload]), true, i & 0x0f));
    });
    return concat(parts);
  }

  it("PES 载荷自帧中间开始时仍能扫出帧头（回归：整条音轨 0 样本 → 没声音）", () => {
    // 载荷开头是上一帧的尾巴（3 字节），随后两个完整帧；末位再发一帧用于冲刷前一段 PES
    // （解复用器靠"下一个 PUS 起点"flush 上一段，最后一个 PES 要有后继才会被处理）
    const samples: DemuxedSample[] = [];
    const tracks: TrackInfo[] = [];
    const demuxer = new TsDemuxer({ onTracks: (t) => tracks.push(...t), onSamples: (s) => samples.push(...s) });
    demuxer.push(
      buildAacTs([
        concat([new Uint8Array([0xaa, 0xbb, 0xcc]), adtsFrame(0x11), adtsFrame(0x22)]),
        adtsFrame(0x33),
        adtsFrame(0x44), // 冲刷上一段（最后一个 PES 无后继不会被处理）
      ]),
    );

    expect(tracks.some((t) => t.kind === "audio")).toBe(true);
    const audio = samples.filter((s) => s.kind === "audio");
    expect(audio).toHaveLength(3);
    // 产出的是裸 AAC（已剥 ADTS 头），且顺序与填入一致
    expect(Array.from(audio[0].data)).toEqual(new Array(21).fill(0x11));
    expect(Array.from(audio[1].data)).toEqual(new Array(21).fill(0x22));
    expect(Array.from(audio[2].data)).toEqual(new Array(21).fill(0x33));
  });

  it("半帧跨 PES：残尾要拼到下一段，不能丢帧", () => {
    const second = adtsFrame(0x22);
    const samples: DemuxedSample[] = [];
    const demuxer = new TsDemuxer({ onSamples: (s) => samples.push(...s) });
    demuxer.push(
      buildAacTs([
        concat([adtsFrame(0x11), second.subarray(0, 10)]), // 第二帧只发了一半
        concat([second.subarray(10), adtsFrame(0x33)]), // 另一半 + 第三帧
        adtsFrame(0x44), // 冲刷上一段
      ]),
    );
    const audio = samples.filter((s) => s.kind === "audio");
    expect(audio).toHaveLength(3);
    expect(Array.from(audio[0].data)).toEqual(new Array(21).fill(0x11));
    expect(Array.from(audio[1].data)).toEqual(new Array(21).fill(0x22)); // 跨 PES 拼回
    expect(Array.from(audio[2].data)).toEqual(new Array(21).fill(0x33));
    // 时间轴按名义帧长推进（48kHz 下 1024 样本/帧 → dts 间隔 1024）
    expect(audio[1].dts - audio[0].dts).toBe(1024);
  });
});


