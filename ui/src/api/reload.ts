import { api } from "./http";

export interface ReloadConfig {
  reload: number;
}

export async function getReload(): Promise<ReloadConfig> {
  try {
    const data = await api.get<ReloadConfig>("config/reload");
    return { reload: data.reload ?? 5 };
  } catch {
    return { reload: 5 };
  }
}

export async function saveReload(cfg: ReloadConfig): Promise<void> {
  await api.post("config/save-reload", cfg);
}