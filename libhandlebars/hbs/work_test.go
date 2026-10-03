// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/svc/libhandlebars/hbs"
	"github.com/stretchr/testify/require"
)

// steps renders tpl with ctx and returns the steps charged.
func steps(t *testing.T, tpl, ctx string) int64 {
	t.Helper()
	m := &countMeter{}
	_, err := mustParse(t, tpl).Render(mustCtx(t, ctx), hbs.Options{Meter: m})
	require.NoError(t, err, tpl)
	return m.used
}

func requireLimit(t *testing.T, err error, msg string) {
	t.Helper()
	var he *hbs.Error
	require.ErrorAs(t, err, &he)
	require.Equal(t, hbs.KindLimit, he.Kind, he.Msg)
	require.Contains(t, he.Msg, msg)
}

// TestCapturedBytesBounded: block helpers called as subexpressions capture
// whole sections as strings, and hash arguments keep them alive. The bytes
// a render produces, captures included, are bounded, so the heap is too.
func TestCapturedBytesBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("{{#if (and")
	for i := range 256 {
		fmt.Fprintf(&b, " k%d=(with t)", i)
	}
	b.WriteString(")}}{{#each a}}{{{../s}}}{{/each}}{{/if}}")
	ctx := `{"t": true, "a": [` + strings.TrimSuffix(strings.Repeat("1,", 15), ",") + `], "s": "` + strings.Repeat("x", 1<<20) + `"}`

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := mustParse(t, b.String()).Render(mustCtx(t, ctx), hbs.Options{})
	runtime.ReadMemStats(&after)
	requireLimit(t, err, "template evaluation produces more than 134217728 bytes")
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<30), "allocated bytes")

	// Helper results count too: each to-str of a large array builds a
	// string.
	var h strings.Builder
	h.WriteString("{{and")
	for i := range 256 {
		fmt.Fprintf(&h, " k%d=(to-str a)", i)
	}
	h.WriteString("}}")
	_, err = mustParse(t, h.String()).Render(mustCtx(t, `{"a": [`+strings.TrimSuffix(strings.Repeat(`"`+strings.Repeat("y", 1<<10)+`",`, 1<<10), ",")+`]}`), hbs.Options{Mode: hbs.ModeFixed})
	require.NoError(t, err, "to-str of an array is \"\" in both modes")
	_, err = mustParse(t, strings.ReplaceAll(h.String(), "to-str a", "escape-uri-component a")).Render(mustCtx(t, `{"a": [`+strings.TrimSuffix(strings.Repeat(`"`+strings.Repeat("y", 1<<10)+`",`, 1<<10), ",")+`]}`), hbs.Options{})
	requireLimit(t, err, "template evaluation produces more than")
}

// TestHelperCharges pins what helper work costs, so steps bound wall time.
func TestHelperCharges(t *testing.T) {
	// round-to-nth: its precision counts as produced bytes, charged before
	// formatting; then its result and the output do. Produced bytes cost a
	// step per started KiB of their running total: 999,999 + 1,000,001 +
	// 1,000,001 bytes is 2930 KiB, 2929 more than the small case's 1.
	base := steps(t, `{{round-to-nth "1" "2"}}`, `{}`)
	require.Equal(t, base+2929, steps(t, `{{round-to-nth "1" "999999"}}`, `{}`))
	require.Equal(t, base+2929, steps(t, `{{round-to-nth "1" "999999"}}`, `{}`), "deterministic")

	// A string argument costs a step per started KiB read; a non-string
	// one, a step per started KiB of the string built.
	small := steps(t, `{{eq s "x"}}`, `{"s": "a"}`)
	require.Equal(t, small+3, steps(t, `{{eq s "x"}}`, `{"s": "`+strings.Repeat("a", 4096)+`"}`))
	arr := `{"a": [` + strings.TrimSuffix(strings.Repeat(`"`+strings.Repeat("a", 1023)+`",`, 100), ",") + `]}`
	// str(a) builds 102,300 bytes: 100 started KiB of produced bytes, 99
	// more than the small case's output; and s's 1-step read is not made.
	require.Equal(t, small+98, steps(t, `{{eq a "x"}}`, arr))

	// select and in-string-array: one step per element scanned.
	items := func(n int) string {
		return `{"a": [` + strings.TrimSuffix(strings.Repeat(`{"k": "no"},`, n), ",") + `]}`
	}
	require.Equal(t, steps(t, `{{#select from=a where="k=v"}}x{{/select}}`, items(10))+990,
		steps(t, `{{#select from=a where="k=v"}}x{{/select}}`, items(1000)))
	strs := func(n int) string { return `{"a": [` + strings.TrimSuffix(strings.Repeat(`"no",`, n), ",") + `]}` }
	require.Equal(t, steps(t, `{{in-string-array haystack=a needle="v"}}`, strs(10))+990,
		steps(t, `{{in-string-array haystack=a needle="v"}}`, strs(1000)))
}

// TestMaxSteps: a render stops at Limits.MaxSteps with no Meter set, so a
// template whose work grows exponentially with the context terminates.
func TestMaxSteps(t *testing.T) {
	// Each level renders the #with section twice, one level down, so the
	// work doubles per level; the leaf's "n": false stops the descent (a
	// missing n would climb to the parent and recurse until MaxDepth).
	ctx := `{"x": ` + strings.Repeat(`{"n": `, 30) + `false` + strings.Repeat(`}`, 30) + `}`
	p := mustParse(t, `{{#with x}}{{with n}}{{with n}}{{/with}}`)
	_, err := p.Render(mustCtx(t, ctx), hbs.Options{Limits: hbs.Limits{MaxSteps: 100_000}})
	requireLimit(t, err, "template evaluation exceeds the maximum of 100000 steps")

	nested := mustParse(t, `{{#each a}}{{#each ../a}}{{#each ../../a}}{{#each ../../../a}}x{{/each}}{{/each}}{{/each}}{{/each}}`)
	big := `{"a": [` + strings.TrimSuffix(strings.Repeat("1,", 200), ",") + `]}`
	_, err = nested.Render(mustCtx(t, big), hbs.Options{Limits: hbs.Limits{MaxSteps: 100_000}})
	requireLimit(t, err, "steps")

	// The default applies when the field is zero.
	if raceEnabled || testing.Short() {
		return
	}
	start := time.Now()
	_, err = p.Render(mustCtx(t, ctx), hbs.Options{})
	requireLimit(t, err, fmt.Sprintf("template evaluation exceeds the maximum of %d steps", hbs.DefaultLimits().MaxSteps))
	t.Logf("default MaxSteps reached in %v", time.Since(start))

	// A Meter error still wins over the step limit.
	_, err = p.Render(mustCtx(t, ctx), hbs.Options{Meter: &countMeter{limit: 1000}, Limits: hbs.Limits{MaxSteps: 1000}})
	require.ErrorIs(t, err, errBudget)
}

// TestIntLiteralNear2p63: an integer literal within about 512 of 2^63
// parses to the float64 2^63, whose int conversion is pinned to the amd64
// result on every CPU.
func TestIntLiteralNear2p63(t *testing.T) {
	for _, mode := range []hbs.Mode{hbs.ModeCompat, hbs.ModeFixed} {
		for tpl, want := range map[string]string{
			"{{to-str 9223372036854775807}}": "-9223372036854775808",
			"{{to-str 9223372036854775296}}": "-9223372036854775808",
			"{{to-str 9223372036854775295}}": "9223372036854774784",
			"{{to-str 123}}":                 "123",
		} {
			got, err := mustParse(t, tpl).Render(mustCtx(t, `{}`), hbs.Options{Mode: mode})
			require.NoError(t, err)
			require.Equal(t, want, got, "%s mode %d", tpl, mode)
		}
	}
}

// TestPathWorkCharged: mapping a path over an array context costs a step
// per element, and resolving a path a step per segment, so MaxSteps bounds
// the time these take.
func TestPathWorkCharged(t *testing.T) {
	// A #with over an array maps every path inside it over the array.
	n := 20_000
	ctx := `{"a": [` + strings.TrimSuffix(strings.Repeat("1,", n), ",") + `], "b": [` + strings.TrimSuffix(strings.Repeat(`{"x": 1},`, n), ",") + `]}`
	p := mustParse(t, `{{#each a}}{{#with ../b}}{{#if x}}{{/if}}{{/with}}{{/each}}`)
	start := time.Now()
	_, err := p.Render(mustCtx(t, ctx), hbs.Options{})
	requireLimit(t, err, "steps")
	if !raceEnabled {
		require.Less(t, time.Since(start), 10*time.Second)
	}
	small := steps(t, `{{#with b}}{{#if x}}{{/if}}{{/with}}`, `{"b": [{"x": 1}]}`)
	// x is mapped over every element: a step for the element and one for
	// its one-segment path, so 999 more elements cost 1998 more steps.
	require.Equal(t, small+1998, steps(t, `{{#with b}}{{#if x}}{{/if}}{{/with}}`, `{"b": [`+strings.TrimSuffix(strings.Repeat(`{"x": 1},`, 1000), ",")+`]}`))

	// Each path segment costs a step.
	one := steps(t, `{{d}}`, `{"d": 1}`)
	deep := `{"d": ` + strings.Repeat(`{"x": `, 250) + `1` + strings.Repeat(`}`, 250) + `}`
	require.Equal(t, one+250, steps(t, `{{d`+strings.Repeat(".x", 250)+`}}`, deep))
}

// TestStrOfArrayBounded: str() of an array checks the produced-bytes bound
// as it builds, so it stops long before building the whole string.
func TestStrOfArrayBounded(t *testing.T) {
	ctx := mustCtx(t, `{"a": [`+strings.TrimSuffix(strings.Repeat("1e308,", 200_000), ",")+`]}`)
	p := mustParse(t, `{{#if (eq a "x")}}y{{/if}}`)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := p.Render(ctx, hbs.Options{Limits: hbs.Limits{MaxOutputBytes: 1 << 20}})
	runtime.ReadMemStats(&after)
	requireLimit(t, err, "template evaluation produces more than 8388608 bytes")
	// The whole string is about 62 MB; building stops near the 8 MiB bound.
	// Go grows a large slice by about 1.25x, so all the buffers it went
	// through add up to about 5x what it holds: about 40 MiB here.
	alloc := after.TotalAlloc - before.TotalAlloc
	t.Logf("allocated %d bytes", alloc)
	require.Less(t, alloc, uint64(8*(8<<20)))
}
