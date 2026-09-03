import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

/** 配置区（骨架占位） */
export function ConfigIndex() {
  const tabs = ["基础配置", "鉴权", "网络", "域名映射", "视频解析", "代理组", "推流", "配置节点", "原始 YAML"];
  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">配置</h1>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">结构化配置标签页（骨架）</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex flex-wrap gap-2">
            {tabs.map((t) => (
              <span key={t} className="rounded-full border bg-muted px-3 py-1 text-sm">
                {t}
              </span>
            ))}
          </div>
          <p className="mt-4 text-sm text-muted-foreground">
            骨架占位：后续用 shadcn 原子组件 + react-hook-form/zod 实现各配置区段结构化表单（现有参数一个都不能少）。
          </p>
        </CardContent>
      </Card>
    </div>
  );
}