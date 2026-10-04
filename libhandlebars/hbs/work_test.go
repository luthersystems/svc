// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs_test

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"
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
	// round-to-nth: its precision is checked against the produced-bytes
	// bound before formatting; then its result (1,000,001 bytes) and the
	// output (the same) are charged at their exact length: a step per started
	// KiB of the running total, 2,000,002 bytes or 1954 KiB, 1953 more than
	// the small case's 1; escaping the output scans it, a step per started
	// 16 bytes: 62,501, 62,500 more than the small case's 1; and formatting
	// is charged on the precision, a step per started 8 digits: 125,000,
	// 124,999 more than the small case's 1; and copying the output, a step
	// per whole 16 bytes: 62,500.
	base := steps(t, `{{round-to-nth "1" "2"}}`, `{}`)
	require.Equal(t, base+1953+62500+124999+62500, steps(t, `{{round-to-nth "1" "999999"}}`, `{}`))
	require.Equal(t, base+1953+62500+124999+62500, steps(t, `{{round-to-nth "1" "999999"}}`, `{}`), "deterministic")

	// A string argument costs a step per started 256 bytes read; a
	// non-string one, a step per started KiB of the string built.
	small := steps(t, `{{eq s "x"}}`, `{"s": "a"}`)
	require.Equal(t, small+15, steps(t, `{{eq s "x"}}`, `{"s": "`+strings.Repeat("a", 4096)+`"}`))
	arr := `{"a": [` + strings.TrimSuffix(strings.Repeat(`"`+strings.Repeat("a", 1023)+`",`, 100), ",") + `]}`
	// str(a) builds 102,300 bytes: 100 started KiB of produced bytes, 99
	// more than the small case's output; s's 1-step read is not made; each
	// of the 100 elements walked costs a step; and each 1023-byte string
	// leaf is read, 4 steps each (a step per started 256 bytes).
	require.Equal(t, small+98+100+400, steps(t, `{{eq a "x"}}`, arr))

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
	// x is mapped over every element: a step for the element, one for its
	// one-segment path and one for the key lookup, so 999 more elements
	// cost 2997 more steps.
	require.Equal(t, small+2997, steps(t, `{{#with b}}{{#if x}}{{/if}}{{/with}}`, `{"b": [`+strings.TrimSuffix(strings.Repeat(`{"x": 1},`, 1000), ",")+`]}`))

	// Each path segment costs steps.
	one := steps(t, `{{d}}`, `{"d": 1}`)
	deep := `{"d": ` + strings.Repeat(`{"x": `, 250) + `1` + strings.Repeat(`}`, 250) + `}`
	// Two steps per segment (resolving it and looking its key up); {{d}}
	// also pays one for the helper-name lookup a dotted path does not make.
	require.Equal(t, one+2*250-1, steps(t, `{{d`+strings.Repeat(".x", 250)+`}}`, deep))
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

// TestOutputCapExactWithEscaping: the output cap applies to the exact
// escaped length, at cap and cap+1, for each escaped byte.
func TestOutputCapExactWithEscaping(t *testing.T) {
	for c, esc := range map[string]string{"&": "&amp;", "<": "&lt;", ">": "&gt;", `"`: "&quot;", "'": "&apos;", "a": "a"} {
		s := strings.Repeat(c, 7) + "x"
		want := strings.Repeat(esc, 7) + "x"
		ctx := mustCtx(t, `{"s": `+strconvQuote(s)+`}`)
		got, err := mustParse(t, `{{s}}`).Render(ctx, hbs.Options{Limits: hbs.Limits{MaxOutputBytes: len(want)}})
		require.NoError(t, err, c)
		require.Equal(t, want, got)
		_, err = mustParse(t, `{{s}}`).Render(ctx, hbs.Options{Limits: hbs.Limits{MaxOutputBytes: len(want) - 1}})
		requireLimit(t, err, "rendered output exceeds")
	}
}

// TestValueWalksBounded: printing or stringifying a nested array counts
// against MaxDepth, and every element walked costs a step.
func TestValueWalksBounded(t *testing.T) {
	deep := mustCtx(t, `{"a": `+strings.Repeat("[", 9000)+strings.Repeat("]", 9000)+`}`)
	for _, tpl := range []string{`{{a}}`, `{{eq a ""}}`, `{{#equal a ""}}x{{/equal}}`} {
		var err error
		withSmallStack(func() {
			_, err = mustParse(t, tpl).Render(deep, hbs.Options{Limits: hbs.Limits{MaxDepth: 8}})
		})
		requireLimit(t, err, "maximum depth of 8")
		withSmallStack(func() { _, err = mustParse(t, tpl).Render(deep, hbs.Options{}) })
		requireLimit(t, err, "maximum depth of 256")
	}

	arr := func(n int) string { return `{"a": [` + strings.TrimSuffix(strings.Repeat("1,", n), ",") + `]}` }
	require.GreaterOrEqual(t, steps(t, `{{a}}`, arr(100_000))-steps(t, `{{a}}`, arr(1)), int64(99_999))
	require.GreaterOrEqual(t, steps(t, `{{eq a ""}}`, arr(100_000))-steps(t, `{{eq a ""}}`, arr(1)), int64(99_999))

	// #each over an object charges every key before sorting them.
	obj := func(n int) string {
		var b strings.Builder
		b.WriteString(`{"m": {`)
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `"k%d": 1`, i)
		}
		b.WriteString(`}}`)
		return b.String()
	}
	m := &countMeter{limit: 1000}
	_, err := mustParse(t, `{{#each m}}{{/each}}`).Render(mustCtx(t, obj(100_000)), hbs.Options{Meter: m})
	require.ErrorIs(t, err, errBudget)
	require.Less(t, m.calls, 3, "the sort is charged before it runs")
}

// TestCompareAndKeyCharges: string comparisons and long path keys cost a
// step per started KiB.
func TestCompareAndKeyCharges(t *testing.T) {
	big := strings.Repeat("y", 1<<20)
	hay := `{"a": [` + strings.TrimSuffix(strings.Repeat(`"`+big+`",`, 16), ",") + `], "n": "` + big[:len(big)-1] + `z"}`
	small := `{"a": ["y"], "n": "z"}`
	require.GreaterOrEqual(t, steps(t, `{{in-string-array haystack=a needle=n}}`, hay)-steps(t, `{{in-string-array haystack=a needle=n}}`, small), int64(16*1024))

	key := strings.Repeat("k", 1<<19)
	ctx := `{"` + key + `": 1}`
	require.GreaterOrEqual(t, steps(t, `{{[`+key+`]}}`, ctx)-steps(t, `{{[k]}}`, `{"k": 1}`), int64(511))
}

// TestEscapeURIBoundedBeforeBuilding: escape-uri-component checks the
// produced-bytes bound with the exact escaped length before it escapes.
func TestEscapeURIBoundedBeforeBuilding(t *testing.T) {
	ctx := mustCtx(t, `{"s": "`+strings.Repeat("/", 3<<20)+`"}`)
	p := mustParse(t, `{{escape-uri-component s}}`)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := p.Render(ctx, hbs.Options{Limits: hbs.Limits{MaxOutputBytes: 1 << 20}})
	runtime.ReadMemStats(&after)
	requireLimit(t, err, "produces more than")
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20))
}

func strconvQuote(s string) string { return strconv.Quote(s) }

// withSmallStack runs f on a goroutine whose stack may not pass 256 KiB, so
// recursion proportional to a value's nesting kills the test binary.
func withSmallStack(f func()) {
	old := debug.SetMaxStack(256 << 10)
	defer debug.SetMaxStack(old)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	<-done
}

// TestRealisticHeadroom renders a synthetic document shaped like a large
// production letter, a 200 KB template with a 3-deep #each over a 300 KB
// context, and checks that it stays far below the default limits.
func TestRealisticHeadroom(t *testing.T) {
	var tpl strings.Builder
	para := `<p>Dear {{customer.name}}, your account {{customer.id}} shows a balance of {{prettyp-num-en customer.balance}} as of {{date-beautify customer.date}}.{{#if customer.vip}} Thank you for being a valued customer.{{/if}}</p>` + "\n"
	for tpl.Len() < 150_000 {
		tpl.WriteString(para)
	}
	tpl.WriteString(`{{#each sections}}<h2>{{title}}</h2>{{#each rows}}<tr>{{#each cells}}<td>{{#if (gt value "0")}}{{round-to-nth value "2"}}{{else}}{{escape-uri-component label}}{{/if}}</td>{{/each}}</tr>{{/each}}{{/each}}` + "\n")
	for tpl.Len() < 200_000 {
		tpl.WriteString(para)
	}

	var ctx strings.Builder
	ctx.WriteString(`{"customer": {"name": "A. Customer", "id": "ACC-0001", "balance": 1234567.891, "date": "2026-10-03", "vip": true}, "sections": [`)
	for s := range 100 {
		if s > 0 {
			ctx.WriteByte(',')
		}
		fmt.Fprintf(&ctx, `{"title": "Section %d", "rows": [`, s)
		for r := range 10 {
			if r > 0 {
				ctx.WriteByte(',')
			}
			ctx.WriteString(`{"cells": [`)
			for c := range 10 {
				if c > 0 {
					ctx.WriteByte(',')
				}
				fmt.Fprintf(&ctx, `{"value": "%d.%03d", "label": "cell %d/%d label"}`, r*c, s, r, c)
			}
			ctx.WriteString(`]}`)
		}
		ctx.WriteString(`]}`)
	}
	ctx.WriteString(`]}`)
	require.Greater(t, ctx.Len(), 300_000)

	m := &countMeter{}
	out, err := mustParse(t, tpl.String()).Render(mustCtx(t, ctx.String()), hbs.Options{Meter: m})
	require.NoError(t, err)
	lim := hbs.DefaultLimits()
	stepsPct := 100 * float64(m.used) / float64(lim.MaxSteps)
	t.Logf("template %d B, context %d B, output %d B, steps %d (%.2f%% of MaxSteps)", tpl.Len(), ctx.Len(), len(out), m.used, stepsPct)
	require.Less(t, stepsPct, 5.0)
	require.Less(t, len(out), lim.MaxOutputBytes/10)
}

// TestStringLengthCharges pins the charge of each operation whose work
// grows with a string's length (DETERMINISM.md, "Cost model").
func TestStringLengthCharges(t *testing.T) {
	long := func(n int, c string) string { return strings.Repeat(c, n) }
	kib := int64(1024)

	// #each over an object: sorting and looking up long keys. 9 keys of
	// 1 MiB sharing a prefix: sort 9 x 1024 x ceil(log2 10) and 9 x 1024
	// lookups at least.
	var m strings.Builder
	m.WriteString(`{"m": {`)
	for i := range 9 {
		if i > 0 {
			m.WriteByte(',')
		}
		fmt.Fprintf(&m, `"%s%d": 1`, long(1<<20-1, "k"), i)
	}
	m.WriteString(`}}`)
	require.GreaterOrEqual(t, steps(t, `{{#each m}}{{/each}}`, m.String()), 9*kib*4+9*kib)

	// global hashes its namespace and key on every read and write.
	key := long(4<<20, "z")
	require.GreaterOrEqual(t, steps(t, `{{global "n" key=k}}`, `{"k": "`+key+`"}`), 4*kib)
	require.GreaterOrEqual(t, steps(t, `{{global "n" key=k val="v"}}`, `{"k": "`+key+`"}`), 4*kib)

	// A hash pair's key is hashed when the pair is stored.
	require.GreaterOrEqual(t, steps(t, `{{and `+long(512<<10, "h")+`=1}}`, `{}`), int64(512))

	// A string-literal path is looked up in the context.
	require.GreaterOrEqual(t, steps(t, `{{"`+long(512<<10, "s")+`"}}`, `{"a": 1}`), int64(512))

	// A long helper name is hashed to find the helper.
	require.GreaterOrEqual(t, steps(t, `{{`+long(512<<10, "q")+` 1}}`, `{}`), int64(512))

	// Block parameters: a step per frame scanned, plus each equal-length
	// compare by KiB. 200 frames of 1 KiB names, then 100 references to the
	// outermost name.
	var bp strings.Builder
	for i := range 200 {
		fmt.Fprintf(&bp, `{{#each o as |%s%03d|}}`, long(1021, "p"), i)
	}
	bp.WriteString(`{{#each o}}` + strings.Repeat(`{{`+long(1021, "p")+`000}}`, 100) + `{{/each}}`)
	bp.WriteString(strings.Repeat(`{{/each}}`, 200))
	require.GreaterOrEqual(t, steps(t, bp.String(), `{"o": [1]}`), int64(100*200*2))
}

// TestFmtVBounded: prettyp-num-en's error message prints the value as fmt's
// %v does, with a depth-bounded walk.
func TestFmtVBounded(t *testing.T) {
	deep := mustCtx(t, `{"a": `+strings.Repeat("[", 9000)+strings.Repeat("]", 9000)+`}`)
	var err error
	withSmallStack(func() {
		_, err = mustParse(t, `{{prettyp-num-en a}}`).Render(deep, hbs.Options{})
	})
	requireLimit(t, err, "maximum depth of 256")

	// The message matches fmt for every kind of value.
	for _, ctx := range []string{
		`{"a": [1, 2.5, "x", true, null, [3, []], {"k": [1e21, -0.0001]}]}`,
		`{"a": {"b": {"c": [1, "two"]}, "a": null, "z": false}}`,
		`{"a": [1e100, 123456789, 0.000001, 1e-7, 100000000000000000000]}`,
		`{"a": []}`, `{"a": {}}`, `{"a": true}`, `{"a": null}`,
	} {
		m, ok := mustCtx(t, ctx).(map[string]any)
		require.True(t, ok)
		v := m["a"]
		_, err := mustParse(t, `{{prettyp-num-en a}}`).Render(mustCtx(t, ctx), hbs.Options{})
		if _, ok := v.(float64); ok {
			continue
		}
		var he *hbs.Error
		require.ErrorAs(t, err, &he)
		require.Equal(t, fmt.Sprintf("value passed in must be a number, got: %v", v), he.Msg, ctx)
	}
}

// allocDuring returns the bytes allocated while f runs.
func allocDuring(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// TestOversizedInputsRejectedBeforeCopying: a string too large for the
// remaining output or produced bytes is refused before it is copied,
// scanned or built into a message.
func TestOversizedInputsRejectedBeforeCopying(t *testing.T) {
	small := hbs.Options{Limits: hbs.Limits{MaxOutputBytes: 1024}}
	var err error

	// A large string leaf of an array being stringified.
	leaf := mustCtx(t, `{"a": ["`+strings.Repeat("x", 16<<20)+`"]}`)
	p := mustParse(t, `{{eq a "x"}}`)
	alloc := allocDuring(func() { _, err = p.Render(leaf, small) })
	requireLimit(t, err, "produces more than 8192 bytes")
	require.Less(t, alloc, uint64(1<<20))

	// A large string to escape.
	amp := mustCtx(t, `{"s": "`+strings.Repeat("&", 64<<20)+`"}`)
	p = mustParse(t, `{{s}}`)
	alloc = allocDuring(func() { _, err = p.Render(amp, small) })
	requireLimit(t, err, "rendered output exceeds the maximum of 1024 bytes")
	require.Less(t, alloc, uint64(1<<20))

	// A malformed select where-clause: no allocation per separator, and
	// the error text, which holds the clause, is bounded before it is built.
	w := strings.Repeat("=", 1<<20)
	wctx := mustCtx(t, `{"a": [], "w": "`+w+`"}`)
	p = mustParse(t, `{{#select from=a where=w}}x{{/select}}`)
	alloc = allocDuring(func() { _, err = p.Render(wctx, hbs.Options{}) })
	var he *hbs.Error
	require.ErrorAs(t, err, &he)
	require.Equal(t, hbs.KindRender, he.Kind)
	require.Equal(t, "select: 'where' not in K=V format: "+w, he.Msg)
	require.Less(t, alloc, uint64(4<<20))
	_, err = p.Render(wctx, small)
	requireLimit(t, err, "produces more than 8192 bytes")
	for where, ok := range map[string]bool{"k=v": true, "k=": true, "=v": true, "=": true, "k": false, "k=v=w": false, "": false} {
		_, err := mustParse(t, `{{#select from=a where="`+where+`"}}x{{/select}}`).Render(mustCtx(t, `{"a": []}`), hbs.Options{})
		require.Equal(t, ok, err == nil, where)
	}
}

// TestFmtVRepros pins the review's prettyp-num-en cases: a large array of
// nulls is charged per element, and a deep array hits the depth limit
// before the helper's own error.
func TestFmtVRepros(t *testing.T) {
	nulls := `{"a": [` + strings.TrimSuffix(strings.Repeat("null,", 1_000_000), ",") + `]}`
	m := &countMeter{}
	_, err := mustParse(t, `{{prettyp-num-en a}}`).Render(mustCtx(t, nulls), hbs.Options{Meter: m})
	require.Error(t, err)
	require.Greater(t, m.used, int64(1_000_000))

	deep := mustCtx(t, `{"a": `+strings.Repeat("[", 1001)+strings.Repeat("]", 1001)+`}`)
	_, err = mustParse(t, `{{prettyp-num-en a}}`).Render(deep, hbs.Options{Limits: hbs.Limits{MaxDepth: 8}})
	requireLimit(t, err, "maximum depth of 8")
}

// TestChargedBeforeAllocating pins the final review's cases: each is
// refused, or charged, before the large allocation it used to make.
func TestChargedBeforeAllocating(t *testing.T) {
	var err error

	// A shared DAG nested past MaxDepth: the measuring pass stops at the
	// depth limit after a few elements, not after walking 2^26 of them.
	dag := []any{nil}
	for range 26 {
		dag = []any{dag, dag}
	}
	p := mustParse(t, `{{eq a ""}}`)
	start := time.Now()
	_, err = p.Render(map[string]any{"a": dag}, hbs.Options{Limits: hbs.Limits{MaxDepth: 4, MaxSteps: 64}})
	requireLimit(t, err, "maximum depth of 4")
	if timingGuards() {
		require.Less(t, time.Since(start), 50*time.Millisecond)
	}

	// A large leaf is charged before its buffer is allocated.
	big := map[string]any{"a": []any{strings.Repeat("x", 32<<20)}}
	alloc := allocDuring(func() { _, err = p.Render(big, hbs.Options{Limits: hbs.Limits{MaxSteps: 64}}) })
	requireLimit(t, err, "maximum of 64 steps")
	require.Less(t, alloc, uint64(1<<20))

	tiny := hbs.Options{Limits: hbs.Limits{MaxOutputBytes: 32}}
	for name, tc := range map[string]struct {
		tpl string
		ctx map[string]any
	}{
		"%v of a large leaf":     {`{{prettyp-num-en x}}`, map[string]any{"x": map[string]any{"k": strings.Repeat("v", 8<<20)}}},
		"%v of a large key":      {`{{prettyp-num-en x}}`, map[string]any{"x": map[string]any{strings.Repeat("k", 8<<20): 1}}},
		"possessive of a large":  {`{{possessive x}}`, map[string]any{"x": strings.Repeat("n", 8<<20)}},
		"date error of a large":  {`{{date-beautify x}}`, map[string]any{"x": strings.Repeat("d", 8<<20)}},
		"partial with long name": {`{{> ` + strings.Repeat("z", 500_000) + `}}`, map[string]any{}},
	} {
		tp := mustParse(t, tc.tpl)
		got := allocDuring(func() { _, err = tp.Render(tc.ctx, tiny) })
		requireLimit(t, err, "produces more than 256 bytes")
		require.Less(t, got, uint64(1<<20), name)
	}

	// Collecting a large object's keys is charged before the keys are.
	keys := map[string]any{}
	for i := range 200_000 {
		keys[strconv.Itoa(i)] = 1
	}
	p = mustParse(t, `{{prettyp-num-en x}}`)
	alloc = allocDuring(func() {
		_, err = p.Render(map[string]any{"x": keys}, hbs.Options{Limits: hbs.Limits{MaxSteps: 1000}})
	})
	requireLimit(t, err, "maximum of 1000 steps")
	require.Less(t, alloc, uint64(1<<20))

	// Within the limits, the texts are unchanged and charged.
	m := &countMeter{}
	_, err = mustParse(t, `{{> `+strings.Repeat("z", 500_000)+`}}`).Render(map[string]any{}, hbs.Options{Meter: m})
	var he *hbs.Error
	require.ErrorAs(t, err, &he)
	require.Equal(t, hbs.KindRender, he.Kind)
	require.Greater(t, m.used, int64(900))
}

// TestDateErrorExact: the date helpers bound their error text at its exact
// length, so a message that fits after earlier output is produced as the
// reference produces it.
func TestDateErrorExact(t *testing.T) {
	ctx := map[string]any{"a": repeatAny(1.0, 100), "s": strings.Repeat("x", 1<<20), "d": strings.Repeat("q", 4<<20)}
	_, err := mustParse(t, `{{#each a}}{{and k=(to-str ../s)}}{{/each}}{{date-beautify d}}`).Render(ctx, hbs.Options{})
	var he *hbs.Error
	require.ErrorAs(t, err, &he)
	require.Equal(t, hbs.KindRender, he.Kind, he.Msg[:min(80, len(he.Msg))])
	_, perr := time.Parse("2006-01-02", strings.Repeat("q", 4<<20))
	require.Equal(t, "date-beautify: expecting date format YYYY-MM-DD, got: "+perr.Error(), he.Msg)
}

// TestEscapeRejectionCharged: a string that fits unescaped but not escaped
// is refused after its scan is charged.
func TestEscapeRejectionCharged(t *testing.T) {
	m := &countMeter{}
	_, err := mustParse(t, `{{s}}`).Render(mustCtx(t, `{"s": "`+strings.Repeat("&", 600_000)+`"}`), hbs.Options{Meter: m, Limits: hbs.Limits{MaxOutputBytes: 1 << 20}})
	requireLimit(t, err, "rendered output exceeds")
	require.GreaterOrEqual(t, m.used, int64(600_000/16))
}
