// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/svc/libhandlebars"
	"github.com/luthersystems/svc/libhandlebars/hbs"
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
	cases := []struct {
		name, tpl string
		ctx       any
	}{
		{"wide each", `{{#each xs}}{{f4999}}{{/each}}`, map[string]any{"xs": wides}},
		{"deep path", `{{#each xs}}{{` + deepPath.String() + `}}{{/each}}`, map[string]any{"xs": []any{reflect.New(cur).Elem().Interface(), reflect.New(cur).Elem().Interface()}}},
		{"long name", `{{#each xs}}{{` + long + `}}{{/each}}`, map[string]any{"xs": structs[:2000]}},
		{"1 MiB tag", `{{#each xs}}{{b}}{{/each}}`, map[string]any{"xs": taggedItems}},
		{"[]struct", `{{#each xs}}{{name}}{{/each}}`, map[string]any{"xs": structs}},
		{"each struct", `{{#each xs}}{{#each this}}{{this}}{{/each}}{{/each}}`, map[string]any{"xs": structs}},
		{"str slice", `{{#each xs}}{{../ys}}{{/each}}`, map[string]any{"xs": make([]int, 200), "ys": make([]int, 2000)}},
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
			if _, err := tpl.Render(c.ctx, hbs.Options{Meter: m, Limits: hbs.Limits{MaxSteps: 1 << 40, MaxOutputBytes: 1 << 30}}); err != nil {
				t.Fatal(c.name, err)
			}
			best = min(best, time.Since(st))
			steps = m.n
		}
		per := float64(best.Nanoseconds()) / float64(steps)
		t.Logf("%-12s %9d steps %7.1f ns/step", c.name, steps, per)
		if per > 200 {
			t.Errorf("%s: %.0f ns per step, ceiling 200", c.name, per)
		}
	}
}
