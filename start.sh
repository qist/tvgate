#!/bin/bash
#
# tvgate 启动/停止/重启脚本
# 用法:
#   ./start.sh          启动（后台）
#   ./start.sh stop     停止
#   ./start.sh restart  重启
#   ./start.sh status   查看状态
#   ./start.sh log      查看日志（tail -f）
#

APP_NAME="tvgate"
APP_DIR="/opt/tvgate"
APP_BIN="${APP_DIR}/tvgate"
CONFIG="${APP_DIR}/build/config.yaml"
PID_FILE="${APP_DIR}/${APP_NAME}.pid"
LOG_FILE="/tmp/tvgate.log"

# 确保在项目目录
cd "${APP_DIR}" || exit 1

# 检查二进制是否存在
if [ ! -f "${APP_BIN}" ]; then
    echo "❌ 二进制 ${APP_BIN} 不存在，先编译: cd ${APP_DIR} && go build -o tvgate ."
    exit 1
fi

start() {
    # 先杀旧进程
    if [ -f "${PID_FILE}" ]; then
        local old_pid
        old_pid=$(cat "${PID_FILE}")
        if kill -0 "${old_pid}" 2>/dev/null; then
            echo "⚠️  已有进程运行 (PID ${old_pid})，先停止..."
            kill "${old_pid}" 2>/dev/null
            sleep 1
        fi
        rm -f "${PID_FILE}"
    fi

    echo "🚀 启动 ${APP_NAME}..."
    nohup "${APP_BIN}" -config "${CONFIG}" > "${LOG_FILE}" 2>&1 &
    local pid=$!
    echo "${pid}" > "${PID_FILE}"

    # 等待启动
    sleep 2
    if kill -0 "${pid}" 2>/dev/null; then
        echo "✅ ${APP_NAME} 已启动 (PID ${pid})"
        echo "   配置: ${CONFIG}"
        echo "   日志: ${LOG_FILE}"
        echo "   PID:  ${PID_FILE}"
    else
        echo "❌ ${APP_NAME} 启动失败，查看日志: ${LOG_FILE}"
        tail -20 "${LOG_FILE}"
        rm -f "${PID_FILE}"
        exit 1
    fi
}

stop() {
    local pid=""
    if [ -f "${PID_FILE}" ]; then
        pid=$(cat "${PID_FILE}")
    else
        pid=$(pgrep -f "${APP_BIN}" | head -1)
    fi

    if [ -z "${pid}" ]; then
        echo "ℹ️  ${APP_NAME} 未在运行"
        rm -f "${PID_FILE}"
        return
    fi

    echo "🛑 停止 ${APP_NAME} (PID ${pid})..."
    kill "${pid}" 2>/dev/null
    sleep 1

    if kill -0 "${pid}" 2>/dev/null; then
        echo "⚠️  进程未响应，强制杀..."
        kill -9 "${pid}" 2>/dev/null
        sleep 1
    fi

    rm -f "${PID_FILE}"
    echo "✅ 已停止"
}

restart() {
    stop
    sleep 1
    start
}

status() {
    local pid=""
    if [ -f "${PID_FILE}" ]; then
        pid=$(cat "${PID_FILE}")
    fi

    if [ -n "${pid}" ] && kill -0 "${pid}" 2>/dev/null; then
        echo "✅ ${APP_NAME} 运行中 (PID ${pid})"
        echo "   日志: ${LOG_FILE}"
    else
        # 尝试 pgrep 兜底
        pid=$(pgrep -f "${APP_BIN}" | head -1)
        if [ -n "${pid}" ]; then
            echo "✅ ${APP_NAME} 运行中 (PID ${pid}) [PID 文件丢失]"
        else
            echo "❌ ${APP_NAME} 未运行"
        fi
    fi
}

log() {
    if [ ! -f "${LOG_FILE}" ]; then
        echo "❌ 日志文件不存在: ${LOG_FILE}"
        exit 1
    fi
    echo "📋 实时日志 (Ctrl+C 退出):"
    tail -f "${LOG_FILE}"
}

case "${1:-start}" in
    start)   start   ;;
    stop)    stop    ;;
    restart) restart ;;
    status)  status  ;;
    log)     log     ;;
    *)
        echo "用法: $0 {start|stop|restart|status|log}"
        exit 1
        ;;
esac
