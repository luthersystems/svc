// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/luthersystems/elps/lisp"
	"github.com/luthersystems/elps/parser"
	"github.com/luthersystems/raymond"
	"github.com/stretchr/testify/require"
)

func resetMustParseCache() {
	mustParseCache.mu.Lock()
	defer mustParseCache.mu.Unlock()
	mustParseCache.m = nil
	mustParseCache.bytes = 0
}

// uncachedMustParse is must-parse before the cache: raymond.Parse on every
// call. It is the reference the cached builtin must match.
func uncachedMustParse(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
	template := args.Cells[0]
	if template.Type != lisp.LString {
		return env.Errorf("non-string template: %v", template.Type)
	}
	if _, err := raymond.Parse(template.Str); err != nil {
		return env.ErrorConditionf("handlebars-parse", "error parsing template: %v", err)
	}
	return lisp.Nil()
}

func newHandlebarsEnv(t testing.TB, uncached bool) *lisp.LEnv {
	t.Helper()
	env := lisp.NewEnv(nil)
	env.Runtime.Reader = parser.NewReader()
	require.True(t, lisp.InitializeUserEnv(env).IsNil())
	require.True(t, LoadPackage(env).IsNil())
	if uncached {
		pkg := env.Runtime.Registry.Package(DefaultPackageName)
		sym := lisp.Symbol("must-parse")
		orig := pkg.Get(sym)
		fun := lisp.FunInPackage(pkg.Name, "<builtin-function ``must-parse''>", orig.Cells[0], uncachedMustParse)
		fun.FunType = orig.FunType
		fun.Str = orig.Str
		fun.Cells[1] = orig.Cells[1]
		require.True(t, pkg.Put(sym, fun).IsNil())
	}
	require.True(t, env.InPackage(lisp.String(lisp.DefaultUserPackage)).IsNil())
	// Steps are only counted under a budget, as substrate meters them.
	env.Runtime.SetStepBudget(1 << 40)
	return env
}

// evalObs is everything a caller could observe about one evaluation.
type evalObs struct {
	out   string
	steps int64
}

func observe(env *lisp.LEnv, src string) evalObs {
	before := env.Runtime.TotalSteps()
	v := env.LoadStringContext(context.Background(), "test", src)
	return evalObs{out: fmt.Sprintf("%v|%v", v.Type, v), steps: env.Runtime.TotalSteps() - before}
}

// TestMustParseCacheParity pins that the cached must-parse is
// indistinguishable from parsing on every call: identical results, error
// text, conditions and ELPS step counts, on a cold cache and on a warm one.
func TestMustParseCacheParity(t *testing.T) {
	resetMustParseCache()
	t.Cleanup(resetMustParseCache)
	exprs := []string{
		`(handlebars:must-parse "hello {{name}}")`,
		`(handlebars:must-parse "{{#if a}}x{{else}}y{{/if}}")`,
		`(handlebars:must-parse "{{#if a}}unterminated")`,
		`(handlebars:must-parse "{{")`,
		`(handlebars:must-parse 42)`,
		`(handlebars:must-parse)`,
		`(handler-bind ((handlebars-parse (lambda (c &rest xs) (list c xs)))) (handlebars:must-parse "{{/x}}"))`,
		`(ignore-errors (handlebars:must-parse "{{"))`,
		`(map 'list #^(handlebars:must-parse %) (list "a" "{{b}}" "c{{d}}e"))`,
		`(handlebars:must-parse "` + strings.Repeat("x", mustParseCacheMaxTemplate+1) + `")`,
	}
	refEnv := newHandlebarsEnv(t, true)
	coldEnv := newHandlebarsEnv(t, false)
	warmEnv := newHandlebarsEnv(t, false)
	for _, src := range exprs {
		ref := observe(refEnv, src)
		require.Positive(t, ref.steps, src)
		cold := observe(coldEnv, src)
		warm := observe(warmEnv, src) // served from the verdict cold stored
		require.Equal(t, ref, cold, "cold: %s", src)
		require.Equal(t, ref, warm, "warm: %s", src)
	}
	mustParseCache.mu.RLock()
	n := len(mustParseCache.m)
	mustParseCache.mu.RUnlock()
	require.Positive(t, n, "the cache was never populated")
}

func mustParseCacheEntryCost(tpl string) int {
	return len(tpl) + mustParseEntryOverhead
}

func TestMustParseCacheBounded(t *testing.T) {
	resetMustParseCache()
	t.Cleanup(resetMustParseCache)
	// Short templates, so the entry overhead dominates and the test stays
	// quick under -race.
	for i := range 2 * mustParseCacheMaxBytes / mustParseCacheEntryCost("0000000") {
		mustParseVerdictOf(fmt.Sprintf("%07d", i))
	}
	require.LessOrEqual(t, mustParseCache.bytes, mustParseCacheMaxBytes)
	require.Greater(t, mustParseCache.bytes, mustParseCacheMaxBytes-mustParseCacheEntryCost("0000000"))
	require.False(t, mustParseVerdictOf("{{ok}}").failed)
	require.True(t, mustParseVerdictOf("{{").failed)

	// An oversized template is never stored.
	resetMustParseCache()
	mustParseVerdictOf(strings.Repeat("z", mustParseCacheMaxTemplate+1))
	require.Empty(t, mustParseCache.m)
}

// TestMustParseCacheConcurrent exercises the cache from many goroutines; run
// with -race.
func TestMustParseCacheConcurrent(t *testing.T) {
	resetMustParseCache()
	t.Cleanup(resetMustParseCache)
	tpls := []string{"{{a}}", "{{", "{{#if x}}y{{/if}}", "{{/x}}"}
	want := make([]mustParseVerdict, len(tpls))
	for i, tpl := range tpls {
		if _, err := raymond.Parse(tpl); err != nil {
			want[i] = mustParseVerdict{errMsg: err.Error(), failed: true}
		}
	}
	resetMustParseCache()
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Go(func() {
			for i := range 200 {
				k := (g + i) % len(tpls)
				if got := mustParseVerdictOf(tpls[k]); got != want[k] {
					t.Errorf("template %q: got %+v, want %+v", tpls[k], got, want[k])
					return
				}
			}
		})
	}
	wg.Wait()
}

func BenchmarkMustParse(b *testing.B) {
	const tpl = `(handlebars:must-parse "Dear {{name}}, {{#each items}}{{this.id}}: {{#if this.ok}}ok{{else}}{{this.reason}}{{/if}}\n{{/each}}Regards, {{sender}}")`
	for _, c := range []struct {
		name     string
		uncached bool
	}{{"uncached", true}, {"cached", false}} {
		b.Run(c.name, func(b *testing.B) {
			env := newHandlebarsEnv(b, c.uncached)
			b.ResetTimer()
			for range b.N {
				if v := env.LoadStringContext(context.Background(), "b", tpl); v.Type == lisp.LError {
					b.Fatal(v)
				}
			}
		})
	}
}
