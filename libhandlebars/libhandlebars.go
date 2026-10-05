package libhandlebars

import (
	"errors"
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
// handlebars ELPS package. Its builtins parse and render under lim.
type handlebarsPackage struct {
	parser parser
	lim    hbs.Limits
}

func (handlebarsPackage) PackageName() string { return DefaultPackageName }

func (handlebarsPackage) PackageDoc() string {
	return `Handlebars template rendering engine.

Provides functions for parsing and rendering Handlebars templates with
JSON context data, powered by the hbs engine (luthersystems/svc).`
}

func (p handlebarsPackage) Builtins() []lisp.LBuiltinDef {
	return builtins(p.lim, p.parser)
}

// documentedBuiltin wraps an LBuiltinDef with a docstring.
type documentedBuiltin struct {
	lisp.LBuiltinDef
	docs string
}

func (b *documentedBuiltin) Docstring() string { return b.docs }

// LoadPackage loads the package, which parses and renders under
// hbs.DefaultLimits().
func LoadPackage(env *lisp.LEnv) *lisp.LVal {
	return elpsutil.PackageLoader(&handlebarsPackage{lim: hbs.DefaultLimits()})(env) // LoadPackageWith()
}

// builtins are the package's functions, parsing through ps and parsing
// and rendering under lim.
func builtins(lim hbs.Limits, ps parser) []lisp.LBuiltinDef {
	builtInMustParse := func(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal { return mustParse(env, args, lim, ps) }
	builtInRender := func(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
		mode, lerr := renderMode(env, args.KeyArg(2))
		if lerr != nil {
			return lerr
		}
		return render(env, args, mode, lim, ps)
	}
	return []lisp.LBuiltinDef{
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
			elpsutil.Function("render", lisp.Formals("tpl", "ctx", lisp.KeyArgSymbol, "strict"), builtInRender),
			`Renders a Handlebars template string with the given context.

tpl is a Handlebars template string and ctx is a JSON-serializable
value used as the template context. Returns the rendered string.
Signals handlebars-parse on template syntax errors and when the template
exceeds the size or nesting limit, and handlebars-render on rendering
errors, including output over the size limit.

By default, output is byte-compatible with earlier releases, helper
bugs included: the context goes through JSON, so every number is a
float, and an ELPS int 3 prints as 3.000000 with to-str.

:strict true turns on strict mode: helper bug fixes and exact ELPS
integers instead of raymond-compatible behavior. ELPS ints in ctx stay
ints (to-str prints 3, and an int above 2^53 prints exactly), and the
known helper bugs are fixed: to-str prints numbers as {{x}} does; mod
returns 0 when either argument is not a number; float32 values convert
to numbers; round-to-nth parses x as a 64-bit float; int parameters
(date-add-months) accept an integral number from the context;
includeZero also accepts a context number 0. Its output can differ from
the default's for the same template and context. :strict false, or nil,
is the default; any other value is an error.`,
		},
		&documentedBuiltin{
			elpsutil.Function("must-parse", lisp.Formals("tpl"), builtInMustParse),
			`Validates that tpl is a syntactically correct Handlebars template.

Returns nil on success. Signals handlebars-parse if the template
contains syntax errors or exceeds the size or nesting limit. Use this
to validate templates at load time without rendering them.`,
		},
	}
}

func builtInLibname(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
	return lisp.String(hbs.Name)
}

func builtInVersion(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
	return lisp.String(hbs.Version)
}

// parse charges the parse and parses tpl under lim through the
// process-wide cache, whose key holds the effective template-size and
// depth limits, so a hit never skips a limit. The charge, hbs.ParseCost
// of the template's length and lexer tokens, is made on every call, so a
// cache hit and a miss cost the same steps.
func parse(env *lisp.LEnv, tpl string, lim hbs.Limits, ps parser) (*hbs.Program, *lisp.LVal) {
	m := &envMeter{env: env}
	prog, err := ps.parse(tpl, lim, m)
	if err != nil {
		if errors.Is(err, errBudget) {
			return nil, m.lerr
		}
		// Syntax errors and template limits (size, nesting) alike.
		return nil, env.ErrorConditionf(condParse, "error parsing template: %v", err)
	}
	return prog, nil
}

func mustParse(env *lisp.LEnv, args *lisp.LVal, lim hbs.Limits, ps parser) *lisp.LVal {
	template := args.Cells[0]
	if template.Type != lisp.LString {
		return env.Errorf("non-string template: %v", template.Type)
	}
	if _, lerr := parse(env, template.Str, lim, ps); lerr != nil {
		return lerr
	}
	return lisp.Nil()
}

// renderMode is render's mode for its :strict argument: ModeFixed for
// true, ModeCompat for false or nil (not given).
func renderMode(env *lisp.LEnv, strict *lisp.LVal) (hbs.Mode, *lisp.LVal) {
	switch {
	case strict.IsNil():
		return hbs.ModeCompat, nil
	case strict.Type == lisp.LSymbol && strict.Str == lisp.TrueSymbol:
		return hbs.ModeFixed, nil
	case strict.Type == lisp.LSymbol && strict.Str == lisp.FalseSymbol:
		return hbs.ModeCompat, nil
	case strict.Type == lisp.LSymbol:
		return hbs.ModeCompat, env.Errorf("strict must be true or false, got: %s", strict.Str)
	default:
		// The type only: the value may be large.
		return hbs.ModeCompat, env.Errorf("strict must be true or false, got: %v", strict.Type)
	}
}

func render(env *lisp.LEnv, args *lisp.LVal, mode hbs.Mode, lim hbs.Limits, ps parser) *lisp.LVal {
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
		// :strict keeps ELPS ints as ints, which JSON cannot tell
		// from floats.
		t := &intTyper{m: m}
		t.walk(context, ctx)
		if t.flush() != nil {
			return m.lerr
		}
	}

	prog, lerr := parse(env, template.Str, lim, ps)
	if lerr != nil {
		return lerr
	}
	out, err := prog.Render(ctx, hbs.Options{Meter: m, Limits: lim, Mode: mode})
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
//
// Its recursion follows v's nesting, which encoding/json bounds at 10,000
// levels; a quote, a tagged value or a scalar array adds no JSON level, so
// a chain of them is unwrapped in a loop, not by recursion.
func (t *intTyper) walk(x *lisp.LVal, v hbs.Value) hbs.Value {
	for t.err == nil && !x.IsNil() && (x.Type == lisp.LQuote || x.Type == lisp.LTaggedVal || scalarArray(x)) {
		t.step()
		if x.Type == lisp.LArray {
			x = x.Cells[1].Cells[0]
		} else {
			x = x.Cells[0]
		}
	}
	if t.err != nil || x.IsNil() {
		return v
	}
	t.step()
	switch x.Type {
	case lisp.LInt:
		if _, ok := v.(float64); ok {
			return x.Int
		}
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

// scalarArray reports whether x is a zero-dimensional array, which
// libjson writes as its one element, as it writes a quote's: no brackets,
// no JSON level.
func scalarArray(x *lisp.LVal) bool {
	return x.Type == lisp.LArray && len(x.Cells) == 2 && x.Cells[0] != nil && x.Cells[1] != nil &&
		x.Cells[0].Len() == 0 && len(x.Cells[1].Cells) > 0
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

// memberName is the JSON member name libjson writes for a map key, as it
// decodes: each byte of invalid UTF-8 is written as U+FFFD.
func memberName(k *lisp.LVal) (string, bool) {
	switch k.Type {
	case lisp.LString, lisp.LSymbol:
		return validUTF8(k.Str), true
	case lisp.LInt:
		return strconv.Itoa(k.Int), true
	default:
		return "", false
	}
}

// validUTF8 is s with each byte of invalid UTF-8 replaced by U+FFFD, as
// encoding/json (and libjson) write it. strings.ToValidUTF8 instead
// replaces a run of such bytes with one U+FFFD.
func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(3 * len(s)) // at most 3 bytes for each one
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			b.WriteRune(utf8.RuneError)
		} else {
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// dumpContext serializes the original context through json:dump-bytes with
// :string-numbers false. ELPS owns validation, native marshaling, resource
// limits and error precedence, including failures near the allocation cap.
// No svc walk or charge may run first and replace the encoder's error.
// Runtime limits keep their conditions; other errors keep render's prefix.
func dumpContext(env *lisp.LEnv, v *lisp.LVal) ([]byte, *lisp.LVal) {
	res := libjson.DefaultSerializer().DumpBytesBuiltin(env, lisp.SExpr([]*lisp.LVal{v, lisp.Bool(false)}))
	if res.Type != lisp.LError {
		return res.Bytes(), nil
	}
	if isLimitError(res) || len(res.Cells) == 0 {
		return nil, res
	}
	return nil, env.Errorf("error while serializing: %s", res.Cells[0].Str)
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
