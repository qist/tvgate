import { useEffect, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { getStatus, type SystemStatus } from "@/api/system";

function fmtUptime(sec?: number): string {
  if (sec == null) return "—";
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d}天${h}小时`;
  if (h > 0) return `${h}小时${m}分`;
  return `${m}分`;
}

function fmtBytes(b?: number): string {
  if (b == null) return "—";
  if (b < 1024) return `${b}B`;
  const u = ["KB", "MB", "GB", "TB"];
  let v = b / 1024;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1)}${u[i]}`;
}

/** 概览仪表盘 */
export function Dashboard() {
  const [status, setStatus] = useState<SystemStatus>({});

  useEffect(() => {
    let active = true;
    const tick = async () => {
      const s = await getStatus();
      if (active) setStatus(s);
    };
    tick();
    const id = setInterval(tick, 5000);
    return () => {
      active = false;
      clearInterval(id);
    };
  }, []);

  const items = [
    { label: "版本", value: status.version || "—" },
    { label: "系统", value: status.os || "—" },
    { label: "CPU", value: status.cpu != null ? `${status.cpu}%` : "—" },
    { label: "内存", value: status.mem != null ? `${status.mem}%` : "—" },
    { label: "活跃连接", value: status.clients != null ? String(status.clients) : "—" },
    { label: "运行时长", value: fmtUptime(status.uptime) },
  ];

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">仪表盘</h1>
      <div className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-6">
        {items.map((it) => (
          <Card key={it.label}>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm font-medium text-muted-foreground">{it.label}</CardTitle>
            </CardHeader>
            <CardContent className="text-2xl font-bold">{it.value}</CardContent>
          </Card>
        ))}
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">资源</CardTitle></CardHeader>
          <CardContent className="space-y-1.5 text-sm">
            <Row label="磁盘" value={status.disk != null ? `${status.disk}% (${fmtBytes(status.disk_used)} / ${fmtBytes(status.disk_total)})` : "—"} />
            <Row label="负载" value={status.load ? `${status.load.load1 ?? "—"} / ${status.load.load5 ?? "—"} / ${status.load.load15 ?? "—"}` : "—"} />
            <Row label="内存用量" value={status.mem_used != null ? fmtBytes(status.mem_used) : "—"} />
            <Row label="CPU 温度" value={status.cpu_temperature != null ? `${status.cpu_temperature}℃` : "—"} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">网络</CardTitle></CardHeader>
          <CardContent className="space-y-1.5 text-sm">
            <Row label="实时下行" value={status.in_bandwidth != null ? `${fmtBytes(status.in_bandwidth)}/s` : "—"} />
            <Row label="实时上行" value={status.out_bandwidth != null ? `${fmtBytes(status.out_bandwidth)}/s` : "—"} />
            <Row label="累计下行" value={fmtBytes(status.in_bytes)} />
            <Row label="累计上行" value={fmtBytes(status.out_bytes)} />
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-mono">{value}</span>
    </div>
  );
}