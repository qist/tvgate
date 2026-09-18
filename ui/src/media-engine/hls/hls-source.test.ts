/**
 * HLS 源选择逻辑单测。聚焦 ⑤ 新增的 `pickAudioRendition`，
 * 该纯函数决定独立音频 rendition（EXT-X-MEDIA;TYPE=AUDIO）是否接入软解。
 */

import { describe, it, expect } from "vitest";
import { HlsSource, pickAudioRendition } from "./hls-source";
import type { HlsMediaRendition } from "../formats/m3u8";

function rendition(p: Partial<HlsMediaRendition>): HlsMediaRendition {
  return {
    type: "AUDIO",
    groupId: p.groupId ?? "a",
    uri: p.uri,
    language: p.language,
    name: p.name,
    isDefault: p.isDefault,
    autoselect: p.autoselect,
    channels: p.channels,
  };
}

describe("pickAudioRendition", () => {
  it("无 audioGroup → 不接入（返回 undefined，视频可播但静音）", () => {
    const r = pickAudioRendition([rendition({ groupId: "a", uri: "x.m3u8" })], undefined);
    expect(r).toBeUndefined();
  });

  it("无匹配 group → 返回 undefined", () => {
    const r = pickAudioRendition([rendition({ groupId: "b", uri: "x.m3u8" })], "a");
    expect(r).toBeUndefined();
  });

  it("无 uri（纯主轨无独立音频）→ 返回 undefined", () => {
    const r = pickAudioRendition([rendition({ groupId: "a", uri: undefined })], "a");
    expect(r).toBeUndefined();
  });

  it("优先级：DEFAULT+AUTOSELECT > DEFAULT > AUTOSELECT > 首条", () => {
    const list = [
      rendition({ groupId: "a", uri: "first.m3u8", language: "und" }),
      rendition({ groupId: "a", uri: "auto.m3u8", autoselect: true, language: "en" }),
      rendition({ groupId: "a", uri: "def.m3u8", isDefault: true, language: "zh" }),
      rendition({ groupId: "a", uri: "both.m3u8", isDefault: true, autoselect: true, language: "zh" }),
    ];
    expect(pickAudioRendition(list, "a")?.uri).toBe("both.m3u8");

    const noBoth = list.filter((r) => r.uri !== "both.m3u8");
    expect(pickAudioRendition(noBoth, "a")?.uri).toBe("def.m3u8");

    const noDef = noBoth.filter((r) => r.uri !== "def.m3u8");
    expect(pickAudioRendition(noDef, "a")?.uri).toBe("auto.m3u8");

    const onlyFirst = noDef.filter((r) => r.uri !== "auto.m3u8");
    expect(pickAudioRendition(onlyFirst, "a")?.uri).toBe("first.m3u8");
  });

  it("重庆有线类场景：单条 DEFAULT+AUTOSELECT 中文音轨被选中", () => {
    const r = pickAudioRendition(
      [rendition({ groupId: "audio", uri: "audio/chongqing.m3u8", isDefault: true, autoselect: true, language: "zh" })],
      "audio",
    );
    expect(r?.uri).toBe("audio/chongqing.m3u8");
    expect(r?.language).toBe("zh");
  });
});

describe("HlsSource.pollIntervalMs", () => {
  /** 目标时长 target、起始序号 seq 的媒体播放列表（序号相同 = 没有新分片）。 */
  const playlist = (target: number, seq = 1): string =>
    `#EXTM3U\n#EXT-X-TARGETDURATION:${target}\n#EXT-X-MEDIA-SEQUENCE:${seq}\n` +
    `#EXTINF:${target},\nseg${seq}.ts\n#EXTINF:${target},\nseg${seq + 1}.ts\n`;

  it("刚拿到新分片 → 按目标时长整拍问（一拍一次，不再半拍两次）", async () => {
    for (const [target, want] of [
      [10, 10_000], // 江苏移动这类 10s 分片
      [7, 7_000], // 广东联通内网 TARGETDURATION=7
      [4, 4_000],
      [2, 2_000],
      [30, 10_000], // 超长分片：封顶 10 秒
      [0.5, 1_000], // 极短分片：不小于 1 秒
    ] as const) {
      const source = new HlsSource("http://x/p.m3u8", {}, { fetcher: async () => playlist(target) });
      await source.load(); // 首次入队 = 有新分片
      expect(source.pollIntervalMs()).toBe(want);
    }
  });

  it("这一问没有新分片 → 半拍后再问（既不空刷也不过晚）", async () => {
    for (const [target, want] of [
      [10, 5_000],
      [4, 2_000],
      [2, 1_000],
      [30, 5_000], // 半拍同样封顶 5 秒
    ] as const) {
      const source = new HlsSource("http://x/p.m3u8", {}, { fetcher: async () => playlist(target) });
      await source.load();
      await source.refresh(); // 同一份播放列表 → 无新分片
      expect(source.pollIntervalMs()).toBe(want);
    }
  });

  it("未解析出目标时长时退回 1 秒", async () => {
    const source = new HlsSource("http://x/p.m3u8", {}, { fetcher: async () => "#EXTM3U\n" });
    await source.load();
    expect(source.pollIntervalMs()).toBe(1000);
  });
});
