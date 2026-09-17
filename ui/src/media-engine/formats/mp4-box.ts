/**
 * ISO BMFF（MP4/fMP4）box 解析。
 * 依据 ISO/IEC 14496-12 公开规范重新实现，仅做只读解析，不做重封装。
 * 用途（见引擎设计 §5.7 fMP4 透传）：从 init(moov) 抽 codec 串与各轨 timescale，
 * 从 media segment(moof/tfdt) 抽起始解码时间，供 HLS fMP4 直通过路识别时间戳/codec。
 */

export interface IsoBox {
  type: string;
  offset: number;
  size: number;
  data: Uint8Array;
}

/** 遍历一段缓冲中的顶层 box（不递归）。返回到的 data 为 payload（去掉 size+type 头）。 */
export function iterateTopLevelBoxes(buffer: Uint8Array, start = 0, end = buffer.length): IsoBox[] {
  const boxes: IsoBox[] = [];
  let pos = start;
  while (pos + 8 <= end) {
    const size = readUint32(buffer, pos);
    const type = readFourCC(buffer, pos + 4);
    if (size < 8) break;
    const payloadStart = pos + 8;
    const payloadEnd = Math.min(pos + size, end);
    boxes.push({ type, offset: pos, size, data: buffer.subarray(payloadStart, payloadEnd) });
    pos += size;
  }
  return boxes;
}

/** 在 container box 的 payload 中查找首个匹配路径（如 ["moov","trak"]）的 box。 */
export function findBox(buffer: Uint8Array, path: string[]): IsoBox | null {
  let scope = buffer;
  let box: IsoBox | null = null;
  for (const want of path) {
    box = iterateTopLevelBoxes(scope).find((b) => b.type === want) ?? null;
    if (!box) return null;
    scope = box.data;
  }
  return box;
}

/** 递归查找某 container 下所有匹配 type 的 box（含嵌套）。 */
export function collectBoxes(buffer: Uint8Array, type: string): IsoBox[] {
  const out: IsoBox[] = [];
  const walk = (scope: Uint8Array) => {
    for (const b of iterateTopLevelBoxes(scope)) {
      if (b.type === type) out.push(b);
      walk(b.data);
    }
  };
  walk(buffer);
  return out;
}

export function readUint32(buf: Uint8Array, offset: number): number {
  return ((buf[offset] << 24) | (buf[offset + 1] << 16) | (buf[offset + 2] << 8) | buf[offset + 3]) >>> 0;
}

export function readUint64(buf: Uint8Array, offset: number): number {
  const hi = readUint32(buf, offset);
  const lo = readUint32(buf, offset + 4);
  return hi * 2 ** 32 + lo;
}

function readFourCC(buf: Uint8Array, offset: number): string {
  let s = "";
  for (let i = 0; i < 4; i++) s += String.fromCharCode(buf[offset + i]);
  return s;
}

/** 解析 mdhd 的 timescale（单位：ticks/秒）。 */
export function parseMdhd(data: Uint8Array): { timescale: number; duration: number } {
  let p = 0;
  const version = data[p];
  p += 4; // version(1) + flags(3)
  if (version === 1) {
    p += 8 + 8; // creation + modification (64-bit)
  } else {
    p += 4 + 4; // creation + modification (32-bit)
  }
  const timescale = readUint32(data, p);
  p += 4;
  const duration = version === 1 ? readUint64(data, p) : readUint32(data, p);
  return { timescale, duration };
}

/** 解析 tfdt 的 baseMediaDecodeTime（= 媒体段起始解码时间，单位：track timescale）。 */
export function parseTfdt(data: Uint8Array): number {
  const version = data[0];
  if (version === 1) return readUint64(data, 4);
  return readUint32(data, 4);
}

/** 从 trak box 抽 codec 字符串（如 "avc1.4d401f"、"mp4a.40.2"、"hvc1..."）。 */
export function parseTrackCodec(trakData: Uint8Array): { codec?: string; handlerType?: string } {
  const stsd = findBox(trakData, ["mdia", "minf", "stbl", "stsd"]);
  if (!stsd) return {};
  // stsd: version(1) + flags(3) + entryCount(4)，之后每个 entry 是 size(4)+format(4)+...
  let p = 4; // skip version+flags
  const entryCount = readUint32(stsd.data, p);
  p += 4;
  for (let i = 0; i < entryCount && p + 8 <= stsd.data.length; i++) {
    const entrySize = readUint32(stsd.data, p);
    const format = readFourCC(stsd.data, p + 4);
    const codec = normalizeCodec(format, stsd.data.subarray(p, p + entrySize));
    if (codec) return { codec, handlerType: format };
    p += entrySize;
  }
  return {};
}

function normalizeCodec(format: string, entry: Uint8Array): string | undefined {
  switch (format) {
    case "avc1":
    case "avc3": {
      // avcC: after fixed sample-entry header (6 reserved + 2 dataRefIndex + 16 video sample entry)
      const avcC = findBoxInEntry(entry, "avcC");
      if (!avcC) return format;
      const sps = entry[avcC.offset + 9]; // AVCProfileIndication
      const profileCompat = entry[avcC.offset + 10];
      const level = entry[avcC.offset + 11]; // AVCLevelIndication
      return `${format}.${hex2(sps)}${hex2(profileCompat)}${hex2(level)}`;
    }
    case "hev1":
    case "hvc1": {
      const hvcC = findBoxInEntry(entry, "hvcC");
      if (!hvcC) return format;
      const generalByte = entry[hvcC.offset + 9]; // profile_space(2)+tier(1)+profile_idc(5)
      const profileSpace = (generalByte >> 6) & 0x3;
      const profile = generalByte & 0x1f;
      const tier = (generalByte >> 5) & 0x1;
      const level = entry[hvcC.offset + 12]; // general_level_idc
      return `${format}.${profileSpaceToString(profileSpace)}${hex2(profile)}${tier === 1 ? "L" : ""}${hex2(level)}`;
    }
    case "mp4a": {
      const esds = findBoxInEntry(entry, "esds");
      if (!esds) return "mp4a.40.2";
      const audioConfig = parseAudioObjectType(entry.subarray(esds.offset));
      return audioConfig ? `mp4a.40.${audioConfig}` : "mp4a.40.2";
    }
    default:
      return undefined;
  }
}

function findBoxInEntry(entry: Uint8Array, type: string): { offset: number } | null {
  // sample entry 的固定头（video ~78B / audio ~28B）不是嵌套 box，不能按 size 扫；
  // 直接按 fourcc 边界扫描（固定头区域为保留零值，不会误中）。
  const want = type.charCodeAt(0) | (type.charCodeAt(1) << 8) | (type.charCodeAt(2) << 16) | (type.charCodeAt(3) << 24);
  for (let i = 8; i + 8 <= entry.length; i++) {
    const v =
      entry[i + 4] |
      (entry[i + 5] << 8) |
      (entry[i + 6] << 16) |
      (entry[i + 7] << 24);
    if (v === want) return { offset: i };
  }
  return null;
}

/** 从 esds（box 起点传参，含 8 字节 box 头）解析 AudioObjectType。 */
export function parseAudioObjectType(esdsData: Uint8Array): number | undefined {
  // ES_Descriptor 位于 esds box 头(8) + version(1) + flags(3) = 偏移 12 处。
  let p = 12;
  // ES_Descriptor(0x03)：跳过 es_id(2)+flags(1) → DecoderConfigDescriptor
  if (esdsData[p] !== 0x03) return undefined;
  p = afterDescriptorLength(esdsData, p) + 3;
  // DecoderConfigDescriptor(0x04)：跳过 objectType/streamType/bufferSize/max/avg(13) → DecoderSpecificInfo
  if (esdsData[p] !== 0x04) return undefined;
  p = afterDescriptorLength(esdsData, p) + 13;
  // DecoderSpecificInfo(0x05)：内容首字节即 AudioSpecificConfig，含 5 位 audioObjectType
  if (esdsData[p] !== 0x05) return undefined;
  p = afterDescriptorLength(esdsData, p);
  return p < esdsData.length ? (esdsData[p] >> 3) & 0x1f : undefined;
}

/** 给定描述符 tag 位置，返回「长度字段之后（内容起点）」的位置。 */
function afterDescriptorLength(buf: Uint8Array, p: number): number {
  let q = p + 1; // 跳过 tag
  let size = 0;
  while (q < buf.length && buf[q] & 0x80) {
    size = (size << 7) | (buf[q] & 0x7f);
    q++;
  }
  if (q >= buf.length) return buf.length;
  size = (size << 7) | (buf[q] & 0x7f);
  return q + 1;
}

function hex2(n: number): string {
  return n.toString(16).padStart(2, "0");
}

function profileSpaceToString(s: number): string {
  return s === 0 ? "" : String.fromCharCode(65 + s - 1);
}
