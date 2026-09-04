import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { getTS, saveTS, type TSConfig } from "@/api/ts";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

export function TSPage() {
  const [cfg, setCfg] = useState<TSConfig | null>(null);
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => setCfg(await getTS()), []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!cfg) return <div className="text-sm text-muted-foreground">加载中…</div>;

  const save = async () => {
    try {
      await saveTS(cfg);
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
        <h1 className="text-xl font-semibold">TS 缓存</h1>
      </div>
      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}
      <Card>
        <CardHeader><CardTitle className="text-base">TS 缓存设置</CardTitle></CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-center gap-2">
            <Switch checked={cfg.enable} onCheckedChange={(v) => setCfg({ ...cfg, enable: v })} />
            <span className="text-sm">启用</span>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="缓存大小 (MB)">
              <Input type="number" value={cfg.cache_size} onChange={(e) => setCfg({ ...cfg, cache_size: +e.target.value || 0 })} />
            </Field>
            <Field label="缓存生存期">
              <Input value={cfg.cache_ttl} onChange={(e) => setCfg({ ...cfg, cache_ttl: e.target.value })} placeholder="2m" />
            </Field>
          </div>
        </CardContent>
      </Card>
      <div className="flex gap-2">
        <Button onClick={save}>保存</Button>
        <Button variant="secondary" onClick={refresh}>重置</Button>
      </div>
    </div>
  );
}