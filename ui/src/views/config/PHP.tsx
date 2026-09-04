import { useCallback, useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { getPHP, savePHP, type PHPConfig } from "@/api/php";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

export function PHPPage() {
  const [cfg, setCfg] = useState<PHPConfig | null>(null);
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => setCfg(await getPHP()), []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!cfg) return <div className="text-sm text-muted-foreground">加载中…</div>;
  const patch = (p: Partial<PHPConfig>) => setCfg({ ...cfg, ...p });

  const save = async () => {
    try {
      await savePHP({ ...cfg, index: cfg.index.filter((x) => x.trim() !== "") });
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
        <h1 className="text-xl font-semibold">PHP 模块</h1>
      </div>
      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}

      <Card>
        <CardHeader><CardTitle className="text-base">PHP 模块设置</CardTitle></CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-center gap-2">
            <Switch checked={cfg.enabled} onCheckedChange={(v) => patch({ enabled: v })} />
            <span className="text-sm">启用</span>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="路径前缀"><Input className="font-mono" value={cfg.path} onChange={(e) => patch({ path: e.target.value })} placeholder="/php/" /></Field>
            <Field label="DocRoot"><Input className="font-mono" value={cfg.docroot} onChange={(e) => patch({ docroot: e.target.value })} placeholder="www" /></Field>
            <Field label="工作模式"><Input value={cfg.worker_mode} onChange={(e) => patch({ worker_mode: e.target.value })} placeholder="可选" /></Field>
            <Field label="Worker 数"><Input type="number" value={cfg.workers} onChange={(e) => patch({ workers: +e.target.value || 0 })} /></Field>
          </div>
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>索引文件（index）</Label>
              <Button size="sm" variant="secondary" onClick={() => patch({ index: [...cfg.index, ""] })}>
                <Plus className="h-4 w-4" /> 添加
              </Button>
            </div>
            {cfg.index.map((idx, i) => (
              <div key={i} className="flex items-center gap-2">
                <Input className="flex-1 font-mono" value={idx} onChange={(e) => patch({ index: cfg.index.map((x, xi) => (xi === i ? e.target.value : x)) })} placeholder="index.php" />
                <Button size="icon" variant="ghost" onClick={() => patch({ index: cfg.index.filter((_, xi) => xi !== i) })}>
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            ))}
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