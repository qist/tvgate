import { useCallback, useEffect, useState } from "react";
import { AsyncActionButton } from "@/components/config/async-action-button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { getLog, saveLog, type LogConfig } from "@/api/log";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

export function LogConfigPage() {
  const [cfg, setCfg] = useState<LogConfig | null>(null);
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => setCfg(await getLog()), []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!cfg) return <div className="text-sm text-muted-foreground">加载中…</div>;
  const patch = (p: Partial<LogConfig>) => setCfg({ ...cfg, ...p });

  const save = async () => {
    try {
      await saveLog(cfg);
      setNotice({ type: "ok", msg: "配置保存成功，热重载生效中" });
      setTimeout(refresh, 6500);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">日志配置</h1>
      </div>
      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}
      <Card>
        <CardHeader><CardTitle className="text-base">日志设置</CardTitle></CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-center gap-2">
            <Switch checked={cfg.enabled} onCheckedChange={(v) => patch({ enabled: v })} />
            <span className="text-sm">启用日志</span>
          </div>
          <Field label="日志文件">
            <Input className="font-mono" value={cfg.file} onChange={(e) => patch({ file: e.target.value })} placeholder="/var/log/tvgate.log" />
          </Field>
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label="单文件大小 (MB)"><Input type="number" value={cfg.maxsize} onChange={(e) => patch({ maxsize: +e.target.value || 0 })} /></Field>
            <Field label="保留备份数"><Input type="number" value={cfg.maxbackups} onChange={(e) => patch({ maxbackups: +e.target.value || 0 })} /></Field>
            <Field label="保留天数"><Input type="number" value={cfg.maxage} onChange={(e) => patch({ maxage: +e.target.value || 0 })} /></Field>
          </div>
          <div className="flex items-center gap-2">
            <Switch checked={cfg.compress} onCheckedChange={(v) => patch({ compress: v })} />
            <span className="text-sm">启用压缩</span>
          </div>
        </CardContent>
      </Card>
      <div className="flex gap-2">
        <AsyncActionButton action={save} busyText="保存中…">保存</AsyncActionButton>
        <AsyncActionButton variant="secondary" action={refresh} busyText="加载中…">重新加载</AsyncActionButton>
      </div>
    </div>
  );
}