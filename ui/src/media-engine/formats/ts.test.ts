import { describe, it, expect } from "vitest";
import { probeTsPacketSize } from "../demux/ts-demuxer";
import {
  parseTsPacket,
  parsePat,
  parsePmt,
  parsePesHeader,
  TS_PACKET_SIZE,
  findTsSync,
} from "./ts";

/** 构造 188 字节 TS 包。 */
function tsPacket(pid: number, payload: Uint8Array, payloadUnitStart: boolean, cc: number, withAdaptation = false): Uint8Array {
  const pkt = new Uint8Array(TS_PACKET_SIZE);
  pkt[0] = 0x47;
  pkt[1] = (withAdaptation ? 0x20 : 0) | (payloadUnitStart ? 0x40 : 0) | ((pid >> 8) & 0x1f);
  pkt[2] = pid & 0xff;
  pkt[3] = 0x10 | (cc & 0x0f); // payload 存在 + 连续计数
  let offset = 4;
  if (withAdaptation) {
    pkt[3] = 0x30 | (cc & 0x0f);
    pkt[4] = 183; // adaptation_field_length
    offset = 5 + 183;
  }
  pkt.set(payload.subarray(0, Math.min(payload.length, TS_PACKET_SIZE - offset)), offset);
  return pkt;
}

describe("probeTsPacketSize", () => {
  it("188 步长三连 sync 探测", () => {
    const buf = new Uint8Array(188 * 3);
    buf[0] = buf[188] = buf[376] = 0x47;
    expect(probeTsPacketSize(buf)).toEqual({ packetSize: 188, syncOffset: 0 });
  });
  it("带偏移时返回 syncOffset", () => {
    const buf = new Uint8Array(188 * 3 + 7);
    buf[7] = buf[195] = buf[383] = 0x47;
    expect(probeTsPacketSize(buf)).toEqual({ packetSize: 188, syncOffset: 7 });
  });
});

describe("parseTsPacket", () => {
  it("解析 PID / payloadUnitStart / payload", () => {
    const payload = new Uint8Array([1, 2, 3]);
    const pkt = tsPacket(0x100, payload, true, 0);
    const p = parseTsPacket(pkt, 0)!;
    expect(p.pid).toBe(0x100);
    expect(p.payloadUnitStart).toBe(true);
    expect(p.hasPayload).toBe(true);
    expect(p.payload[0]).toBe(1);
  });
  it("跳过 adaptation field", () => {
    const pkt = tsPacket(0x101, new Uint8Array(10), true, 1, true);
    const p = parseTsPacket(pkt, 0)!;
    expect(p.hasAdaptation).toBe(true);
    // adaptation_field_length=183 → payload 只有 0 字节
    expect(p.payload.length).toBe(0);
  });
});

describe("PAT / PMT", () => {
  it("PAT 解析出 program→pmtPid", () => {
    // section（不含 pointer_field）：见 mp4/ts 规范最小布局
    const section = new Uint8Array([
      0x00, // table_id
      0xb0, 0x09, // section_syntax + length
      0x00, 0x01, // transport_stream_id
      0xc1, 0x00, 0x00, // version/counter + section + last
      0x00, 0x01, 0xe1, 0x00, // program=1 → pmt pid=0x100
    ]);
    const entries = parsePat(section);
    expect(entries).toHaveLength(1);
    expect(entries[0].programNumber).toBe(1);
    expect(entries[0].pmtPid).toBe(0x100);
  });

  it("PMT 解析出 pcrPid 与流列表", () => {
    const section = new Uint8Array([
      0x02, // table_id
      0xb0, 0x0e, // section_syntax + length(14)
      0x00, 0x01, // program_number
      0xc1, 0x00, 0x00, // version + sections
      0xe1, 0x00, // pcr_pid = 0x100
      0xf0, 0x00, // program_info_length = 0
      0x1b, 0xe1, 0x01, 0xf0, 0x00, // video: stream_type H264, pid 0x101
    ]);
    const pmt = parsePmt(section);
    expect(pmt.pcrPid).toBe(0x100);
    expect(pmt.streams).toHaveLength(1);
    expect(pmt.streams[0].streamType).toBe(0x1b);
    expect(pmt.streams[0].pid).toBe(0x101);
  });
});

describe("PES 头", () => {
  function encodePts(pts: number): Uint8Array {
    const b = new Uint8Array(5);
    b[0] = 0x21 | (((pts >>> 30) & 0x07) << 1);
    b[1] = (pts >>> 22) & 0xff;
    b[2] = ((pts >>> 15) & 0x7f) << 1;
    b[3] = (pts >>> 7) & 0xff;
    b[4] = ((pts & 0x7f) << 1) | 0x01;
    return b;
  }

  function pesVideo(pts: number, body: number[]): Uint8Array {
    const ptsBytes = encodePts(pts);
    const arr = [0x00, 0x00, 0x01, 0xe0, 0x00, 0x00, 0x80, 0x80, 0x05];
    return new Uint8Array([...arr, ...ptsBytes, ...body]);
  }

  it("读取 streamId 与 33 位 PTS", () => {
    const pes = pesVideo(3600, [0x09, 0x10]);
    const h = parsePesHeader(pes)!;
    expect(h.streamId).toBe(0xe0);
    expect(h.pts).toBe(3600);
    expect(h.payloadStart).toBe(9 + 5);
    // PTS 值正确经过编码/解码往返
    for (const pts of [0, 1, 90000, 2 ** 32 - 1, 123456789]) {
      const hh = parsePesHeader(pesVideo(pts, []))!;
      expect(hh.pts).toBe(pts);
    }
  });

  it("audio 私有流(0xbd)有 PTS 也可解析", () => {
    const pts = encodePts(54000);
    const arr = [0x00, 0x00, 0x01, 0xbd, 0x00, 0x00, 0x80, 0x80, 0x05];
    const pes = new Uint8Array([...arr, ...pts, 1, 2, 3]);
    const h = parsePesHeader(pes)!;
    expect(h.pts).toBe(54000);
  });
});

describe("findTsSync", () => {
  it("返回首个 sync byte", () => {
    const buf = new Uint8Array([0, 1, 2, 0x47, 3, 4]);
    expect(findTsSync(buf)).toBe(3);
    expect(findTsSync(new Uint8Array([1, 2]), 0)).toBe(-1);
  });
});
