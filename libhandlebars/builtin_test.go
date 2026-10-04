// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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

type nativeItem struct {
	A int
	B string
}

// onceMarshaler fails on its second call, as a stateful embedder type might.
type onceMarshaler struct{ calls *int }

func (m onceMarshaler) MarshalJSON() ([]byte, error) {
	*m.calls++
	if *m.calls > 1 {
		return nil, errors.New("called twice")
	}
	return []byte(`{"x": "once"}`), nil
}

type failingMarshaler struct{}

func (failingMarshaler) MarshalJSON() ([]byte, error) { return nil, errors.New("boom") }

// TestEncodeNatives: a native is marshalled once and charged before the
// encode, whether encoding/json reflects through it or calls its
// MarshalJSON, and a failing native reports the encoder's text.
func TestEncodeNatives(t *testing.T) {
	t.Run("reflective native then NaN", func(t *testing.T) {
		env := newEnv(t)
		item := &nativeItem{A: 1, B: "b"}
		items := make([]*nativeItem, 100_000)
		for i := range items {
			items[i] = item
		}
		ctx := lisp.SortedMap()
		ctx.MapSetString("a", lisp.Native(items))
		ctx.MapSetString("b", lisp.Float(math.NaN()))
		env.Put(lisp.Symbol("ctx"), ctx)
		var res *lisp.LVal
		var steps int64
		alloc := allocDuring(func() { res, steps = eval(t, env, `(handlebars:render "" ctx)`) })
		require.Equal(t, lisp.LError, res.Type)
		require.Contains(t, res.Cells[0].Str, "unable to encode number NaN")
		require.GreaterOrEqual(t, steps, int64(500_000), "100k structs walked")
		require.LessOrEqual(t, alloc, encodeAllocBound(steps, 0), "%d bytes for %d steps", alloc, steps)
	})

	t.Run("marshaler called once", func(t *testing.T) {
		env := newEnv(t)
		calls := 0
		ctx := lisp.SortedMap()
		ctx.MapSetString("a", lisp.Native(onceMarshaler{calls: &calls}))
		env.Put(lisp.Symbol("ctx"), ctx)
		res, _ := eval(t, env, `(handlebars:render "{{a.x}}" ctx)`)
		require.Equal(t, lisp.LString, res.Type, "%v", res)
		require.Equal(t, "once", res.Str)
		require.Equal(t, 1, calls)
	})

	t.Run("failing marshaler", func(t *testing.T) {
		env := newEnv(t)
		ctx := lisp.SortedMap()
		ctx.MapSetString("a", lisp.Native(failingMarshaler{}))
		ctx.MapSetString("b", lisp.Native(make(chan int)))
		env.Put(lisp.Symbol("ctx"), ctx)
		dump, _ := eval(t, env, `(json:dump-bytes ctx)`)
		require.Equal(t, lisp.LError, dump.Type)
		res, _ := eval(t, env, `(handlebars:render "" ctx)`)
		require.Equal(t, lisp.LError, res.Type)
		require.Equal(t, "error while serializing: "+dump.Cells[0].Str, res.Cells[0].Str)
	})

	t.Run("output unchanged", func(t *testing.T) {
		env := newEnv(t)
		ctx := lisp.SortedMap()
		ctx.MapSetString("a", lisp.Native(map[string]any{"k": []any{1, "<&>", 2.5}, "n": nil}))
		ctx.MapSetString("q", lisp.QExpr([]*lisp.LVal{lisp.Native(json.RawMessage(`{"z": 1}`)), lisp.Int(2)}))
		env.Put(lisp.Symbol("ctx"), ctx)
		res, _ := eval(t, env, `(handlebars:render "{{#each a.k}}{{this}},{{/each}}{{a.n}}|{{q.[0].z}}{{q.[1]}}" ctx)`)
		require.Equal(t, lisp.LString, res.Type, "%v", res)
		require.Equal(t, "1,&lt;&amp;&gt;,2.5,|12", res.Str)
	})
}

type nativeCycle struct {
	Arr  [2000]int
	Self *nativeCycle
}

type nativeHidden struct {
	big map[int]int // unexported: encoding/json skips it
	N   int
}

type nativeDAG struct {
	l, r *nativeDAG // unexported: encoding/json skips them
	V    int
}

// TestEncodeNativesAsJSON: the walk charges a native as encoding/json
// encodes it, and fails where and as it fails, without running it on a
// cycle: a pointer cycle, a map holding itself, unexported fields skipped,
// no depth bound encoding/json lacks, and the same steps on every run.
func TestEncodeNativesAsJSON(t *testing.T) {
	render := func(t *testing.T, env *lisp.LEnv, native any) (*lisp.LVal, *lisp.LVal, int64, time.Duration) {
		t.Helper()
		ctx := lisp.SortedMap()
		ctx.MapSetString("n", lisp.Native(native))
		env.Put(lisp.Symbol("ctx"), ctx)
		dump, _ := eval(t, env, `(json:dump-bytes ctx)`)
		// Best of 3: one slow sample under a busy machine does not decide.
		var res *lisp.LVal
		var steps int64
		best := time.Duration(math.MaxInt64)
		for range 3 {
			start := time.Now()
			res, steps = eval(t, env, `(handlebars:render "{{n.N}}" ctx)`)
			best = min(best, time.Since(start))
		}
		return dump, res, steps, best
	}
	t.Run("pointer cycle", func(t *testing.T) {
		x := &nativeCycle{}
		x.Self = x
		dump, res, steps, d := render(t, newEnv(t), x)
		require.Equal(t, lisp.LError, res.Type)
		require.Equal(t, "error while serializing: "+dump.Cells[0].Str, res.Cells[0].Str)
		if !raceEnabled { // a timing check, as the ceiling tests are
			require.Less(t, d.Nanoseconds()/max(steps, 1), int64(1000), "%v for %d steps", d, steps)
		}
	})
	t.Run("map holding itself", func(t *testing.T) {
		m := map[string]any{}
		m["self"] = m
		dump, res, _, d := render(t, newEnv(t), m)
		require.Equal(t, lisp.LError, res.Type)
		require.Equal(t, "error while serializing: "+dump.Cells[0].Str, res.Cells[0].Str)
		if !raceEnabled {
			require.Less(t, d, time.Second)
		}
	})
	t.Run("unexported fields skipped", func(t *testing.T) {
		big := make(map[int]int, 200_000)
		for i := range 200_000 {
			big[i] = i
		}
		var dag *nativeDAG
		for range 30 {
			dag = &nativeDAG{l: dag, r: dag, V: 1}
		}
		for _, n := range []any{nativeHidden{big: big, N: 7}, dag} {
			_, res, steps, _ := render(t, newEnv(t), n)
			require.Equal(t, lisp.LString, res.Type, "%v", res)
			require.Less(t, steps, int64(1000), "%T", n)
		}
	})
	t.Run("no depth bound, same steps", func(t *testing.T) {
		var deep any = 1
		for range 2100 {
			deep = []any{deep}
		}
		n := map[string]any{"a": deep, "b": make([]int, 10_000), "c": make([]int, 10_000), "d": make([]int, 10_000)}
		env := newEnv(t)
		env.Runtime.MaxValueDepth = 2000
		var first int64
		for i := range 30 {
			dump, res, steps, _ := render(t, env, n)
			require.Equal(t, lisp.LBytes, dump.Type, "%v", dump)
			require.Equal(t, lisp.LString, res.Type, "%v", res)
			if i == 0 {
				first = steps
			}
			require.Equal(t, first, steps, "run %d", i)
		}
	})
}

// embedChain returns a struct type embedding n levels deep, with a Leaf
// field promoted from the bottom.
func embedChain(n int) reflect.Type {
	t := reflect.StructOf([]reflect.StructField{{Name: "Leaf", Type: reflect.TypeFor[int]()}})
	for i := range n {
		t = reflect.StructOf([]reflect.StructField{
			{Name: "E" + strconv.Itoa(i), Type: t, Anonymous: true},
			{Name: "X" + strconv.Itoa(i), Type: reflect.TypeFor[int]()},
		})
	}
	return t
}

// TestNativeCostCeiling: natives whose encoding visits many skipped or
// deeply promoted fields stay within the cost model's ceiling, end to end.
func TestNativeCostCeiling(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing test: skipped under -race and -short")
	}
	sparseFields := make([]reflect.StructField, 2000)
	for i := range sparseFields {
		sparseFields[i] = reflect.StructField{Name: "F" + strconv.Itoa(i), Type: reflect.TypeFor[int](), Tag: `json:",omitempty"`}
	}
	sparse := reflect.MakeSlice(reflect.SliceOf(reflect.StructOf(sparseFields)), 2000, 2000).Interface()
	deep := reflect.MakeSlice(reflect.SliceOf(embedChain(1000)), 300, 300).Interface()
	for name, native := range map[string]any{"2000 empty omitempty fields x2000": sparse, "1000-level embedding x300": deep} {
		env := newEnv(t)
		env.Runtime.MaxAlloc = 1 << 30
		ctx := lisp.SortedMap()
		ctx.MapSetString("n", lisp.Native(native))
		env.Put(lisp.Symbol("ctx"), ctx)
		best := 0.0
		for range 3 {
			start := time.Now()
			res, steps := eval(t, env, `(handlebars:render "x" ctx)`)
			require.Equal(t, lisp.LString, res.Type, "%v", res)
			if per := float64(time.Since(start).Nanoseconds()) / float64(steps); best == 0 || per < best {
				best = per
			}
		}
		t.Logf("%-36s %.0f ns/step", name, best)
		if ceilingFails(t, best) {
			t.Errorf("%s: %.0f ns per step, ceiling 200", name, best)
		}
	}
}

// TestEncodeNativesBounded covers the native-encode review cases: nothing
// past where the charged walk stopped is visited uncharged, a native past
// the allocation cap fails as json:dump-bytes does without being
// marshalled, and a type-name error text is charged by its length.
func TestEncodeNativesBounded(t *testing.T) {
	t.Run("native then NaN then a large list", func(t *testing.T) {
		env := newEnv(t)
		cells := make([]*lisp.LVal, 1_000_000)
		for i := range cells {
			cells[i] = lisp.Int(i)
		}
		ctx := lisp.SortedMap()
		ctx.MapSetString("a", lisp.Native(1))
		ctx.MapSetString("b", lisp.Float(math.NaN()))
		ctx.MapSetString("c", lisp.QExpr(cells))
		env.Put(lisp.Symbol("ctx"), ctx)
		// The million cells past the NaN are never visited (they took
		// about 10 ms, for 44 steps): the call is a fixed overhead. Best
		// of 5, so a GC or a busy machine does not decide it.
		var best time.Duration
		var steps int64
		for i := range 5 {
			start := time.Now()
			var res *lisp.LVal
			res, steps = eval(t, env, `(handlebars:render "" ctx)`)
			d := time.Since(start)
			require.Equal(t, lisp.LError, res.Type)
			require.Contains(t, res.Cells[0].Str, "NaN")
			if i == 0 || d < best {
				best = d
			}
		}
		require.Less(t, best, 3*time.Millisecond, "%v for %d steps", best, steps)
	})
	t.Run("native past the allocation cap", func(t *testing.T) {
		env := newEnv(t)
		ctx := lisp.SortedMap()
		ctx.MapSetString("a", lisp.Native(make([]byte, 8<<20)))
		env.Put(lisp.Symbol("ctx"), ctx)
		env.Runtime.MaxAlloc = 1024
		dump, _ := eval(t, env, `(json:dump-bytes ctx)`)
		require.Equal(t, lisp.LError, dump.Type)
		var res *lisp.LVal
		var steps int64
		alloc := allocDuring(func() { res, steps = eval(t, env, `(handlebars:render "" ctx)`) })
		require.Equal(t, lisp.LError, res.Type)
		require.Equal(t, dump.Cells[0].Str, res.Cells[0].Str)
		require.Less(t, steps, int64(20_000), "its estimate's KiB, not its encoding")
		require.Less(t, alloc, uint64(4<<20), "the 8 MiB native is not marshalled")
	})
	t.Run("type name with a 1 MiB tag", func(t *testing.T) {
		env := newEnv(t)
		elem := reflect.StructOf([]reflect.StructField{{Name: "A", Type: reflect.TypeFor[int](), Tag: reflect.StructTag(`big:"` + strings.Repeat("x", 1<<20) + `"`)}})
		ctx := lisp.SortedMap()
		ctx.MapSetString("a", lisp.Native(reflect.MakeChan(reflect.ChanOf(reflect.BothDir, elem), 0).Interface()))
		env.Put(lisp.Symbol("ctx"), ctx)
		res, steps := eval(t, env, `(handlebars:render "" ctx)`)
		require.Equal(t, lisp.LError, res.Type)
		require.Contains(t, res.Cells[0].Str, "json: unsupported type: chan struct")
		require.GreaterOrEqual(t, steps, int64(1<<16), "the 1 MiB name is charged")
	})
}

// TestEncodeNativesStringOption: a ",string" field, written escaped twice,
// is sized as such, so a native over the allocation cap fails before it is
// marshalled; a nil Marshaler interface is null, not a panic.
func TestEncodeNativesStringOption(t *testing.T) {
	env := newEnv(t)
	ctx := lisp.SortedMap()
	ctx.MapSetString("a", lisp.Native(struct {
		S string `json:",string"`
	}{S: strings.Repeat(`"`, 4<<20)}))
	env.Put(lisp.Symbol("ctx"), ctx)
	dump, _ := eval(t, env, `(json:dump-bytes ctx)`)
	require.Equal(t, lisp.LError, dump.Type)
	var res *lisp.LVal
	alloc := allocDuring(func() { res, _ = eval(t, env, `(handlebars:render "" ctx)`) })
	require.Equal(t, lisp.LError, res.Type)
	require.Equal(t, dump.Cells[0].Str, res.Cells[0].Str)
	require.Less(t, alloc, uint64(8<<20), "not marshalled")

	ctx = lisp.SortedMap()
	ctx.MapSetString("a", lisp.Native(struct {
		J json.Marshaler
		F float64
	}{F: math.NaN()}))
	env.Put(lisp.Symbol("ctx"), ctx)
	res, _ = eval(t, env, `(handlebars:render "" ctx)`)
	require.Equal(t, lisp.LError, res.Type)
	require.Equal(t, "error while serializing: json: unsupported value: NaN", res.Cells[0].Str)
}

// TestNativeColdEmbedding: the first use of a deeply embedded struct type
// in a native is charged by depth, before encoding/json builds its fields.
func TestNativeColdEmbedding(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing test: skipped under -race and -short")
	}
	// Each run builds a cold type; the best of the later runs decides, so
	// one slow sample under a busy machine does not.
	best := math.Inf(1)
	for run := range 4 {
		chain := embedChain(1000)
		// A field unique to this run makes a type no one has built.
		cold := reflect.StructOf([]reflect.StructField{
			{Name: "C", Type: chain, Anonymous: true},
			{Name: "Run" + strconv.Itoa(run+100*int(time.Now().UnixNano()%1000)), Type: reflect.TypeFor[int]()},
		})
		env := newEnv(t)
		ctx := lisp.SortedMap()
		ctx.MapSetString("n", lisp.Native(reflect.New(cold).Elem().Interface()))
		env.Put(lisp.Symbol("ctx"), ctx)
		start := time.Now()
		res, steps := eval(t, env, `(handlebars:render "x" ctx)`)
		require.Equal(t, lisp.LString, res.Type, "%v", res)
		per := float64(time.Since(start).Nanoseconds()) / float64(steps)
		t.Logf("cold 1000-level embedding: %d steps, %.0f ns/step", steps, per)
		if run > 0 {
			best = min(best, per)
		}
	}
	require.False(t, ceilingFails(t, best), "%.0f ns/step", best)
}

type hiddenBig struct {
	Big [4 << 20]byte `json:"-"`
	N   int
}

type zeroBig struct {
	Z [4 << 20]byte `json:",omitzero"`
}

// renderNative renders "x" with ctx {"n": native} and returns the time per
// step and the bytes allocated.
func renderNative(t *testing.T, native any) (*lisp.LVal, float64, uint64) {
	t.Helper()
	env := newEnv(t)
	env.Runtime.MaxAlloc = 1 << 30
	ctx := lisp.SortedMap()
	ctx.MapSetString("n", lisp.Native(native))
	env.Put(lisp.Symbol("ctx"), ctx)
	var res *lisp.LVal
	var steps int64
	var d time.Duration
	alloc := allocDuring(func() {
		start := time.Now()
		res, steps = eval(t, env, `(handlebars:render "x" ctx)`)
		d = time.Since(start)
	})
	return res, float64(d.Nanoseconds()) / float64(max(steps, 1)), alloc
}

// bestNative is renderNative's best of 3 runs, each on a fresh value from
// native, so one slow sample under a busy machine does not decide a
// timing check. It returns the last run's result and allocation.
func bestNative(t *testing.T, native func() any) (*lisp.LVal, float64, uint64) {
	t.Helper()
	var res *lisp.LVal
	var alloc uint64
	best := math.Inf(1)
	for range 3 {
		var per float64
		res, per, alloc = renderNative(t, native())
		best = min(best, per)
	}
	return res, best, alloc
}

// TestNativeResourceReview covers the resource-accounting review cases on
// natives: an invalid json.Number, map entry copies, pointer and interface
// hops, omitzero scans, long map keys and cold deep embedding are charged
// before the work, within the cost model's ceiling.
func TestNativeResourceReview(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing test: skipped under -race and -short")
	}
	var hops any = map[string]int{}
	for range 5000 {
		prev := hops
		hops = &prev
	}
	shared := &zeroBig{}
	rows := make([]*zeroBig, 200)
	for i := range rows {
		rows[i] = shared
	}
	cold := 0
	for name, c := range map[string]struct {
		native  func() any // a fresh value each run: a cold type stays cold
		wantErr string
	}{
		"invalid json.Number":   {func() any { return map[string]any{"n": json.Number("1" + strings.Repeat("\x00", 4<<20))} }, "invalid number literal"},
		"map of hidden structs": {func() any { return map[string]hiddenBig{"a": {}} }, ""},
		"5000 pointer hops":     {func() any { return hops }, ""},
		"omitzero 4 MiB x200":   {func() any { return rows }, ""},
		"4 MiB key then chan":   {func() any { return map[string]any{strings.Repeat("k", 4<<20): make(chan int)} }, "unsupported type: chan int"},
		"cold 2000-level chain": {func() any {
			cold++
			return reflect.New(reflect.StructOf([]reflect.StructField{{Name: "C", Type: embedChain(2000), Anonymous: true}, {Name: "Cold" + strconv.Itoa(cold), Type: reflect.TypeFor[int]()}})).Elem().Interface()
		}, ""},
	} {
		res, per, alloc := bestNative(t, c.native)
		if c.wantErr == "" {
			require.Equal(t, lisp.LString, res.Type, "%s: %v", name, res)
		} else {
			require.Equal(t, lisp.LError, res.Type, name)
			require.Contains(t, res.Cells[0].Str, c.wantErr, name)
		}
		t.Logf("%-24s %6.0f ns/step %6d KB", name, per, alloc>>10)
		require.False(t, ceilingFails(t, per), "%s: %.0f ns/step", name, per)
	}
}

// nestedSlices returns n levels of []any around 1.
func nestedSlices(n int) any {
	var v any = 1
	for range n {
		v = []any{v}
	}
	return v
}

// pointerChain returns n pointer-to-interface hops around a map.
func pointerChain(n int) any {
	var x any = map[string]any{"x": 1}
	for range n {
		y := x
		x = &y
	}
	return x
}

// segmentDAG returns k segments, each nesting the one before it n levels
// deeper ([]any wrappers, or pointer-to-interface hops with ptrs): shared,
// so the value is small, but nested k*n deep for json.Marshal, which has
// no memo.
func segmentDAG(k, n int, ptrs bool) any {
	segs := make([]any, k)
	var prev any = 1
	for i := range segs {
		v := prev
		for range n {
			if ptrs {
				y := v
				v = &y
			} else {
				v = []any{v}
			}
		}
		segs[i], prev = v, v
	}
	return segs
}

// deepRender renders a native and the Go API's JSON mode on a fresh
// goroutine, as an endorser would, and returns both errors.
func deepRender(t *testing.T, v any) (*lisp.LVal, error) {
	t.Helper()
	var res *lisp.LVal
	var gerr error
	env := newEnv(t)
	env.Runtime.MaxAlloc = 1 << 30
	ctx := lisp.SortedMap()
	ctx.MapSetString("n", lisp.Native(v))
	env.Put(lisp.Symbol("ctx"), ctx)
	tpl, err := libhandlebars.Parse(`x`)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		res = env.LoadStringContext(t.Context(), "test", `(handlebars:render "x" ctx)`)
		_, gerr = libhandlebars.RenderWith(tpl, map[string]any{"n": v}, libhandlebars.WithJSONContext())
	}()
	<-done
	return res, gerr
}

// TestNativeDepthBound: a value nested past the walk's bound fails with a
// limit error on a fresh goroutine, never a stack overflow; one just under
// it renders.
func TestNativeDepthBound(t *testing.T) {
	res, gerr := deepRender(t, nestedSlices(60_000))
	require.Equal(t, lisp.LError, res.Type)
	require.Contains(t, res.Cells[0].Str, "nests deeper than 50000")
	require.ErrorContains(t, gerr, "nests deeper than 1024", "the Go API's container bound comes first")

	res, gerr = deepRender(t, pointerChain(30_000))
	require.Equal(t, lisp.LError, res.Type)
	require.Contains(t, res.Cells[0].Str, "nests deeper than 50000")
	require.ErrorContains(t, gerr, "nests deeper than 50000")

	// A []any level is two walk levels (the slice, its interface element).
	// Past encoding/json's decoder depth (10,000) the encoder's own load
	// check fails: the same error as json:dump-bytes.
	for _, n := range []int{24_000, 9_000} {
		env := newEnv(t)
		env.Runtime.MaxAlloc = 1 << 30
		ctx := lisp.SortedMap()
		ctx.MapSetString("n", lisp.Native(nestedSlices(n)))
		env.Put(lisp.Symbol("ctx"), ctx)
		dump, _ := eval(t, env, `(json:dump-bytes ctx)`)
		res, _ = eval(t, env, `(handlebars:render "x" ctx)`)
		if dump.Type == lisp.LError {
			require.Equal(t, lisp.LError, res.Type, "%d: %v", n, res)
			require.Equal(t, "error while serializing: "+dump.Cells[0].Str, res.Cells[0].Str, n)
		} else {
			require.Equal(t, lisp.LString, res.Type, "%d: %v", n, res)
		}
	}
}

// TestNativeDepthNoCrash runs a million levels in a subprocess: the render
// must fail with an error, not abort the process.
func TestNativeDepthNoCrash(t *testing.T) {
	if os.Getenv("HBS_DEEP_CHILD") == "1" {
		for _, v := range []any{nestedSlices(1_000_000), pointerChain(500_000), segmentDAG(60, 24_000, false), segmentDAG(60, 24_000, true)} {
			res, gerr := deepRender(t, v)
			if res.Type != lisp.LError || gerr == nil {
				t.Fatalf("no error: %v %v", res, gerr)
			}
		}
		fmt.Println("DEEP-OK")
		return
	}
	if testing.Short() {
		t.Skip("slow: skipped under -short")
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNativeDepthNoCrash$") //nolint:gosec // this test binary
	cmd.Env = append(os.Environ(), "HBS_DEEP_CHILD=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.Contains(t, string(out), "DEEP-OK")
}

// TestNativeDAGDepth: a native whose shared subtrees nest it past the
// walk's level bound fails that bound, though the walk charges a shared
// subtree from its memo: json.Marshal would walk it all.
func TestNativeDAGDepth(t *testing.T) {
	for _, ptrs := range []bool{false, true} {
		env := newEnv(t)
		ctx := lisp.SortedMap()
		ctx.MapSetString("n", lisp.Native(segmentDAG(3, 24_000, ptrs)))
		env.Put(lisp.Symbol("ctx"), ctx)
		res, _ := eval(t, env, `(handlebars:render "x" ctx)`)
		require.Equal(t, lisp.LError, res.Type, "pointers %v: %v", ptrs, res)
		require.Contains(t, res.String(), "nests deeper than 50000", "pointers %v", ptrs)
	}
}

type zeroFloats struct {
	A [1 << 22]float32 `json:",omitzero"`
}

type zeroPadded struct {
	A [1 << 20]struct {
		I int8
		F float32
	} `json:",omitzero"`
}

type zeroQuads struct {
	A [1 << 20][4]float32 `json:",omitzero"`
}

// TestOmitzeroArraysCeiling: omitzero on large arrays that are not plain
// memory (IsZero tests them element by element, twice) stays within the
// ceiling, through the Go API's JSON mode.
func TestOmitzeroArraysCeiling(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing test: skipped under -race and -short")
	}
	for name, v := range map[string]any{"float32 x4M": &zeroFloats{}, "padded structs x1M": &zeroPadded{}, "[4]float32 x1M": &zeroQuads{}} {
		res, per, _ := bestNative(t, func() any { return v })
		require.Equal(t, lisp.LString, res.Type, "%s: %v", name, res)
		t.Logf("%-20s %.0f ns/step", name, per)
		require.False(t, ceilingFails(t, per), "%s: %.0f ns/step", name, per)
	}
}

// TestNativeInVectorMarshalledOnce: a native inside an ELPS vector is
// handed to the encoder as its bytes too, so its MarshalJSON runs once.
func TestNativeInVectorMarshalledOnce(t *testing.T) {
	env := newEnv(t)
	calls := 0
	env.Put(lisp.Symbol("n"), lisp.Native(onceMarshaler{calls: &calls}))
	res, _ := eval(t, env, `(handlebars:render "{{v.[0].x}}" (sorted-map "v" (vector n)))`)
	require.Equal(t, lisp.LString, res.Type, "%v", res)
	require.Equal(t, "once", res.Str)
	require.Equal(t, 1, calls)
}

// TestRenderFixedInvalidUTF8Keys: render-fixed finds a map key holding
// invalid UTF-8 under the member name JSON gives it (each invalid byte as
// U+FFFD), and leaves a name two keys share as JSON decoded it, on every
// run.
func TestRenderFixedInvalidUTF8Keys(t *testing.T) {
	env := newEnv(t)
	one := lisp.SortedMap()
	one.MapSetString("\xff\xfe", lisp.Int(9007199254740993))
	two := lisp.SortedMap()
	two.MapSetString("\xff", lisp.Int(9007199254740993))
	two.MapSetString("�", lisp.Int(9007199254740995))
	env.Put(lisp.Symbol("one"), one)
	env.Put(lisp.Symbol("two"), two)
	v, _ := eval(t, env, renderCall("render-fixed", "{{[��]}}", "one"))
	require.Equal(t, lisp.LString, v.Type, "%v", v)
	require.Equal(t, "9007199254740993", v.Str)
	first := ""
	for range 20 {
		v, _ = eval(t, env, renderCall("render-fixed", "{{[�]}}", "two"))
		require.Equal(t, lisp.LString, v.Type, "%v", v)
		require.Contains(t, []string{"9007199254740992", "9007199254740996"}, v.Str, "a float64, as JSON decoded it")
		if first == "" {
			first = v.Str
		}
		require.Equal(t, first, v.Str)
	}
}

// TestNativeEscapedNameUnderCap: a native whose field name escapes to six
// times its length (each & as &) fails the allocation cap from the
// walk's estimate, before the encoder builds and writes it.
func TestNativeEscapedNameUnderCap(t *testing.T) {
	name := strings.Repeat("&", 1<<20)
	typ := reflect.StructOf([]reflect.StructField{{Name: "A", Type: reflect.TypeFor[int](), Tag: reflect.StructTag(`json:"` + name + `" cold:"` + t.Name() + `"`)}})
	env := newEnv(t)
	env.Runtime.MaxAlloc = 2 << 20
	ctx := lisp.SortedMap()
	ctx.MapSetString("n", lisp.Native(reflect.New(typ).Elem().Interface()))
	env.Put(lisp.Symbol("ctx"), ctx)
	var res *lisp.LVal
	alloc := allocDuring(func() { res, _ = eval(t, env, `(handlebars:render "x" ctx)`) })
	require.Equal(t, lisp.LError, res.Type, "%v", res)
	require.Contains(t, res.String(), "allocation size exceeds maximum")
	require.Less(t, alloc, uint64(16<<20), "rejected before the encoder runs")
}

// TestNativeAddressableMarshalerText: an addressable RawMessage (a slice
// element) fails with json.Marshal's own text, which names the value's
// type, not its pointer's.
func TestNativeAddressableMarshalerText(t *testing.T) {
	native := []json.RawMessage{json.RawMessage("x")}
	_, merr := json.Marshal(native)
	require.Error(t, merr)
	env := newEnv(t)
	ctx := lisp.SortedMap()
	ctx.MapSetString("n", lisp.Native(native))
	env.Put(lisp.Symbol("ctx"), ctx)
	res, _ := eval(t, env, `(handlebars:render "x" ctx)`)
	require.Equal(t, lisp.LError, res.Type, "%v", res)
	require.Contains(t, res.String(), "error while serializing: "+merr.Error())
}

// TestNativeRawMessageCharged: a RawMessage as a native, behind a pointer
// or in a json.Marshaler field is charged for encoding/json's check of its
// bytes, and fails with json.Marshal's text.
func TestNativeRawMessageCharged(t *testing.T) {
	raw := json.RawMessage(`"` + strings.Repeat("a", 4<<20))
	for name, native := range map[string]any{
		"raw":                raw,
		"pointer":            &raw,
		"marshaler field":    struct{ R json.Marshaler }{raw},
		"raw field":          struct{ R json.RawMessage }{raw},
		"embedded":           struct{ json.RawMessage }{raw},
		"embedded pointer":   struct{ *json.RawMessage }{&raw},
		"embedded marshaler": struct{ json.Marshaler }{raw},
	} {
		_, merr := json.Marshal(native)
		require.Error(t, merr, name)
		env := newEnv(t)
		env.Runtime.MaxAlloc = 64 << 10
		ctx := lisp.SortedMap()
		ctx.MapSetString("r", lisp.Native(native))
		env.Put(lisp.Symbol("ctx"), ctx)
		res, steps := eval(t, env, `(handlebars:render "" ctx)`)
		require.Equal(t, lisp.LError, res.Type, name)
		require.Contains(t, res.String(), "error while serializing: "+merr.Error(), name)
		require.GreaterOrEqual(t, steps, int64(2*len(raw)/16), name)
	}
}

// selfMarshaler embeds an interface that can hold itself.
type selfMarshaler struct{ json.Marshaler }

// TestNativeEmbedSearchCharged: the search for an embedded RawMessage in
// a Marshaler native is charged whether or not it finds one (a struct
// declaring its own MarshalJSON, by embedding time.Time, with many plain
// fields), and a native embedding itself through an interface fails with
// a limit error instead of reaching encoding/json, which would overflow
// the stack.
func TestNativeEmbedSearchCharged(t *testing.T) {
	steps := func(n int) int64 {
		fields := []reflect.StructField{{Name: "Time", Type: reflect.TypeFor[time.Time](), Anonymous: true}}
		for i := range n {
			fields = append(fields, reflect.StructField{Name: "F" + strconv.Itoa(i), Type: reflect.TypeFor[int]()})
		}
		native := reflect.New(reflect.StructOf(fields)).Elem().Interface()
		require.Implements(t, (*json.Marshaler)(nil), native)
		env := newEnv(t)
		ctx := lisp.SortedMap()
		ctx.MapSetString("n", lisp.Native(native))
		env.Put(lisp.Symbol("ctx"), ctx)
		res, steps := eval(t, env, `(handlebars:render "x" ctx)`)
		require.Equal(t, lisp.LString, res.Type, "%v", res)
		return steps
	}
	require.GreaterOrEqual(t, steps(4000)-steps(1), int64(3999/16), "the field list is charged")

	self := &selfMarshaler{}
	self.Marshaler = self
	env := newEnv(t)
	ctx := lisp.SortedMap()
	ctx.MapSetString("n", lisp.Native(self))
	env.Put(lisp.Symbol("ctx"), ctx)
	res, _ := eval(t, env, `(handlebars:render "x" ctx)`)
	require.Equal(t, lisp.LError, res.Type, "%v", res)
	require.Contains(t, res.String(), "error while serializing: json: Marshaler embedding nests deeper than 64")
}

// TestNativeMarshalerChainFailsClosed: a RawMessage wrapped in
// struct{json.Marshaler} past the search's 64 interface hops fails with
// the limit error before encoding/json runs (it went uncharged); within
// the bound it is charged.
func TestNativeMarshalerChainFailsClosed(t *testing.T) {
	raw := json.RawMessage(`"` + strings.Repeat("a", 1<<20))
	chain := func(n int) json.Marshaler {
		var m json.Marshaler = raw
		for range n {
			m = struct{ json.Marshaler }{m}
		}
		return m
	}
	render := func(n int) (*lisp.LVal, int64) {
		env := newEnv(t)
		ctx := lisp.SortedMap()
		ctx.MapSetString("n", lisp.Native(chain(n)))
		env.Put(lisp.Symbol("ctx"), ctx)
		return eval(t, env, `(handlebars:render "x" ctx)`)
	}
	res, steps := render(60)
	require.Equal(t, lisp.LError, res.Type, "%v", res)
	require.GreaterOrEqual(t, steps, int64(2*len(raw)/16), "within the bound: charged")
	res, _ = render(64)
	require.Equal(t, lisp.LError, res.Type, "%v", res)
	require.NotContains(t, res.String(), "nests deeper than 64", "64 wrappers: within the bound")
	for _, n := range []int{65, 128} {
		res, _ = render(n)
		require.Equal(t, lisp.LError, res.Type, "%v", res)
		require.Contains(t, res.String(), "json: Marshaler embedding nests deeper than 64", "%d wrappers", n)
		tpl, err := libhandlebars.Parse(`x`)
		require.NoError(t, err)
		_, err = libhandlebars.RenderWith(tpl, map[string]any{"n": chain(n)}, libhandlebars.WithJSONContext())
		require.ErrorContains(t, err, "json: Marshaler embedding nests deeper than 64", "Go API, %d wrappers", n)
	}
}

// marshalerChain is a Marshaler (time.Time's, embedded) that also embeds a fresh reflect.StructOf chain, levels deep
// with width plain fields a level, whose type name spells out every level
// below: the embedding search looks up methods on it.
func marshalerChain(levels, width int, tag string) any {
	plain := func(i int) []reflect.StructField {
		fs := make([]reflect.StructField, width)
		for j := range fs {
			fs[j] = reflect.StructField{Name: "F" + tag + "_" + strconv.Itoa(i) + "_" + strconv.Itoa(j), Type: reflect.TypeFor[int]()}
		}
		return fs
	}
	chain := reflect.StructOf(plain(0))
	for i := 1; i < levels; i++ {
		chain = reflect.StructOf(append([]reflect.StructField{{Name: "E" + strconv.Itoa(i), Type: chain, Anonymous: true}}, plain(i)...))
	}
	top := reflect.StructOf([]reflect.StructField{
		{Name: "Time", Type: reflect.TypeFor[time.Time](), Anonymous: true},
		{Name: "C", Type: chain, Anonymous: true},
	})
	return reflect.New(top).Elem().Interface()
}

// TestNativeColdMarshalerChain: the embedding search's first look at a
// deep StructOf chain (method lookups whose cost grows with each type's
// name) is charged by that name's length, within the ceiling, and a warm
// look charges the same steps.
func TestNativeColdMarshalerChain(t *testing.T) {
	steps := func(native any) int64 {
		env := newEnv(t)
		ctx := lisp.SortedMap()
		ctx.MapSetString("n", lisp.Native(native))
		env.Put(lisp.Symbol("ctx"), ctx)
		res, steps := eval(t, env, `(handlebars:render "x" ctx)`)
		require.Equal(t, lisp.LString, res.Type, "%v", res)
		return steps
	}
	v := marshalerChain(64, 15, "w")
	require.Implements(t, (*json.Marshaler)(nil), v)
	cold := steps(v)
	warm := steps(v)
	require.Equal(t, cold, warm, "cold and warm alike")
	if raceEnabled || testing.Short() {
		return
	}
	run := 0
	_, per, _ := bestNative(t, func() any {
		run++
		return marshalerChain(64, 300, "c"+strconv.Itoa(run))
	})
	t.Logf("cold 64-level x300 marshaler chain: %.0f ns/step", per)
	require.False(t, ceilingFails(t, per), "%.0f ns/step", per)
	_, per, _ = bestNative(t, func() any {
		run++
		return marshalerChain(1, 20_000, "w"+strconv.Itoa(run))
	})
	t.Logf("cold one-level x20000 marshaler: %.0f ns/step", per)
	require.False(t, ceilingFails(t, per), "%.0f ns/step", per)
}

// TestEncodeMapKeysChargedBeforeValues: sorting a map's long keys is
// charged before its values are walked, so a value that fails the encode
// (a native chan, first) does not leave the keys uncharged.
func TestEncodeMapKeysChargedBeforeValues(t *testing.T) {
	prefix := strings.Repeat("p", 256<<10)
	env := newEnv(t)
	ctx := lisp.SortedMap()
	ctx.MapSetString(prefix+"0000", lisp.Native(make(chan int)))
	for i := 1; i < 256; i++ {
		ctx.MapSetString(prefix+fmt.Sprintf("%04d", i), lisp.Int(i))
	}
	env.Put(lisp.Symbol("ctx"), ctx)
	res, steps := eval(t, env, `(handlebars:render "" ctx)`)
	require.Equal(t, lisp.LError, res.Type, "%v", res)
	require.Contains(t, res.String(), "unsupported type: chan int")
	require.GreaterOrEqual(t, steps, int64(256*(256<<10)/256*9), "every key's bytes, log2(256) times")
}

// TestEncodeKeyCollisionBeforeNatives: libjson refuses an int key spelling
// a string key before it encodes any value, so that is the error even when
// a value is a native the encoder would fail on.
func TestEncodeKeyCollisionBeforeNatives(t *testing.T) {
	env := newEnv(t)
	ctx := lisp.SortedMap()
	ctx.MapSetLVal(lisp.Int(1), lisp.Native(make(chan int)))
	ctx.MapSetString("1", lisp.Int(2))
	env.Put(lisp.Symbol("ctx"), ctx)
	dump, _ := eval(t, env, `(json:dump-bytes ctx)`)
	require.Equal(t, lisp.LError, dump.Type)
	res, _ := eval(t, env, `(handlebars:render "" ctx)`)
	require.Equal(t, lisp.LError, res.Type, "%v", res)
	require.Equal(t, `error while serializing: map int key 1 collides with string key "1"`, res.Cells[0].Str)
	require.Equal(t, "error while serializing: "+dump.Cells[0].Str, res.Cells[0].Str)
}

// TestParseBudgetFailureNotCached: a render whose step budget runs out
// while its template is parsed leaves no verdict behind: the same template
// renders in a later evaluation with an ample budget.
func TestParseBudgetFailureNotCached(t *testing.T) {
	tpl := "{{x}} " + t.Name() // not in the process-wide cache yet
	call := `(handlebars:render ` + strconv.Quote(tpl) + ` (sorted-map "x" 1))`
	env := newEnv(t)
	env.Runtime.SetStepBudget(1)
	res := env.LoadStringContext(t.Context(), "test", call)
	require.Equal(t, lisp.LError, res.Type, "%v", res)
	env = newEnv(t)
	res, _ = eval(t, env, call)
	require.Equal(t, lisp.LString, res.Type, "%v", res)
	require.Equal(t, "1 "+t.Name(), res.Str)
}

// TestEncodeEstimateDoesNotStopWalk: the walk's size estimate over-counts
// (a float is 24 bytes in it), so passing the allocation cap by it alone
// must not stop the walk: the encode below it would run uncharged. Here
// the floats' estimate passes the cap, their JSON does not, and the
// native after them is charged for every field encoding/json visits.
func TestEncodeEstimateDoesNotStopWalk(t *testing.T) {
	floats := make([]*lisp.LVal, 200_000)
	for i := range floats {
		floats[i] = lisp.Float(0.5)
	}
	shared := &sparse100{}
	native := make([]*sparse100, 10_000)
	for i := range native {
		native[i] = shared
	}
	render := func(withFloats, withNative bool) int64 {
		env := newEnv(t)
		env.Runtime.MaxAlloc = 4 << 20
		ctx := lisp.SortedMap()
		if withFloats {
			ctx.MapSetString("a", lisp.QExpr(floats))
		}
		if withNative {
			ctx.MapSetString("b", lisp.Native(native))
		}
		env.Put(lisp.Symbol("ctx"), ctx)
		res, steps := eval(t, env, `(handlebars:render "x" ctx)`)
		require.Equal(t, lisp.LString, res.Type, "%v", res)
		return steps
	}
	nativeOnly, floatsOnly, both := render(false, true), render(true, false), render(true, true)
	t.Logf("native %d, floats %d, both %d steps", nativeOnly, floatsOnly, both)
	require.GreaterOrEqual(t, nativeOnly, int64(10_000*100), "each of the native's fields")
	require.GreaterOrEqual(t, both, (nativeOnly+floatsOnly)*9/10, "both charged in full together")
}

// TestEncodeErrorOrder: where several encode errors apply, the one
// json:dump-bytes reports first is reported: a map's key collision before
// its size passes the cap, and the cap passed by the bytes before a native
// before that native's own error (and the native's when they do not).
func TestEncodeErrorOrder(t *testing.T) {
	check := func(t *testing.T, ctx *lisp.LVal, maxAlloc int, want string) {
		t.Helper()
		env := newEnv(t)
		env.Runtime.MaxAlloc = maxAlloc
		env.Put(lisp.Symbol("ctx"), ctx)
		dump, _ := eval(t, env, `(json:dump-bytes ctx)`)
		require.Equal(t, lisp.LError, dump.Type)
		res, _ := eval(t, env, `(handlebars:render "" ctx)`)
		require.Equal(t, lisp.LError, res.Type, "%v", res)
		require.Contains(t, dump.Cells[0].Str, want)
		require.Contains(t, res.Cells[0].Str, dump.Cells[0].Str)
	}
	collide := lisp.SortedMap()
	collide.MapSetLVal(lisp.Int(1), lisp.Int(0))
	collide.MapSetString("1", lisp.Int(0))
	check(t, collide, 1, `map int key 1 collides with string key "1"`)

	floatsThenChan := func() *lisp.LVal {
		floats := make([]*lisp.LVal, 8)
		for i := range floats {
			floats[i] = lisp.Float(0.5)
		}
		ctx := lisp.SortedMap()
		ctx.MapSetString("a", lisp.QExpr(floats))
		ctx.MapSetString("z", lisp.Native(make(chan int)))
		return ctx
	}
	check(t, floatsThenChan(), 32, "allocation size exceeds maximum (32)")
	check(t, floatsThenChan(), 1<<20, "unsupported type: chan int")
}

// TestEncodeErrorOrderNearCap: just under the default allocation cap, an
// error the encoder meets before it has written past the cap is the one
// reported: a map's key collision, a function value in nested lists, and a
// native's stdlib Marshaler failing (time.Time past year 9999), not the
// cap the whole output would pass.
func TestEncodeErrorOrderNearCap(t *testing.T) {
	env := newEnv(t)
	limit := env.Runtime.MaxAllocBytes()
	check := func(t *testing.T, env *lisp.LEnv, ctx *lisp.LVal, want string) {
		t.Helper()
		env.Put(lisp.Symbol("ctx"), ctx)
		dump, _ := eval(t, env, `(json:dump-bytes ctx)`)
		require.Equal(t, lisp.LError, dump.Type)
		require.Contains(t, dump.String(), want)
		res, _ := eval(t, env, `(handlebars:render "" ctx)`)
		require.Equal(t, lisp.LError, res.Type, "%v", res)
		require.Contains(t, res.String(), want)
	}

	m := lisp.SortedMap()
	m.MapSetString("1", lisp.Int(0))
	for i := range 300 {
		m.MapSetLVal(lisp.Int(i), lisp.Int(0))
	}
	ctx := lisp.SortedMap()
	ctx.MapSetString("a", lisp.String(strings.Repeat("x", limit-150)))
	ctx.MapSetString("b", m)
	check(t, newEnv(t), ctx, `map int key 1 collides with string key "1"`)

	env = newEnv(t)
	fn, _ := eval(t, env, `(lambda () 1)`)
	require.Equal(t, lisp.LFun, fn.Type)
	nested := fn
	for range 100 {
		nested = lisp.QExpr([]*lisp.LVal{nested})
	}
	ctx = lisp.SortedMap()
	ctx.MapSetString("a", lisp.String(strings.Repeat("x", limit-150)))
	ctx.MapSetString("b", nested)
	check(t, env, ctx, "invalid type encountered")

	native := struct {
		S string
		T time.Time
	}{strings.Repeat("s", limit), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}
	ctx = lisp.SortedMap()
	ctx.MapSetString("n", lisp.Native(native))
	check(t, newEnv(t), ctx, "error calling MarshalJSON for type time.Time")
}

// TestEncodeUnloadableNativePastCap: a native whose bytes pass the
// allocation cap but fail libjson's load check reports the load check's
// error, as json:dump-bytes does (the encoder checks a native's bytes
// before its next cap check).
func TestEncodeUnloadableNativePastCap(t *testing.T) {
	big400, ok := new(big.Int).SetString("1"+strings.Repeat("0", 400), 10)
	require.True(t, ok)
	var deep any = 1
	for range 10_001 {
		deep = []any{deep}
	}
	for name, c := range map[string]struct {
		native   any
		prefix   int // bytes of string before the native
		maxAlloc int
	}{
		"raw 1e1000 in a struct": {struct {
			S string
			R json.RawMessage
		}{strings.Repeat("s", 4<<10), json.RawMessage("1e1000")}, 0, 1024},
		"400-digit big.Int": {struct {
			S string
			N *big.Int
		}{strings.Repeat("s", 2<<10), big400}, 0, 1024},
		"nested 10,001 deep": {deep, 0, 4096},
		"tiny raw crossing":  {json.RawMessage("1e1000"), 1000, 1024},
	} {
		env := newEnv(t)
		env.Runtime.MaxAlloc = c.maxAlloc
		ctx := lisp.SortedMap()
		if c.prefix > 0 {
			ctx.MapSetString("a", lisp.String(strings.Repeat("x", c.prefix)))
		}
		ctx.MapSetString("b", lisp.Native(c.native))
		env.Put(lisp.Symbol("ctx"), ctx)
		dump, _ := eval(t, env, `(json:dump-bytes ctx)`)
		require.Equal(t, lisp.LError, dump.Type, name)
		require.NotContains(t, dump.String(), "allocation size", name)
		res, _ := eval(t, env, `(handlebars:render "" ctx)`)
		require.Equal(t, lisp.LError, res.Type, "%s: %v", name, res)
		require.Contains(t, res.Cells[0].Str, dump.Cells[0].Str, name)
	}
}

// TestEncodeFailingAfterEscapesCeiling: an encode that fails after a
// string escaping to near the allocation cap (each < written as <)
// stays within the ceiling: the escapes are charged, and a failing native
// within the cap is reported without running the encoder again.
func TestEncodeFailingAfterEscapesCeiling(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing test: skipped under -race and -short")
	}
	env := newEnv(t)
	fn, _ := eval(t, env, `(lambda () 1)`)
	escaped := strings.Repeat("<", (env.Runtime.MaxAllocBytes()-1024)/6)
	for name, c := range map[string]struct {
		last  *lisp.LVal
		depth int // lists around it: past 64, libjson encodes twice
	}{
		"native": {lisp.Native(make(chan int)), 0}, "lambda": {fn, 0},
		"native, 70 deep": {lisp.Native(make(chan int)), 70}, "lambda, 70 deep": {fn, 70},
		"NaN": {lisp.Float(math.NaN()), 0},
	} {
		best := math.Inf(1)
		for range 3 {
			ctx := lisp.QExpr([]*lisp.LVal{lisp.String(escaped), c.last})
			for range c.depth {
				ctx = lisp.QExpr([]*lisp.LVal{ctx})
			}
			env.Put(lisp.Symbol("ctx"), ctx)
			start := time.Now()
			res, steps := eval(t, env, `(handlebars:render "" ctx)`)
			per := float64(time.Since(start).Nanoseconds()) / float64(steps)
			require.Equal(t, lisp.LError, res.Type, "%s: %v", name, res)
			best = min(best, per)
		}
		t.Logf("%s after %d escaped bytes: %.0f ns/step", name, 6*len(escaped), best)
		require.False(t, ceilingFails(t, best), "%s: %.0f ns/step", name, best)
	}
	// Past the cap by its escapes, then another value: the walk stops at
	// the cap and the encoder replays up to it.
	past := strings.Repeat("<", (env.Runtime.MaxAllocBytes()+4096)/6)
	best := math.Inf(1)
	for range 3 {
		env.Put(lisp.Symbol("ctx"), lisp.QExpr([]*lisp.LVal{lisp.String(past), lisp.Int(1)}))
		start := time.Now()
		res, steps := eval(t, env, `(handlebars:render "" ctx)`)
		require.Equal(t, lisp.LError, res.Type, "%v", res)
		require.Contains(t, res.String(), "allocation size exceeds maximum")
		best = min(best, float64(time.Since(start).Nanoseconds())/float64(steps))
	}
	t.Logf("cap replay after %d escaped bytes: %.0f ns/step", 6*len(past), best)
	require.False(t, ceilingFails(t, best), "cap replay: %.0f ns/step", best)
}
