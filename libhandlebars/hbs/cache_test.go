// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/luthersystems/svc/libhandlebars/hbs/internal/ast"
	"github.com/stretchr/testify/require"
)

// verdict renders a Parse result for comparison.
func verdict(p *Program, err error) string {
	if err != nil {
		herr, _ := err.(*Error) //nolint:errorlint // Parse returns *Error unwrapped
		return fmt.Sprintf("error %d %q", herr.Kind, herr.Msg)
	}
	return fmt.Sprintf("ok %d %s", p.SourceLen(), ast.Print(p.ast))
}

func TestParseCachedMatchesParse(t *testing.T) {
	c := newParseCache(cacheMaxBytes)
	small := Limits{MaxTemplateBytes: 64, MaxDepth: 2}
	srcs := append([]string{nest(3, "{{#if a}}", "{{/if}}"), strings.Repeat("x", 65)}, parseFragments...)
	for _, lim := range []Limits{DefaultLimits(), small, {}} {
		for range 2 { // miss, then hit
			for _, src := range srcs {
				want := verdict(Parse(src, lim))
				require.Equal(t, want, verdict(c.parse(src, lim, nil)), "%q %+v", src, lim)
			}
		}
	}
	// the limits are part of the key: the same source has two verdicts
	src := nest(3, "{{#if a}}", "{{/if}}")
	_, err := c.parse(src, small, nil)
	requireKind(t, err, KindLimit)
	_, err = c.parse(src, DefaultLimits(), nil)
	require.NoError(t, err)
}

func TestParseCachedSharesProgramsNotErrors(t *testing.T) {
	c := newParseCache(cacheMaxBytes)
	p1, err := c.parse("{{x}}", DefaultLimits(), nil)
	require.NoError(t, err)
	p2, err := c.parse("{{x}}", DefaultLimits(), nil)
	require.NoError(t, err)
	require.Same(t, p1, p2)

	_, err1 := c.parse("{{x", DefaultLimits(), nil)
	_, err2 := c.parse("{{x", DefaultLimits(), nil)
	require.Equal(t, err1, err2)
	require.NotSame(t, err1, err2)
	err1.(*Error).Msg = "changed" //nolint:errorlint,forcetypeassert // test mutates its copy
	_, err3 := c.parse("{{x", DefaultLimits(), nil)
	require.Equal(t, err2, err3)
}

func TestParseCachedIsByteBounded(t *testing.T) {
	c := newParseCache(64 << 10)
	for i := range 1000 {
		_, err := c.parse(fmt.Sprintf("{{x%d}}%s", i, strings.Repeat(" ", 1000)), DefaultLimits(), nil)
		require.NoError(t, err)
		_, used := c.stats()
		require.LessOrEqual(t, used, 64<<10)
	}
	n, _ := c.stats()
	require.Positive(t, n)

	// a template over the entry limit is parsed but not kept
	c = newParseCache(cacheMaxBytes)
	_, err := c.parse(strings.Repeat("x", cacheMaxEntryBytes+1), Limits{MaxTemplateBytes: 2 << 20}, nil)
	require.NoError(t, err)
	n, _ = c.stats()
	require.Zero(t, n)

	// the process-wide cache is bounded the same way
	_, err = ParseCached("{{y}}", DefaultLimits())
	require.NoError(t, err)
	_, used := defaultCache.stats()
	require.LessOrEqual(t, used, cacheMaxBytes)
}

func TestParseCachedConcurrent(t *testing.T) {
	c := newParseCache(16 << 10)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 300 {
				src := fmt.Sprintf("{{#if a}}{{x%d}}{{/if}}%s", (i*7+g)%50, strings.Repeat("{{", i%2))
				want := verdict(Parse(src, DefaultLimits()))
				if got := verdict(c.parse(src, DefaultLimits(), nil)); got != want {
					t.Errorf("%q: got %s want %s", src, got, want)
					return
				}
			}
		})
	}
	wg.Wait()
}

// TestCacheWeighsAST: entries are weighed by their AST, not their source,
// so tag-dense templates cannot pin many times the cache bound.
func TestCacheWeighsAST(t *testing.T) {
	c := newParseCache(cacheMaxBytes)
	unit := "<p>Dear {{name}}, your balance is {{prettyp-num-en bal}} as of {{date-beautify d}}.</p>\n"
	for i := range 7 {
		src := strconv.Itoa(i) + strings.Repeat(unit, (1<<20-16)/len(unit))
		p, err := c.parse(src, DefaultLimits(), nil)
		require.NoError(t, err)
		require.Greater(t, p.tokens, 100_000)
	}
	n, bytes := c.stats()
	require.LessOrEqual(t, bytes, cacheMaxBytes)
	require.Less(t, n, 7, "the bound must evict tag-dense templates")
	t.Logf("%d of 7 dense 1 MiB templates cached, weight %d", n, bytes)
}

// TestCacheSkipsOversizedEntry: an entry heavier than the whole bound is
// not cached and evicts nothing.
func TestCacheSkipsOversizedEntry(t *testing.T) {
	c := newParseCache(1 << 20)
	_, err := c.parse("{{a}}", DefaultLimits(), nil)
	require.NoError(t, err)
	_, err = c.parse(strings.Repeat("{{a.b}}", 100_000), DefaultLimits(), nil)
	require.NoError(t, err)
	n, _ := c.stats()
	require.Equal(t, 1, n)
}
