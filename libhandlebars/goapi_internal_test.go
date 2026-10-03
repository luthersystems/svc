// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/luthersystems/svc/libhandlebars/hbs"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

func TestJSONGoContextSetting(t *testing.T) {
	for _, c := range []struct {
		v     string
		ok    bool
		want  bool
		level logrus.Level // 0: nothing logged
	}{
		{"", false, false, 0},
		{"", true, false, 0},
		{"true", true, true, logrus.InfoLevel},
		{"TRUE", true, false, logrus.WarnLevel},
		{"1", true, false, logrus.WarnLevel},
		{" true", true, false, logrus.WarnLevel},
		{"false", true, false, logrus.WarnLevel},
	} {
		log, hook := test.NewNullLogger()
		require.Equal(t, c.want, jsonGoContextSetting(c.v, c.ok, log), "%q", c.v)
		if c.level == 0 {
			require.Empty(t, hook.AllEntries(), "%q", c.v)
			continue
		}
		require.Len(t, hook.AllEntries(), 1, "%q", c.v)
		require.Equal(t, c.level, hook.LastEntry().Level, "%q", c.v)
	}
}

// TestJSONStringLen: jsonStringLen is the length encoding/json (whose
// escaping libjson copies) writes, for every byte and the escaped runes.
func TestJSONStringLen(t *testing.T) {
	ins := []string{"", "plain", "a\"b\\c", "\b\f\n\r\t", "<>&", "  ", "é漢🙂", "\xff\xfe", "\xe2\x80", "x\x00\x1fy"}
	for b := range 256 {
		ins = append(ins, string([]byte{byte(b)}), "a"+string([]byte{byte(b)})+"é")
	}
	for _, s := range ins {
		want, err := json.Marshal(s)
		require.NoError(t, err)
		require.Equal(t, int64(len(want)), jsonStringLen(s), "%q", s)
	}
}

type budgetItem struct{ A, B int }

// TestJSONGoContextBudget: WithJSONContext's conversion is charged against
// MaxSteps before json.Marshal runs, and bounded in depth.
func TestJSONGoContextBudget(t *testing.T) {
	item := &budgetItem{1, 2}
	items := make([]*budgetItem, 100_000)
	for i := range items {
		items[i] = item
	}
	bud := &goBudget{max: 1 << 40}
	require.NoError(t, goJSONCost(bud, reflect.ValueOf(map[string]any{"items": items}), jsonGoMaxDepth))
	require.Greater(t, bud.used, int64(1_000_000), "100k shared pointers charged as encoding/json encodes them")

	bud = &goBudget{max: 1000}
	err := goJSONCost(bud, reflect.ValueOf(items), jsonGoMaxDepth)
	var herr *hbs.Error
	require.ErrorAs(t, err, &herr)
	require.Equal(t, hbs.KindLimit, herr.Kind)

	var nested any = 1
	for range jsonGoMaxDepth + 10 {
		nested = []any{nested}
	}
	tpl, err := Parse(`x`)
	require.NoError(t, err)
	_, err = RenderWith(tpl, map[string]any{"n": nested}, WithJSONContext())
	require.ErrorContains(t, err, "nests deeper than 1024")
	out, err := RenderWith(tpl, map[string]any{"n": []any{1}}, WithJSONContext())
	require.NoError(t, err)
	require.Equal(t, "x", out)
}

type jsCycle struct {
	Arr  [10]int
	Self *jsCycle
}

type (
	jsCA struct{ B *jsCB }
	jsCB struct{ C *jsCC }
	jsCC struct{ A *jsCA }
)

type jsInner struct{ N int }

type jsOuter struct {
	First jsInner
	Self  *jsInner
}

type jsBadText struct{ X int }

func (jsBadText) MarshalText() ([]byte, error) { return nil, errors.New("no text") }

type jsText struct{ S string }

func (t jsText) MarshalText() ([]byte, error) { return []byte("t:" + t.S), nil }

type jsPtrText struct{ S string }

func (t *jsPtrText) MarshalText() ([]byte, error) { return []byte("p:" + t.S), nil }

type jsHidden struct {
	ch   chan int //nolint:unused // unexported: encoding/json skips it
	C    chan int `json:"-"`
	Ok   int
	Zero struct{ C chan int } `json:",omitzero"`
}

type jsEmbedA struct{ X, Y int }
type jsEmbedB struct{ X, Z int }
type jsEmbed struct {
	jsEmbedA
	jsEmbedB
	Y string `json:"y"`
}

type jsHolder struct {
	V  jsPtrText
	PV *jsPtrText
	C  chan int `json:",omitempty"`
}

// TestGoJSONCostMatchesMarshal: for many shapes, goJSONCost fails exactly
// where json.Marshal fails, with its text, succeeds where it succeeds, and
// charges the same steps on every run.
func TestGoJSONCostMatchesMarshal(t *testing.T) {
	cyc := &jsCycle{}
	cyc.Self = cyc
	selfMap := map[string]any{}
	selfMap["self"] = selfMap
	selfSlice := []any{nil}
	selfSlice[0] = selfSlice
	type mixedA struct{ M map[string]any }
	ma := &mixedA{M: map[string]any{}}
	ma.M["a"] = ma
	outer := &jsOuter{}
	outer.Self = &outer.First
	ca := &jsCA{B: &jsCB{C: &jsCC{}}}
	ca.B.C.A = ca
	var deep any = 1
	for range 3000 {
		deep = []any{deep}
	}
	shared := &jsEmbedA{1, 2}
	dag := map[string]any{"a": shared, "b": shared, "c": []any{shared, shared}}
	for name, v := range map[string]any{
		"pointer cycle":       cyc,
		"map cycle":           selfMap,
		"slice cycle":         selfSlice,
		"mixed cycle":         ma,
		"first-field pointer": outer,
		"bad RawMessage first": struct {
			X json.RawMessage
			Y chan int
		}{X: json.RawMessage("{")},
		"bad time first": struct {
			T time.Time
			F float64
		}{time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), math.NaN()},
		"bad text then cycle": struct {
			B jsBadText
			C *jsCycle
		}{C: cyc},
		"nil Marshaler interface then NaN": struct {
			J json.Marshaler
			F float64
		}{F: math.NaN()},
		"string option": struct {
			S string      `json:",string"`
			N int         `json:",string"`
			B *bool       `json:",string"`
			F float64     `json:",string"`
			X json.Number `json:",string"`
		}{S: `a"b\c<>`, N: 7, F: 1.5, X: "12"},
		"string option memo": func() any {
			q := strings.Repeat(`"`, 1024)
			v := struct {
				Q *string `json:",string"`
				U []*string
			}{Q: &q}
			for range 100 {
				v.U = append(v.U, &q)
			}
			return v
		}(),
		"good marshalers then NaN": struct {
			T time.Time
			R json.RawMessage
			F float64
		}{time.Unix(0, 0).UTC(), json.RawMessage(`{"a": 1}`), math.Inf(1)},
		"three-type cycle":   ca,
		"cycle after prefix": map[string]any{"x": []any{[]any{ca.B}}},
		"unexported and -":   jsHidden{Ok: 1},
		"embedded conflicts": jsEmbed{},
		"text keys":          map[jsText]int{{"a"}: 1, {"b"}: 2},
		"int keys":           map[int]string{3: "c", 1: "a", 20: "b"},
		"bad key type":       map[[2]int]int{{1, 2}: 3},
		"NaN":                map[string]any{"a": 1, "b": []any{2.0, math.NaN()}},
		"Inf float32":        []float32{float32(math.Inf(-1))},
		"number ok":          json.Number("1.5e3"),
		"number bad":         map[string]any{"n": json.Number("abc")},
		"chan":               map[string]any{"z": 1, "a": make(chan int)},
		"func in slice":      []any{1, func() {}},
		"complex":            struct{ C complex64 }{1},
		"omitempty chan":     jsHolder{},
		"addressable text":   &jsHolder{V: jsPtrText{"v"}, PV: &jsPtrText{"pv"}},
		"unaddressable text": jsHolder{V: jsPtrText{"v"}},
		"deep":               deep,
		"dag":                dag,
		"bytes":              map[string]any{"b": []byte("hello"), "n": []byte(nil)},
		"nil things":         map[string]any{"m": map[string]int(nil), "s": []int(nil), "p": (*int)(nil), "i": nil},
	} {
		_, want := json.Marshal(v)
		var steps int64 = -1
		for range 20 {
			bud := &goBudget{max: 1 << 50}
			got := goJSONCost(bud, reflect.ValueOf(v), 0)
			if want == nil {
				require.NoError(t, got, name)
				b, merr := json.Marshal(v)
				require.NoError(t, merr, name)
				require.LessOrEqual(t, bud.size, int64(len(b)), "%s: the size is a lower bound", name)
			} else {
				require.Error(t, got, name)
				require.Equal(t, want.Error(), got.Error(), name)
			}
			if steps >= 0 {
				require.Equal(t, steps, bud.used, "%s: same steps every run", name)
			}
			steps = bud.used
		}
	}
}

// TestGoJSONCostDuplicateKeys: a map whose keys encode to the same text is
// refused, as encoding/json would write them in Go's map order.
func TestGoJSONCostDuplicateKeys(t *testing.T) {
	m := map[*big.Int]string{}
	for i := range 8 {
		m[big.NewInt(1)] = strconv.Itoa(i)
	}
	for range 10 {
		err := goJSONCost(&goBudget{max: 1 << 40}, reflect.ValueOf(m), 0)
		require.EqualError(t, err, `json: map map[*big.Int]string has two keys that encode as "1"`)
	}
}

type jsFailKey int

func (k jsFailKey) MarshalText() ([]byte, error) {
	return nil, fmt.Errorf("key %d fails", int(k))
}

// TestGoJSONCostKeyErrors: when several map keys fail to encode, the walk
// reports the least error text, whatever Go's map order (encoding/json
// reports whichever it meets first).
func TestGoJSONCostKeyErrors(t *testing.T) {
	m := map[jsFailKey]int{3: 3, 1: 1, 2: 2, 4: 4}
	for range 50 {
		err := goJSONCost(&goBudget{max: 1 << 40}, reflect.ValueOf(m), 0)
		require.EqualError(t, err, `json: encoding error for type "map[libhandlebars.jsFailKey]int": "key 1 fails"`)
	}
}

// TestGoJSONCostStringOption: a ",string" field's size is a lower bound of
// what encoding/json writes, and close to it.
func TestGoJSONCostStringOption(t *testing.T) {
	v := struct {
		S string `json:",string"`
		N int    `json:",string"`
	}{S: strings.Repeat(`"`, 1<<16), N: 12345}
	b, err := json.Marshal(v)
	require.NoError(t, err)
	bud := &goBudget{max: 1 << 40}
	require.NoError(t, goJSONCost(bud, reflect.ValueOf(v), 0))
	require.LessOrEqual(t, bud.size, int64(len(b)))
	require.Greater(t, bud.size, int64(len(b))*9/10)
}

// TestEncodingJSONTexts pins the encoding/json error texts goJSONCost
// reproduces. If a Go release (or GOEXPERIMENT=jsonv2) changes them, this
// fails, and jsongo.go must follow.
func TestEncodingJSONTexts(t *testing.T) {
	cyc := &jsCycle{}
	cyc.Self = cyc
	for _, c := range []struct {
		v    any
		want string
	}{
		{make(chan int), "json: unsupported type: chan int"},
		{math.NaN(), "json: unsupported value: NaN"},
		{json.Number("x"), `json: invalid number literal "x"`},
		{cyc, "json: unsupported value: encountered a cycle via *libhandlebars.jsCycle"},
		{map[jsFailKey]int{1: 1}, `json: encoding error for type "map[libhandlebars.jsFailKey]int": "key 1 fails"`},
		{struct{ R json.RawMessage }{R: []byte("{")}, "json: error calling MarshalJSON for type json.RawMessage: unexpected end of JSON input"},
		{struct{ B jsBadText }{}, "json: error calling MarshalText for type libhandlebars.jsBadText: no text"},
	} {
		_, err := json.Marshal(c.v)
		require.EqualError(t, err, c.want, "encoding/json's text changed: update jsongo.go")
		// The callers' flow: the walk fails first, or json.Marshal does
		// (a failing method with nothing failing after it).
		werr := goJSONCost(&goBudget{max: 1 << 40}, reflect.ValueOf(c.v), 0)
		if werr == nil {
			_, werr = json.Marshal(c.v)
		}
		require.EqualError(t, werr, c.want)
	}
}

// TestJSONQuotedStringLen: the ",string" length is what encoding/json
// writes, for every byte.
func TestJSONQuotedStringLen(t *testing.T) {
	ins := []string{"", "plain", `a"b\c`, "\b\f\n\r\t", "<>&", "  ", "é漢🙂", "\xff", "x\x00y"}
	for b := range 256 {
		ins = append(ins, string([]byte{byte(b)}), "a"+string([]byte{byte(b)})+"é")
	}
	for _, s := range ins {
		got, err := json.Marshal(struct {
			S string `json:",string"`
		}{s})
		require.NoError(t, err)
		require.Equal(t, int64(len(got)-len(`{"S":}`)), jsonQuotedStringLen(s), "%q", s)
	}
}

type jsLoop struct{ Self *jsLoop }

// TestGoJSONCostHopsChargedOnError: pointer and interface hops are charged
// before they are followed, so a walk that fails below them (a cycle) has
// still paid for them.
func TestGoJSONCostHopsChargedOnError(t *testing.T) {
	loop := &jsLoop{}
	loop.Self = loop
	const n = 10_000
	var x any = loop
	for range n {
		y := x
		x = &y // a pointer hop, then an interface hop
	}
	bud := &goBudget{max: 1 << 40}
	err := goJSONCost(bud, reflect.ValueOf(x), 0)
	require.ErrorContains(t, err, "encountered a cycle")
	require.GreaterOrEqual(t, bud.used, int64(2*n*hopCost))
}

// TestGoJSONCostMapKeysOrderFree: a map's key charges are applied together,
// so a failing budget is spent the same way whatever Go's map order.
func TestGoJSONCostMapKeysOrderFree(t *testing.T) {
	m := map[string]int{"a": 1, strings.Repeat("b", 64<<10): 2, strings.Repeat("c", 1<<20): 3}
	var used int64
	for i := range 50 {
		bud := &goBudget{max: 1000}
		err := goJSONCost(bud, reflect.ValueOf(m), 0)
		var herr *hbs.Error
		require.ErrorAs(t, err, &herr)
		require.Equal(t, hbs.KindLimit, herr.Kind)
		if i == 0 {
			used = bud.used
		}
		require.Equal(t, used, bud.used, "run %d", i)
	}
}

// TestGoJSONCostLongFieldName: converting a struct charges its fields'
// names by length, on every conversion (the field list is cached).
func TestGoJSONCostLongFieldName(t *testing.T) {
	name := "A" + strings.Repeat("a", 1<<20)
	typ := reflect.StructOf([]reflect.StructField{{Name: name, Type: reflect.TypeFor[int]()}})
	v := reflect.New(typ).Elem()
	for range 2 {
		bud := &goBudget{max: 1 << 40}
		require.NoError(t, goJSONCost(bud, v, 0))
		require.GreaterOrEqual(t, bud.used, int64(len(name)/16))
	}
}
