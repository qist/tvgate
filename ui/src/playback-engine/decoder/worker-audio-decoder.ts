/*
 * Worker Audio Decoder
 *
 * Manages software audio decoding (MP2 / AC-3 / E-AC-3) in Web Worker
 * environment via WASM. Accepts URLs to the .wasm files (provided by
 * consumer via config).
 */

import Log from "../utils/logger";
import { type DecodedAudio, MpegAudioDecoder } from "./mpeg-audio-decoder";
import { Ac3AudioDecoder } from "./ac3-audio-decoder";

const TAG = "WorkerAudioDecoder";

export type SoftAudioCodec = "mp2" | "ac3" | "eac3";

/**
 * Audio decoder for use in Web Worker. The consumer provides the WASM URLs
 * via config — the library does NOT bundle WASM.
 */
export class WorkerAudioDecoder {
  private mpegDecoder: MpegAudioDecoder | null = null;
  private ac3Decoder: Ac3AudioDecoder | null = null;
  private wasmUrl: string;
  private codec: SoftAudioCodec;
  private lastDecodedFormat: string | null = null;

  constructor(wasmUrl: string, codec: SoftAudioCodec = "mp2") {
    this.wasmUrl = wasmUrl;
    this.codec = codec;
  }

  async initDecoder(): Promise<boolean> {
    try {
      if (this.codec === "mp2") {
        if (this.mpegDecoder?.isReady) {
          return true;
        }
        this.destroyDecoder();
        Log.i(TAG, `Initializing MP2 decoder from ${this.wasmUrl}`);
        this.mpegDecoder = new MpegAudioDecoder(this.wasmUrl);
        await this.mpegDecoder.ready;
      } else {
        if (this.ac3Decoder?.isReady) {
          return true;
        }
        this.destroyDecoder();
        Log.i(TAG, `Initializing ${this.codec.toUpperCase()} decoder from ${this.wasmUrl}`);
        // 同一个 wasm 内含 ac3/eac3 两个解码器实例，按 codec 创建
        this.ac3Decoder = new Ac3AudioDecoder(this.wasmUrl, this.codec === "eac3");
        await this.ac3Decoder.ready;
      }
      Log.i(TAG, `${this.codec.toUpperCase()} decoder initialized successfully`);
      return true;
    } catch (error) {
      Log.e(TAG, `Failed to initialize ${this.codec.toUpperCase()} decoder`, error);
      this.destroyDecoder();
      return false;
    }
  }

  /** Decode all complete frames in a PES payload (partial frames are carried over). */
  decode(data: Uint8Array): DecodedAudio | null {
    let decodedAudio: DecodedAudio | null = null;
    try {
      if (this.codec === "mp2") {
        decodedAudio = this.mpegDecoder?.decode(data) ?? null;
      } else {
        decodedAudio = this.ac3Decoder?.decode(data) ?? null;
      }
    } catch (error) {
      Log.e(TAG, `${this.codec.toUpperCase()} decode failed`, error);
      return null;
    }
    if (!decodedAudio) return null;

    const decodedFormat = `${decodedAudio.sampleRate}Hz/${decodedAudio.channels}ch`;
    if (this.lastDecodedFormat !== decodedFormat) {
      Log.i(
        TAG,
        `${this.codec.toUpperCase()} decoded format${this.lastDecodedFormat ? " changed" : " detected"}: ` +
          `${this.lastDecodedFormat ?? "none"} -> ${decodedFormat}`,
      );
      this.lastDecodedFormat = decodedFormat;
    }

    return decodedAudio;
  }

  reset(): void {
    this.mpegDecoder?.reset();
    this.ac3Decoder?.reset();
    this.lastDecodedFormat = null;
  }

  private destroyDecoder(): void {
    if (this.mpegDecoder) {
      this.mpegDecoder.destroy();
      this.mpegDecoder = null;
    }
    if (this.ac3Decoder) {
      this.ac3Decoder.destroy();
      this.ac3Decoder = null;
    }
    this.lastDecodedFormat = null;
  }

  destroy(): void {
    this.destroyDecoder();
  }
}
