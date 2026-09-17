/**
 * HTTP-FLV（直播）解封装器。
 *
 * 实现说明：FLV tag 解析 / 时间戳归一化按 media-engine 的输出契约组织，
 * 行为约定与 demux/ts-demuxer.ts 保持一致（首帧归零、时间轴不回退）。
 *
 * 输出协议与 demux/ts-demuxer.ts **完全一致**（onTracks / onSamples / onError / onStreamLayout），
 * 供 pipeline 直接消费：
 *   - 视频：H.264（legacy codecId 7）与 H.265（Enhanced-RTMP：tag 头 bit7 + fourcc hvc1/hev1）
 *     → codecPrivate = avcC / hvcC（**含 box 头**，与 buildAvcC/buildHvcC/buildEsds 对齐），
 *       样本 = length-prefixed（AVCC 风格）VCL NALU。
 *   - 音频：AAC（soundFormat 10）→ codecPrivate = esds，样本 = 裸 AAC 帧（无 ADTS）；
 *           MP3（soundFormat 2）→ 软解轨道（codec "mp3"，timescale 90kHz，逐帧样本）。
 * 时间：FLV tag 时间戳单位为毫秒；首帧归零；回退超 500ms 钳制（直播时间轴不允许倒退）。
 */
import { buildEsds, aacCodecMimeType } from "../formats/aac";
import { avccFromNalus, buildAvc1CodecString, parseSps, splitAnnexB } from "../formats/avc";
import { parseHevcSps } from "./h265-parser";
import { MP3Parser } from "./mp3";
import type { DemuxedSample, TrackInfo, TsDemuxerCallbacks, VideoScanType } from "./ts-demuxer";

export interface FLVProbeResult {
  match: boolean;
  needMoreData?: boolean;
}

/** 与 TsDemuxer 同形的回调（结构一致，pipeline 可原样复用同一套回调）。 */
export type FlvDemuxerCallbacks = TsDemuxerCallbacks;

const FLV_TAG_AUDIO = 8;
const FLV_TAG_VIDEO = 9;

const SOUND_FORMAT_MP3 = 2;
const SOUND_FORMAT_AAC = 10;

const AAC_PACKET_SEQUENCE_HEADER = 0;
const AAC_PACKET_RAW = 1;

const VIDEO_CODEC_AVC = 7;

const AVC_PACKET_SEQUENCE_HEADER = 0;
const AVC_PACKET_NALU = 1;
const AVC_PACKET_END_OF_SEQUENCE = 2;

/** Enhanced-RTMP packetType（tag 头 bit7 置位时 data[0] 低 4 位）。 */
const EX_PACKET_SEQUENCE_START = 0;
const EX_PACKET_CODED_FRAMES = 1;
const EX_PACKET_SEQUENCE_END = 2;
const EX_PACKET_CODED_FRAMES_X = 3;

const VIDEO_TIMESCALE = 90000;
/** 残留缓冲上限（超过视为流损坏，丢弃并重新等 FLV 头）。 */
const MAX_REMAINDER_BYTES = 16 << 20;
/** 时间戳回退容忍（毫秒）：超过则钳制为上次值。 */
const TIMESTAMP_ROLLBACK_MS = 500;

const AAC_SAMPLING_FREQUENCIES = [
  96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350,
];

/** HEVC NALU 类型（用于关键帧判定）。 */
const HEVC_NALU_TYPES = { CRA: 21, IDR_W_RADL: 19, IDR_N_LP: 20, BLA_W_LP: 16, BLA_W_RADL: 17 };

/** 组装一个 MP4 box（4 字节长度 + 4 字节类型 + 负载）。codecPrivate 必须自带 box 头。 */
function box(type: string, payload: Uint8Array): Uint8Array {
  const out = new Uint8Array(8 + payload.byteLength);
  const dv = new DataView(out.buffer);
  dv.setUint32(0, out.byteLength);
  for (let i = 0; i < 4; i++) out[4 + i] = type.charCodeAt(i);
  out.set(payload, 8);
  return out;
}

function concat(a: Uint8Array, b: Uint8Array): Uint8Array {
  const out = new Uint8Array(a.byteLength + b.byteLength);
  out.set(a, 0);
  out.set(b, a.byteLength);
  return out;
}

interface VideoState {
  id: number;
  published: boolean;
  codec: string;
  width?: number;
  height?: number;
  scanType?: VideoScanType;
  codecPrivate?: Uint8Array;
  /** avcC/hvcC 声明的 NALU 长度前缀字节数（通常 4）。 */
  naluLengthSize: number;
  /** 首个关键帧之前不发样本（MSE 首段必须 IDR）。 */
  started: boolean;
}

interface AudioState {
  id: number;
  published: boolean;
  codec: string;
  timescale: number;
  soft: boolean;
  sampleRate?: number;
  channels?: number;
  codecPrivate?: Uint8Array;
  /** 软解（MP3）逐帧 dts 累计（90kHz）。 */
  nextDts90?: number;
  /** 跨 tag 的 MP3 半帧残尾。 */
  incomplete?: Uint8Array;
}

export class FlvDemuxer {
  private buffer: Uint8Array | null = null;
  private headerParsed = false;

  private video: VideoState = { id: 1, published: false, codec: "avc1", naluLengthSize: 4, started: false };
  private audio: AudioState = {
    id: 2,
    published: false,
    codec: "mp4a.40.2",
    timescale: 48000,
    soft: false,
  };

  private sawVideo = false;
  private lastLayoutKey = "";
  private timestampBase?: number;
  private lastTimestamp = 0;

  constructor(private readonly callbacks: FlvDemuxerCallbacks) {}

  /** FLV 魔数探测（前 3 字节 "FLV"）。 */
  static probe(data: Uint8Array): FLVProbeResult {
    if (data.byteLength < 3) return { match: false, needMoreData: true };
    return { match: data[0] === 0x46 && data[1] === 0x4c && data[2] === 0x56 };
  }

  /** 新流：清空缓冲与轨道状态（时间基准重新锚定）。 */
  reset(): void {
    this.buffer = null;
    this.headerParsed = false;
    this.video = { id: this.video.id, published: false, codec: "avc1", naluLengthSize: 4, started: false };
    this.audio = { id: this.audio.id, published: false, codec: "mp4a.40.2", timescale: 48000, soft: false };
    this.sawVideo = false;
    this.lastLayoutKey = "";
    this.timestampBase = undefined;
    this.lastTimestamp = 0;
  }

  /** 主入口：喂入任意边界的字节块（与 TsDemuxer.push 对齐）。 */
  push(chunk: Uint8Array): void {
    if (chunk.byteLength === 0) return;
    const buffer = this.accumulate(chunk);
    let offset = 0;

    if (!this.headerParsed) {
      if (buffer.byteLength < 13) {
        this.setRemainder(buffer.subarray(offset));
        return;
      }
      offset = 13; // FLV 头 9 字节 + 首个 PreviousTagSize 4 字节
      this.headerParsed = true;
    }

    while (buffer.byteLength - offset >= 11) {
      const dataSize = (buffer[offset + 1] << 16) | (buffer[offset + 2] << 8) | buffer[offset + 3];
      const total = 11 + dataSize + 4; // tag header + data + PreviousTagSize
      if (buffer.byteLength - offset < total) break;

      const tagType = buffer[offset];
      // FLV tag 时间戳：24 位大端（data[4..6]）+ 扩展 8 位（data[7]）。
      // 不可按小端读（会把时间戳放大 256 倍 → 乱序跳变）。
      const timestamp =
        ((buffer[offset + 7] & 0xff) << 24) |
        (buffer[offset + 4] << 16) |
        (buffer[offset + 5] << 8) |
        buffer[offset + 6];
      const tagData = buffer.subarray(offset + 11, offset + 11 + dataSize);

      try {
        if (tagType === FLV_TAG_AUDIO) {
          this.parseAudioTag(tagData, timestamp);
        } else if (tagType === FLV_TAG_VIDEO) {
          this.sawVideo = true;
          this.parseVideoTag(tagData, timestamp);
        }
        // FLV script tag（onMetaData）无需解析
      } catch (e) {
        this.callbacks.onError?.(`FLV tag 解析失败: ${e instanceof Error ? e.message : String(e)}`);
      }

      offset += total;
    }

    this.setRemainder(buffer.subarray(offset));
  }

  private accumulate(chunk: Uint8Array): Uint8Array {
    const rest = this.buffer;
    this.buffer = null;
    if (!rest || rest.byteLength === 0) return chunk;
    const merged = new Uint8Array(rest.byteLength + chunk.byteLength);
    merged.set(rest, 0);
    merged.set(chunk, rest.byteLength);
    return merged;
  }

  private setRemainder(data: Uint8Array): void {
    if (data.byteLength === 0) {
      this.buffer = null;
      return;
    }
    if (data.byteLength > MAX_REMAINDER_BYTES) {
      this.callbacks.onError?.("FLV 解复用缓冲超过 16MB，丢弃该流");
      this.buffer = null;
      this.headerParsed = false;
      return;
    }
    // 拷贝：subarray 引用 loader 的大缓冲，长期持有会阻止 GC
    const copy = new Uint8Array(data.byteLength);
    copy.set(data);
    this.buffer = copy;
  }

  /** 时间戳归零 + 直播不可倒退（回退 >500ms 钳制）。 */
  private normalizeTimestamp(ms: number): number {
    if (this.timestampBase === undefined) this.timestampBase = ms;
    let out = ms - this.timestampBase;
    if (out < this.lastTimestamp - TIMESTAMP_ROLLBACK_MS) out = this.lastTimestamp;
    this.lastTimestamp = out;
    return out;
  }

  // -------------------------------------------------------------------------
  // 音频 tag
  // -------------------------------------------------------------------------

  private parseAudioTag(data: Uint8Array, tagTs: number): void {
    if (data.byteLength < 1) return;
    const soundFormat = data[0] >>> 4;

    if (soundFormat === SOUND_FORMAT_AAC) {
      if (data.byteLength < 2) return;
      const packetType = data[1];
      const payload = data.subarray(2);
      if (packetType === AAC_PACKET_SEQUENCE_HEADER) {
        this.parseAacSequenceHeader(payload);
        return;
      }
      if (packetType !== AAC_PACKET_RAW || payload.byteLength === 0) return;
      if (!this.audio.published) return; // 等 AudioSpecificConfig
      const ms = this.normalizeTimestamp(tagTs);
      const ts = Math.round((ms * this.audio.timescale) / 1000);
      this.callbacks.onSamples?.([
        { trackId: this.audio.id, kind: "audio", data: payload, dts: ts, pts: ts, cts: 0, isKeyframe: true },
      ]);
      return;
    }

    if (soundFormat === SOUND_FORMAT_MP3) {
      // MP3 无 packet type 字节：负载紧跟在 1 字节音频头之后
      const payload = data.subarray(1);
      if (payload.byteLength === 0) return;
      if (!this.audio.published) {
        this.audio.soft = true;
        this.audio.codec = "mp3";
        this.audio.timescale = VIDEO_TIMESCALE;
        this.publishAudioIfNeeded();
      }
      this.emitMp3Frames(payload, tagTs);
      return;
    }

    this.callbacks.onError?.(`不支持的 FLV 音频 soundFormat: ${soundFormat}`);
  }

  private parseAacSequenceHeader(asc: Uint8Array): void {
    if (asc.byteLength < 2) return;
    const samplingFreqIndex = ((asc[0] & 0x07) << 1) | ((asc[1] & 0x80) >>> 7);
    const channelConfig = (asc[1] & 0x78) >>> 3;
    const sampleRate = AAC_SAMPLING_FREQUENCIES[samplingFreqIndex] ?? 44100;

    this.audio.codec = aacCodecMimeType(samplingFreqIndex, channelConfig || 2);
    this.audio.sampleRate = sampleRate;
    this.audio.channels = channelConfig || 2;
    this.audio.timescale = sampleRate;
    this.audio.soft = false;
    // FLV 的 AAC 序列头本身就是 AudioSpecificConfig（2~N 字节，含 HE-AAC 扩展）→ 直接包 esds
    this.audio.codecPrivate = buildEsds(asc);
    this.publishAudioIfNeeded();
  }

  /** MP3：跨 tag 拼半帧，逐帧发软解样本（timescale 90kHz，dts 按帧长累加）。 */
  private emitMp3Frames(payload: Uint8Array, tagTs: number): void {
    const merged = this.audio.incomplete ? concat(this.audio.incomplete, payload) : payload;
    this.audio.incomplete = undefined;
    const parser = new MP3Parser(merged);
    let nextDts90 = this.audio.nextDts90 ?? Math.round(this.normalizeTimestamp(tagTs) * (VIDEO_TIMESCALE / 1000));
    const samples: DemuxedSample[] = [];
    for (;;) {
      let frame: ReturnType<MP3Parser["readNextFrame"]> = null;
      try {
        frame = parser.readNextFrame();
      } catch {
        frame = null;
      }
      if (!frame) break;
      samples.push({
        trackId: this.audio.id,
        kind: "audio",
        data: frame.data,
        dts: Math.round(nextDts90),
        pts: Math.round(nextDts90),
        cts: 0,
        isKeyframe: true,
      });
      nextDts90 += (frame.samplesPerFrame / frame.samplingFrequency) * VIDEO_TIMESCALE;
    }
    const rest = parser.getIncompleteData();
    if (rest && rest.byteLength > 0) this.audio.incomplete = rest;
    this.audio.nextDts90 = nextDts90;
    if (samples.length) this.callbacks.onSamples?.(samples);
  }

  // -------------------------------------------------------------------------
  // 视频 tag
  // -------------------------------------------------------------------------

  private parseVideoTag(data: Uint8Array, tagTs: number): void {
    if (data.byteLength < 1) return;
    const frameType = data[0] >>> 4;
    const keyframeFromFrameType = frameType === 1 || frameType === 4;

    // Enhanced-RTMP：bit7 = isExHeader，真实编码由 fourcc 决定
    if ((data[0] & 0x80) !== 0) {
      this.parseEnhancedVideoTag(data, tagTs, keyframeFromFrameType);
      return;
    }

    const codecId = data[0] & 0x0f;
    if (codecId !== VIDEO_CODEC_AVC) {
      this.callbacks.onError?.(`不支持的 FLV 视频 codecId: ${codecId}`);
      return;
    }
    if (data.byteLength < 5) return;
    const packetType = data[1];
    // 24 位有符号 composition time（毫秒）
    let compositionTime = (data[2] << 16) | (data[3] << 8) | data[4];
    if (compositionTime >= 0x800000) compositionTime -= 0x1000000;
    const payload = data.subarray(5);

    if (packetType === AVC_PACKET_SEQUENCE_HEADER) {
      this.parseAvcSequenceHeader(payload);
      return;
    }
    if (packetType === AVC_PACKET_END_OF_SEQUENCE) return;
    if (packetType !== AVC_PACKET_NALU || payload.byteLength === 0) return;
    if (!this.video.published) return; // 等 avcC
    this.emitVideoSample(payload, tagTs, compositionTime, keyframeFromFrameType, false);
  }

  /**
   * Enhanced-RTMP（H.265）：tag 头 [frameType|0xF][packetType][fourcc(4)]，随后：
   *   packetType 0 = SequenceStart → HEVCDecoderConfigurationRecord（hvcC）
   *   packetType 1 = CodedFrames  → 3 字节 CTS + length-prefixed NALU
   *   packetType 3 = CodedFramesX → 无 CTS + NALU（规范为 length-prefixed；Annex-B 走兜底）
   */
  private parseEnhancedVideoTag(data: Uint8Array, tagTs: number, keyframeFromFrameType: boolean): void {
    if (data.byteLength < 5) return;
    const packetType = data[0] & 0x0f;
    const fourcc = String.fromCharCode(data[1], data[2], data[3], data[4]);
    if (fourcc !== "hvc1" && fourcc !== "hev1" && fourcc !== "hvc2") {
      this.callbacks.onError?.(`不支持的 FLV Enhanced 视频 fourcc: ${fourcc}`);
      return;
    }
    const payload = data.subarray(5);

    if (packetType === EX_PACKET_SEQUENCE_START) {
      this.parseHevcConfigurationRecord(payload);
      return;
    }
    if (packetType === EX_PACKET_SEQUENCE_END || payload.byteLength === 0) return;
    if (!this.video.published) return;

    if (packetType === EX_PACKET_CODED_FRAMES) {
      if (payload.byteLength < 3) return;
      let compositionTime = (payload[0] << 16) | (payload[1] << 8) | payload[2];
      if (compositionTime >= 0x800000) compositionTime -= 0x1000000;
      this.emitVideoSample(payload.subarray(3), tagTs, compositionTime, keyframeFromFrameType, true);
      return;
    }
    if (packetType === EX_PACKET_CODED_FRAMES_X) {
      const annexB =
        payload.byteLength >= 4 &&
        payload[0] === 0 &&
        payload[1] === 0 &&
        (payload[2] === 1 || (payload[2] === 0 && payload[3] === 1));
      const body = annexB ? avccFromNalus(splitAnnexB(payload), this.video.naluLengthSize) : payload;
      this.emitVideoSample(body, tagTs, 0, keyframeFromFrameType, true);
      return;
    }
    // packetType 4/5（Metadata / MPEG2TSSequenceStart）暂不处理
  }

  /** AVCDecoderConfigurationRecord（FLV AVC 序列头）：codecPrivate 直取 + SPS 解析分辨率。 */
  private parseAvcSequenceHeader(record: Uint8Array): void {
    if (record.byteLength < 7 || record[0] !== 0x01) {
      this.callbacks.onError?.("AVCDecoderConfigurationRecord 格式非法");
      return;
    }
    this.video.naluLengthSize = (record[4] & 0x03) + 1;
    this.video.codec = buildAvc1CodecString({
      profileIdc: record[1],
      constraintFlags: record[2],
      levelIdc: record[3],
    });
    const sps = readAvcRecordNalu(record, 7);
    if (sps) {
      const info = parseSps(sps);
      if (info) {
        this.video.width = info.width;
        this.video.height = info.height;
        // frame_mbs_only_flag=0 → 允许场编码（1080i 类内容）
        this.video.scanType = info.frameMbsOnly ? "progressive" : "interlaced";
      }
    }
    this.video.codecPrivate = box("avcC", record);
    this.publishVideoIfNeeded();
  }

  /** HEVCDecoderConfigurationRecord（Enhanced-RTMP 序列头）：codecPrivate 直取 + SPS 解析。 */
  private parseHevcConfigurationRecord(record: Uint8Array): void {
    if (record.byteLength < 23 || record[0] !== 0x01) {
      this.callbacks.onError?.("HEVCDecoderConfigurationRecord 格式非法");
      return;
    }
    this.video.naluLengthSize = (record[21] & 0x03) + 1;
    const sps = readHevcRecordNalu(record, 33);
    if (sps) {
      const info = parseHevcSps(sps);
      this.video.width = info.size.width || this.video.width;
      this.video.height = info.size.height || this.video.height;
      this.video.scanType = info.interlaced ? "interlaced" : "progressive";
      if (info.codec) {
        this.video.codec = info.codec;
      }
    }
    if (!this.video.codec.startsWith("hvc1")) {
      // 兜底：由记录字节拼 hvc1.PP.1.Lx.B0（MSE 不接受裸 "hvc1"）
      this.video.codec = `hvc1.${record[1] & 0x1f}.1.L${record[12]}.B0`;
    }
    this.video.codecPrivate = box("hvcC", record);
    this.publishVideoIfNeeded();
  }

  /**
   * 视频样本：拆 length-prefixed NALU，去掉参数集/AUD/SEI，只留 VCL；
   * 首个关键帧之前丢弃（MSE 首段必须 IDR/CRA）。
   */
  private emitVideoSample(
    data: Uint8Array,
    tagTs: number,
    compositionTimeMs: number,
    keyframe: boolean,
    hevc: boolean,
  ): void {
    const lengthSize = this.video.naluLengthSize;
    const vcl: Uint8Array[] = [];
    let offset = 0;
    let isKeyframe = keyframe;
    while (offset + lengthSize <= data.byteLength) {
      let naluLength = 0;
      for (let i = 0; i < lengthSize; i++) naluLength = (naluLength << 8) | data[offset + i];
      if (naluLength === 0 || offset + lengthSize + naluLength > data.byteLength) break;
      const nalu = data.subarray(offset + lengthSize, offset + lengthSize + naluLength);
      offset += lengthSize + naluLength;
      if (nalu.byteLength === 0) continue;
      if (hevc) {
        const type = (nalu[0] >> 1) & 0x3f;
        if (
          type === HEVC_NALU_TYPES.IDR_W_RADL ||
          type === HEVC_NALU_TYPES.IDR_N_LP ||
          type === HEVC_NALU_TYPES.CRA ||
          type === HEVC_NALU_TYPES.BLA_W_LP ||
          type === HEVC_NALU_TYPES.BLA_W_RADL
        ) {
          isKeyframe = true;
        }
        if (type < 32) vcl.push(nalu); // 只留 VCL
      } else {
        const type = nalu[0] & 0x1f;
        if (type === 5) isKeyframe = true;
        if (type !== 6 && type !== 7 && type !== 8 && type !== 9) vcl.push(nalu);
      }
    }
    if (!isKeyframe && !this.video.started) return;
    if (vcl.length === 0) return;
    this.video.started = true;

    const ms = this.normalizeTimestamp(tagTs);
    const dts = Math.round(ms * (VIDEO_TIMESCALE / 1000));
    const pts = Math.round((ms + compositionTimeMs) * (VIDEO_TIMESCALE / 1000));
    this.callbacks.onSamples?.([
      {
        trackId: this.video.id,
        kind: "video",
        data: avccFromNalus(vcl, lengthSize),
        dts,
        pts,
        cts: pts - dts,
        isKeyframe,
      },
    ]);
  }

  // -------------------------------------------------------------------------
  // 轨道发布 / 布局上报
  // -------------------------------------------------------------------------

  private publishVideoIfNeeded(): void {
    if (this.video.published || !this.video.codecPrivate) return;
    this.video.published = true;
    this.callbacks.onTracks?.([
      {
        id: this.video.id,
        kind: "video",
        codec: this.video.codec,
        timescale: VIDEO_TIMESCALE,
        width: this.video.width,
        height: this.video.height,
        scanType: this.video.scanType,
        codecPrivate: this.video.codecPrivate,
      } satisfies TrackInfo,
    ]);
    this.emitLayout();
  }

  private publishAudioIfNeeded(): void {
    if (this.audio.published) return;
    if (!this.audio.soft && !this.audio.codecPrivate) return; // AAC 需要 esds
    this.audio.published = true;
    this.callbacks.onTracks?.([
      {
        id: this.audio.id,
        kind: "audio",
        codec: this.audio.codec,
        timescale: this.audio.timescale,
        channels: this.audio.channels,
        sampleRate: this.audio.sampleRate,
        codecPrivate: this.audio.codecPrivate ?? new Uint8Array(0),
      } satisfies TrackInfo,
    ]);
    this.emitLayout();
  }

  /** layout 上报（去重）：video + 进 MSE 的 AAC / 走软解的 MP3。 */
  private emitLayout(): void {
    const layout = {
      video: this.video.published || this.sawVideo,
      mseAudio: this.audio.published && !this.audio.soft,
      softAudio: this.audio.published && this.audio.soft,
    };
    const key = `${layout.video}/${layout.mseAudio}/${layout.softAudio}`;
    if (key === this.lastLayoutKey) return;
    this.lastLayoutKey = key;
    this.callbacks.onStreamLayout?.(layout);
  }
}

/** 从 AVCDecoderConfigurationRecord 取指定 NALU 类型（7=SPS / 8=PPS）的第一条裸 NALU。 */
function readAvcRecordNalu(record: Uint8Array, wantType: number): Uint8Array | null {
  if (record.byteLength < 7) return null;
  let offset = 5;
  const numSps = record[offset++] & 0x1f;
  if (wantType === 7) {
    if (numSps === 0 || offset + 2 > record.byteLength) return null;
    const len = (record[offset] << 8) | record[offset + 1];
    offset += 2;
    return record.subarray(offset, offset + len);
  }
  for (let i = 0; i < numSps; i++) {
    if (offset + 2 > record.byteLength) return null;
    const len = (record[offset] << 8) | record[offset + 1];
    offset += 2 + len;
  }
  if (offset >= record.byteLength) return null;
  const numPps = record[offset++];
  if (numPps === 0 || offset + 2 > record.byteLength) return null;
  const len = (record[offset] << 8) | record[offset + 1];
  offset += 2;
  return record.subarray(offset, offset + len);
}

/** 从 HEVCDecoderConfigurationRecord 的 arrays 里取指定 NALU 类型（32=VPS/33=SPS/34=PPS）。 */
function readHevcRecordNalu(record: Uint8Array, wantType: number): Uint8Array | null {
  if (record.byteLength < 23) return null;
  let offset = 22;
  const numArrays = record[offset++];
  for (let a = 0; a < numArrays; a++) {
    if (offset + 3 > record.byteLength) return null;
    const naluType = record[offset] & 0x3f;
    const numNalus = (record[offset + 1] << 8) | record[offset + 2];
    offset += 3;
    for (let n = 0; n < numNalus; n++) {
      if (offset + 2 > record.byteLength) return null;
      const len = (record[offset] << 8) | record[offset + 1];
      offset += 2;
      if (offset + len > record.byteLength) return null;
      if (naluType === wantType) return record.subarray(offset, offset + len);
      offset += len;
    }
  }
  return null;
}
