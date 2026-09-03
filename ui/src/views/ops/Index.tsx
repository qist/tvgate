import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

/** 运维工具（骨架占位） */
export function OpsIndex() {
  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">工具</h1>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">骨架占位</CardTitle>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          实时日志（SSE）、配置备份、仓库同步、GitHub 升级等运维 / 工具页。
        </CardContent>
      </Card>
    </div>
  );
}