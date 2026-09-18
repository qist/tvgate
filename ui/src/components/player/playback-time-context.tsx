/**
 * 播放位置上下文。
 *
 * 为什么用 Context 而不是逐层透传：播放时间随帧推进频繁变化，用 prop 链传递会
 * 迫使整条中间组件链跟着重渲染；Context 让时间直达真正显示它的叶子组件，
 * 中间层保持稳定。单位为秒，相对媒体原点（0 即开头，可能大于时钟墙值，如时移场景）。
 */
import { createContext, useContext, type ReactNode } from "react";

/** Provider 缺席（单测、预览页等）时的回退值：从媒体原点起算的 0 秒，消费方不会崩。 */
const OUTSIDE_PROVIDER_POSITION_SECONDS = 0;

const PositionContext = createContext(OUTSIDE_PROVIDER_POSITION_SECONDS);

interface PlaybackTimeProviderProps {
  children: ReactNode;
  value: number;
}

export function PlaybackTimeProvider({ children, value }: PlaybackTimeProviderProps) {
  return <PositionContext value={value}>{children}</PositionContext>;
}

export function usePlaybackTime(): number {
  return useContext(PositionContext);
}
