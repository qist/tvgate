import { useCallback, useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { getDNS, saveDNS, type DNSConfig } from "@/api/dns";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

export function DNSPage() {
  const [cfg, setCfg] = useState<DNSConfig | null>(null);
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => setCfg(await getDNS()), []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!cfg) return <div className="text-sm text-muted-foreground">加载中…</div>;

  const patch = (p: Partial<DNSConfig>) => setCfg({ ...cfg, ...p });

  const save = async () => {
    try {
      await saveDNS({ ...cfg, servers: cfg.servers.filter((s) => s.trim() !== "") });
      setNotice({ type: "ok", msg: "配置保存成功，热重载生效中" });
      setTimeout(refresh, 6500);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">DNS 配置</h1>
      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}
      <Card>
        <CardHeader><CardTitle className="text-base">DNS 服务器</CardTitle></CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-center justify-between">
            <Label>DNS 服务器列表</Label>
            <Button size="sm" variant="secondary" onClick={() => patch({ servers: [...cfg.servers, ""] })}>
              <Plus className="h-4 w-4" /> 添加
            </Button>
          </div>
          {cfg.servers.map((s, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input className="flex-1 font-mono" value={s} onChange={(e) => patch({ servers: cfg.servers.map((x, xi) => (xi === i ? e.target.value : x)) })} placeholder="例如: 223.5.5.5 或 https://doh.example/dns-query" />
              <Button size="icon" variant="ghost" onClick={() => patch({ servers: cfg.servers.filter((_, xi) => xi !== i) })}>
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          ))}
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="查询超时">
              <Input value={cfg.timeout} onChange={(e) => patch({ timeout: e.target.value })} placeholder="5s" />
            </Field>
            <Field label="最大并发连接">
              <Input type="number" value={cfg.max_conns} onChange={(e) => patch({ max_conns: +e.target.value || 0 })} />
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