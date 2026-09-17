/**
 * 错误码契约（clean-room 实现）。
 * 设计 §5.14：UI 依赖错误码映射——**错误码/字段须保持稳定**，故此处沿用既有取值，
 * 仅重新组织实现表述。
 */

export const PlayerErrors = {
  EXCEPTION: "Exception",
  REQUEST_FAILED: "RequestFailed",
  HTTP_STATUS_CODE_INVALID: "HttpStatusCodeInvalid",
  EARLY_EOF: "EarlyEof",
  FORMAT_ERROR: "FormatError",
  FORMAT_UNSUPPORTED: "FormatUnsupported",
  CODEC_UNSUPPORTED: "CodecUnsupported",
  AUDIO_RESYNC_FAILED: "AudioResyncFailed",
  MEDIA_SOURCE_CLOSED: "MediaSourceClosed",
  MEDIA_MSE_ERROR: "MediaMSEError",
  MEDIA_ELEMENT_ERROR: "MediaElementError",
} as const;

type ValueOf<T> = T[keyof T];

export type PlayerErrorDetail = ValueOf<typeof PlayerErrors>;

