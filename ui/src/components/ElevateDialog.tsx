import { useState } from "react";
import { Lock } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { unlock } from "@/api/elevate";

/** 二次验证弹窗：敏感操作前重输登录密码（成功后 10 分钟内免验） */
export function ElevateDialog({ onDone, onClose }: { onDone: () => void; onClose?: () => void }) {
  const [pwd, setPwd] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const submit = async () => {
    if (!pwd || busy) return;
    setBusy(true);
    setErr("");
    try {
      await unlock(pwd);
      onDone();
    } catch (e) {
      setErr((e as Error).message || "验证失败");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
      <div className="w-full max-w-sm rounded-[var(--radius-lg)] border bg-card p-6 shadow-lg">
        <div className="mb-1 flex items-center gap-2">
          <Lock className="h-4 w-4 text-primary" />
          <h2 className="text-base font-semibold">二次验证</h2>
        </div>
        <p className="mb-4 text-sm text-muted-foreground">该操作涉及敏感配置，请重新输入登录密码。验证通过后 10 分钟内无需重复验证。</p>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
          className="space-y-4"
        >
          <div className="space-y-1.5">
            <Label htmlFor="elevate-pwd">登录密码</Label>
            <Input
              id="elevate-pwd"
              type="password"
              autoFocus
              value={pwd}
              onChange={(e) => setPwd(e.target.value)}
              placeholder="请输入登录密码"
              autoComplete="current-password"
            />
          </div>
          {err && <p className="text-sm text-destructive">{err}</p>}
          <div className="flex gap-2">
            <Button type="submit" className="flex-1" disabled={busy || !pwd}>
              {busy ? "验证中…" : "确认"}
            </Button>
            {onClose && (
              <Button type="button" variant="secondary" onClick={onClose}>
                取消
              </Button>
            )}
          </div>
        </form>
      </div>
    </div>
  );
}