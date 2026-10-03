// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/luthersystems/svc/libhandlebars"
	"github.com/luthersystems/svc/libhandlebars/hbs"
	"github.com/luthersystems/svc/libhandlebars/internal/hbref"
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
	// Not {{prettyp-num-en st}}: its error holds fmt's %v of the struct,
	// which raymond printed with unexported fields and pointer addresses;
	// the native context prints the exported fields only (DETERMINISM.md).
	`{{prettyp-num-en m}}`,
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

// TestGoContextDifferential renders goTemplates with Go-typed contexts
// through raymond (svc's old Go API) and through Render: the output and
// error text must match, in the default (native) mode with the Go value
// itself, and in JSON mode with the context raymond sees after
// json.Marshal and json.Unmarshal.
func TestGoContextDifferential(t *testing.T) {
	ctx := goContext()
	jsonCtx, err := json.Marshal(ctx)
	require.NoError(t, err)
	for _, tplStr := range goTemplates {
		tpl, err := libhandlebars.Parse(tplStr)
		require.NoError(t, err, tplStr)

		want, werr := hbref.RenderGo(tplStr, ctx)
		got, gerr := libhandlebars.RenderWith(tpl, ctx, libhandlebars.WithGoContext())
		if werr != nil || gerr != nil {
			if !assert.Error(t, werr, "native %s: raymond rendered %q", tplStr, want) { //nolint:testifylint // report every template
				continue
			}
			if assert.Error(t, gerr, "native %s: got %q", tplStr, got) {
				assert.Equal(t, refRaw(werr), gotRaw(gerr), "native %s", tplStr)
			}
		} else {
			assert.Equal(t, want, got, "native %s", tplStr)
		}

		want, werr = hbref.RenderJSON(tplStr, jsonCtx)
		got, gerr = libhandlebars.RenderWith(tpl, ctx, libhandlebars.WithJSONContext())
		if werr != nil || gerr != nil {
			if !assert.Error(t, werr, "json %s: raymond rendered %q", tplStr, want) { //nolint:testifylint // report every template
				continue
			}
			if assert.Error(t, gerr, "json %s: got %q", tplStr, got) {
				assert.Equal(t, refRaw(werr), gotRaw(gerr), "json %s", tplStr)
			}
		} else {
			assert.Equal(t, want, got, "json %s", tplStr)
		}
	}
}

type goNode struct {
	Name string
	Next *goNode
}

type goMethods struct{ Name string }

func (goMethods) Greeting() string { return "hi" }

func (*goMethods) PtrOnly() string { return "p" }

// TestGoContextCycle: a cyclic Go value converts once per pointer and
// renders as raymond rendered it.
func TestGoContextCycle(t *testing.T) {
	a := &goNode{Name: "a"}
	a.Next = &goNode{Name: "b", Next: a}
	tplStr := `{{name}}{{next.name}}{{next.next.name}}{{next.next.next.name}}`
	tpl, err := libhandlebars.Parse(tplStr)
	require.NoError(t, err)
	want, err := hbref.RenderGo(tplStr, a)
	require.NoError(t, err)
	got, err := libhandlebars.Render(tpl, a)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, "abab", got)
}

// TestGoContextDepth: an acyclic context nested deeper than MaxDepth is a
// limit error, not unbounded recursion.
func TestGoContextDepth(t *testing.T) {
	var v any = "leaf"
	for range hbs.DefaultLimits().MaxDepth + 1 {
		v = []any{v}
	}
	tpl, err := libhandlebars.Parse(`x`)
	require.NoError(t, err)
	_, err = libhandlebars.Render(tpl, map[string]any{"v": v})
	var herr *hbs.Error
	require.ErrorAs(t, err, &herr)
	require.Equal(t, hbs.KindLimit, herr.Kind)
	require.Contains(t, herr.Msg, "Go context nesting exceeds the maximum depth")
	_, err = libhandlebars.RenderWith(tpl, map[string]any{"v": v}, libhandlebars.WithJSONContext())
	require.NoError(t, err, "JSON mode keeps json.Marshal's own bound")
}

// TestGoContextUnsupported: raymond called methods and funcs; the native
// context fails the render where a lookup reaches one, and renders
// everything else.
func TestGoContextUnsupported(t *testing.T) {
	ctx := map[string]any{
		"m":  &goMethods{Name: "n"},
		"mv": goMethods{Name: "v"},
		"f":  func() string { return "called" },
		"ch": make(chan int),
		"c":  complex(1, 2),
	}
	for tplStr, want := range map[string]string{
		`{{m.name}}{{mv.name}}`: "nv",
		`{{m.greeting}}`:        "Go method calls are not supported: libhandlebars_test.goMethods.Greeting",
		`{{m.ptrOnly}}`:         "Go method calls are not supported: libhandlebars_test.goMethods.PtrOnly",
		`{{mv.Greeting}}`:       "Go method calls are not supported: libhandlebars_test.goMethods.Greeting",
		`{{f}}`:                 "Go value not supported: func() string",
		`{{ch}}`:                "Go value not supported: chan int",
		`{{c}}`:                 "Go value not supported: complex128",
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
	tpl, err := libhandlebars.Parse(`{{mv.ptrOnly}}|{{mv.name}}`)
	require.NoError(t, err)
	want, err := hbref.RenderGo(`{{mv.ptrOnly}}|{{mv.name}}`, ctx)
	require.NoError(t, err)
	got, err := libhandlebars.Render(tpl, ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)
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

// TestGoContextSteps: the conversion is charged and bounded by MaxSteps.
func TestGoContextSteps(t *testing.T) {
	big := make([]int, 1<<20)
	_, err := hbs.FromGo(big, hbs.Limits{MaxSteps: 1 << 20}, nil)
	var herr *hbs.Error
	require.ErrorAs(t, err, &herr)
	require.Equal(t, hbs.KindLimit, herr.Kind)
	m := &countMeter{}
	_, err = hbs.FromGo(map[string]any{"a": []int{1, 2}, "b": "x"}, hbs.Limits{}, m)
	require.NoError(t, err)
	require.Positive(t, m.n)
}

type countMeter struct{ n int64 }

func (m *countMeter) Charge(n int64) error { m.n += n; return nil }
