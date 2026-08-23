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

# 检查依赖
for cmd in curl jq zip; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "错误: 缺少依赖 '$cmd'，请先安装"
        case "$cmd" in
            curl) echo "  Ubuntu/Debian: apt install curl" ;;
            jq)   echo "  Ubuntu/Debian: apt install jq" ;;
            zip)  echo "  Ubuntu/Debian: apt install zip" ;;
        esac
        exit 1
    fi
done

REPO="qist/tvgate-android"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
OUT_DIR="${ROOT_DIR}/download"
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
TMP_DIR="${OUT_DIR}/tmp_android_${VERSION_NUM}"
mkdir -p "${TMP_DIR}"

# 确保退出时清理临时目录
trap 'rm -rf "${TMP_DIR}"' EXIT

# 获取 release 中的 APK asset
echo "正在获取 release assets..."
ASSETS=$(curl -sL "https://api.github.com/repos/${REPO}/releases/tags/${VERSION}" | jq -r '.assets[] | "\(.name)\t\(.browser_download_url)"')

if [ -z "$ASSETS" ]; then
    echo "错误: 未找到 ${VERSION} 的 release assets"
    exit 1
fi

# 查找所有 APK 文件
APK_FILES=()
while IFS=$'\t' read -r name url; do
    if [[ "$name" == *.apk ]]; then
        APK_FILES+=("${name}|${url}")
    fi
done <<< "$ASSETS"

if [ ${#APK_FILES[@]} -eq 0 ]; then
    echo "错误: 未找到 APK 文件"
    exit 1
fi

echo "找到 ${#APK_FILES[@]} 个 APK"

# 下载所有 APK
echo "正在下载..."
APK_NAMES=()
for entry in "${APK_FILES[@]}"; do
    APK_FILE="${entry%%|*}"
    APK_URL="${entry#*|}"
    echo "  下载: ${APK_FILE}"
    curl -L -s -o "${TMP_DIR}/${APK_FILE}" "$APK_URL"
    APK_NAMES+=("${APK_FILE}")
done

# 打包 zip
ZIP_NAME="tvgate-android-${VERSION_NUM}.zip"
ZIP_PATH="${OUT_DIR}/${ZIP_NAME}"
echo "正在打包 ${ZIP_NAME}..."
rm -f "${ZIP_PATH}"
cd "${TMP_DIR}"
zip -j "${ZIP_PATH}" "${APK_NAMES[@]}"
cd - >/dev/null

echo "完成: ${OUT_DIR}/${ZIP_NAME}"
