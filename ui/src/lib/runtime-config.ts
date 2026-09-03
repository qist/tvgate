export type RuntimeConfig = {
  appPathPrefix?: string;
  logLevel?: number;
};

function getRuntimeConfig(): RuntimeConfig {
  if (typeof globalThis === "undefined") {
    return {};
  }
  return (globalThis as { __TVGATE_CONFIG__?: RuntimeConfig }).__TVGATE_CONFIG__ ?? {};
}

export function getAppPathPrefix(): string {
  return getRuntimeConfig().appPathPrefix ?? "";
}

export function getRuntimeLogLevel(): number | undefined {
  const value = getRuntimeConfig().logLevel;
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}
