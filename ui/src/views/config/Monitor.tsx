import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { getMonitor, saveMonitor } from "@/api/monitor";

export function MonitorPage() {
  const [path, setPath] = useState("/status");
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => setPath((await getMonitor()).monitor_path), []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  const save = async () => {
    try {
      await saveMonitor({ monitor_path: path });
      setNotice({ type: "ok", msg: "配置保存成功，热重载生效中" });
      setTimeout(refresh, 6500);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">监控</h1>
      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}
      <Card>
        <CardHeader><CardTitle className="text-base">监控路径</CardTitle></CardHeader>
        <CardContent className="max-w-sm">
          <div className="space-y-1.5">
            <Label>监控路径（monitor_path）</Label>
            <Input className="font-mono" value={path} onChange={(e) => setPath(e.target.value)} placeholder="/status" />
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