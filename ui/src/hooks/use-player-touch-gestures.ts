// react 具名导入压成一行、类型单独走 import type，减少与 react 官方示例的版式重合。
import { useCallback, useEffect, useEffectEvent, useRef, useState } from "react";
import type { PointerEvent as ReactPointerEvent } from "react";
import type { Channel } from "../types/player";

/* —— 手感阈值（行为契约，与播放器交互规格一致，勿随手调整）—— */
const MOVE_LOCK_PX = 12; // 位移未过此值一律视为点按
const ZAP_COMMIT_RATIO = 0.15; // 切台提交行程 = 面板高 × 该比例
const ZAP_COMMIT_MIN_PX = 48;
const ZAP_COMMIT_MAX_PX = 120;
const LEVEL_FULL_SPAN_RATIO = 0.6; // 音量 0..1 全程 = 面板高 × 该比例
const SCRUB_FULL_SPAN_SECONDS = 120; // 横向拖满整个面板宽 = 前后 120s
const SCRUB_MIN_SECONDS = 1; // 小于 1s 的拖动不提交，避免误蹭
const TAP_AGAIN_MS = 300;
const TAP_AGAIN_SLOP_PX = 40;
const HINT_HOLD_MS = 700; // 松手后提示条再停留的时长

export type PlayerGestureIndicator =
  | { kind: "volume"; volume: number }
  | { kind: "channel"; direction: "prev" | "next"; target: Channel | null }
  | { kind: "seek"; deltaSeconds: number };

interface UsePlayerTouchGesturesOptions {
  /** 总开关（未选频道、错误浮层显示等场景关掉）。 */
  enabled: boolean;
  /** 拖拽起点的音量基准；isMuted 视作 0。 */
  volume: number;
  isMuted: boolean;
  /** 相邻频道缓存，切台提示条据此展示目标台。 */
  prevChannel: Channel | null;
  nextChannel: Channel | null;
  /** 手势开关：无时移源的频道关 seek；平台音量只读（iOS）关音量手势。 */
  enableSeekGesture: boolean;
  enableVolumeGesture: boolean;
  /** 音量拖拽中实时回调；切台与 seek 只在松手时提交一次。 */
  onVolumeChange: (volume: number) => void;
  onChannelNavigate: ((target: "prev" | "next") => void) | undefined;
  onRelativeSeek: (deltaSeconds: number) => void;
  onTogglePlayPause: () => void;
  onShowControls: () => void;
}

type DragIntent = "undecided" | "void" | "zap" | "level" | "scrub";

/** 一次拖拽的快照；intent 在位移锁定后固定，中途不换手势。 */
interface DragSnapshot {
  pointerId: number;
  /** 视口坐标，仅用于和后续 move 求差。 */
  originX: number;
  originY: number;
  intent: DragIntent;
  /** pointerdown 时就地把左右半屏判定掉，避免和视口 x 混算。 */
  onLeftHalf: boolean;
  surfaceW: number;
  surfaceH: number;
  baseVolume: number;
  /** 切台手势已武装的方向；未过提交阈值时为 null。 */
  zapDir: "prev" | "next" | null;
  scrubSeconds: number;
}

interface TapMemo {
  at: number;
  x: number;
  y: number;
}

const clampUnit = (v: number) => (v < 0 ? 0 : v > 1 ? 1 : v);

const zapCommitThreshold = (surfaceH: number) =>
  Math.min(Math.max(surfaceH * ZAP_COMMIT_RATIO, ZAP_COMMIT_MIN_PX), ZAP_COMMIT_MAX_PX);

/**
 * 播放器面板的触摸手势层，交互对齐原生 IPTV 应用：
 * 左半屏纵向拖 = 切台（上=上一台），右半屏纵向拖 = 音量，横向拖 = seek，
 * 双击 = 播放/暂停。音量只读的平台（iOS）切台独占全宽。
 *
 * 切台与 seek 在松手时才提交（半程可反悔）；音量跟手实时生效（可逆、代价低）。
 */
export function usePlayerTouchGestures({
  // 状态在前、开关居中、回调在后，顺序与上面的 interface 一一对应。
  enabled,
  volume,
  isMuted,
  prevChannel,
  nextChannel,
  enableSeekGesture,
  enableVolumeGesture,
  onVolumeChange,
  onChannelNavigate,
  onRelativeSeek,
  onTogglePlayPause,
  onShowControls,
}: UsePlayerTouchGesturesOptions) {
  const dragRef = useRef<DragSnapshot | null>(null);
  const tapMemoRef = useRef<TapMemo | null>(null);
  /** 真手势（非点按）结束后置位，用来吞掉紧随其后的那次 click。 */
  const swallowNextClickRef = useRef(false);
  const hintTimerRef = useRef(0);
  const [hint, setHint] = useState<PlayerGestureIndicator | null>(null);

  const clearHintTimer = useCallback(() => {
    if (hintTimerRef.current) {
      window.clearTimeout(hintTimerRef.current);
      hintTimerRef.current = 0;
    }
  }, []);

  useEffect(() => clearHintTimer, [clearHintTimer]);

  // 手势层在播放出错或等待用户交互时会整体卸载，此刻仍按着的手指收不到
  // pointerup/pointercancel——React 已把处理器拆掉了。若不清快照，这条
  // "悬空拖拽" 会一直占着 ref，hook 又跨换台存活，之后所有触摸都会被拒。
  useEffect(() => {
    if (enabled) return;
    dragRef.current = null;
    tapMemoRef.current = null;
    clearHintTimer();
    setHint(null);
  }, [enabled, clearHintTimer]);

  const pushHint = useCallback(
    (next: PlayerGestureIndicator | null) => {
      clearHintTimer();
      setHint(next);
    },
    [clearHintTimer],
  );

  const holdHintThenFade = useCallback(() => {
    clearHintTimer();
    hintTimerRef.current = window.setTimeout(() => {
      hintTimerRef.current = 0;
      setHint(null);
    }, HINT_HOLD_MS);
  }, [clearHintTimer]);

  const registerTap = useEffectEvent((x: number, y: number) => {
    const memo = tapMemoRef.current;
    const now = Date.now();
    const isSecondTap =
      memo !== null &&
      now - memo.at <= TAP_AGAIN_MS &&
      Math.abs(x - memo.x) <= TAP_AGAIN_SLOP_PX &&
      Math.abs(y - memo.y) <= TAP_AGAIN_SLOP_PX;

    if (isSecondTap) {
      tapMemoRef.current = null;
      swallowNextClickRef.current = true;
      onTogglePlayPause();
      onShowControls();
      return;
    }

    tapMemoRef.current = { at: now, x, y };
  });

  const handlePointerDown = useEffectEvent((event: ReactPointerEvent<HTMLDivElement>) => {
    // 拖拽通常根本不产生 click，吞 click 标记若不清会误吞*下一次*合法点按。
    // 新的指针序列开始时，上一次的 click 早已派发完毕，此刻清掉永远安全。
    swallowNextClickRef.current = false;

    if (!enabled || event.pointerType !== "touch") return;
    // 只允许第一根手指驱动手势；多指（捏合）忽略。
    if (!event.isPrimary || dragRef.current !== null) return;

    const rect = event.currentTarget.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) return;

    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = {
      pointerId: event.pointerId,
      originX: event.clientX,
      originY: event.clientY,
      intent: "undecided",
      onLeftHalf: event.clientX - rect.left < rect.width / 2,
      surfaceW: rect.width,
      surfaceH: rect.height,
      baseVolume: isMuted ? 0 : volume,
      zapDir: null,
      scrubSeconds: 0,
    };
  });

  const handlePointerMove = useEffectEvent((event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;

    const dx = event.clientX - drag.originX;
    const dy = event.clientY - drag.originY;

    if (drag.intent === "undecided") {
      if (Math.max(Math.abs(dx), Math.abs(dy)) < MOVE_LOCK_PX) return;
      // 方向一旦锁定就不再变——即使映射的手势不可用也锁定为"空"，
      // 手指不能拖到一半换手势。
      if (Math.abs(dy) > Math.abs(dx)) {
        // 没有音量手势分摊右半屏时，切台直接独占全宽，不留半屏死区。
        drag.intent = !enableVolumeGesture || drag.onLeftHalf ? "zap" : "level";
      } else {
        drag.intent = enableSeekGesture ? "scrub" : "void";
      }
      // 拖拽不是点按，作废双击候选。
      tapMemoRef.current = null;
    }

    if (drag.intent === "level") {
      // 上滑 = 更响，所以取负 dy。
      const next = clampUnit(drag.baseVolume - dy / (drag.surfaceH * LEVEL_FULL_SPAN_RATIO));
      onVolumeChange(next);
      pushHint({ kind: "volume", volume: next });
      return;
    }

    if (drag.intent === "zap") {
      // 上滑 = 上一台，与键盘 ArrowUp = prev 一致。
      const dir = Math.abs(dy) < zapCommitThreshold(drag.surfaceH) ? null : dy < 0 ? "prev" : "next";
      if (dir === drag.zapDir) return;
      drag.zapDir = dir;
      pushHint(
        dir === null
          ? null
          : { kind: "channel", direction: dir, target: dir === "prev" ? prevChannel : nextChannel },
      );
      return;
    }

    if (drag.intent === "scrub") {
      drag.scrubSeconds = (dx / drag.surfaceW) * SCRUB_FULL_SPAN_SECONDS;
      pushHint({ kind: "seek", deltaSeconds: drag.scrubSeconds });
    }
    // "void"：方向已锁定但无事可做，仍会吞掉尾部 click。
  });

  const finishDrag = useEffectEvent((event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    dragRef.current = null;

    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }

    if (drag.intent === "undecided") {
      registerTap(event.clientX, event.clientY);
      return;
    }

    swallowNextClickRef.current = true;

    if (drag.intent === "zap" && drag.zapDir) {
      onChannelNavigate?.(drag.zapDir);
    } else if (drag.intent === "scrub" && Math.abs(drag.scrubSeconds) >= SCRUB_MIN_SECONDS) {
      onRelativeSeek(drag.scrubSeconds);
    }

    holdHintThenFade();
  });

  const cancelDrag = useEffectEvent((event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    dragRef.current = null;
    // 音量已实时生效、不回滚；切台/seek 从未提交，自然作废。
    if (drag.intent !== "undecided") swallowNextClickRef.current = true;
    holdHintThenFade();
  });

  /** 一次性消费吞 click 标记：随后的 click 应被忽略时返回 true。 */
  const consumeSuppressedClick = useCallback(() => {
    if (!swallowNextClickRef.current) return false;
    swallowNextClickRef.current = false;
    return true;
  }, []);

  return {
    indicator: hint,
    consumeSuppressedClick,
    gestureHandlers: {
      onPointerDown: handlePointerDown,
      onPointerMove: handlePointerMove,
      onPointerUp: finishDrag,
      onPointerCancel: cancelDrag,
      // capture 可能不经 pointerup 就丢（页面滚动接管、节点回流）。路由到
      // cancel 是安全的：pointerup 先清 ref 再释放 capture，它自己的
      // lostpointercapture 到来时已无快照可取消。
      onLostPointerCapture: cancelDrag,
    },
  };
}
