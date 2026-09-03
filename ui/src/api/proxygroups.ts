import { api } from "./http";

export interface Proxy {
  name: string;
  type: string;
  server: string;
  port: number;
  udp: boolean;
  username: string;
  password: string;
  headers?: Record<string, string>;
}

export interface ProxyGroup {
  proxies: Proxy[];
  domains: string[];
  ipv6: boolean;
  interval: string; // 时长字符串，如 "180s"
  loadbalance: string;
  max_retries: number;
  retry_delay: string;
  max_rt: string;
}

export type ProxyGroupMap = Record<string, ProxyGroup>;

export async function listProxyGroups(): Promise<ProxyGroupMap> {
  try {
    const data = await api.get<ProxyGroupMap>("config/proxygroups");
    return data || {};
  } catch {
    return {};
  }
}

export async function saveProxyGroups(map: ProxyGroupMap): Promise<void> {
  await api.post("config/save-proxygroups", map);
}