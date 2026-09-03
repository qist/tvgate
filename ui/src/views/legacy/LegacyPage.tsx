import { useSearchParams } from "react-router-dom";

/** 通用内嵌页：以 iframe 嵌入现有 go-template 页面（同源，登录态 Cookie 自动继承） */
export function LegacyPage() {
  const [sp] = useSearchParams();
  const src = sp.get("p") || "";

  return (
    <div className="flex h-[calc(100vh-3.5rem)] flex-col gap-2">
      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <span className="truncate">原版页面：{src || "未指定"}</span>
      </div>
      {src ? (
        <iframe
          src={src}
          title="legacy"
          className="h-full w-full rounded-[var(--radius-lg)] border bg-white"
        />
      ) : (
        <p className="text-sm text-muted-foreground">未指定要嵌入的页面。</p>
      )}
    </div>
  );
}