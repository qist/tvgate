/**
 * EPG 节目单解析 / 查询 / 缝隙填充。
 *
 * 这是播放器侧唯一接触"原始节目单数据"的模块：服务端 XMLTV 原样串、频道键回退、
 * 缺数据频道的占位策略都收敛在这里，让 UI 层只面对统一的 EPGProgram 模型。
 * 所有时间一律按**本地时区**解释：服务端按 date=YYYYMMDD 的本地日切片下发数据，
 * 前端若做跨时区换算，节目会整体偏移、甚至落进错误的日期分组。
 */
import type { Channel, EPGProgram } from "../types/player";

/** 频道号 → 节目列表。键由 getEPGChannelId 决定（epgId/epgName/name）。 */
export type EPGData = Record<string, EPGProgram[]>;

/** 占位节目时长取 2 小时：足以盖住节目单首屏，又不会在真实数据到达前误导太久。 */
const FILLER_DURATION_MS = 2 * 60 * 60 * 1000;

/**
 * XMLTV 风格时间戳：YYYYMMDD[HH[MM[SS]]]，可携带可选时区后缀
 * （" +0800" / "+08:00" 两种形态都要容忍）。后缀参与匹配但解析时丢弃——理由见 parseEpgTime。
 */
const XMLTV_TIMESTAMP_PATTERN = /^\d{8}(?:\d{2}){0,3}(?:\s*[+-]\d{2}:?\d{2})?$/;

/**
 * 服务端 /api/player/epg 的原始节目条目：from/to 仍是 XMLTV 原样串
 * （如 "20260916120000 +0800"），是否带时区后缀不可预期，交给 parseEpgTime 统一消化。
 */
export interface RawEPGProgram {
  from: string;
  to: string;
  title: string;
}

/**
 * 解析某频道在 EPGData 中的键：epgId → epgName → name 逐级回退，空串视同缺失继续往后找。
 * 回退顺序必须与写入侧（mapChannels / 服务端 epgId）完全一致，否则整表查询都会 miss。
 * 第二形参是预留位（当前无需读表），保留以维持调用方签名稳定。
 */
export function getEPGChannelId(channel: Channel, _epgData?: EPGData): string | undefined {
  for (const candidate of [channel.epgId, channel.epgName, channel.name]) {
    if (candidate) return candidate;
  }
  return undefined;
}

/** 给定绝对时刻，返回该频道正在播出的节目（beginsAt <= at < endsAt）；线性扫描取第一个命中，无则 null。 */
export function getCurrentProgram(channelId: string, epgData: EPGData, at: Date): EPGProgram | null {
  const lineup = epgData[channelId];
  if (!lineup || lineup.length === 0) return null;

  const atMs = at.getTime();
  for (const entry of lineup) {
    // 左闭右开：节目切换的临界时刻命中后一档，避免两档同时命中
    if (atMs >= entry.beginsAt.getTime() && atMs < entry.endsAt.getTime()) return entry;
  }
  return null;
}

function createFillerProgram(nowMs: number): EPGProgram {
  const beginsAt = new Date(nowMs);
  const endsAt = new Date(nowMs + FILLER_DURATION_MS);
  return {
    // id 取起点毫秒值：同一批占位共享同一 nowMs，重复合并时 id 天然稳定
    id: `filler-${beginsAt.getTime()}`,
    // 标题刻意留空：展示层统一用 t("excellentProgram") 兜底——写死文案会绕过 i18n，
    // 直接放 key 原文又会被当成普通字符串原样显示
    title: "",
    beginsAt,
    endsAt,
  };
}

/**
 * 用真实 EPG 合并「缝隙填充」占位：真实数据永远优先（哪怕只有一条也不补）；
 * 仅对"尚无任何节目且支持回看"的频道补一段占位，避免节目单整片空白。
 * 真实数据随后到达时，调用方以最新表为底再走一遍合并即可自然覆盖占位。
 */
export function fillEPGGaps(epgData: EPGData, channels: Channel[]): EPGData {
  const merged: EPGData = { ...epgData };
  // 时间原点在循环外取一次：同一批占位共享同一时刻，各自 id（filler-<startMs>）因此一致
  const nowMs = Date.now();

  for (const channel of channels) {
    const epgKey = getEPGChannelId(channel, epgData);
    if (!epgKey) continue;

    const existing = merged[epgKey];
    if (existing && existing.length > 0) continue;

    // 只给支持回看的频道补占位：没有回看，用户根本点不进节目单，填充毫无意义
    const timeshiftCapable = channel.sources.some((source) => source.timeshift && source.timeshiftTemplate);
    if (!timeshiftCapable) continue;

    merged[epgKey] = [createFillerProgram(nowMs)];
  }

  return merged;
}

/** "HH:MM" 形态（当天时刻），恰好 5 字符且含冒号。 */
function looksLikeHourMinute(text: string): boolean {
  return text.length === 5 && text.includes(":");
}

function parseHourMinute(text: string): Date {
  const [hourText, minuteText] = text.split(":");
  const today = new Date();
  today.setHours(Number(hourText), Number(minuteText), 0, 0);
  return today;
}

/**
 * 纯数字 XMLTV 串 → 本地时区 Date。只取前 14 位数字，时区后缀（若存在）自然落在切片之外；
 * 缺省的时/分/秒位补 0。按本地时区解释与服务端 date=YYYYMMDD 的切片语义对齐。
 */
function parseXmltvDigits(text: string): Date {
  const digits = text.slice(0, 14);
  return new Date(
    Number(digits.slice(0, 4)), // 年
    Number(digits.slice(4, 6)) - 1, // 月（Date 构造器按 0 起算）
    Number(digits.slice(6, 8)), // 日
    Number(digits.slice(8, 10) || 0), // 时（短格式缺位 → 0）
    Number(digits.slice(10, 12) || 0), // 分
    Number(digits.slice(12, 14) || 0), // 秒
  );
}

/** 兜底：ISO 串或"空格分隔"的日期时间。后者把首个空格换成 T 再交给 Date 解析。 */
function parseLooseTimestamp(text: string): Date | null {
  // 只替换第一个空格是刻意的：后续空格（如时区前）保持原样，交给 Date 自身容错
  const normalized = text.includes("T") ? text : text.replace(" ", "T");
  const parsed = new Date(normalized);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

/**
 * 任意 EPG 时间 → Date。按形态依次尝试：
 *   1. "HH:MM" —— 当天时刻；
 *   2. XMLTV 数字串（可带时区后缀；后缀忽略、按本地时区解释）；
 *   3. ISO / 空格分隔串。
 * 时区后缀必须容忍：漏掉它会让服务端返回的**每一条**节目都解析失败被丢弃，
 * 表现为"服务端日志显示 EPG 解析完成，前端却完全没有数据"（曾复现的线上问题）。
 */
export function parseEpgTime(value: string): Date | null {
  const text = (value ?? "").trim();
  if (!text) return null;

  if (looksLikeHourMinute(text)) return parseHourMinute(text);
  if (XMLTV_TIMESTAMP_PATTERN.test(text)) return parseXmltvDigits(text);
  return parseLooseTimestamp(text);
}

/** 服务端节目列表 → 播放器模型。起点解析失败、止点解析失败或区间倒置/零长的条目一律丢弃。 */
export function mapPrograms(programs: RawEPGProgram[] | undefined): EPGProgram[] {
  const mapped: EPGProgram[] = [];
  for (const raw of programs ?? []) {
    const beginsAt = parseEpgTime(raw.from ?? "");
    if (!beginsAt) continue;
    // 止点缺失时退回解析起点：由此得到的零长区间会被下一行判为非法丢弃，
    // 等价于"无止点的条目视为脏数据"
    const endsAt = parseEpgTime(raw.to ?? raw.from ?? "");
    if (!endsAt || endsAt.getTime() <= beginsAt.getTime()) continue;
    // id 用起点毫秒值：同频道内节目按起点天然唯一，可直接当列表 key
    mapped.push({ id: `epg-${beginsAt.getTime()}`, title: raw.title || "", beginsAt, endsAt });
  }
  return mapped;
}
