// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"strings"
	"testing"

	"github.com/luthersystems/elps/elpsutil"
	"github.com/luthersystems/elps/lisp"
	"github.com/luthersystems/elps/lisp/lisplib"
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

func mustLoader(t *testing.T, lim hbs.Limits) elpsutil.Loader {
	t.Helper()
	l, err := libhandlebars.NewLoader(lim)
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

	big := newEnvWith(t, mustLoader(t, hbs.Limits{MaxTemplateBytes: 4 << 20}))
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
	lim := hbs.Limits{MaxTemplateBytes: 4 << 20}
	p, err := libhandlebars.ParseWith(tpl, lim)
	require.NoError(t, err)
	got, err := libhandlebars.RenderWith(p, map[string]any{"name": "A & B", "items": []any{1, "two"}}, libhandlebars.WithLimits(lim), libhandlebars.WithJSONContext())
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
		strict     hbs.Limits // the template fails to parse under it
		permissive hbs.Limits // and parses under it
	}{
		{"depth", deep, hbs.Limits{}, hbs.Limits{MaxDepth: 512}},
		{"size", long, hbs.Limits{MaxTemplateBytes: 100}, hbs.Limits{}},
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
		for _, lim := range []hbs.Limits{{}, hbs.DefaultLimits()} {
			got, steps := renderIn(t, newEnvWith(t, mustLoader(t, lim)), fn, tpl, ctx())
			require.Equal(t, want.String(), got.String(), "%s %+v", fn, lim)
			require.Equal(t, wantSteps, steps, "%s %+v", fn, lim)
		}
	}
}

// TestLimitsRejectNegative: a negative limit is an error, at the loader's
// construction and in the Go API.
func TestLimitsRejectNegative(t *testing.T) {
	for _, lim := range []hbs.Limits{{MaxTemplateBytes: -1}, {MaxDepth: -1}, {MaxOutputBytes: -1}, {MaxSteps: -1}} {
		_, err := libhandlebars.NewLoader(lim)
		require.ErrorContains(t, err, "negative limit", "%+v", lim)
		_, err = libhandlebars.ParseWith("x", lim)
		require.ErrorContains(t, err, "negative limit", "%+v", lim)
		p, err := libhandlebars.Parse("x")
		require.NoError(t, err)
		_, err = libhandlebars.RenderWith(p, nil, libhandlebars.WithLimits(lim))
		require.ErrorContains(t, err, "negative limit", "%+v", lim)
	}
}
