// 系统状态（轮询 /web/api/v1/status）
import { api } from "./http";

export interface SystemStatus {
  version?: string;
  os?: string;
  uptime?: number;
  cpu?: number;
  cpu_temperature?: number;
  cpu_count?: number;
  mem?: number;
  mem_used?: number;
  mem_total?: number;
  swap?: number;
  disk?: number;
  disk_used?: number;
  disk_total?: number;
  load?: { load1?: number; load5?: number; load15?: number };
  clients?: number;
  connections?: number;
  total_connections?: number;
  in_bytes?: number;
  out_bytes?: number;
  in_bandwidth?: number;
  out_bandwidth?: number;
  interfaces?: Array<{ name?: string; bytes_recv?: number; bytes_sent?: number; recv_bandwidth?: number; send_bandwidth?: number }>;
  proxy_groups?: number;
  goroutines?: number;
  web_path?: string;
  timestamp?: string;
}

export async function getStatus(): Promise<SystemStatus> {
  try {
    return await api.get<SystemStatus>("api/v1/status");
  } catch {
    return {};
  }
}