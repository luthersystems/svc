// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/svc/libhandlebars"
	"github.com/luthersystems/svc/libhandlebars/hbs"
	"github.com/luthersystems/svc/libhandlebars/internal/hbref"
	raymondref "github.com/luthersystems/svc/libhandlebars/internal/raymondref"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type goInner struct {
	V  int
	W  float32
	xs []int //nolint:unused // unexported: invisible to templates
}

type GoEmbedded struct {
	Promoted string
	Shadowed int
}

type goStatus string

type goCount int32

type goCtx struct {
	GoEmbedded
	Name     string
	Tagged   string `handlebars:"tagged"`
	FirstAid uint16
	Shadowed string
	Inner    goInner
	PInner   *goInner
	NilInner *goInner
	Items    []goInner
	secret   string //nolint:unused // unexported: invisible to templates
}

// goTemplates exercise every way a template observes a Go value's type:
// printing, truthiness, includeZero, helpers that switch on the type,
// iteration order and keys, struct field and tag lookup, and the type names
// in helper errors.
var goTemplates = []string{
	`{{n}}|{{z}}|{{f}}|{{f32}}|{{i8}}|{{i16}}|{{i32}}|{{i64}}|{{u}}|{{u8}}|{{u16}}|{{u32}}|{{u64}}|{{b}}|{{s}}`,
	`{{to-str n}}|{{to-str z}}|{{to-str f}}|{{to-str i64}}|{{to-str i8}}|{{to-str f32}}|{{to-str u}}|{{to-str s}}`,
	`{{#if n includeZero=true}}y{{else}}n{{/if}}{{#if z includeZero=true}}y{{else}}n{{/if}}{{#if fz includeZero=true}}y{{else}}n{{/if}}{{#if i8z includeZero=true}}y{{else}}n{{/if}}`,
	`{{#if z}}y{{else}}n{{/if}}{{#if u}}y{{else}}n{{/if}}{{#if fz}}y{{else}}n{{/if}}{{#if empty}}y{{else}}n{{/if}}{{#if nilp}}y{{else}}n{{/if}}`,
	`{{to-int n}}|{{to-int f}}|{{to-int i8}}|{{to-int i64}}|{{to-int s}}`,
	`{{times n 2}}|{{times f 2}}|{{div n 2}}|{{mod n 2}}|{{times i32 3}}`,
	`{{gt n 1}}{{lt f 1}}{{gte i8 1}}{{lte u 1}}`,
	`{{eq n "3"}}{{eq f "2.5"}}{{eq u8 "7"}}{{eq b "true"}}`,
	`{{prettyp-num-en n}}|{{prettyp-num-en f}}|{{prettyp-num-en big}}`,
	`{{round-to-nth f 2}}|{{round-to-nth n 1}}`,
	`{{date-add-months d n}}|{{date-add-months d i8}}`,
	`{{plus a=n}}|{{plus a=f}}|{{minus n a=i8}}|{{minus f a=n}}`,
	`{{len xs}}`,
	`{{len strs}}`,
	`{{#each xs}}{{@index}}:{{this}}:{{to-str this}};{{/each}}`,
	`{{#each strs}}{{@first}}{{this}}{{@last}};{{/each}}{{strs}}|{{xs}}|{{xs.[1]}}|{{arr.[2]}}`,
	`{{#each m}}{{@key}}={{this}}/{{to-str this}};{{/each}}{{m.a}}`,
	`{{#each mi}}{{@key}}={{to-str this}};{{/each}}{{mi.b}}`,
	`{{#each st}}{{@index}}{{@key}}={{this}};{{/each}}`,
	`{{st.name}}|{{st.Name}}|{{st.tagged}}|{{st.Tagged}}|{{st.firstAid}}|{{st.FirstAid}}|{{st.promoted}}|{{st.shadowed}}|{{st.secret}}|{{st.missing}}`,
	`{{st.inner.v}}|{{st.inner.w}}|{{st.pInner.v}}|{{st.nilInner.v}}|{{st.items.[1].v}}|{{st.inner.xs}}`,
	`{{st}}|{{st.inner}}|{{#if st}}t{{/if}}|{{#if st.nilInner}}t{{else}}f{{/if}}|{{#with st}}{{name}}{{/with}}`,
	`{{#each st.items}}{{v}}{{/each}}|{{#each st.inner}}{{@key}}{{/each}}|{{#each empty}}x{{else}}none{{/each}}`,
	`{{named}}|{{to-str named}}|{{eq named "ok"}}|{{#if named}}t{{/if}}|{{#if emptyNamed}}t{{else}}f{{/if}}`,
	`{{cnt}}|{{to-str cnt}}|{{times cnt 2}}|{{#if cnt}}t{{/if}}`,
	`{{nk}}|{{#if nk}}t{{/if}}|{{#each nk}}x{{/each}}|{{nk.[1]}}`,
	`{{ptr}}|{{to-str ptr}}|{{nilp}}|{{pp.v}}`,
	`{{select from=xs where="a=b"}}`,
	`{{#select from=st.items where="v=1"}}x{{/select}}`,
	`{{len m}}`,
	`{{len st}}`,
	`{{date-add-months d s}}`,
	`{{not n}}`,
	`{{prettyp-num-en m}}`,
	`{{prettyp-num-en st.inner}}`,
	`{{prettyp-num-en nested}}`,
	`{{to-str nilp}}|{{eq nilp ""}}|{{nilm}}|{{nils}}|{{nila}}|{{#each nilm}}x{{else}}e{{/each}}|{{#each nils}}x{{else}}e{{/each}}`,
	`{{len nils}}`,
	`{{len nila}}`,
	`{{len arr}}`,
	`{{in-string-array haystack=strs needle="a"}}`,
	`{{in-string-array haystack=anys needle="a"}}|{{len anys}}|{{anys}}`,
	`{{#each anys as |x i|}}{{i}}{{x}}{{/each}}|{{#strs}}{{this}}{{/strs}}|{{#st}}{{name}}{{/st}}|{{#nilp}}x{{else}}np{{/nilp}}`,
	`{{st.items.v}}|{{#each st.items}}{{../n}}{{/each}}|{{nested.[0].[1]}}|{{nested}}`,
	`{{global "g" key="k" val=n}}{{global "g" key="k"}}`,
	`{{global "g" key="k" val=st}}`,
	`{{prettyp-num-en strs}}`,
}

func goContext() map[string]any {
	n, ptr := 3, 9
	inner := goInner{V: 1, W: 0.5}
	return map[string]any{
		"n": n, "z": 0, "f": 2.5, "fz": 0.0, "f32": float32(1.25), "big": 1234567,
		"i8": int8(-7), "i8z": int8(0), "i16": int16(300), "i32": int32(-70000), "i64": int64(1) << 40,
		"u": uint(5), "u8": uint8(7), "u16": uint16(65535), "u32": uint32(1) << 31, "u64": uint64(1) << 63,
		"b": true, "s": "12", "d": "2020-01-31", "empty": []int{},
		"xs":    []int{10, 20, 30},
		"strs":  []string{"a", "b"},
		"arr":   [3]int8{1, 2, 3},
		"m":     map[string]int{"b": 2, "a": 1, "c": 0},
		"mi":    map[string]any{"a": 1, "b": 2.0, "c": "x"},
		"nk":    map[int]string{1: "one"},
		"named": goStatus("ok"), "emptyNamed": goStatus(""),
		"cnt":    goCount(4),
		"ptr":    &ptr,
		"nilp":   (*int)(nil),
		"pp":     &inner,
		"nilm":   map[string]int(nil),
		"nils":   []int(nil),
		"nila":   []any(nil),
		"anys":   []any{"a", 1, 2.5},
		"nested": [][]int{{1, 2}, {3}},
		"st": goCtx{
			GoEmbedded: GoEmbedded{Promoted: "pro", Shadowed: 1},
			Name:       "Ann", Tagged: "tag", FirstAid: 2, Shadowed: "outer",
			Inner: inner, PInner: &goInner{V: 2},
			Items: []goInner{{V: 1}, {V: 2}},
		},
	}
}

func refRaw(err error) string {
	var e *hbref.Error
	if errors.As(err, &e) {
		return string(e.Stage) + ": " + e.Raw
	}
	return "other: " + err.Error()
}

func gotRaw(err error) string {
	var e *hbs.Error
	if errors.As(err, &e) && e.Kind == hbs.KindRender {
		return "render: " + e.Msg
	}
	return "other: " + err.Error()
}

// checkGo renders tplStr with the Go value ctx through raymond (svc's
// old Go API) and through RenderWith with WithGoContext: the output and
// error text must match. Where raymond panicked (it crashed the caller),
// any render error will do.
func checkGo(t *testing.T, tplStr string, ctx any) {
	t.Helper()
	tpl, err := libhandlebars.Parse(tplStr)
	require.NoError(t, err, tplStr)
	want, werr := hbref.RenderGo(tplStr, ctx)
	got, gerr := libhandlebars.RenderWith(tpl, ctx, libhandlebars.WithGoContext())
	if werr == nil {
		if assert.NoError(t, gerr, "native %s", tplStr) {
			assert.Equal(t, want, got, "native %s", tplStr)
		}
		return
	}
	if !assert.Error(t, gerr, "native %s: raymond failed with %s, got %q", tplStr, refRaw(werr), got) { //nolint:testifylint // report every template
		return
	}
	var e *hbref.Error
	if errors.As(werr, &e) && e.Stage == hbref.StagePanic {
		return
	}
	assert.Equal(t, refRaw(werr), gotRaw(gerr), "native %s", tplStr)
}

// checkJSON renders tplStr with ctx through JSON on both sides.
func checkJSON(t *testing.T, tplStr string, ctx any) {
	t.Helper()
	tpl, err := libhandlebars.Parse(tplStr)
	require.NoError(t, err, tplStr)
	jsonCtx, err := json.Marshal(ctx)
	require.NoError(t, err)
	want, werr := hbref.RenderJSON(tplStr, jsonCtx)
	got, gerr := libhandlebars.RenderWith(tpl, ctx, libhandlebars.WithJSONContext())
	if werr == nil {
		if assert.NoError(t, gerr, "json %s", tplStr) {
			assert.Equal(t, want, got, "json %s", tplStr)
		}
		return
	}
	if assert.Error(t, gerr, "json %s: got %q", tplStr, got) {
		assert.Equal(t, refRaw(werr), gotRaw(gerr), "json %s", tplStr)
	}
}

// TestGoContextDifferential renders goTemplates with Go-typed contexts
// through raymond and through Render, in the default (Go) mode with the Go
// value itself, and in JSON mode with the context raymond sees after
// json.Marshal and json.Unmarshal.
func TestGoContextDifferential(t *testing.T) {
	ctx := goContext()
	for _, tplStr := range goTemplates {
		checkGo(t, tplStr, ctx)
		checkJSON(t, tplStr, ctx)
	}
}

type goNode struct {
	Name string
	Next *goNode
}

type goMethods struct{ Name string }

func (goMethods) Greeting() string { return "hi" }

func (*goMethods) PtrOnly() string { return "p" }

type goHolder struct{ Inner goMethods }

// TestGoContextCycle: a cyclic Go value renders as raymond rendered it;
// only what the template touches is read.
func TestGoContextCycle(t *testing.T) {
	a := &goNode{Name: "a"}
	a.Next = &goNode{Name: "b", Next: a}
	checkGo(t, `{{name}}{{next.name}}{{next.next.name}}{{next.next.next.name}}{{#with next}}{{#with next}}{{name}}{{/with}}{{/with}}`, a)
}

// TestGoContextPointerLoop (review B1): a pointer cycle through interfaces
// renders where the template does not touch it, as raymond did, and is a
// limit error, not a hang, where it does (raymond looped forever).
func TestGoContextPointerLoop(t *testing.T) {
	var a any
	a = &a
	var b, c any
	b, c = &c, &b
	ctx := map[string]any{"a": a, "b": b}
	// raymond loops forever on any lookup that reaches a or b.
	checkGo(t, `x{{this.x}}{{#each this}}{{/each}}`, map[string]any{"x": 1})
	tpl, err := libhandlebars.Parse(`{{a}}`)
	require.NoError(t, err)
	for _, s := range []string{`{{a}}`, `{{b}}`, `{{a.x}}`, `{{#each b}}{{/each}}`} {
		tpl, err = libhandlebars.Parse(s)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { _, err := libhandlebars.Render(tpl, ctx); done <- err }()
		select {
		case err := <-done:
			var herr *hbs.Error
			if err != nil {
				require.ErrorAs(t, err, &herr, s)
				require.Equal(t, hbs.KindLimit, herr.Kind, s)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: render did not finish", s)
		}
	}
}

// TestGoContextInterfaceKeys (review B2): maps whose key type a string is
// assignable to (map[any]any from YAML, say) are looked up by string key,
// and #each visits their string keys sorted, @last counting every key.
func TestGoContextInterfaceKeys(t *testing.T) {
	yaml := map[any]any{"name": "Ann", "items": []any{map[any]any{"v": 1}}, 7: "seven"}
	mi := map[any]any{"a": 1, 2: "x", "b": nil}
	ctx := map[string]any{"yaml": yaml, "mi": mi, "named": map[goStatus]int{"k": 1}}
	for _, s := range []string{
		`{{yaml.name}}{{#each yaml.items}}{{v}}{{/each}}`,
		`{{#each mi}}{{@key}}{{@last}}{{@index}}{{/each}}`,
		`{{#each yaml}}{{@key}}={{this}};{{/each}}`,
		`{{named.k}}|{{#each named}}x{{/each}}|{{#if named}}t{{/if}}|{{named}}`,
		`{{mi.a}}{{mi.[2]}}{{yaml}}`,
		`{{len yaml}}`,
	} {
		checkGo(t, s, ctx)
	}
}

type (
	goStatusInt int
	goAmount    int64
	goF         float64
	goU         uint8
	goF32       float32
	goB         bool
	goList      []any
)

// TestGoContextNamedTypes (review B3): named types reach helpers as
// themselves, so svc's type switches miss them as they did; they print and
// test by kind.
func TestGoContextNamedTypes(t *testing.T) {
	ctx := map[string]any{
		"a": goStatusInt(4), "z": goStatusInt(0), "amt": goAmount(7), "f": goF(2.5), "u": goU(3),
		"f32": goF32(1.5), "b": goB(true), "nb": goB(false), "l": goList{"x", 1}, "d": "2020-01-31",
		"up": uintptr(3),
	}
	for _, s := range []string{
		`{{a}}|{{z}}|{{amt}}|{{f}}|{{u}}|{{f32}}|{{b}}|{{nb}}|{{l}}|{{up}}`,
		`{{to-str a}}|{{to-str amt}}|{{to-str f}}|{{to-str up}}`,
		`{{#if z includeZero=true}}y{{else}}n{{/if}}{{#if z}}y{{else}}n{{/if}}{{#if nb}}y{{else}}n{{/if}}{{#if up}}y{{/if}}`,
		`{{plus a=a b=1}}|{{to-int a}}|{{times f 2}}|{{gt a 1}}|{{eq a "4"}}`,
		`{{prettyp-num-en a}}`,
		`{{prettyp-num-en f}}`,
		`{{date-add-months d a}}`,
		`{{len l}}|{{#each l}}{{this}}{{/each}}|{{not nb}}`,
		`{{round-to-nth f 1}}`,
		`{{mod a 3}}`,
	} {
		checkGo(t, s, ctx)
	}
}

// TestGoContextUnsupported: raymond called methods and funcs; the render
// fails where a lookup reaches one and renders everything else, with
// raymond's addressability rule for pointer methods.
func TestGoContextUnsupported(t *testing.T) {
	ctx := map[string]any{
		"m":  &goMethods{Name: "n"},
		"mv": goMethods{Name: "v"},
		"h":  &goHolder{Inner: goMethods{Name: "i"}},
		"f":  func() string { return "called" },
		"ch": make(chan int),
		"c":  complex(1, 2),
	}
	for tplStr, want := range map[string]string{
		`{{m.name}}{{mv.name}}{{h.inner.name}}{{c}}{{#if ch}}t{{/if}}`: "nviUNPRINTABLEt",
		`{{m.greeting}}`:      "Go method calls are not supported: *libhandlebars_test.goMethods.Greeting",
		`{{m.ptrOnly}}`:       "Go method calls are not supported: *libhandlebars_test.goMethods.PtrOnly",
		`{{mv.Greeting}}`:     "Go method calls are not supported: libhandlebars_test.goMethods.Greeting",
		`{{h.inner.ptrOnly}}`: "Go method calls are not supported: *libhandlebars_test.goMethods.PtrOnly",
		`{{f}}`:               "Go func values are not supported: f",
		`{{ch}}`:              "Can't print value: chan int",
	} {
		tpl, err := libhandlebars.Parse(tplStr)
		require.NoError(t, err)
		got, err := libhandlebars.Render(tpl, ctx)
		if err == nil {
			require.Equal(t, want, got, tplStr)
			continue
		}
		require.Contains(t, err.Error(), want, tplStr)
	}
	// A pointer method is invisible on a struct raymond could not address.
	checkGo(t, `{{mv.ptrOnly}}|{{mv.name}}`, ctx)
}

type goPayload interface{ isPayload() }

type goText struct{ Text string }

func (*goText) isPayload() {}

type goMsg struct {
	ID      int
	Payload goPayload
}

type goNamedMap map[string]int

func (goNamedMap) Size() int { return 1 }

// TestGoContextInterfaceFields: a value held in an interface type with
// methods (a protobuf oneof) is not looked into by a path, only as a
// block's context, as raymond did; a method of a named map type is found
// (and refused) as raymond found (and called) it.
func TestGoContextInterfaceFields(t *testing.T) {
	ctx := map[string]any{"msg": &goMsg{ID: 1, Payload: &goText{Text: "hi"}}, "nm": goNamedMap{"a": 1}}
	for _, s := range []string{
		`{{msg.id}}|{{msg.payload.text}}|{{#with msg.payload}}{{text}}{{/with}}|{{#if msg.payload}}t{{/if}}`,
		`{{nm.a}}`,
	} {
		checkGo(t, s, ctx)
	}
	tpl, err := libhandlebars.Parse(`{{nm.size}}`)
	require.NoError(t, err)
	_, err = libhandlebars.Render(tpl, ctx)
	require.ErrorContains(t, err, "Go method calls are not supported: libhandlebars_test.goNamedMap.Size", "raymond called it")
}

type goWide struct {
	GoEmbedded
	A1, A2, A3 int
	Name       string
}

// TestGoContextStructCost (review B4): struct lookups use a per-type plan
// and are charged by the type's field count, the same whether the plan was
// cached, and promoted fields resolve as FieldByName does.
func TestGoContextStructCost(t *testing.T) {
	ctx := map[string]any{"w": goWide{GoEmbedded: GoEmbedded{Promoted: "p", Shadowed: 2}, Name: "n"}}
	checkGo(t, `{{w.promoted}}{{w.shadowed}}{{w.name}}{{#each w}}{{@key}}={{this}};{{/each}}`, ctx)
	tpl, err := libhandlebars.Parse(`{{w.promoted}}`)
	require.NoError(t, err)
	m1, m2 := &countMeter{}, &countMeter{}
	_, err = tpl.Render(ctx, hbs.Options{Meter: m1})
	require.NoError(t, err)
	_, err = tpl.Render(ctx, hbs.Options{Meter: m2})
	require.NoError(t, err)
	require.Equal(t, m1.n, m2.n, "a cached plan charges what building it did")
}

// TestGoContextTypes pins N4: a Go int is an int by default, as under
// raymond, and a float64 with WithJSONContext.
func TestGoContextTypes(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{#if n includeZero=true}}yes{{else}}no{{/if}} {{to-str n}} {{to-str m}}`)
	require.NoError(t, err)
	ctx := map[string]any{"n": 0, "m": 3}
	res, err := libhandlebars.Render(tpl, ctx)
	require.NoError(t, err)
	require.Equal(t, "yes 0 3", res)
	res, err = libhandlebars.RenderWith(tpl, ctx, libhandlebars.WithJSONContext())
	require.NoError(t, err)
	require.Equal(t, "no 0.000000 3.000000", res)
}

// TestGoContextSteps: reading a Go value is charged and bounded by the
// render's own MaxSteps (one budget for the whole call).
func TestGoContextSteps(t *testing.T) {
	big := make([]int, 1<<16)
	tpl, err := libhandlebars.Parse(`{{#each big}}{{this}}{{/each}}`)
	require.NoError(t, err)
	_, err = tpl.Render(map[string]any{"big": big}, hbs.Options{Limits: hbs.Limits{MaxSteps: 1 << 14}})
	var herr *hbs.Error
	require.ErrorAs(t, err, &herr)
	require.Equal(t, hbs.KindLimit, herr.Kind)
	var deep any = []int8{1}
	for range 300 {
		deep = []any{deep}
	}
	tpl, err = libhandlebars.Parse(`{{deep}}`)
	require.NoError(t, err)
	_, err = libhandlebars.Render(tpl, map[string]any{"deep": deep})
	require.ErrorAs(t, err, &herr, "printing nests past MaxDepth")
	require.Equal(t, hbs.KindLimit, herr.Kind)
}

// TestGoContextReview covers the Go-path cases from review: steps and
// output do not depend on map order, struct tags and long names are
// charged by length, a named []interface{} type works with len, and %v of
// a map with int keys matches raymond.
func TestGoContextReview(t *testing.T) {
	m := map[string]any{"x": 1, "y": []int{1, 2}, "z": map[string]any{"q": "r"}}
	var nested any = m
	for range 255 {
		nested = []any{nested}
	}
	ctx := map[string]any{"a": m, "b": nested}
	tpl, err := libhandlebars.Parse(`{{#each a}}{{@key}}{{this}}{{/each}}{{#each b}}{{this}}{{/each}}{{a.y}}`)
	require.NoError(t, err)
	var firstOut string
	var firstErr error
	var firstSteps int64
	for i := range 20 {
		cm := &countMeter{}
		out, err := tpl.Render(ctx, hbs.Options{Meter: cm})
		if i == 0 {
			firstOut, firstErr, firstSteps = out, err, cm.n
			continue
		}
		require.Equal(t, firstOut, out)
		require.Equal(t, firstErr, err)
		require.Equal(t, firstSteps, cm.n, "run %d", i)
	}

	tag := reflect.StructTag(`handlebars:"t" big:"` + strings.Repeat("x", 1<<20) + `"`)
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeFor[int](), Tag: tag},
		{Name: "B", Type: reflect.TypeFor[string]()},
	})
	n := reflect.New(typ).Elem()
	n.Field(1).SetString("b")
	long := strings.Repeat("é", 500<<10) // about 1 MB, under the template limit
	for _, c := range []struct {
		tpl  string
		min  int64
		what string
	}{
		{`{{n.b}}`, 1 << 16, "the 1 MiB tag"},
		{`{{m.[` + long + `]}}`, 1 << 17, "the 1 MB name"},
	} {
		tpl, err := libhandlebars.Parse(c.tpl)
		require.NoError(t, err)
		cm := &countMeter{}
		_, err = tpl.Render(map[string]any{"n": n.Interface(), "m": goNamedMap{}}, hbs.Options{Meter: cm})
		if err != nil {
			require.ErrorContains(t, err, "not supported")
		}
		require.GreaterOrEqual(t, cm.n, c.min, c.what)
	}

	checkGo(t, `{{len n}}`, map[string]any{"n": goList{1, 2}})
	checkGo(t, `{{prettyp-num-en n}}`, map[string]any{"n": map[int]string{1: "one"}})
}

type goInner2 struct{ V int }

func (*goInner2) Foo() string { return "foo" }

type goTagged struct {
	Data goInner2 `handlebars:"payload"`
}

type goErrHolder struct{ E error }

// TestGoContextReview2 covers the final review's Go-path cases: Go values
// inside an engine []any print by raymond's element rule, a tag-matched
// field is a copy, the empty name finds the first untagged field, and a
// path into a nil interface whose type has the method fails.
func TestGoContextReview2(t *testing.T) {
	type unexportedTagged struct {
		in struct{ X int } `handlebars:"in"` //nolint:unused // read by the template through its tag
	}
	checkGo(t, `{{s.in.nothing}}`, map[string]any{"s": unexportedTagged{}})
	checkGo(t, `{{s.in.x}}`, map[string]any{"s": unexportedTagged{}})

	n, str := 5, "str"
	ctx := map[string]any{
		"l": []any{&n}, "s": []any{&str}, "nested": []any{[]any{&n}},
		"c": []any{make(chan int)}, "f": []any{func() {}}, "typed": []*int{&n},
		"o":  goTagged{Data: goInner2{V: 1}},
		"po": &goTagged{Data: goInner2{V: 1}},
		"st": struct{ A, B int }{1, 2},
		"so": struct{ In goInner2 }{goInner2{V: 3}},
		"e":  goErrHolder{},
	}
	for _, tpl := range []string{
		`{{l}}|{{s}}|{{nested}}|{{c}}|{{f}}|{{typed}}`,
		`{{#each l}}{{this}}{{/each}}|{{#each typed}}{{this}}{{/each}}|{{eq l "5"}}|{{eq typed "5"}}`,
		`{{o.payload.v}}|{{o.payload.foo}}|{{po.payload.foo}}`,
		`{{st.[]}}|{{so.[]}}|{{so.[].v}}`,
		`{{e.e.error}}`,
		`{{e.e.x}}`,
	} {
		checkGo(t, tpl, ctx)
	}
}

// TestGoContextVDeterministic: %v sizing of a Go map charges the same
// steps and fails the same way whatever Go's map order.
func TestGoContextVDeterministic(t *testing.T) {
	m := map[int]any{100: nil}
	var deep any = "leaf"
	for range 300 {
		deep = []any{deep}
	}
	for i := range 8 {
		m[i] = make([]int, 4096)
	}
	m[100] = deep
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en m}}`)
	require.NoError(t, err)
	for _, maxSteps := range []int64{0, 8192} {
		var firstErr string
		var firstSteps int64
		for i := range 40 {
			cm := &countMeter{}
			_, err := tpl.Render(map[string]any{"m": m}, hbs.Options{Meter: cm, Limits: hbs.Limits{MaxSteps: maxSteps}})
			require.Error(t, err)
			if i == 0 {
				firstErr, firstSteps = err.Error(), cm.n
				continue
			}
			require.Equal(t, firstErr, err.Error(), "MaxSteps %d run %d", maxSteps, i)
			require.Equal(t, firstSteps, cm.n, "MaxSteps %d run %d", maxSteps, i)
		}
	}
}

// TestGoContextNaNKeys: %v of a map with more than one NaN key fails the
// same way on every run, as fmt's order among them follows Go's map order.
func TestGoContextNaNKeys(t *testing.T) {
	two := map[float64]int{math.NaN(): 1, math.NaN(): 2, 1: 3}
	one := map[float64]int{math.NaN(): 1, 1: 3}
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en m}}`)
	require.NoError(t, err)
	for range 20 {
		_, rerr := libhandlebars.Render(tpl, map[string]any{"m": two})
		require.ErrorContains(t, rerr, "more than one NaN key")
	}
	_, err = libhandlebars.Render(tpl, map[string]any{"m": one})
	require.ErrorContains(t, err, "map[NaN:1 1:3]")

	type nanStruct struct{ F float64 }
	nan := math.NaN()
	for _, m := range []any{
		map[nanStruct]int{{nan}: 1, {nan}: 2},
		map[[1]float64]int{{nan}: 1, {nan}: 2},
		map[complex128]int{complex(nan, 0): 1, complex(nan, 0): 2},
		map[any]int{nanStruct{nan}: 1, nanStruct{nan}: 2, "x": 3},
	} {
		for range 20 {
			_, rerr := libhandlebars.Render(tpl, map[string]any{"m": m})
			require.ErrorContains(t, rerr, "more than one NaN key", "%T", m)
		}
	}
}

type countMeter struct{ n int64 }

func (m *countMeter) Charge(n int64) error { m.n += n; return nil }

type stringerWithNaNs struct {
	m map[float64]int // fmt never reaches it: String is called
}

func (stringerWithNaNs) String() string { return "hello" }

// TestGoContextVStringer: %v sizing treats a value fmt prints with its
// String method as opaque, as fmt does not look inside it.
func TestGoContextVStringer(t *testing.T) {
	v := stringerWithNaNs{m: map[float64]int{math.NaN(): 1, math.NaN(): 2}}
	checkGo(t, `{{prettyp-num-en v}}`, map[string]any{"v": v})
	checkGo(t, `{{prettyp-num-en v}}`, map[string]any{"v": []any{v, 1}})
}

// TestGoContextTypeErrorText: a helper's type error naming a Go type with
// megabytes of struct tags is charged by its length before it is built.
func TestGoContextTypeErrorText(t *testing.T) {
	big := reflect.New(reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeFor[int](), Tag: reflect.StructTag(`big:"` + strings.Repeat("x", 4<<20) + `"`)},
	})).Elem().Interface()
	for _, src := range []string{`{{global "n" key=k}}`, `{{select from=k where="x=y"}}`, `{{len k}}`} {
		tpl, err := libhandlebars.Parse(src)
		require.NoError(t, err)
		m := &countMeter{}
		_, err = tpl.Render(map[string]any{"k": big}, hbs.Options{Meter: m})
		require.Error(t, err, src)
		require.GreaterOrEqual(t, m.n, int64(4<<20)/16, "%s: the type name is charged", src)
	}
}

// TestJSONContextNilMarshaler: a nil json.Marshaler interface is written as
// null, as encoding/json writes it, and the error is the later NaN's; a
// panic in the caller's methods is an error, not a crash.
func TestJSONContextNilMarshaler(t *testing.T) {
	tpl, err := libhandlebars.Parse(`x`)
	require.NoError(t, err)
	_, err = libhandlebars.RenderWith(tpl, struct {
		J json.Marshaler
		F float64
	}{F: math.NaN()}, libhandlebars.WithJSONContext())
	require.EqualError(t, err, "json: unsupported value: NaN")
	_, err = libhandlebars.RenderWith(tpl, map[string]any{"p": panicMarshaler{}}, libhandlebars.WithJSONContext())
	require.ErrorContains(t, err, "render panicked")
}

type panicMarshaler struct{}

func (panicMarshaler) MarshalJSON() ([]byte, error) { panic("boom") }

// TestGoContextMapKeyCopies: #each over a Go map whose key type cannot
// hold a string does not copy its keys (raymond iterated none of them).
func TestGoContextMapKeyCopies(t *testing.T) {
	ctx := map[string]any{"a": make([]int, 200), "m": map[[1 << 20]byte]int{{}: 1}}
	tpl, err := libhandlebars.Parse(`{{#each a}}{{#each ../m}}x{{/each}}{{/each}}`)
	require.NoError(t, err)
	var out string
	alloc := allocDuringGo(func() { out, err = libhandlebars.Render(tpl, ctx) })
	require.NoError(t, err)
	require.Empty(t, out)
	require.Less(t, alloc, uint64(16<<20), "keys copied")
	checkGo(t, `{{#each m}}x{{/each}}`, map[string]any{"m": map[[2]byte]int{{}: 1}})
}

// TestJSONContextNumberAlloc: an invalid json.Number is validated without
// copying and its quoted error text sized and charged first.
func TestJSONContextNumberAlloc(t *testing.T) {
	ctx := map[string]any{"n": json.Number("1" + strings.Repeat("\x00", 4<<20))}
	tpl, err := libhandlebars.Parse(`x`)
	require.NoError(t, err)
	_, want := json.Marshal(ctx)
	var gerr error
	alloc := allocDuringGo(func() { _, gerr = libhandlebars.RenderWith(tpl, ctx, libhandlebars.WithJSONContext()) })
	require.EqualError(t, gerr, want.Error())
	require.Less(t, alloc, uint64(48<<20), "the 16 MB text, built at its size and copied once")
}

func allocDuringGo(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

type rnode struct{ I any }

// skey prints as "k0" whatever it holds: fmt calls String, but its key
// sort still compares the whole value.
type skey struct {
	I   any
	Tag int
}

func (skey) String() string { return "k0" }

func rchain(n int, leaf any) any {
	v := leaf
	for range n {
		v = rnode{v}
	}
	return v
}

// TestGoContextMethodKeysDeep: a map key deeper than MaxDepth behind a
// String method (where the size walk stops) is a depth error when fmt
// would sort it, as fmt compares keys all the way down: the same error
// on every run, for NaNs below the depth bound too, and before fmt runs.
func TestGoContextMethodKeysDeep(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en o}}`)
	require.NoError(t, err)
	nanKeys := map[skey]int{{rchain(300, math.NaN()), 0}: 1, {rchain(300, math.NaN()), 0}: 2}
	for range 20 {
		_, err = libhandlebars.Render(tpl, map[string]any{"o": nanKeys})
		require.ErrorContains(t, err, "maximum depth")
	}
	shallowNaNs := map[skey]int{{rchain(5, math.NaN()), 0}: 1, {rchain(5, math.NaN()), 0}: 2}
	_, err = libhandlebars.Render(tpl, map[string]any{"o": shallowNaNs})
	require.ErrorContains(t, err, "more than one NaN key")

	c := rchain(20_000, 1)
	shared := make(map[skey]int, 200)
	for i := range 200 {
		shared[skey{c, i}] = i
	}
	start := time.Now()
	_, err = libhandlebars.Render(tpl, map[string]any{"o": shared})
	require.ErrorContains(t, err, "maximum depth")
	require.Less(t, time.Since(start), time.Second, "fails before fmt sorts")

	one := map[skey]int{{c, 0}: 0} // one key: fmt compares nothing
	_, err = libhandlebars.Render(tpl, map[string]any{"o": one})
	require.ErrorContains(t, err, "map[k0:0]")
}

// TestGoContextMethodKeysNoCrash: two keys sharing a 1.5M-deep chain
// behind a String method fail with the depth error; fmt's sort, which
// compares them all the way down, would overflow the stack.
func TestGoContextMethodKeysNoCrash(t *testing.T) {
	if os.Getenv("HBS_KEYS_CHILD") == "1" {
		c := rchain(1_500_000, 1)
		tpl, err := libhandlebars.Parse(`{{prettyp-num-en o}}`)
		require.NoError(t, err)
		_, err = libhandlebars.Render(tpl, map[string]any{"o": map[skey]int{{c, 0}: 0, {c, 1}: 1}})
		require.ErrorContains(t, err, "maximum depth")
		fmt.Println("KEYS-OK")
		return
	}
	if testing.Short() {
		t.Skip("slow: skipped under -short")
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestGoContextMethodKeysNoCrash$") //nolint:gosec // this test binary
	cmd.Env = append(os.Environ(), "HBS_KEYS_CHILD=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.Contains(t, string(out), "KEYS-OK")
}

// dagKey is a [32]any whose elements all hold the level below: levels deep,
// 32^levels values to walk, though it is small.
func dagKey(levels int) [32]any {
	var k [32]any
	var box any = 1.0
	for range levels {
		for i := range k {
			k[i] = box
		}
		box = k
	}
	return k
}

// TestGoContextKeyWalkBounded: the NaN test of a map key stops with the
// step budget, as its comparison cost does, so a key with more paths than
// MaxSteps (32^5 here) fails the step limit rather than being walked.
// (hbs's TestMapKeyWalksBounded bounds the walk itself, on 32^6.)
func TestGoContextKeyWalkBounded(t *testing.T) {
	if raceEnabled && testing.Short() {
		t.Skip("slow under -race -short")
	}
	m := map[[32]any]int{dagKey(5): 1}
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en o}}`)
	require.NoError(t, err)
	start := time.Now()
	_, err = libhandlebars.Render(tpl, map[string]any{"o": m})
	require.ErrorContains(t, err, "maximum of")
	if !raceEnabled {
		require.Less(t, time.Since(start), 5*time.Second)
	}
}

type zeroSized struct{ F [0]float64 }

// TestGoContextZeroSizedKeyArray: a map key that is a large array of
// zero-size values is walked within the step budget: with MaxSteps 64 the
// render fails at once.
func TestGoContextZeroSizedKeyArray(t *testing.T) {
	kt := reflect.ArrayOf(1<<24, reflect.TypeFor[zeroSized]())
	m := reflect.MakeMap(reflect.MapOf(kt, reflect.TypeFor[int]()))
	m.SetMapIndex(reflect.New(kt).Elem(), reflect.ValueOf(1))
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en m}}`)
	require.NoError(t, err)
	lim := hbs.DefaultLimits()
	lim.MaxSteps = 64
	best := time.Hour
	for range 3 {
		start := time.Now()
		_, err = tpl.Render(map[string]any{"m": m.Interface()}, hbs.Options{Limits: lim})
		require.ErrorContains(t, err, "maximum of 64 steps")
		best = min(best, time.Since(start))
	}
	if !raceEnabled {
		require.Less(t, best, 20*time.Millisecond, "not walked past the budget")
	}
}

// TestGoContextSafeString: raymond's SafeString in a Go context prints as
// its text, unescaped at the top level (escaped inside a slice, which
// raymond printed as a whole), and behaves as a string in blocks and
// helpers, as raymond rendered it.
func TestGoContextSafeString(t *testing.T) {
	safe := raymondref.SafeString("<b>&</b>")
	ctx := map[string]any{
		"x":   safe,
		"xs":  []raymondref.SafeString{safe, "<i>"},
		"any": []any{safe, "<u>"},
		"m":   map[string]any{"k": safe},
		"p":   &safe,
		"e":   raymondref.SafeString(""),
	}
	for _, tpl := range []string{
		`{{x}}`, `{{{x}}}`, `{{xs}}`, `{{any}}`, `{{m.k}}`, `{{p}}`, `{{e}}`,
		`{{#if x}}y{{/if}}`, `{{#if e}}y{{else}}n{{/if}}`, `{{#each xs}}[{{this}}]{{/each}}`,
		`{{#with x}}{{this}}{{/with}}`, `{{x.length}}`, `{{upper x}}`, `{{equal x "<b>&</b>"}}`,
	} {
		checkGo(t, tpl, ctx)
	}
}

// TestGoContextNoAddresses: %v of a Go value (prettyp-num-en's error
// text) never prints a process address, which would differ between
// processes: where fmt would print one (a chan, a func, a pointer it does
// not follow, at the top or nested in an engine array), the value prints
// as its type. Nil ones print as fmt prints them.
func TestGoContextNoAddresses(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	n := 3
	for _, tc := range []struct {
		x    any
		want string
	}{
		{make(chan int), "got: (chan int)"},
		{[]any{func() {}}, "got: [(func())]"},
		{[]any{&n}, "got: [(*int)]"},
		{[]any{&[]int{1, 2}}, "got: [(*[]int)]"},
		{map[string]any{"p": &n}, "got: map[p:(*int)]"},
		{map[string]*int{"p": &n}, "got: (map[string]*int)"},
		{&[]int{1, 2}, "got: [1 2]"}, // the path lookup follows the pointer
		{[]*int{nil}, "got: [<nil>]"},
		{(chan int)(nil), "got: <nil>"},
	} {
		for range 3 {
			_, err := libhandlebars.Render(tpl, map[string]any{"x": tc.x})
			require.ErrorContains(t, err, tc.want, "%T", tc.x)
		}
	}
}

// TestGoContextReflectValue: a reflect.Value in a Go context prints as fmt
// prints it: unwrapped at the top (and sized, charged and guarded as the
// value it holds), by its String method below; never an address.
func TestGoContextReflectValue(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	n := 3
	for _, tc := range []struct {
		x    any
		want string
	}{
		{reflect.ValueOf(&n), "got: (*int)"},
		{[]any{reflect.ValueOf(&n)}, "got: [<*int Value>]"},
		{[]any{reflect.ValueOf("s")}, "got: [s]"},
		{reflect.ValueOf(map[float64]int{math.NaN(): 1, math.NaN(): 2}), "more than one NaN key"},
	} {
		for range 3 {
			_, err = libhandlebars.Render(tpl, map[string]any{"x": tc.x})
			require.ErrorContains(t, err, tc.want, "%v", tc.x)
		}
	}
	m := &countMeter{}
	big := reflect.ValueOf(make([]int, 1<<20))
	_, err = tpl.Render(map[string]any{"x": big}, hbs.Options{Meter: m})
	require.ErrorContains(t, err, "got: [0 0 0")
	require.Greater(t, m.n, int64(1<<20), "each element charged, as the slice it holds")
}

// TestGoContextScalarsSizedExactly: %v of a large slice of scalars is
// sized at its real length, so it does not fail the produced-bytes bound
// it stays within (64 bytes an element would have).
func TestGoContextScalarsSizedExactly(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	for _, x := range []any{make([]int, 2_100_000), make([]bool, 2_100_000), struct{ A []int8 }{make([]int8, 2_100_000)}} {
		_, err := libhandlebars.Render(tpl, map[string]any{"x": x})
		require.ErrorContains(t, err, "value passed in must be a number", "%T", x)
		require.NotContains(t, err.Error(), "produces more than", "%T", x)
	}
}

type unexpF struct {
	f float64
	c complex64
}

// TestGoContextReflectValueFmt: fmt's %v of reflect.Values and of
// unexported float fields, as raymond printed them: a Value holding a
// Value prints by the inner one's String; a Value of an unexported field
// prints by reflection; unexported floats print without being taken.
func TestGoContextReflectValueFmt(t *testing.T) {
	inner := reflect.ValueOf(struct {
		x int
		y string
		z []float64
	}{5, "hi", []float64{1.5}})
	for _, x := range []any{
		reflect.ValueOf(reflect.ValueOf(7)),
		inner.Field(0), inner.Field(1), inner.Field(2),
		unexpF{1.5, 2}, []unexpF{{1.5, 2}}, map[string]any{"k": unexpF{1.5, 2}},
	} {
		checkGo(t, `{{prettyp-num-en x}}`, map[string]any{"x": x})
	}
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	_, err = libhandlebars.Render(tpl, map[string]any{"x": reflect.ValueOf(reflect.ValueOf(make([]int, 1<<22)))})
	require.ErrorContains(t, err, "got: <[]int Value>", "the inner Value's String, not its 4M elements")
}

type ptrStringer struct{ p *int }

func (ptrStringer) String() string { return "s" }

// TestGoContextKeysByAddress: %v of a map whose keys fmt orders by address
// (pointers behind an error or String method, or a time's Location) would
// differ between runs: it prints as its type. Keys that hold pointers
// fmt never needs to compare print as before.
func TestGoContextKeysByAddress(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	a, b := 1, 2
	same := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		x    any
		want string
	}{
		{map[error]int{errors.New("a"): 1, errors.New("b"): 2}, "got: (map[error]int)"},
		{map[time.Time]int{same.In(time.FixedZone("X", 0)): 1, same.In(time.FixedZone("Y", 0)): 2}, "got: (map[time.Time]int)"},
		{map[time.Time]int{same: 1, same.In(time.FixedZone("X", 0)): 2}, "got: map["}, // UTC's Location is nil: ordered first, always
		{map[any]int{ptrStringer{&a}: 1, ptrStringer{&b}: 2}, "got: (map[interface {}]int)"},
		{map[time.Time]int{same: 1, same.Add(time.Hour).In(time.FixedZone("X", 0)): 2}, "got: map["},
		{map[ptrStringer]int{{&a}: 1}, "got: map[s:1]"},
	} {
		for range 3 {
			_, err = libhandlebars.Render(tpl, map[string]any{"x": tc.x})
			require.ErrorContains(t, err, tc.want, "%T", tc.x)
		}
	}
}

// TestGoContextUnexportedZeroValue: an unexported zero reflect.Value field
// prints by reflection, as fmt prints it (no address in it).
func TestGoContextUnexportedZeroValue(t *testing.T) {
	checkGo(t, `{{prettyp-num-en x}}`, map[string]any{"x": struct{ rv reflect.Value }{}})
}

type lblKey struct{ s string }

type ptrArrKey [1]*lblKey

func (k ptrArrKey) String() string { return k[0].s }

type chanArrKey [1]chan int

func (chanArrKey) String() string { return "c" }

// TestGoContextArrayKeysByAddress: keys holding pointers or chans inside
// arrays (behind a String method) are ordered by address too: the map
// prints as its type, in a struct field and behind an interface as well.
func TestGoContextArrayKeysByAddress(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	x, y := &lblKey{"a"}, &lblKey{"b"}
	type withArr struct {
		N int
		A ptrArrKey
	}
	for _, tc := range []struct {
		x    any
		want string
	}{
		{map[ptrArrKey]int{{x}: 1, {y}: 2}, "got: (map[libhandlebars_test.ptrArrKey]int)"},
		{map[withArr]int{{0, ptrArrKey{x}}: 1, {0, ptrArrKey{y}}: 2}, "got: (map[libhandlebars_test.withArr"},
		{map[any]int{ptrArrKey{x}: 1, ptrArrKey{y}: 2}, "got: (map[interface {}]int)"},
		{map[chanArrKey]int{{make(chan int)}: 1, {make(chan int)}: 2}, "got: (map[libhandlebars_test.chanArrKey]int)"},
	} {
		_, err = libhandlebars.Render(tpl, map[string]any{"x": tc.x})
		require.ErrorContains(t, err, tc.want, "%T", tc.x)
	}
}

// TestGoContextNilRefsSizedExactly: nil pointers and chans print as <nil>
// (5 bytes) and are sized so: %v of many fits the output bound it fits.
func TestGoContextNilRefsSizedExactly(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{prettyp-num-en x}}`)
	require.NoError(t, err)
	_, err = libhandlebars.Render(tpl, map[string]any{"x": make([]chan int, 2_100_000)})
	require.ErrorContains(t, err, "value passed in must be a number, got: [<nil> <nil>")
	lim := hbs.DefaultLimits()
	lim.MaxOutputBytes = 53
	_, err = tpl.Render(map[string]any{"x": []*int{nil}}, hbs.Options{Limits: lim})
	require.EqualError(t, err, "value passed in must be a number, got: [<nil>]")
}
