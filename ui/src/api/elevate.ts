// 二次授权（敏感操作前重输登录密码，10 分钟内有效）
import { api, ApiError } from "./http";
import { resolveBase } from "./base";

export async function isElevated(): Promise<boolean> {
  try {
    const r = await api.get<{ elevated?: boolean }>("api/v1/elevate", { auth: false });
    return r?.elevated === true;
  } catch {
    return false;
  }
}

export async function unlock(password: string): Promise<void> {
  const res = await fetch(resolveBase() + "api/v1/elevate", {
    method: "POST",
    credentials: "same-origin",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify({ password }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, (await res.text()) || "验证失败");
  }
}

/** 判断错误是否为「需要二次验证」（后端 403 + code:403） */
export function isElevateRequired(e: unknown): boolean {
  return e instanceof ApiError && e.status === 403;
}
