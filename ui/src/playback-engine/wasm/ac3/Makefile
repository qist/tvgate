# Makefile for AC-3/E-AC-3 WASM decoder (FFmpeg libavcodec based)
#
# 前置条件：FFmpeg 裁剪静态库（仅 ac3/eac3 解码器）。构建方式：
#   git clone --depth 1 --branch n7.1.1 https://github.com/FFmpeg/FFmpeg.git /opt/ffmpeg-ac3
#   cd /opt/ffmpeg-ac3
#   emconfigure ./configure --target-os=none --arch=x86_32 --enable-cross-compile \
#     --disable-asm --disable-x86asm --disable-inline-asm --disable-programs \
#     --disable-doc --disable-debug --disable-autodetect --disable-everything \
#     --disable-pthreads --disable-runtime-cpudetect --enable-avcodec \
#     --enable-decoder=ac3 --enable-decoder=eac3 --cc=emcc --cxx=em++ \
#     --ar=emar --ranlib=emranlib --extra-cflags="-DNDEBUG -Oz" --extra-ldflags="-Oz"
#   emmake make -j4 libavcodec/libavcodec.a libavutil/libavutil.a
#
# 许可说明：FFmpeg 部分为 LGPL-2.1+（通过 --enable-gpl 未启用任何 GPL 组件），
# 以独立 .wasm 资源文件分发（等同动态链接）。修改本目录后需保留 FFmpeg
# 源码获取途径（见 LICENSE 注记 / 项目文档）。

EMCC = emcc
OUTPUT = ac3_decoder

# FFmpeg 裁剪构建的静态库路径（可用 FFMPEG_LIB_DIR 覆盖）
FFMPEG_LIB_DIR ?= /opt/ffmpeg-ac3

# -O2 preserves readable export names (-O3 would minify them).
# -o .wasm produces standalone WASM with no JS glue.
EMFLAGS = -O2 \
	-s EXPORTED_FUNCTIONS='["_malloc", "_free"]' \
	-s EXPORTED_RUNTIME_METHODS='[]' \
	-s ALLOW_MEMORY_GROWTH=1 \
	-s INITIAL_MEMORY=33554432 \
	-s STACK_SIZE=524288 \
	--no-entry

SOURCES = ac3_decoder.c wsola.c
LIBS = $(FFMPEG_LIB_DIR)/libavcodec/libavcodec.a $(FFMPEG_LIB_DIR)/libavutil/libavutil.a
CFLAGS += -I$(FFMPEG_LIB_DIR)

.PHONY: all clean

all: $(OUTPUT).wasm

$(OUTPUT).wasm: $(SOURCES) $(LIBS)
	$(EMCC) $(EMFLAGS) $(CFLAGS) $(SOURCES) $(LIBS) -o $(OUTPUT).wasm
	@echo "Built $(OUTPUT).wasm ($$(wc -c < $(OUTPUT).wasm) bytes)"

clean:
	rm -f $(OUTPUT).wasm
