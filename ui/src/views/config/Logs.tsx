import { useEffect, useRef, useState } from "react";
import { Eraser, Pause, Play, Radio } from "lucide-react";
import { resolveBase } from "@/api/base";
import { Button } from "@/components/ui/button";

const MAX_LINES = 2000;

export function LogsPage() {
  const [logs, setLogs] = useState<string[]>([]);
  const [status, setStatus] = useState("连接中...");
  const [filter, setFilter] = useState("");
  const [autoScroll, setAutoScroll] = useState(true);
  const [paused, setPaused] = useState(false);
  const boxRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const source = new EventSource(resolveBase() + "api/logs/stream");
    source.onopen = () => setStatus("已连接");
    source.onmessage = (ev) => {
      const line = ev.data as string;
      if (!line) return;
      setLogs((prev) => {
        const next = [...prev, line];
        return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next;
      });
    };
    source.addEventListener("status", (ev) => {
      setStatus((ev as MessageEvent).data as string);
      source.close();
    });
    source.onerror = () => setStatus("连接中断，正在重试...");
    return () => {
      source.close();
    };
  }, []);

  useEffect(() => {
    if (autoScroll && boxRef.current && !paused) {
      boxRef.current.scrollTop = boxRef.current.scrollHeight;
    }
  }, [logs, autoScroll, paused]);

  const visible = filter.trim() ? logs.filter((l) => l.toLowerCase().includes(filter.trim().toLowerCase())) : logs;

  const clear = () => setLogs([]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">实时日志</h1>
        <div className="flex items-center gap-2">
          <span className={`flex items-center gap-1 text-sm ${status === "已连接" ? "text-emerald-600 dark:text-emerald-400" : "text-muted-foreground"}`}>
            <Radio className="h-4 w-4" /> {status}
          </span>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="过滤关键字"
          className="h-9 flex-1 min-w-[180px] rounded-[var(--radius)] border border-input bg-background px-3 text-sm text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        />
        <Button
          variant="outline"
          size="sm"
          onClick={() => setPaused((p) => !p)}
        >
          {paused ? <Play className="mr-1 h-4 w-4" /> : <Pause className="mr-1 h-4 w-4" />}
          {paused ? "继续" : "暂停"}
        </Button>
        <label className="flex cursor-pointer items-center gap-2 text-sm text-muted-foreground">
          <input type="checkbox" className="accent-[hsl(var(--primary))]" checked={autoScroll} onChange={(e) => setAutoScroll(e.target.checked)} />
          自动滚动
        </label>
        <Button variant="outline" size="sm" onClick={clear}>
          <Eraser className="mr-1 h-4 w-4" /> 清空
        </Button>
      </div>

      <div
        ref={boxRef}
        className="h-[65vh] overflow-auto rounded-lg border border-border bg-background p-3 font-mono text-xs whitespace-pre text-foreground"
      >
        {visible.length === 0 ? (
          <span className="text-muted-foreground">{status === "日志已关闭" ? "日志已关闭，请在上方「日志配置」开启。" : "暂无日志..."}</span>
        ) : (
          visible.map((l, i) => <div key={i}>{l}</div>)
        )}
      </div>
      <p className="text-xs text-muted-foreground">共 {visible.length} 行（内存上限 {MAX_LINES} 行）；若提示「日志已关闭」，请先在日志配置中开启后再刷新本页。</p>
    </div>
  );
}