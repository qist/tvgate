import { clsx } from "clsx";
import * as React from "react";

export interface SwitchProps extends Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "onChange"> {
  checked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
}

/* 开关配色只在 on/off 间切换，抽成查表避免 JSX 里堆三元。 */
const TRACK_TONE = {
  on: "border-primary/30 bg-primary",
  off: "border-input/80 bg-muted/90",
} as const;

export const Switch = React.forwardRef<HTMLButtonElement, SwitchProps>(function Switch(
  { className, checked = false, onCheckedChange, disabled, ...rest },
  ref,
) {
  const flip = () => {
    if (!disabled) onCheckedChange?.(!checked);
  };

  const activateViaKeyboard = (event: React.KeyboardEvent<HTMLButtonElement>) => {
    if (event.key !== " " && event.key !== "Enter") return;
    event.preventDefault();
    flip();
  };

  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      data-state={checked ? "checked" : "unchecked"}
      disabled={disabled}
      ref={ref}
      onClick={flip}
      onKeyDown={activateViaKeyboard}
      {...rest}
      className={clsx(
        "relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border",
        "transition-[background-color,border-color,box-shadow] duration-200 motion-reduce:transition-none",
        "shadow-[inset_0_1px_3px_rgba(15,23,42,0.16)]",
        "disabled:cursor-not-allowed disabled:opacity-50",
        checked ? TRACK_TONE.on : TRACK_TONE.off,
        className,
      )}
    >
      <span
        className={clsx(
          "ml-0.5 inline-block h-5 w-5 rounded-full bg-white shadow-md ring-1 ring-black/5",
          "transition-transform duration-200 motion-reduce:transition-none",
          checked ? "translate-x-5" : "translate-x-0",
        )}
      />
    </button>
  );
});

Switch.displayName = "Switch";
