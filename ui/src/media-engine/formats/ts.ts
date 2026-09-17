/**
 * MPEG-TS 传输流解析。
 * 依据 ISO/IEC 13818-1 公开规范重新实现。提供无状态纯解析：188 字节包帧、PAT/PMT PSI、
 * 以及 PES 头（含 PTS/DTS 抽取）。demux 上层（H264/H265/AAC/AC3 样本拆分）另行实现。
 */

export const TS_PACKET_SIZE = 188;

export interface TsPacket {
  pid: number;
  payloadUnitStart: boolean;
  continuityCounter: number;
  hasAdaptation: boolean;
  hasPayload: boolean;
  payload: Uint8Array; // 指向 payload 起始（已跳过 adaptation field）
}

/** 解析单个 188 字节 TS 包。offset 须是包起点（调用方负责按 sync byte 0x47 对齐）。 */
export function parseTsPacket(buffer: Uint8Array, offset: number): TsPacket | null {
  if (offset + TS_PACKET_SIZE > buffer.length) return null;
  if (buffer[offset] !== 0x47) return null;

  const flags1 = buffer[offset + 1];
  const flags2 = buffer[offset + 2];
  const flags3 = buffer[offset + 3];
  const pid = ((flags1 & 0x1f) << 8) | flags2;
  const payloadUnitStart = (flags1 & 0x40) !== 0;
  // adaptation_field_control 位于第 4 字节（offset+3）的 bits 5..4
  const adaptationFieldControl = (flags3 & 0x30) >> 4;
  const continuityCounter = flags3 & 0x0f;

  const hasAdaptation = (adaptationFieldControl & 0x2) !== 0;
  const hasPayload = (adaptationFieldControl & 0x1) !== 0;

  let payloadStart = offset + 4;
  if (hasAdaptation) {
    const adaptationLength = buffer[offset + 4];
    payloadStart += 1 + adaptationLength;
    if (payloadStart > offset + TS_PACKET_SIZE) return null;
  }
  if (!hasPayload) {
    return { pid, payloadUnitStart, continuityCounter, hasAdaptation, hasPayload, payload: new Uint8Array(0) };
  }

  const payload = buffer.subarray(payloadStart, offset + TS_PACKET_SIZE);
  return { pid, payloadUnitStart, continuityCounter, hasAdaptation, hasPayload, payload };
}

/** 在缓冲中找下一个 TS sync byte(0x47) 的偏移。 */
export function findTsSync(buffer: Uint8Array, start = 0): number {
  for (let i = start; i < buffer.length; i++) {
    if (buffer[i] === 0x47) return i;
  }
  return -1;
}

export interface PatEntry {
  programNumber: number;
  pmtPid: number;
}

/** 解析 PAT section payload（已跳过 pointer_field）。 */
export function parsePat(payload: Uint8Array): PatEntry[] {
  const entries: PatEntry[] = [];
  let p = 0;
  if (payload[0] !== 0x00) return entries; // table_id 须为 PAT
  const sectionLength = ((payload[1] & 0x0f) << 8) | payload[2];
  // table_id(1) + section_syntax(2) + section_length(2) + ts_id(2) + version/counter(1) + section_number(1) + last_section_number(1)
  p = 8;
  const end = Math.min(3 + sectionLength, payload.length);
  while (p + 4 <= end) {
    const programNumber = (payload[p] << 8) | payload[p + 1];
    const pmtPid = ((payload[p + 2] & 0x1f) << 8) | payload[p + 3];
    if (programNumber !== 0) entries.push({ programNumber, pmtPid });
    p += 4;
  }
  return entries;
}

export interface PmtStream {
  pid: number;
  streamType: number;
  /** ES_info 描述符原始字节（供私有流如 DVB AC-3/EAC-3 识别）。 */
  esInfo: Uint8Array;
}

export interface PmtInfo {
  pcrPid: number;
  streams: PmtStream[];
}

/** 从 ES_info 描述符识别 DVB 私有流(PES private, 0x06)承载的 Dolby 音频。
 *  判定：Registration Descriptor(0x05) "AC-3"/"EC-3"、
 *  ATSC AC-3(0x82)/ETSI AC-3(0x6A)、ETSI EAC3(0x7D/0x7A —— DVB 注册表 0x7A 即 DD+)。 */
export function detectPrivateAudioCodec(esInfo: Uint8Array): "ac3" | "eac3" | null {
  let p = 0;
  while (p + 2 <= esInfo.length) {
    const tag = esInfo[p];
    const len = esInfo[p + 1];
    if (p + 2 + len > esInfo.length) break;
    const body = esInfo.subarray(p + 2, p + 2 + len);
    if (tag === 0x05) {
      let reg = "";
      for (let i = 0; i < body.length; i++) reg += String.fromCharCode(body[i]);
      if (reg === "AC-3") return "ac3";
      if (reg === "EC-3") return "eac3";
    } else if (tag === 0x82 || tag === 0x6a) {
      return "ac3"; // ATSC AC-3 (0x82) / ETSI AC-3 descriptor (0x6A)
    } else if (tag === 0x7d || tag === 0x7a) {
      // EAC3/DD+（个别广电流把真实 DTS 误标 0x7A，此处分发到 eac3 解码器前由解码侧帧同步兜底）
      return "eac3";
    }
    p += 2 + len;
  }
  return null;
}

/** 解析 PMT section payload（已跳过 pointer_field）。 */
export function parsePmt(payload: Uint8Array): PmtInfo {
  const streams: PmtStream[] = [];
  if (payload[0] !== 0x02) return { pcrPid: 0, streams };
  const sectionLength = ((payload[1] & 0x0f) << 8) | payload[2];
  const pcrPid = ((payload[8] & 0x1f) << 8) | payload[9];
  const programInfoLength = ((payload[10] & 0x0f) << 8) | payload[11];
  let p = 12 + programInfoLength;
  const end = Math.min(3 + sectionLength, payload.length);
  while (p + 5 <= end) {
    const streamType = payload[p];
    const pid = ((payload[p + 1] & 0x1f) << 8) | payload[p + 2];
    const esInfoLength = ((payload[p + 3] & 0x0f) << 8) | payload[p + 4];
    streams.push({ pid, streamType, esInfo: payload.subarray(p + 5, p + 5 + esInfoLength) });
    p += 5 + esInfoLength;
  }
  return { pcrPid, streams };
}

export interface PesHeader {
  streamId: number;
  pts: number | null;
  dts: number | null;
  payloadStart: number; // 相对 payload 缓冲的偏移，PES 数据起点
}

/** 从 TS 包 payload（PES 起点，须 payloadUnitStart）解析 PES 头。 */
export function parsePesHeader(payload: Uint8Array): PesHeader | null {
  if (payload.length < 6) return null;
  if (payload[0] !== 0x00 || payload[1] !== 0x00 || payload[2] !== 0x01) return null;
  const streamId = payload[3];

  // PTS/DTS 仅出现在非 PES-private / non-padding / non-program streams
  const isVideo = (streamId & 0xf0) === 0xe0;
  const isAudio = (streamId & 0xe0) === 0xc0;
  const isPrivate = streamId === 0xbd;
  if (!isVideo && !isAudio && !isPrivate) {
    return { streamId, pts: null, dts: null, payloadStart: 6 };
  }

  if (payload.length < 9) return null;
  const flags = payload[7];
  const ptsDtsFlags = (flags >> 6) & 0x3;
  const headerDataLength = payload[8];
  let p = 9;
  let pts: number | null = null;
  let dts: number | null = null;
  if (ptsDtsFlags & 0x2) {
    if (p + 5 > payload.length) return null;
    pts = readPts(payload, p);
    p += 5;
  }
  if (ptsDtsFlags & 0x1) {
    if (p + 5 > payload.length) return null;
    dts = readPts(payload, p);
    p += 5;
  }
  const payloadStart = 9 + headerDataLength;
  return { streamId, pts, dts, payloadStart };
}

/** 从 5 字节抽取 33 位 PTS/DTS（90kHz 时间基，返回为 number）。 */
function readPts(buf: Uint8Array, p: number): number {
  const p0 = buf[p];
  const p1 = buf[p + 1];
  const p2 = buf[p + 2];
  const p3 = buf[p + 3];
  const p4 = buf[p + 4];
  // 33-bit 无符号整数（JS number 可精确表示到 2^53，无需截断）
  return (
    ((p0 & 0x0e) << 29) |
    (p1 << 22) |
    ((p2 & 0xfe) << 14) |
    (p3 << 7) |
    (p4 >> 1)
  ) >>> 0;
}
