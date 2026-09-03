import { api } from "./http";

export interface TSConfig {
  enable: boolean;
  cache_size: number;
  cache_ttl: string;
}

export async function getTS(): Promise<TSConfig> {
  try {
    const data = await api.get<Partial<TSConfig>>("config/ts");
    return { enable: data.enable === true, cache_size: data.cache_size ?? 128, cache_ttl: data.cache_ttl || "2m" };
  } catch {
    return { enable: false, cache_size: 128, cache_ttl: "2m" };
  }
}

export async function saveTS(cfg: TSConfig): Promise<void> {
  await api.post("config/save-ts", cfg);
}