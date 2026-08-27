package phpgo

// AST 节点定义（覆盖 /www 脚本用到的 PHP 语法子集）

// Node 所有节点
type Node interface{}

// Stmt 语句
type Stmt interface{ Node }

// Expr 表达式
type Expr interface{ Node }

// Program 顶层
type Program struct {
	Stmts []Stmt
}

// ---------------------------------------------------------------------------
// 语句
// ---------------------------------------------------------------------------

// ExprStmt 裸表达式语句
type ExprStmt struct{ E Expr }

// EchoStmt echo
type EchoStmt struct{ E Expr }

// ExitStmt exit / die
type ExitStmt struct{ E Expr }

// ReturnStmt return
type ReturnStmt struct{ E Expr }

// AssignStmt $v = expr  /  $v .= expr  /  $v += expr 等
type AssignStmt struct {
	Name  string // 不含 $
	Value Expr
	Concat bool  // .=
	Op    string // "+=", "-=", "*=", "/=" 等（非 concat 时）
}

// ArrayPushStmt $arr[] = expr
type ArrayPushStmt struct {
	Arr  Expr
	Val  Expr
}

// ArrayAssignStmt $arr[$key] = expr
type ArrayAssignStmt struct {
	Arr Expr
	Key Expr
	Val Expr
}

// NestedArrayAssignStmt $arr[$k1][$k2]... = expr
type NestedArrayAssignStmt struct {
	Base Expr    // 根变量表达式
	Indices []Expr // 各级下标
	Val Expr
}

// IfStmt if / elseif / else
type IfStmt struct {
	Conds []Expr
	Bodies [][]Stmt
	Else   []Stmt
}

// ForeachStmt foreach ($arr as $k => $v) { ... }
type ForeachStmt struct {
	Arr   Expr
	KeyVar string // 可为空（不含 $）
	ValVar string // 不含 $
	Body  []Stmt
}

// ForStmt for (init; cond; post) { ... }
type ForStmt struct {
	Init []Stmt
	Cond Expr
	Post []Stmt
	Body []Stmt
}

// WhileStmt while (cond) { ... }
type WhileStmt struct {
	Cond Expr
	Body []Stmt
}

// DoWhileStmt do { ... } while (cond)
type DoWhileStmt struct {
	Cond Expr
	Body []Stmt
}

// SwitchStmt switch (v) { case ...: ... default: ... }
type SwitchStmt struct {
	Subject Expr
	Cases   []SwitchCase
}

// SwitchCase 单个 case
type SwitchCase struct {
	IsDefault bool
	Value     Expr // nil for default
	Body      []Stmt
}

// BreakStmt break [N]
type BreakStmt struct{ N int }

// ContinueStmt continue [N]
type ContinueStmt struct{ N int }

// FuncDecl 函数定义
type FuncDecl struct {
	Name string
	Params []FuncParam
	Body  []Stmt
}

// FuncParam 函数参数
type FuncParam struct {
	Name     string // 不含 $
	ByRef    bool   // &$var
	Default  Expr   // 默认值（可为 nil）
	Variadic bool   // ...$rest
}

// GlobalStmt global $a, $b, ...
type GlobalStmt struct{ Names []string }

// ConstStmt const NAME = value;
type ConstStmt struct {
	Name string
	Val  Expr
}

// UnsetStmt unset($var) / unset($arr[$key])
type UnsetStmt struct{ Args []Expr }

// IncludeStmt require / include / require_once / include_once
type IncludeStmt struct {
	Path Expr
	Once bool
}

// PostIncStmt $i++ / $i--
type PostIncStmt struct {
	Name string
	IsDec bool // true 为 --
}

// ---------------------------------------------------------------------------
// 表达式
// ---------------------------------------------------------------------------

// VarExpr $name
type VarExpr struct{ Name string }

// ScalarInt
type ScalarInt struct{ Val int64 }

// ScalarFloat
type ScalarFloat struct{ Val float64 }

// ScalarStr 字符串字面量
type ScalarStr struct{ Val string }

// InterpolatedStr 双引号字符串（含变量插值）
// Parts 交替为字符串和变量表达式
type InterpolatedStr struct {
	Parts []interface{} // string 或 Expr
}

// ConstTrue/False/Null
type ConstBool struct{ Val bool }
type ConstNull struct{}

// ArrayExpr array( k => v, ... )  或短数组 [ ... ]
type ArrayExpr struct {
	Keys   []Expr // nil 表示索引数组
	Values []Expr
}

// FuncCall 函数调用（含内置函数）
type FuncCall struct {
	Name string
	Args []Expr
}

// MethodCall $obj->method()
type MethodCall struct {
	Receiver Expr
	Method   string
	Args     []Expr
}

// StaticCall Class::method()
type StaticCall struct {
	Class  string
	Method string
	Args   []Expr
}

// PropertyAccess $obj->prop
type PropertyAccess struct {
	Receiver Expr
	Prop     string
}

// IndexExpr $arr[$key]
type IndexExpr struct {
	Arr Expr
	Key Expr
}

// BinaryExpr 二元运算
type BinaryExpr struct {
	Op    string // ".", "+", "-", "==", "<", ">", "*", "/", "%", "&&", "||", "!=", "!==", "<=", ">=", "==="  等
	Left  Expr
	Right Expr
}

// UnaryExpr 一元运算 (!expr, -expr)
type UnaryExpr struct {
	Op   string // "!" or "-"
	Expr Expr
}

// AssignExpr 赋值表达式 $a = $b = 1
type AssignExpr struct {
	Target Expr
	Val    Expr
}

// TernaryExpr cond ? a : b
type TernaryExpr struct {
	Cond Expr
	Then Expr
	Else Expr
}

// NullCoalesceExpr $a ?? $b
type NullCoalesceExpr struct {
	Left  Expr
	Right Expr
}

// ClosureExpr 闭包 function($a) use ($b) { ... }
type ClosureExpr struct {
	Params []FuncParam
	Uses   []string // 不含 $
	ByRef  []bool   // use(&$var) 对应索引
	Body   []Stmt
}

// CastExpr (int)$val 等
type CastExpr struct {
	Kind string // "int", "float", "bool", "string", "array"
	Expr Expr
}

// InstanceOfExpr $a instanceof Class
type InstanceOfExpr struct {
	Expr  Expr
	Class string
}

// EmptyExpr empty($var)
type EmptyExpr struct{ E Expr }

// IssetExpr isset($a, $b, ...)
type IssetExpr struct{ Args []Expr }

// MagicConstExpr 魔术常量 __DIR__/__FILE__/__LINE__
type MagicConstExpr struct{ Name string }

// ConstExpr 未定义常量引用（运行时查 e.consts，找不到则当字符串名）
type ConstExpr struct{ Name string }
