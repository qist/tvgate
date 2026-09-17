/**
 * HLS 播放列表（m3u8）解析。
 * 依据 RFC 8216 公开规范重新实现，仅做文本→结构解析，不涉及解码。
 * 产出结构化 multivariant（variant 列表）与 media（分段序列）结果，供引擎调度使用。
 */

export interface HlsVariant {
  uri: string;
  bandwidth: number;
  averageBandwidth?: number;
  codecs: string[];
  resolution?: { width: number; height: number };
  frameRate?: number;
  videoRange?: string;
  audioGroup?: string;
  videoGroup?: string;
  closedCaptions?: string | null;
}

export interface HlsMediaRendition {
  type: string;
  groupId: string;
  uri?: string;
  language?: string;
  name?: string;
  isDefault?: boolean;
  autoselect?: boolean;
  channels?: string;
}

export interface HlsByteRange {
  offset: number;
  length: number | null;
}

export interface HlsKey {
  method: string;
  uri?: string;
  iv?: string;
  keyFormat?: string;
  keyFormatVersions?: string;
}

export interface HlsSegment {
  uri: string;
  duration: number;
  title?: string;
  byteRange?: HlsByteRange;
  discontinuity: boolean;
  programDateTime?: Date;
  map?: { uri: string; byteRange?: HlsByteRange };
  key?: HlsKey;
}

export interface ParsedPlaylist {
  isMaster: boolean;
  targetDuration?: number;
  mediaSequence?: number;
  endlist: boolean;
  playlistType?: string;
  variants: HlsVariant[];
  renditions: HlsMediaRendition[];
  segments: HlsSegment[];
}

function parseAttributeList(raw: string): Map<string, string> {
  const attrs = new Map<string, string>();
  let i = 0;
  const n = raw.length;
  while (i < n) {
    while (i < n && raw[i] === " ") i++;
    if (i >= n) break;
    let key = "";
    while (i < n && raw[i] !== "=") key += raw[i++];
    if (i >= n || raw[i] !== "=") break;
    i++; // skip '='
    let value = "";
    if (raw[i] === '"') {
      i++; // skip opening quote
      while (i < n && raw[i] !== '"') value += raw[i++];
      i++; // skip closing quote
    } else {
      while (i < n && raw[i] !== ",") value += raw[i++];
    }
    attrs.set(key, value);
    if (i < n && raw[i] === ",") i++;
  }
  return attrs;
}

function parseResolution(value: string | undefined): { width: number; height: number } | undefined {
  if (!value) return undefined;
  const m = /^(\d+)x(\d+)$/.exec(value);
  if (!m) return undefined;
  return { width: Number(m[1]), height: Number(m[2]) };
}

function parseByteRange(value: string | undefined): HlsByteRange | undefined {
  if (!value) return undefined;
  const m = /^(\d+)(?:@(\d+))?$/.exec(value);
  if (!m) return undefined;
  return { length: Number(m[1]), offset: m[2] ? Number(m[2]) : 0 };
}

function resolveUri(base: string, uri: string): string {
  if (/^[a-z][a-z0-9+.-]*:/i.test(uri) || uri.startsWith("//")) return uri;
  try {
    return new URL(uri, base).href;
  } catch {
    return uri;
  }
}

/** 解析 multivariant（master）播放列表。 */
function parseMasterPlaylist(base: string, lines: string[]): ParsedPlaylist {
  const playlist: ParsedPlaylist = {
    isMaster: true,
    endlist: false,
    variants: [],
    renditions: [],
    segments: [],
  };
  let pending: Map<string, string> | null = null;
  for (const line of lines) {
    if (line.startsWith("#EXT-X-MEDIA:")) {
      const a = parseAttributeList(line.slice("#EXT-X-MEDIA:".length));
      playlist.renditions.push({
        type: a.get("TYPE") ?? "",
        groupId: a.get("GROUP-ID") ?? "",
        uri: a.get("URI") ? resolveUri(base, a.get("URI")!) : undefined,
        language: a.get("LANGUAGE"),
        name: a.get("NAME"),
        isDefault: a.get("DEFAULT") === "YES",
        autoselect: a.get("AUTOSELECT") === "YES",
        channels: a.get("CHANNELS"),
      });
    } else if (line.startsWith("#EXT-X-STREAM-INF:")) {
      pending = parseAttributeList(line.slice("#EXT-X-STREAM-INF:".length));
    } else if (pending && !line.startsWith("#") && line.trim() !== "") {
      const variant: HlsVariant = {
        uri: resolveUri(base, line.trim()),
        bandwidth: Number(pending.get("BANDWIDTH") ?? "0"),
        averageBandwidth: pending.get("AVERAGE-BANDWIDTH") ? Number(pending.get("AVERAGE-BANDWIDTH")) : undefined,
        codecs: (pending.get("CODECS") ?? "").split(",").filter(Boolean),
        resolution: parseResolution(pending.get("RESOLUTION")),
        frameRate: pending.get("FRAME-RATE") ? Number(pending.get("FRAME-RATE")) : undefined,
        videoRange: pending.get("VIDEO-RANGE"),
        audioGroup: pending.get("AUDIO"),
        videoGroup: pending.get("VIDEO"),
        closedCaptions: pending.get("CLOSED-CAPTIONS") || null,
      };
      playlist.variants.push(variant);
      pending = null;
    }
  }
  return playlist;
}

/** 解析 media（子）播放列表。 */
function parseMediaPlaylist(base: string, lines: string[]): ParsedPlaylist {
  const playlist: ParsedPlaylist = {
    isMaster: false,
    endlist: false,
    variants: [],
    renditions: [],
    segments: [],
  };
  let current: HlsSegment | null = null;
  let pendingMap: { uri: string; byteRange?: HlsByteRange } | undefined;
  let pendingKey: HlsKey | undefined;
  let pendingByteRange: HlsByteRange | undefined;
  let pendingDiscontinuity = false;
  for (const line of lines) {
    if (line.startsWith("#EXT-X-TARGETDURATION:")) {
      playlist.targetDuration = Number(line.slice("#EXT-X-TARGETDURATION:".length));
    } else if (line.startsWith("#EXT-X-MEDIA-SEQUENCE:")) {
      playlist.mediaSequence = Number(line.slice("#EXT-X-MEDIA-SEQUENCE:".length));
    } else if (line.startsWith("#EXT-X-PLAYLIST-TYPE:")) {
      playlist.playlistType = line.slice("#EXT-X-PLAYLIST-TYPE:".length).trim();
    } else if (line.startsWith("#EXT-X-ENDLIST")) {
      playlist.endlist = true;
    } else if (line.startsWith("#EXT-X-KEY:")) {
      const a = parseAttributeList(line.slice("#EXT-X-KEY:".length));
      pendingKey = {
        method: a.get("METHOD") ?? "NONE",
        uri: a.get("URI") ? resolveUri(base, a.get("URI")!) : undefined,
        iv: a.get("IV"),
        keyFormat: a.get("KEYFORMAT"),
        keyFormatVersions: a.get("KEYFORMATVERSIONS"),
      };
    } else if (line.startsWith("#EXT-X-MAP:")) {
      const a = parseAttributeList(line.slice("#EXT-X-MAP:".length));
      pendingMap = { uri: resolveUri(base, a.get("URI")!), byteRange: parseByteRange(a.get("BYTERANGE")) };
    } else if (line.startsWith("#EXT-X-BYTERANGE:")) {
      pendingByteRange = parseByteRange(line.slice("#EXT-X-BYTERANGE:".length));
    } else if (line.startsWith("#EXT-X-DISCONTINUITY")) {
      // 标记下一个分段为不连续（可能出现在 EXTINF 之前或之后）
      pendingDiscontinuity = true;
    } else if (line.startsWith("#EXT-X-PROGRAM-DATE-TIME:")) {
      const ts = line.slice("#EXT-X-PROGRAM-DATE-TIME:".length).trim();
      const date = new Date(ts);
      if (!current) current = { uri: "", duration: 0, discontinuity: pendingDiscontinuity };
      if (!Number.isNaN(date.getTime())) current.programDateTime = date;
    } else if (line.startsWith("#EXTINF:")) {
      const body = line.slice("#EXTINF:".length);
      const dur = parseFloat(body.split(",")[0]);
      // 复用已由 DISCONTINUITY/PDT 打开的分段；新建时带上 discontinuity 标记
      if (!current) current = { uri: "", duration: 0, discontinuity: pendingDiscontinuity };
      current.duration = Number.isFinite(dur) ? dur : 0;
      current.title = body.includes(",") ? body.slice(body.indexOf(",") + 1) : undefined;
      pendingDiscontinuity = false;
      if (pendingByteRange) {
        current.byteRange = pendingByteRange;
        pendingByteRange = undefined;
      }
      if (pendingMap) {
        current.map = pendingMap;
        pendingMap = undefined;
      }
      if (pendingKey) {
        current.key = pendingKey;
      }
    } else if (!line.startsWith("#") && line.trim() !== "") {
      if (!current) current = { uri: "", duration: 0, discontinuity: pendingDiscontinuity };
      if (pendingDiscontinuity) current.discontinuity = true;
      // BYTERANGE/MAP/KEY 可能出现在 EXTINF 之后、URI 之前（fMP4 常见），推段时补挂
      if (pendingByteRange) {
        current.byteRange = pendingByteRange;
        pendingByteRange = undefined;
      }
      if (pendingMap) {
        current.map = pendingMap;
        pendingMap = undefined;
      }
      if (pendingKey) current.key = pendingKey;
      current.uri = resolveUri(base, line.trim());
      playlist.segments.push(current);
      current = null;
      pendingKey = undefined;
      pendingDiscontinuity = false;
    }
  }
  return playlist;
}

/** 解析 m3u8 文本，自动区分 master / media 播放列表。 */
export function parseM3U8(base: string, text: string): ParsedPlaylist {
  const lines = text.split(/\r\n|\r|\n/);
  if (lines.some((l) => l.startsWith("#EXT-X-STREAM-INF:") || l.startsWith("#EXT-X-MEDIA:"))) {
    return parseMasterPlaylist(base, lines);
  }
  return parseMediaPlaylist(base, lines);
}
