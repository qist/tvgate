# Makefile for Go Project
# 使用方法:
#   make                # 默认编译所有平台（版本号来自 config/version）
#   make VERSION=v1.2.3 # 手动指定版本号
#   make clean          # 清理 build 目录

MODULE  := github.com/qist/tvgate
OUT_DIR := build

# 如果没有指定 VERSION，就从 config/version 文件读取
VERSION ?= $(shell cat config/version 2>/dev/null || echo latest)

LDFLAGS := -s -w  -extldflags '-static' -X '$(MODULE)/config.Version=$(VERSION)'
GCFLAGS := -trimpath
ASMFLAGS := -trimpath

# 目标列表
TARGETS := \
	$(OUT_DIR)/TVGate-linux-amd64 \
	$(OUT_DIR)/TVGate-linux-arm64 \
	$(OUT_DIR)/TVGate-linux-armv7 \
	$(OUT_DIR)/TVGate-linux-386 \
	$(OUT_DIR)/TVGate-linux-ppc64 \
	$(OUT_DIR)/TVGate-linux-ppc64le \
	$(OUT_DIR)/TVGate-linux-s390x \
	$(OUT_DIR)/TVGate-windows-amd64.exe \
	$(OUT_DIR)/TVGate-windows-arm64.exe \
	$(OUT_DIR)/TVGate-windows-386.exe \
	$(OUT_DIR)/TVGate-darwin-amd64 \
	$(OUT_DIR)/TVGate-darwin-arm64 \
	$(OUT_DIR)/TVGate-android-arm64 

all: $(TARGETS)
	@echo "全部编译完成，版本号: $(VERSION)，文件在 $(OUT_DIR)/"

# Linux amd64
$(OUT_DIR)/TVGate-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .

# Linux arm64
$(OUT_DIR)/TVGate-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .

# Linux armv7
$(OUT_DIR)/TVGate-linux-armv7:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .

# Linux 386
$(OUT_DIR)/TVGate-linux-386:
	CGO_ENABLED=0 GOOS=linux GOARCH=386 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .

# Linux ppc64
$(OUT_DIR)/TVGate-linux-ppc64:
	CGO_ENABLED=0 GOOS=linux GOARCH=ppc64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .

# Linux ppc64le
$(OUT_DIR)/TVGate-linux-ppc64le:
	CGO_ENABLED=0 GOOS=linux GOARCH=ppc64le go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .

# Linux s390x
$(OUT_DIR)/TVGate-linux-s390x:
	CGO_ENABLED=0 GOOS=linux GOARCH=s390x go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .

# Windows amd64
$(OUT_DIR)/TVGate-windows-amd64.exe:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .

# Windows arm64
$(OUT_DIR)/TVGate-windows-arm64.exe:
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .


# Windows 386
$(OUT_DIR)/TVGate-windows-386.exe:
	CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .

# macOS amd64
$(OUT_DIR)/TVGate-darwin-amd64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .

# macOS arm64
$(OUT_DIR)/TVGate-darwin-arm64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .

# Android arm64
$(OUT_DIR)/TVGate-android-arm64:
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -gcflags="$(GCFLAGS)"  -asmflags="$(ASMFLAGS)" -o $@ .

clean:
	rm -rf $(OUT_DIR)/TVGate-*-*
