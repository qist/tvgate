package phpgo

import (
	"math"
	"reflect"
	"strconv"
	"strings"
)

// PHP serialize / unserialize
//
// 支持的类型：null / bool / int / float / string / array / object。
// 解析侧另外支持 R:<id>; 与 r:<id>; 引用标记（其它 PHP 产出的序列化串中可能出现）。
//
// 注意：
//   - 生成侧【不会】产出 R:/r: 引用标记；遇到循环引用时退化为 N;（null），
//     避免无限递归导致栈溢出。
//   - 对象属性来自 map，序列化时按属性名排序输出（PHP 按声明序，phpgo 无顺序信息）。

func init() {
	builtins["serialize"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewNull(), nil
		}
		return NewString(phpSerialize(a[0])), nil
	}
	builtins["unserialize"] = func(e *Env, a []Value) (Value, error) {
		if len(a) < 1 {
			return NewBool(false), nil
		}
		// 第二参数 $options：['allowed_classes' => bool|array, 'max_depth' => int]
		opt := unserializeOptions{allowedAll: true}
		if len(a) >= 2 && deref(a[1]).Kind == KindArray {
			parseUnserializeOptions(deref(a[1]), &opt)
		}
		v, ok := phpUnserialize(a[0].ToString(), opt)
		if !ok {
			// 与 PHP 一致：解析失败返回 false
			return NewBool(false), nil
		}
		return v, nil
	}
}

// ---------------------------------------------------------------------------
// 序列化
// ---------------------------------------------------------------------------

// phpSerialize 生成 PHP serialize() 格式字符串
func phpSerialize(v Value) string {
	var b strings.Builder
	ctx := &serializeCtx{ids: map[uintptr]int{}}
	phpSerializeValue(v, &b, ctx)
	return b.String()
}

// serializeCtx 记录本次 serialize() 的引用状态（对齐 PHP 的 var_hash）：
// ids 为「已序列化过的数组/对象指针 → 引用 id」，id 按首次遇到顺序从 1 递增。
// 同一 zval 再次出现时 PHP 输出 R:<id>;（数组）/ r:<id>;（对象），
// 这同时也是循环引用的终止条件 —— 不会再无限递归，也不会丢数据。
type serializeCtx struct {
	ids   map[uintptr]int
	next  int
	depth int
}

// phpSerializeValue 递归序列化
func phpSerializeValue(v Value, b *strings.Builder, ctx *serializeCtx) {
	v = deref(v)
	// 保险：正常情况循环引用会被 R:/r: 截断，这里只对无法通过指针识别的病态结构兜底
	ctx.depth++
	defer func() { ctx.depth-- }()
	if ctx.depth > 100000 {
		// 纯保险：循环已被 R:/r: 截断，只有无法用指针识别的病态结构才会走到这
		b.WriteString("N;")
		return
	}
	switch v.Kind {
	case KindNull:
		b.WriteString("N;")
	case KindBool:
		if v.Bool {
			b.WriteString("b:1;")
		} else {
			b.WriteString("b:0;")
		}
	case KindInt:
		b.WriteString("i:")
		b.WriteString(strconv.FormatInt(v.Int, 10))
		b.WriteByte(';')
	case KindFloat:
		b.WriteString("d:")
		b.WriteString(phpSerializeFloat(v.Float))
		b.WriteByte(';')
	case KindString:
		phpSerializeString(v.Str, b)
	case KindArray:
		ptr := reflect.ValueOf(v.Arr).Pointer()
		if id, seen := ctx.ids[ptr]; seen && ptr != 0 {
			b.WriteString("R:")
			b.WriteString(strconv.Itoa(id))
			b.WriteByte(';')
			return
		}
		registerSerializeRef(ctx, ptr)
		b.WriteString("a:")
		b.WriteString(strconv.Itoa(len(v.Keys)))
		b.WriteString(":{")
		for _, k := range v.Keys {
			phpSerializeKey(k, b)
			phpSerializeValue(v.Arr[k], b, ctx)
		}
		b.WriteString("}")
	case KindObject:
		if v.Object == nil {
			b.WriteString("N;")
			return
		}
		ptr := reflect.ValueOf(v.Object).Pointer()
		if id, seen := ctx.ids[ptr]; seen && ptr != 0 {
			b.WriteString("r:")
			b.WriteString(strconv.Itoa(id))
			b.WriteByte(';')
			return
		}
		registerSerializeRef(ctx, ptr)
		// 属性按声明/插入顺序输出（PHP 语义，非排序）
		keys := v.Object.PropKeysInOrder()
		b.WriteString("O:")
		b.WriteString(strconv.Itoa(len(v.Object.ClassName)))
		b.WriteString(`:"`)
		b.WriteString(v.Object.ClassName)
		b.WriteString(`":`)
		b.WriteString(strconv.Itoa(len(keys)))
		b.WriteString(":{")
		for _, k := range keys {
			phpSerializeString(k, b)
			phpSerializeValue(v.Object.Properties[k], b, ctx)
		}
		b.WriteString("}")
	default:
		// resource 等不可序列化的类型：PHP 输出 i:0;
		b.WriteString("i:0;")
	}
}

// phpSerializeString 输出 s:<len>:"<bytes>";
func phpSerializeString(s string, b *strings.Builder) {
	b.WriteString("s:")
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteString(`:"`)
	b.WriteString(s)
	b.WriteString(`";`)
}

// phpSerializeKey 输出数组 key：规范整数键用 i:<n>;，其余用 s:<len>:"<key>";
func phpSerializeKey(k string, b *strings.Builder) {
	if n, ok := phpArrayIntKey(k); ok {
		b.WriteString("i:")
		b.WriteString(strconv.FormatInt(n, 10))
		b.WriteByte(';')
		return
	}
	phpSerializeString(k, b)
}

// registerSerializeRef 为数组/对象分配引用 id（指针对应同一 zval 时复用）
func registerSerializeRef(ctx *serializeCtx, ptr uintptr) {
	if ptr == 0 || ctx.ids == nil {
		return
	}
	ctx.next++
	ctx.ids[ptr] = ctx.next
}

// phpArrayIntKey 判断 PHP 数组 key 是否为规范整数字面量。
// "5" / "-5" 是整数键；"01" / "1.0" / " 1" / 超长溢出值都是字符串键。
func phpArrayIntKey(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	i := 0
	if s[0] == '-' {
		i = 1
	}
	if i >= len(s) {
		return 0, false
	}
	if s[i] == '0' && len(s)-i > 1 {
		return 0, false // 前导零不是规范整数
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false // 溢出，PHP 保留为字符串键
	}
	return n, true
}

// phpSerializeFloat 按 PHP serialize() 的浮点数格式输出（serialize_precision=-1 风格）
func phpSerializeFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "INF"
	case math.IsInf(f, -1):
		return "-INF"
	case math.IsNaN(f):
		return "NAN"
	}
	// 整数值不带小数点（PHP: serialize(1.0) => d:1;）
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	s := strconv.FormatFloat(f, 'G', -1, 64)
	// PHP 的指数形式带小数位（1.0E+30），Go 可能输出 1E+30，补齐以贴近 PHP
	if idx := strings.IndexByte(s, 'E'); idx >= 0 && !strings.Contains(s[:idx], ".") {
		s = s[:idx] + ".0" + s[idx:]
	}
	return s
}

// ---------------------------------------------------------------------------
// 反序列化
// ---------------------------------------------------------------------------

// unserializeOptions 对应 unserialize() 第二参数 $options
type unserializeOptions struct {
	allowedAll bool            // true：允许所有类（默认）
	allowed    map[string]bool // 类名白名单（小写），allowedAll 为 true 时忽略
	maxDepth   int             // 最大嵌套深度，0 表示不限制（PHP 7.4+ 的 max_depth）
}

// parseUnserializeOptions 解析 $options 数组
func parseUnserializeOptions(opts Value, o *unserializeOptions) {
	if v, ok := opts.Arr["allowed_classes"]; ok {
		v = deref(v)
		switch v.Kind {
		case KindBool:
			// false：禁止所有类，对象降级为 __PHP_Incomplete_Class
			if !v.Bool {
				o.allowedAll = false
				o.allowed = map[string]bool{}
			}
		case KindArray:
			// 类名白名单（PHP 按类名大小写不敏感匹配）
			o.allowedAll = false
			o.allowed = map[string]bool{}
			for _, k := range v.Keys {
				o.allowed[strings.ToLower(v.Arr[k].ToString())] = true
			}
		default:
			// 非 bool / 非数组：按最严格处理，禁止所有类
			o.allowedAll = false
			o.allowed = map[string]bool{}
		}
	}
	if v, ok := opts.Arr["max_depth"]; ok {
		if n := deref(v).ToInt(); n > 0 {
			o.maxDepth = int(n)
		}
	}
}

// unserializer 递归下降解析器。
// refs 登记可被引用的值（数组 / 对象），按打开顺序编号，供 R:/r: 使用（1-based）。
type unserializer struct {
	s     string
	i     int
	refs  []*Value
	opt   unserializeOptions
	depth int
}

// classAllowed 判断类名是否被 allowed_classes 允许
func (u *unserializer) classAllowed(cls string) bool {
	if u.opt.allowedAll {
		return true
	}
	return u.opt.allowed[strings.ToLower(cls)]
}

// phpUnserialize 解析 PHP serialize() 格式字符串；ok=false 表示格式非法
func phpUnserialize(s string, opt unserializeOptions) (Value, bool) {
	u := &unserializer{s: s, opt: opt}
	v, ok := u.parseValue()
	if !ok {
		return NewNull(), false
	}
	// 只允许尾部空白；其余多余内容视为非法（PHP 会报 E_NOTICE）
	if strings.TrimSpace(s[u.i:]) != "" {
		return NewNull(), false
	}
	return v, true
}

func (u *unserializer) parseValue() (Value, bool) {
	if u.i >= len(u.s) {
		return NewNull(), false
	}
	switch u.s[u.i] {
	case 'N':
		u.i++
		return NewNull(), u.expectByte(';')
	case 'b':
		u.i++
		if !u.expectByte(':') {
			return NewNull(), false
		}
		n, ok := u.readInt()
		if !ok || !u.expectByte(';') {
			return NewNull(), false
		}
		return NewBool(n != 0), true
	case 'i':
		u.i++
		if !u.expectByte(':') {
			return NewNull(), false
		}
		n, ok := u.readInt()
		if !ok || !u.expectByte(';') {
			return NewNull(), false
		}
		return NewInt(n), true
	case 'd':
		u.i++
		if !u.expectByte(':') {
			return NewNull(), false
		}
		tok, ok := u.readUntil(';')
		if !ok {
			return NewNull(), false
		}
		f, ok := phpUnserializeFloat(tok)
		if !ok {
			return NewNull(), false
		}
		return NewFloat(f), true
	case 's':
		u.i++
		if !u.expectByte(':') {
			return NewNull(), false
		}
		n, ok := u.readInt()
		if !ok || !u.expectByte(':') || !u.expectByte('"') {
			return NewNull(), false
		}
		if n < 0 || u.i+int(n) > len(u.s) {
			return NewNull(), false
		}
		str := u.s[u.i : u.i+int(n)]
		u.i += int(n)
		if !u.expectByte('"') || !u.expectByte(';') {
			return NewNull(), false
		}
		return NewString(str), true
	case 'a':
		u.i++
		if !u.expectByte(':') {
			return NewNull(), false
		}
		n, ok := u.readInt()
		if !ok || !u.expectByte(':') || !u.expectByte('{') {
			return NewNull(), false
		}
		// 嵌套深度只对数组/对象计数（与 PHP 的 max_depth 一致，标量不计数）
		u.depth++
		defer func() { u.depth-- }()
		if u.opt.maxDepth > 0 && u.depth > u.opt.maxDepth {
			return NewNull(), false
		}
		arr := NewArray()
		u.refs = append(u.refs, &arr)
		for j := int64(0); j < n; j++ {
			k, ok := u.parseValue()
			if !ok {
				return NewNull(), false
			}
			v, ok := u.parseValue()
			if !ok {
				return NewNull(), false
			}
			arr.ArraySet(k, v)
		}
		if !u.expectByte('}') {
			return NewNull(), false
		}
		return arr, true
	case 'O':
		u.i++
		if !u.expectByte(':') {
			return NewNull(), false
		}
		clen, ok := u.readInt()
		if !ok || !u.expectByte(':') || !u.expectByte('"') {
			return NewNull(), false
		}
		if clen < 0 || u.i+int(clen) > len(u.s) {
			return NewNull(), false
		}
		cls := u.s[u.i : u.i+int(clen)]
		u.i += int(clen)
		if !u.expectByte('"') || !u.expectByte(':') {
			return NewNull(), false
		}
		n, ok := u.readInt()
		if !ok || !u.expectByte(':') || !u.expectByte('{') {
			return NewNull(), false
		}
		// 嵌套深度只对数组/对象计数（与 PHP 的 max_depth 一致，标量不计数）
		u.depth++
		defer func() { u.depth-- }()
		if u.opt.maxDepth > 0 && u.depth > u.opt.maxDepth {
			return NewNull(), false
		}
		obj := NewObject(cls)
		if !u.classAllowed(cls) {
			// 与 PHP 一致：allowed_classes 不允许的类降级为 __PHP_Incomplete_Class，
			// 原类名保留在 __PHP_Incomplete_Class_Name 属性中
			obj = NewObject("__PHP_Incomplete_Class")
			obj.Object.SetProp("__PHP_Incomplete_Class_Name", NewString(cls))
		}
		u.refs = append(u.refs, &obj)
		for j := int64(0); j < n; j++ {
			k, ok := u.parseValue()
			if !ok {
				return NewNull(), false
			}
			v, ok := u.parseValue()
			if !ok {
				return NewNull(), false
			}
			obj.Object.SetProp(k.ToString(), v)
		}
		if !u.expectByte('}') {
			return NewNull(), false
		}
		return obj, true
	case 'R', 'r':
		// 引用：R 为值引用，r 为对象引用，均指向第 id 个登记值
		u.i++
		if !u.expectByte(':') {
			return NewNull(), false
		}
		id, ok := u.readInt()
		if !ok || !u.expectByte(';') {
			return NewNull(), false
		}
		if id < 1 || id > int64(len(u.refs)) {
			return NewNull(), false
		}
		return *u.refs[id-1], true
	}
	return NewNull(), false
}

// expectByte 消费一个期望的字符
func (u *unserializer) expectByte(c byte) bool {
	if u.i >= len(u.s) || u.s[u.i] != c {
		return false
	}
	u.i++
	return true
}

// readInt 读取十进制整数（允许负号）
func (u *unserializer) readInt() (int64, bool) {
	start := u.i
	if u.i < len(u.s) && (u.s[u.i] == '-' || u.s[u.i] == '+') {
		u.i++
	}
	digits := u.i
	for u.i < len(u.s) && u.s[u.i] >= '0' && u.s[u.i] <= '9' {
		u.i++
	}
	if u.i == digits {
		u.i = start
		return 0, false
	}
	n, err := strconv.ParseInt(u.s[start:u.i], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// readUntil 读取到分隔符 c 为止（并消费分隔符）
func (u *unserializer) readUntil(c byte) (string, bool) {
	start := u.i
	for u.i < len(u.s) && u.s[u.i] != c {
		u.i++
	}
	if u.i >= len(u.s) {
		return "", false
	}
	tok := u.s[start:u.i]
	u.i++
	return tok, true
}

// phpUnserializeFloat 解析 d: 后的浮点字面量（含 INF / -INF / NAN）
func phpUnserializeFloat(tok string) (float64, bool) {
	switch tok {
	case "INF":
		return math.Inf(1), true
	case "-INF":
		return math.Inf(-1), true
	case "NAN", "-NAN":
		return math.NaN(), true
	}
	f, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
