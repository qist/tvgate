import { createHashRouter, Navigate } from "react-router-dom";
import { AppShell } from "@/layouts/AppShell";
import { BlankLayout } from "@/layouts/BlankLayout";
import { Dashboard } from "@/views/overview/Dashboard";
import { TasksPage } from "@/views/config/Tasks";
import { ProxyGroupsPage } from "@/views/config/ProxyGroups";
import { DomainMapPage } from "@/views/config/DomainMap";
import { GlobalAuthPage } from "@/views/config/GlobalAuth";
import { PublisherPage } from "@/views/config/Publisher";
import { PlayerPage } from "@/views/config/Player";
import { SyncPage } from "@/views/config/Sync";
import { YAMLEditorPage } from "@/views/config/YAMLEditor";
import { ConfigViewPage } from "@/views/config/ConfigView";
import { LogsPage } from "@/views/config/Logs";
import { GithubPage } from "@/views/config/Github";
import { ConfigBackupPage } from "@/views/config/ConfigBackup";
import { BackupCenterPage } from "@/views/config/BackupCenter";
import { GroupConfigPage } from "@/views/config/GroupConfig";
import { JXPage } from "@/views/config/JX";
import { MulticastPage } from "@/views/config/Multicast";
import { TSPage } from "@/views/config/TS";
import { DNSPage } from "@/views/config/DNS";
import { ServerPage } from "@/views/config/Server";
import { HTTPPage } from "@/views/config/HTTP";
import { PHPPage } from "@/views/config/PHP";
import { ReloadPage } from "@/views/config/Reload";
import { WebPage } from "@/views/config/Web";
import { LogConfigPage } from "@/views/config/LogConfig";
import { MonitorPage } from "@/views/config/Monitor";
import { CodePage } from "@/views/content/Code";
import { OpsIndex } from "@/views/ops/Index";
import { Login } from "@/views/system/Login";
import { LegacyPage } from "@/views/legacy/LegacyPage";

export const router = createHashRouter([
  {
    path: "/login",
    element: <BlankLayout />,
    children: [{ index: true, element: <Login /> }],
  },
  {
    path: "/",
    element: <AppShell />,
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
      { path: "group-config", element: <GroupConfigPage /> },
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
      { path: "monitor", element: <MonitorPage /> },
      { path: "code", element: <CodePage /> },
      { path: "ops", element: <OpsIndex /> },
      { path: "legacy", element: <LegacyPage /> },
      { path: "*", element: <Navigate to="/" replace /> },
    ],
  },
]);