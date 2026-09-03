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

const emptyAuth = (): AuthConfig => ({
  tokens_enabled: false,
  token_param_name: "",
  dynamic_tokens: { enable_dynamic: false, dynamic_ttl: "", secret: "", salt: "" },
  static_tokens: { enable_static: false, token: "", expire_hours: "" },
});

export async function getGlobalAuth(): Promise<AuthConfig> {
  try {
    const data = await api.get<Partial<AuthConfig>>("config/global-auth");
    return { ...emptyAuth(), ...data } as AuthConfig;
  } catch {
    return emptyAuth();
  }
}

export async function saveGlobalAuth(cfg: AuthConfig): Promise<void> {
  await api.post("config/save-global-auth", cfg);
}

/** 提交值若仍是掩码占位符则后端保留原值 */
export const CREDENTIAL_MASK = "********";