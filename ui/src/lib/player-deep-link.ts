import type { Channel } from "../types/player";

function readHashToken(): string {
  const raw = window.location.hash.startsWith("#") ? window.location.hash.slice(1) : window.location.hash;
  if (!raw) {
    return "";
  }
  try {
    return decodeURIComponent(raw).trim();
  } catch {
    return raw.trim();
  }
}

/** 频道的分组主名（首个分组，trim）。 */
function primaryGroup(channel: Channel): string {
  return channel.groups[0]?.trim() ?? "";
}

/**
 * 组名+频道名 → 12 位十六进制唯一标识（48 位：FNV-1a + djb2 各取一部分）。
 * 与频道 key 视觉一致，但不含明文、且不随源地址变化（改名/换源后仍稳定，
 * 只要分组名和频道名不变）。
 */
function encodeDeepLinkId(group: string, name: string): string {
  const input = `${group}/${name}`;
  let h1 = 0x811c9dc5; // FNV-1a 32bit
  for (let i = 0; i < input.length; i++) {
    h1 ^= input.charCodeAt(i);
    h1 = Math.imul(h1, 0x01000193);
  }
  let h2 = 5381; // djb2 32bit
  for (let i = 0; i < input.length; i++) {
    h2 = (Math.imul(h2, 33) ^ input.charCodeAt(i)) >>> 0;
  }
  return (
    (h1 >>> 0).toString(16).padStart(8, "0") + ((h2 >>> 0) & 0xffff).toString(16).padStart(4, "0")
  );
}

function findChannelById(channels: Channel[], channelId: string): Channel | undefined {
  return channels.find((channel) => channel.id === channelId);
}

function findChannelByDeepLinkId(channels: Channel[], linkId: string): Channel | undefined {
  return channels.find((channel) => encodeDeepLinkId(primaryGroup(channel), channel.name.trim()) === linkId);
}

function findChannelByGroupAndName(channels: Channel[], group: string, name: string): Channel | undefined {
  const g = group.trim().toLowerCase();
  const n = name.trim().toLowerCase();
  if (!g || !n) {
    return undefined;
  }
  return channels.find(
    (channel) =>
      channel.name.trim().toLowerCase() === n && channel.groups.some((og) => og.trim().toLowerCase() === g),
  );
}

function findChannelByName(channels: Channel[], channelName: string): Channel | undefined {
  const normalized = channelName.trim().toLowerCase();
  if (!normalized) {
    return undefined;
  }
  return channels.find((channel) => channel.name.trim().toLowerCase() === normalized);
}

/**
 * Resolve the deep-link target channel from the page URL hash.
 *
 * Supports `/player#<token>` where token is any of:
 * - 组名+频道名 的编码标识（12 位 hex，当前版本写入的形式）
 * - a channel id（不透明频道 key，含历史链接与组内同名兜底）
 * - `组名/频道名` 明文（过渡期链接）
 * - a channel name 明文（早期链接）
 *
 * Returns `undefined` when the token is absent or does not match any channel.
 */
export function findDeepLinkChannel(channels: Channel[]): Channel | undefined {
  const token = readHashToken();
  if (!token) {
    return undefined;
  }
  const byId = findChannelById(channels, token);
  if (byId) {
    return byId;
  }
  const byLink = findChannelByDeepLinkId(channels, token.toLowerCase());
  if (byLink) {
    return byLink;
  }
  const slash = token.indexOf("/");
  if (slash > 0) {
    const byGroup = findChannelByGroupAndName(channels, token.slice(0, slash), token.slice(slash + 1));
    if (byGroup) {
      return byGroup;
    }
  }
  return findChannelByName(channels, token);
}

/**
 * Hash token to write for `channel`：组名+频道名 编码成的唯一标识（不暴露明文）。
 * 同组同名无法区分的频道退回频道 key（同样是不透明 hex）。
 */
export function channelDeepLinkToken(channel: Channel, channels: Channel[]): string {
  const name = channel.name.trim();
  if (!name) {
    return channel.id;
  }
  const group = primaryGroup(channel);
  let sameCount = 0;
  for (const other of channels) {
    if (other.name.trim() === name && primaryGroup(other) === group) {
      sameCount++;
      if (sameCount > 1) {
        return channel.id;
      }
    }
  }
  return encodeDeepLinkId(group, name);
}

/**
 * Keep the address bar in sync with the currently playing channel by rewriting
 * the URL to `#<组名+频道名编码标识>`（组内同名等兜底场景为 `#<频道key>`）。
 *
 * 其他 query 参数（notably `my_token`）全部保留。使用绝对 URL，避免服务端
 * 注入的 `<base href>` 影响 `replaceState` 的写入位置。
 */
export function syncChannelDeepLink(channel: Channel, channels: Channel[]): void {
  try {
    const url = new URL(window.location.href);
    const nextUrl = `${url.origin}${url.pathname}${url.search}#${channelDeepLinkToken(channel, channels)}`;
    if (nextUrl === window.location.href) {
      return;
    }
    window.history.replaceState(window.history.state, "", nextUrl);
  } catch {
    // A hardened browser may reject URL/history writes; playback must not break.
  }
}
