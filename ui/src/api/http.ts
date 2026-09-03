// 统一 fetch wrapper：同源、带 Cookie、JSON，401/302 跳登录
import { resolveBase } from "./base";

interface ApiOptions {
  raw?: boolean; // 不 JSON 化响应（如整份文本）
  auth?: boolean; // 未认证时跳登录
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit, opts: ApiOptions = {}): Promise<T> {
  const { raw = false, auth = true } = opts;
  const base = resolveBase();
  const res = await fetch(base + path, {
    credentials: "same-origin",
    ...init,
    headers: {
      Accept: "application/json",
      ...(raw ? {} : { "Content-Type": "application/json" }),
      ...init?.headers,
    },
  });

  // 后端对未认证 JSON 请求返回 401（cookieAuth 按 Accept 判定）；仍可能 302 的场景统一视为未认证。
  // 未认证一律进 SPA 公开登录页（白名单），不走整页跳转（避免中断在途请求）
  if (res.status === 401 || res.status === 302) {
    if (auth && !window.location.hash.startsWith("#/login")) {
      window.location.hash = "#/login";
    }
    throw new ApiError(401, "未认证，请先登录");
  }
  if (!res.ok) {
    throw new ApiError(res.status, (await res.text()) || `请求失败(${res.status})`);
  }
  if (raw || !res.headers.get("content-type")?.includes("application/json")) {
    return (await res.text()) as unknown as T;
  }
  return (await res.json()) as T;
}

export const api = {
  get: <T>(path: string, opts?: ApiOptions) => request<T>(path, { method: "GET" }, opts),
  post: <T>(path: string, body?: unknown, opts?: ApiOptions) =>
    request<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) }, opts),
};

export default api;