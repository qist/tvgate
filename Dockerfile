# ========================================================
# TVGate 纯 Go 构建（含自研 PHP runtime）
# 支持平台: Linux ✅ / Windows ✅ / macOS ✅ / Android ✅
# 特性: 无 CGO ✅ / 无 FrankenPHP ✅ / 无外部 .so/.dll ✅
#       ★ 单一静态二进制：PHP 解释器(phpgo)静态编入，运行时无外部依赖
#       PHP 脚本从磁盘读取，默认目录 /www（部署时把 PHP 代码放该路径）。
# ========================================================
# Stage: build —— 纯 Go 编译，无需 CGO
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
FROM debian:bookworm-slim
WORKDIR /app
ENV TZ=Asia/Shanghai

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates tzdata bash fail2ban \
    && rm -rf /var/lib/apt/lists/*

# 复制可执行文件（单一二进制，含纯 Go PHP runtime）
COPY --from=build /app/build/TVGate /app/TVGate

# 配置 fail2ban
RUN rm -f /etc/fail2ban/jail.d/alpine-ssh.conf \
  && cp /etc/fail2ban/jail.conf /etc/fail2ban/jail.local \
  && sed -i "s/^\[ssh\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/^\[sshd\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/#allowipv6 = auto/allowipv6 = auto/g" /etc/fail2ban/fail2ban.conf

RUN chmod +x /app/TVGate

CMD [ "./TVGate", "-config=/etc/tvgate/config.yaml" ]

