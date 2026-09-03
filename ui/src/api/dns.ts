import { api } from "./http";

export interface DNSConfig {
  servers: string[];
  timeout: string;
  max_conns: number;
}

export async function getDNS(): Promise<DNSConfig> {
  try {
    const data = await api.get<Partial<DNSConfig>>("api/dns/config");
    return { servers: data.servers || [], timeout: data.timeout || "", max_conns: data.max_conns ?? 0 };
  } catch {
    return { servers: [], timeout: "", max_conns: 0 };
  }
}

export async function saveDNS(cfg: DNSConfig): Promise<void> {
  await api.post("api/dns/config/save", cfg);
}