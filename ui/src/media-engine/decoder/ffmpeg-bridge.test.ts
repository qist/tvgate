import { describe, it, expect } from "vitest";
import { detectAbiStyle, createWasmImports } from "./ffmpeg-bridge";

describe("ffmpeg-bridge ABI 探测", () => {
  it("统一封装(me_decoder_*)识别为 unified", () => {
    expect(detectAbiStyle({ me_decoder_create: () => 0, malloc: () => 0 })).toBe("unified");
  });
  it("历史 ac3_decoder_* 识别为 ac3", () => {
    expect(detectAbiStyle({ ac3_decoder_create: () => 0 })).toBe("ac3");
  });
  it("空导出按 ac3 处理（缺省）", () => {
    expect(detectAbiStyle({})).toBe("ac3");
  });
});

describe("ffmpeg-bridge import 契约", () => {
  it("提供 env.emscripten_notify_memory_growth 与 wasi 打桩", () => {
    const imports = createWasmImports() as {
      env: { emscripten_notify_memory_growth?: () => void };
      wasi_snapshot_preview1: Record<string, () => number>;
    };
    expect(typeof imports.env.emscripten_notify_memory_growth).toBe("function");
    expect(imports.wasi_snapshot_preview1.clock_time_get()).toBe(0);
    // 其它 wasi 调用返回 ENOSYS(52)，解码路径不触发
    expect(imports.wasi_snapshot_preview1.fd_write()).toBe(52);
  });
});
