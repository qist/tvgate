/**
 * Matroska/WebM demuxer。
 * 依据 IETF RFC 8796（Matroska 公开规范）独立实现：EBML 流式解析 → Segment(Info/Tracks)
 * → Cluster(SimpleBlock/BlockGroup) → 与 TsDemuxer 同形协议产出样本与 codecPrivate（hvcC/avcC/esds）。
 *
 * 定位：网盘/直链 MKV（典型 4K HEVC + E-AC-3 组合走 MSE 原生 <video> 必然无声 —— 浏览器不解
 * E-AC-3）改走引擎软解管线时的容器解复用层。视频 HEVC/AVC 样本与 fMP4 remuxer 直接对齐
 * （Matroska 的 CodecPrivate 即 hvcC/avcC，块数据即长度前缀 NAL，样本原样进 mdat）；音频
 * AC-3/E-AC-3/MP2/MP3 走既有软解链路，AAC 走 MSE 直通。
 *
 * 时间语义（对照 libavformat/matroskadec.c：`pkt->pts = timecode`）：块 Timestamp = **PTS**，
 * 帧按解码序存放，B 帧下非单调；fMP4 需要单调 DTS —— 故视频 DTS 按「解码序号 × DefaultDuration」
 * 合成，cts = pts − dts 承载重排偏移（负 cts 由 trun v1 有符号回绕承载，mp4-generator 的 u32
 * 写入天然正确）。音频无重排：dts = pts，cts = 0。
 *
 * Seek 重载（cue 定位）双通道：
 *  - 头解析模式（整文件流）：正常解析 Info/Tracks/Cluster；
 *  - 预载模式（重载流从 Cluster 字节偏移开始）：容器头不在流内，TrackInfo 由外部经
 *    preloadedTracks 注入，流内只认 Cluster；绝对时间码低于 seekGateMs 的块整体丢弃
 *    （cue 定位点常早于目标时刻，前移的块不能进 MSE —— 否则重锚会把整条时间轴拉早）。
 */

import type { TsDemuxerCallbacks, TrackInfo, DemuxedSample } from "./ts-demuxer";
import {
    splitAnnexB,
    nalUnitType,
    buildAvcC,
    buildAvc1CodecString,
    avccFromNalus,
    NAL_TYPE_SPS,
    NAL_TYPE_PPS,
} from "../formats/avc";
import { HevcAnnexBReader, buildHvcC, HevcNaluType } from "./h265";
import { parseHevcSps } from "./h265-parser";
import { buildEsds } from "../formats/aac";

// ---- EBML 元素 ID（保留长度标记位，供直接比对） ----
const ID_EBML_HEADER = 0x1a45dfa3;
const ID_SEGMENT = 0x18538067;
const ID_INFO = 0x1549a966;
const ID_TIMECODE_SCALE = 0x2ad7b1;
const ID_DURATION = 0x4489;
const ID_TRACKS = 0x1654ae6b;
const ID_TRACK_ENTRY = 0xae;
const ID_TRACK_NUMBER = 0xd7;
const ID_TRACK_TYPE = 0x83;
const ID_CODEC_ID = 0x86;
const ID_CODEC_PRIVATE = 0x63a2;
const ID_DEFAULT_DURATION = 0x23e383;
const ID_VIDEO = 0xe0;
const ID_PIXEL_WIDTH = 0xb0;
const ID_PIXEL_HEIGHT = 0xba;
const ID_AUDIO = 0xe1;
const ID_SAMPLING_FREQ = 0xb5;
const ID_CHANNELS = 0x9f;
const ID_CLUSTER = 0x1f43b675;
const ID_CLUSTER_TIMECODE = 0xe7;
const ID_SIMPLE_BLOCK = 0xa3;
const ID_BLOCK_GROUP = 0xa0;
const ID_BLOCK = 0xa1;
const ID_BLOCK_DURATION = 0x9b;
const ID_REFERENCE_BLOCK = 0xfb;
const ID_PREV_SIZE = 0xa7;
const ID_CUES = 0x1c53bb6b;
const ID_SEEK_HEAD = 0x114d9b74;
const ID_TAGS = 0x1254c367;
const ID_CHAPTERS = 0x1043a770;
const ID_ATTACHMENTS = 0x1941a469;
const ID_VOID = 0xec;
const ID_CRC32 = 0xbf;

/** 视频轨统一 timescale（与 TsDemuxer 同口径，PTS 由毫秒换算 ×90）。 */
const VIDEO_TIMESCALE = 90000;
/** DefaultDuration 缺省（纳秒）：23.976fps。缺省只影响合成 DTS 步长与样本时长估计，
 *  显示时刻由 cts（= pts − dts）承载，不影响画面时序正确性。 */
const DEFAULT_FRAME_DURATION_NS = 1e9 / (24000 / 1001);
/** CodecPrivate 判定 hvcC 的最小长度（22 字节定长头 + 至少 1 字节数组区）。 */
const HVCC_MIN_BYTES = 23;
/** CodecPrivate 判定 avcC 的最小长度。 */
const AVCC_MIN_BYTES = 7;

// ---- EBML 魔数探测（pipeline feedDemuxer 用） ----

export interface MatroskaProbeResult {
    match: boolean;
    /** 头部不足 4 字节：需继续攒数据再判。 */
    needMoreData: boolean;
}

const PROBE_BYTES = 4;

export function matroskaProbe(data: Uint8Array): MatroskaProbeResult {
    const magic = [0x1a, 0x45, 0xdf, 0xa3];
    if (data.length < PROBE_BYTES) {
        // 与魔数前缀冲突才需要更多数据（否则直接判负，避免无限等待）
        for (let i = 0; i < data.length; i++) {
            if (data[i] !== magic[i]) return { match: false, needMoreData: false };
        }
        return { match: false, needMoreData: true };
    }
    for (let i = 0; i < PROBE_BYTES; i++) {
        if (data[i] !== magic[i]) return { match: false, needMoreData: false };
    }
    return { match: true, needMoreData: false };
}

// ---- Cue 索引解析（seek 尾窗探测用） ----

export interface MatroskaCuePoint {
    /** CueTime，已按 timecodeScale 折算为毫秒。 */
    timeMs: number;
    /** CueClusterPosition：相对 Segment payload 起点的字节偏移。 */
    clusterPos: number;
}

/**
 * 从文件尾窗字节里解析 CuePoint 列表。
 * 尾窗可能从元素中间开始：逐字节扫描 `1C 53 BB 6B`（Cues ID），要求 ID+Size 完整落在
 * 窗口内再解析；CuePoint 结构做严格校验（CueTime 严格递增、偏移不越界），失配即放弃该
 * 候选起点继续向后扫 —— 宁缺勿错（错位解析会产出毒化 seek 的假索引）。
 */
export function parseMatroskaCues(data: Uint8Array, timecodeScaleMs = 1): MatroskaCuePoint[] {
    const out: MatroskaCuePoint[] = [];
    for (let s = 0; s + 8 <= data.length; s++) {
        if (data[s] !== 0x1c || data[s + 1] !== 0x53 || data[s + 2] !== 0xbb || data[s + 3] !== 0x6b) {
            continue;
        }
        const sizeV = readVintValue(data, s + 4, false);
        if (!sizeV || sizeV.value <= 0) continue;
        const cuesEnd = s + 4 + sizeV.vintLen + sizeV.value;
        if (cuesEnd > data.length) continue; // Cues 不完整：窗口太小，宁缺勿错
        let p = s + 4 + sizeV.vintLen;
        let lastTime = -1;
        let ok = true;
        while (p + 2 <= cuesEnd) {
            // CRC32(0xBF)/Void(0xEC) 子元素（ffmpeg 会在 Cues 头部写 CRC32）：跳过
            if (data[p] === 0xbf || data[p] === 0xec) {
                const skipSize = readVintValue(data, p + 1, false);
                if (!skipSize || skipSize.value < 0 || p + 1 + skipSize.vintLen + skipSize.value > cuesEnd) {
                    ok = false;
                    break;
                }
                p = p + 1 + skipSize.vintLen + skipSize.value;
                continue;
            }
            if (data[p] !== 0xbb) break; // CuePoint ID 固定 1 字节；见到别的子元素即收尾
            const cpSize = readVintValue(data, p + 1, false);
            if (!cpSize || cpSize.value <= 0) { ok = false; break; }
            const cpEnd = p + 1 + cpSize.vintLen + cpSize.value;
            if (cpEnd > cuesEnd) { ok = false; break; }
            // CuePoint 内：CueTime (0xE2, uint) + CueTrackPositions (0xB7)
            let q = p + 1 + cpSize.vintLen;
            let timeMs = -1;
            let clusterPos = -1;
            while (q + 1 <= cpEnd && ok) {
                const el = readVintValue(data, q, true);
                const sz = el ? readVintValue(data, q + el.vintLen, false) : null;
                if (!el || !sz || sz.value < 0 || q + el.vintLen + sz.vintLen + sz.value > cpEnd) {
                    ok = false;
                    break;
                }
                const ps = q + el.vintLen + sz.vintLen;
                const pe = ps + sz.value;
                // CueTime ID 取 0xB3（RFC 8796 定案值，ffmpeg/mkvmerge 实际产出）；0xE2 为旧
                // matroska.com 草案值，一并兼容（著名的 CueTime ID 歧义，两者都有文件在用）。
                if ((el.value === 0xb3 || el.value === 0xe2) && timeMs < 0) {
                    timeMs = readUintPayload(data, ps, pe) * timecodeScaleMs;
                } else if (el.value === 0xb7 && clusterPos < 0) {
                    clusterPos = parseCueClusterPosition(data, ps, pe) ?? -1;
                }
                q = pe;
            }
            if (!ok || timeMs < 0 || clusterPos < 0 || timeMs <= lastTime) {
                ok = false;
                break;
            }
            lastTime = timeMs;
            out.push({ timeMs, clusterPos });
            p = cpEnd;
        }
        if (ok && out.length > 0) return out;
        out.length = 0; // 校验失败：继续向后扫描下一个候选位置
    }
    return out;
}

/** CueTrackPositions (0xB7) 内找 CueClusterPosition (0xF1)。 */
function parseCueClusterPosition(data: Uint8Array, from: number, to: number): number | null {
    let r = from;
    while (r + 1 <= to) {
        const el = readVintValue(data, r, true);
        const sz = el ? readVintValue(data, r + el.vintLen, false) : null;
        if (!el || !sz || sz.value < 0) return null;
        const ps = r + el.vintLen + sz.vintLen;
        const pe = ps + sz.value;
        if (pe > to) return null;
        if (el.value === 0xf1) return readUintPayload(data, ps, pe);
        r = pe;
    }
    return null;
}

/** 读取 payload 区间的无符号大端整数（≤6 字节）。 */
function readUintPayload(data: Uint8Array, from: number, to: number): number {
    let v = 0;
    for (let i = from; i < to && i < from + 6; i++) {
        v = v * 256 + data[i];
    }
    return v;
}

/** 读 payload 区间的 IEEE754 浮点（Matroska float 为 4 或 8 字节）。 */
function readFloatPayload(data: Uint8Array, from: number, to: number): number {
    const len = to - from;
    if (len === 4) return new DataView(data.buffer, data.byteOffset + from, 4).getFloat32(0);
    if (len === 8) return new DataView(data.buffer, data.byteOffset + from, 8).getFloat64(0);
    return 0;
}

function ascii(b: Uint8Array): string {
    let s = "";
    for (let i = 0; i < b.length; i++) s += String.fromCharCode(b[i]);
    return s;
}

/** 裸编解码器私有记录（MKV CodecPrivate）→ 完整 MP4 box（4B size + fourcc + payload）。 */
function wrapBox(type: string, payload: Uint8Array): Uint8Array {
    const out = new Uint8Array(payload.length + 8);
    const len = payload.length + 8;
    out[0] = (len >>> 24) & 0xff;
    out[1] = (len >>> 16) & 0xff;
    out[2] = (len >>> 8) & 0xff;
    out[3] = len & 0xff;
    for (let i = 0; i < 4; i++) out[4 + i] = type.charCodeAt(i);
    out.set(payload, 8);
    return out;
}

interface VintResult {
    value: number;
    vintLen: number;
}

/**
 * 读 EBML VINT：keepMarker=true 保留长度标记位（元素 ID / 块轨号语义），否则剥除。
 * size 语义下全 1 位域 = unknown size，返回 value = -1。
 */
function readVintValue(data: Uint8Array, pos: number, keepMarker: boolean): VintResult | null {
    if (pos >= data.length) return null;
    const first = data[pos];
    if (first === 0) return null; // 非法（长度 >8）
    let len = 1;
    let mask = 0x80;
    while (!(first & mask) && len < 8) {
        mask >>= 1;
        len++;
    }
    if (pos + len > data.length) return null;
    let value = keepMarker ? first : first & (0xff >> len);
    for (let i = 1; i < len; i++) {
        value = value * 256 + data[pos + i];
    }
    if (!keepMarker) {
        const maxForLen = len >= 8 ? 2 ** 56 - 1 : (1 << (7 * len)) - 1;
        if (value === maxForLen) return { value: -1, vintLen: len };
    }
    return { value, vintLen: len };
}

// ---- 内部状态 ----

interface MkTrackState {
    trackNumber: number;
    kind: "video" | "audio" | null;
    codecId: string;
    codecPrivate: Uint8Array | null;
    defaultDurationNs: number;
    width: number;
    height: number;
    sampleRate: number;
    channels: number;
    /** 解析产物（发布用）；不支持的轨为 null（样本静默丢弃）。 */
    info: TrackInfo | null;
    /** hvcC/avcC 的 NAL 长度前缀字节数；annexb 兜底模式为 0。 */
    lengthSize: number;
    /** annexb 兜底：CodecPrivate 缺失/非 hvcC/avcC 时样本为 AnnexB，现场收集参数集并转换。 */
    annexb: boolean;
    annexbVps: Uint8Array | null;
    annexbSps: Uint8Array | null;
    annexbPps: Uint8Array | null;
}

type ScopeKind = "info" | "tracks" | "trackentry" | "video" | "audio" | "cluster" | "blockgroup";

interface Scope {
    kind: ScopeKind;
    /** 容器 payload 末端（绝对流偏移）；-1 = 未知长度（仅 cluster 支持按子元素退出）。 */
    end: number;
    ctx: Record<string, unknown>;
}

/** BlockGroup 收集态：Block 到齐先暂存，ReferenceBlock 缺席与否决定关键帧，收口时统一发射。 */
interface BlockGroupCtx {
    frames: { trackNumber: number; relTsMs: number; frameData: Uint8Array[]; lacing: number }[];
    refs: number;
    durationMs: number | null;
}

/** 已读头、payload 未到齐的元素（跨 push 续读）。 */
interface IncompleteElement {
    id: number;
    size: number;
    startAbs: number;
    onPayload?: (payload: Uint8Array) => void;
}

export interface MatroskaDemuxerCallbacks extends TsDemuxerCallbacks {
    /** SegmentInfo.Duration 解析结果（秒）；文件无 Duration 字段时不发。 */
    onDuration?(seconds: number): void;
}

export interface MatroskaDemuxerOptions {
    /**
     * 预载轨道（seek 重载路径）：由文件头探测解析出的 TrackInfo 直接注入 —— 重载流从
     * cue 定位的 Cluster 字节偏移开始，容器头（Info/Tracks）不在流内，必须由外部喂入。
     */
    preloadedTracks?: TrackInfo[];
    /** seek 门限（毫秒）：绝对时间码低于该值的块整体丢弃（cue 定位点常早于目标时刻）。 */
    seekGateMs?: number;
}

export class MatroskaDemuxer {
    private readonly callbacks: MatroskaDemuxerCallbacks;
    private readonly preloadedTracks: TrackInfo[] | null;
    private readonly seekGateMs: number;

    private buf: Uint8Array = new Uint8Array(0);
    /** buf[0] 的绝对流偏移（压缩推进时累加），供 scope end / 不完整元素定位换算。 */
    private base = 0;
    private pos = 0;
    private scopes: Scope[] = [];
    private incomplete: IncompleteElement | null = null;
    private failed = false;

    private timecodeScale = 1e6;
    private durationRaw = 0;
    private durationEmitted = false;
    private tracksPublished = false;
    private readonly trackMap = new Map<number, MkTrackState>();
    /** Segment payload 起点的绝对偏移（CueClusterPosition → 绝对偏移换算用）。 */
    private segmentDataOffset: number | null = null;
    private clusterBaseMs = 0;
    /** 视频合成 DTS：解码序累加器（90kHz 刻度）。 */
    private videoDtsAcc = 0;

    constructor(callbacks: MatroskaDemuxerCallbacks, options: MatroskaDemuxerOptions = {}) {
        this.callbacks = callbacks;
        this.preloadedTracks = options.preloadedTracks ?? null;
        this.seekGateMs = options.seekGateMs ?? 0;
        if (this.preloadedTracks) {
            for (const t of this.preloadedTracks) this.seedTrack(t);
        }
    }

    /** Segment payload 起点的绝对偏移；未解析到 Segment 头时为 null。 */
    get segmentOffset(): number | null {
        return this.segmentDataOffset;
    }

    push(chunk: Uint8Array): void {
        if (this.failed || chunk.length === 0) return;
        const rest = this.buf.length - this.pos;
        const merged = new Uint8Array(rest + chunk.length);
        if (rest > 0) merged.set(this.buf.subarray(this.pos), 0);
        merged.set(chunk, rest);
        this.base += this.pos;
        this.buf = merged;
        this.pos = 0;
        if (this.preloadedTracks && !this.tracksPublished) this.publishTracks();
        this.parseLoop();
        // 丢弃已消费前缀。不完整元素的 payload 一定还完整落在 buf 尾部（未消费），连续可读。
        if (this.pos > 0) {
            this.base += this.pos;
            this.buf = this.buf.subarray(this.pos);
            this.pos = 0;
        }
    }

    reset(): void {
        this.buf = new Uint8Array(0);
        this.base = 0;
        this.pos = 0;
        this.scopes = [];
        this.incomplete = null;
        this.failed = false;
        this.timecodeScale = 1e6;
        this.durationRaw = 0;
        this.durationEmitted = false;
        this.tracksPublished = false;
        this.trackMap.clear();
        this.segmentDataOffset = null;
        this.clusterBaseMs = 0;
        this.videoDtsAcc = 0;
        if (this.preloadedTracks) {
            for (const t of this.preloadedTracks) this.seedTrack(t);
        }
    }

    private seedTrack(t: TrackInfo): void {
        this.trackMap.set(t.id, {
            trackNumber: t.id,
            kind: t.kind,
            codecId: "",
            codecPrivate: t.codecPrivate,
            defaultDurationNs: DEFAULT_FRAME_DURATION_NS,
            width: t.width ?? 0,
            height: t.height ?? 0,
            sampleRate: t.sampleRate ?? 0,
            channels: t.channels ?? 0,
            info: t,
            lengthSize: t.codec.startsWith("hvc1") || t.codec.startsWith("avc1") ? 4 : 0,
            annexb: false,
            annexbVps: null,
            annexbSps: null,
            annexbPps: null,
        });
    }

    // ==================== 解析主循环 ====================

    private parseLoop(): void {
        while (!this.failed) {
            const scope = this.scopes[this.scopes.length - 1];
            const abs = this.base + this.pos;
            // 1) 有界容器越界：收口弹出（Info/Tracks/TrackEntry/Cluster/BlockGroup）
            if (scope && scope.end >= 0 && abs >= scope.end) {
                this.closeScope(scope);
                this.scopes.pop();
                continue;
            }
            // 2) 不完整元素：payload 到齐即执行回调（含"纯跳过"语义）
            if (this.incomplete) {
                const el = this.incomplete;
                const endAbs = el.startAbs + el.size;
                if (this.base + this.buf.length < endAbs) return; // 等更多数据
                const start = el.startAbs - this.base;
                const payload = this.buf.subarray(start, start + el.size);
                this.pos = start + el.size;
                this.incomplete = null;
                el.onPayload?.(payload);
                continue;
            }
            // 3) 读元素头（ID + Size）
            const headStart = this.pos;
            const idV = readVintValue(this.buf, this.pos, true);
            if (!idV) { this.pos = headStart; return; }
            const sizeV = readVintValue(this.buf, this.pos + idV.vintLen, false);
            if (!sizeV) { this.pos = headStart; return; }
            const id = idV.value;
            const size = sizeV.value;
            const payloadStartAbs = this.base + this.pos + idV.vintLen + sizeV.vintLen;
            this.pos += idV.vintLen + sizeV.vintLen;
            // 4) 分派
            if (scope && scope.kind === "cluster") {
                if (this.isClusterChild(id)) {
                    this.openElement(id, size, payloadStartAbs);
                    continue;
                }
                // 兄弟元素（下一个 Cluster/Cues/…）：退出 unknown-size cluster，回退头字节交上层重读
                this.closeScope(scope);
                this.scopes.pop();
                this.pos = headStart;
                continue;
            }
            this.openElement(id, size, payloadStartAbs);
        }
    }

    /** 读到元素头后的分派：容器开 scope / 数值元素收集 / 跳过。 */
    private openElement(id: number, size: number, payloadStartAbs: number): void {
        const scope = this.scopes[this.scopes.length - 1];
        const containerEnd = payloadStartAbs + size;
        const kind: ScopeKind | "top" = scope ? scope.kind : "top";
        switch (kind) {
            case "top": {
                switch (id) {
                    case ID_SEGMENT:
                        // Segment payload 起点（CueClusterPosition 的基准）。不压 scope 也不跳过：
                        // unknown size 是流式封装常态，其内容（Info/Tracks/Cluster…）按顶层语义继续铺开。
                        if (this.segmentDataOffset === null) this.segmentDataOffset = payloadStartAbs;
                        return;
                    case ID_INFO:
                        if (size < 0) return this.fail("Info 元素为未知长度");
                        this.scopes.push({ kind: "info", end: containerEnd, ctx: {} });
                        return;
                    case ID_TRACKS:
                        if (size < 0) return this.fail("Tracks 元素为未知长度");
                        this.scopes.push({ kind: "tracks", end: containerEnd, ctx: {} });
                        return;
                    case ID_CLUSTER:
                        this.scopes.push({ kind: "cluster", end: size >= 0 ? containerEnd : -1, ctx: {} });
                        return;
                    case ID_CUES:
                    case ID_SEEK_HEAD:
                    case ID_TAGS:
                    case ID_CHAPTERS:
                    case ID_ATTACHMENTS:
                    case ID_VOID:
                    case ID_CRC32:
                    case ID_EBML_HEADER:
                    default:
                        return this.skipOrWait(id, size, payloadStartAbs);
                }
            }
            case "info": {
                if (id === ID_TIMECODE_SCALE || id === ID_DURATION) {
                    return this.collectOrWait(id, size, payloadStartAbs, (p) => {
                        if (id === ID_TIMECODE_SCALE) {
                            const v = readUintPayload(p, 0, p.length);
                            if (v > 0) this.timecodeScale = v;
                        } else {
                            const f = readFloatPayload(p, 0, p.length);
                            if (Number.isFinite(f) && f > 0) this.durationRaw = f;
                        }
                    });
                }
                return this.skipOrWait(id, size, payloadStartAbs);
            }
            case "tracks": {
                if (id === ID_TRACK_ENTRY) {
                    if (size < 0) return this.fail("TrackEntry 元素为未知长度");
                    const st: MkTrackState = {
                        trackNumber: 0,
                        kind: null,
                        codecId: "",
                        codecPrivate: null,
                        defaultDurationNs: DEFAULT_FRAME_DURATION_NS,
                        width: 0,
                        height: 0,
                        sampleRate: 0,
                        channels: 0,
                        info: null,
                        lengthSize: 4,
                        annexb: false,
                        annexbVps: null,
                        annexbSps: null,
                        annexbPps: null,
                    };
                    this.scopes.push({ kind: "trackentry", end: containerEnd, ctx: { st } });
                    return;
                }
                return this.skipOrWait(id, size, payloadStartAbs);
            }
            case "trackentry": {
                if (!(scope.ctx as { st?: MkTrackState }).st) (scope.ctx as { st?: MkTrackState }).st = {
                    trackNumber: 0, kind: null, codecId: "", codecPrivate: null,
                    defaultDurationNs: DEFAULT_FRAME_DURATION_NS, width: 0, height: 0,
                    sampleRate: 0, channels: 0, info: null, lengthSize: 4, annexb: false,
                    annexbVps: null, annexbSps: null, annexbPps: null,
                };
                const st = (scope.ctx as { st: MkTrackState }).st;
                switch (id) {
                    case ID_TRACK_NUMBER:
                        return this.collectOrWait(id, size, payloadStartAbs, (p) => {
                            st.trackNumber = readUintPayload(p, 0, p.length);
                        });
                    case ID_TRACK_TYPE:
                        return this.collectOrWait(id, size, payloadStartAbs, (p) => {
                            const t = readUintPayload(p, 0, p.length);
                            st.kind = t === 1 ? "video" : t === 2 ? "audio" : null;
                        });
                    case ID_CODEC_ID:
                        return this.collectOrWait(id, size, payloadStartAbs, (p) => {
                            st.codecId = ascii(p);
                        });
                    case ID_CODEC_PRIVATE:
                        return this.collectOrWait(id, size, payloadStartAbs, (p) => {
                            st.codecPrivate = p.slice();
                        });
                    case ID_DEFAULT_DURATION:
                        return this.collectOrWait(id, size, payloadStartAbs, (p) => {
                            const v = readUintPayload(p, 0, p.length);
                            if (v > 0) st.defaultDurationNs = v;
                        });
                    case ID_VIDEO:
                        if (size < 0) return this.fail("Video 元素为未知长度");
                        this.scopes.push({ kind: "video", end: containerEnd, ctx: { st } });
                        return;
                    case ID_AUDIO:
                        if (size < 0) return this.fail("Audio 元素为未知长度");
                        this.scopes.push({ kind: "audio", end: containerEnd, ctx: { st } });
                        return;
                    default:
                        return this.skipOrWait(id, size, payloadStartAbs);
                }
            }
            case "video": {
                const st = (scope.ctx as { st: MkTrackState }).st;
                if (id === ID_PIXEL_WIDTH || id === ID_PIXEL_HEIGHT) {
                    return this.collectOrWait(id, size, payloadStartAbs, (p) => {
                        const v = readUintPayload(p, 0, p.length);
                        if (id === ID_PIXEL_WIDTH) st.width = v;
                        else st.height = v;
                    });
                }
                return this.skipOrWait(id, size, payloadStartAbs);
            }
            case "audio": {
                const st = (scope.ctx as { st: MkTrackState }).st;
                if (id === ID_SAMPLING_FREQ || id === ID_CHANNELS) {
                    return this.collectOrWait(id, size, payloadStartAbs, (p) => {
                        if (id === ID_SAMPLING_FREQ) {
                            const f = readFloatPayload(p, 0, p.length);
                            if (Number.isFinite(f) && f > 0) st.sampleRate = Math.round(f);
                        } else {
                            st.channels = readUintPayload(p, 0, p.length);
                        }
                    });
                }
                return this.skipOrWait(id, size, payloadStartAbs);
            }
            case "cluster": {
                switch (id) {
                    case ID_CLUSTER_TIMECODE:
                        return this.collectOrWait(id, size, payloadStartAbs, (p) => {
                            const v = readUintPayload(p, 0, p.length);
                            this.clusterBaseMs = (v * this.timecodeScale) / 1e6;
                        });
                    case ID_SIMPLE_BLOCK:
                        return this.collectOrWait(id, size, payloadStartAbs, (p) => {
                            const blk = this.parseBlockPayload(p, true);
                            if (blk) {
                                this.emitSamples(blk.trackNumber, blk.relTsMs, blk.frames, blk.isKeyframe);
                            }
                        });
                    case ID_BLOCK_GROUP:
                        if (size < 0) return this.fail("BlockGroup 元素为未知长度");
                        this.scopes.push({
                            kind: "blockgroup",
                            end: containerEnd,
                            ctx: { bg: { frames: [], refs: 0, durationMs: null } as BlockGroupCtx },
                        });
                        return;
                    default: // PREV_SIZE / VOID / CRC32
                        return this.skipOrWait(id, size, payloadStartAbs);
                }
            }
            case "blockgroup": {
                const bg = (scope.ctx as { bg: BlockGroupCtx }).bg;
                switch (id) {
                    case ID_BLOCK:
                        return this.collectOrWait(id, size, payloadStartAbs, (p) => {
                            const blk = this.parseBlockPayload(p, false);
                            if (blk) {
                                bg.frames.push({
                                    trackNumber: blk.trackNumber,
                                    relTsMs: blk.relTsMs,
                                    frameData: blk.frames,
                                    lacing: 0,
                                });
                            }
                        });
                    case ID_REFERENCE_BLOCK:
                        return this.collectOrWait(id, size, payloadStartAbs, () => {
                            bg.refs++;
                        });
                    case ID_BLOCK_DURATION:
                        return this.collectOrWait(id, size, payloadStartAbs, (p) => {
                            const v = readUintPayload(p, 0, p.length);
                            bg.durationMs = (v * this.timecodeScale) / 1e6;
                        });
                    default:
                        return this.skipOrWait(id, size, payloadStartAbs);
                }
            }
            default:
                return this.fail(`非预期的容器状态: ${kind}`);
        }
    }

    /** TrackEntry 收口：按 CodecID/CodecPrivate 构造 TrackInfo（不支持的轨置 null）。 */
    private finalizeTrack(st: MkTrackState): void {
        if (this.preloadedTracks) return; // 预载模式：轨道以注入为准，流内 TrackEntry 忽略
        if (!st.trackNumber || !st.kind) return;
        if (st.kind === "video") {
            this.finalizeVideoTrack(st);
        } else {
            this.finalizeAudioTrack(st);
        }
        if (st.info) this.trackMap.set(st.trackNumber, st);
    }

    private finalizeVideoTrack(st: MkTrackState): void {
        const cp = st.codecPrivate;
        const base = {
            id: st.trackNumber,
            kind: "video" as const,
            timescale: VIDEO_TIMESCALE,
            width: st.width,
            height: st.height,
            scanType: "progressive" as const,
        };
        if (st.codecId === "V_MPEGH/ISO/HEVC") {
            if (cp && cp.length >= HVCC_MIN_BYTES && cp[0] === 1) {
                st.lengthSize = (cp[21] & 0x03) + 1;
                st.info = {
                    ...base,
                    // MSE 需要完整串（裸 "hvc1" 会被 isTypeSupported 拒绝）；兼容性/约束位按
                    // 播放链路既有口径固定（isTypeSupported 只做前缀匹配）。
                    codec: `hvc1.${cp[1] & 0x1f}.1.L${cp[12]}.B0`,
                    // 关键：MKV CodecPrivate 是**裸** HEVCDecoderConfigurationRecord（无 box 头），
                    // 而 TrackInfo.codecPrivate 的既有约定是完整 box（buildHvcC/buildAvcC 同口径，
                    // mp4-generator 直接塞进 hvc1 sample entry）—— 必须补 hvcC box 头，
                    // 否则 init 段非法（ffmpeg "No start code"、Chrome 拒收整条视频轨）。
                    codecPrivate: wrapBox("hvcC", cp),
                };
                return;
            }
            if (cp && looksLikeAnnexB(cp)) {
                st.annexb = true;
                st.lengthSize = 0;
                return; // 参数集从样本现场收集，收齐后补发 TrackInfo
            }
            st.annexb = true; // 无 CodecPrivate：按 AnnexB 现场收集兜底
            st.lengthSize = 0;
            return;
        }
        if (st.codecId === "V_MPEG4/ISO/AVC") {
            if (cp && cp.length >= AVCC_MIN_BYTES && cp[0] === 1) {
                st.lengthSize = (cp[4] & 0x03) + 1;
                st.info = {
                    ...base,
                    codec: buildAvc1CodecString({
                        profileIdc: cp[1],
                        constraintFlags: cp[2],
                        levelIdc: cp[3],
                    }),
                    // 同 hvcC：裸 avcC 记录补 box 头（TrackInfo.codecPrivate = 完整 box 约定）
                    codecPrivate: wrapBox("avcC", cp),
                };
                return;
            }
            st.annexb = true;
            st.lengthSize = 0;
            return;
        }
        // VP8/VP9/AV1/MPEG1/2 等：不支持（remuxer 无对应 fMP4 sample entry）
        st.info = null;
    }

    private finalizeAudioTrack(st: MkTrackState): void {
        const cp = st.codecPrivate;
        const sampleRate = st.sampleRate > 0 ? st.sampleRate : 48000;
        const channels = st.channels > 0 ? st.channels : 2;
        const base = {
            id: st.trackNumber,
            kind: "audio" as const,
            sampleRate,
            channels,
            codecPrivate: new Uint8Array(0),
        };
        if (st.codecId.startsWith("A_AAC")) {
            // CodecPrivate = AudioSpecificConfig：包成 esds 交 remuxer（MSE 直通）
            if (!cp || cp.length < 2) return;
            const aot = cp[0] >> 3;
            const freqIdx = ((cp[0] & 0x07) << 1) | (cp[1] >> 7);
            const chCfg = (cp[1] >> 3) & 0x0f;
            const freq = AAC_SAMPLE_RATES[freqIdx] ?? sampleRate;
            st.info = {
                ...base,
                codec: `mp4a.40.${aot || 2}`,
                timescale: freq,
                sampleRate: freq,
                channels: chCfg > 0 ? chCfg : channels,
                codecPrivate: buildEsds(cp),
            };
            return;
        }
        if (st.codecId === "A_EAC3" || st.codecId === "A_AC3" || st.codecId === "A_MPEG/L3" || st.codecId === "A_MPEG/L2") {
            const codec =
                st.codecId === "A_EAC3" ? "eac3" : st.codecId === "A_AC3" ? "ac3" : st.codecId === "A_MPEG/L3" ? "mp3" : "mp2";
            st.info = { ...base, codec, timescale: VIDEO_TIMESCALE };
            return;
        }
        // A_OPUS/A_VORBIS/A_FLAC/A_DTS/A_TRUEHD/…：不支持
        st.info = null;
    }

    /** 发布轨道 + 布局（幂等：annexb 参数集收齐后的补发也走这里）。 */
    private publishTracks(): void {
        if (this.tracksPublished && !this.preloadedTracks) {
            // 已发布过的补发场景：仅重发 TrackInfo（remuxer.addTrack 幂等）
            const infos = [...this.trackMap.values()].map((t) => t.info).filter((t): t is TrackInfo => !!t);
            if (infos.length > 0) this.callbacks.onTracks?.(infos);
            return;
        }
        this.tracksPublished = true;
        const infos = [...this.trackMap.values()].map((t) => t.info).filter((t): t is TrackInfo => !!t);
        if (infos.length === 0) return;
        const video = infos.some((t) => t.kind === "video");
        const mseAudio = infos.some((t) => t.kind === "audio" && t.codec.startsWith("mp4a"));
        const softAudio = infos.some(
            (t) => t.kind === "audio" && (t.codec === "ac3" || t.codec === "eac3" || t.codec === "mp2" || t.codec === "mp3"),
        );
        this.callbacks.onTracks?.(infos);
        this.callbacks.onStreamLayout?.({ video, mseAudio, softAudio });
    }

    // ==================== 块解析与样本发射 ====================

    /**
   * SimpleBlock / Block 内的 Block payload 解析。
   * payload：TrackNumber VINT（保留标记位）+ int16 相对时间戳 + flags + [lace 头] + 帧数据。
   * keyframeFromFlags：SimpleBlock 的 flags bit0 = 关键帧；Block（BlockGroup 内）该位保留，
   * 关键帧由调用方按 ReferenceBlock 缺席判定（收口时统一发射）。
   */
    private parseBlockPayload(
        p: Uint8Array,
        keyframeFromFlags: boolean,
    ): { trackNumber: number; relTsMs: number; frames: Uint8Array[]; isKeyframe: boolean } | null {
        const tn = readVintValue(p, 0, false); // 块轨号：VINT 编码但语义为剥标记后的数值（0x81 → 1）
        if (!tn || tn.vintLen + 3 > p.length) return null;
        let o = tn.vintLen;
        const relTs = (p[o] << 8) | p[o + 1];
        // int16 有符号：B 帧的块相对时间戳可为负（pts 按显示序声明、帧按解码序存放）
        const relTsMs = (((relTs > 32767 ? relTs - 65536 : relTs) * this.timecodeScale) / 1e6);
        const flags = p[o + 2];
        o += 3;
        const lacing = (flags >> 1) & 0x03;
        const keyframe = keyframeFromFlags ? (flags & 0x80) !== 0 : false;
        const frames = splitLaces(p.subarray(o), lacing);
        if (frames.length === 0) return null;
        return { trackNumber: tn.value, relTsMs, frames, isKeyframe: keyframe };
    }

    /** BlockGroup 收口：ReferenceBlock 缺席 = 关键帧。 */
    private closeScope(scope: Scope): void {
        if (scope.kind === "tracks") {
            this.publishTracks();
            return;
        }
        if (scope.kind === "trackentry") {
            const st = (scope.ctx as { st: MkTrackState }).st;
            this.finalizeTrack(st);
            return;
        }
        if (scope.kind === "info") {
            if (this.durationRaw > 0 && !this.durationEmitted) {
                this.durationEmitted = true;
                this.callbacks.onDuration?.((this.durationRaw * this.timecodeScale) / 1e9);
            }
            return;
        }
        if (scope.kind === "blockgroup") {
            const bg = (scope.ctx as { bg: BlockGroupCtx }).bg;
            const keyframe = bg.refs === 0;
            for (const f of bg.frames) {
                this.emitSamples(f.trackNumber, f.relTsMs, f.frameData, keyframe, bg.durationMs ?? undefined);
            }
            return;
        }
        // video/audio/cluster 的收口无需动作（值已随子元素结算）
    }

    /** 块 → 样本发射（含 seek 门限丢弃、视频 DTS 合成、annexb 转换）。 */
    private emitSamples(
        trackNumber: number,
        relTsMs: number,
        frames: Uint8Array[],
        isKeyframe: boolean,
        blockDurationMs?: number,
    ): void {
        const st = this.trackMap.get(trackNumber);
        if (!st || !st.info) return; // 未知轨/不支持编码：静默丢弃
        const absMs = this.clusterBaseMs + relTsMs;
        const frameDurMs =
            st.kind === "video"
                ? st.defaultDurationNs / 1e6
                : audioFrameDurationMs(st.info.codec, st.info.sampleRate ?? 48000);
        // seek 门限：整块低于门限（末帧都早于目标）直接丢弃；跨门限的块保留（首帧可能早于
        // 目标，由 remuxer 首关键帧锚定语义吸收——锚点即首个实际发出的样本）
        if (this.seekGateMs > 0 && absMs + frames.length * frameDurMs <= this.seekGateMs) return;
        const laceDurMs = blockDurationMs ?? frameDurMs;
        const samples: DemuxedSample[] = [];
        for (let i = 0; i < frames.length; i++) {
            const f = frames[i];
            if (f.length === 0) continue;
            const tMs = absMs + i * laceDurMs;
            if (st.kind === "video") {
                const pts = Math.round(tMs * 90);
                const dts = Math.round(this.videoDtsAcc);
                this.videoDtsAcc += (VIDEO_TIMESCALE * st.defaultDurationNs) / 1e9;
                const data = st.annexb ? this.convertAnnexBVideo(st, f) : f;
                if (!data) continue;
                samples.push({ trackId: st.trackNumber, kind: "video", data, dts, pts, cts: pts - dts, isKeyframe });
            } else {
                const timescale = st.info.timescale;
                const dts = Math.round((tMs * timescale) / 1000);
                samples.push({
                    trackId: st.trackNumber,
                    kind: "audio",
                    data: f,
                    dts,
                    pts: dts,
                    cts: 0,
                    isKeyframe: true,
                });
            }
        }
        if (samples.length > 0) this.callbacks.onSamples?.(samples);
    }

    /** annexb 兜底：现场收集参数集（收齐补发 TrackInfo），样本转长度前缀。 */
    private convertAnnexBVideo(st: MkTrackState, frame: Uint8Array): Uint8Array | null {
        if (st.codecId === "V_MPEGH/ISO/HEVC") {
            const reader = new HevcAnnexBReader(frame);
            const vcl: Uint8Array[] = [];
            let configChanged = false;
            let nalu: ReturnType<HevcAnnexBReader["readNalu"]>;
            while ((nalu = reader.readNalu()) !== null) {
                switch (nalu.type) {
                    case HevcNaluType.Vps:
                        st.annexbVps = new Uint8Array(nalu.data);
                        configChanged = true;
                        break;
                    case HevcNaluType.Sps:
                        st.annexbSps = new Uint8Array(nalu.data);
                        configChanged = true;
                        break;
                    case HevcNaluType.Pps:
                        st.annexbPps = new Uint8Array(nalu.data);
                        configChanged = true;
                        break;
                    default:
                        break;
                }
                if (nalu.type < 32) vcl.push(new Uint8Array(nalu.data));
            }
            if (configChanged && st.annexbVps && st.annexbSps && st.annexbPps && !st.info) {
                const sps = parseHevcSps(st.annexbSps);
                st.info = {
                    id: st.trackNumber,
                    kind: "video",
                    codec: sps?.codec ?? "hvc1",
                    timescale: VIDEO_TIMESCALE,
                    width: sps?.size.width || st.width,
                    height: sps?.size.height || st.height,
                    scanType: sps?.interlaced ? "interlaced" : "progressive",
                    codecPrivate: buildHvcC(st.annexbVps, st.annexbSps, st.annexbPps),
                };
                st.lengthSize = 4;
                this.publishTracks();
            }
            if (!st.info || vcl.length === 0) return null;
            return avccFromNalus(vcl, st.lengthSize || 4);
        }
        // AVC
        const nalus = splitAnnexB(frame);
        const vcl: Uint8Array[] = [];
        let configChanged = false;
        for (const n of nalus) {
            const t = nalUnitType(n);
            if (t === NAL_TYPE_SPS) {
                st.annexbSps = n;
                configChanged = true;
            } else if (t === NAL_TYPE_PPS) {
                st.annexbPps = n;
                configChanged = true;
            } else if (t !== 6 && t !== 9) {
                vcl.push(n);
            }
        }
        if (configChanged && st.annexbSps && st.annexbPps && !st.info) {
            st.info = {
                id: st.trackNumber,
                kind: "video",
                codec: "avc1",
                timescale: VIDEO_TIMESCALE,
                width: st.width,
                height: st.height,
                scanType: "progressive",
                codecPrivate: buildAvcC([st.annexbSps], [st.annexbPps]),
            };
            st.lengthSize = 4;
            this.publishTracks();
        }
        if (!st.info || vcl.length === 0) return null;
        return avccFromNalus(vcl, st.lengthSize || 4);
    }

    /** cluster 作用域的子元素集合。 */
    private isClusterChild(id: number): boolean {
        return (
            id === ID_CLUSTER_TIMECODE ||
            id === ID_SIMPLE_BLOCK ||
            id === ID_BLOCK_GROUP ||
            id === ID_PREV_SIZE ||
            id === ID_VOID ||
            id === ID_CRC32
        );
    }

    /**
     * 小元素（数值/字节串）收集：payload 在缓冲内即处理，否则登记 incomplete 等到齐。
     */
    private collectOrWait(
        id: number,
        size: number,
        payloadStartAbs: number,
        onPayload: (payload: Uint8Array) => void,
    ): void {
        if (size < 0) return this.fail(`元素 0x${id.toString(16)} 为未知长度`);
        const endAbs = payloadStartAbs + size;
        if (this.base + this.buf.length >= endAbs) {
            const start = payloadStartAbs - this.base;
            onPayload(this.buf.subarray(start, start + size));
            this.pos = start + size;
            return;
        }
        this.pos = payloadStartAbs - this.base;
        this.incomplete = { id, size, startAbs: payloadStartAbs, onPayload };
    }

    /** 跳过元素 payload（可选：到齐后执行收口回调——Segment/Info/Tracks 的容器体跳过语义）。 */
    private skipOrWait(
        id: number,
        size: number,
        payloadStartAbs: number,
        onComplete?: () => void,
    ): void {
        if (size < 0) {
            // Segment 的 unknown size 是常态（流式封装），其"容器体"由后续元素自然铺开，
            // 无需真正跳过；其余 unknown size 元素直接报错（真实文件中不存在）。
            if (id === ID_SEGMENT) return;
            return this.fail(`元素 0x${id.toString(16)} 为未知长度（无法跳过）`);
        }
        const endAbs = payloadStartAbs + size;
        if (this.base + this.buf.length >= endAbs) {
            this.pos = payloadStartAbs - this.base + size;
            onComplete?.();
            return;
        }
        this.pos = payloadStartAbs - this.base;
        this.incomplete = { id, size, startAbs: payloadStartAbs, onPayload: onComplete };
    }

    private fail(msg: string): void {
        if (!this.failed) {
            this.failed = true;
            this.callbacks.onError?.(`Matroska demux: ${msg}`);
        }
    }
}

// ---- 辅助 ----

const AAC_SAMPLE_RATES = [
    96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350,
];

/** 音频名义帧长（毫秒）：lace 多帧时的时间推进（软解锚点按样本外推，此值只需量级正确）。 */
function audioFrameDurationMs(codec: string, sampleRate: number): number {
    const sr = sampleRate > 0 ? sampleRate : 48000;
    if (codec.startsWith("mp4a")) return (1024 * 1000) / sr;
    if (codec === "ac3" || codec === "eac3") return (1536 * 1000) / sr;
    return (1152 * 1000) / sr; // mp2/mp3
}

function looksLikeAnnexB(cp: Uint8Array): boolean {
    return (
        (cp[0] === 0 && cp[1] === 0 && cp[2] === 0 && cp[3] === 1) || (cp[0] === 0 && cp[1] === 0 && cp[2] === 1)
    );
}

/** lace 拆分：0=单帧；1=Xiph；2=fixed；3=EBML（计数字节对所有类型都存在）。 */
function splitLaces(body: Uint8Array, lacing: number): Uint8Array[] {
    if (lacing === 0) return [body];
    if (body.length === 0) return [];
    const count = body[0] + 1;
    let o = 1;
    const sizes: number[] = [];
    if (lacing === 1) {
        // Xiph：count-1 个长度字节（0xFF 续延）；末帧长度 = 剩余字节 - 已声明长度之和
        let sum = 0;
        for (let i = 0; i < count - 1; i++) {
            let v = 0;
            let b: number;
            do {
                if (o >= body.length) return [];
                b = body[o++];
                v += b;
            } while (b === 0xff);
            sum += v;
            sizes.push(v);
        }
        const rest = body.length - o;
        if (rest < sum) return [];
        sizes.push(rest - sum);
    } else if (lacing === 2) {
        // fixed
        const rest = body.length - o;
        if (rest % count !== 0) return [];
        for (let i = 0; i < count; i++) sizes.push(rest / count);
    } else {
        // EBML：首帧 VINT，其后有符号差分
        const first = readVintValue(body, o, false);
        if (!first || first.value < 0) return [];
        sizes.push(first.value);
        o += first.vintLen;
        let prev = first.value;
        for (let i = 1; i < count - 1; i++) {
            const d = readSignedVint(body, o);
            if (!d) return [];
            prev = prev + d.value;
            sizes.push(prev);
            o += d.vintLen;
        }
        if (sizes.reduce((a, b) => a + b, 0) > body.length - o) return [];
        sizes.push(body.length - o - sizes.reduce((a, b) => a + b, 0));
    }
    const frames: Uint8Array[] = [];
    for (const sz of sizes) {
        if (sz < 0 || o + sz > body.length) return [];
        frames.push(body.subarray(o, o + sz));
        o += sz;
    }
    return frames;
}

/** 有符号 VINT（EBML lace 差分）：首比特为符号位展开。 */
function readSignedVint(data: Uint8Array, pos: number): { value: number; vintLen: number } | null {
    const u = readVintValue(data, pos, false);
    if (!u || u.value < 0) return null;
    const bits = 7 * u.vintLen;
    const signBit = 2 ** (bits - 1);
    const value = u.value & signBit ? u.value - 2 ** bits : u.value;
    return { value, vintLen: u.vintLen };
}
