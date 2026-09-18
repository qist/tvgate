/**
 * Document Picture-in-Picture 能力封装。
 *
 * 这里处理两套浏览器 PiP 机制的差异：
 * - Document PiP（`documentPictureInPicture.requestWindow`）：开出可承载完整
 *   React 播放器的子窗口，需要跨窗口复制样式表；
 * - 传统视频 PiP（`video.requestPictureInPicture`）：只有画面。
 * 调用方按"Document PiP 可用 → 视频 PiP 可用"的顺序降级。
 */

/** Document PiP 子窗口默认按播放器可视比例开 16:9。 */
const WINDOW_ASPECT = 16 / 9;
const WINDOW_MIN_WIDTH = 320;
const WINDOW_MAX_WIDTH = 640;
/** 播放器布局尚未完成（rect 为 0）时的保底宽度。 */
const WINDOW_FALLBACK_WIDTH = 480;

// —— W3C Document Picture-in-Picture API 的最小类型面 ——
// 下列成员名（documentPictureInPicture / requestWindow / preferInitialWindowPlacement）
// 是规范定义的字面量，不可改名；这里只声明本仓库实际用到的子集。

type DocumentPictureInPictureOptions = {
  preferInitialWindowPlacement?: boolean;
  width?: number;
  height?: number;
};

/** window.requestWindow 的返回值，用于打开/追踪 PiP 子窗口。 */
type DocumentPictureInPictureController = {
  readonly window: Window | null;
  requestWindow: (options?: DocumentPictureInPictureOptions) => Promise<Window>;
};

/** 控制器挂在 window 全局上；能取到它即代表浏览器实现了该 API。 */
interface WindowWithDocumentPictureInPicture extends Window {
  readonly documentPictureInPicture?: DocumentPictureInPictureController;
}

/** 取 Document PiP 控制器；API 不存在或当前上下文不允许时返回 null。 */
export function getDocumentPictureInPicture(): DocumentPictureInPictureController | null {
  const host = window as WindowWithDocumentPictureInPicture;
  return host.documentPictureInPicture?.requestWindow ? host.documentPictureInPicture : null;
}

// Document PiP 只允许顶层浏览上下文发起，iframe 里 requestWindow 会被拒绝；
// 所以先判顶层上下文，再判 API 本体是否存在。
export function isDocumentPictureInPictureSupported(): boolean {
  const inTopLevelBrowsingContext = window.self === window.top;
  return inTopLevelBrowsingContext && getDocumentPictureInPicture() !== null;
}

export function isPictureInPictureSupported(): boolean {
  // 两套机制（Document PiP / 传统视频 PiP）任一可用即视为支持。
  return isDocumentPictureInPictureSupported() || Boolean(document.pictureInPictureEnabled);
}

/**
 * Document PiP 只允许在顶层浏览上下文发起；iframe 内调用会抛
 * `NotAllowedError`。调用方据此降级到传统视频 PiP。
 */
export function isDocumentPictureInPictureBlockedError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "NotAllowedError";
}

export function isAnyPictureInPictureActive(): boolean {
  return !!(document.pictureInPictureElement || getDocumentPictureInPicture()?.window);
}

export interface DocumentPiPWindowOptions {
  preferInitialWindowPlacement: false;
  width: number;
  height: number;
}

/** 按播放器当前宽度（夹取到 [320, 640]）推导 PiP 子窗口尺寸。 */
export function getDocumentPiPWindowOptions(playerElement: HTMLElement): DocumentPiPWindowOptions {
  const rect = playerElement.getBoundingClientRect();
  const measured = rect.width > 0 ? rect.width : WINDOW_FALLBACK_WIDTH;
  const width = Math.round(Math.min(Math.max(measured, WINDOW_MIN_WIDTH), WINDOW_MAX_WIDTH));

  return {
    preferInitialWindowPlacement: false,
    width,
    height: Math.round(width / WINDOW_ASPECT),
  };
}

/**
 * 把主窗口样式搬进 PiP 子窗口。
 * 同源 <style>/<link> 直接序列化 cssRules 内联复制；跨源样式表读不到
 * cssRules，退回按 href 再挂一遍 <link>（浏览器自行取样式）。
 */
function cloneStyleSheets(targetWindow: Window): void {
  const targetHead = targetWindow.document.head;

  for (const sheet of Array.from(document.styleSheets)) {
    try {
      const cssText = Array.from(sheet.cssRules, (rule) => rule.cssText).join("\n");
      const inline = targetWindow.document.createElement("style");
      inline.textContent = cssText;
      targetHead.appendChild(inline);
    } catch {
      if (!sheet.href) continue;
      const external = targetWindow.document.createElement("link");
      external.rel = "stylesheet";
      external.href = sheet.href;
      external.media = sheet.media.mediaText;
      targetHead.appendChild(external);
    }
  }
}

/** 初始化 PiP 子窗口：同步标题 / 根元素类与数据集 / 配色方案，并挂载样式。 */
export function setupDocumentPiPWindow(targetWindow: Window): void {
  const targetDocument = targetWindow.document;
  const sourceRoot = document.documentElement;
  const targetRoot = targetDocument.documentElement;

  targetDocument.title = document.title;

  // 与主文档共享外观上下文：根类名、性能档位标记、配色方案。
  targetRoot.className = sourceRoot.className;
  const tier = sourceRoot.dataset.performanceTier;
  if (tier) targetRoot.dataset.performanceTier = tier;
  targetRoot.style.colorScheme = sourceRoot.style.colorScheme;

  const targetBody = targetDocument.body;
  // 子窗口本体是全幅黑底容器，滚动与留白交给播放器内部布局处理。
  targetBody.className =
    "player-performance-scope overflow-hidden overscroll-none bg-black text-foreground antialiased";
  targetBody.style.margin = "0";
  targetBody.style.overflow = "hidden";
  targetBody.style.background = "#000";

  // 根元素与 body 都撑满子窗口视口。
  targetRoot.style.width = "100%";
  targetRoot.style.height = "100%";
  targetBody.style.width = "100%";
  targetBody.style.height = "100%";

  cloneStyleSheets(targetWindow);
}
