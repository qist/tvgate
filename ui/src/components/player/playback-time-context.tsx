/**
 * 播放位置时间上下文。
 * 让深层子组件能读取当前播放时间（秒，相对媒体原点），无需逐层透传 prop。
 */
import { createContext, useContext, type ReactNode } from "react";

const PlaybackTimeContext = createContext(0);

interface PlaybackTimeProviderProps {
  children: ReactNode;
  value: number;
}

export function PlaybackTimeProvider({ children, value }: PlaybackTimeProviderProps) {
  return <PlaybackTimeContext value={value}>{children}</PlaybackTimeContext>;
}

export function usePlaybackTime(): number {
  return useContext(PlaybackTimeContext);
}
