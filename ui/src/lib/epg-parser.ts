/**
 * EPG 节目单解析 / 查询 / 缝隙填充。
 * 仅依赖 types/player 的类型；不引用任何上游实现。
 */
import type { Channel, EPGProgram } from "../types/player";

/** 频道号 → 节目列表。键由 getEPGChannelId 决定（tvgId/tvgName/name）。 */
export type EPGData = Record<string, EPGProgram[]>;

/**
 * 解析某频道在 EPGData 中的键：优先 tvgId，其次 tvgName，最后 name。
 * 与 mapChannels / 服务端 epgId 的回退逻辑保持一致。
 */
export function getEPGChannelId(channel: Channel, _epgData?: EPGData): string | undefined {
  return channel.tvgId || channel.tvgName || channel.name || undefined;
}

/** 给定绝对时刻，返回该频道正在播出的节目（start <= at < end），无则 null。 */
export function getCurrentProgram(channelId: string, epgData: EPGData, at: Date): EPGProgram | null {
  const programs = epgData[channelId];
  if (!programs || programs.length === 0) return null;

  const time = at.getTime();
  for (const program of programs) {
    const start = program.start.getTime();
    const end = program.end.getTime();
    if (time >= start && time < end) return program;
  }
  return null;
}

/**
 * 用真实 EPG 合并「缝隙填充」占位：保留已有真实节目，仅为「尚无 EPG 且支持回看」的频道
 * 补一段 2 小时「精彩节目」占位，避免节目单空白（真实数据到达后由调用方再次合并覆盖）。
 */
export function fillEPGGaps(epgData: EPGData, channels: Channel[]): EPGData {
  const result: EPGData = { ...epgData };
  const now = Date.now();

  for (const channel of channels) {
    const key = getEPGChannelId(channel, epgData);
    if (!key) continue;

    const existing = result[key];
    if (existing && existing.length > 0) continue;

    const supportsCatchup = channel.sources.some((s) => s.catchup && s.catchupSource);
    if (!supportsCatchup) continue;

    const start = new Date(now);
    const end = new Date(now + 2 * 60 * 60 * 1000);
    result[key] = [
      {
        id: `filler-${start.getTime()}`,
        // 空标题 → 展示层统一用 t("excellentProgram")（"精彩节目"）兜底，
        // 避免把 i18n key 原样显示成 "excellentProgram"
        title: "",
        start,
        end,
      },
    ];
  }

  return result;
}

/**
 * 服务端 /api/player/epg 下发的单条节目。
 * 时间为 **XMLTV 原样串**（如 "20260916120000 +0800"，带时区后缀），前端必须容忍该后缀。
 */
export interface RawEPGProgram {
  start: string;
  stop: string;
  title: string;
}

/**
 * 任意 EPG 时间 → Date。兼容：
 *   - XMLTV 标准时间戳 "20260916120000 +0800" / "20260916120000+08:00" / 纯数字 YYYYMMDD[HH[MM[SS]]]
 *   - "HH:MM"（当天）
 *   - ISO 串（"2026-09-16T12:00:00"）
 * 时区后缀**按本地时区解释**（与服务端按 date=YYYYMMDD 前缀筛选的语义一致）。
 *
 * ⚠️ 这里必须容忍 XMLTV 的时区后缀。漏掉它会让服务端返回的**每一条**节目都解析失败被丢弃，
 * 表现就是"服务端日志显示 EPG 解析完成，前端却完全没有数据"（曾复现的线上问题）。
 */
export function parseEpgTime(value: string): Date | null {
  const s = (value ?? "").trim();
  if (!s) return null;

  if (s.length === 5 && s.includes(":")) {
    const [h, m] = s.split(":");
    const d = new Date();
    d.setHours(Number(h), Number(m), 0, 0);
    return d;
  }

  if (/^\d{8}(?:\d{2}){0,3}(?:\s*[+-]\d{2}:?\d{2})?$/.test(s)) {
    // 只取前 8~14 位数字，时区后缀忽略（按本地时区解释）
    const num = s.slice(0, 14);
    const y = Number(num.slice(0, 4));
    const mo = Number(num.slice(4, 6)) - 1;
    const da = Number(num.slice(6, 8));
    const h = Number(num.slice(8, 10) || 0);
    const mi = Number(num.slice(10, 12) || 0);
    const se = Number(num.slice(12, 14) || 0);
    return new Date(y, mo, da, h, mi, se);
  }

  const normalized = s.includes("T") ? s : s.replace(" ", "T");
  const parsed = new Date(normalized);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

/** 服务端节目列表 → 播放器模型：解析失败或时间区间非法的条目直接丢弃。 */
export function mapPrograms(programs: RawEPGProgram[] | undefined): EPGProgram[] {
  const out: EPGProgram[] = [];
  for (const p of programs ?? []) {
    const start = parseEpgTime(p.start ?? "");
    if (!start) continue;
    const end = parseEpgTime(p.stop ?? p.start ?? "");
    if (!end || end.getTime() <= start.getTime()) continue;
    out.push({ id: `epg-${start.getTime()}`, title: p.title || "", start, end });
  }
  return out;
}
