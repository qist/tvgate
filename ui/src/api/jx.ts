import { api } from "./http";

export interface ApiGroup {
  endpoints: string[];
  timeout: string; // 时长字符串，如 "5s"
  query_template: string;
  primary: boolean;
  weight: number;
  fallback: boolean;
  max_retries: number;
  filters: Record<string, string>; // 条件过滤 map
}

export interface JXConfig {
  path: string;
  default_id: string;
  api_groups: Record<string, ApiGroup>;
}

const emptyGroup = (): ApiGroup => ({
  endpoints: [],
  timeout: "",
  query_template: "",
  primary: false,
  weight: 1,
  fallback: false,
  max_retries: 1,
  filters: {},
});

export async function getJX(): Promise<JXConfig> {
  try {
    const data = await api.get<Partial<JXConfig>>("config/jx");
    return { path: data.path || "", default_id: data.default_id || "", api_groups: data.api_groups || {} };
  } catch {
    return { path: "", default_id: "", api_groups: {} };
  }
}

export async function saveJX(jx: JXConfig): Promise<void> {
  await api.post("config/save-jx", jx);
}

export { emptyGroup };