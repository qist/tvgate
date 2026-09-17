import { describe, expect, it } from "vitest";
import type { Channel } from "../types/player";
import {
  type EPGData,
  fillEPGGaps,
  getCurrentProgram,
  getEPGChannelId,
  mapPrograms,
  parseEpgTime,
} from "./epg-parser";

/** 本地时区构造，便于与 parseEpgTime 的"按本地时区解释"语义对齐。 */
const at = (y: number, mo: number, da: number, h = 0, mi = 0, se = 0) => new Date(y, mo - 1, da, h, mi, se).getTime();

const channel = (over: Partial<Channel> & { id: string; name: string }): Channel => ({
  groups: [],
  sources: [],
  ...over,
});

describe("parseEpgTime", () => {
  it("解析服务端下发的 XMLTV 原样串（带空格时区后缀）", () => {
    // 回归：正则漏掉 " +0800" 后缀时，服务端返回的每条节目都会被丢弃
    expect(parseEpgTime("20260916120000 +0800")?.getTime()).toBe(at(2026, 9, 16, 12));
  });

  it("解析无时区后缀 / 冒号时区 / 短格式等变体", () => {
    expect(parseEpgTime("20260916120000")?.getTime()).toBe(at(2026, 9, 16, 12));
    expect(parseEpgTime("202609161200")?.getTime()).toBe(at(2026, 9, 16, 12));
    expect(parseEpgTime("20260916")?.getTime()).toBe(at(2026, 9, 16));
    expect(parseEpgTime("20260916120000+08:00")?.getTime()).toBe(at(2026, 9, 16, 12));
  });

  it("解析 ISO 串", () => {
    expect(parseEpgTime("2026-09-16T12:00:00")?.getTime()).toBe(at(2026, 9, 16, 12));
  });

  it("空串与非法串返回 null", () => {
    expect(parseEpgTime("")).toBeNull();
    expect(parseEpgTime("   ")).toBeNull();
    expect(parseEpgTime("not-a-time")).toBeNull();
  });
});

describe("mapPrograms", () => {
  it("XMLTV 串逐条解析成功（回归：整批被丢弃 → 前端 EPG 完全没数据）", () => {
    const out = mapPrograms([
      { start: "20260916120000 +0800", stop: "20260916123000 +0800", title: "新闻30分" },
      { start: "20260916123000 +0800", stop: "20260916130000 +0800", title: "戏曲" },
    ]);
    expect(out).toHaveLength(2);
    expect(out[0].title).toBe("新闻30分");
    expect(out[0].start.getTime()).toBe(at(2026, 9, 16, 12));
    expect(out[0].end.getTime()).toBe(at(2026, 9, 16, 12, 30));
  });

  it("丢弃时间非法或区间倒置的条目", () => {
    expect(mapPrograms([{ start: "", stop: "", title: "x" }])).toHaveLength(0);
    expect(mapPrograms([{ start: "20260916120000 +0800", stop: "20260916110000 +0800", title: "x" }])).toHaveLength(0);
    expect(mapPrograms(undefined)).toHaveLength(0);
  });
});

describe("getEPGChannelId / getCurrentProgram / fillEPGGaps", () => {
  it("频道键按 tvgId → tvgName → name 回退", () => {
    expect(getEPGChannelId(channel({ id: "k", name: "CCTV1", tvgId: "cctv1.example" }))).toBe("cctv1.example");
    expect(getEPGChannelId(channel({ id: "k", name: "CCTV1" }))).toBe("CCTV1");
  });

  it("取给定时刻正在播出的节目", () => {
    const epg: EPGData = {
      k: [{ id: "a", title: "A", start: new Date(at(2026, 9, 16, 12)), end: new Date(at(2026, 9, 16, 13)) }],
    };
    expect(getCurrentProgram("k", epg, new Date(at(2026, 9, 16, 12, 30)))?.title).toBe("A");
    expect(getCurrentProgram("k", epg, new Date(at(2026, 9, 16, 13, 30)))).toBeNull();
  });

  it("缝隙填充：只补支持回看且无数据的频道，标题为空串（展示层翻译兜底）", () => {
    const withCatchup = channel({
      id: "a",
      name: "A",
      sources: [{ url: "/x", catchup: "server", catchupSource: "server" }],
    });
    const withoutCatchup = channel({ id: "b", name: "B", sources: [{ url: "/y" }] });
    const filled = fillEPGGaps({}, [withCatchup, withoutCatchup]);
    expect(filled.A).toHaveLength(1);
    expect(filled.A[0].title).toBe("");
    expect(filled.B).toBeUndefined();
  });
});
