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
	"github.com/luthersystems/elps/lisp/lisplib/libjson"
	"github.com/luthersystems/svc/libhandlebars"
	"github.com/luthersystems/svc/libhandlebars/hbs"
	"github.com/luthersystems/svc/libhandlebars/internal/bigcost"
	"github.com/luthersystems/svc/libhandlebars/internal/hbref"
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

// TestReserveExact: a helper's result and an evaluation error are checked
// against the produced-bytes bound at their exact length: within a small
// bound they render as raymond rendered them.
func TestReserveExact(t *testing.T) {
	for _, c := range []struct {
		tpl, ctx string
		maxOut   int
	}{
		{`{{#if (to-str x)}}{{/if}}{{#if (possessive "s")}}{{/if}}`, `{"x": "xxxxxx"}`, 1},
		{`{{eq}}`, `{}`, 19},
	} {
		want, werr := hbref.RenderJSON(c.tpl, []byte(c.ctx))
		tpl, err := libhandlebars.Parse(c.tpl)
		require.NoError(t, err)
		v, err := hbs.FromJSON([]byte(c.ctx))
		require.NoError(t, err)
		lim := hbs.DefaultLimits()
		lim.MaxOutputBytes = c.maxOut
		got, gerr := tpl.Render(v, hbs.Options{Mode: hbs.ModeCompat, Limits: lim})
		if werr == nil {
			require.NoError(t, gerr, c.tpl)
			require.Equal(t, want, got, c.tpl)
			continue
		}
		require.Error(t, gerr, c.tpl)
		require.Equal(t, refRaw(werr), gotRaw(gerr), c.tpl)
	}
}

// TestNativeNumberNotMarshalledPastCap: a json.Number libjson's load check
// accepts (in float64 range) does not make a native past the allocation
// cap worth marshalling: the render fails at the cap without building the
// native's 256 MiB. An out-of-range one still does, and its load error
// comes first, as json:dump-bytes reports it.
func TestNativeNumberNotMarshalledPastCap(t *testing.T) {
	s := strings.Repeat("x", 1<<20)
	a := make([]string, 256)
	for i := range a {
		a[i] = s
	}
	run := func(num json.Number) (*lisp.LVal, uint64) {
		env := newEnv(t)
		env.Runtime.MaxAlloc = 65536
		env.Runtime.SetStepBudget(1 << 26)
		ctx := lisp.SortedMap()
		ctx.MapSetString("ctx", lisp.Native(map[string]any{"a": num, "b": a}))
		env.Put(lisp.Symbol("ctx"), ctx)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		res := env.LoadStringContext(t.Context(), "test", `(handlebars:render "" ctx)`)
		runtime.ReadMemStats(&after)
		return res, after.TotalAlloc - before.TotalAlloc
	}
	res, alloc := run("1")
	require.Equal(t, lisp.LError, res.Type, "%v", res)
	require.Contains(t, res.String(), "allocation size exceeds maximum (65536)")
	require.Less(t, alloc, uint64(64<<20))
	res, alloc = run("1e400")
	require.Equal(t, lisp.LError, res.Type, "%v", res)
	require.Contains(t, res.String(), "error while serializing: unable to encode native value: json: cannot unmarshal number 1e400 into Go value of type float64")
	require.Less(t, alloc, uint64(64<<20)) // known from the walk: not marshalled
}

type failText struct{}

func (failText) MarshalText() ([]byte, error) { return nil, errors.New("boom") }

type okText struct{}

func (okText) MarshalText() ([]byte, error) { return []byte("ok"), nil }

type quotedNum struct {
	N json.Number `json:",string"`
}

// TestNativeNumberLoadErrorParity: where a native's only bytes libjson's
// load check could refuse are json.Numbers out of float64 range, the walk
// reports the error without marshalling it, exactly as json:dump-bytes
// does: the first such number in the encoder's order, after an allocation
// error the bytes before it meet first.
func TestNativeNumberLoadErrorParity(t *testing.T) {
	big := strings.Repeat("y", 1<<17)
	for _, c := range []struct {
		before   string
		native   any
		maxAlloc int
	}{
		{"", map[string]any{"a": failText{}, "b": json.Number("1e400")}, 0},
		{"", map[string]any{"b": failText{}, "a": json.Number("1e400")}, 0},
		{"", map[string]any{"a": failText{}, "b": make([]byte, 2048)}, 1024},
		{"", map[string]any{"b": failText{}, "a": make([]byte, 2048)}, 1024},
		{"", map[string]any{"a": okText{}, "b": make([]byte, 2048)}, 1024},
		{big, map[string]any{"a": failText{}, "b": json.Number("1e400")}, 0},
		{"", map[string]any{"a": json.Number("1e400")}, 0},
		{"", map[string]any{"b": json.Number("-2e999"), "a": []any{1, json.Number("1e400")}}, 0},
		{"", struct{ X, Y json.Number }{"3", "4e500"}, 0},
		{"", []any{quotedNum{"1e400"}, json.Number("5e600")}, 0},
		{"", []any{quotedNum{"1e400"}}, 0},
		{big, map[string]any{"a": json.Number("1e400")}, 0},
	} {
		build := func(env *lisp.LEnv) *lisp.LVal {
			ctx := lisp.SortedMap()
			if c.before != "" {
				ctx.MapSetString("a", lisp.String(c.before))
			}
			ctx.MapSetString("z", lisp.Native(c.native))
			return ctx
		}
		maxAlloc := c.maxAlloc
		if maxAlloc == 0 {
			maxAlloc = 65536
		}
		env := newEnv(t)
		env.Runtime.MaxAlloc = maxAlloc
		want := libjson.DefaultSerializer().DumpBytesBuiltin(env, lisp.SExpr([]*lisp.LVal{build(env), lisp.Bool(false)}))
		env = newEnv(t)
		env.Runtime.MaxAlloc = maxAlloc
		env.Put(lisp.Symbol("ctx"), build(env))
		got := env.LoadStringContext(t.Context(), "test", `(handlebars:render "" ctx)`)
		if want.Type != lisp.LError {
			require.NotEqual(t, lisp.LError, got.Type, "%#v: %v", c.native, got)
			continue
		}
		require.Equal(t, lisp.LError, got.Type, "%#v", c.native)
		wantMsg := want.Cells[0].Str
		if !strings.HasPrefix(wantMsg, "allocation size exceeds maximum") {
			wantMsg = "error while serializing: " + wantMsg
		}
		require.Contains(t, got.String(), wantMsg, "%#v", c.native)
	}
}

// WideBig embeds a *big.Float after 1024 zero-sized plain fields: only
// embedded fields count toward the search's bound.
type WideBig struct {
	F0    [0]int
	F1    [0]int
	F2    [0]int
	F3    [0]int
	F4    [0]int
	F5    [0]int
	F6    [0]int
	F7    [0]int
	F8    [0]int
	F9    [0]int
	F10   [0]int
	F11   [0]int
	F12   [0]int
	F13   [0]int
	F14   [0]int
	F15   [0]int
	F16   [0]int
	F17   [0]int
	F18   [0]int
	F19   [0]int
	F20   [0]int
	F21   [0]int
	F22   [0]int
	F23   [0]int
	F24   [0]int
	F25   [0]int
	F26   [0]int
	F27   [0]int
	F28   [0]int
	F29   [0]int
	F30   [0]int
	F31   [0]int
	F32   [0]int
	F33   [0]int
	F34   [0]int
	F35   [0]int
	F36   [0]int
	F37   [0]int
	F38   [0]int
	F39   [0]int
	F40   [0]int
	F41   [0]int
	F42   [0]int
	F43   [0]int
	F44   [0]int
	F45   [0]int
	F46   [0]int
	F47   [0]int
	F48   [0]int
	F49   [0]int
	F50   [0]int
	F51   [0]int
	F52   [0]int
	F53   [0]int
	F54   [0]int
	F55   [0]int
	F56   [0]int
	F57   [0]int
	F58   [0]int
	F59   [0]int
	F60   [0]int
	F61   [0]int
	F62   [0]int
	F63   [0]int
	F64   [0]int
	F65   [0]int
	F66   [0]int
	F67   [0]int
	F68   [0]int
	F69   [0]int
	F70   [0]int
	F71   [0]int
	F72   [0]int
	F73   [0]int
	F74   [0]int
	F75   [0]int
	F76   [0]int
	F77   [0]int
	F78   [0]int
	F79   [0]int
	F80   [0]int
	F81   [0]int
	F82   [0]int
	F83   [0]int
	F84   [0]int
	F85   [0]int
	F86   [0]int
	F87   [0]int
	F88   [0]int
	F89   [0]int
	F90   [0]int
	F91   [0]int
	F92   [0]int
	F93   [0]int
	F94   [0]int
	F95   [0]int
	F96   [0]int
	F97   [0]int
	F98   [0]int
	F99   [0]int
	F100  [0]int
	F101  [0]int
	F102  [0]int
	F103  [0]int
	F104  [0]int
	F105  [0]int
	F106  [0]int
	F107  [0]int
	F108  [0]int
	F109  [0]int
	F110  [0]int
	F111  [0]int
	F112  [0]int
	F113  [0]int
	F114  [0]int
	F115  [0]int
	F116  [0]int
	F117  [0]int
	F118  [0]int
	F119  [0]int
	F120  [0]int
	F121  [0]int
	F122  [0]int
	F123  [0]int
	F124  [0]int
	F125  [0]int
	F126  [0]int
	F127  [0]int
	F128  [0]int
	F129  [0]int
	F130  [0]int
	F131  [0]int
	F132  [0]int
	F133  [0]int
	F134  [0]int
	F135  [0]int
	F136  [0]int
	F137  [0]int
	F138  [0]int
	F139  [0]int
	F140  [0]int
	F141  [0]int
	F142  [0]int
	F143  [0]int
	F144  [0]int
	F145  [0]int
	F146  [0]int
	F147  [0]int
	F148  [0]int
	F149  [0]int
	F150  [0]int
	F151  [0]int
	F152  [0]int
	F153  [0]int
	F154  [0]int
	F155  [0]int
	F156  [0]int
	F157  [0]int
	F158  [0]int
	F159  [0]int
	F160  [0]int
	F161  [0]int
	F162  [0]int
	F163  [0]int
	F164  [0]int
	F165  [0]int
	F166  [0]int
	F167  [0]int
	F168  [0]int
	F169  [0]int
	F170  [0]int
	F171  [0]int
	F172  [0]int
	F173  [0]int
	F174  [0]int
	F175  [0]int
	F176  [0]int
	F177  [0]int
	F178  [0]int
	F179  [0]int
	F180  [0]int
	F181  [0]int
	F182  [0]int
	F183  [0]int
	F184  [0]int
	F185  [0]int
	F186  [0]int
	F187  [0]int
	F188  [0]int
	F189  [0]int
	F190  [0]int
	F191  [0]int
	F192  [0]int
	F193  [0]int
	F194  [0]int
	F195  [0]int
	F196  [0]int
	F197  [0]int
	F198  [0]int
	F199  [0]int
	F200  [0]int
	F201  [0]int
	F202  [0]int
	F203  [0]int
	F204  [0]int
	F205  [0]int
	F206  [0]int
	F207  [0]int
	F208  [0]int
	F209  [0]int
	F210  [0]int
	F211  [0]int
	F212  [0]int
	F213  [0]int
	F214  [0]int
	F215  [0]int
	F216  [0]int
	F217  [0]int
	F218  [0]int
	F219  [0]int
	F220  [0]int
	F221  [0]int
	F222  [0]int
	F223  [0]int
	F224  [0]int
	F225  [0]int
	F226  [0]int
	F227  [0]int
	F228  [0]int
	F229  [0]int
	F230  [0]int
	F231  [0]int
	F232  [0]int
	F233  [0]int
	F234  [0]int
	F235  [0]int
	F236  [0]int
	F237  [0]int
	F238  [0]int
	F239  [0]int
	F240  [0]int
	F241  [0]int
	F242  [0]int
	F243  [0]int
	F244  [0]int
	F245  [0]int
	F246  [0]int
	F247  [0]int
	F248  [0]int
	F249  [0]int
	F250  [0]int
	F251  [0]int
	F252  [0]int
	F253  [0]int
	F254  [0]int
	F255  [0]int
	F256  [0]int
	F257  [0]int
	F258  [0]int
	F259  [0]int
	F260  [0]int
	F261  [0]int
	F262  [0]int
	F263  [0]int
	F264  [0]int
	F265  [0]int
	F266  [0]int
	F267  [0]int
	F268  [0]int
	F269  [0]int
	F270  [0]int
	F271  [0]int
	F272  [0]int
	F273  [0]int
	F274  [0]int
	F275  [0]int
	F276  [0]int
	F277  [0]int
	F278  [0]int
	F279  [0]int
	F280  [0]int
	F281  [0]int
	F282  [0]int
	F283  [0]int
	F284  [0]int
	F285  [0]int
	F286  [0]int
	F287  [0]int
	F288  [0]int
	F289  [0]int
	F290  [0]int
	F291  [0]int
	F292  [0]int
	F293  [0]int
	F294  [0]int
	F295  [0]int
	F296  [0]int
	F297  [0]int
	F298  [0]int
	F299  [0]int
	F300  [0]int
	F301  [0]int
	F302  [0]int
	F303  [0]int
	F304  [0]int
	F305  [0]int
	F306  [0]int
	F307  [0]int
	F308  [0]int
	F309  [0]int
	F310  [0]int
	F311  [0]int
	F312  [0]int
	F313  [0]int
	F314  [0]int
	F315  [0]int
	F316  [0]int
	F317  [0]int
	F318  [0]int
	F319  [0]int
	F320  [0]int
	F321  [0]int
	F322  [0]int
	F323  [0]int
	F324  [0]int
	F325  [0]int
	F326  [0]int
	F327  [0]int
	F328  [0]int
	F329  [0]int
	F330  [0]int
	F331  [0]int
	F332  [0]int
	F333  [0]int
	F334  [0]int
	F335  [0]int
	F336  [0]int
	F337  [0]int
	F338  [0]int
	F339  [0]int
	F340  [0]int
	F341  [0]int
	F342  [0]int
	F343  [0]int
	F344  [0]int
	F345  [0]int
	F346  [0]int
	F347  [0]int
	F348  [0]int
	F349  [0]int
	F350  [0]int
	F351  [0]int
	F352  [0]int
	F353  [0]int
	F354  [0]int
	F355  [0]int
	F356  [0]int
	F357  [0]int
	F358  [0]int
	F359  [0]int
	F360  [0]int
	F361  [0]int
	F362  [0]int
	F363  [0]int
	F364  [0]int
	F365  [0]int
	F366  [0]int
	F367  [0]int
	F368  [0]int
	F369  [0]int
	F370  [0]int
	F371  [0]int
	F372  [0]int
	F373  [0]int
	F374  [0]int
	F375  [0]int
	F376  [0]int
	F377  [0]int
	F378  [0]int
	F379  [0]int
	F380  [0]int
	F381  [0]int
	F382  [0]int
	F383  [0]int
	F384  [0]int
	F385  [0]int
	F386  [0]int
	F387  [0]int
	F388  [0]int
	F389  [0]int
	F390  [0]int
	F391  [0]int
	F392  [0]int
	F393  [0]int
	F394  [0]int
	F395  [0]int
	F396  [0]int
	F397  [0]int
	F398  [0]int
	F399  [0]int
	F400  [0]int
	F401  [0]int
	F402  [0]int
	F403  [0]int
	F404  [0]int
	F405  [0]int
	F406  [0]int
	F407  [0]int
	F408  [0]int
	F409  [0]int
	F410  [0]int
	F411  [0]int
	F412  [0]int
	F413  [0]int
	F414  [0]int
	F415  [0]int
	F416  [0]int
	F417  [0]int
	F418  [0]int
	F419  [0]int
	F420  [0]int
	F421  [0]int
	F422  [0]int
	F423  [0]int
	F424  [0]int
	F425  [0]int
	F426  [0]int
	F427  [0]int
	F428  [0]int
	F429  [0]int
	F430  [0]int
	F431  [0]int
	F432  [0]int
	F433  [0]int
	F434  [0]int
	F435  [0]int
	F436  [0]int
	F437  [0]int
	F438  [0]int
	F439  [0]int
	F440  [0]int
	F441  [0]int
	F442  [0]int
	F443  [0]int
	F444  [0]int
	F445  [0]int
	F446  [0]int
	F447  [0]int
	F448  [0]int
	F449  [0]int
	F450  [0]int
	F451  [0]int
	F452  [0]int
	F453  [0]int
	F454  [0]int
	F455  [0]int
	F456  [0]int
	F457  [0]int
	F458  [0]int
	F459  [0]int
	F460  [0]int
	F461  [0]int
	F462  [0]int
	F463  [0]int
	F464  [0]int
	F465  [0]int
	F466  [0]int
	F467  [0]int
	F468  [0]int
	F469  [0]int
	F470  [0]int
	F471  [0]int
	F472  [0]int
	F473  [0]int
	F474  [0]int
	F475  [0]int
	F476  [0]int
	F477  [0]int
	F478  [0]int
	F479  [0]int
	F480  [0]int
	F481  [0]int
	F482  [0]int
	F483  [0]int
	F484  [0]int
	F485  [0]int
	F486  [0]int
	F487  [0]int
	F488  [0]int
	F489  [0]int
	F490  [0]int
	F491  [0]int
	F492  [0]int
	F493  [0]int
	F494  [0]int
	F495  [0]int
	F496  [0]int
	F497  [0]int
	F498  [0]int
	F499  [0]int
	F500  [0]int
	F501  [0]int
	F502  [0]int
	F503  [0]int
	F504  [0]int
	F505  [0]int
	F506  [0]int
	F507  [0]int
	F508  [0]int
	F509  [0]int
	F510  [0]int
	F511  [0]int
	F512  [0]int
	F513  [0]int
	F514  [0]int
	F515  [0]int
	F516  [0]int
	F517  [0]int
	F518  [0]int
	F519  [0]int
	F520  [0]int
	F521  [0]int
	F522  [0]int
	F523  [0]int
	F524  [0]int
	F525  [0]int
	F526  [0]int
	F527  [0]int
	F528  [0]int
	F529  [0]int
	F530  [0]int
	F531  [0]int
	F532  [0]int
	F533  [0]int
	F534  [0]int
	F535  [0]int
	F536  [0]int
	F537  [0]int
	F538  [0]int
	F539  [0]int
	F540  [0]int
	F541  [0]int
	F542  [0]int
	F543  [0]int
	F544  [0]int
	F545  [0]int
	F546  [0]int
	F547  [0]int
	F548  [0]int
	F549  [0]int
	F550  [0]int
	F551  [0]int
	F552  [0]int
	F553  [0]int
	F554  [0]int
	F555  [0]int
	F556  [0]int
	F557  [0]int
	F558  [0]int
	F559  [0]int
	F560  [0]int
	F561  [0]int
	F562  [0]int
	F563  [0]int
	F564  [0]int
	F565  [0]int
	F566  [0]int
	F567  [0]int
	F568  [0]int
	F569  [0]int
	F570  [0]int
	F571  [0]int
	F572  [0]int
	F573  [0]int
	F574  [0]int
	F575  [0]int
	F576  [0]int
	F577  [0]int
	F578  [0]int
	F579  [0]int
	F580  [0]int
	F581  [0]int
	F582  [0]int
	F583  [0]int
	F584  [0]int
	F585  [0]int
	F586  [0]int
	F587  [0]int
	F588  [0]int
	F589  [0]int
	F590  [0]int
	F591  [0]int
	F592  [0]int
	F593  [0]int
	F594  [0]int
	F595  [0]int
	F596  [0]int
	F597  [0]int
	F598  [0]int
	F599  [0]int
	F600  [0]int
	F601  [0]int
	F602  [0]int
	F603  [0]int
	F604  [0]int
	F605  [0]int
	F606  [0]int
	F607  [0]int
	F608  [0]int
	F609  [0]int
	F610  [0]int
	F611  [0]int
	F612  [0]int
	F613  [0]int
	F614  [0]int
	F615  [0]int
	F616  [0]int
	F617  [0]int
	F618  [0]int
	F619  [0]int
	F620  [0]int
	F621  [0]int
	F622  [0]int
	F623  [0]int
	F624  [0]int
	F625  [0]int
	F626  [0]int
	F627  [0]int
	F628  [0]int
	F629  [0]int
	F630  [0]int
	F631  [0]int
	F632  [0]int
	F633  [0]int
	F634  [0]int
	F635  [0]int
	F636  [0]int
	F637  [0]int
	F638  [0]int
	F639  [0]int
	F640  [0]int
	F641  [0]int
	F642  [0]int
	F643  [0]int
	F644  [0]int
	F645  [0]int
	F646  [0]int
	F647  [0]int
	F648  [0]int
	F649  [0]int
	F650  [0]int
	F651  [0]int
	F652  [0]int
	F653  [0]int
	F654  [0]int
	F655  [0]int
	F656  [0]int
	F657  [0]int
	F658  [0]int
	F659  [0]int
	F660  [0]int
	F661  [0]int
	F662  [0]int
	F663  [0]int
	F664  [0]int
	F665  [0]int
	F666  [0]int
	F667  [0]int
	F668  [0]int
	F669  [0]int
	F670  [0]int
	F671  [0]int
	F672  [0]int
	F673  [0]int
	F674  [0]int
	F675  [0]int
	F676  [0]int
	F677  [0]int
	F678  [0]int
	F679  [0]int
	F680  [0]int
	F681  [0]int
	F682  [0]int
	F683  [0]int
	F684  [0]int
	F685  [0]int
	F686  [0]int
	F687  [0]int
	F688  [0]int
	F689  [0]int
	F690  [0]int
	F691  [0]int
	F692  [0]int
	F693  [0]int
	F694  [0]int
	F695  [0]int
	F696  [0]int
	F697  [0]int
	F698  [0]int
	F699  [0]int
	F700  [0]int
	F701  [0]int
	F702  [0]int
	F703  [0]int
	F704  [0]int
	F705  [0]int
	F706  [0]int
	F707  [0]int
	F708  [0]int
	F709  [0]int
	F710  [0]int
	F711  [0]int
	F712  [0]int
	F713  [0]int
	F714  [0]int
	F715  [0]int
	F716  [0]int
	F717  [0]int
	F718  [0]int
	F719  [0]int
	F720  [0]int
	F721  [0]int
	F722  [0]int
	F723  [0]int
	F724  [0]int
	F725  [0]int
	F726  [0]int
	F727  [0]int
	F728  [0]int
	F729  [0]int
	F730  [0]int
	F731  [0]int
	F732  [0]int
	F733  [0]int
	F734  [0]int
	F735  [0]int
	F736  [0]int
	F737  [0]int
	F738  [0]int
	F739  [0]int
	F740  [0]int
	F741  [0]int
	F742  [0]int
	F743  [0]int
	F744  [0]int
	F745  [0]int
	F746  [0]int
	F747  [0]int
	F748  [0]int
	F749  [0]int
	F750  [0]int
	F751  [0]int
	F752  [0]int
	F753  [0]int
	F754  [0]int
	F755  [0]int
	F756  [0]int
	F757  [0]int
	F758  [0]int
	F759  [0]int
	F760  [0]int
	F761  [0]int
	F762  [0]int
	F763  [0]int
	F764  [0]int
	F765  [0]int
	F766  [0]int
	F767  [0]int
	F768  [0]int
	F769  [0]int
	F770  [0]int
	F771  [0]int
	F772  [0]int
	F773  [0]int
	F774  [0]int
	F775  [0]int
	F776  [0]int
	F777  [0]int
	F778  [0]int
	F779  [0]int
	F780  [0]int
	F781  [0]int
	F782  [0]int
	F783  [0]int
	F784  [0]int
	F785  [0]int
	F786  [0]int
	F787  [0]int
	F788  [0]int
	F789  [0]int
	F790  [0]int
	F791  [0]int
	F792  [0]int
	F793  [0]int
	F794  [0]int
	F795  [0]int
	F796  [0]int
	F797  [0]int
	F798  [0]int
	F799  [0]int
	F800  [0]int
	F801  [0]int
	F802  [0]int
	F803  [0]int
	F804  [0]int
	F805  [0]int
	F806  [0]int
	F807  [0]int
	F808  [0]int
	F809  [0]int
	F810  [0]int
	F811  [0]int
	F812  [0]int
	F813  [0]int
	F814  [0]int
	F815  [0]int
	F816  [0]int
	F817  [0]int
	F818  [0]int
	F819  [0]int
	F820  [0]int
	F821  [0]int
	F822  [0]int
	F823  [0]int
	F824  [0]int
	F825  [0]int
	F826  [0]int
	F827  [0]int
	F828  [0]int
	F829  [0]int
	F830  [0]int
	F831  [0]int
	F832  [0]int
	F833  [0]int
	F834  [0]int
	F835  [0]int
	F836  [0]int
	F837  [0]int
	F838  [0]int
	F839  [0]int
	F840  [0]int
	F841  [0]int
	F842  [0]int
	F843  [0]int
	F844  [0]int
	F845  [0]int
	F846  [0]int
	F847  [0]int
	F848  [0]int
	F849  [0]int
	F850  [0]int
	F851  [0]int
	F852  [0]int
	F853  [0]int
	F854  [0]int
	F855  [0]int
	F856  [0]int
	F857  [0]int
	F858  [0]int
	F859  [0]int
	F860  [0]int
	F861  [0]int
	F862  [0]int
	F863  [0]int
	F864  [0]int
	F865  [0]int
	F866  [0]int
	F867  [0]int
	F868  [0]int
	F869  [0]int
	F870  [0]int
	F871  [0]int
	F872  [0]int
	F873  [0]int
	F874  [0]int
	F875  [0]int
	F876  [0]int
	F877  [0]int
	F878  [0]int
	F879  [0]int
	F880  [0]int
	F881  [0]int
	F882  [0]int
	F883  [0]int
	F884  [0]int
	F885  [0]int
	F886  [0]int
	F887  [0]int
	F888  [0]int
	F889  [0]int
	F890  [0]int
	F891  [0]int
	F892  [0]int
	F893  [0]int
	F894  [0]int
	F895  [0]int
	F896  [0]int
	F897  [0]int
	F898  [0]int
	F899  [0]int
	F900  [0]int
	F901  [0]int
	F902  [0]int
	F903  [0]int
	F904  [0]int
	F905  [0]int
	F906  [0]int
	F907  [0]int
	F908  [0]int
	F909  [0]int
	F910  [0]int
	F911  [0]int
	F912  [0]int
	F913  [0]int
	F914  [0]int
	F915  [0]int
	F916  [0]int
	F917  [0]int
	F918  [0]int
	F919  [0]int
	F920  [0]int
	F921  [0]int
	F922  [0]int
	F923  [0]int
	F924  [0]int
	F925  [0]int
	F926  [0]int
	F927  [0]int
	F928  [0]int
	F929  [0]int
	F930  [0]int
	F931  [0]int
	F932  [0]int
	F933  [0]int
	F934  [0]int
	F935  [0]int
	F936  [0]int
	F937  [0]int
	F938  [0]int
	F939  [0]int
	F940  [0]int
	F941  [0]int
	F942  [0]int
	F943  [0]int
	F944  [0]int
	F945  [0]int
	F946  [0]int
	F947  [0]int
	F948  [0]int
	F949  [0]int
	F950  [0]int
	F951  [0]int
	F952  [0]int
	F953  [0]int
	F954  [0]int
	F955  [0]int
	F956  [0]int
	F957  [0]int
	F958  [0]int
	F959  [0]int
	F960  [0]int
	F961  [0]int
	F962  [0]int
	F963  [0]int
	F964  [0]int
	F965  [0]int
	F966  [0]int
	F967  [0]int
	F968  [0]int
	F969  [0]int
	F970  [0]int
	F971  [0]int
	F972  [0]int
	F973  [0]int
	F974  [0]int
	F975  [0]int
	F976  [0]int
	F977  [0]int
	F978  [0]int
	F979  [0]int
	F980  [0]int
	F981  [0]int
	F982  [0]int
	F983  [0]int
	F984  [0]int
	F985  [0]int
	F986  [0]int
	F987  [0]int
	F988  [0]int
	F989  [0]int
	F990  [0]int
	F991  [0]int
	F992  [0]int
	F993  [0]int
	F994  [0]int
	F995  [0]int
	F996  [0]int
	F997  [0]int
	F998  [0]int
	F999  [0]int
	F1000 [0]int
	F1001 [0]int
	F1002 [0]int
	F1003 [0]int
	F1004 [0]int
	F1005 [0]int
	F1006 [0]int
	F1007 [0]int
	F1008 [0]int
	F1009 [0]int
	F1010 [0]int
	F1011 [0]int
	F1012 [0]int
	F1013 [0]int
	F1014 [0]int
	F1015 [0]int
	F1016 [0]int
	F1017 [0]int
	F1018 [0]int
	F1019 [0]int
	F1020 [0]int
	F1021 [0]int
	F1022 [0]int
	F1023 [0]int
	*big.Float
}

// TestBigMethodWideStruct: plain fields do not count toward the supplier
// search's bound, so a huge Float behind 1024 of them is charged.
func TestBigMethodWideStruct(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}} {{x}}`)
	require.NoError(t, err)
	x := WideBig{Float: new(big.Float).SetMantExp(big.NewFloat(1.5), -(1 << 30))}
	for _, opt := range []libhandlebars.RenderOption{libhandlebars.WithGoContext(), libhandlebars.WithJSONContext()} {
		start := time.Now()
		_, err = libhandlebars.RenderWith(tpl, map[string]any{"x": []any{x}}, opt)
		require.ErrorContains(t, err, "maximum of")
		require.Less(t, time.Since(start), 5*time.Second)
	}
}
