/**
 * 频道深链（地址栏 hash 可分享）。
 * 选台后将当前频道写进 URL hash（#频道名 或 #频道ID），刷新/分享可恢复。
 * 反过来也要能认**外部给进来的地址**：后台连接列表/播放页复制出来的地址可能是
 * `/pp/<线路key>`（服务端会把它重写成 `/pp#<线路key>`），而线路 key 不一定是首线路的那个，
 * 所以这里除了频道 id/名称，还要认**任意线路 key** 与服务端稳定 id。
 */
import type { Channel } from "../types/player";

function readHash(): string {
  const raw = window.location.hash.replace(/^#/, "");
  return raw ? decodeURIComponent(raw) : "";
}

/** 深链解析结果：目标频道 + 该命中的线路位次（0 起；未指定线路时为 0）。 */
export interface DeepLinkTarget {
  channel: Channel;
  /** 命中的线路位次；按频道 id/名称命中（未指明线路）时为 0。 */
  sourceIndex: number;
}

/**
 * 在频道列表中查找与当前 URL hash 匹配的频道（含线路）：
 *   ① 频道 id（= 记录级 key，即首线路 key，界面/自身写出的 hash 就是它）
 *   ② 服务端稳定 id（多线路频道聚合后不变，接口里叫 id）
 *   ③ 任意线路 key（`/pp/<线路key>`、后台连接列表里复制的地址）→ 同时定位到该线路
 *   ④ 频道名（唯一，或忽略大小写唯一）——名称有歧义时宁可不跳，避免指到别的同名台
 */
export function findDeepLinkTarget(channels: Channel[]): DeepLinkTarget | null {
  const hash = readHash();
  if (!hash) return null;

  const byId = channels.find((c) => c.id === hash);
  if (byId) return { channel: byId, sourceIndex: 0 };

  const byServerId = channels.find((c) => c.serverId && c.serverId === hash);
  if (byServerId) return { channel: byServerId, sourceIndex: 0 };

  for (const channel of channels) {
    const index = channel.sources.findIndex((source) => source.key === hash);
    if (index >= 0) return { channel, sourceIndex: index };
  }

  const byName = channels.filter((c) => c.name === hash);
  if (byName.length === 1) return { channel: byName[0], sourceIndex: 0 };

  const byNameCi = channels.filter((c) => c.name.toLowerCase() === hash.toLowerCase());
  if (byNameCi.length === 1) return { channel: byNameCi[0], sourceIndex: 0 };

  return null;
}

/**
 * 把当前频道同步到地址栏 hash，不新增历史记录。
 * 频道名在列表中唯一时用 name（更可读），否则用 id（避免歧义）。
 */
export function syncChannelDeepLink(channel: Channel, allChannels: Channel[]): void {
  const nameAmbiguous = allChannels.some(
    (c) => c.id !== channel.id && c.name === channel.name,
  );
  const token = nameAmbiguous ? channel.id : channel.name;
  const next = `#${encodeURIComponent(token)}`;
  if (next === window.location.hash) return;

  try {
    window.history.replaceState(null, "", next);
  } catch {
    // 忽略：某些嵌入环境不允许修改 history
  }
}
