# media-engine WASM 音频解码

统一软解后端：FFmpeg libavcodec（LGPL-2.1+，无 GPL 组件），一套 WASM 覆盖
**MP2 / MP3 / AC-3 / E-AC-3 / AAC**，取代历史 minimp3(MP2) + ac3-only 的分裂布局。

## 文件
- `avcodec_audio.c` —— 干净房间自写的封装（clean-room）：公开 libavcodec
  `send_packet/receive_frame` 与 `av_parser_parse2`（用各 codec 的 parser 切帧，
  无需手写帧表），内置 carry 保留跨 payload 的不完整帧。
- `wsola.c` —— WSOLA 时间拉伸（wsola_* 导出），供主线程 PCM 播放器做
  live-sync 变速（与历史 mp2/ac3 wasm 一致，编入同一模块）。
- `Makefile` —— 构建脚本（emcc → 独立 `.wasm`，无 JS 胶水）。
- 产出：`avcodec_audio.wasm`。

## 构建
按 Makefile 头部注释先裁剪构建 FFmpeg（`--enable-decoder=mp2,mp3,ac3,eac3,aac` +
对应 parser），然后：

    make FFMPEG_LIB_DIR=/opt/ffmpeg-media

> 状态：`avcodec_audio.wasm`（约 746KB）已按此配方用 Emscripten 3.1.64 在
> `/opt/ffmpeg-media`（FFmpeg n7.1.1 裁剪，`--enable-decoder=mp2,mp3,ac3,eac3,aac`
> + `--enable-parser=mpegaudio,ac3,eac3,aac`）构建完成并随仓库交付。
> 环境装配/重建见上方 FFmpeg 步骤；emcc 命令见 Makefile。
> 链接期需补 `__secs_to_zone` musl 打桩（emscripten 独立模式 libc 裁剪，见
> `avcodec_audio.c` 末尾）。
>
> **解码已用自产 fixture 验证**（`../decoder/ffmpeg-bridge.wasm.test.ts` +
> `../decoder/testdata/tone-48k-stereo.*`，48kHz 立体声双音 1s/10s）：5 codec
> 整块与 1KB/768B/333B 分块喂入均无损恢复、0 解码错误、RMS/声道过零率正确。
> 踩坑记录（回归测试锁定）：
> ① FFmpeg parser 会在返回 `consumed=0` 的同时交出其内部缓冲的完整帧（帧恰好
>    结束于本次输入起点）——封装必须在检查 `pkt_size` **之后**才因无进展 break，
>    否则隔帧丢失（AC-3 768B 分块实测丢一半）；
> ② 裁剪构建的 mp2/mp3 默认解码器为**定点版**，输出 S16P——封装须做 s16→float
>    转换，否则帧被静默丢弃（MP2 无声）；
> ③ 流末帧会被 parser 收进内部缓冲（对调用方报告已消耗），只有 `input_len=0`
>    的 EOF flush 调用能取回，且 carry=0 时也必须执行 flush。
>
> 本机重建补充：`/opt/emsdk/upstream` 的 `emscripten_config` 有损坏符号，需自备
> config（LLVM_ROOT 指 `upstream/bin`，NODE_JS 指本机 node）并
> `export EM_CONFIG=<config> PATH=/opt/emsdk/upstream/emscripten:/opt/emsdk/upstream/bin:$PATH`，
> 然后 `make FFMPEG_LIB_DIR=/opt/ffmpeg-media`。

## ABI（导出）
    me_decoder_create(codec_id)     codec_id: 0=ac3 1=eac3 2=mp2 3=mp3 4=aac
    me_decoder_destroy(ptr)
    me_decoder_reset(ptr)
    me_max_samples_per_frame()
    me_decode_payload(ptr, in, inLen, outF32, outCapFloats, info[8]) -> samples
    wsola_create(sampleRate, channels) / wsola_* (本地增强，见 wsola.c)
输出为立体声交织 float32；AC-3/E-AC-3 内部经 "downmix" 选项做 5.1→stereo。
info[6] 为本批输出中起始于本次输入之前（跨 payload 拼帧）的样本数/声道，
供上层按 PTS 精确外推（历史 ac3_decoder_* ABI 同款语义）。

TS 侧加载器：`../decoder/avcodec-audio-decoder.ts`（独立 WASM import 打桩 +
me_* ABI 驱动，同一模块同时供 Worker 解码与主线程 wsola 拉伸）。

## 许可义务
FFmpeg 为 LGPL：以独立 .wasm 资源分发（等同动态链接），分发须附 FFmpeg 源码获取途径
（见 Makefile 头部注释）。本封装仅调用其公共 API。
