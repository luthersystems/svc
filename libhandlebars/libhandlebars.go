package libhandlebars

import (
	"encoding/json"
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

// Template is a parsed template.
type Template = *hbs.Program

// Parse parses a template with hbs.DefaultLimits().
func Parse(template string) (Template, error) {
	return hbs.ParseCached(template, hbs.DefaultLimits())
}

// Render renders tpl in hbs.ModeCompat, as handlebars:render does. ctx is
// converted through JSON (json.Marshal, then hbs.FromJSON), so it must
// marshal to a JSON object or null.
func Render(tpl Template, ctx interface{}) (string, error) {
	b, err := json.Marshal(ctx)
	if err != nil {
		return "", err
	}
	v, err := hbs.FromJSON(b)
	if err != nil {
		return "", err
	}
	return tpl.Render(v, hbs.Options{Mode: hbs.ModeCompat, Limits: hbs.DefaultLimits()})
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
// The charge, one step per started KiB of template, is made on every
// call, so a cache hit and a miss cost the same steps.
func parse(env *lisp.LEnv, tpl string) (*hbs.Program, *lisp.LVal) {
	if lerr := lisp.ChargeStartedKiB(env, len(tpl)); lerr.Type == lisp.LError {
		return nil, lerr
	}
	prog, err := hbs.ParseCached(tpl, hbs.DefaultLimits())
	if err != nil {
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
	ctx, err := hbs.FromJSON(contextBytes)
	if err != nil {
		return env.Errorf("error while unmarshaling: %v", err)
	}

	prog, lerr := parse(env, template.Str)
	if lerr != nil {
		return lerr
	}
	m := &envMeter{env: env}
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
	res := libjson.DefaultSerializer().DumpBytesBuiltin(env, lisp.SExpr([]*lisp.LVal{v, lisp.Bool(false)}))
	if res.Type != lisp.LError {
		return res.Bytes(), nil
	}
	// json:dump-bytes charges only a successful encode. Charge the walk
	// that failed, so a failure costs what the work before it did.
	if lerr := chargeFailedEncode(env, v); lerr != nil {
		return nil, lerr
	}
	if isLimitError(res) || len(res.Cells) == 0 {
		return nil, res
	}
	return nil, env.Errorf("error while serializing: %s", res.Cells[0].Str)
}

// chargeFailedEncode charges, one step per started KiB, the JSON a failed
// encode of v wrote before it stopped, estimated by walking v in the
// encoder's order: it stops at the first value JSON cannot hold, or once
// the estimate passes the runtime's allocation cap, where the encoder
// stopped too. It returns the budget error if the budget runs out first.
func chargeFailedEncode(env *lisp.LEnv, v *lisp.LVal) *lisp.LVal {
	limit := int64(env.Runtime.MaxAllocBytes())
	var size, charged int64
	add := func(n int64) *lisp.LVal {
		size += n
		if kib := (size + 1023) >> 10; kib > charged {
			if lerr := env.ChargeSteps(kib - charged); lerr.Type == lisp.LError {
				return lerr
			}
			charged = kib
		}
		return nil
	}
	stack := []*lisp.LVal{v}
	for len(stack) > 0 && size <= limit {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		var n int64
		switch {
		case x.IsNil():
			n = 4
		default:
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
				n = int64(len(x.Cells)) + 2
				stack = appendReversed(stack, x.Cells)
			case lisp.LQuote, lisp.LTaggedVal:
				n = 1
				stack = appendReversed(stack, x.Cells[:1])
			case lisp.LArray:
				if len(x.Cells) == 2 {
					n = int64(len(x.Cells[1].Cells)) + 2
					stack = appendReversed(stack, x.Cells[1].Cells)
				}
			case lisp.LSortMap:
				m := x.Map()
				// Charge the entries before allocating room for them.
				if lerr := add(int64(m.Len()) * 4); lerr != nil {
					return lerr
				}
				buf := make([]*lisp.LVal, m.Len())
				if e := m.Entries(buf); e.Type == lisp.LError {
					return nil
				}
				for i := len(buf) - 1; i >= 0; i-- {
					if buf[i] != nil && len(buf[i].Cells) == 2 {
						stack = append(stack, buf[i].Cells[1], buf[i].Cells[0])
					}
				}
			default:
				return nil // the encoder stopped here
			}
		}
		if lerr := add(n); lerr != nil {
			return lerr
		}
	}
	return nil
}

func appendReversed(stack, cells []*lisp.LVal) []*lisp.LVal {
	for i := len(cells) - 1; i >= 0; i-- {
		stack = append(stack, cells[i])
	}
	return stack
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
