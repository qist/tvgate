/**
 * 播放器前端的平台判定。
 * 平台标记由入口页（player.html）按 UA 写入 <html data-player-platform>；
 * 这里只读标记、不做 UA 探测——探测时机与结果缓存都由入口页负责。
 */

function platformTag(): string | undefined {
  return document.documentElement.dataset.playerPlatform;
}

/** iOS / iPadOS：音量级别归硬件按键独占，网页只保留 muted 能力。 */
export function isIOS(): boolean {
  return platformTag() === "ios";
}

/** LG webOS 电视内置浏览器：走平台原生媒体管线。 */
export function isLGWebOS(): boolean {
  return platformTag() === "lg-webos";
}

/**
 * HTMLMediaElement.volume 是否真的影响播放。
 *
 * iOS/iPadOS 忽略 volume 写入（级别属于硬件键），muted 仍可写、静音可用。
 * 这个能力没法做特性探测：对游离元素赋 volume 再读回，值不变，探测会误报
 * "支持"，实际播放却不生效。因此复用入口页写入的平台标记——它同时覆盖
 * iOS 套壳浏览器（CriOS、FxiOS 等）和自称 MacIntel 的 iPadOS。
 */
export function isVolumeControlSupported(): boolean {
  return !isIOS();
}
