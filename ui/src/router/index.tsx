import { lazy, Suspense } from "react";
import { createHashRouter, Navigate } from "react-router-dom";
import { AppShell } from "@/layouts/AppShell";
import { BlankLayout } from "@/layouts/BlankLayout";

// 路由级懒加载：首屏只加载当前模块，超大表单页单独 chunk
const lazyLoad = (factory: () => Promise<{ [k: string]: unknown }>, name: string) =>
  lazy(() => factory().then((m) => ({ default: m[name as keyof typeof m] as React.ComponentType })));

const Dashboard = lazyLoad(() => import("@/views/overview/Dashboard"), "Dashboard");
const TasksPage = lazyLoad(() => import("@/views/config/Tasks"), "TasksPage");
const ProxyGroupsPage = lazyLoad(() => import("@/views/config/ProxyGroups"), "ProxyGroupsPage");
const DomainMapPage = lazyLoad(() => import("@/views/config/DomainMap"), "DomainMapPage");
const GlobalAuthPage = lazyLoad(() => import("@/views/config/GlobalAuth"), "GlobalAuthPage");
const PublisherPage = lazyLoad(() => import("@/views/config/Publisher"), "PublisherPage");
const PlayerPage = lazyLoad(() => import("@/views/config/Player"), "PlayerPage");
const SyncPage = lazyLoad(() => import("@/views/config/Sync"), "SyncPage");
const YAMLEditorPage = lazyLoad(() => import("@/views/config/YAMLEditor"), "YAMLEditorPage");
const ConfigViewPage = lazyLoad(() => import("@/views/config/ConfigView"), "ConfigViewPage");
const LogsPage = lazyLoad(() => import("@/views/config/Logs"), "LogsPage");
const GithubPage = lazyLoad(() => import("@/views/config/Github"), "GithubPage");
const ConfigBackupPage = lazyLoad(() => import("@/views/config/ConfigBackup"), "ConfigBackupPage");
const BackupCenterPage = lazyLoad(() => import("@/views/config/BackupCenter"), "BackupCenterPage");
const JXPage = lazyLoad(() => import("@/views/config/JX"), "JXPage");
const MulticastPage = lazyLoad(() => import("@/views/config/Multicast"), "MulticastPage");
const TSPage = lazyLoad(() => import("@/views/config/TS"), "TSPage");
const DNSPage = lazyLoad(() => import("@/views/config/DNS"), "DNSPage");
const ServerPage = lazyLoad(() => import("@/views/config/Server"), "ServerPage");
const HTTPPage = lazyLoad(() => import("@/views/config/HTTP"), "HTTPPage");
const PHPPage = lazyLoad(() => import("@/views/config/PHP"), "PHPPage");
const ReloadPage = lazyLoad(() => import("@/views/config/Reload"), "ReloadPage");
const WebPage = lazyLoad(() => import("@/views/config/Web"), "WebPage");
const LogConfigPage = lazyLoad(() => import("@/views/config/LogConfig"), "LogConfigPage");
const CodePage = lazyLoad(() => import("@/views/content/Code"), "CodePage");
const OpsIndex = lazyLoad(() => import("@/views/ops/Index"), "OpsIndex");
const Login = lazyLoad(() => import("@/views/system/Login"), "Login");
const LegacyPage = lazyLoad(() => import("@/views/legacy/LegacyPage"), "LegacyPage");

/** 路由出口放在 Suspense 中（AppShell 内已包 ErrorBoundary） */
export function RouteFallback() {
  return (
    <Suspense fallback={<div className="py-12 text-center text-sm text-muted-foreground">加载中…</div>}>
      <AppShell />
    </Suspense>
  );
}

export const router = createHashRouter([
  {
    path: "/login",
    element: (
      <Suspense fallback={<div className="flex min-h-screen items-center justify-center text-sm text-muted-foreground">加载中…</div>}>
        <BlankLayout />
      </Suspense>
    ),
    children: [{ index: true, element: <Login /> }],
  },
  {
    path: "/",
    element: <RouteFallback />,
    children: [
      { index: true, element: <Dashboard /> },
      { path: "tasks", element: <TasksPage /> },
      { path: "proxygroups", element: <ProxyGroupsPage /> },
      { path: "domainmap", element: <DomainMapPage /> },
      { path: "global-auth", element: <GlobalAuthPage /> },
      { path: "publisher", element: <PublisherPage /> },
      { path: "player", element: <PlayerPage /> },
      { path: "sync", element: <SyncPage /> },
      { path: "yaml", element: <YAMLEditorPage /> },
      { path: "config", element: <ConfigViewPage /> },
      { path: "logs", element: <LogsPage /> },
      { path: "github", element: <GithubPage /> },
      { path: "config-backup", element: <ConfigBackupPage /> },
      { path: "backup-center", element: <BackupCenterPage /> },
      { path: "jx", element: <JXPage /> },
      { path: "multicast", element: <MulticastPage /> },
      { path: "ts", element: <TSPage /> },
      { path: "dns", element: <DNSPage /> },
      { path: "server", element: <ServerPage /> },
      { path: "http", element: <HTTPPage /> },
      { path: "php", element: <PHPPage /> },
      { path: "reload", element: <ReloadPage /> },
      { path: "web", element: <WebPage /> },
      { path: "log-config", element: <LogConfigPage /> },
      { path: "code", element: <CodePage /> },
      { path: "ops", element: <OpsIndex /> },
      { path: "legacy", element: <LegacyPage /> },
      { path: "*", element: <Navigate to="/" replace /> },
    ],
  },
]);

export default router;