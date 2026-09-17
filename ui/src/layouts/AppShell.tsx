import { useEffect, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import {
  Archive,
  ClipboardList,
  Clock,
  Code2,
  Database,
  FileCode,
  FileJson,
  Github,
  Globe,
  HardDrive,
  LayoutDashboard,
  LogOut,
  Map,
  Menu,
  Moon,
  Network,
  Palette,
  Radio,
  RefreshCw,
  RotateCcw,
  Rss,
  Tv,
  Search,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  Sun,
  Terminal,
  Wifi,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { SelectBox } from "@/components/ui/select-box";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { useAppearance } from "@/hooks/use-appearance";
import { useTheme } from "@/hooks/use-theme";
import { APPEARANCE_ACCENT_CSS, APPEARANCE_LABELS } from "@/lib/appearance";
import { PLAYER_APPEARANCES, type PlayerAppearance } from "@/types/ui";
import { logout } from "@/api/auth";

const navGroups = [
  { label: "概览", items: [{ key: "overview", to: "/", icon: LayoutDashboard, label: "仪表盘" }] },
  {
    label: "配置",
    items: [
      { key: "tasks", to: "/tasks", icon: Clock, label: "定时任务" },
      { key: "proxygroups", to: "/proxygroups", icon: Network, label: "代理组" },
      { key: "jx", to: "/jx", icon: Search, label: "视频解析" },
      { key: "publisher", to: "/publisher", icon: Rss, label: "推流发布" },
      { key: "domainmap", to: "/domainmap", icon: Map, label: "域名映射" },
      { key: "player", to: "/player", icon: Tv, label: "播放器" },
      { key: "global-auth", to: "/global-auth", icon: ShieldCheck, label: "全局认证" },
      { key: "multicast", to: "/multicast", icon: Radio, label: "组播配置" },
      { key: "ts", to: "/ts", icon: HardDrive, label: "TS 缓存" },
      { key: "config", to: "/config", icon: ClipboardList, label: "配置查看" },
    ],
  },
  {
    label: "服务",
    items: [
      { key: "server", to: "/server", icon: Server, label: "服务器" },
      { key: "http", to: "/http", icon: Globe, label: "HTTP" },
      { key: "dns", to: "/dns", icon: Wifi, label: "DNS" },
      { key: "php", to: "/php", icon: FileCode, label: "PHP 模块" },
      { key: "reload", to: "/reload", icon: RotateCcw, label: "重载" },
      { key: "web", to: "/web", icon: Palette, label: "Web 设置" },
      { key: "log-config", to: "/log-config", icon: SlidersHorizontal, label: "日志配置" },
    ],
  },
  {
    label: "内容",
    items: [
      { key: "code", to: "/code", icon: Code2, label: "代码文件" },
      { key: "sync", to: "/sync", icon: RefreshCw, label: "仓库同步" },
      { key: "github", to: "/github", icon: Github, label: "GitHub 加速 & 版本升级" },
    ],
  },
  {
    label: "运维",
    items: [
      { key: "logs", to: "/logs", icon: Terminal, label: "实时日志" },
      { key: "config-backup", to: "/config-backup", icon: Archive, label: "配置备份" },
      { key: "backup-center", to: "/backup-center", icon: Database, label: "备份中心" },
    ],
  },
  {
    label: "工具",
    items: [{ key: "yaml", to: "/yaml", icon: FileJson, label: "YAML 编辑器" }],
  },
];

export function AppShell() {
  const { theme, setTheme } = useTheme();
  const { appearance, setAppearance } = useAppearance();
  const navigate = useNavigate();
  const location = useLocation();
  const [mobileOpen, setMobileOpen] = useState(false);
  // 认证守卫：未认证前不渲染任何子页面，统一跳登录
  const [authed, setAuthed] = useState<boolean | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetch(new URL("auth-status", window.location.href.split("#")[0]).toString(), { credentials: "same-origin" })
      .then((r) => r.json())
      .then((j: { authenticated?: boolean }) => {
        if (cancelled) return;
        if (j && j.authenticated === true) {
          setAuthed(true);
        } else {
          // 未认证：进公开登录页（白名单），不渲染任何授权页面
          navigate("/login", { replace: true });
        }
      })
      .catch(() => {
        if (!cancelled) navigate("/login", { replace: true });
      });
    return () => {
      cancelled = true;
    };
  }, [navigate]);

  if (authed !== true) {
    return <div className="flex min-h-screen items-center justify-center text-sm text-muted-foreground">加载中…</div>;
  }

  const handleLogout = async () => {
    await logout();
    navigate("/login");
  };

  return (
    <div className="admin-app flex min-h-screen">
      {/* 移动端遮罩 */}
      {mobileOpen && <div className="fixed inset-0 z-30 bg-black/40 backdrop-blur-sm md:hidden" onClick={() => setMobileOpen(false)} />}
      {/* 侧栏：桌面常驻（可折叠）；移动端为抽屉 */}
      <aside
        className={`flex w-60 flex-col border-r border-border/60 bg-card/70 backdrop-blur-xl ${
          mobileOpen ? "fixed inset-y-0 left-0 z-40 shadow-2xl" : "hidden md:flex"
        }`}
      >
        <div className="flex h-14 items-center gap-2.5 border-b border-border/60 px-4">
          {mobileOpen && (
            <Button variant="ghost" size="icon" className="md:hidden" onClick={() => setMobileOpen(false)}>
              <Menu className="h-4 w-4" />
            </Button>
          )}
          <span
            aria-hidden="true"
            className="grid h-7 w-7 place-items-center rounded-[0.6rem] bg-[linear-gradient(135deg,rgb(var(--pg-rgb)),rgb(var(--pg-rgb-2)))] text-[13px] font-black text-white shadow-[0_6px_16px_-8px_rgba(var(--pg-rgb),0.9)]"
          >
            T
          </span>
          <span className="text-[15px] font-bold tracking-[-0.01em] text-foreground">TVGate</span>
        </div>
        <nav className="flex-1 space-y-4 overflow-y-auto p-3">
          {navGroups.map((g) => (
            <div key={g.label}>
              <div className="px-2.5 pb-1.5 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground/80">
                {g.label}
              </div>
              <div className="space-y-0.5">
                {g.items.map((it) => {
                  const active = location.pathname + location.search === it.to;
                  return (
                    <button
                      key={it.to}
                      onClick={() => {
                        navigate(it.to);
                        setMobileOpen(false);
                      }}
                      className={`relative flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2 text-left text-sm transition-colors duration-150 motion-reduce:transition-none ${
                        active
                          ? "bg-primary/12 font-medium text-[hsl(var(--primary-text))]"
                          : "text-muted-foreground hover:bg-primary/6 hover:text-foreground"
                      }`}
                    >
                      {/* 激活态左侧色条（风格色） */}
                      <span
                        aria-hidden="true"
                        className={`absolute left-0 top-1/2 h-4 w-0.5 -translate-y-1/2 rounded-full bg-[linear-gradient(180deg,rgb(var(--pg-rgb)),rgb(var(--pg-rgb-2)))] transition-opacity ${
                          active ? "opacity-100" : "opacity-0"
                        }`}
                      />
                      <it.icon className={`h-4 w-4 shrink-0 ${active ? "" : "opacity-85"}`} />
                      <span className="truncate">{it.label}</span>
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </nav>
      </aside>

      {/* 主区 */}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-20 flex h-14 items-center gap-2 border-b border-border/60 bg-background/60 px-4 backdrop-blur-xl">
          <Button variant="ghost" size="icon" className="md:hidden" onClick={() => setMobileOpen(true)}>
            <Menu className="h-4 w-4" />
          </Button>
            <div className="flex-1" />
            {/* 界面风格（6 套配色）：与播放页共用同一存储键，改完两边同时生效 */}
            <div className="flex items-center gap-2" title="界面风格（与播放页共用同一设置）">
              <span
                aria-hidden="true"
                className="h-3.5 w-3.5 shrink-0 rounded-full border border-border/60"
                style={{ backgroundColor: APPEARANCE_ACCENT_CSS }}
              />
              <SelectBox
                variant="sm"
                aria-label="界面风格"
                containerClassName="min-w-[5.5rem] sm:min-w-[6.5rem]"
                value={appearance}
                onChange={(e) => setAppearance(e.currentTarget.value as PlayerAppearance)}
              >
                {PLAYER_APPEARANCES.map((name) => (
                  <option key={name} value={name}>
                    {APPEARANCE_LABELS[name]}
                  </option>
                ))}
              </SelectBox>
            </div>
            <Button
              variant="ghost"
              size="icon"
              title="切换主题"
              onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
            >
              {theme === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </Button>
            <Button variant="ghost" size="icon" title="退出登录" onClick={handleLogout}>
              <LogOut className="h-4 w-4" />
            </Button>
          </header>
          <main className="flex-1 p-4 md:p-6">
            <ErrorBoundary>
              <Outlet />
            </ErrorBoundary>
          </main>
        </div>
      </div>
  );
}