/**
 * 频道台标（列表行 / 播放浮层共用）。
 *
 * 为什么不直接在 <img onError> 里写内联 display:none：
 * React 复用的同一个 <img> 元素在切台时只换 src，内联样式不会被清掉——
 * 一次瞬时失败（弱网、首屏上千个请求挤占连接）就让台标**永久隐藏**，
 * 切台/换台标都不恢复，必须刷新页面。这里把失败态交给 React：
 * src 变化即重置；失败先按退避重试 1 次，仍失败才收起整块（不留空壳深色底）。
 */
import { useEffect, useRef, useState } from "react";

/** 失败后退避重试间隔（毫秒）：只重试一次，避免弱网下反复打请求。 */
const RETRY_DELAY_MS = 1500;

interface ChannelLogoProps {
  src: string;
  alt: string;
  /** 台标底盘容器类（列表里是深色底）；省略则只渲染 <img>（浮层用）。 */
  className?: string;
  /** <img> 自身类。 */
  imgClassName?: string;
  /** 列表内开启浏览器原生懒加载：上千行只在可视区附近真正取图。 */
  lazy?: boolean;
}

export function ChannelLogo({ src, alt, className, imgClassName, lazy }: ChannelLogoProps) {
  const [attempt, setAttempt] = useState(0);
  const [failed, setFailed] = useState(false);
  const timerRef = useRef(0);

  // 换台标（src 变）或组件重挂载：清掉上一张的失败态
  useEffect(() => {
    setFailed(false);
    setAttempt(0);
    return () => window.clearTimeout(timerRef.current);
  }, [src]);

  const handleError = () => {
    if (attempt < 1) {
      window.clearTimeout(timerRef.current);
      timerRef.current = window.setTimeout(() => setAttempt((value) => value + 1), RETRY_DELAY_MS);
      return;
    }
    setFailed(true);
  };

  if (failed) return null;

  const image = (
    <img
      key={attempt}
      src={src}
      alt={alt}
      referrerPolicy="no-referrer"
      loading={lazy ? "lazy" : undefined}
      decoding="async"
      className={imgClassName}
      onError={handleError}
    />
  );

  if (!className) return image;
  return <div className={className}>{image}</div>;
}
