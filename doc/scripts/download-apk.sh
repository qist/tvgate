#!/bin/bash
# ============================================================
# 下载 tvgate-android 最新 tag 的 APK 并打包 zip
#
# 用法:
#   ./doc/scripts/download-apk.sh              # 下载最新 tag 的 APK
#   ./doc/scripts/download-apk.sh v3.0.5       # 下载指定 tag 的 APK
#
# 依赖: curl, jq, zip
# ============================================================
set -euo pipefail

REPO="qist/tvgate-android"
OUT_DIR="download"
VERSION="${1:-}"

# 如果没有指定版本号，获取最新 tag
if [ -z "$VERSION" ]; then
    echo "正在获取 ${REPO} 最新 tag..."
    VERSION=$(curl -sL "https://api.github.com/repos/${REPO}/tags" | jq -r '.[0].name')
    if [ -z "$VERSION" ] || [ "$VERSION" = "null" ]; then
        echo "错误: 获取最新 tag 失败"
        exit 1
    fi
fi

echo "目标版本: ${VERSION}"

# 去掉版本号前缀 v（文件名用纯数字）
VERSION_NUM="${VERSION#v}"

mkdir -p "${OUT_DIR}"

# 获取 release 中的 APK asset
echo "正在获取 release assets..."
ASSETS=$(curl -sL "https://api.github.com/repos/${REPO}/releases/tags/${VERSION}" | jq -r '.assets[] | "\(.name)\t\(.browser_download_url)"')

if [ -z "$ASSETS" ]; then
    echo "错误: 未找到 ${VERSION} 的 release assets"
    exit 1
fi

APK_FILE=""
APK_URL=""

# 查找 APK 文件
while IFS=$'\t' read -r name url; do
    if [[ "$name" == *.apk ]]; then
        APK_FILE="$name"
        APK_URL="$url"
        break
    fi
done <<< "$ASSETS"

if [ -z "$APK_FILE" ]; then
    echo "错误: 未找到 APK 文件"
    exit 1
fi

echo "找到 APK: ${APK_FILE}"

# 下载 APK
echo "正在下载..."
curl -L -o "${OUT_DIR}/${APK_FILE}" "$APK_URL"

# 打包 zip
ZIP_NAME="tvgate-android-${VERSION_NUM}.zip"
echo "正在打包 ${ZIP_NAME}..."
cd "${OUT_DIR}"
zip -j "$ZIP_NAME" "$APK_FILE"
rm -f "$APK_FILE"
cd ..

echo "完成: ${OUT_DIR}/${ZIP_NAME}"
