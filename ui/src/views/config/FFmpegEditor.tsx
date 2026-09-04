import { useEffect, useState } from "react";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { ChevronDown, ChevronRight } from "lucide-react";
import type { FFmpegOptions } from "@/api/publisher";

function hasAnyOpts(o?: FFmpegOptions): boolean {
  if (!o) return false;
  for (const v of Object.values(o)) {
    if (v === null || v === undefined) continue;
    if (typeof v === "string" && v.trim() === "") continue;
    if (typeof v === "number" && v === 0) continue;
    if (typeof v === "boolean" && v === false) continue;
    if (Array.isArray(v) && v.length === 0) continue;
    if (typeof v === "object" && !Array.isArray(v) && Object.keys(v).length === 0) continue;
    return true;
  }
  return false;
}

function lines(v?: string[]): string {
  return (v || []).join("\n");
}
function toArr(s: string): string[] {
  return s.split(/\r?\n/).map((x) => x.trim()).filter((x) => x);
}

function TextArea({
  value,
  onApply,
  placeholder,
}: {
  value?: string[];
  onApply: (v?: string[]) => void;
  placeholder?: string;
}) {
  const [text, setText] = useState(lines(value));
  useEffect(() => setText(lines(value)), [value]);
  return (
    <textarea
      value={text}
      spellCheck={false}
      className="h-20 w-full resize-y rounded-[var(--radius)] border border-input bg-background p-2 font-mono text-xs text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      placeholder={placeholder}
      onChange={(e) => setText(e.target.value)}
      onBlur={() => onApply(toArr(text).length ? toArr(text) : undefined)}
    />
  );
}

interface Opt {
  v: string;
  l: string;
}

// 枚举下拉：默认空=不设置；若存储值不在预设内则额外补一项保留原值
function EnumSelect({
  label,
  value,
  presets,
  onApply,
}: {
  label: string;
  value?: string | number;
  presets: Opt[];
  onApply: (v?: string | number) => void;
}) {
  const cur = value === undefined ? "" : String(value);
  const inPresets = presets.some((p) => p.v === cur);
  const options = inPresets ? presets : [{ v: cur, l: `${cur}（保留）` }, ...presets];
  return (
    <div className="space-y-1">
      <Label className="text-xs text-muted-foreground">{label}</Label>
      <select
        className="h-8 w-full rounded-[var(--radius)] border border-input bg-background px-2 text-xs text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        value={cur}
        onChange={(e) => {
          const v = e.target.value;
          onApply(v === "" ? undefined : v);
        }}
      >
        <option value="">默认（不设置）</option>
        {options.map((p) => (
          <option key={p.v} value={p.v}>
            {p.l}
          </option>
        ))}
      </select>
    </div>
  );
}

// 数字下拉（用于 crf / gop_size）：预设为数字，支持保留原值
function NumberSelect({
  label,
  value,
  presets,
  onApply,
}: {
  label: string;
  value?: string | number;
  presets: number[];
  onApply: (v?: number) => void;
}) {
  const cur = value === undefined || value === "" ? "" : String(value);
  const inPresets = cur !== "" && presets.map(String).includes(cur);
  const opts = inPresets ? presets.map(String) : [cur, ...presets.map(String)].filter((v) => v !== "");
  return (
    <div className="space-y-1">
      <Label className="text-xs text-muted-foreground">{label}</Label>
      <select
        className="h-8 w-full rounded-[var(--radius)] border border-input bg-background px-2 text-xs text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        value={cur}
        onChange={(e) => {
          const v = e.target.value;
          onApply(v === "" ? undefined : Number(v));
        }}
      >
        <option value="">默认（不设置）</option>
        {opts.map((v) => (
          <option key={v} value={v}>
            {v}
          </option>
        ))}
      </select>
    </div>
  );
}

const videoCodecs: Opt[] = [
  { v: "copy", l: "copy" },
  { v: "libx264", l: "libx264" },
  { v: "libx265", l: "libx265" },
  { v: "h264_nvenc", l: "h264_nvenc" },
  { v: "hevc_nvenc", l: "hevc_nvenc" },
];
const audioCodecs: Opt[] = [
  { v: "copy", l: "copy" },
  { v: "aac", l: "aac" },
  { v: "libfdk_aac", l: "libfdk_aac" },
  { v: "opus", l: "opus" },
];
const videoBitrate: Opt[] = [
  { v: "1M", l: "1M" },
  { v: "2M", l: "2M" },
  { v: "4M", l: "4M" },
  { v: "6M", l: "6M" },
  { v: "8M", l: "8M" },
];
const audioBitrate: Opt[] = [
  { v: "96k", l: "96k" },
  { v: "128k", l: "128k" },
  { v: "192k", l: "192k" },
  { v: "256k", l: "256k" },
];
const presetsLib: Opt[] = [
  { v: "ultrafast", l: "ultrafast" },
  { v: "superfast", l: "superfast" },
  { v: "veryfast", l: "veryfast" },
  { v: "faster", l: "faster" },
  { v: "fast", l: "fast" },
  { v: "medium", l: "medium" },
  { v: "slow", l: "slow" },
];
const pixFmt: Opt[] = [
  { v: "yuv420p", l: "yuv420p" },
  { v: "yuv422p", l: "yuv422p" },
  { v: "yuv420p10le", l: "yuv420p10le" },
  { v: "nv12", l: "nv12" },
];
const userAgent: Opt[] = [
  { v: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36", l: "Chrome" },
  { v: "VLC/3.0.20 LibVLC/3.0.20", l: "VLC" },
  { v: "Mozilla/5.0 (Linux; Android 10) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36", l: "Android Chrome" },
];
const outputFormat: Opt[] = [
  { v: "flv", l: "flv" },
  { v: "mpegts", l: "mpegts" },
  { v: "matroska", l: "matroska" },
  { v: "mp4", l: "mp4" },
];

function CheckRow({ label, checked, onApply }: { label: string; checked?: boolean; onApply: (v: boolean) => void }) {
  return (
    <label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
      <input type="checkbox" className="accent-[hsl(var(--primary))]" checked={!!checked} onChange={(e) => onApply(e.target.checked)} />
      {label}
    </label>
  );
}

export function FFmpegEditor({
  title,
  value,
  onChange,
}: {
  title: string;
  value?: FFmpegOptions;
  onChange: (v?: FFmpegOptions) => void;
}) {
  // 开启状态 = 用户显式启用（value 非 undefined，含空对象 {}）。
  // 不能用 hasAnyOpts 判断：开启时 onChange({}) 传空参数集，hasAnyOpts({})
  // 为 false 会导致开关刚点开就被弹回关闭状态，永远无法开启。
  const enabled = value != null;
  const o: FFmpegOptions = value || {};
  const [open, setOpen] = useState(false);

  const apply = (patch: Partial<FFmpegOptions>) => {
    const next: FFmpegOptions = { ...o };
    for (const k of Object.keys(patch) as (keyof FFmpegOptions)[]) {
      const v = patch[k];
      if (v === undefined) delete next[k];
      else {
        (next as any)[k] = v;
      }
    }
    if (hasAnyOpts(next)) onChange(next);
    else onChange(undefined);
  };

  return (
    <div className="rounded-lg border border-border bg-card">
      <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
        <button type="button" className="flex items-center gap-1 text-sm font-semibold" onClick={() => setOpen(!open)}>
          {open ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
          {title}
        </button>
        <div className="flex items-center gap-2">
          <Label className="cursor-pointer select-none text-xs text-muted-foreground">自定义</Label>
          <Switch checked={enabled} onCheckedChange={(v) => (v ? onChange({}) : onChange(undefined))} />
        </div>
      </div>
      {open && (
        <div className="space-y-3 p-3">
          <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">全局参数 (global_args)</Label>
              <TextArea value={o.global_args} placeholder="每行一个" onApply={(v) => apply({ global_args: v })} />
            </div>
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">输入前参数 (input_pre_args)</Label>
              <TextArea value={o.input_pre_args} placeholder="每行一个" onApply={(v) => apply({ input_pre_args: v })} />
            </div>
          </div>
          <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">输入后参数 (input_post_args)</Label>
              <TextArea value={o.input_post_args} placeholder="每行一个" onApply={(v) => apply({ input_post_args: v })} />
            </div>
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">输出前参数 (output_pre_args)</Label>
              <TextArea value={o.output_pre_args} placeholder="每行一个" onApply={(v) => apply({ output_pre_args: v })} />
            </div>
          </div>
          <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">输出后参数 (output_post_args)</Label>
              <TextArea value={o.output_post_args} placeholder="每行一个" onApply={(v) => apply({ output_post_args: v })} />
            </div>
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">自定义参数 (custom_args)</Label>
              <TextArea value={o.custom_args} placeholder="每行一个" onApply={(v) => apply({ custom_args: v })} />
            </div>
          </div>
          <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
            <EnumSelect label="视频编码器 (video_codec)" value={o.video_codec} presets={videoCodecs} onApply={(v) => apply({ video_codec: v as string | undefined })} />
            <EnumSelect label="音频编码器 (audio_codec)" value={o.audio_codec} presets={audioCodecs} onApply={(v) => apply({ audio_codec: v as string | undefined })} />
          </div>
          <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
            <EnumSelect label="视频码率 (video_bitrate)" value={o.video_bitrate} presets={videoBitrate} onApply={(v) => apply({ video_bitrate: v as string | undefined })} />
            <EnumSelect label="音频码率 (audio_bitrate)" value={o.audio_bitrate} presets={audioBitrate} onApply={(v) => apply({ audio_bitrate: v as string | undefined })} />
          </div>
          <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
            <EnumSelect label="Preset (preset)" value={o.preset} presets={presetsLib} onApply={(v) => apply({ preset: v as string | undefined })} />
            <NumberSelect label="CRF (crf)" value={o.crf} presets={[18, 20, 23, 26, 28]} onApply={(v) => apply({ crf: v })} />
          </div>
          <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
            <EnumSelect label="像素格式 (pix_fmt)" value={o.pix_fmt} presets={pixFmt} onApply={(v) => apply({ pix_fmt: v as string | undefined })} />
            <NumberSelect label="GOP (gop_size)" value={o.gop_size} presets={[25, 50, 60, 100]} onApply={(v) => apply({ gop_size: v })} />
          </div>
          <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
            <EnumSelect label="User-Agent (user_agent)" value={o.user_agent} presets={userAgent} onApply={(v) => apply({ user_agent: v as string | undefined })} />
            <EnumSelect label="输出格式 (output_format)" value={o.output_format} presets={outputFormat} onApply={(v) => apply({ output_format: v as string | undefined })} />
          </div>
          <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">请求头 (headers)</Label>
              <TextArea value={o.headers} placeholder="每行一个 Header" onApply={(v) => apply({ headers: v })} />
            </div>
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">视频滤镜 (video_filters)</Label>
              <TextArea value={o.filters?.video_filters} placeholder="每行一个滤镜" onApply={(v) => apply({ filters: { ...o.filters, video_filters: v } })} />
            </div>
          </div>
          <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">音频滤镜 (audio_filters)</Label>
              <TextArea value={o.filters?.audio_filters} placeholder="每行一个滤镜" onApply={(v) => apply({ filters: { ...o.filters, audio_filters: v } })} />
            </div>
            <div className="flex items-end gap-6 pt-1">
              <CheckRow label="StreamCopy" checked={o.stream_copy} onApply={(v) => apply({ stream_copy: v })} />
              <CheckRow label="UseReFlag" checked={o.use_re_flag} onApply={(v) => apply({ use_re_flag: v })} />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}