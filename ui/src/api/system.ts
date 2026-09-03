// 系统状态（轮询 /web/api/v1/status）
import { api } from "./http";

export interface SystemStatus {
  version?: string;
  os?: string;
  platform?: string;
  kernel_arch?: string;
  kernel_version?: string;
  uptime?: number;
  cpu?: number;
  cpu_temperature?: number;
  cpu_count?: number;
  mem?: number;
  mem_used?: number;
  mem_total?: number;
  swap?: number;
  swap_used?: number;
  swap_total?: number;
  disk?: number;
  disk_used?: number;
  disk_total?: number;
  load?: { load1?: number; load5?: number; load15?: number };
  clients?: number;
  active_clients?: Array<{
    id?: string;
    ip?: string;
    url?: string;
    user_agent?: string;
    referer?: string;
    connection_type?: string;
    is_mobile?: boolean;
    connected_at?: string;
    last_active?: string;
  }>;
  connections?: number;
  total_connections?: number;
  in_bytes?: number;
  out_bytes?: number;
  in_bandwidth?: number;
  out_bandwidth?: number;
  interfaces?: Array<{ name?: string; bytes_recv?: number; bytes_sent?: number; packets_recv?: number; packets_sent?: number; recv_bandwidth?: number; send_bandwidth?: number }>;
  disk_partitions?: Array<{ path?: string; total?: number; used?: number; free?: number; used_percent?: number; fs_type?: string; mount_point?: string }>;
  app?: { cpu_percent?: number; memory_usage?: number; total_bytes?: number; in_bytes?: number; out_bytes?: number };
  proxy_groups?: number;
  proxy_group_stats?: Record<string, { connections?: number; bytes_transferred?: number; active_streams?: number; last_error?: string; last_activity?: string }>;
  goroutines?: number;
  client_ip?: string;
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