import { useCallback, useEffect, useMemo, useState } from "react";
import { Plus, Play, Pencil, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import {
  listTasks,
  listStatus,
  saveTasks,
  runTaskNow,
  type Task,
  type TaskStatus,
} from "@/api/tasks";

const WEEKDAYS = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
const pad0 = (n: number) => String(n).padStart(2, "0");

interface Visual {
  mode: "visual" | "expr";
  freq: "daily" | "weekly" | "monthly";
  time: string;
  weekdays: number[];
  monthDay: number;
}

const DEFAULT_VISUAL: Visual = { mode: "visual", freq: "daily", time: "00:00", weekdays: [], monthDay: 1 };

function parseCronVisual(cron: string): Visual | null {
  const f = (cron || "").trim().split(/\s+/);
  if (f.length !== 5) return null;
  const [mm, h, dd, , dw] = f;
  if (!/^\d+$/.test(mm) || !/^\d+$/.test(h)) return null;
  const time = `${pad0(+h)}:${pad0(+mm)}`;
  if (dd === "*" && dw === "*") return { ...DEFAULT_VISUAL, time };
  if (dd === "*" && /^[\d,]+$/.test(dw))
    return { ...DEFAULT_VISUAL, freq: "weekly", time, weekdays: dw.split(",").map(Number) };
  if (/^\d+$/.test(dd) && dw === "*") return { ...DEFAULT_VISUAL, freq: "monthly", time, monthDay: +dd };
  return null;
}

function buildCron(v: Visual): string {
  const [hh, mm] = (v.time || "00:00").split(":").map((s) => pad0(+s || 0));
  if (v.freq === "weekly") {
    const ws = [...v.weekdays].sort((a, b) => a - b);
    return `${mm} ${hh} * * ${ws.length ? ws.join(",") : "*"}`;
  }
  if (v.freq === "monthly") return `${mm} ${hh} ${v.monthDay} * *`;
  return `${mm} ${hh} * * *`;
}

function fmtTime(t?: string): string {
  if (!t) return "—";
  const d = new Date(t);
  if (isNaN(d.getTime()) || d.getFullYear() < 2000) return "—";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

export function TasksPage() {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [statusMap, setStatusMap] = useState<Record<string, TaskStatus>>({});
  const [editing, setEditing] = useState<Set<number>>(new Set());
  const [visual, setVisual] = useState<Record<number, Visual>>({});
  const [snapshot, setSnapshot] = useState<Record<number, Task>>({});
  const [groupFilter, setGroupFilter] = useState("");
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => {
    const [t, s] = await Promise.all([listTasks(), listStatus()]);
    setTasks(t);
    const map: Record<string, TaskStatus> = {};
    s.forEach((st) => (map[st.key] = st));
    setStatusMap(map);
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, 5000);
    return () => clearInterval(id);
  }, [refresh]);

  const keyOf = (t: Task) => t.name || t.command;
  const statusOf = (t: Task) => statusMap[keyOf(t)] || {};
  const groups = useMemo(() => [...new Set(tasks.map((t) => t.group.trim()).filter(Boolean))], [tasks]);

  const shown = groupFilter ? tasks.filter((t) => t.group.trim() === groupFilter) : tasks;

  const openEdit = (i: number) => {
    setSnapshot((prev) => ({ ...prev, [i]: { ...tasks[i] } }));
    const v = parseCronVisual(tasks[i].cron) || { ...DEFAULT_VISUAL, mode: "expr" as const, time: "00:00" };
    setVisual((prev) => ({ ...prev, [i]: v }));
    setEditing((prev) => new Set(prev).add(i));
  };

  const cancelEdit = (i: number) => {
    // 恢复快照，丢弃未保存改动
    setTasks((prev) => {
      const next = prev.slice();
      if (snapshot[i]) next[i] = snapshot[i];
      return next;
    });
    setEditing((prev) => {
      const next = new Set(prev);
      next.delete(i);
      return next;
    });
  };

  const update = (i: number, patch: Partial<Task>) => {
    setTasks((prev) => prev.map((t, idx) => (idx === i ? { ...t, ...patch } : t)));
  };

  const addNew = () => {
    const t: Task = { name: "", enabled: false, group: "", cron: buildCron(DEFAULT_VISUAL), command: "", timeout: "", notes: "" };
    setTasks((prev) => [...prev, t]);
    openEdit(tasks.length);
  };

  const remove = (i: number) => {
    setTasks((prev) => prev.filter((_, idx) => idx !== i));
    setEditing((prev) => {
      const next = new Set(prev);
      next.delete(i);
      return next;
    });
  };

  const save = async () => {
    const data = tasks.filter((t) => t.command.trim() !== "");
    try {
      await saveTasks(data);
      setNotice({ type: "ok", msg: "配置保存成功，定时任务将自动重启" });
      setEditing(new Set());
      setTimeout(refresh, 1500);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  const run = async (t: Task) => {
    try {
      const r = await runTaskNow({ command: t.command, timeout: t.timeout, key: keyOf(t) });
      setNotice({ type: r.success ? "ok" : "err", msg: `${r.success ? "执行成功" : "执行失败"} (${r.duration || ""})${r.error ? " " + r.error : ""}` });
      setTimeout(refresh, 800);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 5000);
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">定时任务</h1>
        <div className="flex items-center gap-2">
          <select
            value={groupFilter}
            onChange={(e) => setGroupFilter(e.target.value)}
            className="h-9 rounded-[var(--radius)] border bg-background px-2 text-sm"
          >
            <option value="">全部任务</option>
            {groups.map((g) => (
              <option key={g} value={g}>
                {g}
              </option>
            ))}
          </select>
          <Button onClick={addNew}>
            <Plus className="mr-1 h-4 w-4" /> 添加任务
          </Button>
        </div>
      </div>

      {notice && (
        <div
          className={`rounded-lg border px-3 py-2 text-sm ${
            notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"
          }`}
        >
          {notice.msg}
        </div>
      )}

      {shown.map((t) => {
        const i = tasks.indexOf(t);
        const isEdit = editing.has(i);
        return isEdit ? (
          <EditCard
            key={i}
            task={t}
            visual={visual[i] || DEFAULT_VISUAL}
            onVisual={(v) => setVisual((prev) => ({ ...prev, [i]: v }))}
            onUpdate={(p) => update(i, p)}
            onCancel={() => cancelEdit(i)}
            onDelete={() => remove(i)}
          />
        ) : (
          <ViewCard key={i} task={t} st={statusOf(t)} onEdit={() => openEdit(i)} onRun={() => run(t)} onDelete={() => remove(i)} />
        );
      })}

      {shown.length > 0 && (
        <div className="flex gap-2">
          <Button onClick={save}>保存全部配置</Button>
          <Button variant="secondary" onClick={refresh}>
            重置
          </Button>
        </div>
      )}
    </div>
  );
}

function ViewCard({ task, st, onEdit, onRun, onDelete }: { task: Task; st: TaskStatus; onEdit: () => void; onRun: () => void; onDelete: () => void }) {
  const stClass = st.ran ? (st.success ? "bg-green-600 text-white" : "bg-red-600 text-white") : "bg-muted text-muted-foreground";
  const stText = st.ran ? (st.success ? "成功" : "失败") : "未执行";
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <CardTitle className="text-base">
            {task.name || task.command || "(未命名)"}
            {task.group && <span className="ml-2 text-sm font-normal text-muted-foreground">{task.group}</span>}
          </CardTitle>
          <Badge className={stClass}>{stText}</Badge>
        </div>
        <div className="flex gap-1.5">
          <Button variant="secondary" size="sm" onClick={onRun} title="立即执行">
            <Play className="h-4 w-4" />
          </Button>
          <Button variant="outline" size="sm" onClick={onEdit} title="编辑">
            <Pencil className="h-4 w-4" />
          </Button>
          <Button variant="destructive" size="sm" onClick={onDelete} title="删除">
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-1 text-sm">
        <Row label="命令" value={task.command || "未设置"} />
        <Row label="Cron" value={task.cron || "—"} />
        <Row label="下次执行" value={fmtTime(st.next_run)} />
        <Row label="最近执行" value={st.ran ? `${fmtTime(st.last_run)}${st.last_duration ? " | " + st.last_duration : ""}` : "—"} />
        <Row label="执行结果" value={(st.last_message || "—").slice(0, 120)} />
      </CardContent>
    </Card>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex gap-3">
      <span className="w-20 shrink-0 text-muted-foreground">{label}</span>
      <span className="min-w-0 break-all text-foreground">{value}</span>
    </div>
  );
}

function EditCard({
  task,
  visual,
  onVisual,
  onUpdate,
  onCancel,
  onDelete,
}: {
  task: Task;
  visual: Visual;
  onVisual: (v: Visual) => void;
  onUpdate: (p: Partial<Task>) => void;
  onCancel: () => void;
  onDelete: () => void;
}) {
  const applyVisual = (v: Visual) => {
    onVisual(v);
    onUpdate({ cron: buildCron(v) });
  };
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between gap-2">
        <CardTitle className="text-base">编辑任务：{task.name || task.command || "(未命名)"}</CardTitle>
        <div className="flex gap-1.5">
          <Button variant="outline" size="sm" onClick={onCancel}>
            <X className="mr-1 h-4 w-4" /> 取消
          </Button>
          <Button variant="destructive" size="sm" onClick={onDelete}>
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label="任务名称">
            <Input value={task.name} onChange={(e) => onUpdate({ name: e.target.value })} placeholder="可选" />
          </Field>
          <Field label="分组">
            <Input value={task.group} onChange={(e) => onUpdate({ group: e.target.value })} placeholder="可选" />
          </Field>
          <Field label="启用">
            <Switch checked={task.enabled} onCheckedChange={(v) => onUpdate({ enabled: v })} />
          </Field>
        </div>

        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="执行时间设置">
            <ModeSwitch value={visual.mode} onChange={(mode) => onVisual({ ...visual, mode })} />
            {visual.mode === "expr" ? (
              <Input className="mt-2 font-mono" value={task.cron} onChange={(e) => onUpdate({ cron: e.target.value })} placeholder="0 */6 * * *" />
            ) : (
              <VisualEditor visual={visual} onChange={applyVisual} />
            )}
          </Field>
          <Field label="执行超时（可选，例: 60s）">
            <Input value={task.timeout} onChange={(e) => onUpdate({ timeout: e.target.value })} placeholder="例如: 60s" />
          </Field>
        </div>

        <Field label="执行命令（Shell 命令，可调用 PHP 脚本）">
          <textarea
            className="min-h-[80px] w-full rounded-[var(--radius)] border bg-background p-2 font-mono text-sm"
            value={task.command}
            onChange={(e) => onUpdate({ command: e.target.value })}
            placeholder="例如: /usr/bin/php /path/script.php"
          />
        </Field>

        <Field label="备注（可选）">
          <Input value={task.notes} onChange={(e) => onUpdate({ notes: e.target.value })} placeholder="用途说明" />
        </Field>
      </CardContent>
    </Card>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

function ModeSwitch({ value, onChange }: { value: "visual" | "expr"; onChange: (m: "visual" | "expr") => void }) {
  return (
    <div className="flex gap-4 rounded-[var(--radius)] border bg-muted/40 p-2 text-sm">
      <label className="flex items-center gap-1.5">
        <input type="radio" checked={value === "visual"} onChange={() => onChange("visual")} /> 可视化配置
      </label>
      <label className="flex items-center gap-1.5">
        <input type="radio" checked={value === "expr"} onChange={() => onChange("expr")} /> Cron 表达式
      </label>
    </div>
  );
}

function VisualEditor({ visual, onChange }: { visual: Visual; onChange: (v: Visual) => void }) {
  return (
    <div className="mt-2 space-y-2">
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
        <select
          className="h-9 rounded-[var(--radius)] border bg-background px-2 text-sm"
          value={visual.freq}
          onChange={(e) => onChange({ ...visual, freq: e.target.value as Visual["freq"] })}
        >
          <option value="daily">每天</option>
          <option value="weekly">每周</option>
          <option value="monthly">每月</option>
        </select>
        <input
          type="time"
          className="h-9 rounded-[var(--radius)] border bg-background px-2 text-sm"
          value={visual.time}
          onChange={(e) => onChange({ ...visual, time: e.target.value || "00:00" })}
        />
        <span className="self-center text-xs text-muted-foreground">→ {buildCron(visual)}</span>
      </div>
      {visual.freq === "weekly" && (
        <div className="flex flex-wrap gap-1.5">
          {WEEKDAYS.map((name, d) => (
            <label
              key={d}
              className={`rounded-full border px-2 py-1 text-xs ${visual.weekdays.includes(d) ? "bg-primary text-primary-foreground" : ""}`}
            >
              <input
                className="sr-only"
                type="checkbox"
                checked={visual.weekdays.includes(d)}
                onChange={() => {
                  const set = visual.weekdays.includes(d) ? visual.weekdays.filter((x) => x !== d) : [...visual.weekdays, d];
                  onChange({ ...visual, weekdays: set });
                }}
              />
              {name}
            </label>
          ))}
        </div>
      )}
      {visual.freq === "monthly" && (
        <select
          className="h-9 rounded-[var(--radius)] border bg-background px-2 text-sm"
          value={visual.monthDay}
          onChange={(e) => onChange({ ...visual, monthDay: +e.target.value || 1 })}
        >
          {Array.from({ length: 31 }, (_, i) => i + 1).map((d) => (
            <option key={d} value={d}>
              每月 {d} 日
            </option>
          ))}
        </select>
      )}
    </div>
  );
}