import { api } from "./http";

export interface PlayerConfig {
  enabled: boolean;
  subscription: string;
  epg: string;
  logo: string;
  logo_dir: string;
  /** Go time.Duration 字符串（如 2h / 30m），空串表示默认 2h */
  update_interval: string;
  ua: string;
}

export async function getPlayer(): Promise<PlayerConfig> {
  const data = await api.get<Partial<PlayerConfig> & { update_interval?: string }>("config/player");
  const interval = data.update_interval || "";
  return {
    enabled: data.enabled === true,
    subscription: data.subscription || "",
    epg: data.epg || "",
    logo: data.logo || "",
    logo_dir: data.logo_dir || "",
    // 默认 2h 时后端返回 "2h0m0s"，按旧版行为显示为空（代表默认）
    update_interval: interval === "2h0m0s" ? "" : interval,
    ua: data.ua || "",
  };
}

export async function savePlayer(cfg: PlayerConfig): Promise<void> {
  await api.post("config/save-player", cfg);
}