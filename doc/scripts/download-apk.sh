#!/bin/bash
# ============================================================
# 下载 tvgate-android 最新 tag 的 APK 并打包 zip
#
# 用法:
#   ./doc/scripts/download-apk.sh              # 下载最新 tag 的 APK
#   ./doc/scripts/download-apk.sh v3.0.5       # 下载指定 tag 的 APK
#
# 依赖: curl, jq, zip
# 环境变量: GITHUB_TOKEN (可选，提供后限流 5000 次/小时)
# ============================================================
set -uo pipefail

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

# 构建 curl 认证参数
CURL_AUTH=()
if [ -n "${GITHUB_TOKEN:-}" ]; then
    CURL_AUTH=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
    echo "认证: 使用 GITHUB_TOKEN (限流 5000 次/小时)"
else
    echo "认证: 匿名 (限流 60 次/小时)"
fi

# 如果没有指定版本号，获取最新 tag
if [ -z "$VERSION" ]; then
    echo "正在获取 ${REPO} 最新 tag..."
    VERSION=$(curl -sL "${CURL_AUTH[@]}" "https://api.github.com/repos/${REPO}/tags" | jq -r '.[0].name')
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

# 获取 release 中的 APK asset（单次 API 调用）
echo "正在获取 release assets..."
RELEASE_JSON=$(curl -sL "${CURL_AUTH[@]}" "https://api.github.com/repos/${REPO}/releases/tags/${VERSION}" 2>/dev/null)

if ! echo "$RELEASE_JSON" | jq -e '.assets' &>/dev/null; then
    RATE_REMAINING=$(curl -sLI "${CURL_AUTH[@]}" "https://api.github.com/repos/${REPO}/releases/tags/${VERSION}" 2>/dev/null | grep -i "x-ratelimit-remaining" | tr -d '\r' | awk '{print $2}')
    RATE_RESET=$(curl -sLI "${CURL_AUTH[@]}" "https://api.github.com/repos/${REPO}/releases/tags/${VERSION}" 2>/dev/null | grep -i "x-ratelimit-reset" | tr -d '\r' | awk '{print $2}')
    if [ -n "${RATE_RESET:-}" ] && [ "${RATE_RESET}" != "0" ]; then
        RESET_STR=$(date -d @"${RATE_RESET}" '+%Y-%m-%d %H:%M:%S' 2>/dev/null || echo "${RATE_RESET}")
        echo "⚠ 获取 release 失败! GitHub API 限流: 剩余 ${RATE_REMAINING:-0}, 重置 ${RESET_STR}"
        echo ""
        echo "提示: 设置 GITHUB_TOKEN 环境变量可提高到 5000 次/小时:"
        echo "  export GITHUB_TOKEN=ghp_xxxxxxxxxxxx"
    else
        echo "错误: 无法获取 release ${VERSION} 的 assets"
    fi
    rm -rf "${TMP_DIR}"
    trap - EXIT
    exit 1
fi

# 保存到临时文件供后续解析
echo "$RELEASE_JSON" > "${TMP_DIR}/.apk_release_${VERSION_NUM}.json"

# 查找所有 APK 文件
APK_ENTRIES=$(jq -r '.assets[] | select(.name | endswith(".apk")) | "\(.name)\t\(.browser_download_url)"' "${TMP_DIR}/.apk_release_${VERSION_NUM}.json" 2>/dev/null)

if [ -z "$APK_ENTRIES" ]; then
    echo "错误: 未找到 APK 文件"
    rm -rf "${TMP_DIR}"
    trap - EXIT
    exit 1
fi

APK_FILES=()
while IFS=$'\t' read -r name url; do
    [ -n "$name" ] && APK_FILES+=("${name}|${url}")
done <<< "$APK_ENTRIES"

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

trap - EXIT

# 打包 zip
ZIP_NAME="tvgate-android-${VERSION_NUM}.zip"
ZIP_PATH="${OUT_DIR}/${ZIP_NAME}"
echo "正在打包 ${ZIP_NAME}..."
rm -f "${ZIP_PATH}"
cd "${TMP_DIR}"
zip -j "${ZIP_PATH}" "${APK_NAMES[@]}"
cd - >/dev/null

rm -rf "${TMP_DIR}"

echo "完成: ${OUT_DIR}/${ZIP_NAME}"
