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
  /** DNS 模式：remote(默认) / local；节点级可选，默认继承组 */
  resolve?: string;
}

export interface ProxyStats {
  LastCheck?: string;
  LastUsed?: string;
  ResponseTime?: number;
  Alive?: boolean;
  FailCount?: number;
  CooldownUntil?: string;
  StatusCode?: number;
}

/** 动态代理池（host:start-end 端口段展开，后台定时探测） */
export interface ProxyPool {
  servers: string[];
  protocol?: string;
  probe_enabled?: boolean;
  probe_interval?: string;
  probe_url?: string;
  probe_timeout?: string;
  /** 单轮失败后的立即重试次数（0=不重试，上限 5） */
  probe_retries?: number;
  probe_concurrency?: number;
  top_n?: number;
  fail_cooldown?: string;
  max_ports?: number;
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
  /** DNS 模式：remote(默认) / local */
  resolve?: string;
  /** 探测/测速用 User-Agent；为空回退 player.ua，再为空则不发送 */
  probe_ua?: string;
  /** 动态代理池；null 表示显式删除 */
  pool?: ProxyPool | null;
  /** 运行时探测状态（后端配置读取时附加；保存时不提交该字段） */
  stats?: { ProxyStats?: Record<string, ProxyStats> };
  /** 运行时池节点（探测成功的端口；保存时不提交该字段） */
  pool_nodes?: Proxy[];
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

export interface ClearCacheResult {
  cleared: number;
  message: string;
}

/** 清理单个代理组的访问缓存并重置其测速统计（改规则后立即生效） */
export async function clearProxyGroupCache(group: string): Promise<ClearCacheResult> {
  return api.post<ClearCacheResult>("config/proxygroups/clear-cache", { group });
}
