import { clsx } from "clsx";
import * as React from "react";

/*
 * 卡片族组件：结构固定（面/头/题/述/容/脚），类名只做最小排版。
 * 卡面质感（半透明 + 轻模糊 + 极淡投影）定义在 index.css 的 @layer components
 * （.ui-card），页面上的背景/阴影工具类（如播放页的 bg-white/72）可正常覆盖。
 */

const Card = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(({ className, ...rest }, ref) => (
  // min-w-0：Card 常作 grid/flex 子项，默认 min-width:auto 会被内部宽表格/代码块撑开，
  // 导致整页横向溢出（表格自身的滚动容器随之失效）；可收缩后滚动交给内部容器。
  <div ref={ref} className={clsx("ui-card", "min-w-0 rounded-2xl border text-card-foreground", className)} {...rest} />
));
Card.displayName = "Card";

const CardHeader = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...rest }, ref) => (
    <div ref={ref} className={clsx("flex flex-col gap-1.5 p-6", className)} {...rest} />
  ),
);
CardHeader.displayName = "CardHeader";

const CardTitle = React.forwardRef<HTMLParagraphElement, React.HTMLAttributes<HTMLHeadingElement>>(
  ({ className, ...rest }, ref) => (
    <h3
      ref={ref}
      className={clsx("font-semibold text-2xl leading-none", "tracking-[-0.02em]", className)}
      {...rest}
    />
  ),
);
CardTitle.displayName = "CardTitle";

const CardDescription = React.forwardRef<HTMLParagraphElement, React.HTMLAttributes<HTMLParagraphElement>>(
  ({ className, ...rest }, ref) => (
    <p ref={ref} className={clsx("text-muted-foreground", "text-sm leading-5", className)} {...rest} />
  ),
);
CardDescription.displayName = "CardDescription";

const CardContent = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...rest }, ref) => <div ref={ref} className={clsx("px-6 pb-6", className)} {...rest} />,
);
CardContent.displayName = "CardContent";

const CardFooter = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...rest }, ref) => (
    <div ref={ref} className={clsx("flex items-center px-6 pb-6", className)} {...rest} />
  ),
);
CardFooter.displayName = "CardFooter";

export { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle };
