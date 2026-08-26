# PHP 基本函数 Go 实现清单

> 目标：用于 Go 实现一个兼容 PHP 常用基础函数的运行时/兼容层。
>
> 说明：以下函数优先覆盖常见 PHP 脚本中的基础能力。PHP 的超全局变量（如 `$_GET`）以及语言关键字不属于普通函数，因此单独列出。

## 1. 输出

| PHP 函数 | Go 实现建议 | 说明 |
|---|---|---|
| `echo` | `fmt.Print` / Runtime Output | `echo` 是语言结构，不是普通函数 |
| `print` | `fmt.Print` / Runtime Output | `print` 是语言结构 |
| `var_dump()` | `Runtime.VarDump()` | 输出变量详细类型和值 |
| `print_r()` | `Runtime.PrintR()` | 友好输出数组/对象 |

## 2. 变量

| PHP 函数 | Go 实现建议 |
|---|---|
| `isset()` | `Runtime.Isset()` |
| `empty()` | `Runtime.Empty()` |
| `unset()` | `Runtime.Unset()` |
| `gettype()` | `Runtime.GetType()` |

## 3. 字符串

| PHP 函数 | Go 标准库/实现 |
|---|---|
| `strlen()` | `len` / UTF-8 专用实现 |
| `strpos()` | `strings.Index` |
| `str_contains()` | `strings.Contains` |
| `substr()` | 自定义 PHP 字符串语义 |
| `str_replace()` | `strings.ReplaceAll` |
| `strtolower()` | `strings.ToLower` |
| `strtoupper()` | `strings.ToUpper` |
| `trim()` | `strings.TrimSpace` + PHP 字符集语义 |
| `explode()` | `strings.Split` |
| `implode()` | `strings.Join` |
| `sprintf()` | `fmt.Sprintf` |

### 注意

PHP 字符串本质上是字节序列，`strlen()` 与 Go 的 UTF-8 字符数量并不完全等价。

例如：

```text
PHP strlen("你好") = 6
Go len("你好")      = 6
Go utf8.RuneCountInString("你好") = 2
```

因此如果目标是 PHP 兼容层，建议保留 PHP 的字节语义。

## 4. 数组

| PHP 函数 | Go 实现建议 |
|---|---|
| `count()` | `Runtime.Count()` |
| `in_array()` | `Runtime.InArray()` |
| `array_key_exists()` | `Runtime.ArrayKeyExists()` |
| `array_push()` | `Runtime.ArrayPush()` |
| `array_pop()` | `Runtime.ArrayPop()` |
| `array_shift()` | `Runtime.ArrayShift()` |
| `array_unshift()` | `Runtime.ArrayUnshift()` |
| `array_merge()` | `Runtime.ArrayMerge()` |
| `array_slice()` | `Runtime.ArraySlice()` |
| `array_filter()` | `Runtime.ArrayFilter()` |
| `array_map()` | `Runtime.ArrayMap()` |
| `sort()` | `Runtime.Sort()` |
| `rsort()` | `Runtime.RSort()` |
| `asort()` | `Runtime.ASort()` |
| `ksort()` | `Runtime.KSort()` |
| `array_keys()` | `Runtime.ArrayKeys()` |
| `array_values()` | `Runtime.ArrayValues()` |

### 建议的数据结构

不要直接使用 Go `map[string]any` 模拟 PHP 数组。

建议定义：

```go
type PHPArray struct {
    Items []PHPArrayItem
}

type PHPArrayItem struct {
    Key   PHPValue
    Value PHPValue
}
```

原因：

PHP array 同时具有：

- 数字索引数组
- 字符串关联数组
- 保持插入顺序
- 整数 key 自动递增
- 字符串/整数 key 的特殊转换规则

因此简单的 Go `map` 很难完整兼容 PHP。

## 5. 类型转换与判断

| PHP 函数 | Go 实现建议 |
|---|---|
| `intval()` | `Runtime.IntVal()` |
| `floatval()` | `Runtime.FloatVal()` |
| `strval()` | `Runtime.StrVal()` |
| `boolval()` | `Runtime.BoolVal()` |
| `is_string()` | `Runtime.IsString()` |
| `is_int()` | `Runtime.IsInt()` |
| `is_float()` | `Runtime.IsFloat()` |
| `is_bool()` | `Runtime.IsBool()` |
| `is_array()` | `Runtime.IsArray()` |
| `is_null()` | `Runtime.IsNull()` |
| `is_numeric()` | `Runtime.IsNumeric()` |

## 6. 文件

| PHP 函数 | Go 标准库/实现 |
|---|---|
| `file_get_contents()` | `os.ReadFile` |
| `file_put_contents()` | `os.WriteFile` |
| `file_exists()` | `os.Stat` |
| `is_file()` | `os.Stat` |
| `is_dir()` | `os.Stat` |
| `mkdir()` | `os.Mkdir` / `os.MkdirAll` |
| `rmdir()` | `os.Remove` |
| `unlink()` | `os.Remove` |
| `fopen()` | `os.Open` / 自定义 PHP File Handle |
| `fclose()` | `Close()` |
| `fread()` | `Read()` |
| `fwrite()` | `Write()` |

### 兼容性注意

`file_get_contents()`、`fopen()` 等函数不仅涉及本地文件，还可能涉及：

- stream wrapper
- `php://`
- `data://`
- `http://`
- `https://`
- 自定义 wrapper


## 7. JSON

| PHP 函数 | Go 标准库 |
|---|---|
| `json_encode()` | `encoding/json.Marshal` |
| `json_decode()` | `encoding/json.Unmarshal` |

建议额外处理 PHP 与 JSON 的差异：

- PHP 数组 → JSON array/object 的判断
- associative array
- `JSON_UNESCAPED_UNICODE`
- `JSON_PRETTY_PRINT`
- `JSON_UNESCAPED_SLASHES`
- `JSON_THROW_ON_ERROR`
- `null`
- 浮点数
- 大整数

## 8. 时间

| PHP 函数 | Go 实现 |
|---|---|
| `time()` | `time.Now().Unix()` |
| `date()` | 自定义 PHP Date Formatter |
| `strtotime()` | 自定义 PHP 时间解析 |
| `microtime()` | `time.Now()` |

### 注意

PHP `date()` 的格式字符与 Go `time.Format()` 完全不同。

例如：

```text
PHP:
date("Y-m-d H:i:s")

Go:
time.Now().Format("2006-01-02 15:04:05")
```

因此不能简单直接映射，必须做格式转换。

## 9. URL / Query String

| PHP 函数 | Go 实现 |
|---|---|
| `urlencode()` | `url.QueryEscape` / PHP 专用实现 |
| `urldecode()` | `url.QueryUnescape` |
| `parse_url()` | `net/url.Parse` + PHP 兼容层 |
| `parse_str()` | `url.ParseQuery` + PHP 数组规则 |
| `http_build_query()` | `url.Values` + PHP 专用实现 |

## 10. 正则表达式

| PHP 函数 | Go 实现 |
|---|---|
| `preg_match()` | `regexp` |
| `preg_match_all()` | `regexp` |
| `preg_replace()` | `regexp.ReplaceAllString` |
| `preg_split()` | `regexp.Split` |

### 重要

PHP `preg_*` 使用 PCRE/PCRE2 语义，而 Go `regexp` 使用 RE2。

两者存在兼容性差异，例如：

- lookahead
- lookbehind
- 部分 backreference
- PCRE 特有语法

如果目标是高兼容 PHP，建议后续接入 PCRE2，而不是完全依赖 Go `regexp`。

## 11. 哈希与编码

| PHP 函数 | Go 实现 |
|---|---|
| `md5()` | `crypto/md5` |
| `sha1()` | `crypto/sha1` |
| `hash()` | `crypto/*` + 算法注册表 |
| `base64_encode()` | `base64.StdEncoding.EncodeToString` |
| `base64_decode()` | `base64.StdEncoding.DecodeString` |

建议 `hash()` 设计成：

```go
func Hash(algo string, data []byte) ([]byte, error)
```

支持：

```text
md5
sha1
sha224
sha256
sha384
sha512
```

## 12. 文件与目录路径

| PHP 函数 | Go 实现 |
|---|---|
| `scandir()` | `os.ReadDir` |
| `glob()` | `path/filepath.Glob` |
| `basename()` | `filepath.Base` |
| `dirname()` | `filepath.Dir` |
| `realpath()` | `filepath.EvalSymlinks` |
| `pathinfo()` | 自定义 PHP PathInfo |

## 13. HTTP

| PHP 能力 | Go 实现 |
|---|---|
| `header()` | Runtime Response Header |
| `http_response_code()` | HTTP Response |
| `setcookie()` | `http.SetCookie` |
| `$_GET` | `http.Request.URL.Query()` |
| `$_POST` | Request Body/Form |
| `$_REQUEST` | GET + POST + Cookie |
| `$_SERVER` | `http.Request` + Runtime |
| `$_COOKIE` | `Request.Cookie()` |
| `$_FILES` | `multipart.FileHeader` |

## 14. PHP 超全局变量

这些不是函数，但 PHP Web Runtime 必须支持：

```text
$_GET
$_POST
$_REQUEST
$_SERVER
$_COOKIE
$_FILES
$_SESSION
$_ENV
$GLOBALS
```

建议统一放入：

```go
type PHPSuperGlobals struct {
    GET     *PHPArray
    POST    *PHPArray
    REQUEST *PHPArray
    SERVER  *PHPArray
    COOKIE  *PHPArray
    FILES   *PHPArray
    SESSION *PHPArray
    ENV     *PHPArray
    GLOBALS *PHPArray
}
```

# Go PHP Runtime 推荐架构

如果你的最终目标是**用 Go 自己实现 PHP 基础运行时**，建议不要把这些函数直接散落到项目中。

可以统一注册：

```go
type PHPFunction func(args ...PHPValue) (PHPValue, error)

type FunctionRegistry struct {
    Functions map[string]PHPFunction
}
```

例如：

```go
registry.Register("strlen", StrLen)
registry.Register("strpos", StrPos)
registry.Register("substr", SubStr)
registry.Register("str_replace", StrReplace)

registry.Register("count", Count)
registry.Register("in_array", InArray)
registry.Register("array_merge", ArrayMerge)

registry.Register("json_encode", JSONEncode)
registry.Register("json_decode", JSONDecode)

registry.Register("file_get_contents", FileGetContents)
registry.Register("file_put_contents", FilePutContents)

registry.Register("base64_encode", Base64Encode)
registry.Register("base64_decode", Base64Decode)

registry.Register("md5", MD5)
registry.Register("sha1", SHA1)
registry.Register("hash", Hash)
```

# 第一阶段建议优先实现

如果主要为了运行 IPTV/TVBox 一类 PHP 脚本，不需要一开始实现整个 PHP。

建议优先：

```text
字符串
├── strlen
├── strpos
├── str_contains
├── substr
├── str_replace
├── strtolower
├── strtoupper
├── trim
├── explode
├── implode
└── sprintf

数组
├── count
├── in_array
├── array_key_exists
├── array_merge
├── array_slice
├── array_filter
├── array_map
├── array_keys
└── array_values

类型
├── intval
├── floatval
├── strval
├── boolval
├── is_string
├── is_int
├── is_array
├── is_null
└── is_numeric

JSON
├── json_encode
└── json_decode

文件
├── file_get_contents
├── file_put_contents
├── file_exists
├── fopen
├── fclose
├── fread
└── fwrite

网络
├── urlencode
├── urldecode
├── parse_url
├── parse_str
└── http_build_query

编码
├── base64_encode
├── base64_decode
├── md5
├── sha1
└── hash

正则
├── preg_match
├── preg_match_all
├── preg_replace
└── preg_split

HTTP
├── header
├── http_response_code
└── setcookie
```

# 实现优先级

```text
P0  PHPValue / PHPArray / 类型系统
    ↓
P0  字符串函数
    ↓
P0  数组函数
    ↓
P0  类型判断/转换
    ↓
P0  JSON
    ↓
P0  URL
    ↓
P0  Base64 / Hash
    ↓
P1  文件系统
    ↓
P1  HTTP / $_GET / $_POST / $_SERVER
    ↓
P1  正则
    ↓
P2  时间
    ↓
P2  Stream
    ↓
P3  Session / Cookie 完整兼容
```

> 核心原则：先实现“函数 + PHP 类型系统”，不要直接把 PHP 函数一对一翻译成 Go 标准库函数。很多 PHP 函数存在独特的类型转换、错误处理和数组语义，直接映射会产生兼容性问题。
