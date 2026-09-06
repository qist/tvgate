# Playback Engine

This directory contains the media playback engine used by the web player. Its public entry point is `index.ts`; UI code should not import private MSE pipeline modules directly.

## Module boundaries

- `backends/`: public MSE and Native backend implementations behind the shared `PlaybackBackend` contract.
- `mse/`: private MSE playback controller, MediaSource integration, and live-sync policy.
- `timeline/`: wall-clock mapping shared by the engine and player UI.
- `worker/`: transmux worker protocol, source scheduling, and pipeline orchestration.
- `demux/`, `remux/`, `hls/`, and `io/`: transport and container processing used by the MSE backend.
- `audio/` and `decoder/`: software-decoded audio playback and decoder adapters.
- `render/`: WebGL deinterlacing and video enhancement used by the MSE backend.
- `wasm/`: bundled native sources and WebAssembly artifacts.

Dependencies should flow from `backends/` into the implementation modules. Private implementation modules must not import UI components, and code outside the engine should use `index.ts` unless it needs a documented timeline helper.

## License

This playback engine is derived from the web player front-end of [stackia/rtp2httpd](https://github.com/stackia/rtp2httpd), which is itself based on [oskar456/rtp2httpd](https://github.com/oskar456/rtp2httpd). Both upstream projects are licensed under the **GNU General Public License v2.0 (GPL-2.0)**.

Accordingly, this entire directory — including the ported code and all subsequent modifications and enhancements (live re-sync, AC-3/E-AC-3 WASM soft decoding, etc.) — is distributed under **GPL-2.0**, as contained in the [LICENSE](LICENSE) file in this directory. This GPL-2.0 grant applies to this directory only; the rest of TVGate remains under the [Mozilla Public License 2.0](../../LICENSE).

The `wasm/ac3/` build additionally links against trimmed FFmpeg libraries (LGPL-2.1+, no GPL components enabled); see the notes in `wasm/ac3/Makefile` for how to obtain the corresponding source code.
