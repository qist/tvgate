# 在线升级的安全校验

在线升级会从 GitHub Releases（或配置的加速镜像）下载 `TVGate-<平台>-<架构>.zip`，
再解压替换当前程序。下载链路可能被 DNS 劫持、运营商缓存或加速镜像篡改，
返回 HTML 错误页或错误文件；此前会直接在解压阶段报 `zip: not a valid zip file`。

v3.4.10 起，升级包在**解压之前**完成多层校验，任何一层失败都会中止升级并保留当前版本。

## 校验层次

| 层次 | 内容 | 失败后果 |
|---|---|---|
| 下载 | HTTP 状态码必须 200；文件大小 1MB ~ 256MB；先写 `.part`，成功后才改名 | 换下一个下载源 |
| ZIP 结构 | 文件头必须是 `PK\x03\x04`；`zip.OpenReader` 必须成功 | 拒绝，提示可能被劫持 |
| 包内程序 | 必须存在 `TVGate-<平台>-<架构>`（或 `.exe`），且解压后不小于 8MB；完整读取以校验 CRC | 拒绝 |
| 发布页 SHA-256 | 读取同名 `.dgst`（**优先官方 github.com**，其次镜像）中的 `SHA2-256`，与文件实际哈希比对 | 拒绝 |
| Ed25519 签名（可选） | 构建内置公钥时，必须存在 `.zip.sig` 且对 zip 的 SHA-256 摘要验签通过 | 拒绝 |

要点：

- `.dgst` 优先从官方 HTTPS 地址获取，因此镜像替换升级包会被 SHA-256 比对发现；
- 未取到 `.dgst` 时（老版本/纯镜像环境）会跳过哈希校验，但仍做结构、程序与 CRC 校验；
- 内置公钥后为 **fail-closed**：缺少 `.sig`、签名格式错误或验签失败一律拒绝，
  防止攻击者通过镜像替换文件或剥离签名。

## 启用签名（可选）

签名默认关闭。启用后需要两个值（都不写入代码）：

1. 私钥（Secret）：`TVGATE_SIGNING_KEY`，Ed25519 32 字节种子的 hex（64 个字符）。
2. 公钥（Repository variable）：`TVGATE_UPDATE_PUBKEY`，对应公钥的 hex（64 个字符）。

生成密钥对（任选一种）：

```bash
# 用 OpenSSL 生成
openssl genpkey -algorithm ed25519 -out update-key.pem
openssl pkey -in update-key.pem -text -noout        # 私钥
openssl pkey -in update-key.pem -pubout -text -noout # 公钥

# 或直接用 Go 打印一对 hex
cat > /tmp/genkey.go <<'EOF'
package main
import ("crypto/ed25519";"crypto/rand";"encoding/hex";"fmt")
func main(){ pub, priv, _ := ed25519.GenerateKey(rand.Reader); fmt.Println("seed:", hex.EncodeToString(priv.Seed())); fmt.Println("pub: ", hex.EncodeToString(pub)) }
EOF
go run /tmp/genkey.go
```

配置仓库：

```bash
gh secret set TVGATE_SIGNING_KEY --repo qist/tvgate   # 粘贴 seed hex
gh variable set TVGATE_UPDATE_PUBKEY --repo qist/tvgate --body "<pub hex>"
```

之后：

- Release CI 会为每个平台包生成 `<包>.zip.sig` 并上传（未配置私钥时跳过）；
- 构建时把公钥注入 `updater.updatePublicKeyHex`（Makefile 用 `UPDATE_PUBKEY=`，
  CI 用 `vars.TVGATE_UPDATE_PUBKEY`）；
- 只有内置了公钥的版本才会强制验签；旧版本仍按 SHA-256 + 结构校验。

本地构建示例：

```bash
make linux-64 UPDATE_PUBKEY=<公钥 hex>
```

## 错误提示

| 提示 | 含义 |
|---|---|
| 文件头不是 ZIP 格式，请检查 DNS/代理是否把下载地址劫持到了错误页面 | 下载到 HTML/JSON 等错误页 |
| 包内缺少目标程序 TVGate-… | 下载到了其它平台包或被替换 |
| SHA-256 不匹配 | 镜像篡改/文件损坏 |
| 缺少签名文件 / 签名验证失败 | 启用了签名校验但签名缺失或不匹配 |
| 未获取到发布页校验文件，已跳过 SHA-256 校验 | 只完成了结构与程序校验 |
