import { describe, it, expect } from "vitest";
import { FetchStreamLoader, LIVE_DATA_TIMEOUT_MS } from "./fetch-loader";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

describe("FetchStreamLoader 直播无数据看门狗（C9a：移动网络切换/静默挂死连接）", () => {
  it("无数据超过阈值即掐断（交上层重连）并标记 dataStalled", async () => {
    const loader = new FetchStreamLoader({ url: "http://x/live.ts" }, {}, { dataWatchdogMs: 30 });
    // biome-ignore lint/suspicious/noExplicitAny: 私有方法仅测试内调用
    (loader as any).armDataWatchdog();

    await sleep(80);

    expect(loader.dataStalled).toBe(true);
  });

  it("持续收到数据即不断刷新计时（不掐断），停止刷新后才掐断", async () => {
    const loader = new FetchStreamLoader({ url: "http://x/live.ts" }, {}, { dataWatchdogMs: 40 });
    // biome-ignore lint/suspicious/noExplicitAny: 私有方法仅测试内调用
    const arm = () => (loader as any).armDataWatchdog();

    arm();
    for (let i = 0; i < 4; i++) {
      await sleep(20);
      arm(); // 模拟收到数据
    }
    expect(loader.dataStalled).toBe(false);

    await sleep(90);
    expect(loader.dataStalled).toBe(true);
  });

  it("未开启看门狗（缺省 0）不会掐断", async () => {
    const loader = new FetchStreamLoader({ url: "http://x/live.ts" }, {});
    // biome-ignore lint/suspicious/noExplicitAny: 私有方法仅测试内调用
    (loader as any).armDataWatchdog();

    await sleep(50);

    expect(loader.dataStalled).toBe(false);
  });

  it("直播阈值常量为 20s", () => {
    expect(LIVE_DATA_TIMEOUT_MS).toBe(20_000);
  });
});
