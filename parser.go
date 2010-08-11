package gotcl

import (
	"os"
	"bytes"
	"unicode"
)

type RuneSource interface {
	ReadRune() (int, int, os.Error)
}

type parser struct {
	data   RuneSource
	tmpbuf *bytes.Buffer
	ch     int
}

func newParser(input RuneSource) *parser {
	p := &parser{data: input, tmpbuf: bytes.NewBuffer(make([]byte, 0, 1024))}
	p.advance()
	return p
}

func issepspace(c int) bool { return c == '\t' || c == ' ' }
func isvarword(c int) bool {
	return unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_'
}

func (p *parser) fail(s string) {
	panic(os.NewError(s))
}

func (p *parser) advance() (result int) {
	if p.ch == -1 {
		p.fail("unexpected EOF")
	}
	result = p.ch
	r, _, e := p.data.ReadRune()
	if e != nil {
		if e != os.EOF {
			p.fail(e.String())
		}
		p.ch = -1
	} else {
		p.ch = r
	}
	return
}

func (p *parser) consumeWhile1(fn func(int) bool, desc string) string {
	p.tmpbuf.Reset()
	for p.ch != -1 && fn(p.ch) {
		p.tmpbuf.WriteRune(p.advance())
	}
	res := p.tmpbuf.String()
	if res == "" {
		p.expectFailed(desc, p.ch)
	}
	return res
}

func (p *parser) expectFailed(expected string, ch int) {
	got := "EOF"
	if ch != -1 {
		got = string(ch)
	}
	p.fail("Expected " + expected + ", got '" + got + "'")
}

func (p *parser) consumeRune(rune int) {
	if p.ch != rune {
		p.expectFailed("'"+string(rune)+"'", p.ch)
	}
	p.advance()
}

func (p *parser) eatSpace() {
	for p.ch != -1 && unicode.IsSpace(p.ch) {
		p.advance()
	}
}

func (p *parser) eatWhile(fn func(int) bool) {
	for p.ch != -1 && fn(p.ch) {
		p.advance()
	}
}

func isword(c int) bool {
	switch c {
	case '[', ']', ';', '$', '"':
		return false
	}
	return !unicode.IsSpace(c)
}
func (p *parser) parseSimpleWordTil(til int) *tliteral {
	p.tmpbuf.Reset()
	prev_esc := false
	for p.ch != -1 && p.ch != til {
		if p.ch == '\\' && !prev_esc {
			prev_esc = true
			p.advance()
		} else if prev_esc || isword(p.ch) {
			c := p.advance()
			if prev_esc {
				p.tmpbuf.WriteString(escaped(c))
				prev_esc = false
			} else {
				p.tmpbuf.WriteRune(c)
			}
		} else {
			break
		}
	}
	res := p.tmpbuf.String()
	if len(res) == 0 {
		p.expectFailed("word", p.ch)
	}
	return &tliteral{strval: res}
}

func (p *parser) parseSubcommand() *subcommand {
	p.consumeRune('[')
	res := make([]TclTok, 0, 16)
	p.eatWhile(issepspace)
	for p.ch != ']' {
		appendttok(&res, p.parseToken())
		p.eatWhile(issepspace)
	}
	p.consumeRune(']')
	return &subcommand{cmd: Command{res}}
}

func (p *parser) parseBlockData() string {
	p.consumeRune('{')
	nest := 0
	p.tmpbuf.Reset()
	for {
		switch p.ch {
		case '\\':
			p.tmpbuf.WriteRune(p.advance())
		case '{':
			nest++
		case '}':
			if nest == 0 {
				p.advance()
				return p.tmpbuf.String()
			}
			nest--
		case -1:
			p.fail("unclosed block")
		}
		p.tmpbuf.WriteRune(p.advance())
	}
	return "" // never happens.
}

func (p *parser) hasExtraChars() bool {
	return p.ch != -1 && !unicode.IsSpace(p.ch) && p.ch != '}' && p.ch != ']'
}

func (p *parser) checkForExtraChars() {
	if p.hasExtraChars() {
		p.fail("extra characters after close-brace")
	}
}

func (p *parser) parseBlock() *block {
	bd := p.parseBlockData()
	p.checkForExtraChars()
	return &block{strval: bd}
}

func (p *parser) parseBlockOrExpand() TclTok {
	bd := p.parseBlockData()
	if bd == "*" && p.hasExtraChars() {
		return &expandTok{p.parseToken()}
	}
	p.checkForExtraChars()
	return &block{strval: bd}
}

func (p *parser) parseVariable() varRef {
	p.consumeRune('$')
	return p.parseVarRef()
}

func (p *parser) parseVarRef() varRef {
	if p.ch == '{' {
		return toVarRef(p.parseBlockData())
	}
	global := false
	if p.ch == ':' {
		p.advance()
		p.consumeRune(':')
		global = true
	}
	name := p.consumeWhile1(isvarword, "variable name")
	var ind TclTok
	if p.ch == '(' {
		p.advance()
		ind = p.parseTokenTil(')')
		p.consumeRune(')')
	}
	return varRef{is_global: global, name: name, arrind: ind}
}

func appendtok(tx *[]littok, t littok) {
	oldlen := len(*tx)
	if oldlen == cap(*tx) {
		newcap := 1 + (cap(*tx)+1)*2
		newsl := make([]littok, oldlen, newcap)
		copy(newsl, *tx)
		*tx = newsl
	}
	*tx = (*tx)[0 : oldlen+1]
	(*tx)[oldlen] = t
}

var escMap = map[int]string{
	'n': "\n", 't': "\t", 'a': "\a", 'v': "\v", 'r': "\r"}

func escaped(r int) string {
	if v, ok := escMap[r]; ok {
		return v
	}
	return string(r)
}

func (p *parser) parseStringLit() strlit {
	p.consumeRune('"')
	var accum bytes.Buffer
	toks := make([]littok, 0, 8)
	record_accum := func() {
		if accum.Len() != 0 {
			appendtok(&toks, littok{kind: kRaw, value: accum.String()})
			accum.Reset()
		}
	}
	for {
		switch p.ch {
		case '"':
			record_accum()
			p.advance()
			return strlit{toks: toks}
		case '$':
			record_accum()
			vref := p.parseVariable()
			appendtok(&toks, littok{kind: kVar, varref: &vref})
		case '[':
			record_accum()
			subcmd := p.parseSubcommand()
			appendtok(&toks, littok{kind: kSubcmd, subcmd: subcmd})
		case '\\':
			p.advance()
			accum.WriteString(escaped(p.advance()))
		case -1:
			p.fail("Unexpected EOF, wanted \"")
		default:
			accum.WriteRune(p.advance())
		}
	}
	panic("unreachable")
}

func isEol(ch int) bool {
	switch ch {
	case -1, ';', '\n':
		return true
	}
	return false
}

func (p *parser) eatExtra() {
	p.eatSpace()
	for p.ch == ';' {
		p.advance()
		p.eatSpace()
	}
}

func (p *parser) parseComment() {
	p.consumeRune('#')
	p.eatWhile(func(c int) bool { return c != '\n' })
}

func appendcmd(tx *[]Command, t Command) {
	oldlen := len(*tx)
	if oldlen == cap(*tx) {
		newcap := 1 + (cap(*tx)+1)*2
		newsl := make([]Command, oldlen, newcap)
		copy(newsl, *tx)
		*tx = newsl
	}
	*tx = (*tx)[0 : oldlen+1]
	(*tx)[oldlen] = t
}

func (p *parser) parseCommands() []Command {
	res := make([]Command, 0, 128)
	p.eatSpace()
	for p.ch != -1 {
		if p.ch == '#' {
			p.parseComment()
		} else {
			appendcmd(&res, p.parseCommand())
		}
		p.eatExtra()
	}
	return res
}

func appendttok(tx *[]TclTok, t TclTok) {
	oldlen := len(*tx)
	if oldlen == cap(*tx) {
		newsl := make([]TclTok, oldlen, (cap(*tx)+1)*2)
		copy(newsl, *tx)
		*tx = newsl
	}
	*tx = (*tx)[0 : oldlen+1]
	(*tx)[oldlen] = t
}

func (p *parser) parseList() []TclTok {
	res := make([]TclTok, 0, 32)
	for p.ch != -1 {
		p.eatSpace()
		if p.ch == -1 {
			break
		}
		appendttok(&res, p.parseListToken())
	}
	return res
}

func notspace(c int) bool { return !unicode.IsSpace(c) }

func (p *parser) parseListToken() TclTok {
	p.eatSpace()
	switch p.ch {
	case '{':
		return &tliteral{strval: p.parseBlockData()}
	case '"':
		return p.parseStringLit()
	}
	return &tliteral{strval: p.consumeWhile1(notspace, "word")}
}

func (p *parser) parseCommand() Command {
	res := make([]TclTok, 0, 16)
	appendttok(&res, p.parseToken())
	p.eatWhile(issepspace)
	for !isEol(p.ch) {
		appendttok(&res, p.parseToken())
		p.eatWhile(issepspace)
	}
	return Command{res}
}

func (p *parser) parseToken() TclTok {
	return p.parseTokenTil(-1)
}

func (p *parser) parseTokenTil(til int) TclTok {
	switch p.ch {
	case '[':
		return p.parseSubcommand()
	case '{':
		return p.parseBlockOrExpand()
	case '"':
		return p.parseStringLit()
	case '$':
		return p.parseVariable()
	}
	return p.parseSimpleWordTil(til)
}

func setError(err *os.Error) {
	if e := recover(); e != nil {
		*err = e.(os.Error)
	}
}

func ParseList(in RuneSource) (items []TclTok, err os.Error) {
	p := newParser(in)
	defer setError(&err)
	items = p.parseList()
	return
}

func ParseCommands(in RuneSource) (cmds []Command, err os.Error) {
	p := newParser(in)
	defer setError(&err)
	cmds = p.parseCommands()
	return
}
