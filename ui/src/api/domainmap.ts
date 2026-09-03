import { api } from "./http";

export interface DynamicTokens {
  enable_dynamic: boolean;
  dynamic_ttl: string;
  secret: string;
  salt: string;
}

export interface StaticTokens {
  enable_static: boolean;
  token: string;
  expire_hours: string;
}

export interface AuthConfig {
  tokens_enabled: boolean;
  token_param_name: string;
  dynamic_tokens: DynamicTokens;
  static_tokens: StaticTokens;
}

export interface DomainMap {
  name: string;
  source: string;
  target: string;
  protocol: string;
  auth?: AuthConfig;
  client_headers?: Record<string, string>;
  server_headers?: Record<string, string>;
}

export type DomainMapList = DomainMap[];

export async function listDomainMaps(): Promise<DomainMapList> {
  try {
    const data = await api.get<DomainMapList>("config/domainmap");
    return Array.isArray(data) ? data : [];
  } catch {
    return [];
  }
}

export async function saveDomainMaps(list: DomainMapList): Promise<void> {
  await api.post("config/save-domainmap", list);
}