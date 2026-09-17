/**
 * 频道深链（地址栏 hash 可分享）clean-room 重写。
 * 选台后将当前频道写进 URL hash（#频道名 或 #频道ID），刷新/分享可恢复。
 */
import type { Channel } from "../types/player";

function readHash(): string {
  const raw = window.location.hash.replace(/^#/, "");
  return raw ? decodeURIComponent(raw) : "";
}

/**
 * 在频道列表中查找与当前 URL hash 匹配的频道。
 * 优先按 name 精确/忽略大小写匹配；若 name 在非当前频道中重复（歧义），退回按 id 匹配。
 */
export function findDeepLinkChannel(channels: Channel[]): Channel | null {
  const hash = readHash();
  if (!hash) return null;

  const byId = channels.find((c) => c.id === hash);
  if (byId) return byId;

  const byName = channels.filter((c) => c.name === hash);
  if (byName.length === 1) return byName[0];

  const byNameCi = channels.filter((c) => c.name.toLowerCase() === hash.toLowerCase());
  if (byNameCi.length === 1) return byNameCi[0];

  // name 歧义或不存在：hash 可能是 id 的另一种写法，已上面的 byId 覆盖
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
