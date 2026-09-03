import { api } from "./http";

export interface PHPConfig {
  enabled: boolean;
  path: string;
  docroot: string;
  index: string[];
  worker_mode: string;
  workers: number;
}

const empty = (): PHPConfig => ({ enabled: false, path: "/php/", docroot: "www", index: ["index.php", "index.html"], worker_mode: "", workers: 0 });

export async function getPHP(): Promise<PHPConfig> {
  try {
    const data = await api.get<Partial<PHPConfig>>("config/php");
    return { ...empty(), ...data, index: data.index || [] } as PHPConfig;
  } catch {
    return empty();
  }
}

export async function savePHP(cfg: PHPConfig): Promise<void> {
  await api.post("config/save-php", cfg);
}