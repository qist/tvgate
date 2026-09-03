// 前端挂载基址：由当前 location 推导（hash 路由），无需服务端注入。
// 形如 /web/ 或 /admin/，以斜杠结尾。
export function resolveBase(): string {
  const path = window.location.pathname;
  // 取到最后一个非空段（含开头斜杠），保证在任意 web.path 下 API 前缀正确
  const idx = path.indexOf("/", 1);
  if (idx <= 0) return "/";
  return path.slice(0, idx) + "/";
}

export const base = resolveBase();