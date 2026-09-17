/**
 * HLS 源选择逻辑单测。聚焦 ⑤ 新增的 `pickAudioRendition`，
 * 该纯函数决定独立音频 rendition（EXT-X-MEDIA;TYPE=AUDIO）是否接入软解。
 */

import { describe, it, expect } from "vitest";
import { pickAudioRendition } from "./hls-source";
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
