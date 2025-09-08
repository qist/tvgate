# ========================================================
# Stage: build
# ========================================================
FROM --platform=$BUILDPLATFORM golang:alpine AS build
WORKDIR /app
ARG TARGETARCH

RUN apk --no-cache add build-base gcc

COPY . .

ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-D_LARGEFILE64_SOURCE"

# 编译 TVGate
RUN go build -o build/TVGate main.go

# ========================================================
# Stage: final image
# ========================================================
FROM alpine
WORKDIR /app
ENV TZ=Asia/Shanghai

RUN apk add --no-cache ca-certificates tzdata fail2ban bash

# 复制 TVGate 可执行文件
COPY --from=build /app/build/ /app/


# 复制 config.yaml
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

# 直接运行 TVGate 并指定配置文件
CMD [ "./TVGate", "-config=/etc/tvgate/config.yaml" ]
