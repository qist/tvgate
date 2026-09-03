import { Plus, Trash2 } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";

/** 通用键值对编辑器（map<string,string> ↔ {k,v}[]） */
export function KeyValueEditor({ value, onChange }: { value: Record<string, string>; onChange: (m: Record<string, string>) => void }) {
  const keys = Object.keys(value);
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between">
        <span className="text-xs text-muted-foreground">Headers</span>
        <Button size="sm" variant="ghost" onClick={() => onChange({ ...value, [""]: "" })}>
          <Plus className="h-3.5 w-3.5" />
        </Button>
      </div>
      {keys.map((k) => (
        <div key={k} className="flex items-center gap-2">
          <Input
            className="flex-1 font-mono"
            value={k}
            placeholder="键"
            onChange={(e) => {
              const next = { ...value };
              delete next[k];
              next[e.target.value] = value[k] ?? "";
              onChange(next);
            }}
          />
          <Input
            className="flex-1 font-mono"
            value={value[k]}
            placeholder="值"
            onChange={(e) => onChange({ ...value, [k]: e.target.value })}
          />
          <Button
            size="icon"
            variant="ghost"
            onClick={() => {
              const next = { ...value };
              delete next[k];
              onChange(next);
            }}
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      ))}
    </div>
  );
}