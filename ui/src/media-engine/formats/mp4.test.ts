import { describe, it, expect } from "vitest";
import {
  iterateTopLevelBoxes,
  findBox,
  collectBoxes,
  parseMdhd,
  parseTfdt,
  parseTrackCodec,
  readUint32,
} from "./mp4-box";
import { generateInitSegment, generateMediaSegment } from "./mp4-generator";
import { buildAvcC } from "./avc";
import { buildEsds, buildAudioSpecificConfig } from "./aac";

function makeVideoInit(): Uint8Array {
  const sps = new Uint8Array([0x67, 0x64, 0x00, 0x1e, 0xac, 0xd9]);
  const pps = new Uint8Array([0x68, 0xce, 0x3c, 0x80]);
  return generateInitSegment([
    {
      id: 1,
      kind: "video",
      codec: "avc1",
      timescale: 90000,
      width: 1280,
      height: 720,
      codecPrivate: buildAvcC([sps], [pps]),
    },
  ]);
}

function makeAvInit(): Uint8Array {
  const sps = new Uint8Array([0x67, 0x64, 0x00, 0x1e, 0xac, 0xd9]);
  const pps = new Uint8Array([0x68, 0xce, 0x3c, 0x80]);
  return generateInitSegment([
    {
      id: 1,
      kind: "video",
      codec: "avc1",
      timescale: 90000,
      width: 640,
      height: 480,
      codecPrivate: buildAvcC([sps], [pps]),
    },
    {
      id: 2,
      kind: "audio",
      codec: "mp4a.40.2",
      timescale: 48000,
      channels: 2,
      sampleRate: 48000,
      codecPrivate: buildEsds(buildAudioSpecificConfig(3, 2)),
    },
  ]);
}

describe("generateInitSegment 结构", () => {
  it("ftyp + moov 顶层级", () => {
    const init = makeVideoInit();
    const boxes = iterateTopLevelBoxes(init);
    expect(boxes.map((b) => b.type)).toEqual(["ftyp", "moov"]);
    // moov 大端长度与缓冲一致
    expect(readUint32(init, 0)).toBe(boxes[0].size);
  });

  it("含 mvhd/trak/mvex，mdhd timescale 正确", () => {
    const init = makeAvInit();
    const moov = findBox(init, ["moov"])!;
    expect(findBox(moov.data, ["mvhd"])).not.toBeNull();
    expect(findBox(moov.data, ["mvex", "trex"])).not.toBeNull();
    const traks = collectBoxes(moov.data, "trak");
    expect(traks).toHaveLength(2);
  });

  it("trun 声明 sample-size 且各样本 size 之和等于 mdat 净载荷", () => {
    const samples = [
      { duration: 3600, data: new Uint8Array([1, 2, 3]), isKeyframe: true, ctsOffset: 0 },
      { duration: 3600, data: new Uint8Array([4, 5]), isKeyframe: false, ctsOffset: 3600 },
    ];
    const seg = generateMediaSegment([{ trackId: 1, baseMediaDecodeTime: 0, samples }]);

    const moof = findBox(seg, ["moof"])!;
    const trun = findBox(moof.data, ["traf", "trun"])!;
    const d = trun.data;

    // version=1（cts 有符号）
    expect(d[0]).toBe(1);
    const flags = readUint32(d, 0) & 0x00ffffff;
    // data-offset | duration | size | flags | cts 五个标志位必须齐全
    expect(flags & 0x000001).toBe(0x000001);
    expect(flags & 0x000100).toBe(0x000100);
    expect(flags & 0x000200).toBe(0x000200); // sample-size-present：缺此位 SourceBuffer 无法切分 mdat
    expect(flags & 0x000400).toBe(0x000400);
    expect(flags & 0x000800).toBe(0x000800);

    const sampleCount = readUint32(d, 4);
    expect(sampleCount).toBe(2);

    // 每样本 16 字节：duration(4) / size(4) / flags(4) / cts(4)
    let sumSize = 0;
    for (let i = 0; i < sampleCount; i++) sumSize += readUint32(d, 12 + 16 * i + 4);

    const mdat = findBox(seg, ["mdat"])!;
    expect(sumSize).toBe(mdat.data.length);
  });

  it("parseTrackCodec 还原 avc1 / mp4a codec", () => {
    const init = makeAvInit();
    const moov = findBox(init, ["moov"])!;
    const traks = collectBoxes(moov.data, "trak");
    const video = traks.find((t) => parseTrackCodec(t.data).handlerType === "avc1")!;
    const audio = traks.find((t) => parseTrackCodec(t.data).handlerType === "mp4a")!;
    const vc = parseTrackCodec(video.data);
    const ac = parseTrackCodec(audio.data);
    expect(vc.codec).toMatch(/^avc1\.[0-9a-f]{6}$/);
    // Chrome 路径下 esds 的 ASC 声明 HE-AAC(AOT=5) 信令（扩展段强制回 LC），故为 40.5
    expect(ac.codec).toBe("mp4a.40.5");
  });

  it("mdhd timescale = 90000 / 48000", () => {
    const init = makeAvInit();
    const traks = collectBoxes(findBox(init, ["moov"])!.data, "trak");
    const scales: number[] = [];
    for (const t of traks) {
      const mdhd = findBox(t.data, ["mdia", "mdhd"])!;
      scales.push(parseMdhd(mdhd.data).timescale);
    }
    expect(scales.sort((a, b) => a - b)).toEqual([48000, 90000]);
  });
});

describe("generateMediaSegment 结构", () => {
  it("moof(mfhd/traf/tfhd/tfdt/trun) + mdat", () => {
    const samples = [
      { duration: 3000, data: new Uint8Array([1, 2, 3, 4]), isKeyframe: true, ctsOffset: 0 },
      { duration: 3000, data: new Uint8Array([5, 6, 7]), isKeyframe: false, ctsOffset: 3000 },
    ];
    const seg = generateMediaSegment([{ trackId: 1, baseMediaDecodeTime: 0, samples }]);
    const top = iterateTopLevelBoxes(seg);
    expect(top.map((b) => b.type)).toEqual(["moof", "mdat"]);
    const moof = top[0].data;
    expect(findBox(moof, ["mfhd"])).not.toBeNull();
    const traf = findBox(moof, ["traf"])!;
    expect(findBox(traf.data, ["tfhd"])).not.toBeNull();
    expect(findBox(traf.data, ["trun"])).not.toBeNull();
    // tfdt 记录 baseMediaDecodeTime
    expect(parseTfdt(findBox(traf.data, ["tfdt"])!.data)).toBe(0);
    // mdat 载荷 = 两个样本数据
    expect(top[1].data.length).toBe(7);
  });

  it("tfdt 取首个样本 dts（4500）", () => {
    const seg = generateMediaSegment([
      { trackId: 1, baseMediaDecodeTime: 4500, samples: [{ duration: 3000, data: new Uint8Array(1), isKeyframe: true, ctsOffset: 0 }] },
    ]);
    const moof = iterateTopLevelBoxes(seg)[0].data;
    const traf = findBox(moof, ["traf"])!;
    expect(parseTfdt(findBox(traf.data, ["tfdt"])!.data)).toBe(4500);
  });
});
