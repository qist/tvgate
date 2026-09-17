# Makefile for unified software audio decoder (FFmpeg libavcodec based)
#
# Produces a single standalone WASM (avcodec_audio.wasm) covering
# MP2 / MP3 / AC-3 / E-AC-3 / AAC, replacing the previous minimp3 (MP2)
# and AC-3-only split. WSOLA 时间拉伸（wsola_* 导出）也编入同一模块，
# 供主线程 PCM 播放器做 live-sync 变速（与历史 mp2/ac3 wasm 一致）。
#
# 前置：FFmpeg 裁剪静态库（只含上述解码器与其 parser）。构建 FFmpeg：
#   git clone --depth 1 --branch n7.1.1 https://github.com/FFmpeg/FFmpeg.git /opt/ffmpeg-media
#   cd /opt/ffmpeg-media
#   emconfigure ./configure --target-os=none --arch=x86_32 --enable-cross-compile \
#     --disable-asm --disable-x86asm --disable-inline-asm --disable-programs \
#     --disable-doc --disable-debug --disable-autodetect --disable-everything \
#     --disable-pthreads --disable-runtime-cpudetect --enable-avcodec \
#     --enable-decoder=mp2,mp3,ac3,eac3,aac \
#     --enable-parser=mpegaudio,ac3,eac3,aac \
#     --cc=emcc --cxx=em++ --ar=emar --ranlib=emranlib \
#     --extra-cflags="-DNDEBUG -Oz" --extra-ldflags="-Oz"
#   emmake make -j4 libavcodec/libavcodec.a libavutil/libavutil.a
#
# 许可：FFmpeg 部分为 LGPL-2.1+（未启用任何 GPL 组件），以独立 .wasm 资源分发
# （等同动态链接）；分发时须保留 FFmpeg 源码获取途径（LGPL 义务）。
#
# 本机重建：/opt/emsdk/upstream 自带的 emscripten_config 有损坏符号，需自备
# config（LLVM_ROOT 指 upstream/bin，NODE_JS 指本机 node）：
#   export EM_CONFIG=/tmp/emscripten_config \
#     PATH=/opt/emsdk/upstream/emscripten:/opt/emsdk/upstream/bin:$PATH

EMCC = emcc
OUTPUT = avcodec_audio

# FFmpeg 裁剪静态库目录（可用 FFMPEG_LIB_DIR 覆盖）
FFMPEG_LIB_DIR ?= /opt/ffmpeg-media

# me_* 与 wsola_* 均由源码内 EMSCRIPTEN_KEEPALIVE 自动导出，无需显式列出。

EMFLAGS = -O2 \
	-s EXPORTED_FUNCTIONS='["_malloc", "_free"]' \
	-s EXPORTED_RUNTIME_METHODS='[]' \
	-s ALLOW_MEMORY_GROWTH=1 \
	-s INITIAL_MEMORY=33554432 \
	-s STACK_SIZE=524288 \
	--no-entry

SOURCES = avcodec_audio.c wsola.c
LIBS = $(FFMPEG_LIB_DIR)/libavcodec/libavcodec.a $(FFMPEG_LIB_DIR)/libavutil/libavutil.a
CFLAGS += -I$(FFMPEG_LIB_DIR)

.PHONY: all clean

all: $(OUTPUT).wasm

$(OUTPUT).wasm: $(SOURCES) $(LIBS)
	$(EMCC) $(EMFLAGS) $(CFLAGS) $(SOURCES) $(LIBS) -o $(OUTPUT).wasm
	@echo "Built $(OUTPUT).wasm ($$(wc -c < $(OUTPUT).wasm) bytes)"

clean:
	rm -f $(OUTPUT).wasm