#!/bin/bash
# ============================================================
# 根据 Makefile 平台名下载 tvgate 对应 release 包并用 7z 压缩
#
# 用法:
#   ./doc/scripts/download-release.sh                    # 下载全部平台
#   ./doc/scripts/download-release.sh linux-64           # 只下载 linux-64
#   ./doc/scripts/download-release.sh linux-arm64-v8a    # 只下载 linux-arm64-v8a
#   ./doc/scripts/download-release.sh v3.0.5 linux-64    # 指定版本 + 平台
#
# 压缩包命名: TVGate-{版本号}.7z（如 TVGate-3.0.5.7z）
# 多个平台会打包到同一个 7z 中
#
# 依赖: curl, jq, 7z (p7zip-full)
# ============================================================
set -euo pipefail

REPO="qist/tvgate"
OUT_DIR="download"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# 解析参数
VERSION=""
PLATFORMS=()

for arg in "$@"; do
    if [[ "$arg" == v* ]]; then
        VERSION="$arg"
    else
        PLATFORMS+=("$arg")
    fi
done

# 如果没有指定版本号，从 config/version 读取
if [ -z "$VERSION" ]; then
    VERSION=$(cat "${ROOT_DIR}/config/version" 2>/dev/null || echo "")
    if [ -z "$VERSION" ]; then
        echo "错误: 无法从 config/version 读取版本号"
        exit 1
    fi
fi

# 去掉 v 前缀（7z 文件名用纯数字）
VERSION_NUM="${VERSION#v}"

# 全部平台列表（与 Makefile 一致）
ALL_PLATFORMS=(
    linux-64
    linux-arm64-v8a
    linux-arm32-v7a
    linux-arm32-v6
    linux-arm32-v5
    linux-32
    linux-loong64
    linux-mips32
    linux-mips32le
    linux-mips64
    linux-mips64le
    linux-ppc64
    linux-ppc64le
    linux-riscv64
    linux-s390x
    windows-64
    windows-32
    windows-arm64-v8a
    macos-64
    macos-arm64-v8a
    android-arm64-v8a
)

# 如果没有指定平台，下载全部
if [ ${#PLATFORMS[@]} -eq 0 ]; then
    PLATFORMS=("${ALL_PLATFORMS[@]}")
fi

echo "版本: ${VERSION}"
echo "平台: ${PLATFORMS[*]}"
echo ""

mkdir -p "${OUT_DIR}"
TMP_DIR="${OUT_DIR}/tmp_${VERSION_NUM}"
mkdir -p "${TMP_DIR}"

# 下载每个平台的 release zip
SUCCESS_COUNT=0
FAIL_COUNT=0

for PLATFORM in "${PLATFORMS[@]}"; do
    ASSET_NAME="TVGate-${PLATFORM}.zip"
    ASSET_URL=$(curl -sL "https://api.github.com/repos/${REPO}/releases/tags/${VERSION}" \
        | jq -r ".assets[] | select(.name == \"${ASSET_NAME}\") | .browser_download_url")

    if [ -z "$ASSET_URL" ] || [ "$ASSET_URL" = "null" ]; then
        echo "  跳过: ${PLATFORM} (未找到 ${ASSET_NAME})"
        ((FAIL_COUNT++))
        continue
    fi

    echo "  下载: ${ASSET_NAME}"
    curl -L -s -o "${TMP_DIR}/${ASSET_NAME}" "$ASSET_URL"

    if [ $? -eq 0 ]; then
        echo "  完成: ${ASSET_NAME}"
        ((SUCCESS_COUNT++))
    else
        echo "  失败: ${ASSET_NAME}"
        ((FAIL_COUNT++))
    fi
done

echo ""
echo "下载完成: 成功 ${SUCCESS_COUNT}, 失败/跳过 ${FAIL_COUNT}"

if [ $SUCCESS_COUNT -eq 0 ]; then
    echo "没有成功下载任何文件，退出"
    rm -rf "${TMP_DIR}"
    exit 1
fi

# 7z 压缩
ARCHIVE_NAME="TVGate-${VERSION_NUM}.7z"
ARCHIVE_PATH="${OUT_DIR}/${ARCHIVE_NAME}"

echo ""
echo "正在压缩 ${ARCHIVE_NAME} ..."

# 如果已存在则先删除
rm -f "${ARCHIVE_PATH}"

cd "${TMP_DIR}"
7z a -t7z -mx=9 -mmt=on "../${ARCHIVE_NAME}" ./*
cd "${ROOT_DIR}"

# 清理临时目录
rm -rf "${TMP_DIR}"

ARCHIVE_SIZE=$(du -h "${ARCHIVE_PATH}" | cut -f1)
echo ""
echo "完成: ${OUT_DIR}/${ARCHIVE_NAME} (${ARCHIVE_SIZE})"
