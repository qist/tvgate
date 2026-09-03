import { useCallback, useEffect, useState } from "react";
import { Download, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { load } from "@/api/yaml";

export function ConfigViewPage() {
  const [content, setContent] = useState("");
  const [loading, setLoading] = useState(false);
  const [note, setNote] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      setContent(await load());
      setNote(null);
    } catch (e) {
      setNote({ type: "err", msg: "加载配置失败: " + (e as Error).message });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const download = () => {
    const blob = new Blob([content], { type: "application/yaml;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "config.yaml";
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">配置查看</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={refresh} disabled={loading}>
            <RefreshCw className={`mr-1 h-4 w-4 ${loading ? "animate-spin" : ""}`} /> 刷新
          </Button>
          <Button size="sm" onClick={download}>
            <Download className="mr-1 h-4 w-4" /> 下载
          </Button>
        </div>
      </div>

      {note && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{note.msg}</div>
      )}

      <Card className="overflow-hidden">
        <pre className="h-[70vh] w-full overflow-auto bg-background p-3 font-mono text-xs text-foreground">
          {content || "加载中..."}
        </pre>
      </Card>
      <p className="text-xs text-muted-foreground">以上为当前运行实例的实时配置文件内容（只读），如需修改请使用「YAML 编辑器」。</p>
    </div>
  );
}