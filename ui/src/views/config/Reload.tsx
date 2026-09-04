import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { getReload, saveReload } from "@/api/reload";

export function ReloadPage() {
  const [reload, setReload] = useState<number>(5);
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => setReload((await getReload()).reload), []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  const save = async () => {
    try {
      await saveReload({ reload });
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
        <h1 className="text-xl font-semibold">重载配置</h1>
      </div>
      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}
      <Card>
        <CardHeader><CardTitle className="text-base">配置文件重载间隔</CardTitle></CardHeader>
        <CardContent className="max-w-sm">
          <div className="space-y-1.5">
            <Label>重载间隔（秒）</Label>
            <Input type="number" value={reload} onChange={(e) => setReload(+e.target.value || 0)} />
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