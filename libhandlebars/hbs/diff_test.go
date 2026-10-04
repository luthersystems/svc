// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/luthersystems/svc/libhandlebars/hbs"
	raymond "github.com/luthersystems/svc/libhandlebars/internal/raymondref"
	"github.com/stretchr/testify/require"
)

var raceEnabled bool

// outcome is the observable result of one render.
type outcome struct {
	out      string
	err      string
	kind     hbs.ErrorKind
	failed   bool // an error was returned
	panicked bool // the reference panicked (a crash in production)
	parseErr bool
	jsonErr  bool
}

func (o outcome) String() string {
	switch {
	case o.panicked:
		return "PANIC: " + o.err
	case o.failed:
		return fmt.Sprintf("ERROR(kind %d): %q", o.kind, o.err)
	default:
		return fmt.Sprintf("OK: %q", o.out)
	}
}

// refRun renders with the reference pipeline: json.Unmarshal into a map,
// raymond.Parse, svc's helpers, Exec.
func refRun(tpl, ctxJSON string) outcome {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(ctxJSON), &m); err != nil {
		return outcome{failed: true, jsonErr: true, err: err.Error()}
	}
	t, err := raymond.Parse(tpl)
	if err != nil {
		return outcome{failed: true, parseErr: true, err: err.Error()}
	}
	refAddHelpers(t)
	var res outcome
	func() {
		defer func() {
			if p := recover(); p != nil {
				res = outcome{failed: true, panicked: true, err: fmt.Sprint(p)}
			}
		}()
		s, err := t.Exec(m)
		if err != nil {
			res = outcome{failed: true, err: err.Error()}
			return
		}
		res = outcome{out: s}
	}()
	return res
}

func candRun(tpl, ctxJSON string, o hbs.Options) outcome {
	ctx, err := hbs.FromJSON([]byte(ctxJSON))
	if err != nil {
		return outcome{failed: true, jsonErr: true, err: err.Error()}
	}
	p, err := hbs.ParseForTest(tpl)
	if err != nil {
		return outcome{failed: true, parseErr: true, err: err.Error()}
	}
	s, err := p.Render(ctx, o)
	if err != nil {
		res := outcome{failed: true, err: err.Error()}
		var he *hbs.Error
		if errors.As(err, &he) {
			res.kind = he.Kind
		}
		return res
	}
	return outcome{out: s}
}

// diff renders with both engines and returns a description of any
// difference, or "". A template on which the candidate reports a nesting
// or evaluation depth limit is not run on the reference: raymond can
// overflow the Go stack there, which cannot be recovered. Any other limit
// error (output size, steps, produced bytes) is compared like any result,
// so a case the reference rendered is reported.
//
// On a CPU other than amd64, a difference is also skipped when the
// reference's to-int converted a float that is NaN, +-Inf or outside int64
// range: the reference uses the hardware conversion there, and the engine
// pins the amd64 result (see refNoteToInt).
func diff(tpl, ctxJSON string) string {
	cand := candRun(tpl, ctxJSON, hbs.Options{})
	if cand.failed && cand.kind == hbs.KindLimit && strings.Contains(cand.err, "depth") {
		return ""
	}
	before := refToIntUnportable.Load()
	ref := refRun(tpl, ctxJSON)
	if refToIntUnportable.Load() != before && runtime.GOARCH != "amd64" {
		return ""
	}
	switch {
	case ref.panicked:
		// The allowed difference: a crash becomes a render error.
		if cand.failed && cand.kind == hbs.KindRender {
			return ""
		}
	case ref.parseErr || ref.jsonErr:
		if cand.failed && cand.err == ref.err {
			return ""
		}
	case ref.failed:
		if cand.failed && cand.kind == hbs.KindRender && cand.err == ref.err {
			return ""
		}
	default:
		if !cand.failed && cand.out == ref.out {
			return ""
		}
	}
	return fmt.Sprintf("template %q\ncontext  %s\nref:  %s\ncand: %s", tpl, ctxJSON, ref, cand)
}

// refToIntUnportable counts the reference's to-int calls on a float64 that is
// NaN, +-Inf or outside [-2^63, 2^63). For those, raymond's int(f) depends on
// the CPU: amd64 gives math.MinInt64 for all of them; arm64 saturates (+Inf
// and 1e19 give math.MaxInt64, -Inf and -1e19 give math.MinInt64) and gives 0
// for NaN. The engine always gives the amd64 result (hbs.floatToInt), so on
// any other CPU diff does not compare these runs. Allowed differences for
// testdata/allowed-diffs.txt on non-amd64 reference runs, all "to-int on a
// non-finite or out-of-range float: the engine pins the amd64 result
// math.MinInt64":
//
//	{{to-int (div 1 0)}}, {{to-int (div -1 0)}}, {{to-int (times -1 (div 1 0))}}
//	{{to-int (div 0 0)}}, {{to-int (mod 1 0)}} (NaN)
//	{{to-int x}} where context x is 1e19, -1e19, 2^63 (9223372036854775808)
//	or any |x| >= 2^63 other than exactly -2^63
//	any to-int argument computed by times/div/mod/plus/minus that is
//	non-finite or outside int64 range
var refToIntUnportable atomic.Int64

// refNoteToInt is called by the reference's to-int helper with its argument.
func refNoteToInt(v interface{}) {
	if f, ok := v.(float64); ok && !(f >= -0x1p63 && f < 0x1p63) {
		refToIntUnportable.Add(1)
	}
}

const quirkCtx = `{
	"s": "<a href='x'>&\"=` + "`" + `", "name": "James", "names": "Chris ",
	"n": 3, "f": 1.5, "zero": 0, "neg": -2, "big": 1e21, "huge": 9007199254740993,
	"t": true, "no": false, "nul": null, "empty": "", "earr": [], "eobj": {},
	"arr": ["a", 1, true, null, 2.5, {"k": "v"}, ["x", "y"]],
	"m": {"b": 1, "a": 2, "B": 3},
	"items": [
		{"name": "one", "price": "10", "qty": 2, "tag": "x", "on": true},
		{"price": "2.5", "qty": 0, "tag": "y", "name": null},
		{"name": "three", "price": "abc", "tag": "x", "sub": {"name": "inner"}}
	],
	"matrix": [[1, 2], [3, 4]],
	"date": "2020-01-31", "date2": "2021-03-15", "baddate": "31/01/2020",
	"phone": "02079460000", "phone2": "+14155550100", "badphone": "abc",
	"str3": "3", "str35": "3.5", "x": "123456789.12",
	"list": ["red", "green", 5], "uri": "a b&c=d/é",
	"key": "ctxkey", "index": "ctxindex", "raw": false,
	"deep": {"a": {"b": {"c": "abc"}}}
}`

// diffCases cover every helper, the quirks in the brief and the error texts.
var diffCases = []string{
	// whitespace control around an inverse and a chained one (setBlockInverseStrip)
	"{{#if no}} a {{~else~}} b {{/if}}|{{#if no}} a {{~else if t~}} b {{~/if}} |",
	"{{#if no}} a {{else if no}} b {{~else~}} c {{~/if}} |{{^t}} a {{~^~}} b {{/t}}|",
	"x {{~#if t~}} a {{~else if no~}} b {{~/if~}} y",
	// printing and escaping
	`{{s}}|{{{s}}}|{{&s}}`, `{{m}}|{{arr}}|{{earr}}|{{eobj}}|{{nul}}|{{missing}}`,
	`{{n}} {{f}} {{big}} {{huge}} {{neg}} {{zero}} {{t}} {{no}}`,
	`{{this}}`, `{{.}}`, `{{deep.a.b.c}} {{deep.a.x.c}} {{deep/a/b/c}}`,
	`{{arr.[0]}}{{arr.1}}{{arr.[6].[1]}}{{arr.[9]}}{{arr.length}}`, `{{s.0}}`,
	`{{"name"}} {{"nope"}} {{1}} {{true}}`, `{{#each matrix}}{{0}}{{1}}{{this}};{{/each}}`,
	// comments, whitespace
	"a {{! c }} b {{!-- c --}} c", "a\n{{#if t}}\nyes\n{{/if}}\nb", "a {{~n~}} b",
	"{{#each items}}\n  {{name}}\n{{/each}}\n", "x {{~#if t~}} y {{~/if~}} z",
	// if / unless / else chains
	`{{#if t}}y{{else}}n{{/if}}{{#if no}}y{{else}}n{{/if}}{{#if nul}}y{{/if}}{{#if earr}}y{{else}}e{{/if}}`,
	`{{#unless t}}y{{else}}n{{/unless}}{{#unless no}}u{{/unless}}`,
	`{{#if no}}1{{else if zero}}2{{else if n}}3{{else}}4{{/if}}`,
	`{{#if zero includeZero=true}}z{{/if}}{{#if 0 includeZero=true}}lit{{/if}}{{#unless 0 includeZero=true}}u{{else}}e{{/unless}}`,
	`{{#if}}x{{/if}}`, `{{#if t n}}x{{/if}}`, `{{if t}}`, `{{#with t}}{{if t}}{{/with}}`,
	`{{^t}}not{{/t}}{{^no}}inv{{/no}}`,
	// round-to-nth precisions fmt rejects print a constant error string
	`{{round-to-nth "1" "999999999"}}|{{round-to-nth "1.5" "10000010"}}|{{round-to-nth "2.25" "12"}}`,
	// each / with / equal / climb / data
	`{{#each items}}{{@index}}:{{name}}:{{@first}}:{{@last}}:{{@key}};{{/each}}`,
	`{{#each m}}{{@key}}={{this}}@{{@index}}{{#if @last}}!{{/if}};{{/each}}`,
	`{{#each items as |it i|}}{{i}}={{it.name}},{{/each}}`, `{{#each m as |v k|}}{{k}}={{v}},{{/each}}`,
	`{{#each items as |x x|}}{{x}}{{/each}}`,
	`{{#each items}}{{#each ../items}}{{@../index}}{{@index}} {{/each}}|{{/each}}`,
	`{{#each items}}{{../name}}{{@root.name}}{{@root}}{{/each}}`,
	`{{#each arr}}[{{this}}]{{/each}}`, `{{#each s}}x{{/each}}{{#each n}}y{{/each}}{{#each earr}}a{{else}}b{{/each}}`,
	`{{#each}}x{{/each}}`, `{{#items}}{{name}}{{/items}}`, `{{#m}}{{a}}{{/m}}`, `{{#name}}{{this}}{{/name}}`,
	`{{#with deep.a}}{{b.c}}{{name}}{{/with}}{{#with nul}}a{{else}}b{{/with}}`,
	`{{#with items}}{{name}}{{/with}}`, `{{#each items}}{{sub.name}}|{{/each}}`,
	`{{#equal n 3}}eq{{/equal}}{{#equal n "3"}}eqs{{/equal}}{{#equal str3 3}}e3{{/equal}}{{#equal a}}x{{/equal}}`,
	`{{@index}}{{@key}}{{@foo}}{{@root.name}}`, `{{#each items}}{{@name}}{{/each}}`,
	`{{#each items}}{{#with sub}}{{@index}}{{/with}}{{/each}}`,
	`{{#each items}}{{@../index}}{{/each}}`,
	// helper conversions and errors
	`{{eq n 3}}{{eq n "3"}}{{eq missing ""}}{{eq arr "a1true2.5UNPRINTABLExy"}}`, `{{eq n}}`,
	`{{len arr}}{{len missing}}{{len earr}}`, `{{len 1}}`, `{{len n}}`, `{{len s}}`, `{{len m}}`, `{{len t}}`,
	`{{not t}}|{{not missing}}|{{not ""}}|{{not earr}}|{{not 0}}|{{not "x"}}`,
	`{{#if (not missing)}}a{{else}}b{{/if}}`, `{{eq (not missing) ""}}`,
	`{{and a=t b=n}}{{and a=t b=no}}{{and}}{{or a=no b=missing}}{{or a=no b=s}}{{or}}`,
	`{{gt n 2}}{{gte n "3"}}{{lt f 2}}{{lte "x" 1}}{{gt missing -1}}{{gt big 1}}`,
	`{{times n f}} {{times "x" 2}} {{div n 0}} {{div 1 4}} {{div 0 0}} {{times big big}}`,
	`{{mod 7 3}} {{mod 7 "x"}} {{mod "x" "y"}} {{mod -7 2}}`,
	`{{date-diff-month date date2}} {{date-diff-month date2 date}} {{date-diff-month baddate date}}`,
	`{{is-after date2 date}}{{is-after date date2}}{{is-after date baddate}}`,
	`{{date-add-months date 1}} {{date-add-months baddate 1}} {{date-add-months date -13}}`,
	`{{date-add-months date n}}`, `{{date-add-months date missing}}`, `{{date-add-months date "1"}}`,
	`{{to-int "3.5"}} {{to-int "42"}} {{to-int n}} {{to-int f}} {{to-int t}} {{to-int missing}} {{to-int 7}} {{to-int "99999999999999999999"}}`,
	`{{to-int (div 1 0)}} {{to-int (div -1 0)}} {{to-int (div 0 0)}} {{to-int (mod 1 0)}} {{to-int big}} {{to-int (times big big)}} {{to-int (times -1 big)}}`,
	`{{plus a=1 b=2.5 c="3" d=missing e="x"}} {{plus}} {{plus a=0.1 b=0.2 c=0.3}}`,
	`{{minus 10 a=1 b=n}} {{minus "x" a=1}} {{minus missing}} {{minus arr}}`,
	`{{#select from=items where="tag=x"}}{{name}};{{/select}}`, `{{#select from=items where="qty=2"}}{{name}}{{/select}}`,
	`{{#select from=s where="a=b"}}{{/select}}`, `{{#select from=missing where="a=b"}}{{/select}}`,
	`{{#select from=items where="tag"}}{{/select}}`, `{{#select from=items where="a=b=c"}}{{/select}}`,
	`{{select from=items where="tag=x"}}`, `{{#each items}}{{select from=../items where="tag=x"}}{{/each}}`,
	`{{global "ns" key="k" val="v"}}{{global "ns" key="k"}}{{global "other" key="k"}}`,
	`{{global "ns"}}`, `{{global "ns" key=1}}`, `{{global "ns" key="k" val=n}}`, `{{global "ns" key=missing}}`,
	`{{round-to-nth x 2}} {{round-to-nth "1.005" 2}} {{round-to-nth n 0}} {{round-to-nth f "1"}}`,
	`{{round-to-nth "abc" 2}}`, `{{round-to-nth 1 "x"}}`, `{{round-to-nth 1.5 -1}}`,
	`{{in-string-array haystack=list needle="red"}}{{in-string-array haystack=list needle=5}}{{in-string-array haystack=list needle="blue"}}`,
	`{{in-string-array haystack=s needle="a"}}`, `{{in-string-array needle="a"}}`,
	`{{prettyp-num-en 1234567.885}} {{prettyp-num-en n}} {{prettyp-num-en "1234.5"}} {{prettyp-num-en -0.001}}`,
	`{{prettyp-num-en "abc"}}`, `{{prettyp-num-en missing}}`, `{{prettyp-num-en arr}}`, `{{prettyp-num-en m}}`,
	`{{possessive name}}|{{{possessive name}}}|{{possessive names}}|{{possessive ""}}|{{possessive missing}}|{{possessive n}}`,
	`{{date-beautify date}}|{{date-DDMMYY-slash date}}|{{date-DDMMYYYY-slash date}}|{{date-DDMMYYYY date}}|{{date-beautify ""}}|{{date-beautify missing}}`,
	`{{date-beautify baddate}}`, `{{date-DDMMYY-slash "x"}}`, `{{date-DDMMYYYY-slash "2020-13-01"}}`, `{{date-DDMMYYYY 5}}`,
	`{{format-phone-gb phone}}|{{format-phone-gb phone2}}|{{format-phone-gb badphone}}|{{format-phone-gb ""}}|{{format-phone-gb "07700900123"}}`,
	`{{escape-uri-component uri}}|{{{escape-uri-component uri}}}`,
	`{{to-str 3}}|{{to-str n}}|{{to-str f}}|{{to-str "s"}}|{{to-str t}}|{{to-str missing}}|{{to-str 2.5}}|{{to-str arr}}`,
	// subexpressions, hashes
	`{{#if (gt (plus a=n b=1) 3)}}big{{/if}}`, `{{to-str (len arr)}}`, `{{len (len arr)}}`,
	`{{eq (to-str 3) "3"}}{{not (eq a b)}}`, `{{nohelper 1 2 a=3}}|{{nohelper}}|{{lookup m "a"}}|{{log "x"}}`,
	`{{#nohelper}}x{{else}}y{{/nohelper}}`, `{{#if (nohelper 1)}}x{{else}}y{{/if}}`,
	`{{eq a=1}}`, `{{len arr x=1}}`, `{{not t f}}`,
	// partials, raw blocks, odd data paths
	`{{> p}}`, `{{> (to-str "x")}}`, `{{> (len arr)}}`, `{{> "lit"}}`, `{{#if t}}{{> p}}{{/if}}`,
	`{{{{raw}}}}{{x}}{{{{/raw}}}}`, `{{@this}}`, `{{@.}}`, `{{#each items}}{{@this}}{{/each}}`, `{{arr.[-1]}}`,
	// deep paths and literal lookups
	`{{#each items}}{{#each ../matrix}}{{../name}}{{../../n}}{{/each}}{{/each}}`, `{{../n}}{{../../n}}`,
	`{{#with deep}}{{#with a}}{{../a.b.c}}{{../../n}}{{/with}}{{/with}}`,
	`{{[name]}} {{deep.[a].b.[c]}} {{[if]}}`,
}

func TestDifferential(t *testing.T) {
	for _, tpl := range diffCases {
		t.Run(tpl, func(t *testing.T) {
			if d := diff(tpl, quirkCtx); d != "" {
				t.Error(d)
			}
		})
	}
}

// TestDifferentialContexts covers FromJSON's accept/reject behaviour.
func TestDifferentialContexts(t *testing.T) {
	ctxs := []string{
		`null`, `{}`, `[]`, `"s"`, `1`, `true`, `{"a":`, ``, `{"a":1}{}`,
		`{"a": 1, "a": 2}`, `{"a": [null, {"b": null}]}`, `{"é": "x"}`,
		`{"items": [null, 1, "two"]}`, `{"items": [[], [1], {}]}`,
	}
	tpls := []string{
		`{{a}}|{{this}}|{{#each items}}[{{this}}{{a}}]{{/each}}|{{#each a}}{{b}}{{/each}}`,
		`{{#each items}}{{#each this}}{{this}}{{/each}}{{/each}}`, `{{len items}}{{@root}}`,
	}
	for _, c := range ctxs {
		for _, tpl := range tpls {
			if d := diff(tpl, c); d != "" {
				t.Error(d)
			}
		}
	}
}

// TestRefToIntProbe checks the inputs on which diff stops comparing off amd64.
func TestRefToIntProbe(t *testing.T) {
	for tpl, unportable := range map[string]bool{
		`{{to-int (div 1 0)}}`: true, `{{to-int (div -1 0)}}`: true, `{{to-int (div 0 0)}}`: true,
		`{{to-int big}}`: true, `{{to-int (times -1 big)}}`: true,
		`{{to-int n}}`: false, `{{to-int f}}`: false, `{{to-int "1e30"}}`: false, `{{to-int 7}}`: false,
		`{{to-int huge}}`: false, `{{div 1 0}}`: false,
	} {
		before := refToIntUnportable.Load()
		refRun(tpl, quirkCtx)
		require.Equal(t, unportable, refToIntUnportable.Load() != before, tpl)
	}
	for _, f := range []float64{math.Inf(1), math.Inf(-1), math.NaN(), 0x1p63, 1e19, -1e19} {
		before := refToIntUnportable.Load()
		refNoteToInt(f)
		require.NotEqual(t, before, refToIntUnportable.Load(), f)
	}
	for _, f := range []float64{-0x1p63, math.Nextafter(0x1p63, 0), 0, -3.5} {
		before := refToIntUnportable.Load()
		refNoteToInt(f)
		require.Equal(t, before, refToIntUnportable.Load(), f)
	}
}

// TestBriefQuirks pins outputs listed in the brief, so the reference and the
// engine are both checked against known values.
func TestBriefQuirks(t *testing.T) {
	cases := []struct{ tpl, want string }{
		{`{{s}}`, "&lt;a href=&apos;x&apos;&gt;&amp;&quot;=`"},
		{`{{m}}|{{arr}}`, "UNPRINTABLE|a1true2.5UNPRINTABLExy"},
		{`{{big}} {{huge}}`, "1000000000000000000000 9007199254740992"},
		{`{{to-str 3}} {{to-str n}}`, "3 3.000000"},
		{`{{date-add-months date 1}}`, "2020-03-02"},
		{`{{prettyp-num-en 1234567.885}}`, "1,234,567.88"},
		{`{{to-int "3.5"}} {{mod 7 "x"}}`, "0 NaN"},
		{`{{round-to-nth x 2}}`, "123456792.00"},
		{`{{not missing}}|{{eq missing ""}}|{{len missing}}`, "|true|0"},
		{`{{#each m}}{{@key}}={{this}};{{/each}}`, "B=3;a=2;b=1;"},
		{`{{#each items}}{{name}},{{/each}}`, "one,,three,"},
		{`{{#each list}}{{name}},{{/each}}`, "James,James,James,"},
		{`{{plus a=missing b=1}}`, "1"},
		{`{{possessive names}}`, "Chris&apos;"},
		{`{{lookup m "a"}}{{log "x"}}{{nohelper 1}}`, ""},
		{`{{{{raw}}}}{{x}}{{{{/raw}}}}`, ""},
	}
	for _, c := range cases {
		ref := refRun(c.tpl, quirkCtx)
		require.Equal(t, c.want, ref.out, "reference: %s", c.tpl)
		cand := candRun(c.tpl, quirkCtx, hbs.Options{})
		require.Equal(t, c.want, cand.out, "engine: %s", c.tpl)
	}
	errs := []struct{ tpl, want string }{
		{`{{date-add-months date n}}`, "Evaluation error: Helper date-add-months called with argument 1 with type float64 but it should be int\nCurrent node:\n\tPath{Original:'n', Pos:23}"},
		{`{{len 1}}`, "Evaluation error: Helper len called with argument 0 with type int but it should be []interface {}\nCurrent node:\n\tNumber{Value:1, Pos:6}"},
		{`{{global "n" key=1}}`, "global: invalid key type: int"},
		{`{{#select from=s where="a=b"}}{{/select}}`, "select: 'from' must be an array: string"},
		{`{{> p}}`, "Evaluation error: Partial not found: p\nCurrent node:\n\tPartial{Name:Path{Original:'p', Pos:4}, Pos:0}"},
	}
	for _, c := range errs {
		ref := refRun(c.tpl, quirkCtx)
		require.Equal(t, c.want, ref.err, "reference: %s", c.tpl)
		cand := candRun(c.tpl, quirkCtx, hbs.Options{})
		require.Equal(t, c.want, cand.err, "engine: %s", c.tpl)
		require.Equal(t, hbs.KindRender, cand.kind)
	}
}

// TestAllowedCrashDifferences: inputs that crash raymond are render errors.
func TestAllowedCrashDifferences(t *testing.T) {
	for _, tpl := range []string{`{{@this}}`, `{{@.}}`, `{{arr.[-1]}}`, `{{#if (eq @this 1)}}{{/if}}`} {
		ref := refRun(tpl, quirkCtx)
		require.True(t, ref.panicked, "reference should crash on %s: %s", tpl, ref)
		cand := candRun(tpl, quirkCtx, hbs.Options{})
		require.True(t, cand.failed, tpl)
		require.Equal(t, hbs.KindRender, cand.kind, tpl)
		require.True(t, strings.HasPrefix(cand.err, "Evaluation error: "), cand.err)
	}
}
