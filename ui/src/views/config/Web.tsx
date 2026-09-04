import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { getWeb, saveWeb, type WebConfig } from "@/api/web";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

export function WebPage() {
  const [cfg, setCfg] = useState<WebConfig | null>(null);
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);
  const [showPwd, setShowPwd] = useState(false);

  const refresh = useCallback(async () => setCfg(await getWeb()), []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!cfg) return <div className="text-sm text-muted-foreground">加载中…</div>;

  const save = async () => {
    try {
      await saveWeb(cfg);
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
        <h1 className="text-xl font-semibold">Web 设置</h1>
      </div>
      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}
      <Card>
        <CardHeader><CardTitle className="text-base">管理后台设置</CardTitle></CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-center gap-2">
            <Switch checked={cfg.enabled} onCheckedChange={(v) => setCfg({ ...cfg, enabled: v })} />
            <span className="text-sm">启用</span>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="用户名">
              <Input value={cfg.username} onChange={(e) => setCfg({ ...cfg, username: e.target.value })} autoComplete="new-password" />
            </Field>
            <Field label="密码">
              <div className="relative">
                <Input
                  type={showPwd ? "text" : "password"}
                  value={cfg.password}
                  onChange={(e) => setCfg({ ...cfg, password: e.target.value })}
                  autoComplete="new-password"
                />
                <button type="button" className="absolute right-2 top-1/2 -translate-y-1/2 text-xs text-muted-foreground" onClick={() => setShowPwd((s) => !s)}>
                  {showPwd ? "隐藏" : "显示"}
                </button>
              </div>
            </Field>
            <Field label="访问路径">
              <Input className="font-mono" value={cfg.path} onChange={(e) => setCfg({ ...cfg, path: e.target.value })} placeholder="/web/" />
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