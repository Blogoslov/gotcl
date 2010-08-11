package gotcl

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
)


type notExpand struct{}

func (ne notExpand) IsExpand() bool {
	return false
}

type tliteral struct {
	notExpand
	strval string
	tval   *TclObj
}

func (l *tliteral) String() string { return l.strval }
func (l *tliteral) Eval(i *Interp) TclStatus {
	if l.tval == nil {
		l.tval = FromStr(l.strval)
	}
	i.retval = l.tval
	return kTclOK
}

type subcommand struct {
	notExpand
	cmd Command
}

func (s *subcommand) String() string { return "[" + s.cmd.String() + "]" }
func (s *subcommand) Eval(i *Interp) TclStatus {
	return i.evalCmd(s.cmd)
}

type block struct {
	notExpand
	strval string
	tval   *TclObj
}

func (b *block) String() string { return "{" + b.strval + "}" }

func (b *block) Eval(i *Interp) TclStatus {
	if b.tval == nil {
		b.tval = FromStr(b.strval)
	}
	return i.Return(b.tval)
}

type expandTok struct {
	subject TclTok
}

func (e *expandTok) IsExpand() bool {
	return true
}

func (e *expandTok) Eval(i *Interp) TclStatus {
	return e.subject.Eval(i)
}

func (e *expandTok) String() string {
	return "{*}" + e.subject.String()
}

type strlit struct {
	notExpand
	toks []littok
}

func (t strlit) String() string {
	res := bytes.NewBuffer(nil)
	res.WriteString(`"`)
	for _, tok := range t.toks {
		if tok.kind == kRaw {
			res.WriteString(tok.value)
		} else if tok.kind == kVar {
			res.WriteString(tok.varref.String())
		} else if tok.kind == kSubcmd {
			res.WriteString(tok.subcmd.String())
		}
	}
	res.WriteString(`"`)
	return res.String()
}

func (t strlit) Eval(i *Interp) TclStatus {
	var res bytes.Buffer
	for _, tok := range t.toks {
		s, rc := tok.EvalStr(i)
		if rc != kTclOK {
			return rc
		}
		res.WriteString(s)
	}
	return i.Return(FromStr(res.String()))
}

type varRef struct {
	notExpand
	is_global bool
	name      string
	arrind    TclTok
}

func (v varRef) Eval(i *Interp) TclStatus {
	x, e := i.GetVar(v)
	if e != nil {
		return i.Fail(e)
	}
	return i.Return(x)
}

func (v varRef) String() string {
	str := v.name
	if v.is_global {
		str = "::" + str
	}
	return "$" + str
}

func toVarRef(s string) varRef {
	global := false
	if strings.HasPrefix(s, "::") {
		s = s[2:]
		global = true
	}
	if s[len(s)-1] == ')' {
		ri := strings.IndexRune(s, '(')
		if ri > 0 {
			ind := &tliteral{strval: s[ri : len(s)-1]}
			s = s[0:ri]
			return varRef{name: s, is_global: global, arrind: ind}
		}
	}
	return varRef{name: s, is_global: global}
}

type Command struct {
	words []TclTok
}

func (c *Command) String() string {
	result := ""
	first := true
	for _, w := range c.words {
		if first {
			first = false
		} else {
			result += " "
		}
		result += w.String()
	}
	return result
}

type TclTok interface {
	String() string
	Eval(i *Interp) TclStatus
	IsExpand() bool
}

const (
	kRaw = iota
	kVar
	kSubcmd
)

type littok struct {
	kind   int
	value  string
	varref *varRef
	subcmd *subcommand
}

func (lt *littok) EvalStr(i *Interp) (string, TclStatus) {
	switch lt.kind {
	case kRaw:
		return lt.value, kTclOK
	case kVar:
		rc := lt.varref.Eval(i)
		return i.retval.AsString(), rc
	case kSubcmd:
		rc := lt.subcmd.Eval(i)
		return i.retval.AsString(), rc
	}
	panic("unrecognized kind")
}

type TclStatus int

const (
	kTclOK TclStatus = iota
	kTclErr
	kTclReturn
	kTclBreak
	kTclContinue
)

type framelink struct {
	frame *stackframe
	name  string
}

type varEntry struct {
	obj  *TclObj
	link *framelink
}

type VarMap map[string]*varEntry

type stackframe struct {
	vars VarMap
	next *stackframe
}

func newstackframe(tail *stackframe) *stackframe {
	return &stackframe{make(VarMap), tail}
}
func (s *stackframe) up() *stackframe { return s.next }

type Interp struct {
	cmds   map[string]TclCmd
	chans  map[string]interface{}
	frame  *stackframe
	retval *TclObj
	err    os.Error
}

func (i *Interp) Return(val *TclObj) TclStatus {
	i.retval = val
	return kTclOK
}

func (i *Interp) Fail(err os.Error) TclStatus {
	i.err = err
	return kTclErr
}

func (i *Interp) FailStr(msg string) TclStatus {
	return i.Fail(os.NewError(msg))
}

type TclObj struct {
	value      *string
	intval     int
	has_intval bool
	listval    []*TclObj
	cmdsval    []Command
	vrefval    *varRef
	exprval    eterm
}


func (t *TclObj) AsString() string {
	if t.value == nil {
		if t.has_intval {
			v := strconv.Itoa(t.intval)
			t.value = &v
		} else if t.listval != nil {
			var str bytes.Buffer
			for ind, i := range t.listval {
				if ind != 0 {
					str.WriteString(" ")
				}
				sv := i.AsString()
				should_bracket := strings.IndexAny(sv, " \t\n\v") != -1 || len(sv) == 0
				if should_bracket {
					str.WriteString("{")
				}
				str.WriteString(sv)
				if should_bracket {
					str.WriteString("}")
				}
			}
			ss := str.String()
			t.value = &ss
		} else {
			panic("unable to stringify TclObj")
		}
	}
	return *t.value
}

func (t *TclObj) AsInt() (int, os.Error) {
	if !t.has_intval {
		v, e := strconv.Atoi(*t.value)
		if e != nil {
			return 0, os.NewError("expected integer but got \"" + *t.value + "\"")
		}
		t.has_intval = true
		t.intval = v
	}
	return t.intval, nil
}

func (t *TclObj) AsCmds() ([]Command, os.Error) {
	if t.cmdsval == nil {
		c, e := ParseCommands(strings.NewReader(t.AsString()))
		if e != nil {
			return nil, e
		}
		t.cmdsval = c
	}
	return t.cmdsval, nil
}

func (t *TclObj) AsBool() bool {
	iv, err := t.AsInt()
	if err != nil {
		s := t.AsString()
		return s != "false" && s != "no"
	}
	return iv != 0
}

func (t *TclObj) asVarRef() varRef {
	if t.vrefval == nil {
		vr := toVarRef(t.AsString())
		t.vrefval = &vr
	}
	return *t.vrefval
}

func FromStr(s string) *TclObj {
	return &TclObj{value: &s}
}

var kTrue, kFalse *TclObj
var smallInts [256]*TclObj

func init() {
	for i := range smallInts {
		smallInts[i] = &TclObj{intval: i, has_intval: true}
	}
	kTrue = FromInt(1)
	kFalse = FromInt(0)
}

func FromInt(i int) *TclObj {
	if i >= 0 && i < len(smallInts) {
		return smallInts[i]
	}
	return &TclObj{intval: i, has_intval: true}
}

func FromList(l []string) *TclObj {
	vl := make([]*TclObj, len(l))
	for i, s := range l {
		vl[i] = FromStr(s)
	}
	return fromList(vl)
}

var kNil = FromStr("")

func FromBool(b bool) *TclObj {
	if b {
		return kTrue
	}
	return kFalse
}

func fromList(items []*TclObj) *TclObj { return &TclObj{listval: items} }

func (t *TclObj) AsList() ([]*TclObj, os.Error) {
	if t.listval == nil {
		var e os.Error
		t.listval, e = parseList(t.AsString())
		if e != nil {
			return nil, e
		}
	}
	return t.listval, nil
}

func (t *TclObj) asExpr() (eterm, os.Error) {
	if t.exprval == nil {
		ev, err := ParseExpr(strings.NewReader(t.AsString()))
		if err != nil {
			return nil, err
		}
		t.exprval = ev
	}
	return t.exprval, nil
}

func parseList(txt string) ([]*TclObj, os.Error) {
	lst, err := ParseList(strings.NewReader(txt))
	if err != nil {
		return nil, err
	}
	result := make([]*TclObj, len(lst))
	for i, li := range lst {
		result[i] = FromStr(li.String())
	}
	return result, nil
}

func (i *Interp) EvalObj(obj *TclObj) TclStatus {
	cmds, e := obj.AsCmds()
	if e != nil {
		return i.Fail(e)
	}
	return i.eval(cmds)
}

type argsig struct {
	name string
	def  *TclObj
}

func (i *Interp) bindArgs(vnames []argsig, args []*TclObj) os.Error {
	lastind := len(vnames) - 1
	for ix, vn := range vnames {
		if ix == lastind && vn.name == "args" {
			i.SetVarRaw(vn.name, fromList(args[ix:]))
			return nil
		} else if ix >= len(args) {
			if vn.def == nil {
				return os.NewError("arg count mismatch")
			}
			i.SetVarRaw(vn.name, vn.def)
		} else {
			i.SetVarRaw(vn.name, args[ix])
		}
	}
	return nil
}

func makeArgSigs(sig []*TclObj) []argsig {
	sigs := make([]argsig, len(sig))
	for i, a := range sig {
		sl, lerr := a.AsList()
		if lerr == nil && len(sl) == 2 {
			sigs[i] = argsig{sl[0].AsString(), sl[1]}
		} else {
			sigs[i] = argsig{name: a.AsString()}
		}
	}
	return sigs
}

func makeProc(sig []*TclObj, body *TclObj) TclCmd {
	cmds, ce := body.AsCmds()
	if ce != nil {
		return func(i *Interp, args []*TclObj) TclStatus { return i.Fail(ce) }
	}
	sigs := makeArgSigs(sig)
	return func(i *Interp, args []*TclObj) TclStatus {
		i.frame = newstackframe(i.frame)
		if be := i.bindArgs(sigs, args); be != nil {
			i.frame = i.frame.up()
			return i.Fail(be)
		}
		rc := i.eval(cmds)
		if rc == kTclReturn {
			rc = kTclOK
		}
		i.frame = i.frame.up()
		return rc
	}
}

func tclProc(i *Interp, args []*TclObj) TclStatus {
	if len(args) != 3 {
		return i.FailStr("wrong # args")
	}
	sig, err := args[1].AsList()
	if err != nil {
		return i.Fail(err)
	}
	i.SetCmd(args[0].AsString(), makeProc(sig, args[2]))
	return i.Return(kNil)
}

var tclStdin = bufio.NewReader(os.Stdin)

func NewInterp() *Interp {
	i := new(Interp)
	i.cmds = make(map[string]TclCmd)
	i.frame = newstackframe(nil)
	i.chans = make(map[string]interface{})
	i.chans["stdin"] = tclStdin
	i.chans["stdout"] = os.Stdout
	i.chans["stderr"] = os.Stderr

	for n, f := range tclBasicCmds {
		i.SetCmd(n, f)
	}

	i.SetCmd("proc", tclProc)
	i.SetCmd("error", func(ni *Interp, args []*TclObj) TclStatus { return i.FailStr(args[0].AsString()) })
	return i
}

type TclCmd func(*Interp, []*TclObj) TclStatus

func (i *Interp) SetCmd(name string, cmd TclCmd) {
	if cmd == nil {
		i.cmds[name] = nil, false
	} else {
		i.cmds[name] = cmd
	}
}

func (i *Interp) eval(cmds []Command) TclStatus {
	for ind, c := range cmds {
		res := i.evalCmd(c)
		if res != kTclOK {
			return res
		}
		if ind == len(cmds)-1 {
			return res
		}
	}
	return kTclOK
}

func (i *Interp) GetVarMap(global bool) VarMap {
	f := i.frame
	if global {
		for f.next != nil {
			f = f.next
		}
	}
	return f.vars
}

func (i *Interp) LinkVar(level int, theirs, mine string) {
	theirf := i.frame
	for level > 0 {
		theirf = theirf.up()
		level--
	}
	m := i.GetVarMap(false)
	m[mine] = &varEntry{link: &framelink{theirf, theirs}}
}

func (i *Interp) SetVarRaw(name string, val *TclObj) {
	i.SetVar(toVarRef(name), val)
}

func (i *Interp) SetVar(vr varRef, val *TclObj) {
	m := i.GetVarMap(vr.is_global)
	if val == nil {
		m[vr.name] = nil, false
	} else {
		n := vr.name
		old, ok := m[n]
		for ok && old != nil && old.link != nil {
			m = old.link.frame.vars
			n = old.link.name
			old, ok = m[n]
		}
		if old == nil {
			old = &varEntry{obj: val}
			m[n] = old
		}
		old.obj = val
	}
}

func (i *Interp) GetVarRaw(name string) (*TclObj, os.Error) {
	return i.GetVar(toVarRef(name))
}

func (i *Interp) GetVar(vr varRef) (*TclObj, os.Error) {
	v, ok := i.GetVarMap(vr.is_global)[vr.name]
	if !ok {
		return nil, os.NewError("variable not found: " + vr.String())
	}
	for v.link != nil {
		v, ok = v.link.frame.vars[v.link.name]
		if !ok {
			return nil, os.NewError("variable not found: " + vr.String())
		}
	}
	return v.obj, nil
}

func evalArgs(i *Interp, toks []TclTok) ([]*TclObj, TclStatus) {
	res := make([]*TclObj, len(toks))
	rc := kTclOK
	oind := 0
	for _, t := range toks {
		rc = t.Eval(i)
		if t.IsExpand() {
			rlist, e := i.retval.AsList()
			if e != nil {
				i.err = e
				return nil, kTclErr
			}
			if len(rlist) > 1 {
				nres := make([]*TclObj, len(res)+len(rlist))
				copy(nres, res)
				res = nres
			}
			oind += copy(res[oind:], rlist)
		} else {
			res[oind] = i.retval
			oind++
		}
		if rc != kTclOK {
			break
		}
	}
	return res[0:oind], rc
}

func (i *Interp) ClearError() { i.err = nil }

func (i *Interp) evalCmd(cmd Command) TclStatus {
	if len(cmd.words) == 0 {
		return i.Return(kNil)
	}
	args, rc := evalArgs(i, cmd.words)
	if rc != kTclOK {
		return rc
	}
	fname := args[0].AsString()
	if f, ok := i.cmds[fname]; ok {
		return f(i, args[1:])
	}
	if f, ok := i.cmds["unknown"]; ok {
		return f(i, args)
	}
	return i.FailStr("command not found: " + fname)
}

func (i *Interp) EvalString(s string) (*TclObj, os.Error) {
	return i.Run(strings.NewReader(s))
}

func (i *Interp) Run(in io.Reader) (*TclObj, os.Error) {
	cmds, e := ParseCommands(bufio.NewReader(in))
	if e != nil {
		return nil, e
	}
	r := i.eval(cmds)
	if r == kTclOK {
		if i.retval == nil {
			return kNil, nil
		}
		return i.retval, nil
	}
	if r != kTclOK && i.err == nil {
		i.err = os.NewError("uncaught error: " + strconv.Itoa(int(r)))
	}
	return nil, i.err
}
