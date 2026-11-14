#!/usr/bin/env bash
#
# TVGate Android 交叉编译脚本（独立于主 Makefile，不参与 make all）
#
# Android 的 32 位 ARM 目标必须外部链接生成 PIE（android/arm requires
# external (cgo) linking），因此不能复用主 Makefile 里 CGO_ENABLED=0 +
# -extldflags '-static' 的通用构建规则，只能走 NDK clang + CGO 外部链接。
# 做法与 tvgate-android 仓库的 build-android.sh 保持一致。
#
# 用法:
#   ./scripts/build-android.sh              # 编译全部 ABI
#   ./scripts/build-android.sh arm64-v8a    # 只编译 64 位 ARM
#   ./scripts/build-android.sh arm32-v7a    # 只编译 32 位 ARM（armeabi-v7a）
#
# 环境变量:
#   ANDROID_NDK_HOME / ANDROID_NDK_ROOT / ANDROID_HOME / ANDROID_SDK_ROOT
#   ANDROID_API   最低 API 级别，默认 21
#   OUT_DIR       输出目录，默认 <repo>/build
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MODULE="github.com/qist/tvgate"
OUT_DIR="${OUT_DIR:-$ROOT/build}"
ANDROID_API="${ANDROID_API:-21}"
VERSION="${VERSION:-$(cat "$ROOT/config/version" 2>/dev/null || echo latest)}"

# ---- 定位 NDK ----
NDK="${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-}}"
if [ -z "$NDK" ]; then
  for base in "${ANDROID_HOME:-}" "${ANDROID_SDK_ROOT:-}" "$HOME/Android/Sdk" "/opt/android-sdk"; do
    [ -n "$base" ] || continue
    if compgen -G "$base/ndk/*" >/dev/null 2>&1; then
      NDK="$(ls -d "$base"/ndk/* | sort -V | tail -1)"
      break
    fi
  done
fi

if [ -z "$NDK" ] || [ ! -d "$NDK" ]; then
  echo "ERROR: 找不到 Android NDK，请设置 ANDROID_NDK_HOME（或 ANDROID_HOME / ANDROID_SDK_ROOT）" >&2
  echo "       示例: ANDROID_NDK_HOME=\$HOME/Android/Sdk/ndk/25.2.9519653 ./scripts/build-android.sh" >&2
  exit 1
fi

# 主机预编译工具链目录（linux-x86_64 / darwin-x86_64）
CLANG_DIR="$(ls -d "$NDK"/toolchains/llvm/prebuilt/*/bin 2>/dev/null | head -1)"
if [ -z "$CLANG_DIR" ] || [ ! -d "$CLANG_DIR" ]; then
  echo "ERROR: NDK 缺少 toolchains/llvm/prebuilt/*/bin: $NDK" >&2
  exit 1
fi

# ---- 确保前端产物存在（go:embed 依赖 web/dist） ----
DIST_STAMP="$ROOT/web/dist/.built"
if [ ! -f "$DIST_STAMP" ]; then
  if command -v npm >/dev/null 2>&1; then
    echo "==> 构建 Web 前端 (npm) ..."
    [ -d "$ROOT/ui/node_modules" ] || (cd "$ROOT/ui" && npm install)
    (cd "$ROOT/ui" && npm run build)
    touch "$DIST_STAMP"
  else
    echo "⚠️  web/dist 未构建且未检测到 npm，沿用已有占位产物"
  fi
fi

build_one() {
  local abi="$1" goarch goarm cc name out
  case "$abi" in
    arm64-v8a|arm64)
      goarch=arm64; goarm="";   cc="aarch64-linux-android${ANDROID_API}-clang";              name=arm64-v8a ;;
    arm32-v7a|armeabi-v7a|arm)
      goarch=arm;   goarm=7;    cc="armv7a-linux-androideabi${ANDROID_API}-clang";           name=arm32-v7a ;;
    *)
      echo "ERROR: 不支持的 ABI: $abi（可选 arm64-v8a / arm32-v7a）" >&2; exit 1 ;;
  esac

  out="$OUT_DIR/TVGate-android-$name"
  mkdir -p "$OUT_DIR"

  local envs=(GOOS=android GOARCH="$goarch" CGO_ENABLED=1 CC="$CLANG_DIR/$cc")
  [ -n "$goarm" ] && envs+=(GOARM="$goarm")

  echo "==> 编译 TVGate android-$name (GOARCH=$goarch${goarm:+ GOARM=$goarm} CC=$cc)"
  # -gcflags=all=-l 关闭内联以缩减体积（与 tvgate-android 构建参数一致）
  ( cd "$ROOT" && env "${envs[@]}" \
      go build -trimpath -gcflags=all=-l \
      -ldflags="-s -w -X '$MODULE/config.Version=$VERSION'" \
      -o "$out" . )
  echo "    -> $out ($(du -h "$out" | cut -f1))"
}

ABI="${1:-all}"
case "$ABI" in
  all) build_one arm64-v8a; build_one arm32-v7a ;;
  *)   build_one "$ABI" ;;
esac

echo "完成。版本号: $VERSION，产物在 $OUT_DIR/"
