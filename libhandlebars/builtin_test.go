// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"encoding/json"
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/luthersystems/elps/lisp"
	"github.com/luthersystems/elps/lisp/lisplib"
	"github.com/luthersystems/elps/lisp/lisplib/libjson"
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
		// render-fixed keeps ELPS ints as ints (JSON makes them float64s):
		// exact above 2^53, everywhere in the context.
		{"{{n}} {{to-str n}}", `(sorted-map "n" 9007199254740993)`, "9007199254740992 9007199254740992.000000", "9007199254740993 9007199254740993"},
		{"{{#each a}}{{this}},{{/each}}{{m.k}} {{m.[1]}} {{q}} {{v.[0]}}",
			`(sorted-map "a" (list 9007199254740993 1.5 3.0 "s") "m" (sorted-map 'k 9007199254740995 1 9007199254740997) "q" (quote 9007199254740999) "v" (vector 9007199254741001))`,
			"9007199254740992,1.5,3,s,9007199254740996 9007199254740996 9007199254741000 9007199254741000",
			"9007199254740993,1.5,3,s,9007199254740995 9007199254740997 9007199254740999 9007199254741001"},
		{"{{#if z includeZero=true}}y{{else}}n{{/if}} {{to-str f}}", `(sorted-map "z" 0 "f" 2.0)`, "n 2.000000", "y 2"},
	} {
		v, _ := eval(t, env, renderCall("render", tc.tpl, tc.ctx))
		require.Equal(t, lisp.LString, v.Type, "%s: %v", tc.tpl, v)
		require.Equal(t, tc.compat, v.Str, tc.tpl)
		v, _ = eval(t, env, renderCall("render-fixed", tc.tpl, tc.ctx))
		require.Equal(t, lisp.LString, v.Type, "%s: %v", tc.tpl, v)
		require.Equal(t, tc.fixed, v.Str, tc.tpl)
	}
}

// TestParseSteps: parse costs hbs.ParseCost on every call, cache hit or
// miss: 6 steps per lexer token and a step per started 16 bytes.
func TestParseSteps(t *testing.T) {
	env := newEnv(t)
	tag := t.Name() // not in the process-wide parse cache yet
	steps := func(tpl string) int64 {
		_, miss := eval(t, env, "(handlebars:must-parse "+strconv.Quote(tpl)+")")
		_, hit := eval(t, env, "(handlebars:must-parse "+strconv.Quote(tpl)+")")
		require.Equal(t, miss, hit, "cache hit and miss must cost the same: %.40q", tpl)
		return miss
	}
	text := func(n int) int64 { return steps(tag + strings.Repeat("x", n-len(tag))) }
	base := text(1024) // one content token and EOF: 12 + 64
	require.Equal(t, base+1, text(1025))
	require.Equal(t, base+64, text(2048))
	require.Equal(t, base+65, text(2049))
	require.Equal(t, base+6336, text(100*1024))

	// Each {{a}} is 3 tokens (open, id, close) and 5 bytes: 1000 more cost
	// 18,000 steps for the tokens and 312 or 313 for the 5,000 bytes.
	dense := func(k int) int64 { return steps(tag + "|" + strings.Repeat("{{a}}", k)) }
	require.InDelta(t, dense(1000)+6*3*1000+312, dense(2000), 1)

	// A failed parse costs what the tokens up to the error do, hit or miss.
	steps(tag + "{{{" + strings.Repeat("x", 1021-len(tag)))
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
	// The context's JSON, {"s":"..."}, is len(s)+8 bytes.
	//
	// Measured against the empty string, as the sum of: the encode walk's
	// estimate (a step per started KiB) and its escape scan (per started 64
	// bytes of the string), json:dump-bytes (per whole KiB), the decode's
	// byte scan (per started 16 bytes of the JSON) and token pass (1 + a
	// step per started 8 bytes of the string), the output (per started
	// KiB), the escape scan (per started 16 bytes) and the copy (per whole
	// 16 bytes).
	require.Equal(t, empty+4, call("x"))
	require.Equal(t, empty+336, call(strings.Repeat("x", 1016)))
	require.Equal(t, empty+340, call(strings.Repeat("x", 1024)))
	require.Equal(t, empty+1364, call(strings.Repeat("x", 4097)))

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

// TestContextSerializationCapped: the context is serialized under the
// runtime's allocation cap, as json:dump-bytes is, so a value that shares
// structure cannot expand into gigabytes of JSON.
func TestContextSerializationCapped(t *testing.T) {
	env := newEnv(t)
	v := lisp.QExpr([]*lisp.LVal{lisp.Int(1)})
	for range 20 {
		v = lisp.QExpr([]*lisp.LVal{v, v})
	}
	ctx := lisp.SortedMap()
	ctx.MapSetString("a", v)
	env.Put(lisp.Symbol("big"), ctx)
	env.Runtime.MaxAlloc = 1024

	dump, _ := eval(t, env, `(json:dump-bytes big)`)
	require.Equal(t, lisp.LError, dump.Type)
	res, _ := eval(t, env, `(handlebars:render "" big)`)
	require.Equal(t, lisp.LError, res.Type)
	require.Equal(t, dump.Cells[0].Str, res.Cells[0].Str)
	require.True(t, strings.HasPrefix(res.Cells[0].Str, "allocation size exceeds maximum"), res.Cells[0].Str)

	// Other serialization errors keep their text exactly.
	fn, _ := eval(t, env, `(sorted-map "f" (lambda () 1))`)
	_, derr := libjson.Dump(fn, false)
	require.Error(t, derr)
	res, _ = eval(t, env, `(handlebars:render "" (sorted-map "f" (lambda () 1)))`)
	require.Equal(t, lisp.LError, res.Type)
	require.Equal(t, "error while serializing: "+derr.Error(), res.Cells[0].Str)

	// A non-size error never falls back to the unbounded serializer: with a
	// small value depth limit, a too-deep list next to a large shared
	// structure fails fast and allocates little.
	env.Runtime.MaxAlloc = 0
	deep := lisp.Int(1)
	for range 1100 {
		deep = lisp.QExpr([]*lisp.LVal{deep})
	}
	dag := lisp.QExpr([]*lisp.LVal{lisp.Int(1)})
	for range 24 {
		dag = lisp.QExpr([]*lisp.LVal{dag, dag})
	}
	both := lisp.SortedMap()
	both.MapSetString("a", deep)
	both.MapSetString("b", dag)
	env.Put(lisp.Symbol("both"), both)
	env.Runtime.MaxValueDepth = 1024
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	res, _ = eval(t, env, `(handlebars:render "" both)`)
	runtime.ReadMemStats(&after)
	require.Equal(t, lisp.LError, res.Type)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(64<<20))
}

// TestFailedEncodeCharged: a context encode that fails is charged for the
// JSON it got through, so a failure late in a large value costs steps in
// proportion, and runs out of budget instead of running on.
func TestFailedEncodeCharged(t *testing.T) {
	env := newEnv(t)
	failing := func(n int) *lisp.LVal {
		cells := make([]*lisp.LVal, n)
		for i := range cells {
			cells[i] = lisp.Int(0)
		}
		ctx := lisp.SortedMap()
		ctx.MapSetString("a", lisp.QExpr(cells))
		ctx.MapSetString("b", lisp.FunInPackage("user", "f", lisp.Formals(), func(*lisp.LEnv, *lisp.LVal) *lisp.LVal { return lisp.Nil() }))
		return ctx
	}
	steps := func(n int) (*lisp.LVal, int64) {
		env.Put(lisp.Symbol("ctx"), failing(n))
		return eval(t, env, `(handlebars:render "" ctx)`)
	}
	small, sSteps := steps(100)
	require.Equal(t, lisp.LError, small.Type)
	require.True(t, strings.HasPrefix(small.Cells[0].Str, "error while serializing: invalid type encountered"), small.Cells[0].Str)
	large, lSteps := steps(1_000_000)
	require.Equal(t, small.Cells[0].Str, large.Cells[0].Str)
	require.Greater(t, lSteps-sSteps, int64(1500)) // "0" and a comma, 1M times: about 2 MB

	env.Runtime.SetStepBudget(1000)
	res, _ := steps(1_000_000)
	require.Equal(t, lisp.CondStepBudgetExceeded, res.Str)
}

// TestEncodeWalkStopsWhereEncoderDoes: the charging walk stops at a value
// that contains itself, as the encoder does, so the encoder's own error is
// returned, not a budget error from walking the cycle.
func TestEncodeWalkStopsWhereEncoderDoes(t *testing.T) {
	env := newEnv(t)
	self := lisp.QExpr([]*lisp.LVal{lisp.Int(1)})
	self.Cells = append(self.Cells, self)
	ctx := lisp.SortedMap()
	ctx.MapSetString("a", self)
	env.Put(lisp.Symbol("ctx"), ctx)
	want, _ := eval(t, env, `(json:dump-bytes ctx)`)
	require.Equal(t, lisp.LError, want.Type)
	env.Runtime.SetStepBudget(1000)
	got, _ := eval(t, env, `(handlebars:render "" ctx)`)
	require.Equal(t, lisp.LError, got.Type)
	require.NotEqual(t, lisp.CondStepBudgetExceeded, got.Str, got.Cells[0].Str)
	require.Equal(t, want.Cells[0].Str, strings.TrimPrefix(got.Cells[0].Str, "error while serializing: "))
}

// allocDuring returns the bytes allocated by f.
func allocDuring(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// encodeAllocBound is the most a context encode may allocate for the steps
// it was charged: 8 bytes for each byte of charged work (a step stands for
// at most about a KiB) and of its input, plus 1 MiB.
func encodeAllocBound(steps int64, input int) uint64 {
	return 8*(uint64(steps)*1024+uint64(input)) + 1<<20 //nolint:gosec // non-negative
}

// TestEncodeWalkReview covers the context-encode gaps found in review: a
// string charged at its escaped length, a native's JSON charged by its
// tokens, a large map's collection charged before the allocation cap
// stops it, and an invalid number ending the walk where the encoder fails.
func TestEncodeWalkReview(t *testing.T) {
	fn := lisp.FunInPackage("user", "f", lisp.Formals(), func(*lisp.LEnv, *lisp.LVal) *lisp.LVal { return lisp.Nil() })

	t.Run("escaped string then a function", func(t *testing.T) {
		env := newEnv(t)
		ctx := lisp.SortedMap()
		ctx.MapSetString("a", lisp.String(strings.Repeat("\x01", 1<<20)))
		ctx.MapSetString("b", fn)
		env.Put(lisp.Symbol("ctx"), ctx)
		var res *lisp.LVal
		var steps int64
		alloc := allocDuring(func() { res, steps = eval(t, env, `(handlebars:render "" ctx)`) })
		require.Equal(t, lisp.LError, res.Type)
		require.GreaterOrEqual(t, steps, int64(6<<10), "6 MiB of escapes")
		require.LessOrEqual(t, alloc, encodeAllocBound(steps, 1<<20), "%d bytes for %d steps", alloc, steps)
	})

	t.Run("native JSON then an invalid number", func(t *testing.T) {
		env := newEnv(t)
		raw := json.RawMessage("[" + strings.TrimSuffix(strings.Repeat("{},", 100_000), ",") + "]")
		ctx := lisp.SortedMap()
		ctx.MapSetString("a", lisp.Native(raw))
		ctx.MapSetString("b", lisp.Float(math.NaN()))
		env.Put(lisp.Symbol("ctx"), ctx)
		var res *lisp.LVal
		var steps int64
		alloc := allocDuring(func() { res, steps = eval(t, env, `(handlebars:render "" ctx)`) })
		require.Equal(t, lisp.LError, res.Type)
		require.Contains(t, res.Cells[0].Str, "NaN")
		require.GreaterOrEqual(t, steps, int64(300_000), "the native's 100k objects")
		require.LessOrEqual(t, alloc, encodeAllocBound(steps, len(raw)), "%d bytes for %d steps", alloc, steps)
	})

	t.Run("large map over the allocation cap", func(t *testing.T) {
		env := newEnv(t)
		m := lisp.SortedMap()
		for i := range 10_000 {
			m.MapSetString(strconv.Itoa(i), lisp.Int(i))
		}
		ctx := lisp.SortedMap()
		ctx.MapSetString("m", m)
		env.Put(lisp.Symbol("ctx"), ctx)
		env.Runtime.MaxAlloc = 1024
		var res *lisp.LVal
		var steps int64
		alloc := allocDuring(func() { res, steps = eval(t, env, `(handlebars:render "" ctx)`) })
		require.Equal(t, lisp.LError, res.Type)
		require.GreaterOrEqual(t, steps, int64(10_000*14), "collecting and sorting 10k entries")
		require.LessOrEqual(t, alloc, encodeAllocBound(steps, 0), "%d bytes for %d steps", alloc, steps)
	})

	t.Run("invalid number before a large list", func(t *testing.T) {
		env := newEnv(t)
		cells := make([]*lisp.LVal, 10_000)
		for i := range cells {
			cells[i] = lisp.Int(i)
		}
		ctx := lisp.SortedMap()
		ctx.MapSetString("a", lisp.Float(math.NaN()))
		ctx.MapSetString("b", lisp.QExpr(cells))
		env.Put(lisp.Symbol("ctx"), ctx)
		env.Runtime.SetStepBudget(1000)
		res, _ := eval(t, env, `(handlebars:render "" ctx)`)
		require.Equal(t, lisp.LError, res.Type)
		require.NotEqual(t, lisp.CondStepBudgetExceeded, res.Str)
		require.Contains(t, res.Cells[0].Str, "unable to encode number NaN")
	})
}
