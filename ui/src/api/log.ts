import { api } from "./http";

export interface LogConfig {
  enabled: boolean;
  file: string;
  maxsize: number;
  maxbackups: number;
  maxage: number;
  compress: boolean;
}

const empty = (): LogConfig => ({ enabled: false, file: "", maxsize: 100, maxbackups: 3, maxage: 7, compress: false });

export async function getLog(): Promise<LogConfig> {
  try {
    const data = await api.get<Partial<LogConfig>>("config/log");
    return { ...empty(), ...data } as LogConfig;
  } catch {
    return empty();
  }
}

export async function saveLog(cfg: LogConfig): Promise<void> {
  await api.post("config/save-log", cfg);
}