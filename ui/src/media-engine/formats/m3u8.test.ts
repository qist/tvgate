import { describe, it, expect } from "vitest";
import { parseM3U8 } from "./m3u8";
import { pickVariant } from "../hls/hls-source";
import { HlsSource } from "../hls/hls-source";

describe("parseM3U8", () => {
  it("解析 multivariant 播放列表（variant + rendition）", () => {
    const text = [
      "#EXTM3U",
      '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud1",NAME="AAC",DEFAULT=YES,AUTOSELECT=YES,URI="audio/aac.m3u8",LANGUAGE="zh"',
      '#EXT-X-STREAM-INF:BANDWIDTH=1280000,AVERAGE-BANDWIDTH=1200000,CODECS="avc1.4d401f,mp4a.40.2",RESOLUTION=1280x720,FRAME-RATE=25.000,VIDEO-RANGE=SDR,AUDIO="aud1"',
      "video/720.m3u8",
      '#EXT-X-STREAM-INF:BANDWIDTH=2560000,CODECS="avc1.64001f,mp4a.40.2",RESOLUTION=1920x1080',
      "video/1080.m3u8",
    ].join("\n");
    const p = parseM3U8("https://cdn.example.com/hls/index.m3u8", text);
    expect(p.isMaster).toBe(true);
    expect(p.variants).toHaveLength(2);
    expect(p.variants[0].bandwidth).toBe(1280000);
    expect(p.variants[0].averageBandwidth).toBe(1200000);
    expect(p.variants[0].codecs).toEqual(["avc1.4d401f", "mp4a.40.2"]);
    expect(p.variants[0].resolution).toEqual({ width: 1280, height: 720 });
    expect(p.variants[0].frameRate).toBe(25);
    expect(p.variants[0].audioGroup).toBe("aud1");
    // URI 相对 base 解析
    expect(p.variants[0].uri).toBe("https://cdn.example.com/hls/video/720.m3u8");
    expect(p.renditions).toHaveLength(1);
    expect(p.renditions[0].groupId).toBe("aud1");
    expect(p.renditions[0].isDefault).toBe(true);
  });

  it("解析 media 播放列表（分段序列）", () => {
    const text = [
      "#EXTM3U",
      "#EXT-X-TARGETDURATION:6",
      "#EXT-X-MEDIA-SEQUENCE:10",
      "#EXT-X-PLAYLIST-TYPE:VOD",
      "#EXTINF:6.006,",
      "seg-10.ts",
      "#EXTINF:6.006,",
      "seg-11.ts",
      "#EXT-X-ENDLIST",
    ].join("\n");
    const p = parseM3U8("https://cdn.example.com/hls/video/", text);
    expect(p.isMaster).toBe(false);
    expect(p.targetDuration).toBe(6);
    expect(p.mediaSequence).toBe(10);
    expect(p.endlist).toBe(true);
    expect(p.playlistType).toBe("VOD");
    expect(p.segments).toHaveLength(2);
    expect(p.segments[0].uri).toBe("https://cdn.example.com/hls/video/seg-10.ts");
    expect(p.segments[0].duration).toBeCloseTo(6.006, 3);
  });

  it("解析 BYTERANGE / DISCONTINUITY / MAP", () => {
    const text = [
      "#EXTM3U",
      "#EXT-X-TARGETDURATION:4",
      "#EXT-X-MAP:URI=\"init.mp4\",BYTERANGE=\"1000@0\"",
      "#EXTINF:4,",
      "#EXT-X-BYTERANGE:2000@1000",
      "seg.ts",
      "#EXT-X-DISCONTINUITY",
      "#EXTINF:4,",
      "seg2.ts",
    ].join("\n");
    const p = parseM3U8("https://x/hls/", text);
    expect(p.segments).toHaveLength(2);
    expect(p.segments[0].map?.uri).toBe("https://x/hls/init.mp4");
    expect(p.segments[0].map?.byteRange).toEqual({ offset: 0, length: 1000 });
    expect(p.segments[0].byteRange).toEqual({ offset: 1000, length: 2000 });
    expect(p.segments[1].discontinuity).toBe(true);
  });
});

describe("pickVariant", () => {
  const variants = [
    { uri: "a.m3u8", bandwidth: 500000, codecs: ["avc1.4d401f"] },
    { uri: "b.m3u8", bandwidth: 2000000, codecs: ["hvc1.1.6.L93.B0"] },
    { uri: "c.m3u8", bandwidth: 3000000, codecs: ["avc1.64001f"] },
  ];
  it("无上限时选带宽最高", () => {
    expect(pickVariant(variants)?.uri).toBe("c.m3u8");
  });
  it("有上限时选 ≤ 上限的最高者", () => {
    expect(pickVariant(variants, 1000000)?.uri).toBe("a.m3u8");
  });
  it("优先可解 codec", () => {
    const withWeird = [
      { uri: "x.m3u8", bandwidth: 9999999, codecs: ["vp09.00.10.08"] },
      { uri: "y.m3u8", bandwidth: 100000, codecs: ["avc1.4d401f"] },
    ];
    expect(pickVariant(withWeird)?.uri).toBe("y.m3u8");
  });
});

describe("HlsSource", () => {
  it("master → variant → 顺序给出分段，VOD 结束返回 null", async () => {
    const textFor = (url: string): string => {
      if (url.endsWith("index.m3u8")) {
        return ["#EXTM3U", '#EXT-X-STREAM-INF:BANDWIDTH=1500000,CODECS="avc1.4d401f,mp4a.40.2"', "video.m3u8"].join("\n");
      }
      return [
        "#EXTM3U",
        "#EXT-X-TARGETDURATION:6",
        "#EXT-X-MEDIA-SEQUENCE:1",
        "#EXTINF:6,",
        "s1.ts",
        "#EXTINF:6,",
        "s2.ts",
        "#EXT-X-ENDLIST",
      ].join("\n");
    };
    let info: unknown;
    const source = new HlsSource("https://x/hls/index.m3u8", { onInfo: (i) => (info = i) }, { fetcher: (url) => Promise.resolve(textFor(url)) });
    const got = await source.load();
    expect(got?.live).toBe(false);
    expect(got?.totalDuration).toBeCloseTo(12, 3);
    expect(got?.bandwidth).toBe(1500000);
    expect(info).toMatchObject({ live: false, segmentCount: 2 });
    expect(await source.next()).toBe("https://x/hls/s1.ts");
    expect(await source.next()).toBe("https://x/hls/s2.ts");
    expect(await source.next()).toBeNull();
  });

  it("直播：从距 live edge 3 段处起播并跳过已消费序号", async () => {
    let seq = 0;
    const textFor = (): string => {
      const lines = ["#EXTM3U", "#EXT-X-TARGETDURATION:4", "#EXT-X-MEDIA-SEQUENCE:" + seq];
      for (let i = seq; i < seq + 6; i++) lines.push("#EXTINF:4,", "seg" + i + ".ts");
      return lines.join("\n");
    };
    const source = new HlsSource("https://x/hls/live.m3u8", {}, { fetcher: () => Promise.resolve(textFor()) });
    const got = await source.load();
    expect(got?.live).toBe(true);
    expect(got?.segmentCount).toBe(6);
    const first = await source.next();
    // live edge = seq+5；起播从 seq+3 开始
    expect(first).toBe("https://x/hls/seg" + (seq + 3) + ".ts");
    const all: (string | null)[] = [];
    for (let i = 0; i < 4; i++) all.push(await source.next());
    expect(all).toEqual([
      "https://x/hls/seg" + (seq + 4) + ".ts",
      "https://x/hls/seg" + (seq + 5) + ".ts",
      null, // 尚无新段
      null,
    ]);
    // 播放列表更新后，next() 应拿到新段
    seq += 6;
    expect(await source.next()).toBe("https://x/hls/seg6.ts");
  });
});
