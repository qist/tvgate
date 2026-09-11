/*
 * Worker Audio Decoder
 *
 * Manages software audio decoding (MP2 / AC-3 / E-AC-3) in Web Worker
 * environment via WASM. Accepts the URL to the unified wasm module
 * (avcodec_audio.wasm, FFmpeg libavcodec based — one module covers all
 * codecs), provided by consumer via config.
 */

import Log from "../utils/logger";
import { type AvcodecCodec, AvcodecAudioDecoder, type DecodedAudio } from "./avcodec-audio-decoder";

const TAG = "WorkerAudioDecoder";

export type SoftAudioCodec = "mp2" | "ac3" | "eac3";

/**
 * Audio decoder for use in Web Worker. The consumer provides the WASM URL
 * via config — the library does NOT bundle WASM.
 */
export class WorkerAudioDecoder {
  private decoder: AvcodecAudioDecoder | null = null;
  private wasmUrl: string;
  private codec: SoftAudioCodec;
  private lastDecodedFormat: string | null = null;

  constructor(wasmUrl: string, codec: SoftAudioCodec = "mp2") {
    this.wasmUrl = wasmUrl;
    this.codec = codec;
  }

  async initDecoder(): Promise<boolean> {
    try {
      if (this.decoder?.isReady) {
        return true;
      }
      this.destroyDecoder();
      Log.i(TAG, `Initializing ${this.codec.toUpperCase()} decoder from ${this.wasmUrl}`);
      // 同一个 wasm 内含全部 codec 解码器，按 codec 创建
      this.decoder = new AvcodecAudioDecoder(this.wasmUrl, this.codec as AvcodecCodec);
      await this.decoder.ready;
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
      decodedAudio = this.decoder?.decode(data) ?? null;
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
    this.decoder?.reset();
    this.lastDecodedFormat = null;
  }

  private destroyDecoder(): void {
    if (this.decoder) {
      this.decoder.destroy();
      this.decoder = null;
    }
    this.lastDecodedFormat = null;
  }

  destroy(): void {
    this.destroyDecoder();
  }
}