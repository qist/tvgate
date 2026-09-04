import Log from "../utils/logger";
import { IllegalStateException } from "../utils/exception";
import { AACFrame, AudioSpecificConfig } from "./aac";
import { AVCDecoderConfigurationRecord, H264NaluAVC1, type H264NaluPayload, H264NaluType } from "./h264";
import type { MPEG4AudioObjectTypes, MPEG4SamplingFrequencyIndex } from "./mpeg4-audio";
import { MPEG4SamplingFrequencies } from "./mpeg4-audio";
import SPSParser from "./sps-parser";
import type {
  OnDataAvailableCallback,
  OnErrorCallback,
  OnPcrCallback,
  OnTrackDiscontinuityCallback,
  OnTrackMetadataCallback,
} from "./ts-demuxer";

export interface FLVProbeResult {
  match: boolean;
  needMoreData?: boolean;
}

const FLV_TAG_AUDIO = 8;
const FLV_TAG_VIDEO = 9;
const FLV_TAG_SCRIPT = 18;

const SOUND_FORMAT_AAC = 10;

const AAC_PACKET_SEQUENCE_HEADER = 0;
const AAC_PACKET_RAW = 1;

const VIDEO_CODEC_AVC = 7;

const AVC_PACKET_SEQUENCE_HEADER = 0;
const AVC_PACKET_NALU = 1;
const AVC_PACKET_END_OF_SEQUENCE = 2;

type AACAudioMetadata = {
  codec: "aac";
  audio_object_type: MPEG4AudioObjectTypes;
  sampling_freq_index: MPEG4SamplingFrequencyIndex;
  sampling_frequency: number;
  channel_config: number;
};

type FLVDemuxerOptions = {
  waitForInitialVideoKeyframe?: boolean;
};

/**
 * HTTP-FLV（直播）解封装器。
 *
 * 与 TSDemuxer 对齐输出协议（onTrackMetadata / onDataAvailable），
 * 供播放引擎的 remux 管线直接消费：
 *  - 视频：H.264 AVCC（length-prefixed NALU）→ annexb units
 *  - 音频：AAC raw（无 ADTS 头）
 * 时序：FLV tag 时间戳单位为毫秒；首帧时间戳归零（normalizeTimestamp）后作为 dts/pts（ms）。
 */
class FLVDemuxer {
  private readonly TAG: string = "FLVDemuxer";

  /** MSE append 合批：tag 数或时间任一达到阈值才向 remuxer 派发一次 */
  private static readonly DISPATCH_TAG_THRESHOLD = 3;
  private static readonly DISPATCH_INTERVAL_MS = 40;

  public onError: OnErrorCallback | null = null;
  public onTrackMetadata: OnTrackMetadataCallback | null = null;
  public onDataAvailable: OnDataAvailableCallback | null = null;
  public onTrackDiscontinuity: OnTrackDiscontinuityCallback | null = null;
  /** FLV 无 PCR；占位以兼容 pipeline 的统一绑定。 */
  public onPcr: OnPcrCallback | null = null;

  // 跨 chunk 累积的未解析数据（只保留不完整头/tag 前缀）
  private buffer_: Uint8Array | null = null;
  private header_parsed_ = false;

  private video_metadata_: {
    sps: H264NaluAVC1 | undefined;
    pps: H264NaluAVC1 | undefined;
    details: Record<string, unknown>;
  } = { sps: undefined, pps: undefined, details: {} };

  private audio_metadata_: AACAudioMetadata = {
    codec: "aac",
    audio_object_type: undefined as unknown as MPEG4AudioObjectTypes,
    sampling_freq_index: undefined as unknown as MPEG4SamplingFrequencyIndex,
    sampling_frequency: undefined as unknown as number,
    channel_config: undefined as unknown as number,
  };

  private has_video_ = false;
  private video_init_segment_dispatched_ = false;
  private audio_init_segment_dispatched_ = false;

  private drop_video_until_keyframe_ = true;
  private video_output_started_ = false;

  /** 首帧时间戳基准：输出时间轴从 0 起，避免大基准 DTS 导致 MSE 黑屏/卡顿 */
  private timestamp_base_: number | undefined = undefined;
  private last_timestamp_: number = 0;

  /** MSE append 合批状态 */
  private pending_tags_ = 0;
  private last_dispatch_at_ = 0;

  private video_track_ = {
    type: "video",
    id: 1,
    sequenceNumber: 0,
    samples: [] as Record<string, unknown>[],
    length: 0,
  };
  private audio_track_ = {
    type: "audio",
    id: 2,
    sequenceNumber: 0,
    samples: [] as Record<string, unknown>[],
    length: 0,
  };

  /** FLV 时间戳已是毫秒，无需偏移。 */
  public set timestampBase(_value: number) {
    // no-op：与 TSDemuxer 的接口对齐
  }

  public constructor(_probe_data: FLVProbeResult, options: FLVDemuxerOptions = {}) {
    if (options.waitForInitialVideoKeyframe === false) {
      this.drop_video_until_keyframe_ = false;
      this.video_output_started_ = true;
    }
  }

  public static probe(data: Uint8Array): FLVProbeResult {
    const length = data.byteLength;
    if (length < 3) {
      return { match: false, needMoreData: true };
    }
    return { match: data[0] === 0x46 && data[1] === 0x4c && data[2] === 0x56 };
  }

  public destroy() {
    this.buffer_ = null;
    this.header_parsed_ = false;
    this.timestamp_base_ = undefined;
    this.last_timestamp_ = 0;
    this.pending_tags_ = 0;
    this.last_dispatch_at_ = 0;
    this.video_metadata_ = null as unknown as typeof this.video_metadata_;
    this.audio_metadata_ = null as unknown as typeof this.audio_metadata_;
    this.video_track_ = null as unknown as typeof this.video_track_;
    this.audio_track_ = null as unknown as typeof this.audio_track_;
    this.onError = null;
    this.onTrackMetadata = null;
    this.onDataAvailable = null;
    this.onTrackDiscontinuity = null;
    this.onPcr = null;
  }

  public resetSegmentBoundary(
    _probe_data?: FLVProbeResult,
    _options: { resetAudioParserState?: boolean } = {},
  ): void {
    this.buffer_ = null;
    this.header_parsed_ = false;
    this.video_output_started_ = !!this.video_init_segment_dispatched_;
    this.drop_video_until_keyframe_ = !this.video_init_segment_dispatched_;
  }

  public flushSegmentBoundary(): void {
    this.maybeDispatchMediaSegment(true);
  }

  /**
   * 时间戳归零：以首帧时间戳为基准，输出 dts/pts 从 0 起。
   * 直播流禁止时间轴倒退（否则 MSE 产生空洞导致卡顿）：回退超过 500ms
   * 时钳制为上次值，保持输出单调。
   */
  private normalizeTimestamp(ts: number): number {
    if (this.timestamp_base_ === undefined) {
      this.timestamp_base_ = ts;
    }

    let out = ts - this.timestamp_base_;

    if (out < this.last_timestamp_ - 500) {
      Log.w(this.TAG, `timestamp rollback ${out} -> ${this.last_timestamp_}`);
      out = this.last_timestamp_;
    }

    this.last_timestamp_ = out;
    return out;
  }

  /** MSE append 合批：攒够 tag 数或距上次派发超时才派发一次，避免逐 tag appendBuffer。 */
  private maybeDispatchMediaSegment(force = false): void {
    if (this.pending_tags_ === 0) {
      return;
    }
    if (
      !force &&
      this.pending_tags_ < FLVDemuxer.DISPATCH_TAG_THRESHOLD &&
      Date.now() - this.last_dispatch_at_ < FLVDemuxer.DISPATCH_INTERVAL_MS
    ) {
      return;
    }
    this.pending_tags_ = 0;
    this.last_dispatch_at_ = Date.now();
    this.dispatchAudioVideoMediaSegment();
  }

  public parseChunks(chunk: Uint8Array, _byte_start: number): number {
    if (!this.onError || !this.onTrackMetadata || !this.onDataAvailable) {
      throw new IllegalStateException("onError & onTrackMetadata & onDataAvailable callback must be specified");
    }

    const buffer = this.accumulate(chunk);
    let offset = 0;

    if (!this.header_parsed_) {
      if (buffer.byteLength < 13) {
        // FLV 头（9）＋ 首个 PreviousTagSize（4）不完整，等下一块
        this.setRemainder(buffer.subarray(offset));
        return chunk.byteLength;
      }
      offset = 13;
      this.header_parsed_ = true;
    }

    let consumedTags = 0;
    while (true) {
      if (buffer.byteLength - offset < 11) {
        break;
      }
      const data_size =
        (buffer[offset + 1] << 16) | (buffer[offset + 2] << 8) | buffer[offset + 3];
      const total = 11 + data_size + 4; // tag header + data + PreviousTagSize
      if (buffer.byteLength - offset < total) {
        break;
      }

      const tag_type = buffer[offset];
      // FLV tag 时间戳为 24 位大端（data[4] 为最高字节）+ 扩展 8 位（data[7]），
      // 与 flv.js 的读取一致；按小端读会把时间戳放大 256 倍导致乱序跳变
      const timestamp =
        ((buffer[offset + 7] & 0xff) << 24) |
        (buffer[offset + 4] << 16) |
        (buffer[offset + 5] << 8) |
        buffer[offset + 6];
      const tag_data = buffer.subarray(offset + 11, offset + 11 + data_size);

      if (tag_type === FLV_TAG_AUDIO) {
        this.parseAudioTag(tag_data, timestamp);
      } else if (tag_type === FLV_TAG_VIDEO) {
        this.parseVideoTag(tag_data, timestamp);
      } else if (tag_type === FLV_TAG_SCRIPT) {
        // script data（onMetaData）：无需解析，跳过
      }
      consumedTags++;

      offset += total;
    }

    this.setRemainder(buffer.subarray(offset));

    this.pending_tags_ += consumedTags;
    this.maybeDispatchMediaSegment();
    return chunk.byteLength;
  }

  /** 把新 chunk 接到剩余数据上，并限制残留缓冲上限。 */
  private accumulate(chunk: Uint8Array): Uint8Array {
    const rest = this.buffer_;
    this.buffer_ = null;
    if (!rest || rest.byteLength === 0) {
      return chunk;
    }
    const merged = new Uint8Array(rest.byteLength + chunk.byteLength);
    merged.set(rest, 0);
    merged.set(chunk, rest.byteLength);
    return merged;
  }

  private setRemainder(data: Uint8Array): void {
    if (data.byteLength > (16 << 20)) {
      Log.e(this.TAG, "FLV demux buffer exceeds 16MB; dropping the stream");
      this.buffer_ = null;
      this.header_parsed_ = false;
      return;
    }
    if (data.byteLength === 0) {
      this.buffer_ = null;
      return;
    }
    // 拷贝：subarray 引用的是 loader 的大 chunk 缓冲，长期持有会阻止 GC
    const copy = new Uint8Array(data.byteLength);
    copy.set(data);
    this.buffer_ = copy;
  }

  // ---------------------------------------------------------------------------
  // 音频 tag（AAC）
  // ---------------------------------------------------------------------------

  private parseAudioTag(data: Uint8Array, tag_ts: number): void {
    if (data.byteLength < 2) {
      return;
    }
    const sound_format = data[0] >>> 4;
    if (sound_format !== SOUND_FORMAT_AAC) {
      // 仅支持 AAC（ffmpeg 默认输出）；其他格式直接忽略
      Log.w(this.TAG, `Unsupported FLV audio soundFormat: ${sound_format}`);
      return;
    }

    const aac_packet_type = data[1];
    const payload = data.subarray(2);

    if (aac_packet_type === AAC_PACKET_SEQUENCE_HEADER) {
      this.parseAACSequenceHeader(payload);
      return;
    }
    if (aac_packet_type !== AAC_PACKET_RAW) {
      return;
    }
    if (payload.byteLength === 0) {
      return;
    }

    // 等视频首帧初始化（避免只闻其声）；无视频流时直接放行
    if (this.has_video_ && !this.video_init_segment_dispatched_) {
      return;
    }

    const frame = this.buildADTSFrame(payload);
    let pts_ms = this.normalizeTimestamp(tag_ts);

    const audio_sample = { codec: "aac", data: frame } as const;
    if (!this.audio_init_segment_dispatched_) {
      this.dispatchAudioInitSegment(audio_sample);
    } else if (this.detectAudioMetadataChange(frame)) {
      this.dispatchAudioMediaSegment();
      this.dispatchAudioInitSegment(audio_sample);
    }

    const pts_ms_int = Math.floor(pts_ms);
    const aac_sample = {
      unit: frame.data,
      length: frame.data.byteLength,
      pts: pts_ms_int,
      dts: pts_ms_int,
    };
    this.audio_track_.samples.push(aac_sample);
    this.audio_track_.length += frame.data.byteLength;
  }

  private parseAACSequenceHeader(payload: Uint8Array): void {
    if (payload.byteLength < 2) {
      return;
    }
    const audio_object_type = (payload[0] >>> 3) & 0x1f;
    const sampling_freq_index = ((payload[0] & 0x07) << 1) | ((payload[1] & 0x80) >>> 7);
    const channel_config = (payload[1] & 0x78) >>> 3;

    const meta: AACAudioMetadata = {
      codec: "aac",
      audio_object_type: audio_object_type as MPEG4AudioObjectTypes,
      sampling_freq_index: sampling_freq_index as MPEG4SamplingFrequencyIndex,
      sampling_frequency: MPEG4SamplingFrequencies[sampling_freq_index] ?? 44100,
      channel_config,
    };

    // AAC sequence header 只代表音频元数据，不代表有视频流
    if (this.audio_init_segment_dispatched_) {
      this.dispatchAudioMediaSegment();
      this.audio_init_segment_dispatched_ = false;
    }
    this.audio_metadata_ = meta;
  }

  /** AAC raw 帧组装（不含 ADTS 头——与 TSDemuxer 输出协议一致，remuxer 按 AudioSpecificConfig 打包）。 */
  private buildADTSFrame(raw: Uint8Array): AACFrame {
    const frame = new AACFrame();
    frame.audio_object_type = this.audio_metadata_.audio_object_type;
    frame.sampling_freq_index = this.audio_metadata_.sampling_freq_index;
    frame.sampling_frequency = this.audio_metadata_.sampling_frequency;
    frame.channel_config = this.audio_metadata_.channel_config || 2;
    frame.data = raw;
    return frame;
  }

  private detectAudioMetadataChange(frame: AACFrame): boolean {
    return (
      frame.audio_object_type !== this.audio_metadata_.audio_object_type ||
      frame.sampling_freq_index !== this.audio_metadata_.sampling_freq_index ||
      frame.channel_config !== this.audio_metadata_.channel_config
    );
  }

  private detectVideoMetadataChange(details: Record<string, unknown>): boolean {
    const old = this.video_metadata_;
    return (
      (old.details.codec_mimetype as string) !== (details.codec_mimetype as string) ||
      (old.details.codec_size as Record<string, number> | undefined)?.width !==
        (details.codec_size as Record<string, number> | undefined)?.width ||
      (old.details.codec_size as Record<string, number> | undefined)?.height !==
        (details.codec_size as Record<string, number> | undefined)?.height
    );
  }

  // ---------------------------------------------------------------------------
  // 视频 tag（H.264 AVC）
  // ---------------------------------------------------------------------------

  private parseVideoTag(data: Uint8Array, tag_ts: number): void {
    if (data.byteLength < 1) {
      return;
    }
    const frame_type = data[0] >>> 4;
    const codec_id = data[0] & 0x0f;
    if (codec_id !== VIDEO_CODEC_AVC) {
      Log.w(this.TAG, `Unsupported FLV video codecId: ${codec_id}`);
      return;
    }
    if (data.byteLength < 5) {
      return;
    }

    const avc_packet_type = data[1];
    // 24 位有符号 composition time（单位 ms）
    let composition_time = (data[2] << 16) | (data[3] << 8) | data[4];
    if (composition_time >= 0x800000) {
      composition_time -= 0x1000000;
    }
    const payload = data.subarray(5);

    if (avc_packet_type === AVC_PACKET_SEQUENCE_HEADER) {
      this.parseAVCSequenceHeader(payload);
      return;
    }
    if (avc_packet_type === AVC_PACKET_END_OF_SEQUENCE) {
      return;
    }
    if (avc_packet_type !== AVC_PACKET_NALU || payload.byteLength === 0) {
      return;
    }
    this.has_video_ = true;

    this.parseAVCNalu(payload, tag_ts, composition_time, frame_type === 1, tag_ts);
  }

  /** AVCDecoderConfigurationRecord（SPS/PPS）→ init segment 元数据。 */
  private parseAVCSequenceHeader(payload: Uint8Array): void {
    if (payload.byteLength < 7 || payload[0] !== 0x01) {
      Log.e(this.TAG, "Malformed AVCDecoderConfigurationRecord");
      return;
    }
    let offset = 5;
    const num_sps = payload[5] & 0x1f;
    if (num_sps === 0) {
      Log.e(this.TAG, "AVCDecoderConfigurationRecord contains no SPS");
      return;
    }
    offset = 6;
    const sps_length = (payload[offset] << 8) | payload[offset + 1];
    offset += 2;
    const sps_data = payload.subarray(offset, offset + sps_length);
    offset += sps_length;
    if (offset >= payload.byteLength) {
      return;
    }
    const num_pps = payload[offset++];
    if (num_pps === 0 || offset + 2 > payload.byteLength) {
      return;
    }
    const pps_length = (payload[offset] << 8) | payload[offset + 1];
    offset += 2;
    const pps_data = payload.subarray(offset, offset + pps_length);

    const sps_nalu: H264NaluPayload = { type: H264NaluType.kSliceSPS, data: sps_data };
    const pps_nalu: H264NaluPayload = { type: H264NaluType.kSlicePPS, data: pps_data };
    const details = SPSParser.parseSPS(sps_data) as unknown as Record<string, unknown>;

    this.has_video_ = true;
    const metadata_changed =
      !!this.video_metadata_.sps && !!this.video_metadata_.pps && this.detectVideoMetadataChange(details);
    if (metadata_changed) {
      // flush stashed frames before changing codec metadata
      this.dispatchVideoMediaSegment();
    }
    this.video_metadata_ = {
      sps: new H264NaluAVC1(sps_nalu),
      pps: new H264NaluAVC1(pps_nalu),
      details,
    };
    if (!this.video_init_segment_dispatched_ || metadata_changed) {
      // 首次或 SPS/PPS 变化：通知新的 codec metadata（init segment）
      this.dispatchVideoInitSegment();
    }
  }

  /** AVCC（4 字节 NALU 长度前缀）→ annexb units 组帧。 */
  private parseAVCNalu(
    data: Uint8Array,
    dts_ms: number,
    cts: number,
    keyframe: boolean,
    _file_position: number,
  ): void {
    const units: { type: H264NaluType; data: Uint8Array; lengthPrefixed: false }[] = [];
    let length = 0;
    let offset = 0;

    while (offset + 4 <= data.byteLength) {
      const nalu_length =
        (data[offset] << 24) | (data[offset + 1] << 16) | (data[offset + 2] << 8) | data[offset + 3];
      if (nalu_length === 0 || offset + 4 + nalu_length > data.byteLength) {
        break;
      }
      const nalu = data.subarray(offset + 4, offset + 4 + nalu_length);
      offset += 4 + nalu_length;

      const nalu_type: H264NaluType = nalu[0] & 0x1f;
      if (
        nalu_type === H264NaluType.kSliceSPS ||
        nalu_type === H264NaluType.kSlicePPS ||
        nalu_type === H264NaluType.kSliceAUD
      ) {
        // SPS/PPS 已在 sequence header 携带，帧内出现的直接跳过
        continue;
      }
      if (nalu_type === H264NaluType.kSliceIDR) {
        keyframe = true;
      }
      units.push({ type: nalu_type, data: nalu, lengthPrefixed: false });
      length += 4 + nalu.byteLength;
    }

    // 等待视频 init segment（SPS/PPS）且首帧为关键帧
    if (!this.video_init_segment_dispatched_) {
      return;
    }
    if (this.drop_video_until_keyframe_ || !this.video_output_started_) {
      if (!keyframe || units.length === 0) {
        return;
      }
      this.drop_video_until_keyframe_ = false;
      this.video_output_started_ = true;
    }
    if (units.length === 0) {
      return;
    }

    const dts = this.normalizeTimestamp(dts_ms);
    const pts = this.normalizeTimestamp(dts_ms + cts);

    const sample = {
      units,
      length,
      isKeyframe: keyframe,
      dts: Math.floor(dts),
      pts: Math.floor(pts),
      cts: Math.floor(pts - dts),
      file_position: dts,
    };
    this.video_track_.samples.push(sample);
    this.video_track_.length += length;
  }

  // ---------------------------------------------------------------------------
  // init segment & media segment 分发（协议对齐 TSDemuxer）
  // ---------------------------------------------------------------------------

  private dispatchVideoInitSegment(): void {
    const details = this.video_metadata_.details as Record<string, Record<string, unknown> & unknown>;
    const meta: Record<string, unknown> = {};
    meta.type = "video";
    meta.id = this.video_track_.id;
    meta.timescale = 1000;
    meta.duration = 0;

    const codec_size = details.codec_size as Record<string, number>;
    const present_size = details.present_size as Record<string, number>;
    const frame_rate = details.frame_rate as Record<string, unknown>;
    const sar_ratio = details.sar_ratio as Record<string, number>;

    meta.codecWidth = codec_size.width;
    meta.codecHeight = codec_size.height;
    meta.presentWidth = present_size.width;
    meta.presentHeight = present_size.height;
    meta.profile = details.profile_string;
    meta.level = details.level_string;
    meta.bitDepth = details.bit_depth;
    meta.chromaFormat = details.chroma_format;
    meta.sarRatio = sar_ratio;
    meta.frameRate = frame_rate;
    meta.colourPrimaries = details.colour_primaries;
    meta.transferCharacteristics = details.transfer_characteristics;
    meta.matrixCoefficients = details.matrix_coefficients;
    meta.videoFullRange = details.video_full_range_flag;
    meta.mayBeInterlaced =
      (details.frame_mbs_only_flag as unknown) === 0 || (details.interlaced_source as unknown) === true;

    const fps_den = frame_rate.fps_den as number;
    const fps_num = frame_rate.fps_num as number;
    meta.refSampleDuration = 1000 * (fps_den / fps_num);
    meta.codec = details.codec_mimetype;

    const sps_without_header = this.video_metadata_.sps?.data.subarray(4);
    const pps_without_header = this.video_metadata_.pps?.data.subarray(4);
    if (sps_without_header == null || pps_without_header == null) {
      return;
    }
    const avcc = new AVCDecoderConfigurationRecord(sps_without_header, pps_without_header, details);
    meta.avcc = avcc.getData();

    this.onTrackMetadata?.("video", meta);
    this.video_init_segment_dispatched_ = true;
  }

  private dispatchAudioInitSegment(sample: { codec: "aac"; data: AACFrame }) {
    const meta: Record<string, unknown> = {};
    meta.type = "audio";
    meta.id = this.audio_track_.id;
    meta.timescale = 1000;
    meta.duration = 0;

    if (sample.codec !== "aac") {
      return;
    }
    const audio_specific_config = new AudioSpecificConfig(sample.data);
    meta.audioSampleRate = audio_specific_config.sampling_rate;
    meta.channelCount = audio_specific_config.channel_count;
    meta.codec = audio_specific_config.codec_mimetype;
    meta.originalCodec = audio_specific_config.original_codec_mimetype;
    meta.config = audio_specific_config.config;
    meta.refSampleDuration = (1024 / (meta.audioSampleRate as number)) * (meta.timescale as number);

    this.onTrackMetadata?.("audio", meta);
    this.audio_init_segment_dispatched_ = true;
  }

  private shouldWaitForVideoKeyframe(): boolean {
    return this.has_video_ && !this.video_init_segment_dispatched_;
  }

  /**
   * 注意：这里不能 reset track —— 样本数组由 remuxer 拥有。
   * remuxer 对单样本批次（samples.length===1 && !force）会保留在 demuxer
   * 队列里继续累积（见 MP4Remuxer._remuxVideo/_remuxAudio），攒够 250ms
   * 批量后才消费并自行 track.samples=[]。若派发后清空，单样本批次会被丢弃，
   * 导致视频帧大量丢失、画面一直不播放（与 TSDemuxer 行为一致）。
   */

  private dispatchVideoMediaSegment(): void {
    if (this.shouldWaitForVideoKeyframe()) {
      return;
    }
    if (this.video_init_segment_dispatched_ && this.video_track_.length) {
      this.onDataAvailable?.(null, this.video_track_, true);
    }
  }

  private dispatchAudioMediaSegment(): void {
    if (this.shouldWaitForVideoKeyframe()) {
      return;
    }
    if (this.audio_init_segment_dispatched_ && this.audio_track_.length) {
      this.onDataAvailable?.(this.audio_track_, null, true);
    }
  }

  private dispatchAudioVideoMediaSegment(): void {
    if (this.shouldWaitForVideoKeyframe()) {
      return;
    }
    const hasAudio = this.audio_init_segment_dispatched_ && this.audio_track_.length;
    const hasVideo = this.video_track_.length;
    if (hasAudio || hasVideo) {
      this.onDataAvailable?.(hasAudio ? this.audio_track_ : null, hasVideo ? this.video_track_ : null);
    }
  }
}

export default FLVDemuxer;