import { resolveBase } from "./base";

export interface FFmpegOptions {
  global_args?: string[];
  input_pre_args?: string[];
  input_post_args?: string[];
  filters?: { video_filters?: string[]; audio_filters?: string[] };
  video_codec?: string;
  audio_codec?: string;
  video_bitrate?: string;
  audio_bitrate?: string;
  preset?: string;
  crf?: number;
  output_format?: string;
  output_pre_args?: string[];
  output_post_args?: string[];
  custom_args?: string[];
  user_agent?: string;
  headers?: string[];
  stream_copy?: boolean;
  use_re_flag?: boolean;
  pix_fmt?: string;
  gop_size?: number;
}

export interface PlayOutput {
  protocol: string;
  enabled: boolean;
  flv_ffmpeg_options?: FFmpegOptions;
  hls_ffmpeg_options?: FFmpegOptions;
  hls_segment_duration?: number;
  hls_segment_count?: number;
  hls_path?: string;
  hls_enable_playback?: boolean;
  hls_retention_days?: string;
  ts_filename_template?: string;
  hls_daily_archive?: boolean;
  hls_archive_interval?: string;
  hls_archive_retention?: string;
  hls_archive_path?: string;
}

export interface ReceiverItem {
  push_url: string;
  play_urls?: { flv?: string; hls?: string };
  ffmpeg_options?: FFmpegOptions;
}

export interface StreamData {
  source: { type?: string; url: string; backup_url?: string; ffmpeg_options?: FFmpegOptions };
  local_play_urls?: PlayOutput[];
  mode: string;
  receivers: { primary?: ReceiverItem; backup?: ReceiverItem; all?: ReceiverItem[] };
}

export interface StreamItem {
  buffer_size?: number;
  protocol: string;
  enabled: boolean;
  streamkey: { type: string; value?: string; length?: number; expiration?: string };
  stream: StreamData;
}

export interface FFmpegStatus {
  installed: boolean;
  path?: string;
  version?: string;
  error?: string;
  hint?: string;
}

export declare namespace FFmpegProcessStats {
  export type Duration = any;
}
export interface ProcessStats {
  running: boolean;
  pid?: number;
  current_bitrate?: number;
  avg_bitrate?: number;
  cpu_percent?: number;
  memory_rss?: number;
  bytes_transferred?: number;
  duration?: number;
  restarts?: number;
  last_error?: string;
}

export interface StreamStatus {
  name: string;
  enabled: boolean;
  protocol?: string;
  has_manager: boolean;
  primary?: ProcessStats;
  flv_viewers?: number;
  hls_viewers?: number;
}

const base = () => resolveBase() + "api/publisher";

export async function loadConfig(): Promise<Record<string, any>> {
  const r = await fetch(resolveBase() + "config/publisher", { credentials: "same-origin" });
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}

export async function saveConfig(cfg: Record<string, any>): Promise<void> {
  const r = await fetch(resolveBase() + "config/save-publisher", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify(cfg),
  });
  if (!r.ok) throw new Error(await r.text());
}

export async function loadStats(): Promise<{ streams: StreamStatus[]; ts?: number }> {
  const r = await fetch(`${base()}/stats`, { credentials: "same-origin" });
  if (!r.ok) throw new Error(await r.text());
  const data = await r.json();
  return { streams: Array.isArray(data?.streams) ? data.streams : [], ts: data?.ts };
}

export async function loadFFmpegStatus(): Promise<FFmpegStatus> {
  const r = await fetch(`${base()}/ffmpeg`, { credentials: "same-origin" });
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}