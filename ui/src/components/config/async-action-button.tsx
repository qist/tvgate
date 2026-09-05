import { Loader2 } from "lucide-react";
import { useState } from "react";
import { Button, type ButtonProps } from "@/components/ui/button";

interface AsyncActionButtonProps extends ButtonProps {
  /** 点击执行的异步动作；执行期间按钮转圈并禁用，防止重复提交 */
  action: () => Promise<void> | void;
  /** 执行中的按钮文案（默认"处理中…"） */
  busyText?: string;
}

/** 带加载状态的按钮：执行 action 期间显示转圈 + busyText 并禁用。 */
export function AsyncActionButton({ action, busyText = "处理中…", disabled, children, ...rest }: AsyncActionButtonProps) {
  const [busy, setBusy] = useState(false);
  return (
    <Button
      {...rest}
      disabled={disabled || busy}
      onClick={async () => {
        setBusy(true);
        try {
          await action();
        } finally {
          setBusy(false);
        }
      }}
    >
      {busy ? (
        <>
          <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden />
          {busyText}
        </>
      ) : (
        children
      )}
    </Button>
  );
}
