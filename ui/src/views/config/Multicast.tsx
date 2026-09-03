import { useCallback, useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { getMulticast, saveMulticast, type MulticastConfig } from "@/api/multicast";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

export function MulticastPage() {
  const [cfg, setCfg] = useState<MulticastConfig | null>(null);
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => setCfg(await getMulticast()), []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!cfg) return <div className="text-sm text-muted-foreground">加载中…</div>;

  const patch = (p: Partial<MulticastConfig>) => setCfg({ ...cfg, ...p });

  const save = async () => {
    try {
      await saveMulticast(cfg);
      setNotice({ type: "ok", msg: "配置保存成功，热重载生效中" });
      setTimeout(refresh, 6500);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">组播配置</h1>
      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}

      <Card>
        <CardHeader><CardTitle className="text-base">FCC 与网卡</CardTitle></CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>组播网卡 (multicast_ifaces)</Label>
              <Button size="sm" variant="secondary" onClick={() => patch({ multicast_ifaces: [...cfg.multicast_ifaces, ""] })}>
                <Plus className="h-4 w-4" /> 添加
              </Button>
            </div>
            {cfg.multicast_ifaces.map((iface, i) => (
              <div key={i} className="flex items-center gap-2">
                <Input className="flex-1 font-mono" value={iface} onChange={(e) => patch({ multicast_ifaces: cfg.multicast_ifaces.map((x, xi) => (xi === i ? e.target.value : x)) })} placeholder="eth0" />
                <Button size="icon" variant="ghost" onClick={() => patch({ multicast_ifaces: cfg.multicast_ifaces.filter((_, xi) => xi !== i) })}>
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            ))}
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="重连间隔">
              <Input value={cfg.mcast_rejoin_interval} onChange={(e) => patch({ mcast_rejoin_interval: e.target.value })} placeholder="例如: 30s" />
            </Field>
            <Field label="FCC 类型">
              <select className="h-9 w-full rounded-[var(--radius)] border bg-background px-2 text-sm" value={cfg.fcc_type} onChange={(e) => patch({ fcc_type: e.target.value })}>
                {["", "telecom", "huawei"].map((t) => <option key={t} value={t}>{t || "（默认）"}</option>)}
              </select>
            </Field>
            <Field label="FCC 缓存大小">
              <Input type="number" value={cfg.fcc_cache_size} onChange={(e) => patch({ fcc_cache_size: +e.target.value || 0 })} />
            </Field>
            <div className="grid grid-cols-2 gap-2 sm:col-span-2">
              <Field label="监听端口 (min)"><Input type="number" value={cfg.fcc_listen_port_min} onChange={(e) => patch({ fcc_listen_port_min: +e.target.value || 0 })} /></Field>
              <Field label="监听端口 (max)"><Input type="number" value={cfg.fcc_listen_port_max} onChange={(e) => patch({ fcc_listen_port_max: +e.target.value || 0 })} /></Field>
            </div>
            <Field label="上游接口">
              <Input className="font-mono" value={cfg.upstream_interface} onChange={(e) => patch({ upstream_interface: e.target.value })} placeholder="默认上游接口" />
            </Field>
            <Field label="FCC 上游接口">
              <Input className="font-mono" value={cfg.upstream_interface_fcc} onChange={(e) => patch({ upstream_interface_fcc: e.target.value })} placeholder="FCC 专用上游接口" />
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