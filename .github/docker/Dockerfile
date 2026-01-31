# ========================================================
# Stage: build
# ========================================================
FROM golang:alpine AS build
WORKDIR /app

# 复制源码
COPY . .

# 接收 buildx 注入的参数
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=latest

# 禁用 CGO，纯 Go 静态编译
ENV CGO_ENABLED=0

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

CMD [ "./TVGate", "-config=/etc/tvgate/config.yaml" ]
