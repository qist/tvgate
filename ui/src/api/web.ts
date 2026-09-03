import { api } from "./http";

export interface WebConfig {
  enabled: boolean;
  username: string;
  password: string;
  path: string;
}

export async function getWeb(): Promise<WebConfig> {
  try {
    const data = await api.get<Partial<WebConfig>>("config/web");
    return { enabled: data.enabled === true, username: data.username || "", password: data.password || "", path: data.path || "/web/" };
  } catch {
    return { enabled: false, username: "", password: "", path: "/web/" };
  }
}

export async function saveWeb(cfg: WebConfig): Promise<void> {
  await api.post("config/save-web", cfg);
}