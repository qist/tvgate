import { api } from "./http";

export interface MonitorConfig {
  monitor_path: string;
}

export async function getMonitor(): Promise<MonitorConfig> {
  try {
    const data = await api.get<MonitorConfig>("config/server-monitor");
    return { monitor_path: data.monitor_path || "/status" };
  } catch {
    return { monitor_path: "/status" };
  }
}

export async function saveMonitor(cfg: MonitorConfig): Promise<void> {
  await api.post("config/save-server-monitor", cfg);
}