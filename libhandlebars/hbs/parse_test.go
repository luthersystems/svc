package hbs

import (
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/svc/libhandlebars/hbs/parser"
	"github.com/stretchr/testify/require"
)

// requireKind requires err to be an *Error of the given kind and returns
// its message.
func requireKind(t *testing.T, err error, kind ErrorKind, msgAndArgs ...any) string {
	t.Helper()
	var herr *Error
	require.ErrorAs(t, err, &herr, msgAndArgs...)
	require.Equal(t, kind, herr.Kind, "error %q", herr.Msg)
	return herr.Msg
}

// TestParseTemplateSize checks MaxTemplateBytes at the cap and at cap+1,
// through Parse and ParseCached. The cap counts bytes, not runes.
func TestParseTemplateSize(t *testing.T) {
	for _, limit := range []int{1, 2, 100, 4096, DefaultLimits.MaxTemplateBytes} {
		lim := Limits{MaxTemplateBytes: limit, MaxDepth: DefaultLimits.MaxDepth}
		c := newParseCache(cacheMaxBytes)
		for name, build := range map[string]func(n int) string{
			"ascii": func(n int) string { return strings.Repeat("x", n) },
			"utf8":  func(n int) string { return strings.Repeat("é", n/2) + strings.Repeat("x", n%2) },
			"mustache": func(n int) string {
				if n < 5 {
					return strings.Repeat("x", n)
				}
				return "{{" + strings.Repeat("a", n-4) + "}}"
			},
		} {
			at, over := build(limit), build(limit+1)
			require.Len(t, at, limit, name)
			require.Len(t, over, limit+1, name)
			for _, parse := range []func(string, Limits) (*Program, error){Parse, c.parse} {
				p, err := parse(at, lim)
				require.NoError(t, err, "%s at %d bytes", name, limit)
				require.Equal(t, limit, p.SourceLen())
				_, err = parse(over, lim)
				msg := requireKind(t, err, KindLimit, "%s at %d bytes", name, limit+1)
				require.Equal(t, fmt.Sprintf("template is %d bytes, limit is %d", limit+1, limit), msg)
			}
		}
	}
}

// TestParseSizeCheckedFirst checks the size cap is applied before the
// depth scan: a template over both caps reports its size.
func TestParseSizeCheckedFirst(t *testing.T) {
	src := nest(100, "{{#if a}}", "{{/if}}")
	_, err := Parse(src, Limits{MaxTemplateBytes: len(src) - 1, MaxDepth: 2})
	msg := requireKind(t, err, KindLimit)
	require.Equal(t, fmt.Sprintf("template is %d bytes, limit is %d", len(src), len(src)-1), msg)
}

func TestParseZeroLimitsAreDefaults(t *testing.T) {
	_, err := Parse(strings.Repeat("x", DefaultLimits.MaxTemplateBytes+1), Limits{})
	requireKind(t, err, KindLimit)
	_, err = Parse(nest(DefaultLimits.MaxDepth+1, "{{#if a}}", "{{/if}}"), Limits{})
	requireKind(t, err, KindLimit)
	_, err = Parse(nest(DefaultLimits.MaxDepth, "{{#if a}}", "{{/if}}"), Limits{})
	require.NoError(t, err)
}

// nest returns n copies of open, then "x", then n copies of end.
func nest(n int, open, end string) string {
	return wrap(n, open, "x", end)
}

// wrap returns n copies of open, then inner, then n copies of end.
func wrap(n int, open, inner, end string) string {
	return strings.Repeat(open, n) + inner + strings.Repeat(end, n)
}

// elseIfChain returns a block with n-1 else-if links: depth n.
func elseIfChain(n int) string {
	return "{{#if a}}x" + strings.Repeat("{{else if b}}y", n-1) + "{{else}}z{{/if}}"
}

// depthCases build a template whose nesting depth is exactly n.
var depthCases = map[string]func(n int) string{
	"block":   func(n int) string { return nest(n, "{{#if a}}", "{{/if}}") },
	"inverse": func(n int) string { return nest(n, "{{^if a}}", "{{/if}}") },
	"each":    func(n int) string { return nest(n, "{{#each a as |b|}}\n", "\n{{/each}}") },
	"else":    func(n int) string { return nest(n, "{{#if a}}y{{else}}", "{{/if}}") },
	"elseif":  elseIfChain,
	"sexpr":   func(n int) string { return "{{f " + nest(n, "(g ", ")") + "}}" },
	"hash":    func(n int) string { return "{{f " + nest(n, "(g k=", ")") + "}}" },
	"blocksexpr": func(n int) string {
		return wrap(n/2, "{{#if a}}", "{{#if "+nest(n-n/2-1, "(g ", ")")+"}}{{/if}}", "{{/if}}")
	},
	"raw": func(n int) string {
		return wrap(n-1, "{{#if a}}", "{{{{raw}}}}{{#x}}{{{{/raw}}}}", "{{/if}}")
	},
	"rawsexpr": func(n int) string { return "{{{{raw " + nest(n-1, "(g ", ")") + "}}}}x{{{{/raw}}}}" },
	"chainsexpr": func(n int) string {
		return "{{#if a}}" + strings.Repeat("{{else if b}}", n-4) + "{{else if (f (g x))}}{{/if}}"
	},
	"partial": func(n int) string { return wrap(n-1, "{{#if a}}", "{{> (f x)}}", "{{/if}}") },
}

func TestParseDepthLimit(t *testing.T) {
	for _, limit := range []int{1, 2, 7, DefaultLimits.MaxDepth} {
		lim := Limits{MaxTemplateBytes: 1 << 20, MaxDepth: limit}
		for name, build := range depthCases {
			if limit < 4 && (name == "chainsexpr" || name == "blocksexpr") {
				continue
			}
			at := build(limit)
			_, err := Parse(at, lim)
			require.NoError(t, err, "%s at depth %d", name, limit)
			// within the limits the engine parses exactly as raymond did
			checkSame(t, at)

			over := build(limit + 1)
			_, err = Parse(over, lim)
			msg := requireKind(t, err, KindLimit, "%s at depth %d", name, limit+1)

			require.Contains(t, msg, "template nesting depth exceeds limit of")

			// The parser's own counter agrees with the prescan.
			_, perr := parser.ParseLimit(at, limit)
			require.NoError(t, perr, "%s at depth %d", name, limit)
			_, perr = parser.ParseLimit(over, limit)
			var lerr *parser.LimitError
			require.ErrorAs(t, perr, &lerr, "%s at depth %d", name, limit+1)
			require.Equal(t, msg, lerr.Msg, name)

			// the cache gives the same verdicts
			c := newParseCache(cacheMaxBytes)
			_, err = c.parse(at, lim)
			require.NoError(t, err, "%s at depth %d", name, limit)
			_, err = c.parse(over, lim)
			require.Equal(t, msg, requireKind(t, err, KindLimit), name)
		}
	}
}

func TestParseDepthErrorMessage(t *testing.T) {
	_, err := Parse("{{#if a}}\n{{#if b}}\n{{#if c}}{{/if}}{{/if}}{{/if}}", Limits{MaxDepth: 2})
	msg := requireKind(t, err, KindLimit)
	require.Equal(t, "Parse error on line 3:\ntemplate nesting depth exceeds limit of 2", msg)
}

// withMaxStack runs f on a new goroutine whose stack may not grow past
// maxStack bytes. Recursion proportional to the input overflows that stack
// and kills the test binary, which is the failure this guards against.
func withMaxStack(maxStack int, f func()) {
	old := debug.SetMaxStack(maxStack)
	defer debug.SetMaxStack(old)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	<-done
}

// testMaxStack is the stack a parse may use. Parsing recurses at most
// MaxDepth levels; a recursion per nesting level of a million-deep
// template would need hundreds of MB.
const testMaxStack = 4 << 20

// TestParseMillionNestedIf is the stack overflow guard (D4): a million
// nested {{#if}} blocks, with the size cap raised out of the way, fail with
// a depth limit error in under a second, on a stack of 4 MiB.
func TestParseMillionNestedIf(t *testing.T) {
	const n = 1_000_000
	src := nest(n, "{{#if a}}", "{{/if}}")
	big := Limits{MaxTemplateBytes: 1 << 30, MaxDepth: DefaultLimits.MaxDepth}

	var err error
	var took time.Duration
	withMaxStack(testMaxStack, func() {
		start := time.Now()
		_, err = Parse(src, big)
		took = time.Since(start)
	})
	msg := requireKind(t, err, KindLimit)
	require.Equal(t, fmt.Sprintf("Parse error on line 1:\ntemplate nesting depth exceeds limit of %d", DefaultLimits.MaxDepth), msg)
	require.Less(t, took, time.Second)
	t.Logf("%d nested {{#if}}: %v", n, took)
}

// TestParseDeepNestingFailsFast checks the same for other deep shapes,
// through Parse with the default and raised size caps, and through the
// parser alone (its own depth counter, without the prescan).
func TestParseDeepNestingFailsFast(t *testing.T) {
	const n = 1_000_000
	big := Limits{MaxTemplateBytes: 1 << 30, MaxDepth: DefaultLimits.MaxDepth}
	for name, src := range map[string]string{
		"block":   nest(n, "{{#if a}}", "{{/if}}"),
		"open":    strings.Repeat("{{#if a}}", n),
		"inverse": nest(n, "{{^if a}}", "{{/if}}"),
		"each":    nest(n, "{{#each a as |b|}}", "{{/each}}"),
		"sexpr":   "{{f " + strings.Repeat("(g ", n) + "}}",
		"hash":    "{{f " + nest(n, "(g k=", ")") + "}}",
		"elseif":  elseIfChain(n),
		"raw":     strings.Repeat("{{#if a}}", n) + "{{{{raw}}}}x{{{{/raw}}}}",
	} {
		for _, parse := range []func() error{
			func() error {
				_, err := Parse(src, DefaultLimits)
				requireKind(t, err, KindLimit, name) // the size cap
				return nil
			},
			func() error {
				_, err := Parse(src, big)
				msg := requireKind(t, err, KindLimit, name)
				require.Contains(t, msg, "nesting depth", name)
				return nil
			},
			func() error {
				_, err := parser.ParseLimit(src, DefaultLimits.MaxDepth)
				var lerr *parser.LimitError
				require.ErrorAs(t, err, &lerr, name)
				return nil
			},
		} {
			var took time.Duration
			withMaxStack(testMaxStack, func() {
				start := time.Now()
				_ = parse()
				took = time.Since(start)
			})
			require.Less(t, took, time.Second, name)
		}
	}
}

// settledGoroutines returns runtime.NumGoroutine once it is at most want,
// or its last value after a second. Goroutines of earlier tests may still
// be exiting when a test starts.
func settledGoroutines(want int) int {
	n := runtime.NumGoroutine()
	for deadline := time.Now().Add(time.Second); n > want && time.Now().Before(deadline); n = runtime.NumGoroutine() {
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	return n
}

// TestParseLeaksNoGoroutines is the goroutine leak guard (R2): raymond's
// lexer ran on a goroutine that a failed parse never drained. The engine's
// lexer is synchronous, so no parse, failed or not, leaves one behind.
func TestParseLeaksNoGoroutines(t *testing.T) {
	bad := []string{
		"{{#if a}}", "{{x", "{{! x", "{{!-- x", "{{f \"x}}", "{{/if}}", "{{{{raw}}}}x", "{{x.5}}",
		"{{#if (f}}{{/if}}", "{{#if a}}{{/unless}}", "x\n{{f (g}}\ny", "{{[x", "{{#each a as |b}}",
	}
	deep := nest(DefaultLimits.MaxDepth+1, "{{#if a}}", "{{/if}}")
	before := runtime.NumGoroutine()
	c := newParseCache(cacheMaxBytes)
	for i := range 10_000 {
		src := bad[i%len(bad)]
		_, err := Parse(src, DefaultLimits)
		requireKind(t, err, KindParse, src)
		// a different source each time, so the cache misses
		_, err = c.parse(src+strings.Repeat(" ", i%64), DefaultLimits)
		requireKind(t, err, KindParse, src)
		_, err = parser.Parse(src)
		require.Error(t, err, src)
		if i%10 == 0 {
			_, err = Parse(deep, DefaultLimits)
			requireKind(t, err, KindLimit)
			_, err = Parse(src, Limits{MaxTemplateBytes: 1, MaxDepth: 1})
			requireKind(t, err, KindLimit)
		}
	}
	require.LessOrEqual(t, settledGoroutines(before), before)
}

// scalingShapes build templates of about n bytes that stress one path each.
var scalingShapes = map[string]func(n int) string{
	"content":   func(n int) string { return strings.Repeat("a{b\\c ", n/6) },
	"mustaches": func(n int) string { return strings.Repeat("{{a.b}} ", n/8) },
	"helpers":   func(n int) string { return strings.Repeat("{{f a \"s\" 1 k=(g b)}}\n", n/24) },
	"blocks":    func(n int) string { return strings.Repeat("  {{#if a}}\n  x\n  {{else}}\n  y\n  {{/if}}\n", n/48) },
	"elseif":    func(n int) string { return strings.Repeat("{{#if a}}x{{else if b}}y{{else if c}}z{{/if}}\n", n/48) },
	"comment":   func(n int) string { return "{{!-- " + strings.Repeat(" ", n) + " --}}" },
	"commentws": func(n int) string { return "{{! " + strings.Repeat("     x", n/6) + " }}" },
	"wscontent": func(n int) string { return "{{#if a}}" + strings.Repeat(" ", n) + "{{/if}}" },
	"wsnl":      func(n int) string { return "{{#if a}}" + strings.Repeat(" \n", n/2) + "{{/if}}" },
	"path":      func(n int) string { return "{{a" + strings.Repeat(".b", n/2) + "}}" },
	"hashpairs": func(n int) string { return "{{f" + strings.Repeat(" k=v", n/4) + "}}" },
	"params":    func(n int) string { return "{{f" + strings.Repeat(" a", n/2) + "}}" },
	"raw":       func(n int) string { return "{{{{raw}}}}" + strings.Repeat("{{x}}", n/5) + "{{{{/raw}}}}" },
	"escaped":   func(n int) string { return strings.Repeat("\\{{x}} ", n/7) },
	"else":      func(n int) string { return "{{" + strings.Repeat(" ", n) + "x}}" },
	"blockparm": func(n int) string { return "{{#each a as" + strings.Repeat(" ", n) + "|x|}}{{/each}}" },
}

// parseTime returns the fastest of five parses of src, each from a
// collected heap.
func parseTime(t *testing.T, src string, lim Limits) time.Duration {
	best := time.Duration(1 << 62)
	for range 5 {
		runtime.GC()
		start := time.Now()
		_, err := Parse(src, lim)
		d := time.Since(start)
		var herr *Error
		if errors.As(err, &herr) && herr.Kind == KindLimit {
			t.Fatalf("unexpected limit error: %s", herr.Msg)
		}
		best = min(best, d)
	}
	return best
}

// TestParseScalesLinearly checks parse time grows about linearly from 10KB
// to 100KB to 1MB on every shape: at most 30x per 10x of input (3x slack).
func TestParseScalesLinearly(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing test: skipped with -short and -race")
	}
	lim := Limits{MaxTemplateBytes: 4 << 20, MaxDepth: DefaultLimits.MaxDepth}
	for name, build := range scalingShapes {
		var times [3]time.Duration
		for i, n := range []int{10 << 10, 100 << 10, 1 << 20} {
			times[i] = parseTime(t, build(n), lim)
		}
		t.Logf("%-10s 10KB %v  100KB %v  1MB %v", name, times[0], times[1], times[2])
		// a floor keeps timer noise on tiny inputs from failing the test
		floor := 200 * time.Microsecond
		require.Less(t, times[1], 30*max(times[0], floor), "%s: 10KB -> 100KB", name)
		require.Less(t, times[2], 30*max(times[1], floor), "%s: 100KB -> 1MB", name)
	}
}
