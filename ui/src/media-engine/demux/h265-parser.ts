/**
 * H.265/HEVC 参数集解析（本项目自研。
 *
 * 规范依据（公开标准，逐条标注条款）：
 *   - ITU-T H.265 §7.3.2.1 VPS、§7.3.2.2 SPS（含 §7.3.2.2.1 profile_tier_level）、
 *     §7.3.2.3 PPS、§7.3.4 缩放矩阵、§7.3.7 短期参考图像集、§E.2.1/§E.2.2 VUI 与 HRD；
 *   - ISO/IEC 14496-15 §8.3.3.1 hvcC 记录：本文件只产出它需要的 PTL/编解码字段，
 *     记录本身由 `h265.ts` 的 `buildHvcC()` 组装。
 *
 * 解析策略：只需要"能喂 hvcC + 能给出 codec 串与图像尺寸"的字段，其余语法元素按规范
 * **逐位跳过**（跳过的分支都保留注释说明，便于对照规范核查）。因此本文件不是一个
 * 完整的 SPS 解码器，而是一个"对齐走过的"参数集读取器。
 */
import ExpGolomb from "./exp-golomb";

/** VPS 中与 hvcC 有关的字段（§7.3.2.1）。 */
export interface HevcVpsInfo {
  /** vps_max_sub_layers_minus1 + 1。 */
  numTemporalLayers: number;
  /** vps_temporal_id_nesting_flag。 */
  temporalIdNested: boolean;
}

/** profile_tier_level 里 hvcC 需要的 general 层字段（§7.3.2.2.1）。 */
export interface HevcProfileTierLevel {
  profileSpace: number;
  tierFlag: boolean;
  profileIdc: number;
  /** general_profile_compatibility_flags：32 bit，按大端 4 字节。 */
  compatibilityFlags: [number, number, number, number];
  /** general_constraint_indicator_flags：48 bit，按大端 6 字节。 */
  constraintFlags: [number, number, number, number, number, number];
  levelIdc: number;
}

/** SPS 解析结果。 */
export interface HevcSpsInfo {
  /** MSE 需要的完整 codec 串（形如 `hvc1.1.6.L93.B0`）。 */
  codec: string;
  /** 编解码尺寸（已扣除 conformance window）。 */
  size: { width: number; height: number };
  /** 显示尺寸（按 SAR 拉伸后的宽）。 */
  displaySize: { width: number; height: number };
  /** 交织内容标志：VUI field_seq_flag 或 PTL 的 interlaced_source 约束位。 */
  interlaced: boolean;
  chromaFormatIdc: number;
  /** 亮度位深（= bitDepthLumaMinus8 + 8，便于展示）。 */
  bitDepth: number;
  bitDepthLumaMinus8: number;
  bitDepthChromaMinus8: number;
  /** hvcC 记录的 general 层参数。 */
  pti: HevcProfileTierLevel;
  /** hvcC 的 min_spatial_segmentation_idc（来自 VUI bitstream_restriction）。 */
  minSpatialSegmentationIdc: number;
  frameRate: { fixed: boolean; fps: number; numerator: number; denominator: number };
  sar: { width: number; height: number };
  color?: { primaries: number; transfer: number; matrix: number; fullRange: boolean };
  /** 诊断用（未参与 hvcC）。 */
  profileName: string;
  levelName: string;
  chromaFormatName: string;
}

/** PPS 解析结果。 */
export interface HevcPpsInfo {
  /** hvcC 的 parallelismType（0 混合 / 1 基于 slice / 2 基于 tile / 3 基于波前）。 */
  parallelismType: number;
}

/** EBSP → RBSP：去掉防竞争字节（H.265 §7.4.2.1，`00 00 03` 中的 `03`）。 */
export function ebspToRbsp(ebsp: Uint8Array): Uint8Array {
  const out = new Uint8Array(ebsp.byteLength);
  let write = 0;
  for (let i = 0; i < ebsp.byteLength; i++) {
    // 只在 `00 00` 之后跳过 0x03，其余字节原样保留。
    if (i >= 2 && ebsp[i] === 0x03 && ebsp[i - 1] === 0x00 && ebsp[i - 2] === 0x00) {
      continue;
    }
    out[write++] = ebsp[i];
  }
  return out.subarray(0, write);
}

/** 建立"跳过 2 字节 NAL 头"后的位读取器（NAL unit header 见 H.265 §7.3.1.2）。 */
function openNal(nal: Uint8Array): ExpGolomb {
  const reader = new ExpGolomb(ebspToRbsp(nal));
  reader.readByte(); // forbidden_zero_bit(1) + nal_unit_type(6) + nuh_layer_id(6) 的高 1 位
  reader.readByte(); // nuh_layer_id 低位 + nuh_temporal_id_plus1(3)
  return reader;
}

/** §7.3.2.2.1：profile_tier_level（只保留 general 层与"跳过子层"所需的信息）。 */
function readProfileTierLevel(reader: ExpGolomb, maxSubLayersMinus1: number): HevcProfileTierLevel {
  const profileSpace = reader.readBits(2);
  const tierFlag = reader.readBool();
  const profileIdc = reader.readBits(5);
  const compatibilityFlags: [number, number, number, number] = [
    reader.readByte(),
    reader.readByte(),
    reader.readByte(),
    reader.readByte(),
  ];
  const constraintFlags: [number, number, number, number, number, number] = [
    reader.readByte(),
    reader.readByte(),
    reader.readByte(),
    reader.readByte(),
    reader.readByte(),
    reader.readByte(),
  ];
  const levelIdc = reader.readByte();

  const subLayerProfilePresent: boolean[] = [];
  const subLayerLevelPresent: boolean[] = [];
  for (let i = 0; i < maxSubLayersMinus1; i++) {
    subLayerProfilePresent.push(reader.readBool());
    subLayerLevelPresent.push(reader.readBool());
  }
  // reserved_zero_2bits：子层数量未到 8 时补齐到 8 项。
  if (maxSubLayersMinus1 > 0) {
    for (let i = maxSubLayersMinus1; i < 8; i++) {
      reader.readBits(2);
    }
  }
  for (let i = 0; i < maxSubLayersMinus1; i++) {
    if (subLayerProfilePresent[i]) {
      // sub_layer_profile_space(2) + tier(1) + profile_idc(5) + compatibility(32) + constraint(48) = 88 bit = 11 字节
      for (let j = 0; j < 11; j++) {
        reader.readByte();
      }
    }
    if (subLayerLevelPresent[i]) {
      reader.readByte();
    }
  }

  return { profileSpace, tierFlag, profileIdc, compatibilityFlags, constraintFlags, levelIdc };
}

/** §7.3.4：scaling_list_data（SPS/PPS 内嵌）。 */
function skipScalingListData(reader: ExpGolomb): void {
  for (let sizeId = 0; sizeId < 4; sizeId++) {
    const matrices = sizeId === 3 ? 2 : 6;
    for (let matrixId = 0; matrixId < matrices; matrixId++) {
      const predModeFlag = reader.readBool();
      if (!predModeFlag) {
        reader.readUEG(); // scaling_list_pred_matrix_id_delta
        continue;
      }
      if (sizeId > 1) {
        reader.readSEG(); // scaling_list_dc_coef_minus8
      }
      const coefNum = Math.min(64, 1 << (4 + (sizeId << 1)));
      for (let i = 0; i < coefNum; i++) {
        reader.readSEG(); // scaling_list_delta_coef
      }
    }
  }
}

/** §7.3.7：stp_ref_pic_set()（SPS 内只可能出现非 delta 索引的形态）。 */
function skipShortTermRefPicSets(reader: ExpGolomb, count: number): void {
  let prevNumDeltaPocs = 0;
  for (let i = 0; i < count; i++) {
    // 只有 i>0 才可能继承上一个集合（i==0 时该语法元素不存在）。
    const interPrediction = i !== 0 ? reader.readBool() : false;
    if (interPrediction) {
      // delta_idx_minus1 只在"slice header 里引用 SPS 集合"时出现；SPS 自身内不出现。
      reader.readBool(); // delta_rps_sign
      reader.readUEG(); // abs_delta_rps_minus1
      let nextNumDeltaPocs = 0;
      for (let j = 0; j <= prevNumDeltaPocs; j++) {
        const usedByCurrPic = reader.readBool();
        const useDelta = usedByCurrPic ? false : reader.readBool();
        if (usedByCurrPic || useDelta) {
          nextNumDeltaPocs++;
        }
      }
      prevNumDeltaPocs = nextNumDeltaPocs;
      continue;
    }
    const numNegative = reader.readUEG();
    const numPositive = reader.readUEG();
    prevNumDeltaPocs = numNegative + numPositive;
    for (let j = 0; j < numNegative; j++) {
      reader.readUEG(); // delta_poc_s0_minus1
      reader.readBool(); // used_by_curr_pic_s0_flag
    }
    for (let j = 0; j < numPositive; j++) {
      reader.readUEG(); // delta_poc_s1_minus1
      reader.readBool(); // used_by_curr_pic_s1_flag
    }
  }
}

/** §7.3.2.2.1 / §E.2.2：hrd_parameters()。 */
function skipHrdParameters(
  reader: ExpGolomb,
  maxSubLayersMinus1: number,
  nalHrdPresent: boolean,
  vclHrdPresent: boolean,
): void {
  let subPicHrdParamsPresent = false;
  if (nalHrdPresent || vclHrdPresent) {
    subPicHrdParamsPresent = reader.readBool();
    if (subPicHrdParamsPresent) {
      reader.readByte(); // tick_divisor_minus2
      reader.readBits(5); // du_cpb_removal_delay_increment_length_minus1
      reader.readBool(); // sub_pic_cpb_params_in_pic_timing_sei_flag
      reader.readBits(5); // dpb_output_delay_du_length_minus1
    }
    reader.readBits(4); // bit_rate_scale
    reader.readBits(4); // cpb_size_scale
    if (subPicHrdParamsPresent) {
      reader.readBits(4); // cpb_size_du_scale
    }
    reader.readBits(5); // initial_cpb_removal_delay_length_minus1
    reader.readBits(5); // au_cpb_removal_delay_length_minus1
    reader.readBits(5); // dpb_output_delay_length_minus1
  }

  for (let i = 0; i <= maxSubLayersMinus1; i++) {
    const fixedGeneral = reader.readBool(); // fixed_pic_rate_general_flag
    let fixedWithinCvs = true;
    if (!fixedGeneral) {
      fixedWithinCvs = reader.readBool();
    }
    let lowDelay = false;
    if (fixedWithinCvs) {
      reader.readUEG(); // elemental_duration_in_tc_minus1
    } else {
      lowDelay = reader.readBool();
    }
    let cpbCount = 1;
    if (!lowDelay) {
      cpbCount = reader.readUEG() + 1;
    }
    for (const present of [nalHrdPresent, vclHrdPresent]) {
      if (!present) {
        continue;
      }
      for (let j = 0; j < cpbCount; j++) {
        reader.readUEG(); // bit_rate_value_minus1
        reader.readUEG(); // cpb_size_value_minus1
        if (subPicHrdParamsPresent) {
          reader.readUEG(); // cpb_size_du_value_minus1
          reader.readUEG(); // bit_rate_du_value_minus1
        }
        reader.readBool(); // cbr_flag
      }
    }
  }
}

/** VUI 中我们关心的信息（§E.2.1）。 */
interface VuiInfo {
  sarWidth: number;
  sarHeight: number;
  fieldSeqFlag: boolean;
  frameRateFixed: boolean;
  fpsNumerator: number;
  fpsDenominator: number;
  minSpatialSegmentationIdc: number;
  color?: { primaries: number; transfer: number; matrix: number; fullRange: boolean };
}

/** §E.2.1：vui_parameters()（只保留 SAR/色彩/帧率/交织/空间分段）。 */
function readVuiParameters(reader: ExpGolomb, maxSubLayersMinus1: number): VuiInfo {
  const info: VuiInfo = {
    sarWidth: 1,
    sarHeight: 1,
    fieldSeqFlag: false,
    frameRateFixed: false,
    fpsNumerator: 0,
    fpsDenominator: 0,
    minSpatialSegmentationIdc: 0,
  };

  if (reader.readBool()) {
    // aspect_ratio_info_present_flag：idc → SAR 查表（H.265 Table E.1），255 表示显式给出。
    const SAR_TABLE: ReadonlyArray<readonly [number, number]> = [
      [1, 1], [12, 11], [10, 11], [16, 11], [40, 33], [24, 11], [20, 11], [32, 11],
      [80, 33], [18, 11], [15, 11], [64, 33], [160, 99], [4, 3], [3, 2], [2, 1],
    ];
    const aspectRatioIdc = reader.readByte();
    if (aspectRatioIdc >= 1 && aspectRatioIdc <= 16) {
      const entry = SAR_TABLE[aspectRatioIdc - 1];
      info.sarWidth = entry[0];
      info.sarHeight = entry[1];
    } else if (aspectRatioIdc === 255) {
      info.sarWidth = reader.readBits(16);
      info.sarHeight = reader.readBits(16);
    }
  }

  if (reader.readBool()) {
    reader.readBool(); // overscan_appropriate_flag
  }

  if (reader.readBool()) {
    // video_signal_type_present_flag
    reader.readBits(3); // video_format
    const fullRange = reader.readBool(); // video_full_range_flag
    if (reader.readBool()) {
      // colour_description_present_flag
      info.color = {
        primaries: reader.readByte(),
        transfer: reader.readByte(),
        matrix: reader.readByte(),
        fullRange,
      };
    }
  }

  if (reader.readBool()) {
    // chroma_loc_info_present_flag
    reader.readUEG(); // chroma_sample_loc_type_top_field
    reader.readUEG(); // chroma_sample_loc_type_bottom_field
  }

  reader.readBool(); // neutral_chroma_indication_flag
  info.fieldSeqFlag = reader.readBool();
  reader.readBool(); // frame_field_info_present_flag

  if (reader.readBool()) {
    // default_display_window_flag：窗口偏移（我们按 conformance window 报告尺寸）
    reader.readUEG();
    reader.readUEG();
    reader.readUEG();
    reader.readUEG();
  }

  if (reader.readBool()) {
    // vui_timing_info_present_flag
    info.fpsDenominator = reader.readBits(32); // vui_num_units_in_tick
    info.fpsNumerator = reader.readBits(32); // vui_time_scale
    if (reader.readBool()) {
      reader.readUEG(); // vui_num_ticks_poc_diff_one_minus1
    }
    if (reader.readBool()) {
      // vui_hrd_parameters_present_flag
      const nalHrdPresent = reader.readBool();
      const vclHrdPresent = reader.readBool();
      skipHrdParameters(reader, maxSubLayersMinus1, nalHrdPresent, vclHrdPresent);
    }
  }

  if (reader.readBool()) {
    // bitstream_restriction_flag
    reader.readBool(); // tiles_fixed_structure_flag
    reader.readBool(); // motion_vectors_over_pic_boundaries_flag
    reader.readBool(); // restricted_ref_pic_lists_flag
    info.minSpatialSegmentationIdc = reader.readUEG();
    reader.readUEG(); // max_bytes_per_pic_denom
    reader.readUEG(); // max_bits_per_min_cu_denom
    reader.readUEG(); // log2_max_mv_length_horizontal
    reader.readUEG(); // log2_max_mv_length_vertical
  }

  return info;
}

/** H.265 剖面名表（Annex A Table A.1）；表外取值统一回落 "Unknown"。 */
const HEVC_PROFILE_NAMES: Record<number, string> = {
  1: "Main",
  2: "Main10",
  3: "MainSP",
  4: "Rext",
  9: "SCC",
};

/** 剖面名（H.265 Annex A Table A.1；未知返回 Unknown）。 */
export function hevcProfileName(profileIdc: number): string {
  return HEVC_PROFILE_NAMES[profileIdc] ?? "Unknown";
}

/** 色度格式名（H.265 Table 6-1）。 */
export function hevcChromaFormatName(chromaFormatIdc: number): string {
  return ["4:0:0", "4:2:0", "4:2:2", "4:4:4"][chromaFormatIdc] ?? "Unknown";
}

/** 等级名：level_idc / 30（如 93 → "3.1"，120 → "4"）。 */
export function hevcLevelName(levelIdc: number): string {
  return (levelIdc / 30).toFixed(1);
}

/** 解析 VPS（§7.3.2.1，`nal` 含 2 字节 NAL 头）。 */
export function parseHevcVps(nal: Uint8Array): HevcVpsInfo {
  const reader = openNal(nal);
  reader.readBits(4); // vps_video_parameter_set_id
  reader.readBits(2); // vps_base_layer_internal_flag + vps_base_layer_available_flag
  reader.readBits(6); // vps_max_layers_minus1
  const maxSubLayersMinus1 = reader.readBits(3);
  const temporalIdNested = reader.readBool();
  reader.destroy();
  return { numTemporalLayers: maxSubLayersMinus1 + 1, temporalIdNested };
}

/** 解析 PPS（§7.3.2.3）：读到 tiles/entropy 两个标志即可推出 hvcC 的 parallelismType。 */
export function parseHevcPps(nal: Uint8Array): HevcPpsInfo {
  const reader = openNal(nal);
  reader.readUEG(); // pps_pic_parameter_set_id
  reader.readUEG(); // pps_seq_parameter_set_id
  reader.readBool(); // dependent_slice_segments_enabled_flag
  reader.readBool(); // output_flag_present_flag
  reader.readBits(3); // num_extra_slice_header_bits
  reader.readBool(); // sign_data_hiding_enabled_flag
  reader.readBool(); // cabac_init_present_flag
  reader.readUEG(); // num_ref_idx_l0_default_active_minus1
  reader.readUEG(); // num_ref_idx_l1_default_active_minus1
  reader.readSEG(); // init_qp_minus26
  reader.readBool(); // constrained_intra_pred_flag
  reader.readBool(); // transform_skip_enabled_flag
  if (reader.readBool()) {
    reader.readUEG(); // cu_qp_delta_enabled_flag → diff_cu_qp_delta_depth
  }
  reader.readSEG(); // pps_cb_qp_offset
  reader.readSEG(); // pps_cr_qp_offset
  reader.readBool(); // pps_slice_chroma_qp_offsets_present_flag
  reader.readBool(); // weighted_pred_flag
  reader.readBool(); // weighted_bipred_flag
  reader.readBool(); // transquant_bypass_enabled_flag
  const tilesEnabled = reader.readBool();
  const entropySyncEnabled = reader.readBool();
  reader.destroy();

  // hvcC parallelIdType：两种并行机制都开 = 混合（0）；只有波前 = 3；只有 tile = 2；都无 = 基于 slice（1）。
  let parallelismType = 1;
  if (tilesEnabled && entropySyncEnabled) {
    parallelismType = 0;
  } else if (entropySyncEnabled) {
    parallelismType = 3;
  } else if (tilesEnabled) {
    parallelismType = 2;
  }
  return { parallelismType };
}

/** 解析 SPS（§7.3.2.2，`nal` 含 2 字节 NAL 头）。 */
export function parseHevcSps(nal: Uint8Array): HevcSpsInfo {
  const reader = openNal(nal);

  reader.readBits(4); // sps_video_parameter_set_id
  const maxSubLayersMinus1 = reader.readBits(3);
  reader.readBool(); // sps_temporal_id_nesting_flag
  const pti = readProfileTierLevel(reader, maxSubLayersMinus1);

  reader.readUEG(); // sps_seq_parameter_set_id
  const chromaFormatIdc = reader.readUEG();
  if (chromaFormatIdc === 3) {
    reader.readBits(1); // separate_colour_plane_flag
  }
  const picWidth = reader.readUEG();
  const picHeight = reader.readUEG();

  let cropLeft = 0;
  let cropRight = 0;
  let cropTop = 0;
  let cropBottom = 0;
  if (reader.readBool()) {
    // conformance_window_flag（§7.4.3.2.1 规定按 SubWidthC/SubHeightC 折算）
    cropLeft = reader.readUEG();
    cropRight = reader.readUEG();
    cropTop = reader.readUEG();
    cropBottom = reader.readUEG();
  }

  const bitDepthLumaMinus8 = reader.readUEG();
  const bitDepthChromaMinus8 = reader.readUEG();
  const log2MaxPicOrderCntLsbMinus4 = reader.readUEG();

  const orderingInfoPresent = reader.readBool();
  for (let i = orderingInfoPresent ? 0 : maxSubLayersMinus1; i <= maxSubLayersMinus1; i++) {
    reader.readUEG(); // max_dec_pic_buffering_minus1
    reader.readUEG(); // max_num_reorder_pics
    reader.readUEG(); // max_latency_increase_plus1
  }

  reader.readUEG(); // log2_min_luma_coding_block_size_minus3
  reader.readUEG(); // log2_diff_max_min_luma_coding_block_size
  reader.readUEG(); // log2_min_luma_transform_block_size_minus2
  reader.readUEG(); // log2_diff_max_min_luma_transform_block_size
  reader.readUEG(); // max_transform_hierarchy_depth_inter
  reader.readUEG(); // max_transform_hierarchy_depth_intra

  if (reader.readBool()) {
    // scaling_list_enabled_flag → sps_scaling_list_data_present_flag
    if (reader.readBool()) {
      skipScalingListData(reader);
    }
  }

  reader.readBool(); // amp_enabled_flag
  reader.readBool(); // sample_adaptive_offset_enabled_flag
  if (reader.readBool()) {
    // pcm_enabled_flag → pcm 采样参数
    reader.readByte(); // pcm_sample_bit_depth_luma_minus1 + chroma_minus1
    reader.readUEG(); // log2_min_pcm_luma_coding_block_size_minus3
    reader.readUEG(); // log2_diff_max_min_pcm_luma_coding_block_size
    reader.readBool(); // pcm_loop_filter_disabled_flag
  }

  skipShortTermRefPicSets(reader, reader.readUEG());

  if (reader.readBool()) {
    // long_term_ref_pics_present_flag
    const longTermCount = reader.readUEG();
    for (let i = 0; i < longTermCount; i++) {
      const pocBits = log2MaxPicOrderCntLsbMinus4 + 4;
      for (let j = 0; j < pocBits; j++) {
        reader.readBits(1); // lt_ref_pic_poc_lsb_sps
      }
      reader.readBits(1); // used_by_curr_pic_lt_sps_flag
    }
  }

  reader.readBool(); // sps_temporal_mvp_enabled_flag
  reader.readBool(); // strong_intra_smoothing_enabled_flag

  let vui: VuiInfo | undefined;
  if (reader.readBool()) {
    // vui_parameters_present_flag
    vui = readVuiParameters(reader, maxSubLayersMinus1);
  }
  // 其后的 sps_extension_* 与尺寸/codec 串无关，不再读取。

  reader.destroy();

  // 报告尺寸：按色度子采样折算 conformance window（§7.4.3.2.1 SubWidthC/SubHeightC）
  const subWidthC = chromaFormatIdc === 1 || chromaFormatIdc === 2 ? 2 : 1;
  const subHeightC = chromaFormatIdc === 1 ? 2 : 1;
  const width = picWidth - (cropLeft + cropRight) * subWidthC;
  const height = picHeight - (cropTop + cropBottom) * subHeightC;
  const sarWidth = vui?.sarWidth ?? 1;
  const sarHeight = vui?.sarHeight ?? 1;
  const displayWidth = sarWidth !== 1 || sarHeight !== 1 ? (width * sarWidth) / sarHeight : width;

  return {
    // 兼容性标志固定写 1、约束固定 B0：与播放链路的既有行为一致（MSE isTypeSupported 只做前缀匹配）。
    codec: `hvc1.${pti.profileIdc}.1.L${pti.levelIdc}.B0`,
    size: { width, height },
    displaySize: { width: displayWidth, height },
    interlaced: (vui?.fieldSeqFlag ?? false) || (pti.constraintFlags[0] & 0x40) !== 0,
    chromaFormatIdc,
    bitDepth: bitDepthLumaMinus8 + 8,
    bitDepthLumaMinus8,
    bitDepthChromaMinus8,
    pti,
    minSpatialSegmentationIdc: vui?.minSpatialSegmentationIdc ?? 0,
    frameRate: {
      fixed: vui?.frameRateFixed ?? false,
      fps: vui && vui.fpsDenominator > 0 ? vui.fpsNumerator / vui.fpsDenominator : 0,
      numerator: vui?.fpsNumerator ?? 0,
      denominator: vui?.fpsDenominator ?? 0,
    },
    sar: { width: sarWidth, height: sarHeight },
    color: vui?.color,
    profileName: hevcProfileName(pti.profileIdc),
    levelName: hevcLevelName(pti.levelIdc),
    chromaFormatName: hevcChromaFormatName(chromaFormatIdc),
  };
}
