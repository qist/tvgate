# Makefile for Go Project
# 使用方法:
#   make                # 默认编译所有平台（版本号来自 config/version）
#   make VERSION=v1.2.3 # 手动指定版本号
#   make clean          # 清理 build 目录

MODULE  := github.com/qist/tvgate
OUT_DIR := build

# 如果没有指定 VERSION，就从 config/version 文件读取
VERSION ?= $(shell cat config/version 2>/dev/null || echo latest)

LDFLAGS := -s -w -extldflags '-static' -X '$(MODULE)/config.Version=$(VERSION)'
GCFLAGS := -trimpath
ASMFLAGS := -trimpath

# 目标列表（命名规范与 Xray-core 一致）
# 64 = amd64, 32 = 386, arm64-v8a, arm32-v7a, macos = darwin
TARGETS := \
	$(OUT_DIR)/TVGate-linux-64 \
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

all: $(TARGETS)
	@echo "全部编译完成，版本号: $(VERSION)，文件在 $(OUT_DIR)/"

# ==================== Linux ====================

# Linux 64 (amd64)
$(OUT_DIR)/TVGate-linux-64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux arm64-v8a
$(OUT_DIR)/TVGate-linux-arm64-v8a:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux arm32-v7a
$(OUT_DIR)/TVGate-linux-arm32-v7a:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux arm32-v6
$(OUT_DIR)/TVGate-linux-arm32-v6:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux arm32-v5
$(OUT_DIR)/TVGate-linux-arm32-v5:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=5 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux 32 (386)
$(OUT_DIR)/TVGate-linux-32:
	CGO_ENABLED=0 GOOS=linux GOARCH=386 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux loong64
$(OUT_DIR)/TVGate-linux-loong64:
	CGO_ENABLED=0 GOOS=linux GOARCH=loong64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux mips32
$(OUT_DIR)/TVGate-linux-mips32:
	CGO_ENABLED=0 GOOS=linux GOARCH=mips go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux mips32le
$(OUT_DIR)/TVGate-linux-mips32le:
	CGO_ENABLED=0 GOOS=linux GOARCH=mipsle go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux mips64
$(OUT_DIR)/TVGate-linux-mips64:
	CGO_ENABLED=0 GOOS=linux GOARCH=mips64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux mips64le
$(OUT_DIR)/TVGate-linux-mips64le:
	CGO_ENABLED=0 GOOS=linux GOARCH=mips64le go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux ppc64
$(OUT_DIR)/TVGate-linux-ppc64:
	CGO_ENABLED=0 GOOS=linux GOARCH=ppc64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux ppc64le
$(OUT_DIR)/TVGate-linux-ppc64le:
	CGO_ENABLED=0 GOOS=linux GOARCH=ppc64le go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux riscv64
$(OUT_DIR)/TVGate-linux-riscv64:
	CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Linux s390x
$(OUT_DIR)/TVGate-linux-s390x:
	CGO_ENABLED=0 GOOS=linux GOARCH=s390x go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# ==================== Windows ====================

# Windows 64 (amd64)
$(OUT_DIR)/TVGate-windows-64.exe:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Windows 32 (386)
$(OUT_DIR)/TVGate-windows-32.exe:
	CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# Windows arm64-v8a
$(OUT_DIR)/TVGate-windows-arm64-v8a.exe:
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# ==================== macOS ====================

# macOS 64 (amd64)
$(OUT_DIR)/TVGate-macos-64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# macOS arm64-v8a
$(OUT_DIR)/TVGate-macos-arm64-v8a:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

# ==================== Android ====================

# Android arm64-v8a
$(OUT_DIR)/TVGate-android-arm64-v8a:
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)" -asmflags="$(ASMFLAGS)" -o $@ .

clean:
	rm -rf $(OUT_DIR)/TVGate-*

.PHONY: all clean
