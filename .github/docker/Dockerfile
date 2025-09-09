# ========================================================
# Stage: build
# ========================================================
FROM --platform=$BUILDPLATFORM golang:alpine AS build
WORKDIR /app

# 复制源码
COPY . .

# 纯 Go 静态编译
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=${TARGETARCH:-amd64}

# 可通过 build-arg 注入版本号
ARG VERSION=latest

RUN go build -ldflags="-s -w -X 'github.com/qist/tvgate/config.Version=${VERSION}'" -o build/TVGate main.go

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
COPY --from=build /app/doc/config.yaml /etc/tvgate/config.yaml

# Configure fail2ban
RUN rm -f /etc/fail2ban/jail.d/alpine-ssh.conf \
  && cp /etc/fail2ban/jail.conf /etc/fail2ban/jail.local \
  && sed -i "s/^\[ssh\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/^\[sshd\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/#allowipv6 = auto/allowipv6 = auto/g" /etc/fail2ban/fail2ban.conf

RUN chmod +x /app/TVGate

EXPOSE 8888
VOLUME [ "/etc/tvgate" ]

# 默认启动 TVGate
CMD [ "./TVGate", "-config=/etc/tvgate/config.yaml" ]
