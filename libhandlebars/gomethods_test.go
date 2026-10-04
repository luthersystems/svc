// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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
