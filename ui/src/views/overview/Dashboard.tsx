import { useEffect, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
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

function fmtTime(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return "—";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

/** 概览仪表盘：系统实时状态（含活跃连接/分区/网卡，替代原独立 /status 监控页） */
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

  const partitions = status.disk_partitions || [];
  const ifaces = status.interfaces || [];
  const clients = status.active_clients || [];

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
      <div className="grid gap-4 lg:grid-cols-3">
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">资源</CardTitle></CardHeader>
          <CardContent className="space-y-1.5 text-sm">
            <Row label="磁盘" value={status.disk != null ? `${status.disk}% (${fmtBytes(status.disk_used)} / ${fmtBytes(status.disk_total)})` : "—"} />
            <Row label="负载" value={status.load ? `${status.load.load1 ?? "—"} / ${status.load.load5 ?? "—"} / ${status.load.load15 ?? "—"}` : "—"} />
            <Row label="内存用量" value={status.mem_used != null ? fmtBytes(status.mem_used) : "—"} />
            <Row label="交换分区" value={status.swap != null ? `${status.swap}%` : "—"} />
            <Row label="CPU 温度" value={status.cpu_temperature != null && status.cpu_temperature > 0 ? `${status.cpu_temperature}℃` : "不支持"} />
            <Row label="启动时间" value={status.start_time ? new Date(status.start_time).toLocaleString("zh-CN", { hour12: false }) : "—"} />
            <Row label="Goroutines" value={status.goroutines != null ? String(status.goroutines) : "—"} />
            <Row label="客户端 IP" value={status.client_ip || "—"} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">网络</CardTitle></CardHeader>
          <CardContent className="space-y-1.5 text-sm">
            <Row label="实时下行" value={status.in_bandwidth != null ? `${fmtBytes(status.in_bandwidth)}/s` : "—"} />
            <Row label="实时上行" value={status.out_bandwidth != null ? `${fmtBytes(status.out_bandwidth)}/s` : "—"} />
            <Row label="累计下行" value={fmtBytes(status.in_bytes)} />
            <Row label="累计上行" value={fmtBytes(status.out_bytes)} />
            <Row label="连接数" value={status.connections != null ? String(status.connections) : "—"} />
            <Row label="总连接数" value={status.total_connections != null ? String(status.total_connections) : "—"} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">应用（TVGate 进程）</CardTitle></CardHeader>
          <CardContent className="space-y-1.5 text-sm">
            <Row label="进程 CPU" value={status.app?.cpu_percent != null ? `${status.app.cpu_percent}%` : "—"} />
            <Row label="进程内存" value={status.app?.memory_usage != null ? fmtBytes(status.app.memory_usage) : "—"} />
            <Row label="入口流量" value={status.app?.in_bytes != null ? fmtBytes(status.app.in_bytes) : "—"} />
            <Row label="出口流量" value={status.app?.out_bytes != null ? fmtBytes(status.app.out_bytes) : "—"} />
            <Row label="总流量" value={status.app?.total_bytes != null ? fmtBytes(status.app.total_bytes) : "—"} />
          </CardContent>
        </Card>
      </div>

      {/* 活跃连接 */}
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-base">活跃连接（{clients.length}）</CardTitle></CardHeader>
        <CardContent className="overflow-x-auto">
          {clients.length === 0 ? (
            <p className="text-sm text-muted-foreground">暂无活跃连接</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="min-w-[120px]">IP</TableHead>
                  <TableHead className="min-w-[180px]">URL</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead className="min-w-[160px]">User-Agent</TableHead>
                  <TableHead>接入时间</TableHead>
                  <TableHead>最近活跃</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {clients.map((c, i) => (
                  <TableRow key={c.id || i}>
                    <TableCell className="font-mono text-xs">{c.ip}</TableCell>
                    <TableCell className="max-w-[260px] truncate font-mono text-xs" title={c.url}>{c.url}</TableCell>
                    <TableCell>{c.connection_type}</TableCell>
                    <TableCell className="max-w-[200px] truncate text-xs" title={c.user_agent}>{c.user_agent}</TableCell>
                    <TableCell className="text-xs">{fmtTime(c.connected_at)}</TableCell>
                    <TableCell className="text-xs">{fmtTime(c.last_active)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* 存储分区 + 网卡 */}
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-base">存储分区</CardTitle></CardHeader>
          <CardContent className="overflow-x-auto">
            {partitions.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无数据</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>挂载点</TableHead>
                    <TableHead>文件系统</TableHead>
                    <TableHead>总量</TableHead>
                    <TableHead>已用</TableHead>
                    <TableHead>使用率</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {partitions.map((p, i) => (
                    <TableRow key={i}>
                      <TableCell className="font-mono text-xs">{p.mount_point || p.path}</TableCell>
                      <TableCell className="text-xs">{p.fs_type}</TableCell>
                      <TableCell className="text-xs">{fmtBytes(p.total)}</TableCell>
                      <TableCell className="text-xs">{fmtBytes(p.used)}</TableCell>
                      <TableCell className="text-xs">{p.used_percent != null ? `${p.used_percent}%` : "—"}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-base">网卡</CardTitle></CardHeader>
          <CardContent className="overflow-x-auto">
            {ifaces.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无数据</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>名称</TableHead>
                    <TableHead>下行</TableHead>
                    <TableHead>上行</TableHead>
                    <TableHead>实时下行</TableHead>
                    <TableHead>实时上行</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {ifaces.map((n, i) => (
                    <TableRow key={i}>
                      <TableCell className="font-mono text-xs">{n.name}</TableCell>
                      <TableCell className="text-xs">{fmtBytes(n.bytes_recv)}</TableCell>
                      <TableCell className="text-xs">{fmtBytes(n.bytes_sent)}</TableCell>
                      <TableCell className="text-xs">{fmtBytes(n.recv_bandwidth)}/s</TableCell>
                      <TableCell className="text-xs">{fmtBytes(n.send_bandwidth)}/s</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
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