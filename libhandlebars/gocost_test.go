// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/svc/libhandlebars"
	"github.com/luthersystems/svc/libhandlebars/hbs"
	"github.com/stretchr/testify/require"
)

type goCostItem struct{ Name string }

func (goCostItem) A() {}
func (goCostItem) B() {}

// TestGoContextCostCeiling: reading Go values stays within the cost
// model's ceiling of 200 ns a step (DETERMINISM.md, "Cost model") on the
// worst shapes found in review: very wide structs, deep paths through
// nested structs, long non-ASCII names on a type with methods, and large
// slices of structs.
func TestGoContextCostCeiling(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing test: skipped under -race and -short")
	}
	wideFields := make([]reflect.StructField, 0, 5000)
	for i := range 5000 {
		wideFields = append(wideFields, reflect.StructField{Name: fmt.Sprintf("F%d", i), Type: reflect.TypeFor[int]()})
	}
	wide := reflect.StructOf(wideFields)
	// embedded chain 40 deep
	cur := reflect.StructOf([]reflect.StructField{{Name: "Leaf", Type: reflect.TypeFor[string]()}})
	for i := range 40 {
		cur = reflect.StructOf([]reflect.StructField{{Name: fmt.Sprintf("E%d", i), Type: cur, Anonymous: false}, {Name: fmt.Sprintf("X%d", i), Type: reflect.TypeFor[int]()}})
	}
	var deepPath strings.Builder
	for i := 39; i >= 0; i-- {
		fmt.Fprintf(&deepPath, "e%d.", i)
	}
	deepPath.WriteString("leaf")
	wides := make([]any, 200)
	for i := range wides {
		wides[i] = reflect.New(wide).Elem().Interface()
	}
	tagged := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeFor[int](), Tag: reflect.StructTag(`big:"` + strings.Repeat("x", 1<<20) + `"`)},
		{Name: "B", Type: reflect.TypeFor[string]()},
	})
	taggedItems := make([]any, 50)
	for i := range taggedItems {
		taggedItems[i] = reflect.New(tagged).Elem().Interface()
	}
	structs := make([]goCostItem, 20000)
	long := strings.Repeat("é", 1000)
	bigMap := make(map[any]any, 100_000)
	for i := range 100_000 {
		bigMap[strconv.Itoa(i)] = i
	}
	bigTag := reflect.ChanOf(reflect.BothDir, reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeFor[int](), Tag: reflect.StructTag(`big:"` + strings.Repeat("x", 4<<20) + `"`)},
	}))
	cases := []struct {
		name, tpl string
		ctx       any
		once      bool // the render ends with an error
	}{
		{"%v of a 100k map[any]any", `{{prettyp-num-en m}}`, map[string]any{"m": bigMap}, true},
		{"chan type with 4 MiB tag", `{{c}}`, map[string]any{"c": reflect.MakeChan(bigTag, 0).Interface()}, true},
		{"boxing 1 MiB structs", `{{#each xs}}{{#each ../ys}}{{/each}}{{/each}}`, map[string]any{"xs": make([]int, 32), "ys": []struct{ Big [1 << 20]byte }{{}}}, false},
		{"1000-level embedding lookups", `{{#each xs}}{{leaf}}{{/each}}`, map[string]any{"xs": reflect.MakeSlice(reflect.SliceOf(embedChain(1000)), 20000, 20000).Interface()}, false},
		{"wide each", `{{#each xs}}{{f4999}}{{/each}}`, map[string]any{"xs": wides}, false},
		{"deep path", `{{#each xs}}{{` + deepPath.String() + `}}{{/each}}`, map[string]any{"xs": []any{reflect.New(cur).Elem().Interface(), reflect.New(cur).Elem().Interface()}}, false},
		{"long name", `{{#each xs}}{{` + long + `}}{{/each}}`, map[string]any{"xs": structs[:2000]}, false},
		{"1 MiB tag", `{{#each xs}}{{b}}{{/each}}`, map[string]any{"xs": taggedItems}, false},
		{"[]struct", `{{#each xs}}{{name}}{{/each}}`, map[string]any{"xs": structs}, false},
		{"each struct", `{{#each xs}}{{#each this}}{{this}}{{/each}}{{/each}}`, map[string]any{"xs": structs}, false},
		{"str slice", `{{#each xs}}{{../ys}}{{/each}}`, map[string]any{"xs": make([]int, 200), "ys": make([]int, 2000)}, false},
	}
	for _, c := range cases {
		tpl, err := libhandlebars.Parse(c.tpl)
		if err != nil {
			t.Fatal(err)
		}
		best := time.Hour
		var steps int64
		for range 5 {
			m := &countMeter{}
			st := time.Now()
			if _, err := tpl.Render(c.ctx, hbs.Options{Meter: m, Limits: hbs.Limits{MaxSteps: 1 << 40, MaxOutputBytes: 1 << 30}}); (err != nil) != c.once {
				t.Fatal(c.name, err)
			}
			best = min(best, time.Since(st))
			steps = m.n
		}
		per := float64(best.Nanoseconds()) / float64(steps)
		t.Logf("%-12s %9d steps %7.1f ns/step", c.name, steps, per)
		if ceilingFails(t, per) {
			t.Errorf("%s: %.0f ns per step, ceiling 200", c.name, per)
		}
	}
}

// TestGoContextColdPlan: the first use of a struct type in a render is
// charged for building its plan, before it is built, so a cold build of a
// 5,000-field type stays within the ceiling.
func TestGoContextColdPlan(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing test: skipped under -race and -short")
	}
	worst := 0.0
	for run := range 3 {
		fields := make([]reflect.StructField, 5000)
		for i := range fields {
			// A tag unique to this run makes a type no plan was built for.
			fields[i] = reflect.StructField{Name: "F" + strconv.Itoa(i), Type: reflect.TypeFor[int](), Tag: reflect.StructTag(`cold:"` + strconv.Itoa(run) + `"`)}
		}
		v := reflect.New(reflect.StructOf(fields)).Elem().Interface()
		tpl, err := libhandlebars.Parse(`{{x.f4999}}`)
		require.NoError(t, err)
		m := &countMeter{}
		start := time.Now()
		_, err = tpl.Render(map[string]any{"x": v}, hbs.Options{Meter: m})
		require.NoError(t, err)
		per := float64(time.Since(start).Nanoseconds()) / float64(m.n)
		t.Logf("cold 5000-field plan: %d steps, %.0f ns/step", m.n, per)
		if run > 0 { // the first run also warms the engine itself
			worst = max(worst, per)
		}
	}
	if ceilingFails(t, worst) {
		t.Errorf("cold plan: %.0f ns per step, ceiling 200", worst)
	}
}

// TestGoContextLongFieldName: a field's name is charged by its length when
// the type's plan is built, on every render (whether or not reflect or the
// engine has the type's fields cached). The JSON conversion's charge is
// tested in TestGoJSONCostLongFieldName.
func TestGoContextLongFieldName(t *testing.T) {
	name := "A" + strings.Repeat("a", 1<<20)
	typ := reflect.StructOf([]reflect.StructField{{Name: name, Type: reflect.TypeFor[int]()}})
	v := reflect.New(typ).Elem().Interface()
	tpl, err := libhandlebars.Parse(`{{x.missing}}`)
	require.NoError(t, err)
	for range 2 {
		m := &countMeter{}
		_, err = tpl.Render(map[string]any{"x": v}, hbs.Options{Meter: m})
		require.NoError(t, err)
		require.GreaterOrEqual(t, m.n, int64(len(name)/16))
	}
}

// TestGoContextMapKeyCompare: printing a Go map (%v, here in a helper's
// error text) sorts its keys (fmtsort),
// and the comparisons are charged by key length: strings sharing a long
// prefix, and arrays compared element by element.
func TestGoContextMapKeyCompare(t *testing.T) {
	prefix := strings.Repeat("p", 64<<10)
	strs := make(map[string]int, 512)
	for i := range 512 {
		strs[prefix+strconv.Itoa(i)] = i
	}
	arrs := make(map[[256]byte]int, 4000)
	for i := range 4000 {
		var k [256]byte
		k[254], k[255] = byte(i>>8), byte(i)
		arrs[k] = i
	}
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		v    any
		min  int64 // the comparisons alone: n keys, log2(n) times, by length
	}{
		{"shared prefix", strs, 512 * 10 * (64 << 10) / 256},
		{"array keys", arrs, 4000 * 12 * 256 / 4},
	} {
		// Best of the warm runs: one slow sample under a parallel test
		// run does not decide it.
		best := math.Inf(1)
		for run := range 4 {
			m := &countMeter{}
			start := time.Now()
			_, err := tpl.Render(map[string]any{"x": tc.v}, hbs.Options{Meter: m})
			require.Error(t, err)
			require.GreaterOrEqual(t, m.n, tc.min, tc.name)
			per := float64(time.Since(start).Nanoseconds()) / float64(m.n)
			t.Logf("%s: %d steps, %.0f ns/step", tc.name, m.n, per)
			if run > 0 {
				best = min(best, per)
			}
		}
		if !raceEnabled && !testing.Short() && ceilingFails(t, best) {
			t.Errorf("%s: %.0f ns per step, ceiling 200", tc.name, best)
		}
	}
}
