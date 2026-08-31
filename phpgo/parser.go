package phpgo

import (
	"fmt"
	"strings"
)

// Parser 递归下降解析器
type Parser struct {
	toks      []Tok
	pos       int
	constsMap map[string]Value // 预定义常量
}

// NewParser ...
func NewParser(toks []Tok) *Parser {
	return &Parser{toks: toks, constsMap: defaultPHPConsts()}
}

func (p *Parser) cur() Tok          { return p.toks[p.pos] }
func (p *Parser) adv() Tok          { t := p.toks[p.pos]; p.pos++; return t }
func (p *Parser) at(k tokKind) bool { return p.cur().Kind == k }
func (p *Parser) atVal(v string) bool {
	return p.cur().Kind == tIdent && strings.EqualFold(p.cur().Val, v)
}

func (p *Parser) expect(k tokKind) (Tok, error) {
	if p.cur().Kind != k {
		return Tok{}, fmt.Errorf("parse: 期望 %d，实际 %q at %d", k, p.cur().Val, p.cur().Pos)
	}
	return p.adv(), nil
}

// skip semicolons
func (p *Parser) skipSemis() {
	for p.at(tSemi) {
		p.adv()
	}
}

// peekN 返回当前位置偏移 n 的 token（不消耗）
func (p *Parser) peekN(n int) Tok {
	if p.pos+n >= len(p.toks) {
		return Tok{Kind: tEOF}
	}
	return p.toks[p.pos+n]
}

// Parse 解析整个程序
func (p *Parser) Parse() (*Program, error) {
	prog := &Program{}
	for !p.at(tEOF) {
		p.skipSemis()
		if p.at(tEOF) {
			break
		}
		if p.at(tFunc) {
			fn, err := p.parseFunc()
			if err != nil {
				return nil, err
			}
			prog.Stmts = append(prog.Stmts, fn)
			continue
		}
		st, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		if st != nil {
			prog.Stmts = append(prog.Stmts, st)
		}
	}
	return prog, nil
}

func (p *Parser) parseFunc() (Stmt, error) {
	p.adv() // func
	name, err := p.expect(tIdent)
	if err != nil {
		return nil, err
	}
	params, err := p.parseFuncParams()
	if err != nil {
		return nil, err
	}
	// 跳过返回类型声明：function foo(): string { ... }
	if p.at(tColon) {
		p.adv() // 跳过 :
		// 跳过返回类型（可能是 int, string, array, void, bool, float, ?type 等）
		if p.at(tQuestion) {
			p.adv()
		}
		// 跳过类型标识符（可能含命名空间 \NS\Class）
		// tArray 也是合法的返回类型
		for p.at(tIdent) || p.at(tArray) || p.atVal("\\") {
			p.adv()
		}
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &FuncDecl{Name: name.Val, Params: params, Body: body}, nil
}

func (p *Parser) parseFuncParams() ([]FuncParam, error) {
	if _, err := p.expect(tLParen); err != nil {
		return nil, err
	}
	var params []FuncParam
	for !p.at(tRParen) && !p.at(tEOF) {
		var param FuncParam
		if p.at(tAmp) {
			p.adv()
			param.ByRef = true
		}
		// 跳过类型提示：int, string, array, bool, float, ?type, \Name\Class 等
		if p.at(tQuestion) {
			p.adv()
		}
		// tArray 也可能作为类型提示（如 array $params）
		for (p.at(tIdent) || p.at(tArray)) && !p.atVal("true") && !p.atVal("false") && !p.atVal("null") {
			// 检查下一个 token 是否是 $var，如果是则当前 token 是类型提示
			if p.peekN(1).Kind == tVar {
				p.adv() // 跳过类型提示
				break
			}
			// 可能是可变参数 ... $var
			if p.peekN(1).Kind == tIdent && p.peekN(1).Val == "..." {
				break
			}
			p.adv() // 跳过命名空间前缀等
		}
		// 跳过可变参数标记 ...
		if p.atVal("...") {
			p.adv()
			param.Variadic = true
		}
		if !p.at(tVar) {
			return nil, fmt.Errorf("parse: 函数参数须为变量 at %d", p.cur().Pos)
		}
		param.Name = p.adv().Val[1:]
		if p.at(tEquals) {
			p.adv()
			def, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			param.Default = def
		}
		params = append(params, param)
		if p.at(tComma) {
			p.adv()
			continue
		}
		break
	}
	if _, err := p.expect(tRParen); err != nil {
		return nil, err
	}
	return params, nil
}

// parseBlock 解析 {...} 内的语句列表（也支持单语句块）
func (p *Parser) parseBlock() ([]Stmt, error) {
	if !p.at(tLBrace) {
		st, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		if st != nil {
			return []Stmt{st}, nil
		}
		return nil, nil
	}
	if _, err := p.expect(tLBrace); err != nil {
		return nil, err
	}
	var stmts []Stmt
	for !p.at(tRBrace) && !p.at(tEOF) {
		p.skipSemis()
		if p.at(tRBrace) {
			break
		}
		st, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		if st != nil {
			stmts = append(stmts, st)
		}
	}
	if _, err := p.expect(tRBrace); err != nil {
		return nil, err
	}
	return stmts, nil
}

// parseStmt 解析单条语句
func (p *Parser) parseStmt() (Stmt, error) {
	p.skipSemis()
	switch {
	case p.at(tEcho) || p.at(tPrint):
		p.adv()
		var args []Expr
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, e)
		for p.at(tComma) {
			p.adv()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, e)
		}
		p.skipSemis()
		if len(args) == 1 {
			return &EchoStmt{E: args[0]}, nil
		}
		return &ExprStmt{E: &FuncCall{Name: "echo", Args: args}}, nil
	case p.at(tExit) || p.at(tDie):
		p.adv()
		var e Expr
		if p.at(tLParen) {
			p.adv()
			if !p.at(tRParen) {
				e, _ = p.parseExpr()
			}
			p.expect(tRParen)
		}
		p.skipSemis()
		return &ExitStmt{E: e}, nil
	case p.at(tReturn):
		p.adv()
		var e Expr
		if !p.at(tSemi) && !p.at(tRBrace) && !p.at(tEOF) {
			e, _ = p.parseExpr()
		}
		p.skipSemis()
		return &ReturnStmt{E: e}, nil
	case p.at(tIf):
		return p.parseIf()
	case p.at(tForeach):
		return p.parseForeach()
	case p.at(tFor):
		return p.parseFor()
	case p.at(tWhile):
		return p.parseWhile()
	case p.at(tDo):
		return p.parseDoWhile()
	case p.at(tSwitch):
		return p.parseSwitch()
	case p.at(tBreak):
		p.adv()
		n := 1
		if p.at(tInt) {
			n = int(p.adv().Val[0] - '0')
		}
		p.skipSemis()
		return &BreakStmt{N: n}, nil
	case p.at(tContinue):
		p.adv()
		n := 1
		if p.at(tInt) {
			n = int(p.adv().Val[0] - '0')
		}
		p.skipSemis()
		return &ContinueStmt{N: n}, nil
	case p.at(tFunc):
		if p.peekN(1).Kind == tIdent {
			return p.parseFunc()
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipSemis()
		return &ExprStmt{E: e}, nil
	case p.at(tGlobal):
		p.adv()
		var names []string
		for p.at(tVar) {
			names = append(names, p.adv().Val[1:])
			if p.at(tComma) {
				p.adv()
				continue
			}
			break
		}
		p.skipSemis()
		return &GlobalStmt{Names: names}, nil
	case p.at(tVar):
		return p.parseAssignOrExpr()
	case p.at(tList):
		return p.parseListAssign()
	case p.at(tConst):
		return p.parseConstStmt()
	case p.atVal("unset"):
		return p.parseUnset()
	case p.at(tDeclare):
		return p.parseDeclare()
	case p.at(tTry):
		return p.parseTry()
	case p.at(tClass):
		return p.parseClass()
	case p.at(tThrow):
		p.adv()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipSemis()
		return &ThrowStmt{E: e}, nil
	}
	// @ 错误抑制
	if p.atVal("@") {
		p.adv()
	}
	if isExprStart(p.cur()) {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.at(tPlusPlus) || p.at(tMinusMinus) {
			isDec := p.at(tMinusMinus)
			p.adv()
			p.skipSemis()
			if v, ok := e.(*VarExpr); ok {
				return &PostIncStmt{Name: v.Name, IsDec: isDec}, nil
			}
		}
		p.skipSemis()
		return &ExprStmt{E: e}, nil
	}
	if !p.at(tEOF) {
		p.adv()
	}
	return nil, nil
}

func isExprStart(t Tok) bool {
	switch t.Kind {
	case tVar, tIdent, tInt, tFloat, tDoubleStr, tSingleStr, tConstTrue, tConstFalse, tConstNull, tArray, tLBracket, tLParen, tBang, tMinus, tStringCast, tIntCast, tFloatCast, tBoolCast, tArrayCast, tObjectCast, tFunc, tConst, tNew, tThrow, Caret:
		return true
	}
	return false
}

func (p *Parser) parseAssignOrExpr() (Stmt, error) {
	v := p.adv() // $name
	name := v.Val[1:]
	if p.at(tLBracket) {
		var indices []Expr
		for p.at(tLBracket) {
			p.adv()
			if p.at(tRBracket) {
				p.adv()
				if p.at(tEquals) {
					p.adv()
					val, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					p.skipSemis()
					return &ArrayPushStmt{Arr: &VarExpr{Name: name}, Val: val}, nil
				}
				indices = append(indices, nil)
				continue
			}
			key, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			p.expect(tRBracket)
			indices = append(indices, key)
		}
		if p.at(tEquals) {
			p.adv()
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			p.skipSemis()
			if len(indices) == 1 {
				return &ArrayAssignStmt{Arr: &VarExpr{Name: name}, Key: indices[0], Val: val}, nil
			}
			return &NestedArrayAssignStmt{
				Base:    &VarExpr{Name: name},
				Indices: indices,
				Val:     val,
			}, nil
		}
		if p.at(tConcatEq) || p.at(tPlusEq) || p.at(tMinusEq) || p.at(tStarEq) || p.at(tSlashEq) || p.at(tCaretEq) {
			op := p.adv().Val
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			p.skipSemis()
			if len(indices) == 1 {
				return &ArrayAssignStmt{
					Arr: &VarExpr{Name: name},
					Key: indices[0],
					Val: &BinaryExpr{Op: op, Left: &IndexExpr{Arr: &VarExpr{Name: name}, Key: indices[0]}, Right: val},
				}, nil
			}
			return &ExprStmt{E: &VarExpr{Name: name}}, nil
		}
		e := Expr(&VarExpr{Name: name})
		for _, idx := range indices {
			if idx == nil {
				break
			}
			e = &IndexExpr{Arr: e, Key: idx}
		}
		e2, err := p.parsePostfixFrom(e)
		if err != nil {
			return nil, err
		}
		p.skipSemis()
		return &ExprStmt{E: e2}, nil
	}
	if p.at(tPlusPlus) {
		p.adv()
		p.skipSemis()
		return &PostIncStmt{Name: name, IsDec: false}, nil
	}
	if p.at(tMinusMinus) {
		p.adv()
		p.skipSemis()
		return &PostIncStmt{Name: name, IsDec: true}, nil
	}
	if p.at(tEquals) || p.at(tConcatEq) || p.at(tPlusEq) || p.at(tMinusEq) || p.at(tStarEq) || p.at(tSlashEq) || p.at(tCaretEq) {
		concat := false
		op := ""
		switch p.cur().Kind {
		case tConcatEq:
			concat = true
			p.adv()
		case tPlusEq:
			op = "+="
			p.adv()
		case tMinusEq:
			op = "-="
			p.adv()
		case tStarEq:
			op = "*="
			p.adv()
		case tSlashEq:
			op = "/="
			p.adv()
		case tCaretEq:
			op = "^="
			p.adv()
		default:
			p.adv()
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipSemis()
		return &AssignStmt{Name: name, Value: val, Concat: concat, Op: op}, nil
	}
	e, err := p.parseExprFromVar(name)
	if err != nil {
		return nil, err
	}
	// Check for assignment to property/array: $this->prop = val, $arr[$k] = val
	if p.at(tEquals) || p.at(tPlusEq) || p.at(tMinusEq) || p.at(tStarEq) || p.at(tSlashEq) || p.at(tCaretEq) || p.at(tConcatEq) {
		op := p.adv().Val
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipSemis()
		return &AssignStmt{Target: e, Value: val, Op: op}, nil
	}
	p.skipSemis()
	return &ExprStmt{E: e}, nil
}

func (p *Parser) parseListAssign() (Stmt, error) {
	p.adv() // list
	p.expect(tLParen)
	var targets []Expr
	for !p.at(tRParen) {
		if p.at(tComma) {
			targets = append(targets, &ConstNull{})
			p.adv()
			continue
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		targets = append(targets, e)
		if p.at(tComma) {
			p.adv()
			continue
		}
		break
	}
	p.expect(tRParen)
	p.expect(tEquals)
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipSemis()
	return &ExprStmt{E: &AssignExpr{
		Target: &FuncCall{Name: "__list", Args: targets},
		Val:    val,
	}}, nil
}

// parseConstStmt 解析 const NAME = value;
func (p *Parser) parseConstStmt() (Stmt, error) {
	p.adv() // const
	name, err := p.expect(tIdent)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tEquals); err != nil {
		return nil, err
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipSemis()
	return &ConstStmt{Name: name.Val, Val: val}, nil
}

func (p *Parser) parseIf() (Stmt, error) {
	ifs := &IfStmt{}
	p.adv() // if
	if _, err := p.expect(tLParen); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tRParen); err != nil {
		return nil, err
	}
	p.skipSemis()
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	ifs.Conds = append(ifs.Conds, cond)
	ifs.Bodies = append(ifs.Bodies, body)

	for p.at(tElseIf) || (p.at(tElse) && p.peekN(1).Kind == tIf) {
		if p.at(tElse) {
			p.adv()
		}
		p.adv()
		if _, err := p.expect(tLParen); err != nil {
			return nil, err
		}
		c2, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tRParen); err != nil {
			return nil, err
		}
		p.skipSemis()
		b2, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		ifs.Conds = append(ifs.Conds, c2)
		ifs.Bodies = append(ifs.Bodies, b2)
	}
	if p.at(tElse) {
		p.adv()
		p.skipSemis()
		el, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		ifs.Else = el
	}
	return ifs, nil
}

func (p *Parser) parseForeach() (Stmt, error) {
	p.adv() // foreach
	if _, err := p.expect(tLParen); err != nil {
		return nil, err
	}
	arrExpr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tAs); err != nil {
		return Tok{}, fmt.Errorf("parse: foreach 期望 as at %d", p.cur().Pos)
	}
	v1, err := p.expect(tVar)
	if err != nil {
		return Tok{}, err
	}
	keyVar := ""
	valVar := v1.Val[1:]
	if p.at(tArrowFn) {
		p.adv()
		keyVar = valVar
		if p.at(tAmp) {
			p.adv()
		}
		v2, err := p.expect(tVar)
		if err != nil {
			return nil, err
		}
		valVar = v2.Val[1:]
	}
	if _, err := p.expect(tRParen); err != nil {
		return nil, err
	}
	p.skipSemis()
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ForeachStmt{Arr: arrExpr, KeyVar: keyVar, ValVar: valVar, Body: body}, nil
}

func (p *Parser) parseFor() (Stmt, error) {
	p.adv() // for
	if _, err := p.expect(tLParen); err != nil {
		return nil, err
	}
	var initStmts []Stmt
	for !p.at(tSemi) && !p.at(tEOF) {
		st, err := p.parseStmt()
		if err != nil {
			e, err2 := p.parseExpr()
			if err2 != nil {
				return nil, err
			}
			initStmts = append(initStmts, &ExprStmt{E: e})
		} else if st != nil {
			initStmts = append(initStmts, st)
		}
		if p.at(tComma) {
			p.adv()
			continue
		}
		break
	}
	p.expect(tSemi)
	var cond Expr
	if !p.at(tSemi) {
		c, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		cond = c
	}
	p.expect(tSemi)
	var postStmts []Stmt
	for !p.at(tRParen) && !p.at(tEOF) {
		st, err := p.parseStmt()
		if err != nil {
			e, err2 := p.parseExpr()
			if err2 != nil {
				return nil, err
			}
			postStmts = append(postStmts, &ExprStmt{E: e})
		} else if st != nil {
			postStmts = append(postStmts, st)
		}
		if p.at(tComma) {
			p.adv()
			continue
		}
		break
	}
	p.expect(tRParen)
	p.skipSemis()
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ForStmt{Init: initStmts, Cond: cond, Post: postStmts, Body: body}, nil
}

func (p *Parser) parseWhile() (Stmt, error) {
	p.adv() // while
	if _, err := p.expect(tLParen); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tRParen); err != nil {
		return nil, err
	}
	p.skipSemis()
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &WhileStmt{Cond: cond, Body: body}, nil
}

func (p *Parser) parseDoWhile() (Stmt, error) {
	p.adv() // do
	p.skipSemis()
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tWhile); err != nil {
		return nil, fmt.Errorf("parse: do-while 期望 while at %d", p.cur().Pos)
	}
	if _, err := p.expect(tLParen); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tRParen); err != nil {
		return nil, err
	}
	p.skipSemis()
	return &DoWhileStmt{Cond: cond, Body: body}, nil
}

func (p *Parser) parseSwitch() (Stmt, error) {
	p.adv() // switch
	if _, err := p.expect(tLParen); err != nil {
		return nil, err
	}
	subj, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tRParen); err != nil {
		return nil, err
	}
	p.skipSemis()
	if p.at(tLBrace) {
		p.adv()
	}
	sw := &SwitchStmt{Subject: subj}
	for !p.at(tRBrace) && !p.at(tEOF) {
		p.skipSemis()
		if p.at(tCase) {
			p.adv()
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if p.at(tColon) {
				p.adv()
			} else {
				p.skipSemis()
			}
			var body []Stmt
			for !p.at(tCase) && !p.at(tDefault) && !p.at(tRBrace) && !p.at(tEOF) {
				st, err := p.parseStmt()
				if err != nil {
					return nil, err
				}
				if st != nil {
					body = append(body, st)
				}
				p.skipSemis()
			}
			sw.Cases = append(sw.Cases, SwitchCase{Value: val, Body: body})
		} else if p.at(tDefault) {
			p.adv()
			if p.at(tColon) {
				p.adv()
			} else {
				p.skipSemis()
			}
			var body []Stmt
			for !p.at(tCase) && !p.at(tDefault) && !p.at(tRBrace) && !p.at(tEOF) {
				st, err := p.parseStmt()
				if err != nil {
					return nil, err
				}
				if st != nil {
					body = append(body, st)
				}
				p.skipSemis()
			}
			sw.Cases = append(sw.Cases, SwitchCase{IsDefault: true, Body: body})
		} else {
			break
		}
	}
	if p.at(tRBrace) {
		p.adv()
	}
	return sw, nil
}

// parseExpr 解析表达式（入口）
func (p *Parser) parseExpr() (Expr, error) {
	return p.parseAssignExpr()
}

func (p *Parser) parseAssignExpr() (Expr, error) {
	left, err := p.parseTernary()
	if err != nil {
		return nil, err
	}
	if p.at(tEquals) {
		p.adv()
		right, err := p.parseAssignExpr()
		if err != nil {
			return nil, err
		}
		return &AssignExpr{Target: left, Val: right}, nil
	}
	if p.at(tConcatEq) || p.at(tPlusEq) || p.at(tMinusEq) || p.at(tStarEq) || p.at(tSlashEq) || p.at(tCaretEq) {
		op := p.adv().Val
		right, err := p.parseAssignExpr()
		if err != nil {
			return nil, err
		}
		return &AssignExpr{
			Target: left,
			Val:    &BinaryExpr{Op: op, Left: left, Right: right},
		}, nil
	}
	return left, nil
}

func (p *Parser) parseTernary() (Expr, error) {
	left, err := p.parseNullCoalesce()
	if err != nil {
		return nil, err
	}
	if p.at(tQuestion) {
		p.adv()
		if p.at(tColon) {
			p.adv()
			elseExpr, err := p.parseAssignExpr()
			if err != nil {
				return nil, err
			}
			return &TernaryExpr{Cond: left, Then: nil, Else: elseExpr}, nil
		}
		thenExpr, err := p.parseAssignExpr()
		if err != nil {
			return nil, err
		}
		if p.at(tColon) {
			p.adv()
			elseExpr, err := p.parseAssignExpr()
			if err != nil {
				return nil, err
			}
			return &TernaryExpr{Cond: left, Then: thenExpr, Else: elseExpr}, nil
		}
		return &TernaryExpr{Cond: left, Then: thenExpr, Else: nil}, nil
	}
	return left, nil
}

func (p *Parser) parseNullCoalesce() (Expr, error) {
	left, err := p.parseLogicalOr()
	if err != nil {
		return nil, err
	}
	if p.at(tNullCoalesce) {
		p.adv()
		right, err := p.parseNullCoalesce()
		if err != nil {
			return nil, err
		}
		return &NullCoalesceExpr{Left: left, Right: right}, nil
	}
	return left, nil
}

func (p *Parser) parseLogicalOr() (Expr, error) {
	left, err := p.parseLogicalAnd()
	if err != nil {
		return nil, err
	}
	for p.at(tPipePipe) {
		p.adv()
		right, err := p.parseLogicalAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "||", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseLogicalAnd() (Expr, error) {
	left, err := p.parseBitwiseOr()
	if err != nil {
		return nil, err
	}
	for p.at(tAmpAmp) {
		p.adv()
		right, err := p.parseBitwiseOr()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "&&", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseBitwiseOr() (Expr, error) {
	left, err := p.parseBitwiseXor()
	if err != nil {
		return nil, err
	}
	for p.at(tPipe) {
		p.adv()
		right, err := p.parseBitwiseXor()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "|", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseBitwiseXor() (Expr, error) {
	left, err := p.parseBitwiseAnd()
	if err != nil {
		return nil, err
	}
	for p.at(Caret) {
		p.adv()
		right, err := p.parseBitwiseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "^", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseBitwiseAnd() (Expr, error) {
	left, err := p.parseCompare()
	if err != nil {
		return nil, err
	}
	for p.at(tAmp) {
		p.adv()
		right, err := p.parseCompare()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "&", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseCompare() (Expr, error) {
	left, err := p.parseShift()
	if err != nil {
		return nil, err
	}
	// instanceof 运算符：$a instanceof MyClass
	if p.at(tInstanceOf) {
		p.adv()
		// 右侧是类名（可能是 tIdent 或 StaticCall）
		var className string
		if p.at(tIdent) {
			className = p.adv().Val
		} else {
			// 无法解析类名，返回 true 作为安全默认值
			return left, nil
		}
		left = &InstanceOfExpr{Expr: left, Class: className}
		return left, nil
	}
	for p.at(tEqEq) || p.at(tEqEqEq) || p.at(tNotEq) || p.at(tNotEqEq) || p.at(tLT) || p.at(tGT) || p.at(tLE) || p.at(tGE) {
		op := p.adv().Val
		right, err := p.parseShift()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseShift() (Expr, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for p.at(ShiftLeft) || p.at(ShiftRight) {
		op := p.adv().Val
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseAdd() (Expr, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.at(tPlus) || p.at(tMinus) || p.at(tConcat) {
		op := p.adv().Val
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseMul() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.at(tStar) || p.at(tSlash) || p.at(tPercent) {
		op := p.adv().Val
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseUnary() (Expr, error) {
	// @ 错误抑制：吞掉符号，继续解析并求值后续表达式（PHP 语义：@expr 等价于 expr）
	if p.atVal("@") {
		p.adv()
		return p.parseUnary()
	}
	if p.at(tBang) {
		p.adv()
		e, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "!", Expr: e}, nil
	}
	if p.at(tMinus) {
		p.adv()
		e, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "-", Expr: e}, nil
	}
	if p.at(tPlusPlus) {
		p.adv()
		v, err := p.expect(tVar)
		if err != nil {
			return nil, err
		}
		return &AssignExpr{
			Target: &VarExpr{Name: v.Val[1:]},
			Val:    &BinaryExpr{Op: "+", Left: &VarExpr{Name: v.Val[1:]}, Right: &ScalarInt{Val: 1}},
		}, nil
	}
	if p.at(tMinusMinus) {
		p.adv()
		v, err := p.expect(tVar)
		if err != nil {
			return nil, err
		}
		return &AssignExpr{
			Target: &VarExpr{Name: v.Val[1:]},
			Val:    &BinaryExpr{Op: "-", Left: &VarExpr{Name: v.Val[1:]}, Right: &ScalarInt{Val: 1}},
		}, nil
	}
	if p.at(tStringCast) || p.at(tIntCast) || p.at(tFloatCast) || p.at(tBoolCast) || p.at(tArrayCast) || p.at(tObjectCast) {
		var kind string
		switch p.adv().Kind {
		case tStringCast:
			kind = "string"
		case tIntCast:
			kind = "int"
		case tFloatCast:
			kind = "float"
		case tBoolCast:
			kind = "bool"
		case tArrayCast:
			kind = "array"
		case tObjectCast:
			kind = "object"
		}
		e, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &CastExpr{Kind: kind, Expr: e}, nil
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() (Expr, error) {
	e, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	return p.parsePostfixFrom(e)
}

func (p *Parser) parsePostfixFrom(e Expr) (Expr, error) {
	for {
		if p.at(tLBracket) {
			p.adv()
			if p.at(tRBracket) {
				p.adv()
				e = &IndexExpr{Arr: e, Key: &ConstNull{}}
				continue
			}
			key, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			p.expect(tRBracket)
			e = &IndexExpr{Arr: e, Key: key}
			continue
		}
		if p.at(tLParen) {
			name := ""
			switch v := e.(type) {
			case *VarExpr:
				name = v.Name
			case *ScalarStr:
				name = v.Val
			case *ConstExpr:
				name = v.Name
			default:
				return nil, fmt.Errorf("parse: 不可调用的表达式 at %d", p.cur().Pos)
			}
			args, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			e = &FuncCall{Name: name, Args: args}
			continue
		}
		if p.at(tArrow) {
			p.adv()
			m, err := p.expect(tIdent)
			if err != nil {
				return nil, err
			}
			if p.at(tLParen) {
				args, err := p.parseArgs()
				if err != nil {
					return nil, err
				}
				e = &MethodCall{Receiver: e, Method: m.Val, Args: args}
			} else {
				e = &PropertyAccess{Receiver: e, Prop: m.Val}
			}
			continue
		}
		if p.at(tDoubleColon) {
			p.adv()
			m, err := p.expect(tIdent)
			if err != nil {
				return nil, err
			}
			// 判断是方法调用还是常量访问
			if p.at(tLParen) {
				args, err := p.parseArgs()
				if err != nil {
					return nil, err
				}
				class := ""
				if s, ok := e.(*ScalarStr); ok {
					class = s.Val
				} else if v, ok := e.(*VarExpr); ok {
					class = v.Name
				} else if c, ok := e.(*ConstExpr); ok {
					class = c.Name
				}
				e = &StaticCall{Class: class, Method: m.Val, Args: args}
				continue
			}
			// 常量访问：self::CONSTANT 或 ClassName::CONSTANT
			class := ""
			if s, ok := e.(*ScalarStr); ok {
				class = s.Val
			} else if v, ok := e.(*VarExpr); ok {
				class = v.Name
			} else if c, ok := e.(*ConstExpr); ok {
				class = c.Name
			}
			e = &SelfConstExpr{Class: class, Name: m.Val}
			continue
		}
		break
	}
	return e, nil
}

func (p *Parser) parseArgs() ([]Expr, error) {
	p.adv() // (
	var args []Expr
	for !p.at(tRParen) && !p.at(tEOF) {
		if p.at(tAmp) {
			p.adv()
			v, err := p.expect(tVar)
			if err != nil {
				return nil, err
			}
			args = append(args, &varRef{name: v.Val[1:]})
			if p.at(tComma) {
				p.adv()
				continue
			}
			break
		}
		// ...$var splat 展开
		if p.atVal("...") {
			p.adv()
			a, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, &SplatExpr{Expr: a})
			if p.at(tComma) {
				p.adv()
				continue
			}
			break
		}
		a, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		if p.at(tComma) {
			p.adv()
			continue
		}
		break
	}
	p.expect(tRParen)
	return args, nil
}

func (p *Parser) parsePrimary() (Expr, error) {
	t := p.cur()
	switch t.Kind {
	case tVar:
		p.adv()
		name := t.Val[1:]
		if name == "this" {
			return &ThisExpr{}, nil
		}
		return p.parseExprFromVar(name)
	case tInt:
		p.adv()
		var n int64
		fmt.Sscan(t.Val, &n)
		return &ScalarInt{Val: n}, nil
	case tFloat:
		p.adv()
		var f float64
		fmt.Sscan(t.Val, &f)
		return &ScalarFloat{Val: f}, nil
	case tDoubleStr:
		p.adv()
		// 检查是否含变量插值
		if containsInterp(t.Val) {
			return parseInterpolatedStr(t.Val), nil
		}
		return &ScalarStr{Val: unescapeStr(t.Val, true)}, nil
	case tSingleStr:
		p.adv()
		return &ScalarStr{Val: unescapeStr(t.Val, false)}, nil
	case tConstTrue:
		p.adv()
		return &ConstBool{Val: true}, nil
	case tConstFalse:
		p.adv()
		return &ConstBool{Val: false}, nil
	case tConstNull:
		p.adv()
		return &ConstNull{}, nil
	case tArray:
		return p.parseArray()
	case tLBracket:
		return p.parseShortArray()
	case tLParen:
		p.adv()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.expect(tRParen)
		return e, nil
	case tIdent:
		// 先查预定义常量
		if cv, ok := p.constsMap[t.Val]; ok {
			p.adv()
			switch cv.Kind {
			case KindInt:
				return &ScalarInt{Val: cv.Int}, nil
			case KindFloat:
				return &ScalarFloat{Val: cv.Float}, nil
			case KindString:
				return &ScalarStr{Val: cv.Str}, nil
			case KindBool:
				return &ConstBool{Val: cv.Bool}, nil
			}
		}
		// 特殊处理 __DIR__ 等魔术常量
		switch t.Val {
		case "__DIR__", "__FILE__", "__LINE__", "__FUNCTION__", "__CLASS__", "__METHOD__", "__NAMESPACE__":
			p.adv()
			return &MagicConstExpr{Name: t.Val}, nil
		}
		// 未定义常量：运行时查 e.consts（define 注册的），找不到则当字符串名
		p.adv()
		return &ConstExpr{Name: t.Val}, nil
	case tFunc:
		// 闭包 function($a) use($b) { ... }
		return p.parseClosure()
	case tNew:
		return p.parseNew()
	}
	return nil, fmt.Errorf("parse: 无法解析的表达式 at %d (%q)", t.Pos, t.Val)
}

func (p *Parser) parseClosure() (Expr, error) {
	p.adv() // function
	params, err := p.parseFuncParams()
	if err != nil {
		return nil, err
	}
	var uses []string
	var byRef []bool
	if p.at(tUse) {
		p.adv()
		p.expect(tLParen)
		for !p.at(tRParen) && !p.at(tEOF) {
			ref := false
			if p.at(tAmp) {
				p.adv()
				ref = true
			}
			v, err := p.expect(tVar)
			if err != nil {
				return nil, err
			}
			uses = append(uses, v.Val[1:])
			byRef = append(byRef, ref)
			if p.at(tComma) {
				p.adv()
				continue
			}
			break
		}
		p.expect(tRParen)
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ClosureExpr{Params: params, Uses: uses, ByRef: byRef, Body: body}, nil
}

// parseArray array( k => v, ... )
func (p *Parser) parseArray() (Expr, error) {
	p.adv() // array
	p.expect(tLParen)
	arr := &ArrayExpr{}
	for !p.at(tRParen) && !p.at(tEOF) {
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.at(tArrowFn) {
			p.adv()
			key := v
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			arr.Keys = append(arr.Keys, key)
			arr.Values = append(arr.Values, val)
		} else {
			arr.Values = append(arr.Values, v)
		}
		if p.at(tComma) {
			p.adv()
			continue
		}
		break
	}
	p.expect(tRParen)
	return arr, nil
}

// parseShortArray 短数组语法 [ k => v, ... ] 或 [ v, ... ]
func (p *Parser) parseShortArray() (Expr, error) {
	p.adv() // [
	arr := &ArrayExpr{}
	for !p.at(tRBracket) && !p.at(tEOF) {
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.at(tArrowFn) {
			p.adv()
			key := v
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			arr.Keys = append(arr.Keys, key)
			arr.Values = append(arr.Values, val)
		} else {
			arr.Values = append(arr.Values, v)
		}
		if p.at(tComma) {
			p.adv()
			continue
		}
		break
	}
	p.expect(tRBracket)
	return arr, nil
}

// parseExprFromVar 处理 $var 后续的下标链
func (p *Parser) parseExprFromVar(name string) (Expr, error) {
	e := Expr(&VarExpr{Name: name})
	for {
		if p.at(tLBracket) {
			p.adv()
			if p.at(tRBracket) {
				p.adv()
				e = &IndexExpr{Arr: e, Key: &ConstNull{}}
				continue
			}
			key, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			p.expect(tRBracket)
			e = &IndexExpr{Arr: e, Key: key}
			continue
		}
		break
	}
	return p.parsePostfixFrom(e)
}

// ---------------------------------------------------------------------------
// 双引号字符串变量插值
// ---------------------------------------------------------------------------

// containsInterp 检查双引号字符串是否含 $var 或 {$var}
func containsInterp(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '$' && i+1 < len(s) && isIdentStart(s[i+1]) {
			return true
		}
		if s[i] == '{' && i+1 < len(s) && s[i+1] == '$' {
			return true
		}
	}
	return false
}

// parseInterpolatedStr 把双引号字符串解析为 InterpolatedStr
func parseInterpolatedStr(s string) Expr {
	var parts []interface{}
	var buf []byte
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				buf = append(buf, '\n')
			case 't':
				buf = append(buf, '\t')
			case 'r':
				buf = append(buf, '\r')
			case '\\':
				buf = append(buf, '\\')
			case '"':
				buf = append(buf, '"')
			case '$':
				buf = append(buf, '$')
			default:
				buf = append(buf, '\\', s[i+1])
			}
			i += 2
			continue
		}
		// {$var...} 复杂插值
		if s[i] == '{' && i+1 < len(s) && s[i+1] == '$' {
			if len(buf) > 0 {
				parts = append(parts, string(buf))
				buf = buf[:0]
			}
			// 找到匹配的 }
			depth := 1
			j := i + 1
			for j < len(s) && depth > 0 {
				if s[j] == '{' {
					depth++
				}
				if s[j] == '}' {
					depth--
				}
				if depth > 0 {
					j++
				}
			}
			inner := s[i+1 : j] // $var 或 $arr[$key]
			// 解析内部为表达式
			toks, err := NewLexer("<?php " + inner + ";?>").Tokenize()
			if err == nil {
				p := NewParser(toks)
				e, err := p.parseExpr()
				if err == nil {
					parts = append(parts, e)
				}
			}
			i = j + 1
			continue
		}
		// $var 简单插值
		if s[i] == '$' && i+1 < len(s) && isIdentStart(s[i+1]) {
			if len(buf) > 0 {
				parts = append(parts, string(buf))
				buf = buf[:0]
			}
			j := i + 1
			for j < len(s) && isIdentRune(s[j]) {
				j++
			}
			name := s[i+1 : j]
			// 支持 $arr[$key] 形式
			if j < len(s) && s[j] == '[' {
				k := j + 1
				for k < len(s) && s[k] != ']' {
					k++
				}
				keyStr := s[j+1 : k]
				// 如果 key 以 $ 开头，解析为变量
				if len(keyStr) > 0 && keyStr[0] == '$' {
					parts = append(parts, &IndexExpr{Arr: &VarExpr{Name: name}, Key: &VarExpr{Name: keyStr[1:]}})
				} else {
					parts = append(parts, &IndexExpr{Arr: &VarExpr{Name: name}, Key: &ScalarStr{Val: keyStr}})
				}
				j = k + 1
			} else {
				parts = append(parts, &VarExpr{Name: name})
			}
			i = j
			continue
		}
		buf = append(buf, s[i])
		i++
	}
	if len(buf) > 0 {
		parts = append(parts, string(buf))
	}
	if len(parts) == 0 {
		return &ScalarStr{Val: ""}
	}
	if len(parts) == 1 {
		if s, ok := parts[0].(string); ok {
			return &ScalarStr{Val: s}
		}
	}
	return &InterpolatedStr{Parts: parts}
}

// unescapeStr 处理转义
func unescapeStr(s string, double bool) string {
	if !double {
		out := strings.NewReplacer(`\'`, "'", `\\`, `\`).Replace(s)
		return out
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'v':
				b.WriteByte('\v')
			case 'f':
				b.WriteByte('\f')
			case 'e':
				b.WriteByte(0x1b)
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case '$':
				b.WriteByte('$')
			case 'x', 'X':
				// PHP \xHH：恰好两个十六进制位
				if i+3 < len(s) {
					if hi, ok1 := unescapeHexDigit(s[i+2]); ok1 {
						if lo, ok2 := unescapeHexDigit(s[i+3]); ok2 {
							b.WriteByte(byte(hi<<4 | lo))
							i += 3 // 消耗 xHH；底部 i++ 再前进 1
							continue
						}
					}
				}
				b.WriteByte('\\')
				b.WriteByte(s[i+1])
			case '0', '1', '2', '3', '4', '5', '6', '7':
				// PHP 八进制 \0..\777（最多 3 位）
				val := 0
				k := 1
				base := i + 1
				for k <= 3 && base+k <= len(s) && s[base+k-1] >= '0' && s[base+k-1] <= '7' {
					val = val*8 + int(s[base+k-1]-'0')
					k++
				}
				b.WriteByte(byte(val))
				i += k - 1 // 跳过已消费的八进制位（配合 continue 仅执行 for 的 i++）
				continue
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i+1])
			}
			i++
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// unescapeHexDigit 解析十六进制字符（0-9 a-f A-F），非法返回 false。
func unescapeHexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// parseDeclare 解析 declare(strict_types=1); 语句（直接跳过）
func (p *Parser) parseDeclare() (Stmt, error) {
	p.adv() // declare
	if _, err := p.expect(tLParen); err != nil {
		return nil, err
	}
	// 跳过 declare 内的所有内容直到 )
	for !p.at(tRParen) && !p.at(tEOF) {
		p.adv()
	}
	p.expect(tRParen)
	p.skipSemis()
	// declare 可带 block: declare(...) { ... } 或不带 block: declare(...);
	if p.at(tLBrace) {
		p.adv()
		for !p.at(tRBrace) && !p.at(tEOF) {
			p.skipSemis()
			if p.at(tRBrace) {
				break
			}
			p.parseStmt()
		}
		p.expect(tRBrace)
	}
	return nil, nil
}

// parseTry 解析 try { ... } catch (Type $e) { ... } finally { ... }
func (p *Parser) parseTry() (Stmt, error) {
	p.adv() // try
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	var catches []CatchClause
	for p.at(tCatch) {
		p.adv() // catch
		if _, err := p.expect(tLParen); err != nil {
			return nil, err
		}
		var types []string
		// 第一个类型
		typ := ""
		if p.at(tIdent) {
			typ = p.adv().Val
		}
		// 可能是命名空间前缀 \NS\Class
		for p.atVal("\\") {
			p.adv()
			if p.at(tIdent) {
				typ += "\\" + p.adv().Val
			}
		}
		types = append(types, typ)
		// 多个类型用 | 分隔：catch (ExceptionA | ExceptionB $e)
		for p.at(tPipe) {
			p.adv()
			typ2 := ""
			if p.at(tIdent) {
				typ2 = p.adv().Val
			}
			for p.atVal("\\") {
				p.adv()
				if p.at(tIdent) {
					typ2 += "\\" + p.adv().Val
				}
			}
			types = append(types, typ2)
		}
		var varName string
		if p.at(tVar) {
			varName = p.adv().Val[1:]
		}
		p.expect(tRParen)
		catchBody, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		catches = append(catches, CatchClause{Types: types, Var: varName, Body: catchBody})
	}
	var finally []Stmt
	if p.at(tFinally) {
		p.adv()
		finally, err = p.parseBlock()
		if err != nil {
			return nil, err
		}
	}
	return &TryStmt{Body: body, Catches: catches, Finally: finally}, nil
}

// parseNew 解析 new ClassName(args...)
func (p *Parser) parseNew() (Expr, error) {
	p.adv() // new
	// 类名
	className := ""
	if p.at(tIdent) {
		className = p.adv().Val
	}
	for p.atVal("\\") {
		p.adv()
		if p.at(tIdent) {
			className += "\\" + p.adv().Val
		}
	}
	var args []Expr
	if p.at(tLParen) {
		args, _ = p.parseArgs()
	}
	return &NewExpr{Class: className, Args: args}, nil
}

// parseUnset 解析 unset($var) / unset($arr[$key]) 语句
func (p *Parser) parseUnset() (Stmt, error) {
	p.adv() // unset
	if _, err := p.expect(tLParen); err != nil {
		return nil, err
	}
	var args []Expr
	for !p.at(tRParen) && !p.at(tEOF) {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, e)
		if p.at(tComma) {
			p.adv()
			continue
		}
		break
	}
	p.expect(tRParen)
	p.skipSemis()
	return &UnsetStmt{Args: args}, nil
}

// parseClass 解析 class 定义
func (p *Parser) parseClass() (Stmt, error) {
	p.adv() // class
	// 可选 abstract/final 已被 lexer 当 tIdent 跳过，这里跳过修饰符
	for p.atVal("abstract") || p.atVal("final") {
		p.adv()
	}
	name, err := p.expect(tIdent)
	if err != nil {
		return nil, err
	}
	// 跳过 extends/implements
	if p.atVal("extends") {
		p.adv()
		for p.at(tIdent) || p.atVal("\\") {
			p.adv()
		}
	}
	if p.atVal("implements") {
		p.adv()
		for p.at(tIdent) || p.atVal("\\") || p.at(tComma) {
			p.adv()
		}
	}
	if _, err := p.expect(tLBrace); err != nil {
		return nil, err
	}
	cls := &ClassDecl{Name: name.Val}
	for !p.at(tRBrace) && !p.at(tEOF) {
		p.skipSemis()
		if p.at(tRBrace) {
			break
		}
		// 可见性修饰符
		visib := "public"
		if p.atVal("public") || p.atVal("private") || p.atVal("protected") {
			visib = p.adv().Val
		}
		// static 修饰符（跳过）
		if p.atVal("static") {
			p.adv()
		}
		// 常量
		if p.at(tConst) {
			p.adv()
			cn, err := p.expect(tIdent)
			if err != nil {
				return nil, err
			}
			p.expect(tEquals)
			cv, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			cls.Consts = append(cls.Consts, ConstStmt{Name: cn.Val, Val: cv})
			p.skipSemis()
			continue
		}
		// 方法
		if p.at(tFunc) {
			fn, err := p.parseFunc()
			if err != nil {
				return nil, err
			}
			cls.Methods = append(cls.Methods, fn.(*FuncDecl))
			continue
		}
		// 属性
		if p.at(tVar) {
			propName := p.adv().Val[1:]
			var def Expr
			if p.at(tEquals) {
				p.adv()
				def, err = p.parseExpr()
				if err != nil {
					return nil, err
				}
			}
			cls.Properties = append(cls.Properties, ClassProperty{Name: propName, Default: def, Visib: visib})
			p.skipSemis()
			continue
		}
		// 跳过未知内容
		p.adv()
	}
	p.expect(tRBrace)
	return cls, nil
}
