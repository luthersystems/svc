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
		"raw messages": struct {
			A json.RawMessage
			B *json.RawMessage
			C any
			D json.RawMessage
		}{A: json.RawMessage(" [1, 2] "), C: json.RawMessage(`"x"`)},
		"bad pointer RawMessage": struct {
			B *json.RawMessage
		}{B: func() *json.RawMessage { r := json.RawMessage(`[1,`); return &r }()},
		"bad RawMessage in interface":           []any{1, json.RawMessage(`1 2`)},
		"addressable bad RawMessage":            []json.RawMessage{json.RawMessage("x")},
		"addressable bad field":                 &struct{ A json.RawMessage }{json.RawMessage("1 2")},
		"addressable empty RawMessage":          []json.RawMessage{{}},
		"addressable deep RawMessage":           []json.RawMessage{json.RawMessage(strings.Repeat("[", 10001) + strings.Repeat("]", 10001))},
		"addressable failing value marshaler":   []any{[]jsErrM{{}}, func() {}},
		"addressable failing pointer marshaler": []any{[]jsPtrErrM{{}}, func() {}},
		"addressable bad-output marshaler":      []any{[]jsBadOutM{{}}, func() {}},
		"embedded RawMessage":                   []any{[]jsEmbedRaw{{json.RawMessage("{")}}, func() {}},
		"addressable failing text":              []any{[]jsPtrErrText{{}}, func() {}},
		"three-type cycle":                      ca,
		"cycle after prefix":                    map[string]any{"x": []any{[]any{ca.B}}},
		"unexported and -":                      jsHidden{Ok: 1},
		"embedded conflicts":                    jsEmbed{},
		"text keys":                             map[jsText]int{{"a"}: 1, {"b"}: 2},
		"int keys":                              map[int]string{3: "c", 1: "a", 20: "b"},
		"bad key type":                          map[[2]int]int{{1, 2}: 3},
		"NaN":                                   map[string]any{"a": 1, "b": []any{2.0, math.NaN()}},
		"Inf float32":                           []float32{float32(math.Inf(-1))},
		"number ok":                             json.Number("1.5e3"),
		"number bad":                            map[string]any{"n": json.Number("abc")},
		"chan":                                  map[string]any{"z": 1, "a": make(chan int)},
		"func in slice":                         []any{1, func() {}},
		"complex":                               struct{ C complex64 }{1},
		"omitempty chan":                        jsHolder{},
		"addressable text":                      &jsHolder{V: jsPtrText{"v"}, PV: &jsPtrText{"pv"}},
		"unaddressable text":                    jsHolder{V: jsPtrText{"v"}},
		"deep":                                  deep,
		"dag":                                   dag,
		"bytes":                                 map[string]any{"b": []byte("hello"), "n": []byte(nil)},
		"nil things":                            map[string]any{"m": map[string]int(nil), "s": []int(nil), "p": (*int)(nil), "i": nil},
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

type (
	jsZ0  struct{}
	jsZ1  struct{ A, B jsZ0 }
	jsZ2  struct{ A, B jsZ1 }
	jsZ3  struct{ A, B jsZ2 }
	jsZ4  struct{ A, B jsZ3 }
	jsZ5  struct{ A, B jsZ4 }
	jsZ6  struct{ A, B jsZ5 }
	jsZ7  struct{ A, B jsZ6 }
	jsZ8  struct{ A, B jsZ7 }
	jsZ9  struct{ A, B jsZ8 }
	jsZ10 struct{ A, B jsZ9 }
	jsZ11 struct{ A, B jsZ10 }
	jsZ12 struct{ A, B jsZ11 }
	jsZ13 struct{ A, B jsZ12 }
	jsZ14 struct{ A, B jsZ13 }
	jsZ15 struct{ A, B jsZ14 }
	jsZ16 struct{ A, B jsZ15 }
	jsZ17 struct{ A, B jsZ16 }
	jsZ18 struct{ A, B jsZ17 }
	jsZ19 struct{ A, B jsZ18 }
	jsZ20 struct{ A, B jsZ19 }
	jsZ21 struct{ A, B jsZ20 }
	jsZ22 struct{ A, B jsZ21 }
	jsZ23 struct{ A, B jsZ22 }
)

// TestZeroAnalysisShared: an omitzero field's type is analysed once per
// type, not once per path to it (2^24 paths here), and the analysis is
// charged by the types and fields it visits, on every walk.
func TestZeroAnalysisShared(t *testing.T) {
	v := struct {
		X jsZ23 `json:",omitzero"`
	}{}
	start := time.Now()
	for range 2 {
		bud := &goBudget{max: 1 << 40}
		require.NoError(t, goJSONCost(bud, reflect.ValueOf(v), 0))
		require.GreaterOrEqual(t, bud.used, int64(23*3+1), "24 types, all but Z0 with 2 fields")
	}
	require.Less(t, time.Since(start), time.Second, "linear in the types, not the paths")
	z := zeroAnalysis(reflect.TypeFor[jsZ23]())
	require.True(t, z.plain)
	require.Equal(t, int64(23*3+1), z.work)
	require.Equal(t, int64(1<<24-1), z.elems)
}

// TestRawMessageCharged: a json.RawMessage's bytes are charged and checked
// by the walk, before encoding/json checks them: invalid ones fail with
// encoding/json's error, and valid ones before a later failure are paid
// for too.
func TestRawMessageCharged(t *testing.T) {
	big := strings.Repeat("x", 4<<20)
	bad := struct{ A json.RawMessage }{json.RawMessage(`"` + big)}
	_, merr := json.Marshal(bad)
	require.Error(t, merr)
	bud := &goBudget{max: 1 << 40}
	require.EqualError(t, goJSONCost(bud, reflect.ValueOf(bad), 0), merr.Error())
	require.GreaterOrEqual(t, bud.used, int64(2*len(big)/16))

	thenNaN := struct {
		A json.RawMessage
		F float64
	}{json.RawMessage(`"` + big + `"`), math.NaN()}
	_, merr = json.Marshal(thenNaN)
	require.Error(t, merr)
	bud = &goBudget{max: 1 << 40}
	require.EqualError(t, goJSONCost(bud, reflect.ValueOf(thenNaN), 0), merr.Error())
	require.GreaterOrEqual(t, bud.used, int64(2*len(big)/16))

	bud = &goBudget{max: 1000}
	var herr *hbs.Error
	require.ErrorAs(t, goJSONCost(bud, reflect.ValueOf(bad), 0), &herr)
	require.Equal(t, hbs.KindLimit, herr.Kind, "over budget before checking")
}

// TestEscapedFieldNameSized: a field's name is sized as encoding/json
// writes it, HTML-escaped (each & as \u0026), and its cold field list is
// charged at that length.
func TestEscapedFieldNameSized(t *testing.T) {
	name := strings.Repeat("&", 1<<20)
	typ := reflect.StructOf([]reflect.StructField{{Name: "A", Type: reflect.TypeFor[int](), Tag: reflect.StructTag(`json:"` + name + `" cold:"` + t.Name() + `"`)}})
	v := reflect.New(typ).Elem()
	b, err := json.Marshal(v.Interface())
	require.NoError(t, err)
	bud := &goBudget{max: 1 << 40}
	require.NoError(t, goJSONCost(bud, v, 0))
	require.LessOrEqual(t, bud.size, int64(len(b)))
	require.Greater(t, bud.size, int64(5*len(name)), "the name at its escaped length")
	require.GreaterOrEqual(t, bud.used, int64(6*len(name)/16), "the cold field list at the escaped length")
}

// TestGoJSONCostMemoContainerDepth: with a container depth bound, a shared
// subtree's memo entry is keyed on the container depth it starts at (fixed
// arrays and structs count, pointers do not), so the bound holds whatever
// the field order: here p is reached at depth 1 and under 900 arrays.
func TestGoJSONCostMemoContainerDepth(t *testing.T) {
	var inner any = 1
	for range 200 {
		inner = []any{inner}
	}
	p := &inner
	arr := reflect.TypeFor[*any]()
	for range 900 {
		arr = reflect.ArrayOf(1, arr)
	}
	nested := reflect.New(arr).Elem()
	for x := nested; ; x = x.Index(0) {
		if x.Kind() != reflect.Array {
			x.Set(reflect.ValueOf(p))
			break
		}
	}
	for _, directFirst := range []bool{true, false} {
		direct := reflect.StructField{Name: "Direct", Type: reflect.TypeFor[*any]()}
		deep := reflect.StructField{Name: "Deep", Type: arr}
		fields := []reflect.StructField{direct, deep}
		if !directFirst {
			fields = []reflect.StructField{deep, direct}
		}
		v := reflect.New(reflect.StructOf(fields)).Elem()
		v.FieldByName("Direct").Set(reflect.ValueOf(p))
		v.FieldByName("Deep").Set(nested)
		err := goJSONCost(&goBudget{max: 1 << 40}, v, jsonGoMaxDepth)
		require.ErrorContains(t, err, "nests deeper than 1024", "direct first %v", directFirst)
	}
}

type jsErrM struct{}

func (jsErrM) MarshalJSON() ([]byte, error) { return nil, errors.New("boom") }

type jsPtrErrM struct{}

func (*jsPtrErrM) MarshalJSON() ([]byte, error) { return nil, errors.New("ptr boom") }

type jsBadOutM struct{}

func (jsBadOutM) MarshalJSON() ([]byte, error) { return []byte("{"), nil }

type jsEmbedRaw struct{ json.RawMessage }

type jsPtrErrText struct{}

func (*jsPtrErrText) MarshalText() ([]byte, error) { return nil, errors.New("text boom") }

// TestRawMessageBehindMarshaler: a RawMessage held in a json.Marshaler
// field or map element is charged and checked as one (encoding/json names
// the static type json.Marshaler in its error); and []byte is charged for
// its base64 encoding.
func TestRawMessageBehindMarshaler(t *testing.T) {
	raw := json.RawMessage(`"` + strings.Repeat("a", 4<<20))
	for name, v := range map[string]any{
		"field": struct{ R json.Marshaler }{raw},
		"map":   map[string]json.Marshaler{"r": raw},
		"ptr":   struct{ R json.Marshaler }{&raw},
	} {
		_, merr := json.Marshal(v)
		require.Error(t, merr, name)
		bud := &goBudget{max: 1 << 40}
		require.EqualError(t, goJSONCost(bud, reflect.ValueOf(v), 0), merr.Error(), name)
		require.GreaterOrEqual(t, bud.used, int64(2*len(raw)/16), name)
	}

	b := map[string]any{"b": make([]byte, 8<<20)}
	out, err := json.Marshal(b)
	require.NoError(t, err)
	bud := &goBudget{max: 1 << 40}
	require.NoError(t, goJSONCost(bud, reflect.ValueOf(b), 0))
	require.GreaterOrEqual(t, bud.used, int64(len(out)/32), "base64 charged by its output")
}

type jsEmbRawPtr struct{ *json.RawMessage }

type jsEmbRawDeep struct{ jsEmbedRaw }

type jsEmbRawOwn struct{ json.RawMessage }

func (jsEmbRawOwn) MarshalJSON() ([]byte, error) { return []byte(`"own"`), nil }

// TestRawMessageEmbedded: a RawMessage a struct reaches by embedding (by
// value, by pointer, two levels down, behind an embedded or a named
// json.Marshaler, in a type built by reflect.StructOf) is charged for
// encoding/json's check of its bytes before it runs, whatever the build;
// encoding/json reports the error. A type declaring its own MarshalJSON
// is charged too, and still encodes.
func TestRawMessageEmbedded(t *testing.T) {
	raw := json.RawMessage(`"` + strings.Repeat("a", 4<<20))
	structOf := reflect.New(reflect.StructOf([]reflect.StructField{{Name: "RawMessage", Type: rawMessageType, Anonymous: true}})).Elem()
	structOf.Field(0).Set(reflect.ValueOf(raw))
	for name, v := range map[string]any{
		"value":              jsEmbedRaw{raw},
		"pointer":            jsEmbRawPtr{&raw},
		"outer pointer":      &jsEmbedRaw{raw},
		"two levels":         jsEmbRawDeep{jsEmbedRaw{raw}},
		"marshaler field":    struct{ R json.Marshaler }{jsEmbedRaw{raw}},
		"addressable":        []jsEmbedRaw{{raw}},
		"embedded marshaler": struct{ json.Marshaler }{raw},
		"embedded pointer":   struct{ json.Marshaler }{&raw},
		"embedded nested":    struct{ json.Marshaler }{struct{ json.Marshaler }{raw}},
		"embedded embedding": struct{ json.Marshaler }{jsEmbedRaw{raw}},
		"StructOf":           structOf.Interface(),
	} {
		_, merr := json.Marshal(v)
		require.Error(t, merr, name)
		bud := &goBudget{max: 1 << 40}
		if err := goJSONCost(bud, reflect.ValueOf(v), 0); err != nil {
			require.EqualError(t, err, merr.Error(), name)
		}
		require.GreaterOrEqual(t, bud.used, int64(2*len(raw)/16), name)
	}
	own := jsEmbRawOwn{raw}
	_, err := json.Marshal(own)
	require.NoError(t, err)
	bud := &goBudget{max: 1 << 40}
	require.NoError(t, goJSONCost(bud, reflect.ValueOf(own), 0), "its own method encodes")
	require.GreaterOrEqual(t, bud.used, int64(2*len(raw)/16), "and is charged all the same")
	_, _, found := embeddedRaw(reflect.ValueOf(jsEmbRawPtr{}))
	require.Equal(t, embedNone, found, "a nil embedded pointer is left to encoding/json")
}

type jsPrecA struct{ json.RawMessage }

// jsPrecB has MarshalJSON from its own RawMessage (depth 1), which shadows
// jsPrecA's (depth 2).
type jsPrecB struct {
	jsPrecA
	json.RawMessage
}

// TestRawMessageSelectorRules: the RawMessage whose MarshalJSON Go selects
// (the shallowest) is the one charged; a struct with many fields is
// charged for the field list the search looks at.
func TestRawMessageSelectorRules(t *testing.T) {
	raw := json.RawMessage(`"` + strings.Repeat("a", 1<<20))
	v := jsPrecB{jsPrecA{json.RawMessage(`1`)}, raw}
	_, merr := json.Marshal(v)
	require.Error(t, merr)
	bud := &goBudget{max: 1 << 40}
	if err := goJSONCost(bud, reflect.ValueOf(v), 0); err != nil {
		require.EqualError(t, err, merr.Error())
	}
	require.GreaterOrEqual(t, bud.used, int64(2*len(raw)/16), "the depth-1 RawMessage's bytes")

	fields := []reflect.StructField{{Name: "RawMessage", Type: rawMessageType, Anonymous: true}}
	for i := range 1000 {
		fields = append(fields, reflect.StructField{Name: "F" + strconv.Itoa(i), Type: reflect.TypeFor[int]()})
	}
	wide := reflect.New(reflect.StructOf(fields)).Elem()
	wide.Field(0).Set(reflect.ValueOf(json.RawMessage("x")))
	bud = &goBudget{max: 1 << 40}
	_ = goJSONCost(bud, wide, 0)
	require.GreaterOrEqual(t, bud.used, int64(1000/16), "the field list looked at is charged")
}

// jsSelf embeds an interface that can hold itself: encoding/json would
// recurse through its MarshalJSON until the stack overflows.
type jsSelf struct{ json.Marshaler }

// TestRawMessageSearchFailsClosed: a RawMessage reached past the search's
// bounds, and a value embedding itself through an interface, fail with a
// limit error before encoding/json runs, rather than going uncharged (or
// crashing); a wide struct is charged for its field list, not refused.
func TestRawMessageSearchFailsClosed(t *testing.T) {
	raw := json.RawMessage(`"` + strings.Repeat("a", 1<<20))
	bud := &goBudget{max: 1 << 40}
	_ = goJSONCost(bud, reflect.ValueOf(zlAt(60, raw)), 0)
	require.GreaterOrEqual(t, bud.used, int64(2*len(raw)/16), "61 levels down: found")
	require.EqualError(t, goJSONCost(&goBudget{max: 1 << 40}, reflect.ValueOf(zlAt(70, raw)), 0), errEmbedDeep.Error(), "71 levels down: past the bound")

	self := &jsSelf{}
	self.Marshaler = self
	require.EqualError(t, goJSONCost(&goBudget{max: 1 << 40}, reflect.ValueOf(self), 0), errEmbedDeep.Error())
	tpl, err := Parse(`{{x}}`)
	require.NoError(t, err)
	_, err = RenderWith(tpl, map[string]any{"s": self}, WithJSONContext())
	require.ErrorContains(t, err, errEmbedDeep.Error())

	fields := []reflect.StructField{{Name: "RawMessage", Type: rawMessageType, Anonymous: true}}
	for i := range 17_000 {
		fields = append(fields, reflect.StructField{Name: "F" + strconv.Itoa(i), Type: reflect.TypeFor[int]()})
	}
	wide := reflect.New(reflect.StructOf(fields)).Elem()
	wide.Field(0).Set(reflect.ValueOf(raw))
	bud = &goBudget{max: 1 << 40}
	_ = goJSONCost(bud, wide, 0)
	require.GreaterOrEqual(t, bud.used, int64(2*len(raw)/16), "17,000 fields: found and charged")
}
