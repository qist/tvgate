# Makefile for Go Project
# 使用方法:
#   make                       # 默认编译所有平台（版本号来自 config/version）
#   make linux-64              # 只编译某个平台，例如 linux-64
#   make linux-arm64-v8a       # 只编译 linux-arm64-v8a
#   make windows-64            # 只编译 windows-64
#   make android-arm64-v8a     # 只编译 android-arm64-v8a
#   make list                  # 列出所有可用的编译目标
#   make VERSION=v1.2.3        # 手动指定版本号
#   make clean                 # 清理 build 目录

MODULE  := github.com/qist/tvgate
OUT_DIR := build

# 如果没有指定 VERSION，就从 config/version 文件读取
VERSION ?= $(shell cat config/version 2>/dev/null || echo latest)

LDFLAGS := -s -w -extldflags '-static' -X '$(MODULE)/config.Version=$(VERSION)'
GCFLAGS := -trimpath
ASMFLAGS := -trimpath
BUILD    = CGO_ENABLED=0 GOOS=$(1) GOARCH=$(2) $(if $(3),GOARM=$(3) )go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# ==================== Linux ====================

linux-64: $(OUT_DIR)/TVGate-linux-64
$(OUT_DIR)/TVGate-linux-64:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,amd64)

linux-arm64-v8a: $(OUT_DIR)/TVGate-linux-arm64-v8a
$(OUT_DIR)/TVGate-linux-arm64-v8a:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,arm64)

linux-arm32-v7a: $(OUT_DIR)/TVGate-linux-arm32-v7a
$(OUT_DIR)/TVGate-linux-arm32-v7a:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,arm,7)

linux-arm32-v6: $(OUT_DIR)/TVGate-linux-arm32-v6
$(OUT_DIR)/TVGate-linux-arm32-v6:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,arm,6)

linux-arm32-v5: $(OUT_DIR)/TVGate-linux-arm32-v5
$(OUT_DIR)/TVGate-linux-arm32-v5:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,arm,5)

linux-32: $(OUT_DIR)/TVGate-linux-32
$(OUT_DIR)/TVGate-linux-32:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,386)

linux-loong64: $(OUT_DIR)/TVGate-linux-loong64
$(OUT_DIR)/TVGate-linux-loong64:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,loong64)

linux-mips32: $(OUT_DIR)/TVGate-linux-mips32
$(OUT_DIR)/TVGate-linux-mips32:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,mips)

linux-mips32le: $(OUT_DIR)/TVGate-linux-mips32le
$(OUT_DIR)/TVGate-linux-mips32le:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,mipsle)

linux-mips64: $(OUT_DIR)/TVGate-linux-mips64
$(OUT_DIR)/TVGate-linux-mips64:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,mips64)

linux-mips64le: $(OUT_DIR)/TVGate-linux-mips64le
$(OUT_DIR)/TVGate-linux-mips64le:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,mips64le)

linux-ppc64: $(OUT_DIR)/TVGate-linux-ppc64
$(OUT_DIR)/TVGate-linux-ppc64:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,ppc64)

linux-ppc64le: $(OUT_DIR)/TVGate-linux-ppc64le
$(OUT_DIR)/TVGate-linux-ppc64le:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,ppc64le)

linux-riscv64: $(OUT_DIR)/TVGate-linux-riscv64
$(OUT_DIR)/TVGate-linux-riscv64:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,riscv64)

linux-s390x: $(OUT_DIR)/TVGate-linux-s390x
$(OUT_DIR)/TVGate-linux-s390x:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,s390x)

# ==================== Windows ====================

windows-64: $(OUT_DIR)/TVGate-windows-64.exe
$(OUT_DIR)/TVGate-windows-64.exe:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,windows,amd64)

windows-32: $(OUT_DIR)/TVGate-windows-32.exe
$(OUT_DIR)/TVGate-windows-32.exe:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,windows,386)

windows-arm64-v8a: $(OUT_DIR)/TVGate-windows-arm64-v8a.exe
$(OUT_DIR)/TVGate-windows-arm64-v8a.exe:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,windows,arm64)

# ==================== macOS ====================

macos-64: $(OUT_DIR)/TVGate-macos-64
$(OUT_DIR)/TVGate-macos-64:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,darwin,amd64)

macos-arm64-v8a: $(OUT_DIR)/TVGate-macos-arm64-v8a
$(OUT_DIR)/TVGate-macos-arm64-v8a:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,darwin,arm64)

# ==================== Android ====================

android-arm64-v8a: $(OUT_DIR)/TVGate-android-arm64-v8a
$(OUT_DIR)/TVGate-android-arm64-v8a:
	@mkdir -p $(OUT_DIR)
	$(call BUILD,android,arm64)

# ==================== 汇总 ====================

ALL_TARGETS := \
	linux-64 linux-arm64-v8a linux-arm32-v7a linux-arm32-v6 linux-arm32-v5 \
	linux-32 linux-loong64 linux-mips32 linux-mips32le linux-mips64 \
	linux-mips64le linux-ppc64 linux-ppc64le linux-riscv64 linux-s390x \
	windows-64 windows-32 windows-arm64-v8a \
	macos-64 macos-arm64-v8a \
	android-arm64-v8a

all: $(OUT_DIR)/TVGate-linux-64 \
	$(OUT_DIR)/TVGate-linux-arm64-v8a \
	$(OUT_DIR)/TVGate-linux-arm32-v7a \
	$(OUT_DIR)/TVGate-linux-arm32-v6 \
	$(OUT_DIR)/TVGate-linux-arm32-v5 \
	$(OUT_DIR)/TVGate-linux-32 \
	$(OUT_DIR)/TVGate-linux-loong64 \
	$(OUT_DIR)/TVGate-linux-mips32 \
	$(OUT_DIR)/TVGate-linux-mips32le \
	$(OUT_DIR)/TVGate-linux-mips64 \
	$(OUT_DIR)/TVGate-linux-mips64le \
	$(OUT_DIR)/TVGate-linux-ppc64 \
	$(OUT_DIR)/TVGate-linux-ppc64le \
	$(OUT_DIR)/TVGate-linux-riscv64 \
	$(OUT_DIR)/TVGate-linux-s390x \
	$(OUT_DIR)/TVGate-windows-64.exe \
	$(OUT_DIR)/TVGate-windows-32.exe \
	$(OUT_DIR)/TVGate-windows-arm64-v8a.exe \
	$(OUT_DIR)/TVGate-macos-64 \
	$(OUT_DIR)/TVGate-macos-arm64-v8a \
	$(OUT_DIR)/TVGate-android-arm64-v8a
	@echo "全部编译完成，版本号: $(VERSION)，文件在 $(OUT_DIR)/"

# 列出所有可用目标
list:
	@echo "可用编译目标:"
	@echo "  make all                 # 编译全部平台"
	@for t in $(ALL_TARGETS); do echo "  make $$t"; done
	@echo "  make clean               # 清理 build 目录"
	@echo "  make list                # 列出所有可用目标"

clean:
	rm -rf $(OUT_DIR)/TVGate-*

.PHONY: all clean list $(ALL_TARGETS)
