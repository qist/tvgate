package phpgo

import (
	"fmt"
	"strings"
	"unicode"
)

// Tok 词法记号
type Tok struct {
	Kind tokKind
	Val  string
	Pos  int
}

type tokKind int

const (
	tEOF tokKind = iota
	tVar       // $name
	tIdent     // functionName / constant
	tInt
	tFloat
	tStr       // "..." 或 '...'  （单引号/双引号区分在 parser）
	tSingleStr
	tDoubleStr
	tLParen
	tRParen
	tLBrace
	tRBrace
	tLBracket
	tRBracket
	tSemi
	tComma
	tArrow     // ->
	tDoubleArrow // ->
	tEquals    // =
	tEqEq      // ==
	tEqEqEq    // ===
	tNotEq     // !=
	tNotEqEq   // !==
	tConcat    // .
	tPlus
	tMinus
	tStar
	tSlash
	tPercent   // %
	tDot       // 句点（字符串拼接），与 tConcat 同义
	tArrowFn   // =>
	tColon
	tDoubleColon // ::
	tLT
	tGT
	tLE         // <=
	tGE         // >=
	tQuestion  // ?
	tNullCoalesce // ??
	tPlusEq
	tMinusEq    // -=
	tStarEq     // *=
	tSlashEq    // /=
	tConcatEq  // .=
	tAmp       // &
	tAmpAmp    // &&
	tPipe      // |
	tPipePipe  // ||
	tBang       // !
	tPlusPlus  // ++
	tMinusMinus // --
	tFunc
	tIf
	tElse
	tElseIf
	tEcho
	tReturn
	tExit
	tDie
	tTrue
	tFalse
	tNull
	tArray     // array(
	tForeach
	tFor
	tWhile
	tDo
	tSwitch
	tCase
	tDefault
	tBreak
	tContinue
	tGlobal
	tAs
	tUse
	tList
	tPrint
	tConst
	tDeclare
	tTry
	tCatch
	tThrow
	tFinally
	tNew
	tClass
	tConstTrue
	tConstFalse
	tConstNull
	tStringCast // (string), (int), (float), (bool), (array)
	tIntCast
	tFloatCast
	tBoolCast
	tArrayCast
)

// Lexer 把 PHP 源码切成记号
type Lexer struct {
	src string
	pos int
}

// NewLexer ...
func NewLexer(src string) *Lexer { return &Lexer{src: src} }

func (l *Lexer) peek() byte {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *Lexer) peekAt(off int) byte {
	if l.pos+off >= len(l.src) || l.pos+off < 0 {
		return 0
	}
	return l.src[l.pos+off]
}

func (l *Lexer) next() byte {
	c := l.peek()
	l.pos++
	return c
}

func isIdentRune(r byte) bool {
	return unicode.IsLetter(rune(r)) || unicode.IsDigit(rune(r)) || r == '_'
}

func isIdentStart(r byte) bool {
	return unicode.IsLetter(rune(r)) || r == '_'
}

// Tokenize 分词（忽略 PHP 标签外内容与注释）
func (l *Lexer) Tokenize() ([]Tok, error) {
	var toks []Tok
	inPHP := false
	for l.pos < len(l.src) {
		c := l.peek()
		// 处理 PHP 开闭标签
		if !inPHP && strings.HasPrefix(l.src[l.pos:], "<?php") {
			l.pos += 5
			inPHP = true
			continue
		}
		if !inPHP && strings.HasPrefix(l.src[l.pos:], "<?=") {
			l.pos += 3
			inPHP = true
			// <?= 等价于 <?php echo
			toks = append(toks, Tok{tEcho, "echo", l.pos})
			continue
		}
		if !inPHP && strings.HasPrefix(l.src[l.pos:], "<?") {
			l.pos += 2
			inPHP = true
			continue
		}
		if inPHP && strings.HasPrefix(l.src[l.pos:], "?>") {
			l.pos += 2
			inPHP = false
			// ?> 后面的一个换行符被 PHP 吞掉
			if l.peek() == '\n' {
				l.pos++
			}
			continue
		}
		if !inPHP {
			l.pos++
			continue
		}
		// 跳过空白
		if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
			l.pos++
			continue
		}
		// 注释
		if c == '#' {
			for l.peek() != '\n' && l.peek() != 0 {
				l.pos++
			}
			continue
		}
		if c == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/' {
			for l.peek() != '\n' && l.peek() != 0 {
				l.pos++
			}
			continue
		}
		if c == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '*' {
			l.pos += 2
			for l.pos+1 < len(l.src) && !(l.src[l.pos] == '*' && l.src[l.pos+1] == '/') {
				l.pos++
			}
			l.pos += 2
			continue
		}
		start := l.pos
		switch {
		case c == '$':
			l.pos++
			for isIdentRune(l.peek()) {
				l.pos++
			}
			toks = append(toks, Tok{tVar, l.src[start:l.pos], start})
		case isIdentStart(c):
			for isIdentRune(l.peek()) {
				l.pos++
			}
			word := l.src[start:l.pos]
			lower := strings.ToLower(word)
			switch lower {
			case "function":
				toks = append(toks, Tok{tFunc, word, start})
			case "if":
				toks = append(toks, Tok{tIf, word, start})
			case "else":
				toks = append(toks, Tok{tElse, word, start})
			case "elseif":
				toks = append(toks, Tok{tElseIf, word, start})
			case "echo":
				toks = append(toks, Tok{tEcho, word, start})
			case "return":
				toks = append(toks, Tok{tReturn, word, start})
			case "exit":
				toks = append(toks, Tok{tExit, word, start})
			case "die":
				toks = append(toks, Tok{tDie, word, start})
			case "true":
				toks = append(toks, Tok{tConstTrue, word, start})
			case "false":
				toks = append(toks, Tok{tConstFalse, word, start})
			case "null":
				toks = append(toks, Tok{tConstNull, word, start})
			case "array":
				toks = append(toks, Tok{tArray, word, start})
			case "foreach":
				toks = append(toks, Tok{tForeach, word, start})
			case "for":
				toks = append(toks, Tok{tFor, word, start})
			case "while":
				toks = append(toks, Tok{tWhile, word, start})
			case "do":
				toks = append(toks, Tok{tDo, word, start})
			case "switch":
				toks = append(toks, Tok{tSwitch, word, start})
			case "case":
				toks = append(toks, Tok{tCase, word, start})
			case "default":
				toks = append(toks, Tok{tDefault, word, start})
			case "break":
				toks = append(toks, Tok{tBreak, word, start})
			case "continue":
				toks = append(toks, Tok{tContinue, word, start})
			case "global":
				toks = append(toks, Tok{tGlobal, word, start})
			case "as":
				toks = append(toks, Tok{tAs, word, start})
			case "use":
				toks = append(toks, Tok{tUse, word, start})
			case "list":
				toks = append(toks, Tok{tList, word, start})
			case "print":
				toks = append(toks, Tok{tPrint, word, start})
		case "const":
			toks = append(toks, Tok{tConst, word, start})
		case "declare":
			toks = append(toks, Tok{tDeclare, word, start})
		case "try":
			toks = append(toks, Tok{tTry, word, start})
		case "catch":
			toks = append(toks, Tok{tCatch, word, start})
		case "throw":
			toks = append(toks, Tok{tThrow, word, start})
		case "finally":
			toks = append(toks, Tok{tFinally, word, start})
		case "new":
			toks = append(toks, Tok{tNew, word, start})
		case "class":
			toks = append(toks, Tok{tClass, word, start})
		default:
				toks = append(toks, Tok{tIdent, word, start})
			}
		case unicode.IsDigit(rune(c)) || (c == '.' && unicode.IsDigit(rune(l.peekAt(1)))):
			isFloat := false
			for unicode.IsDigit(rune(l.peek())) {
				l.pos++
			}
			if l.peek() == '.' {
				isFloat = true
				l.pos++
				for unicode.IsDigit(rune(l.peek())) {
					l.pos++
				}
			}
			if l.peek() == 'e' || l.peek() == 'E' {
				isFloat = true
				l.pos++
				if l.peek() == '+' || l.peek() == '-' {
					l.pos++
				}
				for unicode.IsDigit(rune(l.peek())) {
					l.pos++
				}
			}
			s := l.src[start:l.pos]
			if isFloat {
				toks = append(toks, Tok{tFloat, s, start})
			} else {
				toks = append(toks, Tok{tInt, s, start})
			}
		case c == '"':
			l.pos++
			for l.peek() != '"' && l.peek() != 0 {
				if l.peek() == '\\' {
					l.pos += 2
				} else {
					l.pos++
				}
			}
			l.pos++ // 跳过闭引号
			toks = append(toks, Tok{tDoubleStr, l.src[start+1 : l.pos-1], start})
		case c == '\'':
			l.pos++
			for l.peek() != '\'' && l.peek() != 0 {
				if l.peek() == '\\' {
					l.pos += 2
				} else {
					l.pos++
				}
			}
			l.pos++
			toks = append(toks, Tok{tSingleStr, l.src[start+1 : l.pos-1], start})
		case c == '(':
			// 检测类型转换 (int), (integer), (float), (double), (bool), (string), (array)
			rest := l.src[l.pos+1:]
			trim := strings.TrimLeft(rest, " \t")
			lower := strings.ToLower(trim)
			castKind := tokKind(0)
			if strings.HasPrefix(lower, "int)") {
				castKind = tIntCast
			} else if strings.HasPrefix(lower, "integer)") {
				castKind = tIntCast
			} else if strings.HasPrefix(lower, "float)") {
				castKind = tFloatCast
			} else if strings.HasPrefix(lower, "double)") {
				castKind = tFloatCast
			} else if strings.HasPrefix(lower, "bool)") {
				castKind = tBoolCast
			} else if strings.HasPrefix(lower, "boolean)") {
				castKind = tBoolCast
			} else if strings.HasPrefix(lower, "string)") {
				castKind = tStringCast
			} else if strings.HasPrefix(lower, "array)") {
				castKind = tArrayCast
			}
			if castKind > 0 {
				// 跳过 ( + 内容 + )
				// 计算 rest 中的前缀长度（含空格）
				prefix := l.src[l.pos+1:]
				// 找到 ')' 的位置
				closeIdx := strings.IndexByte(prefix, ')')
				if closeIdx >= 0 {
					l.pos += 1 + closeIdx + 1
					toks = append(toks, Tok{castKind, "", start})
					continue
				}
			}
			l.pos++
			toks = append(toks, Tok{tLParen, "(", start})
		case c == ')':
			l.pos++
			toks = append(toks, Tok{tRParen, ")", start})
		case c == '{':
			l.pos++
			toks = append(toks, Tok{tLBrace, "{", start})
		case c == '}':
			l.pos++
			toks = append(toks, Tok{tRBrace, "}", start})
		case c == '[':
			l.pos++
			toks = append(toks, Tok{tLBracket, "[", start})
		case c == ']':
			l.pos++
			toks = append(toks, Tok{tRBracket, "]", start})
		case c == ';':
			l.pos++
			toks = append(toks, Tok{tSemi, ";", start})
		case c == ',':
			l.pos++
			toks = append(toks, Tok{tComma, ",", start})
		case c == '=':
			l.pos++
			if l.peek() == '=' {
				l.pos++
				if l.peek() == '=' {
					l.pos++
					toks = append(toks, Tok{tEqEqEq, "===", start})
				} else {
					toks = append(toks, Tok{tEqEq, "==", start})
				}
			} else if l.peek() == '>' {
				l.pos++
				toks = append(toks, Tok{tArrowFn, "=>", start})
			} else {
				toks = append(toks, Tok{tEquals, "=", start})
			}
		case c == '!':
			l.pos++
			if l.peek() == '=' {
				l.pos++
				if l.peek() == '=' {
					l.pos++
					toks = append(toks, Tok{tNotEqEq, "!==", start})
				} else {
					toks = append(toks, Tok{tNotEq, "!=", start})
				}
			} else {
				toks = append(toks, Tok{tBang, "!", start})
			}
		case c == '.':
			l.pos++
			if l.peek() == '=' {
				l.pos++
				toks = append(toks, Tok{tConcatEq, ".=", start})
			} else if l.peek() == '.' {
				l.pos++
				toks = append(toks, Tok{tConcat, "..", start}) // 变参函数 ...
			} else {
				toks = append(toks, Tok{tConcat, ".", start})
			}
		case c == '+':
			l.pos++
			if l.peek() == '=' {
				l.pos++
				toks = append(toks, Tok{tPlusEq, "+=", start})
			} else if l.peek() == '+' {
				l.pos++
				toks = append(toks, Tok{tPlusPlus, "++", start})
			} else {
				toks = append(toks, Tok{tPlus, "+", start})
			}
		case c == '-':
			l.pos++
			if l.peek() == '=' {
				l.pos++
				toks = append(toks, Tok{tMinusEq, "-=", start})
			} else if l.peek() == '-' {
				l.pos++
				toks = append(toks, Tok{tMinusMinus, "--", start})
			} else if l.peek() == '>' {
				l.pos++
				toks = append(toks, Tok{tArrow, "->", start})
			} else {
				toks = append(toks, Tok{tMinus, "-", start})
			}
		case c == '*':
			l.pos++
			if l.peek() == '=' {
				l.pos++
				toks = append(toks, Tok{tStarEq, "*=", start})
			} else {
				toks = append(toks, Tok{tStar, "*", start})
			}
		case c == '/':
			l.pos++
			if l.peek() == '=' {
				l.pos++
				toks = append(toks, Tok{tSlashEq, "/=", start})
			} else {
				toks = append(toks, Tok{tSlash, "/", start})
			}
		case c == '%':
			l.pos++
			toks = append(toks, Tok{tPercent, "%", start})
		case c == ':':
			l.pos++
			if l.peek() == ':' {
				l.pos++
				toks = append(toks, Tok{tDoubleColon, "::", start})
			} else {
				toks = append(toks, Tok{tColon, ":", start})
			}
		case c == '<':
			l.pos++
			if l.peek() == '=' {
				l.pos++
				toks = append(toks, Tok{tLE, "<=", start})
			} else if l.peek() == '>' {
				l.pos++
				toks = append(toks, Tok{tNotEq, "<>", start}) // PHP 的 <> 也是不等
			} else {
				toks = append(toks, Tok{tLT, "<", start})
			}
		case c == '>':
			l.pos++
			if l.peek() == '=' {
				l.pos++
				toks = append(toks, Tok{tGE, ">=", start})
			} else {
				toks = append(toks, Tok{tGT, ">", start})
			}
		case c == '?':
			l.pos++
			if l.peek() == '?' {
				l.pos++
				toks = append(toks, Tok{tNullCoalesce, "??", start})
			} else {
				toks = append(toks, Tok{tQuestion, "?", start})
			}
		case c == '&':
			l.pos++
			if l.peek() == '&' {
				l.pos++
				toks = append(toks, Tok{tAmpAmp, "&&", start})
			} else {
				toks = append(toks, Tok{tAmp, "&", start})
			}
		case c == '|':
			l.pos++
			if l.peek() == '|' {
				l.pos++
				toks = append(toks, Tok{tPipePipe, "||", start})
			} else {
				toks = append(toks, Tok{tPipe, "|", start})
			}
		case c == '~':
			l.pos++
			toks = append(toks, Tok{tIdent, "~", start})
		case c == '@':
			l.pos++
			toks = append(toks, Tok{tIdent, "@", start}) // 错误抑制符，parser 跳过
		default:
			return nil, fmt.Errorf("lexer: 无法识别字符 %q at %d", c, start)
		}
	}
	toks = append(toks, Tok{tEOF, "", l.pos})
	return toks, nil
}
