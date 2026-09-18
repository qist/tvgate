import { mseToWallClock } from "../media-engine/timeline";
import type { EPGProgram } from "../types/player";

/** 一档节目的可播放时间轴：起止、当前墙钟播放头与进度换算。 */
export interface ProgramTimeline {
  startTime: Date;
  endTime: Date;
  playheadTime: Date;
  durationSeconds: number;
  positionSeconds: number;
  progress: number;
}

const clampNumber = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

/**
 * 由节目起止 + 流时钟锚点构造时间轴。
 * 节目时长非法（≤0）或播放头尚未就绪（换算出 NaN）时返回 null，
 * 调用方按"无节目单"处理。
 */
export function createProgramTimeline(
  program: Pick<EPGProgram, "beginsAt" | "endsAt">,
  streamOrigin: Date,
  mediaTime: number,
): ProgramTimeline | null {
  const duration = (program.endsAt.getTime() - program.beginsAt.getTime()) / 1000;
  if (!Number.isFinite(duration) || duration <= 0) return null;

  const playhead = mseToWallClock(mediaTime, streamOrigin);
  if (!Number.isFinite(playhead.getTime())) return null;

  const rawOffset = (playhead.getTime() - program.beginsAt.getTime()) / 1000;
  const position = clampNumber(rawOffset, 0, duration);

  return {
    startTime: program.beginsAt,
    endTime: program.endsAt,
    playheadTime: playhead,
    durationSeconds: duration,
    positionSeconds: position,
    progress: position / duration,
  };
}

/** 时间轴位置（秒）→ 墙钟时刻；非有限输入夹取到节目起点。 */
export function programPositionToWallClock(timeline: ProgramTimeline, positionSeconds: number): Date {
  const offset = Number.isFinite(positionSeconds)
    ? clampNumber(positionSeconds, 0, timeline.durationSeconds)
    : 0;
  return new Date(timeline.startTime.getTime() + offset * 1000);
}

/** 时间轴进度（0..1）→ 墙钟时刻。 */
export function programProgressToWallClock(timeline: ProgramTimeline, progress: number): Date {
  const ratio = Number.isFinite(progress) ? clampNumber(progress, 0, 1) : 0;
  return programPositionToWallClock(timeline, timeline.durationSeconds * ratio);
}
