// 类型导入与运行时导入分行书写，保持"类型 → 工具 → 子组件"的自上而下顺序。
import type { HTMLAttributes } from "react";
import { clsx } from "clsx";
import { Switch } from "./switch";

export interface LabeledSwitchProps extends HTMLAttributes<HTMLDivElement> {
  /** 左侧文案，同时充当右侧 Switch 的可访问名称。 */
  label: string;
  checked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
  disabled?: boolean;
  labelClassName?: string;
  switchClassName?: string;
}

/**
 * 单行开关：左侧文案、右侧 Switch。
 * label 同时充当 Switch 的可访问名称（aria-label），行内其余属性透传给容器。
 */
export function LabeledSwitch({
  label,
  checked,
  onCheckedChange,
  disabled,
  className,
  labelClassName,
  switchClassName,
  ...rest
}: LabeledSwitchProps) {
  // 类名先在函数体内合成，JSX 只保留语义清晰的变量，方便阅读与排查样式来源。
  const containerClassName = clsx("flex items-center justify-between", className);
  const captionClassName = clsx("min-w-0", labelClassName);

  return (
    <div className={containerClassName} {...rest}>
      <span className={captionClassName}>{label}</span>
      {/* aria-label 必须与左侧文案一致，读屏用户才能理解开关含义。 */}
      <Switch
        disabled={disabled}
        checked={checked}
        onCheckedChange={onCheckedChange}
        className={switchClassName}
        aria-label={label}
      />
    </div>
  );
}
