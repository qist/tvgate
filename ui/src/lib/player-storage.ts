/**
 * 播放器偏好本地持久化。
 * 仅用标准 localStorage，所有读写带异常保护，避免隐私模式/不可用环境抛错。
 * 对外函数签名与旧版保持一致，供 player.tsx 及组件无缝替换。
 */

const PREFIX = "tvgate-player-";

function readRaw(key: string): string | null {
  try {
    return window.localStorage.getItem(PREFIX + key);
  } catch {
    return null;
  }
}

function writeRaw(key: string, value: string): void {
  try {
    window.localStorage.setItem(PREFIX + key, value);
  } catch {
    // 忽略：隐私模式 / 配额 / 不可用
  }
}

function removeRaw(key: string): void {
  try {
    window.localStorage.removeItem(PREFIX + key);
  } catch {
    // 忽略：隐私模式 / 不可用
  }
}

function readBool(key: string, fallback: boolean): boolean {
  const v = readRaw(key);
  if (v === null) return fallback;
  return v === "true";
}

function writeBool(key: string, value: boolean): void {
  writeRaw(key, value ? "true" : "false");
}

function readInt(key: string, fallback: number): number {
  const v = readRaw(key);
  if (v === null) return fallback;
  const n = Number.parseInt(v, 10);
  return Number.isFinite(n) ? n : fallback;
}

// ---- 最后播放频道 / 线路 ----
export function getLastChannelId(): string | null {
  return readRaw("last-channel-id");
}
export function saveLastChannelId(id: string): void {
  writeRaw("last-channel-id", id);
}

export function getLastSourceIndex(id: string): number {
  return readInt(`last-source-index:${id}`, 0);
}
export function saveLastSourceIndex(id: string, index: number): void {
  writeRaw(`last-source-index:${id}`, String(index));
}

// ---- 侧栏显隐 ----
/**
 * 侧边栏（节目栏）是否显示：**默认不显示**（设计：点播放画面才出现，选完自动隐藏），
 * 故不做持久化——避免上次的展开状态让它在打开播放页时自己冒出来。
 */
export function getSidebarVisible(): boolean {
  return false;
}

// ---- 无缝换台 ----
export function getSeamlessSwitch(): boolean {
  return readBool("seamless-switch", false);
}
export function saveSeamlessSwitch(enabled: boolean): void {
  writeBool("seamless-switch", enabled);
}

// ---- 自动反交错 ----
export function getAutoDeinterlace(): boolean {
  return readBool("auto-deinterlace", false);
}
export function saveAutoDeinterlace(enabled: boolean): void {
  writeBool("auto-deinterlace", enabled);
}

// ---- 画质增强 ----
export function getPictureEnhancement(): boolean {
  return readBool("picture-enhancement", false);
}
export function savePictureEnhancement(enabled: boolean): void {
  writeBool("picture-enhancement", enabled);
}

// ---- 音量 / 静音 ----
function readFloat(key: string, fallback: number): number {
  const v = readRaw(key);
  if (v === null) return fallback;
  const n = Number.parseFloat(v);
  return Number.isFinite(n) ? n : fallback;
}

export function getVolume(): number {
  const v = readFloat("volume", 1);
  return v < 0 || v > 1 ? 1 : v;
}
export function saveVolume(volume: number): void {
  writeRaw("volume", String(Math.min(1, Math.max(0, volume))));
}
export function getMuted(): boolean {
  return readBool("muted", false);
}
export function saveMuted(muted: boolean): void {
  writeBool("muted", muted);
}

// ---- 频道分组选择 ----
/**
 * 侧边栏选中的频道分组（跨会话记忆，null = 全部）。
 *
 * ⚠️ 读取时**不校验组是否存在**：首屏频道/分组元数据还没到达，组列表是空的，
 * 用空列表校验会把有效记忆误判为失效并回落"全部"（这正是"分组不记忆、每次都要重选"
 * 的成因）。有效性由使用方在分组列表加载完成后判断。
 */
export function getSelectedGroup(): string | null {
  return readRaw("channel-group");
}

/** 记住选中的分组；传 null 表示"全部"（清除记忆）。 */
export function saveSelectedGroup(group: string | null): void {
  if (group === null) {
    removeRaw("channel-group");
  } else {
    writeRaw("channel-group", group);
  }
}

// ---- 软解音频声道模式 ----
/** mono = 左右合成单声道（分离声道源在只出一路声道的设备上也能听到两边内容）。 */
export function getAudioChannelMode(): "stereo" | "mono" {
  return readRaw("audio-channel-mode") === "mono" ? "mono" : "stereo";
}
export function saveAudioChannelMode(mode: "stereo" | "mono"): void {
  writeRaw("audio-channel-mode", mode);
}
