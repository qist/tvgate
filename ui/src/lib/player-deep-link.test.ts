import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Channel } from "../types/player";
import { findDeepLinkTarget, syncChannelDeepLink } from "./player-deep-link";

/**
 * 仓库里没有装 jsdom/happy-dom（其余测试都是纯逻辑），这里给模块用到的
 * window.location.hash / window.history.replaceState 打一个最小替身。
 */
const fake = { hash: "" };
const replaceState = vi.fn();

beforeEach(() => {
  fake.hash = "";
  replaceState.mockClear();
  (globalThis as unknown as { window: unknown }).window = {
    location: {
      get hash() {
        return fake.hash;
      },
      set hash(value: string) {
        fake.hash = value;
      },
    },
    history: {
      replaceState: (_state: unknown, _title: string, url: string) => {
        replaceState(_state, _title, url);
        fake.hash = url;
      },
    },
  };
});

function channel(id: string, name: string, keys: string[], serverId?: string): Channel {
  return {
    id,
    name,
    serverId,
    groups: ["江苏移动"],
    sources: keys.map((key) => ({ key, url: `/player/${key}` })),
  };
}

const xizang = channel("7f345d2a64bd", "西藏卫视", ["7f345d2a64bd", "b8747a598ad1", "f1246e9e6ca5"], "558a250c10");
const cctv1 = channel("960640ab13a1", "CCTV1", ["960640ab13a1"]);

describe("findDeepLinkTarget 深链解析", () => {
  it("按频道 id（= 首线路 key）命中", () => {
    fake.hash = "#7f345d2a64bd";
    const t = findDeepLinkTarget([xizang, cctv1]);
    expect(t?.channel.name).toBe("西藏卫视");
    expect(t?.sourceIndex).toBe(0);
  });

  it("按任意线路 key 命中，并定位到该线路", () => {
    fake.hash = "#b8747a598ad1"; // 第 2 条线路
    const t = findDeepLinkTarget([xizang, cctv1]);
    expect(t?.channel.name).toBe("西藏卫视");
    expect(t?.sourceIndex).toBe(1);

    fake.hash = "#f1246e9e6ca5"; // 第 3 条线路
    expect(findDeepLinkTarget([xizang, cctv1])?.sourceIndex).toBe(2);
  });

  it("按服务端稳定 id 命中", () => {
    fake.hash = "#558a250c10";
    expect(findDeepLinkTarget([xizang, cctv1])?.channel.name).toBe("西藏卫视");
  });

  it("按唯一频道名命中（大小写不敏感）；名称有歧义时不跳", () => {
    fake.hash = "#CCTV1";
    expect(findDeepLinkTarget([xizang, cctv1])?.channel.id).toBe("960640ab13a1");

    const dup = channel("dup", "CCTV1", ["dupkey"]);
    expect(findDeepLinkTarget([cctv1, dup])).toBeNull();
  });

  it("认不出时返回 null（不误跳到别的台）", () => {
    fake.hash = "#deadbeef";
    expect(findDeepLinkTarget([xizang, cctv1])).toBeNull();
  });
});

describe("syncChannelDeepLink 写 hash", () => {
  it("名称唯一时写名称，歧义时写 id", () => {
    syncChannelDeepLink(xizang, [xizang, cctv1]);
    // 写入时按 URL 规则编码（非 ASCII 名称会被转义），读取侧 decodeURIComponent 还原
    expect(decodeURIComponent(fake.hash)).toBe("#西藏卫视");

    const dup = channel("dup", "西藏卫视", ["dupkey"]);
    syncChannelDeepLink(dup, [xizang, dup]);
    expect(decodeURIComponent(fake.hash)).toBe("#dup");
  });
});
