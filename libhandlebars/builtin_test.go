package libhandlebars_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/luthersystems/elps/lisp"
	"github.com/luthersystems/elps/lisp/lisplib"
	"github.com/luthersystems/elps/parser"
	"github.com/luthersystems/svc/libhandlebars"
	"github.com/luthersystems/svc/libhandlebars/hbs"
	"github.com/stretchr/testify/require"
)

// newEnv returns an ELPS environment with the handlebars package loaded and
// a step budget large enough to count every step.
func newEnv(t *testing.T) *lisp.LEnv {
	t.Helper()
	env := lisp.NewEnv(nil)
	env.Runtime.Reader = parser.NewReader()
	require.NotEqual(t, lisp.LError, lisp.InitializeUserEnv(env).Type)
	require.NotEqual(t, lisp.LError, lisplib.LoadLibrary(env).Type)
	require.NotEqual(t, lisp.LError, libhandlebars.LoadPackage(env).Type)
	require.NotEqual(t, lisp.LError, env.InPackage(lisp.String(lisp.DefaultUserPackage)).Type)
	env.Runtime.SetStepBudget(1 << 60)
	return env
}

// eval evaluates src and returns its value and the steps it cost.
func eval(t *testing.T, env *lisp.LEnv, src string) (*lisp.LVal, int64) {
	t.Helper()
	_, before := env.Runtime.StepBudget()
	v := env.LoadStringContext(t.Context(), "test", src)
	_, after := env.Runtime.StepBudget()
	return v, after - before
}

func renderCall(fn, tpl, ctx string) string {
	return "(handlebars:" + fn + " " + strconv.Quote(tpl) + " " + ctx + ")"
}

func TestLibnameVersion(t *testing.T) {
	env := newEnv(t)
	v, _ := eval(t, env, "(handlebars:libname)")
	require.Equal(t, lisp.String("luthersystems/svc/hbs").Str, v.Str)
	v, _ = eval(t, env, "(handlebars:version)")
	require.Equal(t, hbs.Version, v.Str)
}

func TestConditions(t *testing.T) {
	env := newEnv(t)
	for _, tc := range []struct {
		src, cond, msg string
	}{
		{renderCall("render", "{{{x}}", "(sorted-map)"), "handlebars-parse", "error parsing template: Parse error on line 1"},
		{"(handlebars:must-parse \"{{{x}}\")", "handlebars-parse", "error parsing template: Parse error on line 1"},
		{renderCall("render", strings.Repeat("{{#if t}}", 300), "(sorted-map)"), "handlebars-parse", "error parsing template: Parse error on line 1:\ntemplate nesting depth exceeds limit of 256"},
		{"(handlebars:must-parse " + strconv.Quote(strings.Repeat("{{#if t}}", 300)) + ")", "handlebars-parse", "error parsing template: Parse error on line 1:\ntemplate nesting depth exceeds limit of 256"},
		{"(handlebars:must-parse (string:repeat \"x\" 1048577))", "handlebars-parse", "error parsing template: template is 1048577 bytes, limit is 1048576"},
		{renderCall("render", "{{@this}}", "(sorted-map)"), "handlebars-render", "error while rendering template: "},
		{renderCall("render-fixed", "{{@this}}", "(sorted-map)"), "handlebars-render", "error while rendering template: "},
		{renderCall("render", "{{^x}}{{unless t}}{{/x}}", `(sorted-map "t" true)`), "handlebars-render", "error while rendering template: template evaluation exceeds the maximum depth of 256"},
	} {
		v, _ := eval(t, env, tc.src)
		name := tc.src[:min(len(tc.src), 80)]
		require.Equal(t, lisp.LError, v.Type, name)
		require.Equal(t, tc.cond, v.Str, name)
		require.True(t, strings.HasPrefix(v.Cells[0].Str, tc.msg), "%s: %q", name, v.Cells[0].Str)
	}

	// Output over MaxOutputBytes is a render error.
	v, _ := eval(t, env, `(handlebars:render "{{#each a}}{{#each ../a}}xxxxxxxxxxxxxxxx{{/each}}{{/each}}" (sorted-map "a" (make-sequence 0 1100)))`)
	require.Equal(t, lisp.LError, v.Type)
	require.Equal(t, "handlebars-render", v.Str)
	require.Contains(t, v.Cells[0].Str, "output")
}

func TestRenderModes(t *testing.T) {
	env := newEnv(t)
	for _, tc := range []struct {
		tpl, ctx, compat, fixed string
	}{
		{"{{to-str n}}", `(sorted-map "n" 3)`, "3.000000", "3"},
		{"{{to-str 3}}", "(sorted-map)", "3", "3"},
		{`{{round-to-nth "123456789.12" "2"}}`, "(sorted-map)", "123456792.00", "123456789.12"},
		{`{{round-to-nth "1.55806543" "2"}}`, "(sorted-map)", "1.56", "1.56"},
		{`{{round-to-nth "1.2304" "2"}}`, "(sorted-map)", "1.23", "1.23"},
		{`{{round-to-nth "1.0001" "2"}}`, "(sorted-map)", "1.00", "1.00"},
		{`{{round-to-nth "1.999" "2"}}`, "(sorted-map)", "2.00", "2.00"},
		{"{{plus a=0.1 b=0.2 c=0.3}}", "(sorted-map)", "0.6000000000000001", "0.6000000000000001"},
		{"{{to-int (div 1 0)}}", "(sorted-map)", "-9223372036854775808", "-9223372036854775808"},
	} {
		v, _ := eval(t, env, renderCall("render", tc.tpl, tc.ctx))
		require.Equal(t, lisp.LString, v.Type, "%s: %v", tc.tpl, v)
		require.Equal(t, tc.compat, v.Str, tc.tpl)
		v, _ = eval(t, env, renderCall("render-fixed", tc.tpl, tc.ctx))
		require.Equal(t, lisp.LString, v.Type, "%s: %v", tc.tpl, v)
		require.Equal(t, tc.fixed, v.Str, tc.tpl)
	}
}

// TestParseSteps: parse costs one step per started KiB of template on every
// call, cache hit or miss.
func TestParseSteps(t *testing.T) {
	env := newEnv(t)
	tag := t.Name() // not in the process-wide parse cache yet
	steps := func(n int) int64 {
		tpl := tag + strings.Repeat("x", n-len(tag))
		_, miss := eval(t, env, "(handlebars:must-parse "+strconv.Quote(tpl)+")")
		_, hit := eval(t, env, "(handlebars:must-parse "+strconv.Quote(tpl)+")")
		require.Equal(t, miss, hit, "cache hit and miss must cost the same, n=%d", n)
		return miss
	}
	base := steps(1024)
	require.Equal(t, base+1, steps(1025))
	require.Equal(t, base+1, steps(2048))
	require.Equal(t, base+2, steps(2049))
	require.Equal(t, base+99, steps(100*1024))

	// A failed parse costs the same as a good one of the same length.
	_, bad := eval(t, env, "(handlebars:must-parse "+strconv.Quote(tag+"{{{"+strings.Repeat("x", 1021-len(tag)))+")")
	require.Equal(t, base, bad)
}

// TestRenderSteps: render charges the context's JSON, the parse, then the
// engine's work,
// including output by started KiB, and the charge is a pure function of
// (template, context).
func TestRenderSteps(t *testing.T) {
	env := newEnv(t)
	call := func(s string) int64 {
		v, n := eval(t, env, `(handlebars:render "{{s}}" (sorted-map "s" `+strconv.Quote(s)+`))`)
		require.Equal(t, lisp.LString, v.Type, "%v", v)
		require.Equal(t, s, v.Str)
		return n
	}
	empty := call("")
	require.Equal(t, empty, call(""), "same input, same steps")
	// The context's JSON, {"s":"..."}, is len(s)+8 bytes and costs a step
	// per started KiB; the output costs a step per started KiB too.
	require.Equal(t, empty+1, call("x"))                         // ctx 1 KiB, output 1
	require.Equal(t, empty+1, call(strings.Repeat("x", 1016)))   // ctx 1 KiB, output 1
	require.Equal(t, empty+2, call(strings.Repeat("x", 1024)))   // ctx 2 KiB, output 1
	require.Equal(t, empty+4+5, call(strings.Repeat("x", 4097))) // ctx 5 KiB, output 5

	// Iterations cost steps.
	each := func(n int) int64 {
		_, s := eval(t, env, `(handlebars:render "{{#each a}}{{/each}}" (sorted-map "a" (make-sequence 0 `+strconv.Itoa(n)+`)))`)
		return s
	}
	require.Greater(t, each(1000), each(10)+1000)
}

// TestRenderBudget: a render that exhausts the ELPS budget fails with the
// budget condition and stops early.
func TestRenderBudget(t *testing.T) {
	env := newEnv(t)
	env.Runtime.SetStepBudget(5000)
	v, _ := eval(t, env, `(handlebars:render "{{#each a}}{{#each ../a}}x{{/each}}{{/each}}" (sorted-map "a" (make-sequence 0 1000)))`)
	require.Equal(t, lisp.LError, v.Type)
	require.Equal(t, lisp.CondStepBudgetExceeded, v.Str)
	_, used := env.Runtime.StepBudget()
	require.Less(t, used, int64(5000+1000))
}
