// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/elps/lisp"
	"github.com/luthersystems/svc/libhandlebars"
	"github.com/luthersystems/svc/libhandlebars/hbs"
	"github.com/luthersystems/svc/libhandlebars/internal/bigcost"
	"github.com/stretchr/testify/require"
)

type MethVal struct{ N int }

func (v MethVal) String() string { return "V" + strconv.Itoa(v.N) }

type MethPtr struct{ N int }

func (p *MethPtr) String() string {
	if p == nil {
		return "nil-ptr"
	}
	return "P" + strconv.Itoa(p.N)
}

type MethPanic int

func (MethPanic) String() string { panic("boom") }

type MethErr struct{ Msg string }

func (e MethErr) Error() string { return "E:" + e.Msg }

type MethFmt int

func (f MethFmt) Format(s fmt.State, c rune) { _, _ = fmt.Fprintf(s, "F%c%d", c, int(f)) }

type MethHolder struct {
	D    time.Duration
	d    time.Duration
	V    MethVal
	v    MethVal
	P    MethPtr
	PP   *MethPtr
	VP   *MethVal
	I    any
	i    any
	E    error
	F    MethFmt
	N    json.Number
	Big  *big.Int
	BigV big.Int
	Flt  *big.Float
	Rat  *big.Rat
	T    time.Time
	Vals []MethVal
	Keys map[MethVal]MethErr
}

// TestGoContextMethodValues: values with String, Error and Format methods
// print as raymond's fmt printed them, in every position fmt calls the
// method and every one it cannot (unexported fields, pointer receivers
// on values, nil pointers with value receivers, panics).
func TestGoContextMethodValues(t *testing.T) {
	h := MethHolder{
		D: time.Second, d: time.Second, V: MethVal{1}, v: MethVal{2}, P: MethPtr{3},
		PP: &MethPtr{4}, I: MethVal{5}, i: MethVal{6}, E: MethErr{"e"}, F: 7, N: "8.5",
		Big: big.NewInt(9), BigV: *big.NewInt(10), Flt: big.NewFloat(1.25),
		Rat: big.NewRat(1, 3), T: time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC),
		Vals: []MethVal{{11}, {12}}, Keys: map[MethVal]MethErr{{2}: {"b"}, {1}: {"a"}},
	}
	hp := h
	hp.PP, hp.I, hp.Big, hp.Flt, hp.Rat, hp.E = nil, MethPanic(0), nil, nil, nil, nil
	for _, x := range []any{
		h, &h, hp, []MethHolder{h, hp}, []any{h, hp},
		[]time.Duration{1, time.Minute}, []json.Number{"1", "2.5"},
		[]any{MethVal{1}, &MethPtr{2}, (*MethPtr)(nil), (*MethVal)(nil), MethPanic(1), MethFmt(3), nil, errors.New("x")},
		map[string]any{"a": MethVal{1}, "b": []any{MethErr{"z"}, 2}},
		map[MethVal]int{{3}: 1, {1}: 2}, map[any]int{MethVal{1}: 1, "s": 2},
		[]*big.Int{big.NewInt(1), nil}, []*big.Float{big.NewFloat(2.5)}, [2]MethFmt{1, 2},
		struct{ A []error }{[]error{nil, MethErr{"y"}}},
	} {
		checkGo(t, `{{prettyp-num-en x}}`, map[string]any{"x": x})
	}
}

// TestGoContextMethodValuesBounded: a value holding many method-bearing
// values (here one 1 MiB json.Number, 200 times) is printed a method
// result at a time, each charged by its length and checked against the
// produced-bytes bound before the next: the render fails at the bound
// having built about that much, not the whole text (1.3 GB before).
func TestGoContextMethodValuesBounded(t *testing.T) {
	num := json.Number(strings.Repeat("1", 1<<20))
	xs := make([]json.Number, 200)
	for i := range xs {
		xs[i] = num
	}
	loc := time.FixedZone(strings.Repeat("Z", 1<<20), 0)
	ts := make([]time.Time, 200)
	for i := range ts {
		ts[i] = time.Unix(0, 0).In(loc)
	}
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	lim := hbs.DefaultLimits()
	lim.MaxSteps = 1 << 40
	bound := int64(8 * lim.MaxOutputBytes) // the produced-bytes bound
	for _, x := range []any{xs, ts} {
		m := &countMeter{}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, err = tpl.Render(map[string]any{"x": x}, hbs.Options{Limits: lim, Meter: m})
		runtime.ReadMemStats(&after)
		require.ErrorContains(t, err, "produces more than", "%T", x)
		// Each method result is charged by its length, before the next.
		require.Greater(t, m.n, bound/32, "%T", x)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(6*bound), "%T", x) //nolint:gosec // bound is positive
	}
}

// TestGoContextShortMethodsNotOversized: a method's result is counted at
// its length, not a fixed 64 bytes, so many short ones within the bound
// print (here into the prettyp-num-en error).
func TestGoContextShortMethodsNotOversized(t *testing.T) {
	ds := make([]time.Duration, 200)
	for i := range ds {
		ds[i] = time.Second
	}
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	lim := hbs.DefaultLimits()
	lim.MaxOutputBytes = 1000
	_, err = tpl.Render(map[string]any{"x": ds}, hbs.Options{Limits: lim})
	require.EqualError(t, err, "value passed in must be a number, got: "+fmt.Sprint(ds))
}

// bigValues are math/big values whose decimal text takes milliseconds:
// superlinear in an Int's bits, quadratic in a Float's fraction bits.
func bigValues() map[string]any {
	xi := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 1<<18), big.NewInt(3))
	return map[string]any{
		"int":       xi,
		"rat":       new(big.Rat).SetFrac(xi, new(big.Int).Add(xi, big.NewInt(2))),
		"float-neg": new(big.Float).SetMantExp(big.NewFloat(1.5), -(1 << 14)),
		"float-pos": new(big.Float).SetMantExp(big.NewFloat(1.5), 1<<18),
		"float-pre": new(big.Float).SetPrec(1 << 14).SetFloat64(1.5),
	}
}

// TestGoContextBigValuesCharged: math/big values are charged their
// decimal conversion's work before fmt runs it, at the cost model's rate.
func TestGoContextBigValuesCharged(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	lim := hbs.DefaultLimits()
	lim.MaxSteps = 1 << 40
	for name, x := range bigValues() {
		want, ok := bigcost.Steps(x)
		require.True(t, ok, name)
		best := 0.0
		for range 3 {
			m := &countMeter{}
			start := time.Now()
			_, err = tpl.Render(map[string]any{"x": []any{x}}, hbs.Options{Limits: lim, Meter: m})
			d := time.Since(start)
			require.ErrorContains(t, err, "value passed in must be a number", name)
			require.GreaterOrEqual(t, m.n, want, name)
			if ns := float64(d.Nanoseconds()) / float64(m.n); best == 0 || ns < best {
				best = ns
			}
		}
		if !raceEnabled {
			require.False(t, ceilingFails(t, best), "%s: %.0f ns/step", name, best)
		}
	}
}

// TestBigValuesFailFast: a Float whose shortest form would take hours
// (1.5·2^-(2^30)) fails the step limit before it is formatted, in the Go
// context and through JSON (its MarshalText), and so do a Float of high
// precision and an Int too long for MaxSteps. (A top-level one the Go
// context follows to its struct, which fmt prints by reflection.)
func TestBigValuesFailFast(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}} {{x}}`)
	require.NoError(t, err)
	huge := new(big.Float).SetMantExp(big.NewFloat(1.5), -(1 << 30))
	run := func(x any, opt libhandlebars.RenderOption) {
		t.Helper()
		start := time.Now()
		_, err := libhandlebars.RenderWith(tpl, map[string]any{"x": x}, opt)
		require.ErrorContains(t, err, "maximum of", "%#v", x)
		require.Less(t, time.Since(start), 5*time.Second, "%#v", x)
	}
	for _, x := range []any{
		[]any{huge}, []*big.Float{new(big.Float).SetPrec(1 << 20).SetFloat64(1.5)},
		[]*big.Int{new(big.Int).Lsh(big.NewInt(1), 1<<26)}, struct{ F *big.Float }{huge},
	} {
		run(x, libhandlebars.WithGoContext())
		run(x, libhandlebars.WithJSONContext())
	}
	run(huge, libhandlebars.WithJSONContext())
	// Addressable, a big.Int encodes by its pointer's MarshalJSON.
	run(&struct{ I big.Int }{*new(big.Int).Lsh(big.NewInt(1), 1<<26)}, libhandlebars.WithJSONContext())
}

// TestGoContextBigValueNotWalked: a big.Int held by value has no methods
// (they are the pointer's): fmt prints it by reflection, as raymond did,
// and it is not charged as a conversion.
func TestGoContextBigValueNotWalked(t *testing.T) {
	checkGo(t, `{{prettyp-num-en x}}`, map[string]any{"x": []big.Int{*big.NewInt(5)}})
	_, ok := bigcost.Steps(*big.NewInt(5))
	require.False(t, ok)
	require.Equal(t, reflect.Struct, reflect.TypeFor[big.Int]().Kind())
}

// TestGoContextMethodValuesExactLength: a method value's text counts at
// its real length, never an estimate: a short one within a small output
// bound prints, and nil pointers whose methods fmt cannot call print
// "<nil>", as raymond's fmt did.
func TestGoContextMethodValuesExactLength(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en v}}`)
	require.NoError(t, err)
	lim := hbs.DefaultLimits()
	lim.MaxOutputBytes = 8
	_, err = tpl.Render(map[string]any{"v": []time.Duration{0}}, hbs.Options{Limits: lim})
	require.EqualError(t, err, "value passed in must be a number, got: [0s]")
	start := time.Now()
	checkGo(t, `{{prettyp-num-en v}}`, map[string]any{"v": make([]*time.Time, 2_100_000)})
	t.Logf("2.1M nil *time.Time: %v", time.Since(start))
	checkGo(t, `{{prettyp-num-en v}}`, map[string]any{"v": []*MethVal{nil}, "w": []*MethPtr{nil}})
	checkGo(t, `{{prettyp-num-en w}}`, map[string]any{"w": []*MethPtr{nil}})
	checkGo(t, `{{prettyp-num-en w}}`, map[string]any{"w": []*MethErr{nil}})
	checkGo(t, `{{prettyp-num-en w}}`, map[string]any{"w": []*MethFmt{nil}})
}

type BigWrap struct{ *big.Float }

type bigInner struct{ *big.Float }

type BigOuter struct{ bigInner }

type BigByValue struct{ big.Float }

type BigKey struct{ *big.Float }

// TestBigValuesEmbeddedFailFast: math/big methods promoted through
// embedding (exported or not, by pointer, or by value behind a pointer)
// and math/big map keys under JSON (their MarshalText runs twice) are
// charged before they run: a Float that would take hours fails the step
// limit at once.
func TestBigValuesEmbeddedFailFast(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}} {{x}}`)
	require.NoError(t, err)
	huge := func() *big.Float { return new(big.Float).SetMantExp(big.NewFloat(1.5), -(1 << 30)) }
	run := func(x any, opt libhandlebars.RenderOption) {
		t.Helper()
		start := time.Now()
		_, err := libhandlebars.RenderWith(tpl, map[string]any{"x": x}, opt)
		require.ErrorContains(t, err, "maximum of", "%#v", x)
		require.Less(t, time.Since(start), 5*time.Second, "%#v", x)
	}
	byVal := &BigByValue{}
	byVal.Set(huge())
	for _, x := range []any{
		[]any{BigWrap{huge()}}, []any{BigOuter{bigInner{huge()}}}, []any{&BigOuter{bigInner{huge()}}},
		[]*BigByValue{byVal}, map[string]any{"k": BigWrap{huge()}},
	} {
		run(x, libhandlebars.WithGoContext())
		run(x, libhandlebars.WithJSONContext())
	}
	run(BigWrap{huge()}, libhandlebars.WithJSONContext())
	run(map[*big.Float]int{huge(): 1}, libhandlebars.WithJSONContext())
	run(map[BigKey]int{{huge()}: 1}, libhandlebars.WithJSONContext())
	run(map[*big.Int]int{new(big.Int).Lsh(big.NewInt(1), 1<<26): 1}, libhandlebars.WithJSONContext())
	// Small ones print as before.
	checkGo(t, `{{prettyp-num-en x}}`, map[string]any{"x": []any{BigWrap{big.NewFloat(2.5)}, BigOuter{bigInner{big.NewFloat(3)}}, BigWrap{}}})
	checkJSON(t, `{{x}}`, map[string]any{"x": map[*big.Int]int{big.NewInt(7): 1}})
}

type nanKey struct {
	F float64
	S string
}

// TestGoContextNaNKeysOrdered: map keys holding NaNs that fmt's sort still
// orders (they differ in another field) print as raymond printed them;
// keys it ties (plain float NaNs) remain an error.
func TestGoContextNaNKeysOrdered(t *testing.T) {
	nan := math.NaN()
	checkGo(t, `{{prettyp-num-en x}}`, map[string]any{"x": map[nanKey]int{{nan, "a"}: 1, {nan, "b"}: 2, {1, "c"}: 3}})
	checkGo(t, `{{prettyp-num-en x}}`, map[string]any{"x": map[[2]float64]int{{nan, 1}: 1, {nan, 2}: 2}})
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	for _, x := range []any{map[float64]int{nan: 1, math.NaN(): 2}, map[nanKey]int{{nan, "a"}: 1, {math.NaN(), "a"}: 2}} {
		_, err = libhandlebars.Render(tpl, map[string]any{"x": x})
		require.EqualError(t, err, "Go map with more than one NaN key has no deterministic text")
	}
}

type MineAll struct{}

func (MineAll) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "mineF") }
func (MineAll) Error() string              { return "mineE" }
func (MineAll) String() string             { return "mineS" }

type ShadowBig struct {
	MineAll
	*big.Float
}

type DeepBig struct {
	MineAll
	BigWrap
}

type OwnAll struct{ *big.Float }

func (OwnAll) Format(s fmt.State, _ rune)   { _, _ = fmt.Fprint(s, "ownF") }
func (OwnAll) String() string               { return "ownS" }
func (OwnAll) MarshalText() ([]byte, error) { return []byte("ownT"), nil }

type FloatRat struct {
	*big.Float
	*big.Rat
}

type IntRat struct {
	*big.Int
	*big.Rat
}

type FloatInt struct {
	*big.Float
	*big.Int
}

type EmbFormatter struct{ fmt.Formatter }

type EmbStringer struct{ fmt.Stringer }

type EmbText struct{ encoding.TextMarshaler }

type IfaceField struct{ T encoding.TextMarshaler }

// TestBigMethodResolved: the method a call reaches is found by Go's
// selector rules, per method name: math/big's (promoted through any
// embedding, an embedded interface, or behind an interface type) is
// charged and fails fast when huge; a type's own method, or one that
// shadows math/big's, prints as raymond printed it.
func TestBigMethodResolved(t *testing.T) {
	huge := new(big.Float).SetMantExp(big.NewFloat(1.5), -(1 << 30))
	hugeInt := new(big.Int).Lsh(big.NewInt(1), 1<<26)
	var tm encoding.TextMarshaler = huge
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}} {{x}}`)
	require.NoError(t, err)
	run := func(x any, opt libhandlebars.RenderOption) {
		t.Helper()
		start := time.Now()
		_, err := libhandlebars.RenderWith(tpl, map[string]any{"x": x}, opt)
		require.ErrorContains(t, err, "maximum of", "%#v", x)
		require.Less(t, time.Since(start), 5*time.Second, "%#v", x)
	}
	goMode, jsonMode := libhandlebars.WithGoContext(), libhandlebars.WithJSONContext()
	for _, x := range []any{
		[]any{EmbFormatter{huge}}, []any{EmbStringer{huge}}, []any{FloatRat{huge, big.NewRat(1, 3)}},
		[]any{IntRat{hugeInt, big.NewRat(1, 3)}},
	} {
		run(x, goMode)
	}
	for _, x := range []any{
		IfaceField{tm}, []encoding.TextMarshaler{tm}, map[encoding.TextMarshaler]int{huge: 1},
		EmbText{huge}, []any{EmbText{huge}}, map[EmbText]int{{huge}: 1},
		FloatInt{huge, hugeInt}, []any{IntRat{hugeInt, big.NewRat(1, 3)}},
	} {
		run(x, jsonMode)
	}
	for _, x := range []any{ShadowBig{MineAll{}, huge}, DeepBig{MineAll{}, BigWrap{huge}}, OwnAll{huge}} {
		checkGo(t, `{{prettyp-num-en x}}`, map[string]any{"x": []any{x}})
	}
	checkJSON(t, `{{x}}`, map[string]any{"x": OwnAll{huge}})
}

// TestBigMethodNativesFailFast: an ELPS native holding math/big behind
// embedding, an interface or a map key is charged before the encoder
// runs its methods: with a 2^26 step budget the render fails at once.
func TestBigMethodNativesFailFast(t *testing.T) {
	huge := new(big.Float).SetMantExp(big.NewFloat(1.5), -(1 << 30))
	var tm encoding.TextMarshaler = huge
	for _, x := range []any{
		EmbText{huge}, IfaceField{tm}, map[*big.Float]int{huge: 1}, map[encoding.TextMarshaler]int{huge: 1},
		BigWrap{huge}, FloatInt{huge, new(big.Int).Lsh(big.NewInt(1), 1<<26)}, new(big.Int).Lsh(big.NewInt(1), 1<<26),
	} {
		env := newEnv(t)
		env.Runtime.SetStepBudget(1 << 26)
		ctx := lisp.SortedMap()
		ctx.MapSetString("x", lisp.Native(x))
		env.Put(lisp.Symbol("ctx"), ctx)
		start := time.Now()
		res := env.LoadStringContext(t.Context(), "test", `(handlebars:render "{{x}}" ctx)`)
		require.Equal(t, lisp.LError, res.Type, "%T: %v", x, res)
		require.Less(t, time.Since(start), 5*time.Second, "%T", x)
	}
}

type Deep1 struct{ Deep2 }
type Deep2 struct{ Deep3 }
type Deep3 struct{ Deep4 }
type Deep4 struct{ Deep5 }
type Deep5 struct{ Deep6 }
type Deep6 struct{ Deep7 }
type Deep7 struct{ Deep8 }
type Deep8 struct{ Deep9 }
type Deep9 struct{ Deep10 }
type Deep10 struct{ Deep11 }
type Deep11 struct{ Deep12 }
type Deep12 struct{ Deep13 }
type Deep13 struct{ Deep14 }
type Deep14 struct{ Deep15 }
type Deep15 struct{ Deep16 }
type Deep16 struct{ Deep17 }
type Deep17 struct{ Deep18 }
type Deep18 struct{ Deep19 }
type Deep19 struct{ Deep20 }
type Deep20 struct{ Deep21 }
type Deep21 struct{ Deep22 }
type Deep22 struct{ Deep23 }
type Deep23 struct{ Deep24 }
type Deep24 struct{ Deep25 }
type Deep25 struct{ Deep26 }
type Deep26 struct{ Deep27 }
type Deep27 struct{ Deep28 }
type Deep28 struct{ Deep29 }
type Deep29 struct{ Deep30 }
type Deep30 struct{ Deep31 }
type Deep31 struct{ Deep32 }
type Deep32 struct{ Deep33 }
type Deep33 struct{ Deep34 }
type Deep34 struct{ Deep35 }
type Deep35 struct{ Deep36 }
type Deep36 struct{ Deep37 }
type Deep37 struct{ Deep38 }
type Deep38 struct{ Deep39 }
type Deep39 struct{ Deep40 }
type Deep40 struct{ Deep41 }
type Deep41 struct{ Deep42 }
type Deep42 struct{ Deep43 }
type Deep43 struct{ Deep44 }
type Deep44 struct{ Deep45 }
type Deep45 struct{ Deep46 }
type Deep46 struct{ Deep47 }
type Deep47 struct{ Deep48 }
type Deep48 struct{ Deep49 }
type Deep49 struct{ Deep50 }
type Deep50 struct{ Deep51 }
type Deep51 struct{ Deep52 }
type Deep52 struct{ Deep53 }
type Deep53 struct{ Deep54 }
type Deep54 struct{ Deep55 }
type Deep55 struct{ Deep56 }
type Deep56 struct{ Deep57 }
type Deep57 struct{ Deep58 }
type Deep58 struct{ Deep59 }
type Deep59 struct{ Deep60 }
type Deep60 struct{ Deep61 }
type Deep61 struct{ Deep62 }
type Deep62 struct{ Deep63 }
type Deep63 struct{ Deep64 }
type Deep64 struct{ Deep65 }

type Deep65 struct{ *big.Float }

// TestBigMethodSearchFailsClosed: a math/big method promoted from deeper
// than the supplier search's 64 levels is an error, not an uncharged call,
// in the Go context, through JSON, and in an ELPS native; at 64 levels it
// is found.
func TestBigMethodSearchFailsClosed(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}} {{x}}`)
	require.NoError(t, err)
	deep := Deep1{} // its Float, 65 levels down, is nil: the search fails on the type
	_, err = libhandlebars.RenderWith(tpl, map[string]any{"x": []any{deep}}, libhandlebars.WithGoContext())
	require.EqualError(t, err, "Go value's method embedding search passed its bound of 64 levels or 16384 embedded fields")
	_, err = libhandlebars.RenderWith(tpl, map[string]any{"x": deep}, libhandlebars.WithJSONContext())
	require.ErrorContains(t, err, "json: method embedding search passed its bound of 64 levels")
	env := newEnv(t)
	ctx := lisp.SortedMap()
	ctx.MapSetString("x", lisp.Native(deep))
	env.Put(lisp.Symbol("ctx"), ctx)
	res := env.LoadStringContext(t.Context(), "test", `(handlebars:render "{{x}}" ctx)`)
	require.Equal(t, lisp.LError, res.Type, "%v", res)
	require.Contains(t, res.String(), "method embedding search passed its bound of 64 levels")
	// One level shallower, it is found (and, nil, prints as fmt prints it).
	checkGo(t, `{{prettyp-num-en x}}`, map[string]any{"x": []any{deep.Deep2}})
}

type StrChain struct{ fmt.Stringer }

// TestBigMethodInterfaceChains: a method promoted through any number of
// embedded interfaces is followed (a step a hop): 70 deep around a small
// Float prints as raymond printed it, around a huge one fails fast. A
// chain back to itself, whose call would overflow the stack, is an error.
func TestBigMethodInterfaceChains(t *testing.T) {
	chain := func(f *big.Float, n int) fmt.Stringer {
		var s fmt.Stringer = f
		for range n {
			s = StrChain{s}
		}
		return s
	}
	checkGo(t, `{{prettyp-num-en x}}`, map[string]any{"x": []any{chain(big.NewFloat(2.5), 70)}})
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	start := time.Now()
	_, err = libhandlebars.RenderWith(tpl, map[string]any{"x": []any{chain(new(big.Float).SetMantExp(big.NewFloat(1.5), -(1<<30)), 70)}}, libhandlebars.WithGoContext())
	require.ErrorContains(t, err, "maximum of")
	require.Less(t, time.Since(start), 5*time.Second)

	loop := &StrChain{}
	loop.Stringer = StrChain{loop}
	_, err = libhandlebars.RenderWith(tpl, map[string]any{"x": []any{loop}}, libhandlebars.WithGoContext())
	require.EqualError(t, err, "Go value's method embedding cycles through an interface")
}

type E3Top struct {
	E3C1
	E3X1
}

type E3C1 struct{ E3C2 }
type E3C2 struct{ E3C3 }
type E3C3 struct{ E3C4 }
type E3C4 struct{ E3C5 }
type E3C5 struct{ E3C6 }
type E3C6 struct{ E3C7 }
type E3C7 struct{ E3C8 }
type E3C8 struct{ E3C9 }
type E3C9 struct{ E3C10 }
type E3C10 struct{ E3C11 }
type E3C11 struct{ E3C12 }
type E3C12 struct{ E3C13 }
type E3C13 struct{ E3C14 }
type E3C14 struct{ E3C15 }
type E3C15 struct{ E3C16 }
type E3C16 struct{ *big.Int }

type E3X1 struct {
	E3X2
	E3Y2
}

type E3Y1 struct {
	E3X2
	E3Y2
}

type E3X2 struct {
	E3X3
	E3Y3
}

type E3Y2 struct {
	E3X3
	E3Y3
}

type E3X3 struct {
	E3X4
	E3Y4
}

type E3Y3 struct {
	E3X4
	E3Y4
}

type E3X4 struct {
	E3X5
	E3Y5
}

type E3Y4 struct {
	E3X5
	E3Y5
}

type E3X5 struct {
	E3X6
	E3Y6
}

type E3Y5 struct {
	E3X6
	E3Y6
}

type E3X6 struct {
	E3X7
	E3Y7
}

type E3Y6 struct {
	E3X7
	E3Y7
}

type E3X7 struct {
	E3X8
	E3Y8
}

type E3Y7 struct {
	E3X8
	E3Y8
}

type E3X8 struct {
	E3X9
	E3Y9
}

type E3Y8 struct {
	E3X9
	E3Y9
}

type E3X9 struct {
	E3X10
	E3Y10
}

type E3Y9 struct {
	E3X10
	E3Y10
}

type E3X10 struct {
	E3X11
	E3Y11
}

type E3Y10 struct {
	E3X11
	E3Y11
}

type E3X11 struct {
	E3X12
	E3Y12
}

type E3Y11 struct {
	E3X12
	E3Y12
}

type E3X12 struct {
	E3X13
	E3Y13
}

type E3Y12 struct {
	E3X13
	E3Y13
}

type E3X13 struct {
	E3X14
	E3Y14
}

type E3Y13 struct {
	E3X14
	E3Y14
}

type E3X14 struct {
	E3X15
	E3Y15
}

type E3Y14 struct {
	E3X15
	E3Y15
}

type E3X15 struct{}
type E3Y15 struct{}

// TestBigMethodSearchDiamonds: the supplier search looks at each embedded
// type once (counting the paths to it), so a diamond of embeddings, here
// 2^14 paths through 30 types, costs its types, not its paths: the Int 17
// levels down is found, as Go finds it.
func TestBigMethodSearchDiamonds(t *testing.T) {
	var x E3Top
	x.Int = big.NewInt(42)
	checkGo(t, `{{prettyp-num-en x}}`, map[string]any{"x": []any{x}})
	checkJSON(t, `{{x}}`, map[string]any{"x": x})
}

type TMLoop struct{ encoding.TextMarshaler }

// TestBigMethodErrorsOrderFree: of several map keys whose math/big search
// fails (or a step limit), the error reported does not depend on Go's map
// order: the step limit where the walk stops, else the least error text.
func TestBigMethodErrorsOrderFree(t *testing.T) {
	strLoop := &StrChain{}
	strLoop.Stringer = StrChain{strLoop}
	tmLoop := &TMLoop{}
	tmLoop.TextMarshaler = tmLoop
	huge := new(big.Float).SetMantExp(big.NewFloat(1.5), -(1 << 30))
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}} {{x}}`)
	require.NoError(t, err)
	for _, c := range []struct {
		x   any
		opt libhandlebars.RenderOption
	}{
		{[]any{map[fmt.Stringer]int{strLoop: 0, huge: 1}}, libhandlebars.WithGoContext()},
		{[]any{map[fmt.Stringer]int{strLoop: 0, Deep1{}: 1}}, libhandlebars.WithGoContext()},
		{map[encoding.TextMarshaler]int{tmLoop: 0, &Deep1{}: 1}, libhandlebars.WithJSONContext()},
	} {
		seen := map[string]bool{}
		for range 200 {
			_, err := libhandlebars.RenderWith(tpl, map[string]any{"x": c.x}, c.opt)
			require.Error(t, err)
			seen[err.Error()] = true
		}
		require.Len(t, seen, 1, "%v", seen)
	}
}
