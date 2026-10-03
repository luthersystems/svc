package libhandlebars

import (
	"errors"
	"strconv"
	"strings"

	"github.com/luthersystems/elps/elpsutil"
	"github.com/luthersystems/elps/lisp"
	"github.com/luthersystems/elps/lisp/lisplib/libjson"
	"github.com/luthersystems/svc/libhandlebars/hbs"
)

// DefaultPackageName is the package name used by LoadPackage.
const DefaultPackageName = "handlebars"

// ELPS condition types the package signals.
const (
	condParse  = "handlebars-parse"
	condRender = "handlebars-render"
)

// handlebarsPackage implements the elpsutil Package interfaces for the
// handlebars ELPS package.
type handlebarsPackage struct{}

func (handlebarsPackage) PackageName() string { return DefaultPackageName }

func (handlebarsPackage) PackageDoc() string {
	return `Handlebars template rendering engine.

Provides functions for parsing and rendering Handlebars templates with
JSON context data, powered by the hbs engine (luthersystems/svc).`
}

func (handlebarsPackage) Builtins() []lisp.LBuiltinDef {
	return builtins
}

// documentedBuiltin wraps an LBuiltinDef with a docstring.
type documentedBuiltin struct {
	lisp.LBuiltinDef
	docs string
}

func (b *documentedBuiltin) Docstring() string { return b.docs }

// LoadPackage loads the package.
func LoadPackage(env *lisp.LEnv) *lisp.LVal {
	return elpsutil.PackageLoader(&handlebarsPackage{})(env)
}

var builtins = []lisp.LBuiltinDef{
	&documentedBuiltin{
		elpsutil.Function("libname", lisp.Formals(), builtInLibname),
		`Returns the name of the template engine ("` + hbs.Name + `").`,
	},
	&documentedBuiltin{
		elpsutil.Function("version", lisp.Formals(), builtInVersion),
		`Returns the template engine's version string.

The version changes whenever a release changes any render output, error
or step charge, so peers that return the same version render alike.`,
	},
	&documentedBuiltin{
		elpsutil.Function("render", lisp.Formals("tpl", "ctx"), builtInRender),
		`Renders a Handlebars template string with the given context.

tpl is a Handlebars template string and ctx is a JSON-serializable
value used as the template context. Returns the rendered string.
Signals handlebars-parse on template syntax errors and when the template
exceeds the size or nesting limit, and handlebars-render on rendering
errors, including output over the size limit.

Output is byte-compatible with earlier releases, helper bugs included.
render-fixed renders with those bugs fixed.`,
	},
	&documentedBuiltin{
		elpsutil.Function("render-fixed", lisp.Formals("tpl", "ctx"), builtInRenderFixed),
		`Renders like render, with the known helper bugs fixed.

Takes the same arguments and signals the same conditions as render. The
template language is the same; only these helpers differ: to-str prints
numbers as {{x}} does; mod returns 0 when either argument is not a
number; float32 values convert to numbers; round-to-nth parses x as a
64-bit float; int parameters (date-add-months) accept an integral
number from the context; includeZero also accepts a context number 0.

A phylum opts in by calling render-fixed. Its output can differ from
render's for the same template and context.`,
	},
	&documentedBuiltin{
		elpsutil.Function("must-parse", lisp.Formals("tpl"), builtInMustParse),
		`Validates that tpl is a syntactically correct Handlebars template.

Returns nil on success. Signals handlebars-parse if the template
contains syntax errors or exceeds the size or nesting limit. Use this
to validate templates at load time without rendering them.`,
	},
}

func builtInLibname(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
	return lisp.String(hbs.Name)
}

func builtInVersion(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
	return lisp.String(hbs.Version)
}

// parse charges the parse and parses tpl through the process-wide cache.
// The charge, hbs.ParseCost of the template's length and lexer tokens, is
// made on every call, so a cache hit and a miss cost the same steps.
func parse(env *lisp.LEnv, tpl string) (*hbs.Program, *lisp.LVal) {
	m := &envMeter{env: env}
	prog, err := hbs.ParseCachedMetered(tpl, hbs.DefaultLimits(), m)
	if err != nil {
		if errors.Is(err, errBudget) {
			return nil, m.lerr
		}
		// Syntax errors and template limits (size, nesting) alike.
		return nil, env.ErrorConditionf(condParse, "error parsing template: %v", err)
	}
	return prog, nil
}

func builtInMustParse(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
	template := args.Cells[0]
	if template.Type != lisp.LString {
		return env.Errorf("non-string template: %v", template.Type)
	}
	if _, lerr := parse(env, template.Str); lerr != nil {
		return lerr
	}
	return lisp.Nil()
}

func builtInRender(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
	return render(env, args, hbs.ModeCompat)
}

func builtInRenderFixed(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
	return render(env, args, hbs.ModeFixed)
}

func render(env *lisp.LEnv, args *lisp.LVal, mode hbs.Mode) *lisp.LVal {
	template, context := args.Cells[0], args.Cells[1]
	if template.Type != lisp.LString {
		return env.Errorf("non-string template: %v", template.Type)
	}

	var contextBytes []byte
	switch context.Type {
	case lisp.LBytes:
		contextBytes = context.Bytes()
	default:
		var lerr *lisp.LVal
		contextBytes, lerr = dumpContext(env, context)
		if lerr != nil {
			return lerr
		}
	}
	// Decoding the context costs one step per started KiB of its JSON.
	if lerr := lisp.ChargeStartedKiB(env, len(contextBytes)); lerr.Type == lisp.LError {
		return lerr
	}
	// Numbers are charged as they are parsed (some take 20 us each).
	m := &envMeter{env: env}
	ctx, err := hbs.FromJSONMetered(contextBytes, m)
	if err != nil {
		if errors.Is(err, errBudget) {
			return m.lerr
		}
		return env.Errorf("error while unmarshaling: %v", err)
	}

	if mode == hbs.ModeFixed && context.Type != lisp.LBytes {
		// render-fixed keeps ELPS ints as ints, which JSON cannot tell
		// from floats.
		t := &intTyper{m: m}
		t.walk(context, ctx)
		if t.flush() != nil {
			return m.lerr
		}
	}

	prog, lerr := parse(env, template.Str)
	if lerr != nil {
		return lerr
	}
	out, err := prog.Render(ctx, hbs.Options{Meter: m, Limits: hbs.DefaultLimits(), Mode: mode})
	if err != nil {
		if errors.Is(err, errBudget) {
			return m.lerr
		}
		// Evaluation errors and render limits (output size, depth).
		return env.ErrorConditionf(condRender, "error while rendering template: %v", err)
	}
	return lisp.String(out)
}

// intTyper restores the ELPS ints in a context that went through JSON:
// walking the ELPS value alongside its decoded form, it replaces each
// float64 that came from an LInt with the int itself. The JSON route has
// already validated and bounded the value (depth, cycles, size), so the walk
// follows the decoded structure and stops wherever the two differ. It
// costs a step per value.
type intTyper struct {
	m       hbs.Meter
	pending int64
	err     error
}

func (t *intTyper) step() {
	t.pending++
	if t.pending >= 64 {
		_ = t.flush()
	}
}

func (t *intTyper) flush() error {
	if t.err == nil && t.pending > 0 {
		t.err = t.m.Charge(t.pending)
	}
	t.pending = 0
	return t.err
}

// walk retypes the ints of x within v, its decoded form, and returns v.
func (t *intTyper) walk(x *lisp.LVal, v hbs.Value) hbs.Value {
	if t.err != nil || x.IsNil() {
		return v
	}
	t.step()
	switch x.Type {
	case lisp.LInt:
		if _, ok := v.(float64); ok {
			return x.Int
		}
	case lisp.LQuote, lisp.LTaggedVal:
		return t.walk(x.Cells[0], v)
	case lisp.LSExpr:
		t.list(x.Cells, v)
	case lisp.LArray:
		if len(x.Cells) == 2 {
			t.list(x.Cells[1].Cells, v)
		}
	case lisp.LSortMap:
		obj, ok := v.(map[string]any)
		if !ok {
			return v
		}
		ents := x.MapEntries()
		if ents.Type == lisp.LError {
			return v
		}
		// A JSON member name two keys share (a string and a symbol) holds
		// one of them; leave it as JSON decoded it.
		names := make(map[string]int, len(ents.Cells))
		for _, e := range ents.Cells {
			if k, ok := memberName(e.Cells[0]); ok {
				names[k]++
			}
		}
		for _, e := range ents.Cells {
			if k, ok := memberName(e.Cells[0]); ok && names[k] == 1 {
				if ev, ok := obj[k]; ok {
					obj[k] = t.walk(e.Cells[1], ev)
				}
			}
		}
	default:
	}
	return v
}

func (t *intTyper) list(cells []*lisp.LVal, v hbs.Value) {
	arr, ok := v.([]any)
	if !ok || len(arr) != len(cells) {
		return
	}
	for i, c := range cells {
		arr[i] = t.walk(c, arr[i])
	}
}

// memberName is the JSON member name libjson writes for a map key.
func memberName(k *lisp.LVal) (string, bool) {
	switch k.Type {
	case lisp.LString, lisp.LSymbol:
		return k.Str, true
	case lisp.LInt:
		return strconv.Itoa(k.Int), true
	default:
		return "", false
	}
}

// dumpContext serializes an ELPS render context to JSON as json:dump-bytes
// does, with :string-numbers false: under the runtime's allocation cap
// (Runtime.MaxAlloc), value depth limit and evaluation context, charged per
// KiB written. The bytes are those libjson's Dump writes.
//
// A runtime limit is returned as json:dump-bytes reports it. Any other
// failure is reported as before, "error while serializing: <message>": the
// capped walk returns the same encoder error Dump would, so the unbounded
// Dump never runs.
func dumpContext(env *lisp.LEnv, v *lisp.LVal) ([]byte, *lisp.LVal) {
	// json:dump-bytes charges a successful encode by the bytes it writes,
	// but its walk costs per value (shared structure writes little and
	// walks much) and a failed encode charges nothing. Charge the walk
	// first, value by value, so neither runs uncharged.
	if lerr := chargeEncode(env, v); lerr != nil {
		return nil, lerr
	}
	res := libjson.DefaultSerializer().DumpBytesBuiltin(env, lisp.SExpr([]*lisp.LVal{v, lisp.Bool(false)}))
	if res.Type != lisp.LError {
		return res.Bytes(), nil
	}
	if isLimitError(res) || len(res.Cells) == 0 {
		return nil, res
	}
	return nil, env.Errorf("error while serializing: %s", res.Cells[0].Str)
}

// chargeEncode charges encoding v to JSON before the encoder runs:
// encodeValueCost steps per value and one step per started KiB of the
// JSON, estimated by walking v in the encoder's order. The walk stops where
// the encoder stops with an error, so the encoder then reports its own: at
// a value JSON cannot hold, past the value depth limit, at a value that
// contains itself, and once the estimate passes the runtime's allocation
// cap. It returns the budget error if the budget runs out first.
func chargeEncode(env *lisp.LEnv, v *lisp.LVal) *lisp.LVal {
	w := &encodeWalk{
		env:   env,
		limit: int64(env.Runtime.MaxAllocBytes()),
		depth: env.Runtime.ValueDepthLimit(),
		path:  map[*lisp.LVal]struct{}{},
	}
	if w.depth < 1024 { // as libjson's encoder reads the limit
		w.depth = lisp.MaxValueDepth
	}
	_, lerr := w.walk(v, 0)
	return lerr
}

type encodeWalk struct {
	env         *lisp.LEnv
	path        map[*lisp.LVal]struct{}
	limit, size int64
	charged     int64
	depth       int
}

// add charges n more estimated bytes and one value.
func (w *encodeWalk) add(n int64) *lisp.LVal {
	w.size += n
	if kib := (w.size + 1023) >> 10; kib > w.charged {
		if lerr := w.env.ChargeSteps(kib - w.charged); lerr.Type == lisp.LError {
			return lerr
		}
		w.charged = kib
	}
	if lerr := w.env.ChargeSteps(encodeValueCost); lerr.Type == lisp.LError {
		return lerr
	}
	return nil
}

// walk charges x and its contents. It reports true where the encoder
// fails, and the budget error if the budget runs out.
func (w *encodeWalk) walk(x *lisp.LVal, depth int) (bool, *lisp.LVal) {
	if w.size > w.limit {
		return true, nil
	}
	if x.IsNil() {
		return false, w.add(4)
	}
	if depth >= w.depth {
		return true, nil
	}
	var n int64
	var children []*lisp.LVal
	switch x.Type {
	case lisp.LInt:
		var buf [24]byte
		n = int64(len(strconv.AppendInt(buf[:0], int64(x.Int), 10)))
	case lisp.LFloat:
		n = 24
	case lisp.LString, lisp.LSymbol:
		n = int64(len(x.Str)) + 2
	case lisp.LBytes:
		n = int64(len(x.Bytes()))*4/3 + 4
	case lisp.LSExpr:
		n, children = int64(len(x.Cells))+2, x.Cells
	case lisp.LQuote, lisp.LTaggedVal:
		n, children = 1, x.Cells[:1]
	case lisp.LArray:
		if len(x.Cells) == 2 {
			n, children = int64(len(x.Cells[1].Cells))+2, x.Cells[1].Cells
		}
	case lisp.LSortMap:
		m := x.Map()
		// Charge the entries before allocating room for them.
		if lerr := w.add(int64(m.Len()) * 4); lerr != nil {
			return true, lerr
		}
		buf := make([]*lisp.LVal, m.Len())
		if e := m.Entries(buf); e.Type == lisp.LError {
			return true, nil
		}
		for _, entry := range buf {
			if entry != nil && len(entry.Cells) == 2 {
				children = append(children, entry.Cells[0], entry.Cells[1])
			}
		}
	default:
		return true, nil // the encoder stops here
	}
	if lerr := w.add(n); lerr != nil {
		return true, lerr
	}
	if len(children) == 0 {
		return false, nil
	}
	if _, cyclic := w.path[x]; cyclic {
		return true, nil
	}
	w.path[x] = struct{}{}
	defer delete(w.path, x)
	for _, c := range children {
		if stop, lerr := w.walk(c, depth+1); stop || lerr != nil {
			return true, lerr
		}
	}
	return false, nil
}

// encodeValueCost is the steps encoding one value costs beyond its bytes:
// about 200 ns a value on a structure that shares one value many times.
const encodeValueCost = 3

// isLimitError reports whether lerr is a runtime limit: the allocation cap,
// a step limit or budget, or a cancelled evaluation.
func isLimitError(lerr *lisp.LVal) bool {
	switch lerr.Str {
	case lisp.CondContextCancelled, lisp.CondStepLimitExceeded, lisp.CondStepBudgetExceeded:
		return true
	default:
	}
	return len(lerr.Cells) > 0 && strings.HasPrefix(lerr.Cells[0].Str, "allocation size exceeds maximum")
}

// errBudget is what envMeter returns to the engine when the ELPS budget
// is exhausted; render then returns the ELPS error itself.
var errBudget = errors.New("libhandlebars: ELPS step budget exhausted")

// envMeter charges the engine's steps to the ELPS environment.
type envMeter struct {
	env  *lisp.LEnv
	lerr *lisp.LVal
}

func (m *envMeter) Charge(steps int64) error {
	if lerr := m.env.ChargeSteps(steps); lerr.Type == lisp.LError {
		m.lerr = lerr
		return errBudget
	}
	return nil
}
