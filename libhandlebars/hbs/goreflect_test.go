// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
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
	deep := diamond(18)
	start := time.Now()
	fieldsByName(deep)
	require.Less(t, time.Since(start), 100*time.Millisecond, "a level-18 diamond")

	// A cold lookup through a fresh level-18 diamond, end to end.
	if raceEnabled || testing.Short() {
		return
	}
	cold := reflect.StructOf([]reflect.StructField{{Name: "D", Type: diamond(18), Anonymous: true}, {Name: "Cold", Type: reflect.TypeFor[int]()}})
	p, err := Parse(`{{x.missing}}`, DefaultLimits())
	require.NoError(t, err)
	x := reflect.New(cold).Interface()
	m := &countMeter{}
	start = time.Now()
	_, err = p.Render(map[string]any{"x": x}, Options{Meter: m})
	require.NoError(t, err)
	per := float64(time.Since(start).Nanoseconds()) / float64(m.n)
	t.Logf("cold level-18 diamond: %d steps, %.0f ns/step", m.n, per)
	require.Less(t, per, 200.0)
}

type countMeter struct{ n int64 }

func (m *countMeter) Charge(n int64) error { m.n += n; return nil }
