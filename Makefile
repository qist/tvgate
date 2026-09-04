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

# PHP 模块：纯 Go 自研 runtime（phpgo），不依赖 CGO / FrankenPHP / 外部 PHP 库。
# 单一静态二进制，PHP 脚本从磁盘读取（默认 www，相对配置文件所在目录，见 config.PHP.DocRoot）。

# ==================== 前端（React SPA）====================
# 产物由 go:embed 编入单二进制；无 npm 环境时静默跳过（web/dist 占位保证 go build 可编译）
UI_DIR      := ui
DIST_STAMP  := web/dist/.built
# 前端源码变化时自动重建 dist（go:embed 依赖此产物，避免二进制嵌入过期前端）
UI_SRCS     := $(shell find $(UI_DIR)/src -type f 2>/dev/null)
# Go 源码变化时自动重编（所有平台二进制目标的公共依赖，避免改代码后 make 判定"无需重建"）
GO_SRCS     := $(shell find . -name '*.go' -not -path './ui/*' 2>/dev/null) go.mod go.sum config/version

.PHONY: web-ui ui-install go-only
web-ui: $(DIST_STAMP)

$(DIST_STAMP): $(UI_SRCS) $(UI_DIR)/package.json $(UI_DIR)/package-lock.json
	@command -v npm >/dev/null 2>&1 || { echo "⚠️ 未检测到 npm，跳过前端构建"; exit 0; }
	@test -d $(UI_DIR)/node_modules || (cd $(UI_DIR) && npm install)
	cd $(UI_DIR) && npm run build
	@touch $@

ui-install:
	@cd $(UI_DIR) && npm install

# 纯 Go 编译（前端为占位 dist，仅编译/升级场景使用）
go-only:
	@echo "仅编译 Go（SPA 为占位页）。正常构建请用 make all/linux-64（自动触发 make web-ui）。"

# ==================== Linux ====================

linux-64: $(DIST_STAMP) $(OUT_DIR)/TVGate-linux-64
$(OUT_DIR)/TVGate-linux-64: $(DIST_STAMP) $(GO_SRCS)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,amd64)

linux-arm64-v8a: $(OUT_DIR)/TVGate-linux-arm64-v8a
$(OUT_DIR)/TVGate-linux-arm64-v8a: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,arm64)

linux-arm32-v7a: $(OUT_DIR)/TVGate-linux-arm32-v7a
$(OUT_DIR)/TVGate-linux-arm32-v7a: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,arm,7)

linux-arm32-v6: $(OUT_DIR)/TVGate-linux-arm32-v6
$(OUT_DIR)/TVGate-linux-arm32-v6: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,arm,6)

linux-arm32-v5: $(OUT_DIR)/TVGate-linux-arm32-v5
$(OUT_DIR)/TVGate-linux-arm32-v5: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,arm,5)

linux-32: $(OUT_DIR)/TVGate-linux-32
$(OUT_DIR)/TVGate-linux-32: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,386)

linux-loong64: $(OUT_DIR)/TVGate-linux-loong64
$(OUT_DIR)/TVGate-linux-loong64: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,loong64)

linux-mips32: $(OUT_DIR)/TVGate-linux-mips32
$(OUT_DIR)/TVGate-linux-mips32: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,mips)

linux-mips32le: $(OUT_DIR)/TVGate-linux-mips32le
$(OUT_DIR)/TVGate-linux-mips32le: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,mipsle)

linux-mips64: $(OUT_DIR)/TVGate-linux-mips64
$(OUT_DIR)/TVGate-linux-mips64: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,mips64)

linux-mips64le: $(OUT_DIR)/TVGate-linux-mips64le
$(OUT_DIR)/TVGate-linux-mips64le: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,mips64le)

linux-ppc64: $(OUT_DIR)/TVGate-linux-ppc64
$(OUT_DIR)/TVGate-linux-ppc64: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,ppc64)

linux-ppc64le: $(OUT_DIR)/TVGate-linux-ppc64le
$(OUT_DIR)/TVGate-linux-ppc64le: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,ppc64le)

linux-riscv64: $(OUT_DIR)/TVGate-linux-riscv64
$(OUT_DIR)/TVGate-linux-riscv64: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,riscv64)

linux-s390x: $(OUT_DIR)/TVGate-linux-s390x
$(OUT_DIR)/TVGate-linux-s390x: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,linux,s390x)

# ==================== Windows ====================

windows-64: $(OUT_DIR)/TVGate-windows-64.exe
$(OUT_DIR)/TVGate-windows-64.exe: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,windows,amd64)

windows-32: $(OUT_DIR)/TVGate-windows-32.exe
$(OUT_DIR)/TVGate-windows-32.exe: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,windows,386)

windows-arm64-v8a: $(OUT_DIR)/TVGate-windows-arm64-v8a.exe
$(OUT_DIR)/TVGate-windows-arm64-v8a.exe: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,windows,arm64)

# ==================== macOS ====================

macos-64: $(OUT_DIR)/TVGate-macos-64
$(OUT_DIR)/TVGate-macos-64: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,darwin,amd64)

macos-arm64-v8a: $(OUT_DIR)/TVGate-macos-arm64-v8a
$(OUT_DIR)/TVGate-macos-arm64-v8a: $(GO_SRCS) $(DIST_STAMP)
	@mkdir -p $(OUT_DIR)
	$(call BUILD,darwin,arm64)

# ==================== Android ====================

android-arm64-v8a: $(OUT_DIR)/TVGate-android-arm64-v8a
$(OUT_DIR)/TVGate-android-arm64-v8a: $(GO_SRCS) $(DIST_STAMP)
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

all: web-ui $(OUT_DIR)/TVGate-linux-64 \
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
