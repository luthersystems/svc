package libhandlebars

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"math/bits"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

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
	w, lerr := chargeEncode(env, v)
	if lerr != nil {
		return nil, lerr
	}
	if w.capErr {
		// The encoder would pass the allocation cap: its error, as
		// json:dump-bytes raises it (a cancelled context first).
		if cerr := env.CheckContext(); cerr.Type == lisp.LError {
			return nil, cerr
		}
		return nil, env.Errorf("allocation size exceeds maximum (%d)", env.Runtime.MaxAllocBytes())
	}
	if w.nativeErr != nil {
		// Where the encoder would have failed, with its text.
		return nil, env.Errorf("error while serializing: %s", w.nativeErr.Error())
	}
	if len(w.natives) > 0 {
		if v, lerr = w.withNatives(v); lerr != nil {
			return nil, lerr
		}
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
func chargeEncode(env *lisp.LEnv, v *lisp.LVal) (*encodeWalk, *lisp.LVal) {
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
	return w, lerr
}

type encodeWalk struct {
	env         *lisp.LEnv
	path        map[*lisp.LVal]struct{}
	natives     map[*lisp.LVal][]byte // each native's JSON, marshalled once
	hasNative   map[*lisp.LVal]bool   // the containers on the way to a marshalled native
	nativeErr   error                 // the error marshalling a native, where the walk stopped
	stack       []*lisp.LVal          // the containers the walk is inside
	lower       int64                 // a lower bound of the JSON's length so far
	capErr      bool                  // lower passed the allocation cap at a native
	limit, size int64
	charged     int64
	depth       int
}

// native marshals a native value's JSON once, as the encoder would through
// encoding/json, charging it first: a value encoding/json walks by
// reflection is sized by nativeCost before it is marshalled; a
// json.Marshaler's own work is the embedder's, and its bytes are charged
// by JSONCost after, as the encoder decodes them to check they load. The
// bytes are reused for the encode (dumpContext), so a Marshaler is called
// once. stop is set where the encoder would fail.
func (w *encodeWalk) native(x *lisp.LVal) ([]byte, bool, *lisp.LVal) {
	b, done := w.natives[x]
	before := w.lower
	if !done {
		if v := reflect.ValueOf(x.Native); !v.IsValid() || !v.Type().Implements(marshalerType) {
			if stop, lerr := w.nativeCost(v); stop || lerr != nil {
				return nil, true, lerr
			}
			// Past the allocation cap even by a lower bound: the encoder
			// would fail there, so do not marshal it.
			if w.lower > w.limit {
				w.capErr = true
				return nil, true, nil
			}
		}
		var err error
		if b, err = json.Marshal(x.Native); err != nil {
			w.nativeErr = err
			return nil, true, nil
		}
		if w.natives == nil {
			w.natives = map[*lisp.LVal][]byte{}
		}
		w.natives[x] = b
		w.markPath()
	}
	w.lower = before + int64(len(b))
	if w.lower > w.limit {
		w.capErr = true
		return nil, true, nil
	}
	if lerr := w.env.ChargeSteps(hbs.JSONCost(b)); lerr.Type == lisp.LError {
		return nil, true, lerr
	}
	return b, false, nil
}

// walkCoster adapts the encode walk to goJSONCost: values go through
// addN, so they count toward the estimate and the allocation cap, and a
// budget error is kept for the walk to return.
type walkCoster struct {
	w    *encodeWalk
	lerr *lisp.LVal
}

var errWalkBudget = errors.New("budget")

func (c *walkCoster) charge(values, bytes int64) error {
	c.w.lower += bytes // goJSONCost's estimates never exceed what is written
	if lerr := c.w.addN(values, bytes); lerr != nil {
		c.lerr = lerr
		return errWalkBudget
	}
	return nil
}

func (c *walkCoster) steps(n int64) error {
	if lerr := c.w.env.ChargeSteps(n); lerr.Type == lisp.LError {
		c.lerr = lerr
		return errWalkBudget
	}
	return nil
}

// nativeCost charges encoding/json's walk of a native (goJSONCost). It
// reports stop where encoding/json would fail, recording its error.
func (w *encodeWalk) nativeCost(v reflect.Value) (bool, *lisp.LVal) {
	c := &walkCoster{w: w}
	err := goJSONCost(c, v, 0)
	var fail *jsonFailure
	switch {
	case err == nil:
		return false, nil
	case errors.As(err, &fail):
		w.nativeErr = fail
		return true, nil
	default:
		return true, c.lerr
	}
}

// jsonNesting is the deepest nesting of objects and arrays in valid JSON
// b (one pass, charged with b by JSONCost).
func jsonNesting(b []byte) int {
	depth, deepest := 0, 0
	inString := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			default:
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '[', '{':
			depth++
			deepest = max(deepest, depth)
		case ']', '}':
			depth--
		default:
		}
	}
	return deepest
}

// markPath marks the containers the walk is inside as leading to a
// marshalled native, stopping at one already marked (so each is marked
// once).
func (w *encodeWalk) markPath() {
	if w.hasNative == nil {
		w.hasNative = map[*lisp.LVal]bool{}
	}
	for i := len(w.stack) - 1; i >= 0 && !w.hasNative[w.stack[i]]; i-- {
		w.hasNative[w.stack[i]] = true
	}
}

// withNatives returns v with each native the walk marshalled replaced by a
// native json.RawMessage of its bytes, so the encoder writes the same JSON
// without marshalling it again. Containers on the way are copied; the rest
// is shared.
func (w *encodeWalk) withNatives(v *lisp.LVal) (*lisp.LVal, *lisp.LVal) {
	memo := map[*lisp.LVal]*lisp.LVal{}
	var lerr *lisp.LVal
	// copying charges a copied container's cells, a step per 16, before
	// the copy: the walk may have stopped partway through it.
	copying := func(n int) bool {
		if lerr == nil {
			if e := w.env.ChargeSteps(int64(n/16 + 1)); e.Type == lisp.LError {
				lerr = e
			}
		}
		return lerr == nil
	}
	var sub func(x *lisp.LVal) *lisp.LVal
	sub = func(x *lisp.LVal) *lisp.LVal {
		if x == nil {
			return x
		}
		if r, ok := memo[x]; ok {
			return r
		}
		// Only the containers the charged walk marked lead to a
		// replacement; nothing past where it stopped is visited.
		if x.Type != lisp.LNative && !w.hasNative[x] {
			return x
		}
		memo[x] = x // a cycle back to x keeps the original
		var out *lisp.LVal
		switch x.Type {
		case lisp.LNative:
			// JSON nesting past encoding/json's decoder limit (10,000)
			// fails the encoder's load check; a RawMessage would fail its
			// compaction first, with other text. Leave such a native to
			// the encoder.
			if b, ok := w.natives[x]; ok && jsonNesting(b) <= 10_000 {
				out = lisp.Native(json.RawMessage(b))
			}
		case lisp.LSortMap:
			if !copying(2 * x.Map().Len()) {
				break
			}
			ents := x.MapEntries()
			if ents.Type == lisp.LError {
				break
			}
			changed := false
			vals := make([]*lisp.LVal, len(ents.Cells))
			for i, e := range ents.Cells {
				vals[i] = sub(e.Cells[1])
				changed = changed || vals[i] != e.Cells[1]
			}
			if changed {
				out = lisp.SortedMapSized(len(vals))
				for i, e := range ents.Cells {
					out.MapSetLVal(e.Cells[0], vals[i])
				}
			}
		case lisp.LArray:
			// The elements are the cells of Cells[1], which the walk
			// visited as the array's own children.
			if len(x.Cells) != 2 || !copying(len(x.Cells[1].Cells)) {
				break
			}
			var cells []*lisp.LVal
			for i, c := range x.Cells[1].Cells {
				if s := sub(c); s != c {
					if cells == nil {
						cells = append([]*lisp.LVal(nil), x.Cells[1].Cells...)
					}
					cells[i] = s
				}
			}
			if cells != nil {
				data := *x.Cells[1]
				data.Cells = cells
				cp := *x
				cp.Cells = []*lisp.LVal{x.Cells[0], &data}
				out = &cp
			}
		case lisp.LSExpr, lisp.LQuote, lisp.LTaggedVal:
			if !copying(len(x.Cells)) {
				break
			}
			var cells []*lisp.LVal
			for i, c := range x.Cells {
				if s := sub(c); s != c {
					if cells == nil {
						cells = append([]*lisp.LVal(nil), x.Cells...)
					}
					cells[i] = s
				}
			}
			if cells != nil {
				cp := *x
				cp.Cells = cells
				out = &cp
			}
		default:
		}
		if out == nil {
			return x
		}
		memo[x] = out
		return out
	}
	out := sub(v)
	return out, lerr
}

// add charges n more estimated bytes and one value.
func (w *encodeWalk) add(n int64) *lisp.LVal { return w.addN(1, n) }

// addN charges n more estimated bytes and values values.
func (w *encodeWalk) addN(values, n int64) *lisp.LVal {
	w.size += n
	if kib := (w.size + 1023) >> 10; kib > w.charged {
		if lerr := w.env.ChargeSteps(kib - w.charged); lerr.Type == lisp.LError {
			return lerr
		}
		w.charged = kib
	}
	if lerr := w.env.ChargeSteps(encodeValueCost * values); lerr.Type == lisp.LError {
		return lerr
	}
	return nil
}

// scan charges reading n bytes of a string to size its JSON escaping: a
// step per started 64 bytes.
func (w *encodeWalk) scan(n int) *lisp.LVal {
	if n == 0 {
		return nil
	}
	if lerr := w.env.ChargeSteps(int64((n-1)/64 + 1)); lerr.Type == lisp.LError {
		return lerr
	}
	return nil
}

// jsonStringLen is the length of s as libjson writes it, quotes included:
// encoding/json's escaping (\uXXXX for controls, <, >, &, U+2028, U+2029
// and each invalid UTF-8 byte; two bytes for \, ", \b, \f, \n, \r, \t).
func jsonStringLen(s string) int64 {
	n := int64(2)
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '\\' || c == '"' || c == '\b' || c == '\f' || c == '\n' || c == '\r' || c == '\t':
				n += 2
			case c < 0x20 || c == '<' || c == '>' || c == '&':
				n += 6
			default:
				n++
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1, r == '\u2028', r == '\u2029':
			n += 6
		default:
			n += int64(size)
		}
		i += size
	}
	return n
}

// jsonQuotedStringLen is the length encoding/json writes for a ",string"
// string field: s as a JSON string, written again as a JSON string (that
// second time without HTML escaping), so each " and \ of the first form
// is escaped once more. Computed without building either.
func jsonQuotedStringLen(s string) int64 {
	inner, special := int64(2), int64(2) // the inner quotes
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '\\' || c == '"':
				inner, special = inner+2, special+2
			case c == '\b' || c == '\f' || c == '\n' || c == '\r' || c == '\t':
				inner, special = inner+2, special+1
			case c < 0x20 || c == '<' || c == '>' || c == '&':
				inner, special = inner+6, special+1
			default:
				inner++
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1, r == '\u2028', r == '\u2029':
			inner, special = inner+6, special+1
		default:
			inner += int64(size)
		}
		i += size
	}
	return 2 + inner + special
}

// walk charges x and its contents. It reports true where the encoder
// fails, and the budget error if the budget runs out.
func (w *encodeWalk) walk(x *lisp.LVal, depth int) (bool, *lisp.LVal) {
	if w.size > w.limit {
		return true, nil
	}
	if x.IsNil() {
		w.lower += 4
		return false, w.add(4)
	}
	if depth >= w.depth {
		return true, nil
	}
	var n int64
	var children []*lisp.LVal
	lower := int64(-1) // n unless set: the bytes surely written
	switch x.Type {
	case lisp.LInt:
		var buf [24]byte
		n = int64(len(strconv.AppendInt(buf[:0], int64(x.Int), 10)))
	case lisp.LFloat:
		if math.IsInf(x.Float, 0) || math.IsNaN(x.Float) {
			return true, nil // the encoder refuses it
		}
		n, lower = 24, 1
	case lisp.LSymbol:
		switch x.Str {
		case lisp.TrueSymbol, lisp.FalseSymbol, "json:null":
			n, lower = 5, 4
		default:
			if lerr := w.scan(len(x.Str)); lerr != nil {
				return true, lerr
			}
			n = jsonStringLen(x.Str)
		}
	case lisp.LString:
		if lerr := w.scan(len(x.Str)); lerr != nil {
			return true, lerr
		}
		n = jsonStringLen(x.Str)
	case lisp.LNative:
		b, stop, lerr := w.native(x)
		if stop || lerr != nil {
			return true, lerr
		}
		n, lower = int64(len(b)), 0 // native() counted it
	case lisp.LBytes:
		n, lower = int64(len(x.Bytes()))*4/3+4, int64(base64.StdEncoding.EncodedLen(len(x.Bytes())))
	case lisp.LSExpr:
		n, children, lower = int64(len(x.Cells))+2, x.Cells, 2
	case lisp.LQuote, lisp.LTaggedVal:
		n, children, lower = 1, x.Cells[:1], 0
	case lisp.LArray:
		lower = 0
		if len(x.Cells) == 2 {
			n, children, lower = int64(len(x.Cells[1].Cells))+2, x.Cells[1].Cells, 2
		}
	case lisp.LSortMap:
		m := x.Map()
		// The encoder collects and sorts the entries before it writes any
		// (and before its allocation cap can stop it): charge that, and stop
		// at the cap, before allocating room for them here.
		if lerr := w.env.ChargeSteps(int64(m.Len()) * int64(1+bits.Len(uint(m.Len())))); lerr.Type == lisp.LError {
			return true, lerr
		}
		if lerr := w.add(int64(m.Len()) * 4); lerr != nil {
			return true, lerr
		}
		lower = 2 + int64(m.Len()) // braces and colons
		if w.size > w.limit {
			return true, nil
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
	if lower < 0 {
		lower = n
	}
	w.lower += lower
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
	w.stack = append(w.stack, x)
	defer func() {
		delete(w.path, x)
		w.stack = w.stack[:len(w.stack)-1]
	}()
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
