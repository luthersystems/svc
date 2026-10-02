package hbs_test

import (
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"
)

// A grammar-aware generator of templates and contexts for differential
// testing. It draws names from a small vocabulary so that paths resolve,
// helpers get the argument types that trigger their conversions, and blocks
// nest.

var (
	genPaths = []string{
		"a", "b", "name", "n", "s", "items", "m", "arr", "this", ".", "../name", "../../a",
		"@index", "@key", "@first", "@last", "@root.name", "@root", "@../index", "items.[0]",
		"items.[1].name", "m.a", "nul", "missing", "f", "t", "no", "sub.name", "x", "i",
		"tag", "date", "list", "this.name", "[name]", "arr.[2]", "@foo", "../items",
	}
	genHelpers = []string{
		"eq", "len", "not", "and", "or", "gt", "gte", "lt", "lte", "times", "div", "mod",
		"date-diff-month", "is-after", "date-add-months", "to-int", "plus", "minus",
		"select", "global", "round-to-nth", "in-string-array", "prettyp-num-en",
		"possessive", "date-beautify", "date-DDMMYY-slash", "date-DDMMYYYY-slash",
		"date-DDMMYYYY", "format-phone-gb", "escape-uri-component", "to-str",
		"if", "unless", "each", "with", "equal", "lookup", "nohelper",
	}
	genBlockHelpers = []string{"if", "unless", "each", "with", "equal", "select", "items", "m", "name", "nohelper", "eq", "len", "to-str"}
	genHashKeys     = []string{"a", "b", "c", "from", "where", "key", "val", "haystack", "needle", "includeZero"}
	genStrings      = []string{"", "x", "3", "1.5", "a<b>&'\"", "tag=x", "name=one", "2020-01-31", "abc", "red", "k", " s ", "07700900123", "-2"}
	genNumbers      = []string{"0", "1", "3", "-1", "2.5", "12", "-0.5", "100"}
	genContent      = []string{"x", " ", "\n", "<&'\">", "  \n", "\t", "é", "y z", "\n  "}
	genKeys         = []string{"a", "b", "name", "n", "s", "items", "m", "arr", "nul", "f", "t", "no", "sub", "x", "tag", "date", "list", "key", "index", "i"}
)

type gen struct {
	r     *rand.Rand
	b     strings.Builder
	depth int
}

func newGen(s1, s2 uint64) *gen { return &gen{r: rand.New(rand.NewPCG(s1, s2))} }

func (g *gen) pick(xs []string) string { return xs[g.r.IntN(len(xs))] }

func (g *gen) w(s string) { g.b.WriteString(s) }

func (g *gen) template() string {
	g.b.Reset()
	g.program(2 + g.r.IntN(4))
	return g.b.String()
}

func (g *gen) program(n int) {
	for range n {
		g.statement()
	}
}

func (g *gen) open() string {
	if g.r.IntN(6) == 0 {
		return "{{~"
	}
	return "{{"
}

func (g *gen) close() string {
	if g.r.IntN(6) == 0 {
		return "~}}"
	}
	return "}}"
}

func (g *gen) statement() {
	switch k := g.r.IntN(10); {
	case k < 3:
		g.w(g.pick(genContent))
	case k < 6 || g.depth >= 4:
		if g.r.IntN(5) == 0 {
			g.w("{{{")
			g.expr()
			g.w("}}}")
			return
		}
		g.w(g.open())
		g.expr()
		g.w(g.close())
	case k < 9:
		g.block()
	default:
		g.w("{{! c }}")
	}
}

func (g *gen) block() {
	h := g.pick(genBlockHelpers)
	g.depth++
	defer func() { g.depth-- }()
	g.w(g.open() + "#" + h)
	g.args(h)
	if g.r.IntN(5) == 0 {
		g.w(" as |x i|")
	}
	g.w(g.close())
	g.program(1 + g.r.IntN(3))
	switch g.r.IntN(4) {
	case 0:
		g.w("{{else}}")
		g.program(1 + g.r.IntN(2))
	case 1:
		g.w("{{else if ")
		g.param()
		g.w("}}")
		g.program(1)
	default:
	}
	g.w(g.open() + "/" + h + g.close())
}

func (g *gen) args(h string) {
	switch h {
	case "select":
		g.w(" from=" + g.pick([]string{"items", "../items", "arr", "s", "missing"}))
		g.w(` where="` + g.pick([]string{"tag=x", "name=one", "n=3", "tag", "a=b=c"}) + `"`)
		return
	case "items", "m", "name":
		return
	default:
	}
	n, ok := genArity[h]
	if !ok || g.r.IntN(8) == 0 {
		n = g.r.IntN(3)
	}
	for range n {
		g.w(" ")
		g.param()
	}
	g.hash()
}

var genArity = map[string]int{
	"eq": 2, "len": 1, "not": 1, "and": 0, "or": 0, "gt": 2, "gte": 2, "lt": 2, "lte": 2,
	"times": 2, "div": 2, "mod": 2, "date-diff-month": 2, "is-after": 2, "date-add-months": 2,
	"to-int": 1, "plus": 0, "minus": 1, "global": 1, "round-to-nth": 2, "in-string-array": 0,
	"prettyp-num-en": 1, "possessive": 1, "date-beautify": 1, "date-DDMMYY-slash": 1,
	"date-DDMMYYYY-slash": 1, "date-DDMMYYYY": 1, "format-phone-gb": 1,
	"escape-uri-component": 1, "to-str": 1, "if": 1, "unless": 1, "each": 1, "with": 1, "equal": 2,
}

func (g *gen) hash() {
	if g.r.IntN(3) != 0 {
		return
	}
	for range 1 + g.r.IntN(3) {
		g.w(" " + g.pick(genHashKeys) + "=")
		g.param()
	}
}

func (g *gen) expr() {
	if g.r.IntN(2) == 0 {
		g.w(g.pick(genPaths))
		return
	}
	h := g.pick(genHelpers)
	g.w(h)
	g.args(h)
}

func (g *gen) param() {
	switch k := g.r.IntN(10); {
	case k < 4:
		g.w(g.pick(genPaths))
	case k < 6:
		g.w(`"` + strings.ReplaceAll(g.pick(genStrings), `"`, `\"`) + `"`)
	case k < 8:
		g.w(g.pick(genNumbers))
	case k < 9 || g.depth >= 4:
		g.w(g.pick([]string{"true", "false"}))
	default:
		g.depth++
		g.w("(")
		g.expr()
		g.w(")")
		g.depth--
	}
}

func (g *gen) context() string {
	m := map[string]any{}
	for range 4 + g.r.IntN(10) {
		m[g.pick(genKeys)] = g.value(0)
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func (g *gen) value(depth int) any {
	k := g.r.IntN(12)
	if depth >= 3 && k >= 8 {
		k = g.r.IntN(8)
	}
	switch k {
	case 0:
		return nil
	case 1:
		return g.r.IntN(2) == 0
	case 2:
		return []float64{0, 1, 3, -2, 2.5, 1e21, 9007199254740993, 0.1}[g.r.IntN(8)]
	case 3, 4:
		return g.pick(genStrings)
	case 5:
		return float64(g.r.IntN(5))
	case 6:
		return g.pick([]string{"one", "x", "y", "2021-03-15"})
	case 7:
		return ""
	case 8, 9:
		arr := make([]any, g.r.IntN(4))
		for i := range arr {
			arr[i] = g.value(depth + 1)
		}
		return arr
	default:
		obj := map[string]any{}
		for range g.r.IntN(5) {
			obj[g.pick(genKeys)] = g.value(depth + 1)
		}
		return obj
	}
}

func TestRandomDifferential(t *testing.T) {
	n := 5000
	if testing.Short() || raceEnabled {
		n = 500
	}
	for seed := range uint64(n) {
		g := newGen(seed, 7)
		tpl := g.template()
		ctx := g.context()
		if d := diff(tpl, ctx); d != "" {
			t.Fatalf("seed %d:\n%s", seed, d)
		}
	}
}

func FuzzDifferential(f *testing.F) {
	for i := range uint64(20) {
		f.Add(i, uint64(1))
	}
	f.Fuzz(func(t *testing.T, s1, s2 uint64) {
		g := newGen(s1, s2)
		tpl := g.template()
		ctx := g.context()
		if d := diff(tpl, ctx); d != "" {
			t.Fatal(d)
		}
	})
}

// FuzzTemplateText fuzzes raw template text against a fixed context.
func FuzzTemplateText(f *testing.F) {
	for _, tpl := range diffCases {
		f.Add(tpl)
	}
	f.Fuzz(func(t *testing.T, tpl string) {
		if len(tpl) > 2000 {
			return
		}
		if d := diff(tpl, quirkCtx); d != "" {
			t.Fatal(d)
		}
	})
}
