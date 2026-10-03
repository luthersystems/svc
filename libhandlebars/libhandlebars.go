package libhandlebars

import (
	"encoding/json"
	"errors"
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
// (Runtime.MaxAlloc) and evaluation context, charged per KiB written. The
// bytes are those libjson's Dump writes.
//
// A failure that is not one of those limits is reported as before, as
// "error while serializing: <Dump's error>": Dump runs again only then, on a
// value the capped walk has already reached the end of or failed inside for
// a reason other than its size.
func dumpContext(env *lisp.LEnv, v *lisp.LVal) ([]byte, *lisp.LVal) {
	s := libjson.DefaultSerializer()
	res := s.DumpBytesBuiltin(env, lisp.SExpr([]*lisp.LVal{v, lisp.Bool(false)}))
	if res.Type != lisp.LError {
		return res.Bytes(), nil
	}
	if isLimitError(res) {
		return nil, res
	}
	if _, err := s.Dump(v, false); err != nil {
		return nil, env.Errorf("error while serializing: %v", err)
	}
	return nil, res
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
