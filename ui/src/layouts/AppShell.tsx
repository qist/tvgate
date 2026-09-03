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
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { useTheme } from "@/hooks/use-theme";
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
      { key: "github", to: "/github", icon: Github, label: "GitHub 加速" },
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
    <div className="flex min-h-screen">
      {/* 移动端遮罩 */}
      {mobileOpen && <div className="fixed inset-0 z-30 bg-black/40 md:hidden" onClick={() => setMobileOpen(false)} />}
      {/* 侧栏：桌面常驻（可折叠）；移动端为抽屉 */}
      <aside
        className={`flex w-60 flex-col border-r bg-card ${
          mobileOpen ? "fixed inset-y-0 left-0 z-40 shadow-lg" : "hidden md:flex"
        }`}
      >
        <div className="flex h-14 items-center gap-2 border-b px-4">
          {mobileOpen && (
            <Button variant="ghost" size="icon" className="md:hidden" onClick={() => setMobileOpen(false)}>
              <Menu className="h-4 w-4" />
            </Button>
          )}
          <span className="font-bold text-primary">TVGate</span>
        </div>
        <nav className="flex-1 space-y-4 overflow-y-auto p-3">
          {navGroups.map((g) => (
            <div key={g.label}>
                <div className="px-2 pb-1 text-xs font-semibold text-muted-foreground">{g.label}</div>
                {g.items.map((it) => {
                  const active = location.pathname + location.search === it.to;
                  return (
                    <button
                      key={it.to}
                      onClick={() => {
                        navigate(it.to);
                        setMobileOpen(false);
                      }}
                      className={`flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm ${
                        active ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-accent/50"
                      }`}
                    >
                      <it.icon className="h-4 w-4 shrink-0" />
                      <span>{it.label}</span>
                    </button>
                  );
                })}
              </div>
          ))}
        </nav>
      </aside>

      {/* 主区 */}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 items-center gap-2 border-b bg-card px-4">
          <Button variant="ghost" size="icon" className="md:hidden" onClick={() => setMobileOpen(true)}>
            <Menu className="h-4 w-4" />
          </Button>
            <div className="flex-1" />
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
          <main className="flex-1 p-4">
            <ErrorBoundary>
              <Outlet />
            </ErrorBoundary>
          </main>
        </div>
      </div>
  );
}