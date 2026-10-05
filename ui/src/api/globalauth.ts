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

/**
 * 给对外地址补上全局令牌（global_auth 启用时），参数名与服务端 auth.ExtractToken 一致：
 * 优先 token_param_name，为空回落 my_token。
 *
 * 未启用授权或没有可用静态令牌时原样返回——此时服务端不校验，拼了反而多余。
 * 仅开启动态令牌（无静态令牌）时无法在前端拼出令牌，仍需手动带 ?<参数名>= 访问。
 */
export function appendGlobalToken(url: string, cfg: AuthConfig | null | undefined): string {
  if (!cfg || !cfg.tokens_enabled) return url;
  const token = cfg.static_tokens?.enable_static ? (cfg.static_tokens.token || "").trim() : "";
  if (!token) return url;
  const param = (cfg.token_param_name || "").trim() || "my_token";
  return url + (url.includes("?") ? "&" : "?") + param + "=" + encodeURIComponent(token);
}