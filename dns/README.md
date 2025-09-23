# DNS 解析模块

该模块提供 DNS 解析功能，支持配置自定义 DNS 服务器，并在配置的 DNS 服务器不可用时自动回退到系统 DNS。

## 功能特性

1. 支持配置自定义 DNS 服务器
2. 自动检测配置的 DNS 服务器可用性
3. 当配置的 DNS 服务器不可用时，自动回退到系统 DNS
4. 支持超时控制
5. 线程安全的实现
6. 支持多种DNS协议：
   - 传统DNS (UDP/TCP:53)
   - DNS-over-HTTPS (DoH)
   - DNS-over-TLS (DoT)
   - DNSCrypt
   - DNS-over-QUIC (DoQ)

## 配置

在 `config.yaml` 中添加 DNS 配置：

```yaml
dns:
  servers:
    - "8.8.8.8"                                    # 传统DNS
    - "sdns://AQcAAAAAAAAADjIwOC42Ny4yMjAuMjIw"   # DNSCrypt格式
    - "sdns://AgcAAAAAAAAADjIwOC42Ny4yMjAuMjIwILxRg8tR2Wo7dV5npt77i_W5N8LwpHiYyrWG6H54W6xw" # DoH格式
  timeout: 5s
  max_conns: 10
```

### 配置说明

- `servers`: DNS 服务器列表，按顺序尝试解析
  - 可以是传统DNS服务器IP地址
  - 可以是DNS stamp格式的服务器地址，支持DNSCrypt、DoH、DoT等协议
- `timeout`: DNS 查询超时时间，默认为 5 秒
- `max_conns`: 最大连接数，默认为 10

## 使用方法

### 初始化

在程序启动时初始化 DNS 解析器：

```go
err := dns.Init()
if err != nil {
    log.Printf("初始化DNS解析器失败: %v", err)
}
```

### 解析域名

```go
// 解析IP地址
ips, err := dns.LookupIP("example.com")
if err != nil {
    log.Printf("DNS解析失败: %v", err)
}

// 使用上下文的解析方法
ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
defer cancel()
addrs, err := dns.LookupIPAddr(ctx, "example.com")
if err != nil {
    log.Printf("DNS解析失败: %v", err)
}
```

## 工作原理

1. 程序启动时读取配置文件中的 DNS 配置
2. 如果配置了 DNS 服务器，则根据服务器地址格式创建相应的解析器
3. 解析域名时首先尝试使用配置的 DNS 服务器
4. 如果配置的 DNS 服务器解析失败，则自动回退到系统 DNS
5. 所有解析操作都有超时控制，避免长时间阻塞

## 支持的协议

### 传统DNS
使用标准的UDP/TCP端口53进行DNS查询。

### DNSCrypt
通过加密通道进行DNS查询，保护DNS查询内容不被窃听或篡改。

### DNS-over-HTTPS (DoH)
通过HTTPS协议进行DNS查询，避免DNS查询被中间网络设备监控。

### DNS-over-TLS (DoT)
通过TLS加密通道进行DNS查询，提供安全的DNS解析服务。

### DNS-over-QUIC (DoQ)
基于QUIC协议的DNS查询，提供更低延迟和更好性能的DNS解析。

## 故障处理

- 当配置的 DNS 服务器都不可用时，会自动回退到系统 DNS
- 所有解析操作都有超时控制，防止长时间阻塞
- 提供日志记录功能，便于问题排查