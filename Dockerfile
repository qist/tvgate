# ========================================================
# TVGate 纯 Go 构建（含自研 PHP runtime）
# 支持平台: Linux ✅ / Windows ✅ / macOS ✅ / Android ✅
# 特性: 无 CGO ✅ / 无 FrankenPHP ✅ / 无外部 .so/.dll ✅
#       ★ 单一静态二进制：PHP 解释器(phpgo)静态编入，运行时无外部依赖
#       PHP 脚本从磁盘读取，默认目录 /www（部署时把 PHP 代码放该路径）。
# ========================================================
# Stage: ui —— React 管理后台（Vite 构建，产物给 go:embed）
# 固定 BUILDPLATFORM：node:20-alpine 无 riscv64 等目标平台 manifest，
# 前端产物与目标架构无关，只需在构建机平台构建一次再 COPY 到各目标平台。
# ========================================================
FROM --platform=$BUILDPLATFORM node:20-alpine AS ui
WORKDIR /ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# ========================================================
# Stage: build —— 纯 Go 编译，无需 CGO
# ========================================================
FROM --platform=$BUILDPLATFORM golang:alpine AS build
WORKDIR /app

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=latest

ENV CGO_ENABLED=0

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 用前端真实构建产物覆盖占位 dist（go:embed 编入单二进制）
# 注意: vite outDir 为 ui/../web/dist，在 ui 阶段（WORKDIR /ui）产物位于 /web/dist
COPY --from=ui /web/dist /app/web/dist

# 针对 ARM 处理 GOARM
RUN if [ "$TARGETARCH" = "arm" ]; then \
        GOARM="${TARGETVARIANT#v}" ; \
        echo "Building for $TARGETOS/$TARGETARCH with GOARM=$GOARM" ; \
        GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=$GOARM go build -ldflags="-s -w -X 'github.com/qist/tvgate/config.Version=${VERSION}'" -o build/TVGate main.go ; \
    else \
        echo "Building for $TARGETOS/$TARGETARCH" ; \
        GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w -X 'github.com/qist/tvgate/config.Version=${VERSION}'" -o build/TVGate main.go ; \
    fi

# ========================================================
# Stage: final image
# ========================================================
FROM alpine:latest
WORKDIR /app
ENV TZ=Asia/Shanghai

# 安装必要依赖
RUN apk add --no-cache ca-certificates tzdata bash fail2ban

# 复制可执行文件
COPY --from=build /app/build/TVGate /app/TVGate

# 复制配置文件
# COPY --from=build /app/doc/config.yaml /etc/tvgate/config.yaml

# 配置 fail2ban
RUN rm -f /etc/fail2ban/jail.d/alpine-ssh.conf \
  && cp /etc/fail2ban/jail.conf /etc/fail2ban/jail.local \
  && sed -i "s/^\[ssh\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/^\[sshd\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/#allowipv6 = auto/allowipv6 = auto/g" /etc/fail2ban/fail2ban.conf

RUN chmod +x /app/TVGate

# 服务监听端口（默认配置 server.port: 8888；启用 HTTP/3 时同端口还需映射 UDP）
EXPOSE 8888

CMD [ "./TVGate", "-config=/etc/tvgate/config.yaml" ]
