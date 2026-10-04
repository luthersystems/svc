// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/luthersystems/elps/elpsutil"
	"github.com/luthersystems/elps/lisp"
	"github.com/luthersystems/elps/lisp/lisplib"
	"github.com/luthersystems/elps/lisp/lisplib/libjson"
	"github.com/luthersystems/elps/parser"
	"github.com/luthersystems/svc/libhandlebars"
	"github.com/luthersystems/svc/libhandlebars/hbs"
	"github.com/luthersystems/svc/libhandlebars/internal/hbref"
	"github.com/stretchr/testify/require"
)

// newEnvWith is newEnv with the package loaded by load.
func newEnvWith(t *testing.T, load elpsutil.Loader) *lisp.LEnv {
	t.Helper()
	env := lisp.NewEnv(nil)
	env.Runtime.Reader = parser.NewReader()
	require.NotEqual(t, lisp.LError, lisp.InitializeUserEnv(env).Type)
	require.NotEqual(t, lisp.LError, lisplib.LoadLibrary(env).Type)
	require.NotEqual(t, lisp.LError, load(env).Type)
	require.NotEqual(t, lisp.LError, env.InPackage(lisp.String(lisp.DefaultUserPackage)).Type)
	env.Runtime.SetStepBudget(1 << 60)
	return env
}

func mustLoader(t *testing.T, opts ...libhandlebars.Option) elpsutil.Loader {
	t.Helper()
	l, err := libhandlebars.LoadPackageWith(opts...)
	require.NoError(t, err)
	return l
}

// renderIn renders tpl (bound as a string, not source text) with ctx in env.
func renderIn(t *testing.T, env *lisp.LEnv, fn, tpl string, ctx *lisp.LVal) (*lisp.LVal, int64) {
	t.Helper()
	env.Put(lisp.Symbol("tpl"), lisp.String(tpl))
	env.Put(lisp.Symbol("ctx"), ctx)
	return eval(t, env, "(handlebars:"+fn+" tpl ctx)")
}

// sortedMap builds an ELPS sorted map from key, value pairs.
func sortedMap(kv ...any) *lisp.LVal {
	m := lisp.SortedMap()
	for i := 0; i+1 < len(kv); i += 2 {
		m.MapSetString(kv[i].(string), kv[i+1].(*lisp.LVal)) //nolint:forcetypeassert // test pairs
	}
	return m
}

func isParseCondition(v *lisp.LVal) bool {
	return v.Type == lisp.LError && strings.Contains(v.String(), "handlebars-parse")
}

// TestLoaderLimitsLargeTemplate: a 2 MiB template (an image embedded as a
// data URI) fails to parse under the default 1 MiB limit, and renders,
// as raymond rendered it, under a loader's (or a Go caller's) 4 MiB one.
func TestLoaderLimitsLargeTemplate(t *testing.T) {
	tpl := `<p>{{name}}</p><img src="data:image/png;base64,` + strings.Repeat("iVBORw0KGgo", (2<<20)/11) + `"/>{{#each items}}<i>{{this}}</i>{{/each}}`
	require.Greater(t, len(tpl), 2<<20)
	const ctxJSON = `{"name": "A & B", "items": [1, "two"]}`
	want, err := hbref.RenderJSON(tpl, []byte(ctxJSON))
	require.NoError(t, err)
	ctx := func() *lisp.LVal {
		return sortedMap("name", lisp.String("A & B"), "items", lisp.QExpr([]*lisp.LVal{lisp.Int(1), lisp.String("two")}))
	}

	def := newEnvWith(t, libhandlebars.LoadPackage)
	for _, fn := range []string{"render", "render-fixed"} {
		res, _ := renderIn(t, def, fn, tpl, ctx())
		require.True(t, isParseCondition(res), "%s: %v", fn, res)
	}
	def.Put(lisp.Symbol("tpl"), lisp.String(tpl))
	res, _ := eval(t, def, "(handlebars:must-parse tpl)")
	require.True(t, isParseCondition(res), "%v", res)

	big := newEnvWith(t, mustLoader(t, libhandlebars.WithMaxTemplateBytes(4<<20)))
	res, _ = renderIn(t, big, "render", tpl, ctx())
	require.Equal(t, lisp.LString, res.Type, "%v", res)
	require.Equal(t, want, res.Str)
	big.Put(lisp.Symbol("tpl"), lisp.String(tpl))
	res, _ = eval(t, big, "(handlebars:must-parse tpl)")
	require.NotEqual(t, lisp.LError, res.Type, "%v", res)

	// The Go API: Parse under the defaults fails; ParseWith and
	// RenderWith(WithLimits) render it.
	_, err = libhandlebars.Parse(tpl)
	require.Error(t, err)
	raise := libhandlebars.WithMaxTemplateBytes(4 << 20)
	p, err := libhandlebars.ParseWith(tpl, raise)
	require.NoError(t, err)
	got, err := libhandlebars.RenderWith(p, map[string]any{"name": "A & B", "items": []any{1, "two"}}, raise, libhandlebars.WithJSONContext())
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestLoaderLimitsCacheKey: the process-wide parse cache keys a verdict on
// the effective template-size and depth limits, so loaders with different
// limits in one process each get their own verdict, in either order.
func TestLoaderLimitsCacheKey(t *testing.T) {
	ctx := func() *lisp.LVal { return sortedMap("t", lisp.Bool(true)) }
	deep := func(tag string) string {
		return strings.Repeat("{{#if t}}", 300) + tag + strings.Repeat("{{/if}}", 300)
	}
	long := func(tag string) string { return strings.Repeat("x", 200) + tag }
	for _, c := range []struct {
		name       string
		tpl        func(string) string
		strict     libhandlebars.Option // the template fails to parse under it
		permissive libhandlebars.Option // and parses under it
	}{
		{"depth", deep, nil, libhandlebars.WithMaxDepth(512)},
		{"size", long, libhandlebars.WithMaxTemplateBytes(100), nil},
		{"depth, own caches", deep, libhandlebars.WithParseCache(1<<20, 1<<10), libhandlebars.WithConfig(libhandlebars.Config{
			Limits: hbs.Limits{MaxDepth: 512}, ParseCacheMaxBytes: 1 << 20, ParseCacheMaxEntryBytes: 1 << 10,
		})},
	} {
		for _, strictFirst := range []bool{true, false} {
			tpl := c.tpl(c.name + map[bool]string{true: "-a", false: "-b"}[strictFirst]) // fresh to the cache
			strict := newEnvWith(t, mustLoader(t, c.strict))
			permissive := newEnvWith(t, mustLoader(t, c.permissive))
			check := func(env *lisp.LEnv, ok bool) {
				t.Helper()
				for range 2 { // a cache miss, then a hit
					res, _ := renderIn(t, env, "render", tpl, ctx())
					if ok {
						require.Equal(t, lisp.LString, res.Type, "%s strictFirst=%v: %v", c.name, strictFirst, res)
					} else {
						require.True(t, isParseCondition(res), "%s strictFirst=%v: %v", c.name, strictFirst, res)
					}
				}
			}
			if strictFirst {
				check(strict, false)
				check(permissive, true)
			} else {
				check(permissive, true)
				check(strict, false)
			}
		}
	}
}

// TestLoaderDefaultLimitsSteps: a loader built with zero (or the default)
// limits renders exactly as LoadPackage does, step for step.
func TestLoaderDefaultLimitsSteps(t *testing.T) {
	const tpl = `{{#each items}}{{this}}{{to-str this}}{{/each}}{{plus a=1 b=2}}{{name}}`
	ctx := func() *lisp.LVal {
		return sortedMap("name", lisp.String("n"), "items", lisp.QExpr([]*lisp.LVal{lisp.Int(1), lisp.Float(2.5)}))
	}
	for _, fn := range []string{"render", "render-fixed"} {
		want, wantSteps := renderIn(t, newEnvWith(t, libhandlebars.LoadPackage), fn, tpl, ctx())
		for i, opts := range [][]libhandlebars.Option{
			nil, {libhandlebars.WithConfig(libhandlebars.DefaultConfig())}, {libhandlebars.WithLimits(hbs.DefaultLimits())},
			{libhandlebars.WithConfig(libhandlebars.Config{})},
		} {
			got, steps := renderIn(t, newEnvWith(t, mustLoader(t, opts...)), fn, tpl, ctx())
			require.Equal(t, want.String(), got.String(), "%s %d", fn, i)
			require.Equal(t, wantSteps, steps, "%s %d", fn, i)
		}
	}
}

// TestConfigRejectsInvalid: a negative setting, or an unknown GoContext,
// is an error, at the loader's construction and in the Go API.
func TestConfigRejectsInvalid(t *testing.T) {
	for _, opt := range []libhandlebars.Option{
		libhandlebars.WithMaxTemplateBytes(-1), libhandlebars.WithMaxDepth(-1), libhandlebars.WithMaxOutputBytes(-1),
		libhandlebars.WithMaxSteps(-1), libhandlebars.WithProducedFactor(-1), libhandlebars.WithParseCache(-1, 0),
		libhandlebars.WithParseCache(0, -1), libhandlebars.WithConfig(libhandlebars.Config{GoContext: "yes"}),
	} {
		_, err := libhandlebars.LoadPackageWith(opt)
		require.Error(t, err)
		_, err = libhandlebars.ParseWith("x", opt)
		require.Error(t, err)
		p, err := libhandlebars.Parse("x")
		require.NoError(t, err)
		_, err = libhandlebars.RenderWith(p, nil, opt)
		require.Error(t, err)
	}
}

// TestConfigEachField: every consensus-visible setting moved away from
// its default moves the result it governs (a template that renders under
// the defaults fails under the setting), and the performance-only cache
// bounds leave results as they are.
func TestConfigEachField(t *testing.T) {
	s60 := strings.Repeat("s", 60)
	items := make([]*lisp.LVal, 50)
	for i := range items {
		items[i] = lisp.Int(i)
	}
	ctx := func() *lisp.LVal {
		return sortedMap("t", lisp.Bool(true), "s", lisp.String(s60), "items", lisp.QExpr(items))
	}
	for _, c := range []struct {
		name, tpl, err string
		opts           []libhandlebars.Option
	}{
		{"MaxTemplateBytes", "{{t}} and some text", "handlebars-parse", []libhandlebars.Option{libhandlebars.WithMaxTemplateBytes(10)}},
		{"MaxDepth", "{{#if t}}{{#if t}}{{#if t}}x{{/if}}{{/if}}{{/if}}", "handlebars-parse", []libhandlebars.Option{libhandlebars.WithMaxDepth(2)}},
		{"MaxOutputBytes", "hello world", "rendered output exceeds the maximum of 5 bytes", []libhandlebars.Option{libhandlebars.WithMaxOutputBytes(5)}},
		{"MaxSteps", "{{#each items}}{{this}}{{/each}}", "exceeds the maximum of 20 steps", []libhandlebars.Option{libhandlebars.WithMaxSteps(20)}},
		{
			"ProducedFactor", "{{#if (to-str s)}}{{/if}}{{#if (to-str s)}}{{/if}}", "produces more than 100 bytes",
			[]libhandlebars.Option{libhandlebars.WithMaxOutputBytes(100), libhandlebars.WithProducedFactor(1)},
		},
		{"ParseCache", "{{#each items}}{{this}}{{/each}}", "", []libhandlebars.Option{libhandlebars.WithParseCache(64, 8)}},
	} {
		want, _ := renderIn(t, newEnvWith(t, libhandlebars.LoadPackage), "render", c.tpl, ctx())
		require.Equal(t, lisp.LString, want.Type, "%s default: %v", c.name, want)
		got, _ := renderIn(t, newEnvWith(t, mustLoader(t, c.opts...)), "render", c.tpl, ctx())
		if c.err == "" {
			require.Equal(t, want.String(), got.String(), c.name)
			continue
		}
		require.Equal(t, lisp.LError, got.Type, "%s: %v", c.name, got)
		require.Contains(t, got.String(), c.err, c.name)
	}
	// The factor is a multiple of MaxOutputBytes: 8 (the default) lets
	// the 120 bytes produced through, as does the default output limit.
	got, _ := renderIn(t, newEnvWith(t, mustLoader(t, libhandlebars.WithMaxOutputBytes(100))), "render",
		"{{#if (to-str s)}}{{/if}}{{#if (to-str s)}}{{/if}}", ctx())
	require.Equal(t, lisp.LString, got.Type, "%v", got)
}

// TestConfigOptionsCompose: options apply in order, each over the last,
// and WithConfig sets every field.
func TestConfigOptionsCompose(t *testing.T) {
	deep := "{{#if t}}{{#if t}}{{#if t}}x{{/if}}{{/if}}{{/if}}"
	ctx := func() *lisp.LVal { return sortedMap("t", lisp.Bool(true)) }
	for _, c := range []struct {
		opts []libhandlebars.Option
		ok   bool
	}{
		{[]libhandlebars.Option{libhandlebars.WithMaxDepth(2)}, false},
		{[]libhandlebars.Option{libhandlebars.WithMaxDepth(2), libhandlebars.WithMaxDepth(10)}, true},
		{[]libhandlebars.Option{libhandlebars.WithMaxDepth(2), libhandlebars.WithConfig(libhandlebars.DefaultConfig())}, true},
		{[]libhandlebars.Option{libhandlebars.WithConfig(libhandlebars.DefaultConfig()), libhandlebars.WithMaxDepth(2)}, false},
		{[]libhandlebars.Option{libhandlebars.WithLimits(hbs.Limits{MaxDepth: 2}), libhandlebars.WithMaxOutputBytes(1 << 20)}, false},
		{[]libhandlebars.Option{libhandlebars.WithMaxDepth(2), libhandlebars.WithLimits(hbs.Limits{MaxOutputBytes: 1 << 20})}, true},
		{[]libhandlebars.Option{nil, libhandlebars.WithMaxDepth(2), nil}, false},
	} {
		got, _ := renderIn(t, newEnvWith(t, mustLoader(t, c.opts...)), "render", deep, ctx())
		require.Equal(t, c.ok, got.Type == lisp.LString, "%d: %v", len(c.opts), got)
	}
}

// TestConfigGoContext: Config.GoContext selects how RenderWith reads a Go
// context, as WithJSONContext and WithGoContext do.
func TestConfigGoContext(t *testing.T) {
	p, err := libhandlebars.Parse(`{{to-str n}}`)
	require.NoError(t, err)
	for _, c := range []struct {
		opts []libhandlebars.Option
		want string
	}{
		{[]libhandlebars.Option{libhandlebars.WithGoContext()}, "3"},
		{[]libhandlebars.Option{libhandlebars.WithJSONContext()}, "3.000000"},
		{[]libhandlebars.Option{libhandlebars.WithConfig(libhandlebars.Config{GoContext: libhandlebars.GoContextJSON})}, "3.000000"},
		{[]libhandlebars.Option{libhandlebars.WithConfig(libhandlebars.Config{GoContext: libhandlebars.GoContextReflect})}, "3"},
		{[]libhandlebars.Option{libhandlebars.WithJSONContext(), libhandlebars.WithGoContext()}, "3"},
	} {
		got, err := libhandlebars.RenderWith(p, map[string]any{"n": 3}, c.opts...)
		require.NoError(t, err)
		require.Equal(t, c.want, got)
	}
}

// TestConfigMaxDepthCeiling: MaxDepth bounds the engine's recursion, so a
// value above hbs.MaxDepthCeiling is refused; at the ceiling, templates and
// contexts nested past it fail with the depth error (the reviewer's
// 1.6M-level template and 3M-deep value overflowed the stack, a fatal
// crash, under an unbounded MaxDepth).
func TestConfigMaxDepthCeiling(t *testing.T) {
	over := libhandlebars.WithMaxDepth(hbs.MaxDepthCeiling + 1)
	const msg = "Limits.MaxDepth is 10001; must be <= 10000"
	_, err := libhandlebars.LoadPackageWith(over)
	require.ErrorContains(t, err, msg)
	_, err = libhandlebars.ParseWith("x", over)
	require.ErrorContains(t, err, msg)
	p, err := libhandlebars.Parse("{{a}}")
	require.NoError(t, err)
	_, err = libhandlebars.RenderWith(p, nil, over)
	require.ErrorContains(t, err, msg)

	atCap := []libhandlebars.Option{libhandlebars.WithMaxDepth(hbs.MaxDepthCeiling), libhandlebars.WithMaxTemplateBytes(64 << 20)}
	n, deepValue := 1_600_000, 3_000_000 // the reviewer's sizes
	if raceEnabled {
		n, deepValue = 20_000, 20_000 // past the ceiling, within -race's memory
	}
	sexpr := "{{a " + strings.Repeat("(a ", n) + "1" + strings.Repeat(")", n) + "}}"
	blocks := strings.Repeat("{{#if t}}", n) + "x" + strings.Repeat("{{/if}}", n)
	env := newEnvWith(t, mustLoader(t, atCap...))
	for _, tpl := range []string{sexpr, blocks} {
		res, _ := renderIn(t, env, "render", tpl, sortedMap("t", lisp.Bool(true)))
		require.True(t, isParseCondition(res), "%v", res)
		_, err = libhandlebars.ParseWith(tpl, atCap...)
		require.ErrorContains(t, err, "nesting depth exceeds limit of 10000")
	}
	var v any = "x"
	for range deepValue {
		v = []any{v}
	}
	_, err = libhandlebars.RenderWith(p, map[string]any{"a": v}, append(atCap, libhandlebars.WithGoContext())...)
	require.ErrorContains(t, err, "maximum depth of 10000")
	// At the ceiling, nesting just within it renders.
	ok := strings.Repeat("{{#if t}}", hbs.MaxDepthCeiling-1) + "x" + strings.Repeat("{{/if}}", hbs.MaxDepthCeiling-1)
	res, _ := renderIn(t, env, "render", ok, sortedMap("t", lisp.Bool(true)))
	require.Equal(t, "x", res.Str, "%v", res)
}

type cyclicNode struct {
	Name string
	Next *cyclicNode
}

// TestConfigCyclicGoContext: a Go context that contains itself (a slice,
// a map, a struct through a pointer) fails with the depth error at the
// default MaxDepth and at the ceiling, never a stack overflow.
func TestConfigCyclicGoContext(t *testing.T) {
	s := []any{nil}
	s[0] = s
	m := map[string]any{}
	m["m"] = m
	n := &cyclicNode{Name: "n"}
	n.Next = n
	for _, depth := range []int{0, hbs.MaxDepthCeiling} {
		for _, c := range []struct {
			tpl, err string
			ctx      any
		}{
			{"{{x}}", "maximum depth", s}, {"{{prettyp-num-en x}}", "maximum depth", s},
			{"{{prettyp-num-en x}}", "maximum depth", m},
			{"{{#each x}}{{#each this}}{{this}}{{/each}}{{/each}}", "maximum depth", s},
			// fmt does not follow a nested pointer: it prints as its type.
			{"{{prettyp-num-en x}}", "got: (libhandlebars_test.cyclicNode)", n},
		} {
			p, err := libhandlebars.Parse(c.tpl)
			require.NoError(t, err)
			_, err = libhandlebars.RenderWith(p, map[string]any{"x": c.ctx}, libhandlebars.WithMaxDepth(depth), libhandlebars.WithGoContext())
			require.ErrorContains(t, err, c.err, "%s %T at MaxDepth %d", c.tpl, c.ctx, depth)
		}
	}
}

// TestRaisedValueDepthNoCrash runs, in a subprocess (a stack overflow is
// fatal), contexts nested past elps's default value depth in an
// environment whose limit the embedder raised: a 3,000,000-level vector
// and map context, and a chain of 3,000,000 quotes for render-fixed. Each
// render ends with an error or a result as json:dump-bytes and the decoder
// decide, not a crash; the walk that charges the encode stops at elps's
// default limit, and render-fixed's int walk unwraps quotes in a loop.
func TestRaisedValueDepthNoCrash(t *testing.T) {
	if os.Getenv("HBS_RAISED_DEPTH_CHILD") == "1" {
		const n = 3_000_000
		v := lisp.Int(7)
		for range n {
			v = lisp.QExpr([]*lisp.LVal{v})
		}
		m := lisp.Int(7)
		for range n {
			mm := lisp.SortedMap()
			mm.MapSetString("k", m)
			m = mm
		}
		q := lisp.Int(7)
		for range n {
			q = lisp.Quote(q)
		}
		for _, c := range []struct {
			fn  string
			ctx *lisp.LVal
		}{
			{"render", sortedMap("a", v)}, {"render", sortedMap("a", m)},
			{"render-fixed", sortedMap("a", v)}, {"render-fixed", sortedMap("a", q)},
		} {
			env := newEnvWith(t, mustLoader(t, libhandlebars.WithMaxDepth(hbs.MaxDepthCeiling)))
			if res := lisp.WithMaxValueDepth(math.MaxInt)(env); res.Type == lisp.LError {
				t.Fatal(res)
			}
			// json:dump-bytes itself, under the same raised limit.
			dump := libjson.DefaultSerializer().DumpBytesBuiltin(env, lisp.SExpr([]*lisp.LVal{c.ctx, lisp.Bool(false)}))
			env.Put(lisp.Symbol("ctx"), c.ctx)
			res := env.LoadStringContext(t.Context(), "test", "(handlebars:"+c.fn+` "{{a}}" ctx)`)
			switch {
			case dump.Type == lisp.LError:
				// The encoder's own error.
				if res.Type != lisp.LError || !strings.Contains(res.String(), dump.Cells[0].Str) {
					t.Fatalf("%s: dump %.200v, render %.300v", c.fn, dump, res)
				}
			case len(dump.Bytes()) < 1<<20:
				// Shallow JSON (the quote chain): the render succeeds.
				if res.Type != lisp.LString {
					t.Fatalf("%s: %.300v", c.fn, res)
				}
			default:
				// Deep JSON: the decoder refuses it.
				if res.Type != lisp.LError || !strings.Contains(res.String(), "exceeded max depth") {
					t.Fatalf("%s: %.300v", c.fn, res)
				}
			}
			fmt.Printf("%s: dump %v, render %.120v\n", c.fn, dump.Type, res)
		}
		fmt.Println("RAISED-DEPTH-OK")
		return
	}
	if testing.Short() || raceEnabled {
		t.Skip("3M-level contexts: skipped under -short and -race (memory)")
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRaisedValueDepthNoCrash$") //nolint:gosec // this test binary
	cmd.Env = append(os.Environ(), "HBS_RAISED_DEPTH_CHILD=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%.2000s", out)
	require.Contains(t, string(out), "RAISED-DEPTH-OK")
}
