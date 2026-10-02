package shape

import (
	"sort"
	"strconv"
	"strings"

	"github.com/luthersystems/svc/libhandlebars/internal/raymondref/ast"
	"github.com/luthersystems/svc/libhandlebars/internal/raymondref/parser"
)

// Kind is a set of value classes a path was used as.
type Kind uint16

const (
	KScalar Kind = 1 << iota // printed: string or number
	KTruthy                  // tested by if/unless/not/and/or: anything
	KNumber                  // a numeric helper argument
	KDate                    // a date helper argument (YYYY-MM-DD string)
	KString                  // a string helper argument
	KArray                   // iterated, or len/haystack
	KObject                  // has fields, or with/each element
)

// Node is the inferred schema of one context value.
type Node struct {
	Kind    Kind
	Fields  map[string]*Node
	Elem    *Node    // for arrays
	Strings []string // string values the template compares this with
	Numbers []string // number literals the template compares this with
}

func newNode() *Node { return &Node{Fields: map[string]*Node{}} }

func (n *Node) field(name string) *Node {
	n.Kind |= KObject
	f, ok := n.Fields[name]
	if !ok {
		f = newNode()
		n.Fields[name] = f
	}
	return f
}

func (n *Node) elem() *Node {
	n.Kind |= KArray
	if n.Elem == nil {
		n.Elem = newNode()
	}
	return n.Elem
}

func (n *Node) addString(s string) {
	for _, x := range n.Strings {
		if x == s {
			return
		}
	}
	n.Strings = append(n.Strings, s)
}

func (n *Node) addNumber(s string) {
	for _, x := range n.Numbers {
		if x == s {
			return
		}
	}
	n.Numbers = append(n.Numbers, s)
}

// FieldNames returns the sorted field names.
func (n *Node) FieldNames() []string {
	names := make([]string, 0, len(n.Fields))
	for k := range n.Fields {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

type frame struct {
	ctx    *Node
	params map[string]*Node // block params bound in this frame
}

type inferrer struct {
	root   *Node
	frames []frame
}

// Infer derives a context schema from how tpl uses its paths.
func Infer(tpl string) (*Node, error) {
	prog, err := parser.Parse(tpl)
	if err != nil {
		return nil, err
	}
	in := &inferrer{root: newNode()}
	in.root.Kind |= KObject
	in.frames = []frame{{ctx: in.root}}
	in.program(prog)
	return in.root, nil
}

func (in *inferrer) program(p *ast.Program) {
	if p == nil {
		return
	}
	for _, st := range p.Body {
		switch st := st.(type) {
		case *ast.MustacheStatement:
			in.expr(st.Expression, KScalar)
		case *ast.BlockStatement:
			in.block(st)
		}
	}
}

func (in *inferrer) push(ctx *Node, params []string, bind ...*Node) {
	f := frame{ctx: ctx, params: map[string]*Node{}}
	for i, p := range params {
		if i < len(bind) && bind[i] != nil {
			f.params[p] = bind[i]
		} else {
			f.params[p] = newNode()
		}
	}
	in.frames = append(in.frames, f)
}

func (in *inferrer) pop() { in.frames = in.frames[:len(in.frames)-1] }

func (in *inferrer) block(b *ast.BlockStatement) {
	e := b.Expression
	name := e.HelperName()
	var bp []string
	if b.Program != nil {
		bp = b.Program.BlockParams
	}
	switch name {
	case "each":
		if len(e.Params) > 0 {
			src := in.value(e.Params[0])
			var el *Node
			if src != nil {
				el = src.elem()
			} else {
				el = newNode()
			}
			idx := newNode()
			idx.Kind |= KScalar
			in.push(el, bp, el, idx)
			in.program(b.Program)
			in.pop()
		}
		in.program(b.Inverse)
		return
	case "with":
		if len(e.Params) > 0 {
			obj := in.value(e.Params[0])
			if obj == nil {
				obj = newNode()
			}
			obj.Kind |= KObject
			in.push(obj, bp, obj)
			in.program(b.Program)
			in.pop()
		}
		in.program(b.Inverse)
		return
	case "select":
		el := newNode()
		if e.Hash != nil {
			for _, hp := range e.Hash.Pairs {
				if hp.Key == "from" {
					if src := in.value(hp.Val); src != nil {
						el = src.elem()
					}
				}
			}
			for _, hp := range e.Hash.Pairs {
				if s, ok := hp.Val.(*ast.StringLiteral); ok && hp.Key == "where" {
					if k, v, ok := strings.Cut(s.Value, "="); ok {
						f := el.field(k)
						f.Kind |= KString
						f.addString(v)
					}
				}
			}
		}
		el.Kind |= KObject
		in.push(el, bp, el)
		in.program(b.Program)
		in.pop()
		in.program(b.Inverse)
		return
	}
	// if, unless, helpers in block form and mustache-style sections keep
	// the context for the purposes of inference.
	in.expr(e, KTruthy)
	in.program(b.Program)
	in.program(b.Inverse)
}

var argKinds = map[string]Kind{
	"gt": KNumber, "gte": KNumber, "lt": KNumber, "lte": KNumber,
	"times": KNumber, "div": KNumber, "mod": KNumber, "round-to-nth": KNumber,
	"prettyp-num-en": KNumber, "to-int": KNumber, "to-str": KNumber,
	"plus": KNumber, "minus": KNumber,
	"date-diff-month": KDate, "is-after": KDate, "date-add-months": KDate,
	"date-beautify": KDate, "date-DDMMYY-slash": KDate,
	"date-DDMMYYYY-slash": KDate, "date-DDMMYYYY": KDate,
	"possessive": KString, "format-phone-gb": KString, "escape-uri-component": KString,
	"not": KTruthy, "and": KTruthy, "or": KTruthy, "if": KTruthy, "unless": KTruthy,
	"len": KArray, "eq": KScalar, "global": KString,
}

// expr records the use of an expression. use is how its value is used
// when it is a plain path.
func (in *inferrer) expr(e *ast.Expression, use Kind) {
	if e == nil {
		return
	}
	name := e.HelperName()
	isHelper := len(e.Params) > 0 || e.Hash != nil
	if _, known := argKinds[name]; known && !isHelper {
		isHelper = true
	}
	if !isHelper {
		if n := in.value(e.Path); n != nil {
			n.Kind |= use
		}
		return
	}
	k := argKinds[name]
	for i, p := range e.Params {
		pk := k
		if name == "date-add-months" && i == 1 {
			pk = KNumber
		}
		if name == "global" && i == 0 {
			pk = KString
		}
		n := in.value(p)
		if n == nil {
			continue
		}
		n.Kind |= pk
		if name == "eq" {
			for j, q := range e.Params {
				if j == i {
					continue
				}
				switch lit := q.(type) {
				case *ast.StringLiteral:
					n.addString(lit.Value)
				case *ast.NumberLiteral:
					n.addNumber(lit.Original)
				}
			}
		}
	}
	if e.Hash != nil {
		for _, hp := range e.Hash.Pairs {
			n := in.value(hp.Val)
			if n == nil {
				continue
			}
			switch {
			case name == "in-string-array" && hp.Key == "haystack":
				n.Kind |= KArray
				n.elem().Kind |= KString
			case name == "global" && hp.Key == "key":
				n.Kind |= KString
			case name == "global" && hp.Key == "val":
				n.Kind |= KString
			default:
				n.Kind |= k
			}
		}
	}
}

// value resolves a parameter to the schema node it reads, recording
// subexpression uses on the way. Literals and data variables give nil.
func (in *inferrer) value(n ast.Node) *Node {
	switch n := n.(type) {
	case *ast.SubExpression:
		e := n.Expression
		if _, known := argKinds[e.HelperName()]; !known && len(e.Params) == 0 && e.Hash == nil {
			// (path) with no arguments evaluates the path.
			return in.value(e.Path)
		}
		in.expr(e, KScalar)
		return nil
	case *ast.Expression:
		in.expr(n, KScalar)
		return nil
	case *ast.PathExpression:
		return in.path(n)
	}
	return nil
}

func (in *inferrer) path(p *ast.PathExpression) *Node {
	parts := p.Parts
	var base *Node
	switch {
	case p.Data:
		if len(parts) == 0 || parts[0] != "root" {
			return nil
		}
		base, parts = in.root, parts[1:]
	default:
		fi := len(in.frames) - 1 - p.Depth
		if fi < 0 {
			fi = 0
		}
		base = in.frames[fi].ctx
		if p.Depth == 0 && !p.Scoped && len(parts) > 0 {
			for j := len(in.frames) - 1; j >= 0; j-- {
				if b, ok := in.frames[j].params[parts[0]]; ok {
					base, parts = b, parts[1:]
					break
				}
			}
		}
	}
	for _, part := range parts {
		if len(part) >= 2 && part[0] == '[' && part[len(part)-1] == ']' {
			part = part[1 : len(part)-1]
		}
		if idx, err := strconv.Atoi(part); err == nil && idx >= 0 {
			base = base.elem()
			continue
		}
		base = base.field(part)
	}
	return base
}
