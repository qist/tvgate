/**
 * MPEG-TS demuxer。
 * 依据 ISO/IEC 13818-1 公开规范重新实现，复用 formats/ts、formats/avc、formats/aac 的纯算法。
 * 职责：同步/探包 → PAT/PMT 发现轨道 → 按 PID 重组 PES → 按 stream_type 分派解析器
 * → 产出带 dts/pts/cts/isKeyframe 的样本与 codecPrivate（avcC / esds）。
 * 当前支持 H.264(AnnexB/AVCC) + AAC(ADTS)；H265/AC3/EAC3/MP3 留待后续接入同一分派。
 */

import {
  TS_PACKET_SIZE,
  parseTsPacket,
  parsePat,
  parsePmt,
  parsePesHeader,
  detectPrivateAudioCodec,
} from "../formats/ts";
import {
  splitAnnexB,
  nalUnitType,
  parseSps,
  avccFromNalus,
  buildAvcC,
  buildAvc1CodecString,
  NAL_TYPE_SPS,
  NAL_TYPE_PPS,
  NAL_TYPE_IDR,
} from "../formats/avc";
import { HevcAnnexBReader, buildHvcC, HevcNaluType, type HevcNalu } from "./h265";
import { parseHevcSps } from "./h265-parser";
import { parseAdtsFrame, buildAudioSpecificConfig, buildEsds, aacCodecMimeType, findAdtsSync } from "../formats/aac";
import { AC3Parser, EAC3Parser } from "./ac3";
import { MP3Parser, type MP3Frame } from "./mp3";

export type TrackKind = "video" | "audio";

/** 视频扫描方式：媒体信息徽标据此显示 "1080p" / "1080i"。 */
export type VideoScanType = "progressive" | "interlaced";

export interface TrackInfo {
  id: number;
  kind: TrackKind;
  codec: string;
  timescale: number;
  width?: number;
  height?: number;
  scanType?: VideoScanType;
  channels?: number;
  sampleRate?: number;
  codecPrivate: Uint8Array;
}

export interface DemuxedSample {
  trackId: number;
  kind: TrackKind;
  data: Uint8Array; // video: AVCC(length-prefixed)；audio: 裸 AAC 帧（已剥 ADTS 头）
  dts: number; // 单位：该轨 timescale
  pts: number;
  cts: number; // pts - dts
  isKeyframe: boolean;
}

export interface TsDemuxerCallbacks {
  onTracks?(tracks: TrackInfo[]): void;
  onSamples?(samples: DemuxedSample[]): void;
  onError?(message: string): void;
  /** PMT 解析后立即上报声明的轨道（video/进 MSE 的 AAC），供下游提前建 SourceBuffer。 */
  onStreamLayout?(layout: { video: boolean; mseAudio: boolean; softAudio?: boolean }): void;
}

// stream_type（ISO/IEC 13818-1 Table 2-34）
const STREAM_TYPE_H264 = 0x1b;
const STREAM_TYPE_HEVC = 0x24;
const STREAM_TYPE_AAC = 0x0f;
const STREAM_TYPE_AC3 = 0x81;
const STREAM_TYPE_EAC3 = 0x87;
const STREAM_TYPE_MPEG1_AUDIO = 0x03; // MP1/2/3
const STREAM_TYPE_MPEG2_AUDIO = 0x04; // MP2
const STREAM_TYPE_PRIVATE = 0x06; // PES private（DVB 常见承载 AC-3/EAC-3，靠 descriptor 识别）

/** 走软解通道的音频 stream_type → 初始 codec 标记。 */
const PASSTHROUGH_AUDIO_CODECS: Record<number, string> = {
  [STREAM_TYPE_AC3]: "ac3",
  [STREAM_TYPE_EAC3]: "eac3",
  [STREAM_TYPE_MPEG2_AUDIO]: "mp2",
  [STREAM_TYPE_MPEG1_AUDIO]: "mp3",
};

/**
 * 从 MPEG 音频帧头推断 Layer（I/II/III）。
 * stream_type 0x03(MPEG-1)/0x04(MPEG-2) **不保证**内容层：0x03 也常承载 Layer II(MP2)。
 * 只按 stream_type 标码会把 MP2 误标成 mp3 → 路由到 mp3 解码器 → 解不出声。返回 null 表示未见到有效帧头。
 *
 * 注意：**必须校验整个帧头**。压缩载荷（尤其 AC-3）里随机出现 `0xFF 0xEx` 的概率约 1.6%/128B，
 * 只看同步字会把 AC-3 误判成 MP2 → 整轨路由到 MP2 解码器 → 解出垃圾 PCM（听感为持续杂音）。
 */
function detectMpegAudioLayer(data: Uint8Array): 1 | 2 | 3 | null {
  const max = Math.min(data.length - 4, 128);
  for (let i = 0; i < max; i++) {
    // 同步字：11 位 1（0xFF + 0xE0 高 3 位）
    if (data[i] !== 0xff || (data[i + 1] & 0xe0) !== 0xe0) continue;
    const layerBits = (data[i + 1] >> 1) & 0x03;
    if (layerBits === 0) continue; // 00 = reserved
    // 整帧头校验：bitrate_index（第 3 字节高 4 位）与 sampling_rate_index（次 2 位）都必须合法，
    // 否则只是随机出现的 0xFF 0xEx，不能据此定码。
    const bitrateIdx = (data[i + 2] >> 4) & 0x0f;
    const sampleRateIdx = (data[i + 2] >> 2) & 0x03;
    if (bitrateIdx === 0 || bitrateIdx === 0x0f) continue; // free / bad
    if (sampleRateIdx === 3) continue; // reserved
    return layerBits === 1 ? 3 : layerBits === 2 ? 2 : 1;
  }
  return null;
}

/**
 * Dolby 同步字搜索窗口（字节）。须不小于 AC-3 的最大帧长，否则"自帧中间开始的 PES"
 * 在窗口内找不到下一个帧头会漏判：A/52 最高码率（640 kbps @ 32 kHz，A/52 表 5.18）
 * 一帧为 3840 字节，故取 4096。
 */
const DOLBY_SYNC_SCAN_BYTES = 4096;

/**
 * 判定私有流载荷实际承载的 Dolby 编码（AC-3 / E-AC-3）。
 *
 * 依据公开规范独立实现：
 *  - AC-3 与 E-AC-3 的同步字同为 16 位大端 `0x0B77`
 *    （ATSC A/52 §5.4.1.1；ETSI TS 102 366 §F.4.2）；
 *  - 同步字之后第 5 字节的高 5 位是 `bsid`（bit stream identification）：
 *    标准 AC-3 取 1~8，E-AC-3（DD+）取 11~16，9/10 为保留值。
 *
 * PES 载荷可能自帧中间开始，故取窗口内**第一个**同步字作帧头判定；
 * 窗口内未见同步字返回 null，交由后续 PES 继续判定。
 */
function probeDolbyCodec(data: Uint8Array): "ac3" | "eac3" | null {
  const limit = Math.min(data.length, DOLBY_SYNC_SCAN_BYTES) - 6;
  for (let i = 0; i < limit; i++) {
    if (data[i] !== 0x0b || data[i + 1] !== 0x77) continue;
    const bsid = (data[i + 5] >> 3) & 0x1f;
    if (bsid >= 11) return "eac3";
    if (bsid <= 8) return "ac3";
    // 9/10 为保留值：大概率是随机字节命中同步字，继续向后找。
  }
  return null;
}

/** 按内容去重收集参数集（数量极少，线性比较即可；上限 8 组防异常流膨胀）。 */
function addParamSet(list: Uint8Array[] | undefined, nal: Uint8Array): Uint8Array[] {
  const out = list ?? [];
  for (const existing of out) {
    if (existing.length === nal.length) {
      let same = true;
      for (let i = 0; i < nal.length; i++) {
        if (existing[i] !== nal[i]) {
          same = false;
          break;
        }
      }
      if (same) return out;
    }
  }
  if (out.length >= 8) return out;
  return [...out, nal];
}

const VIDEO_TIMESCALE = 90000;
const PTS_MODULUS = 2 ** 33;
const AAC_SAMPLES_PER_FRAME = 1024;

/** AAC 跨 PES 残帧上限（字节）：超过说明长时间找不到帧头（流不同步），丢弃避免无界增长。 */
const AAC_PENDING_LIMIT = 32 * 1024;

interface PesBuffer {
  chunks: Uint8Array[];
  bytes: number;
}

interface TrackState {
  id: number;
  pid: number;
  kind: TrackKind;
  streamType: number;
  timescale: number;
  codec?: string; // 供发布时标记（如 mp4a.40.2 / ac3 / eac3 / mp2 / mp3）
  /** MPEG-1/2 音频经首帧 Layer 嗅探后才最终定码（防止把 MP2 当 mp3）。 */
  codecFinalized?: boolean;
  // 视频
  sps?: Uint8Array;
  pps?: Uint8Array;
  /** 已见到的全部 SPS/PPS（按内容去重）：avcC 必须收全 —— 多 PPS 交替的流
   *  （不同 pps_id 对应不同 slice 类型/场帧编码）若只写最后一个，
   *  解码器会缺另一个 PPS → 引用它的关键帧送包失败（MEDIA_ERR_DECODE）。 */
  spsAll?: Uint8Array[];
  ppsAll?: Uint8Array[];
  vps?: Uint8Array; // HEVC
  width?: number;
  height?: number;
  scanType?: VideoScanType;
  // 音频
  channels?: number;
  sampleRate?: number;
  codecPrivate?: Uint8Array;
  published: boolean;
  lastPts?: number; // 解绕后的 90kHz PTS
  lastDts?: number; // 解绕后的 90kHz DTS
  audioNextDts?: number; // 音频名义帧长累加（单位：音频 timescale）
  /** 跨 PES 的 Dolby 半帧残尾：与下一 PES 拼接后再逐帧切分。 */
  ac3Incomplete?: Uint8Array;
  /** MP2/mp3 跨 PES 残帧（上一个 payload 的半帧）。 */
  mp2Incomplete?: Uint8Array;
  /** AAC 跨 PES 残帧：PES 边界与 ADTS 帧边界无关，半帧要留到下一段拼。 */
  aacPending?: Uint8Array;
  /** 上一完整 MPEG 音频帧的 PTS 与帧长（90kHz）：payload 头部被半帧占用时接续 PTS 基准。 */
  audioLastFramePts90?: number;
  audioLastFrameDur90?: number;
}

export class TsDemuxer {
  private buffer: Uint8Array<ArrayBufferLike> = new Uint8Array(0);
  private packetSize = TS_PACKET_SIZE;
  private probed = false;
  private nextTrackId = 1;

  private tracks = new Map<number, TrackState>();
  private pesBuffers = new Map<number, PesBuffer>();
  private pmtPids = new Set<number>();

  constructor(private readonly callbacks: TsDemuxerCallbacks = {}) {}

  /** 推入一段 TS 字节流（可任意切分，内部自行缓冲与同步）。 */
  push(chunk: Uint8Array): void {
    if (!this.probed) {
      this.buffer = concatBytes(this.buffer, chunk);
      const probe = probeTsPacketSize(this.buffer);
      if (!probe) {
        if (this.buffer.length > 4096) this.buffer = this.buffer.subarray(this.buffer.length - 1024);
        return;
      }
      this.packetSize = probe.packetSize;
      this.probed = true;
      // 从首个 sync 处开始消费
      this.consume(probe.syncOffset);
      return;
    }
    this.buffer = concatBytes(this.buffer, chunk);
    this.consume(0);
  }

  private consume(startOffset: number): void {
    let offset = startOffset;
    // 逐包扫描；不足一包时残留等下次
    while (offset + this.packetSize <= this.buffer.length) {
      if (this.buffer[offset] !== 0x47) {
        // 失同步：重新探测
        this.probed = false;
        this.buffer = this.buffer.subarray(offset);
        this.pesBuffers.clear();
        return;
      }
      const pkt = parseTsPacket(this.buffer, offset);
      if (pkt) this.handlePacket(pkt.pid, pkt.payloadUnitStart, pkt.payload);
      offset += this.packetSize;
    }
    this.buffer = this.buffer.subarray(offset);
  }

  private handlePacket(pid: number, payloadUnitStart: boolean, payload: Uint8Array): void {
    if (payload.length === 0) return;

    if (pid === 0) {
      // PAT：登记 PMT PID
      if (!payloadUnitStart) return;
      const pointer = payload[0];
      const section = payload.subarray(1 + pointer);
      for (const e of parsePat(section)) this.pmtPids.add(e.pmtPid);
      return;
    }

    if (this.pmtPids.has(pid)) {
      // PMT：按 stream_type 建轨
      if (!payloadUnitStart) return;
      const pointer = payload[0];
      const section = payload.subarray(1 + pointer);
      if (section.length === 0 || section[0] !== 0x02) return;
      const pmt = parsePmt(section);
      let video = false;
      let mseAudio = false;
      // 需软解的音轨（AC-3/E-AC-3/MP1-3，MSE 不能解码）：pipeline 据此决定是否给 MSE
      // 挂一条静音 AAC 假音轨（C2）。必须在首个 append 前上报（Chromium 锁 SourceBuffer 数量）。
      let softAudio = false;
      for (const s of pmt.streams) {
        let st = s.streamType;
        // DVB 私有流(0x06) 不声明具体编码，按 ES descriptor 判定 Dolby：Registration
        // Descriptor(0x05) "AC-3"/"EC-3"、ATSC AC-3(0x82) / ETSI AC-3(0x6A)、EAC3(0x7A/0x7D)。
        // 描述符缺失时**不能丢弃该轨**（源站普遍不写）：registerTrack 会注册为待定轨，
        // 由首个 PES 的内容嗅探定码 —— 否则主音轨整条消失（例：只剩并存的 MP2 备轨被播放）。
        if (st === STREAM_TYPE_PRIVATE) {
          const dolby = detectPrivateAudioCodec(s.esInfo);
          if (dolby === "ac3") st = STREAM_TYPE_AC3;
          else if (dolby === "eac3") st = STREAM_TYPE_EAC3;
        }
        this.registerTrack(s.pid, st);
        // 注意：必须同时认 H.264(0x1b) 与 HEVC(0x24)。曾漏掉 0x24 —— HEVC 流上报
        // {video:false} → 后端 expectsVideo=false → maybeReleaseHold 直接 return →
        // 启动 hold 永不释放，所有 append 排队到 12s 兜底（起播卡死/报 updating reset）。
        if (st === STREAM_TYPE_H264 || st === STREAM_TYPE_HEVC) video = true;
        else if (st === STREAM_TYPE_AAC) mseAudio = true;
        else if (
          st === STREAM_TYPE_AC3 ||
          st === STREAM_TYPE_EAC3 ||
          st === STREAM_TYPE_MPEG1_AUDIO ||
          st === STREAM_TYPE_MPEG2_AUDIO
        ) {
          softAudio = true;
        }
      }
      if (this.tracks.size > 0) this.publishTracks();
      // PMT 一声明轨道布局就上报：MSE 侧需在任何 init append 前建齐缓冲（Chromium 锁 SourceBuffer 数量）
      this.callbacks.onStreamLayout?.({ video, mseAudio, softAudio });
      return;
    }

    const track = this.trackByPid(pid);
    if (!track) return;

    // PES 重组
    if (payloadUnitStart) {
      const prev = this.pesBuffers.get(pid);
      if (prev) {
        const pes = concatChunks(prev.chunks);
        this.pesBuffers.delete(pid);
        this.handlePes(track, pes);
      }
      this.pesBuffers.set(pid, { chunks: [payload], bytes: payload.length });
    } else {
      const buf = this.pesBuffers.get(pid);
      if (buf) {
        buf.chunks.push(payload);
        buf.bytes += payload.length;
      }
    }
  }

  private trackByPid(pid: number): TrackState | undefined {
    for (const t of this.tracks.values()) if (t.pid === pid) return t;
    return undefined;
  }

  private registerTrack(pid: number, streamType: number): void {
    if (this.trackByPid(pid)) return;
    const passthroughCodec = PASSTHROUGH_AUDIO_CODECS[streamType];
    if (streamType === STREAM_TYPE_H264) {
      this.tracks.set(pid, {
        id: this.nextTrackId++,
        pid,
        kind: "video",
        streamType,
        codec: "avc1",
        timescale: VIDEO_TIMESCALE,
        published: false,
      });
    } else if (streamType === STREAM_TYPE_HEVC) {
      this.tracks.set(pid, {
        id: this.nextTrackId++,
        pid,
        kind: "video",
        streamType,
        codec: "hvc1",
        timescale: VIDEO_TIMESCALE,
        published: false,
      });
    } else if (streamType === STREAM_TYPE_AAC) {
      this.tracks.set(pid, {
        id: this.nextTrackId++,
        pid,
        kind: "audio",
        streamType,
        codec: "mp4a.40.2",
        timescale: 48000, // 由首个 ADTS 帧修正
        published: false,
      });
    } else if (passthroughCodec) {
      const isMpegAudio =
        streamType === STREAM_TYPE_MPEG1_AUDIO || streamType === STREAM_TYPE_MPEG2_AUDIO;
      this.tracks.set(pid, {
        id: this.nextTrackId++,
        pid,
        kind: "audio",
        streamType,
        codec: passthroughCodec,
        // MPEG-1/2 音频层须由首帧帧头嗅探确定；ac3/eac3 直接可用
        codecFinalized: !isMpegAudio,
        // 帧内无采样率声明，直接用 90kHz（软解 PCM 时间同 MSE 时间轴换算由消费侧完成）
        timescale: VIDEO_TIMESCALE,
        published: false,
      });
    } else if (streamType === STREAM_TYPE_PRIVATE) {
      // DVB 私有流(0x06) 而 ES descriptor 未标识 Dolby 编码（源站普遍不写描述符：
      // ffprobe 也是靠内容同步字 0x0B77 才认出 AC-3 的）：注册为"待定音频轨"，
      // 由首个 PES 的内容嗅探（probeDolbyCodec）定码。
      // 若此处直接丢弃，主音轨会整条消失 —— 例如只并存的 MP2 备轨被播放、徽标显示 MP2。
      this.tracks.set(pid, {
        id: this.nextTrackId++,
        pid,
        kind: "audio",
        streamType,
        codecFinalized: false,
        timescale: VIDEO_TIMESCALE,
        published: false,
      });
    }
  }

  /**
   * 软解音轨的声道数只在首帧才能解析出来（PES/PMT 里没有），而轨道在 PMT 阶段就已发布，
   * 因此这里在声道数首次可知/变化时**重新发布该轨**，让上层把 channelCount 填进媒体信息
   * （徽标显示"立体声/5.1"）。remuxer.addTrack 幂等、setSilentAudioTrack 只认首次，
   * 重复发布无副作用。
   */
  private syncAudioChannels(track: TrackState, channels: number | undefined): void {
    if (!channels || channels <= 0 || track.channels === channels) {
      return;
    }
    track.channels = channels;
    track.published = false;
    this.publishTracks();
  }

  private publishTracks(): void {
    const infos: TrackInfo[] = [];
    for (const t of this.tracks.values()) {
      if (t.published) continue;
      // 视频须先拿到 SPS/PPS；AAC 须先由 ADTS 首帧确定采样率（构造 esds）；
      // 透传音频（ac3/eac3/mp2/mp3）注册即可发布，帧处理交给 WASM 解码桥。
      const codec = t.codec ?? (t.kind === "video" ? "avc1" : "mp4a.40.2");
      // 视频须先拿到 SPS/PPS（HEVC 还须 VPS）；AAC 须先由 ADTS 首帧确定采样率（构造 esds）；
      // 透传音频（ac3/eac3/mp2/mp3）注册即可发布，帧处理交给 WASM 解码桥。
      if (t.kind === "video") {
        if (t.streamType === STREAM_TYPE_HEVC) {
          if (!(t.vps && t.sps && t.pps)) continue;
        } else if (!(t.sps && t.pps)) continue;
      }
      // AAC 须等到首帧构造出 esds 才能发布（codec 串会随 AOT 变化，故按 streamType 判定）
      if (t.streamType === STREAM_TYPE_AAC && !t.codecPrivate) continue;
      // MPEG-1/2 音频须等首帧 Layer 嗅探定码后才发布（避免 MP2 被误标 mp3 发布出去）；
      // 私有流(0x06) 同理：descriptor 未标识编码时也必须等首帧内容嗅探定码，
      // 否则会被上面 `t.codec ?? "mp4a.40.2"` 兜底成错误 codec 发布出去。
      const needsContentSniff =
        t.streamType === STREAM_TYPE_MPEG1_AUDIO ||
        t.streamType === STREAM_TYPE_MPEG2_AUDIO ||
        t.streamType === STREAM_TYPE_PRIVATE;
      if (needsContentSniff && !t.codecFinalized) continue;
      infos.push({
        id: t.id,
        kind: t.kind,
        codec,
        timescale: t.timescale,
        width: t.width,
        height: t.height,
        scanType: t.scanType,
        channels: t.channels,
        sampleRate: t.sampleRate,
        codecPrivate: t.codecPrivate ?? new Uint8Array(0),
      });
      t.published = true;
    }
    if (infos.length > 0) this.callbacks.onTracks?.(infos);
  }

  private handlePes(track: TrackState, pes: Uint8Array): void {
    if (pes.length < 6) return;
    const header = parsePesHeader(pes);
    if (!header) return;

    // 33 位 PTS 解绕，得到单调（允许回绕）的 90kHz 时间
    const rawPts = header.pts;
    if (rawPts == null) return;
    const rawDts = header.dts ?? rawPts;
    const pts90 = this.unwrap(rawPts, track.lastPts ?? null);
    const dts90 = this.unwrap(rawDts, track.lastDts ?? null);
    track.lastPts = pts90;
    track.lastDts = dts90;

    // PES_packet_length>0 时按声明长度截断，避免把 TS 包尾填充算进帧（video 通常为 0 = 不限定）
    const pesPacketLength = (pes[4] << 8) | pes[5];
    const payloadEnd =
      pesPacketLength > 0 ? Math.min(6 + pesPacketLength, pes.length) : pes.length;
    const payload = pes.subarray(header.payloadStart, payloadEnd);
    if (payload.length === 0) return;

    if (track.kind === "video") {
      if (track.streamType === STREAM_TYPE_HEVC) {
        this.handleVideoHevcPes(track, payload, dts90, pts90);
      } else {
        this.handleVideoPes(track, payload, dts90, pts90);
      }
    } else if (track.streamType === STREAM_TYPE_AAC) {
      this.handleAacPes(track, payload, pts90);
    } else {
      this.handlePassthroughAudioPes(track, payload, pts90);
    }
  }

  private handleVideoPes(track: TrackState, payload: Uint8Array, dts90: number, pts90: number): void {
    const nalus = splitAnnexB(payload);
    if (nalus.length === 0) return;

    let isKeyframe = false;
    let configChanged = false;
    for (const n of nalus) {
      const type = nalUnitType(n);
      if (type === NAL_TYPE_SPS) {
        track.sps = n;
        track.spsAll = addParamSet(track.spsAll, n);
        configChanged = true;
        const info = parseSps(n);
        if (info) {
          track.width = info.width;
          track.height = info.height;
          // frame_mbs_only_flag=0 → 允许场编码（MBAFF/场编码，1080i 类内容）
          track.scanType = info.frameMbsOnly ? "progressive" : "interlaced";
          // MSE 需要完整 avc1.PPCCLL（裸 "avc1" 会被 isTypeSupported 拒绝）
          track.codec = buildAvc1CodecString(info);
        }
      } else if (type === NAL_TYPE_PPS) {
        track.pps = n;
        track.ppsAll = addParamSet(track.ppsAll, n);
        configChanged = true;
      } else if (type === NAL_TYPE_IDR) {
        isKeyframe = true;
      }
    }

    if (configChanged && track.sps && track.pps) {
      // 收全所有已见参数集：多 PPS 交替的流（不同 pps_id 对应不同 slice/场帧编码）
      // 若只写最后一个，解码器会缺另一个 PPS，引用它的关键帧送包即失败 → MEDIA_ERR_DECODE。
      track.codecPrivate = buildAvcC(track.spsAll ?? [track.sps], track.ppsAll ?? [track.pps]);
    }
    if (!track.published) this.publishTracks();
    if (!track.published) return; // 尚无可用的 codecPrivate，等下个 SPS

    // 参数集已随 codecPrivate(avcC) 交付；样本只保留 VCL（去 SEI/SPS/PPS/AUD），再转 AVCC 长度前缀
    const vcl = nalus.filter((n) => {
      const t = nalUnitType(n);
      return t !== 6 && t !== NAL_TYPE_SPS && t !== NAL_TYPE_PPS && t !== 9;
    });
    if (vcl.length === 0) return;
    const data = avccFromNalus(vcl);
    const cts = pts90 - dts90;
    this.callbacks.onSamples?.([
      {
        trackId: track.id,
        kind: "video",
        data,
        dts: dts90,
        pts: pts90,
        cts,
        isKeyframe,
      },
    ]);
  }

  /** HEVC（0x24）视频 PES：AnnexB NAL → VPS/SPS/PPS 收集 + hvcC + VCL 样本（复用 avcc length-prefixed）。 */
  private handleVideoHevcPes(track: TrackState, payload: Uint8Array, dts90: number, pts90: number): void {
    const parser = new HevcAnnexBReader(payload);
    let configChanged = false;
    let isKeyframe = false;
    const vcl: Uint8Array[] = [];
    let nalu: HevcNalu | null;
    while ((nalu = parser.readNalu()) !== null) {
      const type = nalu.type;
      switch (type) {
        case HevcNaluType.Vps:
          track.vps = new Uint8Array(nalu.data);
          configChanged = true;
          break;
        case HevcNaluType.Sps: {
          track.sps = new Uint8Array(nalu.data);
          configChanged = true;
          const info = parseHevcSps(nalu.data);
          track.width = info.size.width || track.width;
          track.height = info.size.height || track.height;
          // VUI field_seq_flag 或 PTL interlaced_source 约束位 → 隔行内容
          track.scanType = info.interlaced ? "interlaced" : "progressive";
          // MSE 需要完整的 hvc1.PP.1.Lx.B0 串（裸 "hvc1" 会被 isTypeSupported 拒绝）。
          track.codec = info.codec || "hvc1";
          break;
        }
        case HevcNaluType.Pps:
          track.pps = new Uint8Array(nalu.data);
          configChanged = true;
          break;
        case HevcNaluType.IdrWRadl:
        case HevcNaluType.IdrNLp:
        case HevcNaluType.Cra:
          isKeyframe = true;
          break;
        default:
          break;
      }
      // 只保留 VCL（type < 32 为 slice）；参数集/AUD/SEI 等不进样本。
      if (type < 32) {
        vcl.push(new Uint8Array(nalu.data));
      }
    }

    if (configChanged && track.vps && track.sps && track.pps) {
      // 组装完整 hvcC box（含 box 头，mp4-generator 直接塞入 hvc1 sample entry）。
      track.codecPrivate = buildHvcC(track.vps, track.sps, track.pps);
    }
    if (!track.published) this.publishTracks();
    if (!track.published) return; // 尚无可用的 hvcC，等下个参数集
    if (vcl.length === 0) return;
    const data = avccFromNalus(vcl); // 4B length-prefixed（HEVC 样本与 AVCC 同格式）
    const cts = pts90 - dts90;
    this.callbacks.onSamples?.([
      { trackId: track.id, kind: "video", data, dts: dts90, pts: pts90, cts, isKeyframe },
    ]);
  }

  /** 透传音频（ac3/eac3/mp2/mp3）。
   *  AC-3/E-AC-3/MP2/MP3：**一律逐帧切分**（每帧带连续推导的 PTS），纠正源流 PTS 重叠；
   *  跨 PES 半帧由各解析器的 carry 兜底，保证送进解码器的始终是完整帧。 */
  private handlePassthroughAudioPes(track: TrackState, payload: Uint8Array, pts90: number): void {
    // 定码（一次性）：
    //  1) PMT 已定 ac3/eac3（0x81/0x87，或 0x06 且 ES descriptor 已标识）时**直接信任**，
    //     不再做 MPEG 层嗅探 —— AC-3 压缩载荷里随机出现的 `0xFF 0xEx` 会被误判成 MP2，
    //     整轨随之路由到 MP2 解码器解出垃圾 PCM（持续杂音，界面也显示成 MP2）。
    //  2) 容器声明不保证内容（0x03/0x04 与未标识的 0x06 都常见）：按内容定码 ——
    //     先按 Dolby 同步字与 bsid 判 AC-3/E-AC-3，再退到 MPEG 帧头定 Layer。
    if (!track.codecFinalized) {
      if (track.codec === "ac3" || track.codec === "eac3") {
        track.codecFinalized = true;
      } else {
        const dolby = probeDolbyCodec(payload);
        if (dolby) {
          track.codec = dolby;
          track.codecFinalized = true;
        } else {
          const layer = detectMpegAudioLayer(payload);
          if (layer === null) return; // 尚未见到有效帧头，等后续 PES
          track.codec = layer === 3 ? "mp3" : "mp2"; // Layer I/II → mp2，Layer III → mp3
          track.codecFinalized = true;
        }
      }
      this.publishTracks();
      if (!track.published) return;
    }

    const codec = track.codec;
    if (codec === "ac3" || codec === "eac3") {
      this.handleDolbyPes(track, payload, pts90, codec === "eac3");
      return;
    }

    // mp2/mp3：与 AC-3/E-AC-3 同一模型 —— 在 demuxer 内逐帧切分、逐帧连续推导 PTS，
    // 每帧带自己的 PTS 送解码器；跨 PES 半帧由 mp2Incomplete 兜底。
    this.handleMp2Pes(track, payload, pts90);
  }

  /**
   * MP2/mp3 软解主路径：**按帧切分 + 逐帧 PTS 外推**（与 AC-3/E-AC-3 同一模型）。
   *
   * - 帧长由帧头给出，跨 PES 的半帧由 `mp2Incomplete` 兜底，保证进解码器的始终是完整帧；
   * - PES PTS 只作基准；payload 头部被上一段半帧占用时按「上一帧 PTS + 上一帧时长」接续，
   *   避免把 PES PTS 误当成当前帧起点（否则整段逐帧标签前移一帧）；
   * - 单帧解析异常只跳过该帧并照常推进 PTS，保持时间轴连续（与 AC-3 一致）。
   */
  private handleMp2Pes(track: TrackState, payload: Uint8Array, pts90: number): void {
    // 跨 PES 半帧兜底：把上一 PES 的残尾拼到本次头部
    let data = payload;
    const carried = !!(track.mp2Incomplete && track.mp2Incomplete.length > 0);
    if (carried && track.mp2Incomplete) {
      const buf = new Uint8Array(track.mp2Incomplete.length + payload.length);
      buf.set(track.mp2Incomplete, 0);
      buf.set(payload, track.mp2Incomplete.length);
      data = buf;
    }

    const parser = new MP3Parser(data);
    const samples: DemuxedSample[] = [];

    // 首帧 PTS 基准：头部被半帧占用时用「上一帧 PTS + 上一帧时长」接续（与 PES PTS 偏差
    // >1ms 时以接续值为准）；否则锚定本 PES PTS。
    let framePts90 = pts90;
    if (carried && track.audioLastFramePts90 !== undefined && track.audioLastFrameDur90 !== undefined) {
      const continued = track.audioLastFramePts90 + track.audioLastFrameDur90;
      if (Math.abs(continued - pts90) > VIDEO_TIMESCALE / 1000) {
        framePts90 = continued;
      }
    }

    const readFrame = (): MP3Frame | null => {
      try {
        return parser.readNextFrame();
      } catch {
        // 畸形帧导致解析异常：跳过该帧，时间轴照常推进
        return null;
      }
    };

    let frame = readFrame();
    while (frame) {
      this.syncAudioChannels(track, frame.channelCount);
      const dur90 = (frame.samplesPerFrame / frame.samplingFrequency) * VIDEO_TIMESCALE;
      samples.push({
        trackId: track.id,
        kind: "audio",
        data: frame.data,
        dts: Math.round(framePts90),
        pts: Math.round(framePts90),
        cts: 0,
        isKeyframe: true,
      });
      track.audioLastFramePts90 = framePts90;
      track.audioLastFrameDur90 = dur90;
      framePts90 += dur90;
      frame = readFrame();
    }

    // getIncompleteData() 无残帧时返回 null —— 必须每次都赋值，否则上次的残帧会被重复拼接
    track.mp2Incomplete = parser.getIncompleteData() ?? undefined;
    if (samples.length > 0) this.callbacks.onSamples?.(samples);
  }

  /**
   * Dolby（AC-3/E-AC-3）逐帧切分 + 连续 PTS 推导 + 跨 PES 半帧兜底。
   * 对应 playback-engine 7d69b1c 修复：整 PES 转发会让单 PES 内多帧落到同一时间
   * （源流 PTS 重叠），导致软解 PCM 时间轴重叠 → AudioContext 卡顿/播放暂停。
   * 这里首帧锚定 PES PTS，其后按名义帧长（AC-3=1536/sr；E-AC-3=256*num_blks/sr）连续累加，
   * 从根上消除重叠。解析异常（畸形帧/跨 PES 半帧）跳过并推进 PTS，保持时间轴连续。
   */
  private handleDolbyPes(track: TrackState, payload: Uint8Array, pts90: number, isEac3: boolean): void {
    // 跨 PES 半帧兜底：把上一 PES 的残尾拼到本次头部
    let data = payload;
    if (track.ac3Incomplete && track.ac3Incomplete.length > 0) {
      const buf = new Uint8Array(track.ac3Incomplete.length + payload.length);
      buf.set(track.ac3Incomplete, 0);
      buf.set(payload, track.ac3Incomplete.length);
      data = buf;
    }

    const samples: DemuxedSample[] = [];
    let framePts90 = pts90; // 首帧锚定 PES PTS；逐帧按名义帧长连续推导

    const pushFrame = (frameData: Uint8Array, sr: number, samplesPerFrame: number): void => {
      const dur90 = (samplesPerFrame / sr) * VIDEO_TIMESCALE; // 帧长（90kHz）
      samples.push({
        trackId: track.id,
        kind: "audio",
        data: frameData,
        dts: Math.round(framePts90),
        pts: Math.round(framePts90),
        cts: 0,
        isKeyframe: true,
      });
      framePts90 += dur90;
    };

    if (isEac3) {
      const parser = new EAC3Parser(data);
      let f = parser.readNextFrame();
      while (f) {
        this.syncAudioChannels(track, f.channels);
        pushFrame(f.data, f.samplingFrequency, 256 * f.numBlks);
        f = parser.readNextFrame();
      }
      track.ac3Incomplete = parser.getIncompleteData() ?? undefined;
    } else {
      const parser = new AC3Parser(data);
      let f = parser.readNextFrame();
      while (f) {
        this.syncAudioChannels(track, f.channels);
        pushFrame(f.data, f.samplingFrequency, 1536);
        f = parser.readNextFrame();
      }
      track.ac3Incomplete = parser.getIncompleteData() ?? undefined;
    }

    if (samples.length > 0) this.callbacks.onSamples?.(samples);
  }

  private handleAacPes(track: TrackState, payload: Uint8Array, pts90: number): void {
    // ADTS 帧边界与 PES 边界**无关**：PES 载荷可能自帧中间开始（上一帧的尾巴）或止于半帧。
    // 因此与 AC3/MP2 一样接残尾 + 扫同步字后再逐帧切；只认"载荷第 0 字节是 ADTS 头"会在
    // 载荷不从帧头开始时整段丢弃 → 整条音轨 0 样本（实测江苏移动系流：有画面没声音）。
    const data = track.aacPending ? concatBytes(track.aacPending, payload) : payload;
    track.aacPending = undefined;
    if (data.length > AAC_PENDING_LIMIT) return; // 久等不到帧头（不同步）：丢弃，避免无界增长
    let offset = findAdtsSync(data);
    if (offset < 0) return;
    const samples: DemuxedSample[] = [];
    while (offset + 7 <= data.length) {
      const frame = parseAdtsFrame(data, offset);
      if (!frame || frame.frameLength < frame.headerLength) break;
      if (offset + frame.frameLength > data.length) {
        // 半帧（PES 载荷极少恰好整除帧长）：留到下一个 PES 拼接，否则每段都丢一帧
        track.aacPending = data.slice(offset);
        break;
      }

      // 首帧确定采样率/声道，构造 esds
      if (track.sampleRate !== frame.sampleRate || !track.codecPrivate) {
        track.sampleRate = frame.sampleRate;
        track.channels = frame.channels;
        track.timescale = frame.sampleRate;
        const asc = buildAudioSpecificConfig(frame.samplingFrequencyIndex, frame.channelConfig, frame.profile);
        track.codecPrivate = buildEsds(asc);
        // codec 串须与 esds 声明的 AOT 一致（Chrome 为 mp4a.40.5），否则 SourceBuffer 建轨出错
        track.codec = aacCodecMimeType(frame.samplingFrequencyIndex, frame.channelConfig);
        this.publishTracks();
      }
      if (track.audioNextDts === undefined) {
        // 首帧 dts 由 PTS 锚定（90kHz → 音频 timescale），其后按名义帧长累加
        track.audioNextDts = Math.round((pts90 * track.timescale) / VIDEO_TIMESCALE);
      }

      const rawFrame = data.subarray(frame.dataStart, offset + frame.frameLength);
      const dts = track.audioNextDts;
      samples.push({
        trackId: track.id,
        kind: "audio",
        data: rawFrame,
        dts,
        pts: dts,
        cts: 0,
        isKeyframe: true, // 音频每帧均可随机访问
      });
      // 名义帧长递增，避免逐帧换算带来的累计舍入漂移
      track.audioNextDts = dts + AAC_SAMPLES_PER_FRAME;

      offset += frame.frameLength;
    }
    if (samples.length > 0) this.callbacks.onSamples?.(samples);
  }

  /** 33 位时间戳解绕（处理 2^33 回绕）。 */
  private unwrap(raw: number, prev: number | null): number {
    if (prev === null) return raw;
    let delta = raw - (prev % PTS_MODULUS);
    if (delta < -PTS_MODULUS / 2) delta += PTS_MODULUS;
    else if (delta > PTS_MODULUS / 2) delta -= PTS_MODULUS;
    return prev + delta;
  }

  reset(): void {
    this.buffer = new Uint8Array(0);
    this.probed = false;
    this.tracks.clear();
    this.pesBuffers.clear();
    this.pmtPids.clear();
    this.nextTrackId = 1;
  }
}

/** 探测 TS 包大小与同步偏移：验证连续 3 包在候选步长下均为 sync byte 0x47。 */
export function probeTsPacketSize(
  data: Uint8Array,
): { packetSize: number; syncOffset: number } | null {
  const candidates = [188, 192, 204, 208];
  for (const size of candidates) {
    for (let offset = 0; offset + size * 3 <= data.length; offset++) {
      if (data[offset] !== 0x47) continue;
      if (data[offset + size] === 0x47 && data[offset + size * 2] === 0x47) {
        return { packetSize: size, syncOffset: offset };
      }
    }
  }
  return null;
}

function concatBytes(a: Uint8Array, b: Uint8Array): Uint8Array {
  const out = new Uint8Array(a.length + b.length);
  out.set(a, 0);
  out.set(b, a.length);
  return out;
}

function concatChunks(chunks: Uint8Array[]): Uint8Array {
  let total = 0;
  for (const c of chunks) total += c.length;
  const out = new Uint8Array(total);
  let o = 0;
  for (const c of chunks) {
    out.set(c, o);
    o += c.length;
  }
  return out;
}
