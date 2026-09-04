#!/bin/sh
# ============================================================
# TVGate 梅林固件（Asuswrt-Merlin）一键安装脚本
#
# 在梅林路由器 SSH 中执行（需已安装 Entware，/opt 可写）:
#   sh merlin-install.sh                     # 官方 GitHub 下载最新版
#   sh merlin-install.sh https://hk.gh-proxy.com/   # 走加速前缀下载
#   sh merlin-install.sh <加速前缀> v3.1.0    # 指定版本
#
# 功能: 检测架构 → 下载对应 release → 安装到 /opt/TVGate →
#       写入 Entware 自启脚本 → 启动（config.yaml 首次启动自动生成）
# ============================================================
set -u

REPO="qist/tvgate"
APP_DIR="/opt/TVGate"
INIT_FILE="/opt/etc/init.d/S99tvgate"
PROXY="${1:-}"
VERSION="${2:-}"

# ---------- 工具函数 ----------
log()  { echo "[TVGate] $*"; }
fail() { echo "[TVGate][错误] $*" >&2; exit 1; }

# ---------- 前置检查 ----------
[ -d /opt/etc/init.d ] || fail "未检测到 Entware（/opt/etc/init.d 不存在）。先安装 Entware，见 doc/MERLIN.md 第 2 步"
command -v curl >/dev/null 2>&1 || fail "缺少 curl，执行: opkg install curl"

# ---------- 架构检测 ----------
case "$(uname -m)" in
    aarch64)          ARCH="linux-arm64-v8a" ;;
    armv7l|armv8l)    ARCH="linux-arm32-v7a" ;;
    armv5*|mips*)     fail "MIPS 平台请手动下载 TVGate-linux-mips32.zip 试试（未广泛测试）" ;;
    *)                fail "未知架构: $(uname -m)，请到 GitHub Releases 手动下载对应包" ;;
esac
log "检测到架构: $(uname -m) → $ARCH"

# ---------- 解析下载地址 ----------
API="https://api.github.com/repos/$REPO/releases/latest"
[ -n "$VERSION" ] && API="https://api.github.com/repos/$REPO/releases/tags/$VERSION"
[ -n "$PROXY" ] && API="${PROXY}${API}"
[ -n "$PROXY" ] && DL_PREFIX="$PROXY" || DL_PREFIX=""

log "查询版本信息: $API"
JSON=$(curl -sL --connect-timeout 15 --max-time 60 "$API") || fail "无法访问 GitHub API（可加加速前缀重试）"
URL=$(echo "$JSON" | grep -o "https://[^\"]*$ARCH[^\"]*" | head -1)
[ -n "$URL" ] || fail "未找到 $ARCH 资产，检查网络或版本号"
[ -z "$DL_PREFIX" ] || case "$URL" in
    http*) URL="${DL_PREFIX}${URL}" ;;
esac
log "下载: $URL"

# ---------- 下载与解压 ----------
TMP="/tmp/tvgate-install"
rm -rf "$TMP" && mkdir -p "$TMP"
curl -L --connect-timeout 15 -o "$TMP/pkg.zip" "$URL" || fail "下载失败"
[ "$(wc -c < "$TMP/pkg.zip")" -gt 1000000 ] || fail "下载的文件过小，可能是加速前缀返回了错误页"

unzip -o -q "$TMP/pkg.zip" -d "$TMP" 2>/dev/null || fail "解压失败，执行: opkg install unzip"
BIN=$(find "$TMP" -type f -name 'TVGate-*' ! -name '*.yaml' ! -name '*.md' | head -1)
[ -n "$BIN" ] || BIN=$(find "$TMP" -type f ! -name '*.yaml' ! -name '*.md' ! -name '*.txt' | head -1)
[ -n "$BIN" ] || fail "压缩包中未找到二进制"

# ---------- 安装 ----------
mkdir -p "$APP_DIR/log"
install -m 0755 "$BIN" "$APP_DIR/tvgate"
log "已安装到 $APP_DIR/tvgate ($(uname -m))"
rm -rf "$TMP"

# ---------- 配置说明 ----------
if [ -f "$APP_DIR/config.yaml" ]; then
    log "保留已有配置 $APP_DIR/config.yaml"
else
    log "未检测到 config.yaml，首次启动时 TVGate 会自动生成默认配置（后台 http://<路由器IP>:8888/web/ 修改）"
fi

# ---------- Entware 自启脚本 ----------
cat > "$INIT_FILE" <<'EOF'
#!/bin/sh
# TVGate Entware 服务脚本（梅林开机经 rc.unslung 自动调用 start）
APP_DIR="/opt/TVGate"
BIN="$APP_DIR/tvgate"
CONF="$APP_DIR/config.yaml"
PIDF="$APP_DIR/tvgate.pid"
LOGF="$APP_DIR/log/stdout.log"

is_running() {
    [ -f "$PIDF" ] && kill -0 "$(cat "$PIDF")" 2>/dev/null
}

start() {
    if is_running; then
        echo "TVGate 已在运行 (PID $(cat "$PIDF"))"
        return 0
    fi
    if [ ! -x "$BIN" ]; then
        echo "TVGate 二进制不存在: $BIN"
        return 1
    fi
    echo "启动 TVGate..."
    (cd "$APP_DIR" && "$BIN" -config "$CONF" >> "$LOGF" 2>&1 &
     echo $! > "$PIDF")
    sleep 1
    is_running && echo "TVGate 已启动 (PID $(cat "$PIDF"))" || echo "TVGate 启动失败，查看 $LOGF"
}

stop() {
    if ! is_running; then
        echo "TVGate 未在运行"
        rm -f "$PIDF"
        return 0
    fi
    echo "停止 TVGate..."
    kill "$(cat "$PIDF")" 2>/dev/null
    i=0
    while is_running && [ "$i" -lt 10 ]; do sleep 1; i=$((i+1)); done
    is_running && kill -9 "$(cat "$PIDF")" 2>/dev/null
    rm -f "$PIDF"
    echo "TVGate 已停止"
}

status() {
    if is_running; then
        echo "TVGate 运行中 (PID $(cat "$PIDF"))"
    else
        echo "TVGate 未运行"
    fi
}

case "$1" in
    start)   start ;;
    stop)    stop ;;
    restart) stop; sleep 1; start ;;
    status)  status ;;
    *)       echo "用法: $0 {start|stop|restart|status}" ;;
esac
EOF
chmod +x "$INIT_FILE"
log "已写入自启脚本 $INIT_FILE"

# ---------- jffs 脚本确认（开机自启链路） ----------
for F in /jffs/scripts/services-start /jffs/scripts/unmount; do
    [ -f "$F" ] || { mkdir -p /jffs/scripts; printf '#!/bin/sh\n' > "$F"; chmod +x "$F"; }
    chmod +x "$F" 2>/dev/null
done
grep -q "rc.unslung start" /jffs/scripts/services-start 2>/dev/null || {
    printf '/opt/etc/init.d/rc.unslung start\n' >> /jffs/scripts/services-start
    log "已补写 services-start 的 rc.unslung 启动行（Entware 开机自启）"
}
grep -q "S99tvgate" /jffs/scripts/unmount 2>/dev/null || {
    printf '/opt/etc/init.d/S99tvgate stop\n' >> /jffs/scripts/unmount
    log "已补写 unmount 停止行（U 盘弹出前安全停止）"
}

# ---------- 启动 ----------
"$INIT_FILE" start
echo
log "安装完成！管理后台: http://<路由器IP>:8888/web/（默认 admin/admin，请尽快改密码）"
log "常用命令: $INIT_FILE {start|stop|restart|status}"
