package hbs_test

import (
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luthersystems/svc/libhandlebars/hbs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParse(t testing.TB, src string) *hbs.Program {
	t.Helper()
	p, err := hbs.ParseForTest(src)
	require.NoError(t, err)
	return p
}

func mustCtx(t testing.TB, js string) hbs.Value {
	t.Helper()
	v, err := hbs.FromJSON([]byte(js))
	require.NoError(t, err)
	return v
}

func TestModeFixed(t *testing.T) {
	cases := []struct{ tpl, compat, fixed string }{
		{`{{to-str n}}|{{to-str f}}|{{to-str 3}}|{{to-str big}}`, "3.000000|1.500000|3|1000000000000000000000.000000", "3|1.5|3|1000000000000000000000"},
		{`{{mod 7 "x"}}|{{mod "x" 2}}|{{mod 7 3}}`, "NaN|0|1", "0|0|1"},
		{`{{round-to-nth x 2}}`, "123456792.00", "123456789.12"},
		{`{{#if zero includeZero=true}}z{{/if}}{{#unless zero includeZero=true}}u{{/unless}}`, "u", "z"},
		{`{{plus a=0.1 b=0.2 c=0.3}}|{{minus 1 c=0.3 a=0.1 b=0.2}}`, "0.6000000000000001|0.39999999999999997", "0.6000000000000001|0.39999999999999997"},
	}
	for _, c := range cases {
		p := mustParse(t, c.tpl)
		ctx := mustCtx(t, quirkCtx)
		got, err := p.Render(ctx, hbs.Options{Mode: hbs.ModeFixed})
		require.NoError(t, err, c.tpl)
		assert.Equal(t, c.fixed, got, c.tpl)
		if c.compat != "" {
			got, err = p.Render(ctx, hbs.Options{})
			require.NoError(t, err, c.tpl)
			assert.Equal(t, c.compat, got, c.tpl)
		}
	}

	// An integral context number is accepted where an int is expected.
	p := mustParse(t, `{{date-add-months date n}}`)
	got, err := p.Render(mustCtx(t, quirkCtx), hbs.Options{Mode: hbs.ModeFixed})
	require.NoError(t, err)
	assert.Equal(t, "2020-05-01", got)
	_, err = mustParse(t, `{{date-add-months date f}}`).Render(mustCtx(t, quirkCtx), hbs.Options{Mode: hbs.ModeFixed})
	require.EqualError(t, err, "Evaluation error: Helper date-add-months called with argument 1 with type float64 but it should be int\nCurrent node:\n\tPath{Original:'f', Pos:23}")
}

// TestToIntPinned: to-int on NaN, +-Inf and floats outside int64 range gives
// the amd64 result, math.MinInt64, on every CPU and in both modes.
func TestToIntPinned(t *testing.T) {
	if math.MaxInt != math.MaxInt64 {
		t.Skip("int is not 64 bits")
	}
	const minInt64 = "-9223372036854775808"
	ctx := mustCtx(t, `{"big": 1e19, "nbig": -1e19, "p63": 9223372036854775808,
		"m63": -9223372036854775808, "below": 9223372036854774784, "nbelow": -9223372036854774784,
		"n": 3.9, "neg": -3.9, "half": -0.5, "zero": 0, "f32": 7}`)
	cases := []struct{ tpl, want string }{
		{`{{to-int (div 0 0)}}`, minInt64},            // NaN
		{`{{to-int (mod 1 0)}}`, minInt64},            // NaN
		{`{{to-int (div 1 0)}}`, minInt64},            // +Inf
		{`{{to-int (div -1 0)}}`, minInt64},           // -Inf
		{`{{to-int (times -1 (div 1 0))}}`, minInt64}, // -Inf
		{`{{to-int big}}`, minInt64},                  // 1e19
		{`{{to-int nbig}}`, minInt64},                 // -1e19
		{`{{to-int p63}}`, minInt64},                  // 2^63
		{`{{to-int m63}}`, minInt64},                  // -2^63, in range
		{`{{to-int (times big big)}}`, minInt64},
		{`{{to-int below}}`, "9223372036854774784"},
		{`{{to-int nbelow}}`, "-9223372036854774784"},
		{`{{to-int n}}|{{to-int neg}}|{{to-int half}}|{{to-int zero}}`, "3|-3|0|0"},
		{`{{to-int (div 7 2)}}|{{to-int (times -2.5 2)}}|{{to-int 12}}`, "3|-5|12"},
		{`{{to-str (to-int (div 1 0))}}`, minInt64},
	}
	for _, mode := range []hbs.Mode{hbs.ModeCompat, hbs.ModeFixed} {
		for _, c := range cases {
			got, err := mustParse(t, c.tpl).Render(ctx, hbs.Options{Mode: mode})
			require.NoError(t, err, c.tpl)
			assert.Equal(t, c.want, got, "mode %v: %s", mode, c.tpl)
		}
	}
}

// countMeter counts charges and fails past a budget.
type countMeter struct {
	limit int64
	used  int64
	calls int
}

var errBudget = errors.New("step budget exceeded")

func (m *countMeter) Charge(n int64) error {
	m.calls++
	m.used += n
	if m.limit > 0 && m.used > m.limit {
		return errBudget
	}
	return nil
}

func TestMeter(t *testing.T) {
	ctx := mustCtx(t, quirkCtx)
	p := mustParse(t, `{{#each items}}{{name}}{{#if on}}!{{/if}}{{/each}}{{plus a=1}}`)
	var first int64
	for i := range 3 {
		m := &countMeter{}
		_, err := p.Render(ctx, hbs.Options{Meter: m})
		require.NoError(t, err)
		if i == 0 {
			first = m.used
			require.Positive(t, first)
		}
		require.Equal(t, first, m.used, "charges are deterministic")
	}

	// Output is charged per started KiB: 4 KiB + 1 byte costs 5 steps more
	// than nothing.
	small := &countMeter{}
	_, err := mustParse(t, `{{s}}`).Render(mustCtx(t, `{"s": ""}`), hbs.Options{Meter: small})
	require.NoError(t, err)
	big := &countMeter{}
	_, err = mustParse(t, `{{s}}`).Render(mustCtx(t, `{"s": "`+strings.Repeat("x", 4097)+`"}`), hbs.Options{Meter: big})
	require.NoError(t, err)
	require.Equal(t, small.used+5, big.used)

	// A budget error is returned unchanged and stops the render early.
	huge := mustParse(t, `{{#each a}}{{#each ../a}}xxxxxxxxxxxxxxxx{{/each}}{{/each}}`)
	arr := `{"a": [` + strings.Repeat(`1,`, 999) + `1]}`
	m := &countMeter{limit: 1000}
	_, err = huge.Render(mustCtx(t, arr), hbs.Options{Meter: m})
	require.ErrorIs(t, err, errBudget)
	require.Less(t, m.used, int64(1000+64+1))
}

func TestLimits(t *testing.T) {
	ctx := mustCtx(t, `{"s": "abcd", "q": "<"}`)
	p := mustParse(t, `{{s}}{{s}}`)
	got, err := p.Render(ctx, hbs.Options{Limits: hbs.Limits{MaxOutputBytes: 8}})
	require.NoError(t, err)
	require.Equal(t, "abcdabcd", got)
	_, err = p.Render(ctx, hbs.Options{Limits: hbs.Limits{MaxOutputBytes: 7}})
	var he *hbs.Error
	require.ErrorAs(t, err, &he)
	require.Equal(t, hbs.KindLimit, he.Kind)
	// Escaping counts.
	_, err = mustParse(t, `{{q}}`).Render(ctx, hbs.Options{Limits: hbs.Limits{MaxOutputBytes: 3}})
	require.ErrorAs(t, err, &he)
	require.Equal(t, hbs.KindLimit, he.Kind)

	// Deep static nesting: Parse refuses it at the default depth, and
	// Render enforces its own MaxDepth on a program parsed with a higher one.
	deep := strings.Repeat(`{{#if t}}`, 300) + "x" + strings.Repeat(`{{/if}}`, 300)
	_, err = hbs.Parse(deep, hbs.DefaultLimits())
	require.ErrorAs(t, err, &he)
	require.Equal(t, hbs.KindLimit, he.Kind)
	deepProg, err := hbs.Parse(deep, hbs.Limits{MaxDepth: 300})
	require.NoError(t, err)
	_, err = deepProg.Render(mustCtx(t, `{"t": true}`), hbs.Options{})
	require.ErrorAs(t, err, &he)
	require.Equal(t, hbs.KindLimit, he.Kind)
	got, err = deepProg.Render(mustCtx(t, `{"t": true}`), hbs.Options{Limits: hbs.Limits{MaxDepth: 300}})
	require.NoError(t, err)
	require.Equal(t, "x", got)

	// Helper recursion that overflows raymond's Go stack: a {{mustache}}
	// helper evaluates the enclosing block, which calls it again.
	for _, tpl := range []string{
		`{{#each items}}{{if true}}{{/each}}`,
		`{{#if t}}{{with t}}{{/if}}`,
		`{{#each items}}{{equal 1 1}}{{equal 1 1}}{{/each}}`,
		`{{#if t}}{{#with t}}{{equal 1 1}}{{/with}}{{/if}}`,
	} {
		_, err = mustParse(t, tpl).Render(mustCtx(t, `{"t": true, "items": [1]}`), hbs.Options{})
		require.ErrorAs(t, err, &he, tpl)
		require.Equal(t, hbs.KindLimit, he.Kind, tpl)
	}
}

// TestGlobalPerRender: the global helper's map belongs to one render (D7).
func TestGlobalPerRender(t *testing.T) {
	p := mustParse(t, `[{{global "n" key="k"}}]{{#if set}}{{global "n" key="k" val="v"}}{{/if}}[{{global "n" key="k"}}]`)
	got, err := p.Render(mustCtx(t, `{"set": true}`), hbs.Options{})
	require.NoError(t, err)
	require.Equal(t, "[][v]", got)
	got, err = p.Render(mustCtx(t, `{"set": false}`), hbs.Options{})
	require.NoError(t, err)
	require.Equal(t, "[][]", got)
}

// TestConcurrentRender shares one Program and one context between
// goroutines (run with -race).
func TestConcurrentRender(t *testing.T) {
	p := mustParse(t, `{{#each items}}{{name}}{{global "n" key="k" val=name}}{{global "n" key="k"}}{{/each}}{{plus a=n b=f}}`)
	ctx := mustCtx(t, quirkCtx)
	want, err := p.Render(ctx, hbs.Options{})
	require.NoError(t, err)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				got, err := p.Render(ctx, hbs.Options{})
				if err != nil {
					t.Error(err)
					return
				}
				assert.Equal(t, want, got)
			}
		})
	}
	wg.Wait()
}

func TestFromJSON(t *testing.T) {
	v, err := hbs.FromJSON([]byte(`null`))
	require.NoError(t, err)
	m, ok := v.(map[string]any)
	require.True(t, ok)
	require.Nil(t, m)
	got, err := mustParse(t, `a{{x}}{{this}}b`).Render(v, hbs.Options{})
	require.NoError(t, err)
	require.Equal(t, "aUNPRINTABLEb", got)

	_, err = hbs.FromJSON([]byte(`[1]`))
	require.EqualError(t, err, "json: cannot unmarshal array into Go value of type map[string]interface {}")
	_, err = hbs.FromJSON([]byte(`"s"`))
	require.EqualError(t, err, "json: cannot unmarshal string into Go value of type map[string]interface {}")

	// A nil context (no JSON) has no root at all.
	got, err = mustParse(t, `a{{x}}{{this}}{{@root}}b`).Render(nil, hbs.Options{})
	require.NoError(t, err)
	require.Equal(t, "ab", got)
}

// nestedEach is the D3 case: raymond takes about 30 s for n=3000.
func nestedEach(n int) (string, string) {
	ctx := `{"a": [` + strings.Repeat(`1,`, n-1) + `1]}`
	return `{{#each a}}{{#each ../a}}x{{/each}}{{/each}}`, ctx
}

func TestLinearNestedEach(t *testing.T) {
	tpl, ctxJSON := nestedEach(3000)
	p := mustParse(t, tpl)
	ctx := mustCtx(t, ctxJSON)
	start := time.Now()
	got, err := p.Render(ctx, hbs.Options{})
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.Len(t, got, 9_000_000)
	t.Logf("3000x3000 nested each: %v", elapsed)
	if !raceEnabled && !testing.Short() {
		require.Less(t, elapsed, time.Second)
	}
}
