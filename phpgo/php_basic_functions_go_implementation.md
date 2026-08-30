# PHP 基本函数 Go 实现清单（phpgo 实际实现）

> 本文档记录 **phpgo**（集成于 TVGate 的纯 Go PHP 运行时，无 PHP-FPM / 无 CGO）当前**已实际注册/实现**的 PHP 函数，按类别组织。
>
> - **别名**：指向同名函数体的函数（如 `join` → `implode`），行为与别名目标完全一致。
> - **no-op / stub**：仅返回固定值、不真正生效的占位实现（不影响脚本流程，但功能不完整）。
> - 覆盖范围以 `phpgo` 目录下各 `fn_*.go` / `funcs.go` 中的 `builtins[...]` 注册为准。

## 目录

1. [字符串](#1-字符串)
2. [数组](#2-数组)
3. [数学](#3-数学)
4. [随机数](#4-随机数)
5. [日期时间](#5-日期时间)
6. [文件系统](#6-文件系统)
7. [加密 / 哈希 / 进制](#7-加密--哈希--进制)
8. [URL / 网络](#8-url--网络)
9. [JSON / 序列化](#9-json--序列化)
10. [cURL](#10-curl)
11. [正则表达式](#11-正则表达式)
12. [变量 / 类型 / 函数调用](#12-变量--类型--函数调用)
13. [输出控制 / 语言结构 / 杂项](#13-输出控制--语言结构--杂项)
14. [PHP 超全局变量](#14-php-超全局变量)
15. [兼容性注意事项](#15-兼容性注意事项)

---

## 1. 字符串

| 函数 | 说明 |
|---|---|
| `strlen` `substr` `strpos` | 基础字符串操作 |
| `strtolower` `strtoupper` | 大小写转换 |
| `mb_strtolower` `mb_strtoupper` `mb_strlen` `mb_substr` `mb_strpos` `mb_stripos` | **别名**（分别指向 `strtolower`/`strtoupper`/`strlen`/`substr`/`strpos`/`stripos`） |
| `str_ireplace` `str_replace` `strtr` | 替换 |
| `str_contains` `str_starts_with` `str_ends_with` | 包含判断 |
| `strpos` `stripos` `strrpos` `strstr` `strchr` | 查找（`strchr` 为 **别名** → `strstr`） |
| `strtok` | 按分隔符切词 |
| `trim` `rtrim` `ltrim` | 去除空白 |
| `str_pad` `str_repeat` `str_split` `chunk_split` `substr_count` `substr_replace` | 填充 / 拆分 |
| `ucfirst` `lcfirst` `ucwords` | 单词首字母 |
| `strrev` `str_shuffle` `str_rot13` `strip_tags` `str_word_count` | 其它字符串工具 |
| `nl2br` `addslashes` `stripslashes` `wordwrap` | 文本格式 |
| `sprintf` `printf` `vprintf` `vsprintf` | 格式化输出 |
| `strcmp` `strcasecmp` `strncmp` `strncasecmp` `strnatcmp` `strnatcasecmp` | 比较 |
| `implode` `join` | 数组合并字符串（`join` 为 **别名**） |
| `explode` | 字符串拆分数组 |
| `htmlspecialchars` `htmlspecialchars_decode` `htmlentities` `html_entity_decode` `utf8_decode` | HTML / 编码 |
| `print_r` `var_dump` | 变量调试输出 |
| `urlencode` | URL 编码 |
| `strval` `floatval` `doubleval` `intval` | 类型转换（`doubleval` 为 **别名** → `floatval`） |

## 2. 数组

| 函数 | 说明 |
|---|---|
| `array_keys` `array_values` `array_key_exists` `count` | 基础数组操作 |
| `in_array` `array_search` `array_key_first` `array_key_last` | 查找 |
| `array_map` `array_filter` `array_reduce` `array_walk` | 遍历处理 |
| `array_merge` `array_merge_recursive` | 合并 |
| `array_diff` `array_intersect` `array_diff_key` `array_intersect_key` | 差集 / 交集 |
| `array_slice` `array_splice` `array_chunk` `array_pad` `array_fill` `array_fill_keys` `array_combine` `array_column` | 切片 / 填充 / 重组 |
| `array_push` `array_pop` `array_shift` `array_unshift` | 栈 / 队列 |
| `array_flip` `array_reverse` `array_unique` `array_rand` `shuffle` `array_count_values` `array_sum` `array_product` `range` `compact` | 其它 |
| `sort` `rsort` `asort` `arsort` `ksort` `krsort` `usort` `uasort` `uksort` | 排序 |
| `end` `current` `next` `prev` `reset` | 数组指针 |

## 3. 数学

| 函数 | 说明 |
|---|---|
| `abs` `ceil` `floor` `round` `min` `max` `number_format` `intdiv` `pow` `sqrt` `pi` | 基础数学 |
| `exp` `log` `log10` `log2` `log1p` `fmod` `deg2rad` `rad2deg` | 指数 / 对数 |
| `sin` `cos` `tan` `asin` `acos` `atan` `atan2` `sinh` `cosh` `tanh` `hypot` | 三角函数 |
| `decoct` `octdec` `is_finite` `is_infinite` `is_nan` | 进制 / 数值判断 |
| `srand` `mt_srand` | 显式设置随机种子（影响 `rand`/`mt_rand`；seed=0 时自动按时间播种） |

## 4. 随机数

| 函数 | 说明 |
|---|---|
| `rand` `random_int` `mt_getrandmax` `uniqid` `random_bytes` | 随机数 |
| `mt_rand` | **别名** → `rand` |
| `usleep` | 真实睡眠（微秒，Go `time.Sleep`） |
| `sleep` | 真实睡眠（秒，Go `time.Sleep`，成功返回 0） |

## 5. 日期时间

| 函数 | 说明 |
|---|---|
| `date` `gmdate` `time` `microtime` `strtotime` | 基础日期时间 |
| `date_default_timezone_set` `date_default_timezone_get` | 时区（按请求隔离） |
| `mktime` `gmmktime` `checkdate` `getdate` `gettimeofday` | 构造 / 解析 |

## 6. 文件系统

| 函数 | 说明 |
|---|---|
| `file_get_contents` `file` `file_put_contents` `readfile` | 文件内容读写 |
| `file_exists` `is_file` `is_dir` `is_writable` `filesize` `filemtime` `fileatime` `filectime` `mime_content_type` | 文件信息 |
| `fopen` `fclose` `fread` `fwrite` `fgets` `feof` `fseek` `ftell` `rewind` `ftruncate` `fflush` `flock` | 文件句柄操作 |
| `mkdir` `rmdir` `unlink` `rename` `copy` `touch` | 文件 / 目录操作 |
| `scandir` `glob` | 目录列举 |
| `dirname` `basename` `realpath` `pathinfo` | 路径处理 |
| `stream_context_create` `stream_get_contents` `fsockopen` | 流 / 网络套接字 |

## 7. 加密 / 哈希 / 进制

| 函数 | 说明 |
|---|---|
| `md5` `sha1` `hash` `hash_hmac` `crc32` | 哈希 |
| `base64_encode` `base64_decode` | Base64（`funcs.go` 与 `fn_crypto.go` 各注册一次，行为一致） |
| `openssl_encrypt` `openssl_decrypt` | 对称加解密（AES / 3DES 等，由 fn_openssl_* 辅助实现） |
| `openssl_pkey_get_public` `openssl_public_encrypt` `openssl_public_decrypt` | RSA 公钥加解密 |
| `dechex` `hexdec` `decbin` `bindec` `base_convert` | 进制转换 |
| `hex2bin` `bin2hex` `chr` `ord` | 字节转换 |
| `pack` `unpack` | 二进制打包 |

## 8. URL / 网络

| 函数 | 说明 |
|---|---|
| `urlencode` `rawurlencode` `urldecode` `rawurldecode` | URL 编解码（`rawurldecode` 注册两次，行为一致） |
| `http_build_query` `parse_url` `parse_str` | URL 解析 |
| `ip2long` `long2ip` `gethostbyname` `gethostbynamel` `dns_get_record` | IP / DNS（遵循项目 `dns.servers` 配置） |
| `utf8_encode` | 编码 |
| `get_headers` `header` `http_response_code` | HTTP |

## 9. JSON / 序列化

| 函数 | 说明 |
|---|---|
| `json_encode` `json_decode` | JSON（关联数组按 PHP 插入顺序输出） |
| `json_last_error` `json_last_error_msg` | 跟踪最近一次 `json_encode`/`json_decode` 的错误（成功为 0/"No error"） |
| `var_export` | 变量导出 |

## 10. cURL

| 函数 | 说明 |
|---|---|
| `curl_init` `curl_setopt` `curl_setopt_array` `curl_exec` `curl_error` `curl_getinfo` `curl_close` | 基础 cURL |
| `curl_errno` `curl_reset` | cURL 扩展 |
| `curl_multi_init` `curl_multi_add_handle` `curl_multi_exec` `curl_multi_getcontent` `curl_multi_info_read` `curl_multi_select` `curl_multi_remove_handle` `curl_multi_close` | **并发**多句柄（真并发 goroutine 实现） |

## 11. 正则表达式

| 函数 | 说明 |
|---|---|
| `preg_match` `preg_match_all` `preg_replace` | 基础 PCRE 接口 |
| `preg_replace_callback` `preg_replace_callback_array` `preg_split` `preg_quote` `preg_grep` | 扩展 |

> 使用 Go `regexp`（RE2 子集），与 PHP PCRE/PCRE2 存在差异（见[兼容性注意事项](#15-兼容性注意事项)）。

## 12. 变量 / 类型 / 函数调用

| 函数 | 说明 |
|---|---|
| `gettype` `boolval` `isset` `empty` `unset` | 变量操作 |
| `is_array` `is_string` `is_int` `is_float` `is_bool` `is_null` `is_numeric` | 类型判断 |
| `is_integer` `is_double` | **别名**（分别 → `is_int` / `is_float`） |
| `is_object` `is_scalar` `is_iterable` `is_countable` `is_resource` `is_callable` `settype` `get_debug_type` | 其它类型 |
| `call_user_func` `call_user_func_array` `function_exists` `defined` `constant` `extract` `define` | 函数 / 常量调用 |

## 13. 输出控制 / 语言结构 / 杂项

| 函数 | 说明 |
|---|---|
| `echo` `print` | 语言结构 |
| `ob_start` `ob_get_clean` `ob_get_contents` `ob_end_clean` `ob_get_level` `ob_flush` `flush` | 输出缓冲（真实实现；脚本结束自动刷残留缓冲） |
| `ob_implicit_flush` | 记录标记（phpgo 输出统一在脚本结束后下发，不改变收集行为） |
| `setcookie` | 真实发送 Set-Cookie 响应头 |
| `error_reporting` | 设置/获取错误级别（返回旧值） |
| `ini_set` `ini_get` | 请求内 ini 存储（返回旧值/已设值） |
| `php_sapi_name` | 返回 `cli-server` |
| `phpinfo` | 输出最小化 phpgo 信息 HTML |
| `session_start` `session_id` | 最小化会话（生成/复用 PHPSESSID 并下发 Cookie，`$_SESSION` 请求内可读写） |
| `getenv` | 返回真实进程环境变量（其次 `$_ENV`） |
| `error_log` | 写入错误日志（默认 stderr；PHP 类型 3 可写指定文件） |
| `set_time_limit` | 记录到 ini（纯 Go 运行时无法中途终止脚本执行，仅保留值） |

## 14. PHP 超全局变量

```text
$_GET      $_POST     $_REQUEST   $_SERVER
$_COOKIE   $_FILES    $_SESSION   $_ENV
$GLOBALS
```

## 15. 兼容性注意事项

### 字符串字节语义

PHP 字符串本质上是字节序列，`strlen()` 与 Go 的 UTF-8 字符数量并不完全等价：

```text
PHP strlen("你好") = 6
Go len("你好")      = 6
Go utf8.RuneCountInString("你好") = 2
```

因此 phpgo 保留 PHP 的字节语义。

### 日期格式

PHP `date()` 的格式字符与 Go `time.Format()` 完全不同：

```text
PHP:  date("Y-m-d H:i:s")
Go:   time.Now().Format("2006-01-02 15:04:05")
```

必须做格式字符转换，不能直接映射。

### 正则表达式

PHP `preg_*` 使用 PCRE/PCRE2 语义，phpgo 使用 Go `regexp`（RE2），存在差异：

- lookahead / lookbehind
- 部分 backreference
- PCRE 特有语法

如需完整 PCRE 兼容，应接入 PCRE2。

### 超时（重要）

Go（phpgo）的 HTTP 栈整体比原生 PHP + libcurl **慢**，同一个短超时在 Go 里更容易触发超时/无响应。

例如缓存/链接可用性校验里常见的写法：

```php
curl_setopt($ch, CURLOPT_CONNECTTIMEOUT, 0.1);
curl_setopt($ch, CURLOPT_TIMEOUT, 0.1);
```

原生 PHP 下 `0.1s` 通常够用（libcurl 很快），但在 phpgo 下同样的链接经常在 `0.1s` 内拿不到响应，导致校验被误判为失败。

结论：

- **超时时间要调大**（Go 里建议按秒给余量，如 `CONNECTTIMEOUT=3, TIMEOUT=5`）。
- 需要保留"快速校验"时，用**短超时 + 兜底重试**：先试 `0.1s`，超时/无响应再以更长超时（如 `4s`）重试一次，避免短超时误判。
- 校验超时（拿不到明确 HTTP 状态码）应视为"无法判断"，由调用方用缓存兜底，**不要清缓存、不要覆盖**；只有拿到明确非 200（403/404 等）才判定链接失效。
- 注意 phpgo 中超时/请求失败时 `curl_getinfo(CURLINFO_HTTP_CODE)` 可能返回空值而非 `0`，判断时用 `> 0` 而非 `=== 0`。

### 未实现 / 占位

绝大多数占位函数已真实化，剩余说明：

- `set_time_limit`：仅记录到 ini，**无法真正中途终止脚本执行**（纯 Go 运行时限制）。
- `ob_implicit_flush`：仅记录标记，phpgo 输出统一在脚本结束后下发，无真实增量推流。
- `flush`：输出统一在脚本结束下发，调用仅合并残留缓冲，无法中途推流。
- `error_log` / `getenv` 依赖宿主环境（stderr / 进程环境变量）。
- `srand`/`mt_srand` 已支持显式播种；`rand`/`mt_rand` 使用可播种 PRNG（未播种时自动随机）。

---

# Go PHP Runtime 推荐架构

如果你的最终目标是**用 Go 自己实现 PHP 基础运行时**，建议不要把这些函数直接散落到项目中，统一注册：

```go
type PHPFunction func(args ...PHPValue) (PHPValue, error)

type FunctionRegistry struct {
    Functions map[string]PHPFunction
}
```

例如：

```go
registry.Register("strlen", StrLen)
registry.Register("substr", SubStr)
registry.Register("json_encode", JSONEncode)
registry.Register("file_get_contents", FileGetContents)
registry.Register("curl_exec", CurlExec)
```

> 核心原则：先实现"函数 + PHP 类型系统"，不要直接把 PHP 函数一对一翻译成 Go 标准库函数。很多 PHP 函数存在独特的类型转换、错误处理和数组语义，直接映射会产生兼容性问题。
