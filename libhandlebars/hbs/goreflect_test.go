// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// diamond returns an embedding diamond n levels deep: each level embeds the
// two types of the level below, all reaching the same base with field Z.
func diamond(n int) reflect.Type {
	base := reflect.StructOf([]reflect.StructField{{Name: "Z", Type: reflect.TypeFor[int]()}})
	a, b := base, base
	for i := range n {
		mk := func(tag string) reflect.Type {
			return reflect.StructOf([]reflect.StructField{
				{Name: "A" + tag + strconv.Itoa(i), Type: a, Anonymous: true},
				{Name: "B" + tag + strconv.Itoa(i), Type: b, Anonymous: true},
				{Name: "W" + tag + strconv.Itoa(i), Type: reflect.TypeFor[int]()},
			})
		}
		a, b = mk("a"), mk("b")
	}
	return a
}

// allNames collects every field name reachable through embedding.
func allNames(t reflect.Type, names map[string]bool, seen map[reflect.Type]bool) {
	if seen[t] {
		return
	}
	seen[t] = true
	for i := range t.NumField() {
		f := t.Field(i)
		names[f.Name] = true
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if f.Anonymous && ft.Kind() == reflect.Struct {
			allNames(ft, names, seen)
		}
	}
}

type fbnInner struct{ X, Y int }
type fbnOther struct{ X, Q int }
type fbnDouble struct {
	fbnInner
	fbnOther
	Y string
}
type fbnTwice struct {
	*fbnInner
	Mid struct{ fbnInner }
}

// TestFieldsByName: fieldsByName agrees with reflect.FieldByName for every
// name, on conflicts and diamonds, and stays linear where VisibleFields is
// exponential.
func TestFieldsByName(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[fbnDouble](), reflect.TypeFor[fbnTwice](), diamond(6), diamond(5),
	} {
		got := fieldsByName(typ)
		names := map[string]bool{"Nope": true}
		allNames(typ, names, map[reflect.Type]bool{})
		for name := range names {
			want, ok := typ.FieldByName(name)
			f, gok := got[name]
			require.Equal(t, ok, gok, "%v.%s", typ, name)
			if ok {
				require.Equal(t, want.Index, f.Index, "%v.%s", typ, name)
			}
		}
	}
	// Each embedded type is visited once: the fields visited grow
	// linearly with the levels (VisibleFields grows as 2^levels).
	_, v16 := fieldsByNameCount(diamond(16))
	_, v18 := fieldsByNameCount(diamond(18))
	// The top type's 3 fields, 2 types of 3 at each lower level, the base.
	require.Equal(t, 3+3*2*15+1, v16)
	require.Equal(t, v16+3*2*2, v18)

	// A cold lookup through a fresh level-18 diamond, end to end.
	if raceEnabled || testing.Short() {
		return
	}
	// Best of 3, each on a fresh type: one run is about half a
	// millisecond, which a busy machine can stretch.
	p, err := Parse(`{{x.missing}}`, DefaultLimits())
	require.NoError(t, err)
	per := math.Inf(1)
	for run := range 3 {
		cold := reflect.StructOf([]reflect.StructField{{Name: "D", Type: diamond(18), Anonymous: true}, {Name: "Cold" + strconv.Itoa(run), Type: reflect.TypeFor[int]()}})
		x := reflect.New(cold).Interface()
		m := &countMeter{}
		start := time.Now()
		_, err = p.Render(map[string]any{"x": x}, Options{Meter: m})
		require.NoError(t, err)
		per = min(per, float64(time.Since(start).Nanoseconds())/float64(m.n))
		t.Logf("cold level-18 diamond: %d steps, %.0f ns/step", m.n, per)
	}
	if per > 200 {
		// Contention from parallel packages: compare with the plain
		// evaluator now (as the ceiling tests do).
		base := min(internalBaselineNs(t), 50)
		require.False(t, per > 400 || per > 6*base, "%.0f ns/step, plain evaluator %.0f", per, base)
	}
}

type countMeter struct{ n int64 }

func (m *countMeter) Charge(n int64) error { m.n += n; return nil }

// internalBaselineNs is the plain evaluator's time per step now.
func internalBaselineNs(t *testing.T) float64 {
	t.Helper()
	p, err := Parse(`{{#each a}}{{x}}{{/each}}`, DefaultLimits())
	require.NoError(t, err)
	a := make([]any, 20000)
	for i := range a {
		a[i] = 1.0
	}
	best := 0.0
	for range 5 {
		m := &countMeter{}
		start := time.Now()
		_, err := p.Render(map[string]any{"x": "abc", "a": a}, Options{Meter: m})
		require.NoError(t, err)
		if ns := float64(time.Since(start).Nanoseconds()) / float64(m.n); best == 0 || ns < best {
			best = ns
		}
	}
	return best
}

type deepKey struct{ I any }

// TestMapKeyWalksBounded: the NaN test and comparison cost of a map key
// stop at MaxDepth, as the size walk does, instead of following the key
// all the way down (Value.Equal would).
func TestMapKeyWalksBounded(t *testing.T) {
	deep := func(n int, leaf any) reflect.Value {
		v := leaf
		for range n {
			v = deepKey{v}
		}
		return reflect.ValueOf(v)
	}
	nan := math.NaN()
	walk := func() *keyWalk { return &keyWalk{limit: 1 << 40, maxDepth: 100} }
	kw := walk()
	require.True(t, kw.nan(deep(5, nan), 0))
	require.False(t, kw.nan(deep(5, 1.0), 0))
	require.False(t, kw.deep)
	kw = walk()
	require.False(t, kw.nan(deep(1000, nan), 0), "past MaxDepth: not followed")
	require.True(t, kw.deep, "and recorded")
	require.True(t, walk().nan(reflect.ValueOf([2]complex128{0, complex(math.Inf(1), nan)}), 0))
	// Each level is two: the struct, then its interface field.
	cut, deeper := walk(), walk()
	require.Equal(t, cut.cmp(deep(60, 1.0), 0), deeper.cmp(deep(100_000, 1.0), 0), "past MaxDepth: not followed")
	require.True(t, deeper.deep)
	shallow := walk()
	shallow.cmp(deep(40, 1.0), 0)
	require.False(t, shallow.deep)

	// A key with a billion paths (each level's 32 elements share the one
	// below): nan stops at the limit, as cmp does.
	var dag [32]any
	var box any = 1.0
	for range 6 {
		for i := range dag {
			dag[i] = box
		}
		box = dag
	}
	bounded := &keyWalk{limit: 1000, maxDepth: 100}
	require.False(t, bounded.nan(reflect.ValueOf(dag), 0))
	require.Equal(t, int64(1001), bounded.visits, "stops past the limit")
	require.Less(t, bounded.cmp(reflect.ValueOf(dag), 0), int64(2000), "and cmp near it")
}

// TestKeyWalkCmpShared: cmp shares nan's visit budget across a whole key,
// so a key whose every level holds a large comparable value (each level's
// cmp under the limit on its own) stops near the limit instead of walking
// depth times it.
func TestKeyWalkCmpShared(t *testing.T) {
	ints := make([]reflect.StructField, 64)
	for i := range ints {
		ints[i] = reflect.StructField{Name: "F" + strconv.Itoa(i), Type: reflect.TypeFor[int]()}
	}
	leaf := reflect.New(reflect.StructOf(ints)).Elem().Interface()
	var u [64]any
	for i := range u {
		u[i] = leaf
	}
	// L_0 = struct{U any}; L_d = struct{U any; N L_{d-1}}.
	lt := reflect.StructOf([]reflect.StructField{{Name: "U", Type: reflect.TypeFor[any]()}})
	lv := reflect.New(lt).Elem()
	lv.Field(0).Set(reflect.ValueOf(u))
	for range 200 {
		nt := reflect.StructOf([]reflect.StructField{{Name: "U", Type: reflect.TypeFor[any]()}, {Name: "N", Type: lt}})
		nv := reflect.New(nt).Elem()
		nv.Field(0).Set(reflect.ValueOf(u))
		nv.Field(1).Set(lv)
		lt, lv = nt, nv
	}
	kw := &keyWalk{limit: 10_000, maxDepth: 1 << 20}
	kw.cmp(lv, 0)
	require.Greater(t, kw.visits, kw.limit, "the key is past the budget")
	require.Less(t, kw.visits, 2*kw.limit, "and the walk stopped near it")
}

// TestGoSizerExact: the %v sizer counts exactly the bytes fmt prints for
// values without methods or addresses (so the produced-bytes check never
// refuses a text that fits), nil references and separators included.
func TestGoSizerExact(t *testing.T) {
	type pair struct {
		A int
		B []string
		C map[string]bool
	}
	var nilFn func()
	for _, x := range []any{
		[]int{}, []int{1}, []int{1, 22, 333}, []*int{nil}, []*int{nil, nil},
		[]chan int{nil, nil, nil}, []func(){nilFn}, [2]bool{true, false},
		map[string]int{}, map[string]int{"a": 1}, map[string]int{"a": 1, "bb": 22},
		pair{}, pair{1, []string{"x", ""}, map[string]bool{"k": true, "": false}},
		struct{}{}, []any{nil, 1.5, "s", []any{}}, [][]int{{1}, {}, {2, 3}},
		[]float32{0.1, 1e-40}, []complex128{complex(1, -2), 0}, []uint8{0, 255},
	} {
		z := &goSizer{r: &renderer{maxDepth: 64}, limit: 1 << 40}
		got := z.size(reflect.ValueOf(x), 0)
		require.False(t, z.addr, "%#v", x)
		require.Equal(t, len(fmt.Sprintf("%v", x)), got, "%#v", x)
	}
}

type printVal struct{ N int }

func (v printVal) String() string { return "V" + strconv.Itoa(v.N) }

type printPtr struct{ N int }

func (p *printPtr) String() string {
	if p == nil {
		return "nil-ptr"
	}
	return "P" + strconv.Itoa(p.N)
}

type printPanic int

func (printPanic) String() string { panic("boom") }

type printFmt int

func (f printFmt) Format(s fmt.State, c rune) { _, _ = fmt.Fprintf(s, "F%c%d", c, int(f)) }

type printHolder struct {
	D   time.Duration
	d   time.Duration
	V   printVal
	v   printVal
	P   printPtr
	PP  *printPtr
	VP  *printVal
	I   any
	i   any
	E   error
	R   reflect.Value
	F   printFmt
	N   json.Number
	Big *big.Int
}

// TestGoPrintMatchesFmt: goPrint, the walk that prints a value holding
// method-bearing values, writes fmt's %v exactly, wherever fmt calls the
// method and wherever it cannot (unexported fields, pointer receivers,
// nil pointers, panics), and sizes all but the method results exactly.
func TestGoPrintMatchesFmt(t *testing.T) {
	h := printHolder{
		D: time.Second, d: time.Second, V: printVal{1}, v: printVal{2},
		P: printPtr{3}, I: printVal{4}, i: printVal{5}, E: errors.New("e"),
		R: reflect.ValueOf(7), F: 8, N: "9.5", Big: big.NewInt(10),
	}
	hp := h
	hp.PP, hp.VP, hp.I, hp.R, hp.Big = &printPtr{11}, nil, printPanic(0), reflect.ValueOf("rv"), nil
	for _, x := range []any{
		time.Second, []time.Duration{1, 2}, []json.Number{"1", "2.5"}, h, &h, hp, []printHolder{h, hp},
		[]any{printVal{1}, &printPtr{2}, (*printPtr)(nil), (*printVal)(nil), printPanic(1), printFmt(3), nil},
		map[printVal]printPtr{{2}: {1}, {1}: {2}}, map[time.Duration]string{2: "b", 1: "a"},
		map[any]int{printVal{1}: 1, 2: 2, "s": 3, time.Duration(4): 4},
		map[float64]printVal{math.NaN(): {1}, 1: {2}, -1: {3}},
		[]reflect.Value{reflect.ValueOf(1), reflect.ValueOf("s"), {}},
		struct{ A []error }{[]error{nil, errors.New("x")}},
		[2]printFmt{1, 2}, []*big.Int{big.NewInt(1), nil}, []byte{1, 2},
		[]float32{0.1}, []complex64{complex(1, 2)}, map[string][]printVal{"k": {{1}}},
	} {
		r := &renderer{maxDepth: 64, maxSteps: 1 << 40, maxProduced: 1 << 40}
		z := &goSizer{r: r, limit: 1 << 40}
		size := z.size(reflect.ValueOf(x), 0)
		require.False(t, z.addr, "%#v", x)
		want := fmt.Sprintf("%v", x)
		require.Equal(t, want, string((&goPrinter{r: r}).print(nil, reflect.ValueOf(x), 0)), "%#v", x)
		require.LessOrEqual(t, size, len(want), "%#v", x)
	}
}
