package hbdiff

import (
	"runtime/debug"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func nest(n int, open, end string) string {
	return strings.Repeat(open, n) + "x" + strings.Repeat(end, n)
}

func elseIfChain(n int) string {
	return "{{#if a}}" + strings.Repeat("{{else if a}}", n-1) + "x{{/if}}"
}

// refDepthShapes builds templates nested n levels deep, as RefFatal counts.
var refDepthShapes = map[string]func(n int) string{
	"block":   func(n int) string { return nest(n, "{{#if t}}", "{{/if}}") },
	"inverse": func(n int) string { return nest(n, "{{^if f}}", "{{/if}}") },
	"else":    func(n int) string { return nest(n, "{{#if f}}{{else}}", "{{/if}}") },
	"unless":  func(n int) string { return nest(n, "{{#unless f}}", "{{/unless}}") },
	"each":    func(n int) string { return nest(n, "{{#each a}}", "{{/each}}") },
	"with":    func(n int) string { return nest(n, "{{#with this}}", "{{/with}}") },
	"sexpr":   func(n int) string { return "{{to-str " + nest(n, "(to-str ", ")") + "}}" },
	"hash":    func(n int) string { return "{{and k=" + nest(n, "(and k=", ")") + "}}" },
	"elseif":  elseIfChain,
	"raw": func(n int) string {
		return strings.Repeat("{{#if t}}", n-1) + "{{{{raw}}}}x{{{{/raw}}}}" + strings.Repeat("{{/if}}", n-1)
	},
}

// refDepthCtx makes every shape above evaluate every level.
const refDepthCtx = `{"t": true, "f": false, "a": [1]}`

// TestRefFatalDepth: RefFatal measures nesting depth, not the number of
// tags, so large shallow templates are still compared with the reference.
func TestRefFatalDepth(t *testing.T) {
	var b strings.Builder
	for range 1250 {
		b.WriteString("{{#if t}}{{x}}{{/if}}{{y}}{{to-str (to-str z)}}")
	}
	shallow := b.String()
	require.Greater(t, strings.Count(shallow, "{{")+strings.Count(shallow, "("), 5000)
	require.False(t, RefFatal(shallow))

	for name, build := range refDepthShapes {
		require.False(t, RefFatal(build(maxRefDepth)), "%s at %d", name, maxRefDepth)
		require.True(t, RefFatal(build(maxRefDepth+1)), "%s at %d", name, maxRefDepth+1)
	}
	// Closed blocks give their depth back.
	require.False(t, RefFatal(strings.Repeat(nest(maxRefDepth, "{{#if t}}", "{{/if}}"), 3)))
	// Unclosed blocks keep it.
	require.True(t, RefFatal(strings.Repeat("{{#if t}}", maxRefDepth+1)))
}

// refBoundStack is the stack the reference may use at maxRefDepth. The Go
// default is 1 GB; staying under a small fraction of it shows the bound
// leaves a wide margin. Exceeding it kills the test binary.
const refBoundStack = 64 << 20

// TestRefDepthBound renders every deep shape at maxRefDepth through the
// reference on a stack of refBoundStack bytes: the bound RefFatal applies
// is one the reference survives.
func TestRefDepthBound(t *testing.T) {
	old := debug.SetMaxStack(refBoundStack)
	defer debug.SetMaxStack(old)
	for name, build := range refDepthShapes {
		tpl := build(maxRefDepth)
		done := make(chan Result)
		go func() { done <- Ref(tpl, []byte(refDepthCtx)) }()
		r := <-done
		require.NotEqual(t, KindPanic, r.ErrKind, "%s: %s", name, describe(r))
		cand := DefaultCandidate(tpl, []byte(refDepthCtx))
		require.Equal(t, KindLimit, cand.ErrKind, "%s: %s", name, describe(cand))
		t.Logf("%s at depth %d: reference %s", name, maxRefDepth, describe(r)[:min(60, len(describe(r)))])
	}
}
