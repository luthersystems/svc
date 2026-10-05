// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"github.com/luthersystems/elps/lisp"
	"github.com/luthersystems/elps/lisp/lisplib/libjson"
	"github.com/luthersystems/svc/libhandlebars"
	"github.com/luthersystems/svc/libhandlebars/hbs"
	"github.com/stretchr/testify/require"
)

// FuzzGoContext exercises reflection, printing and helpers on Go values,
// including cycles and unsupported types, under small render limits.
func FuzzGoContext(f *testing.F) {
	addContextSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			t.Skip()
		}
		g := &contextFuzzGen{data: data}
		tplText := contextFuzzTemplates[g.n(len(contextFuzzTemplates))]
		maxOutput, maxSteps, maxDepth := 16+g.n(1024), int64(8+g.n(256)*32), 1+g.n(16)
		value := g.goValue(0)
		ctx := map[string]any{"x": value, "items": []any{value, g.goValue(0)}}
		tpl, err := libhandlebars.Parse(tplText)
		require.NoError(t, err)

		out, err := libhandlebars.Render(tpl, ctx)
		if err != nil {
			require.NotContains(t, err.Error(), "render panicked")
		}
		if err == nil {
			require.LessOrEqual(t, len(out), hbs.DefaultLimits().MaxOutputBytes)
		}
		out, err = libhandlebars.RenderWith(tpl, ctx,
			libhandlebars.WithGoContext(), libhandlebars.WithMaxOutputBytes(maxOutput),
			libhandlebars.WithMaxSteps(maxSteps), libhandlebars.WithMaxDepth(maxDepth))
		if err != nil {
			require.NotContains(t, err.Error(), "render panicked")
		}
		if err == nil {
			require.LessOrEqual(t, len(out), maxOutput)
		}

		// Rendering the random value repeatedly must stop at the step or
		// output limit even when the chosen template produces little output.
		expand, err := libhandlebars.Parse(`{{#each items}}{{x}}{{/each}}`)
		require.NoError(t, err)
		items := make([]any, 64+g.n(256))
		for i := range items {
			items[i] = map[string]any{"x": value}
		}
		_, err = libhandlebars.RenderWith(expand, map[string]any{"items": items},
			libhandlebars.WithGoContext(), libhandlebars.WithMaxSteps(8),
			libhandlebars.WithMaxOutputBytes(maxOutput))
		var limit *hbs.Error
		require.ErrorAs(t, err, &limit)
		require.Equal(t, hbs.KindLimit, limit.Kind)
	})
}

// FuzzELPSEncode renders random ELPS trees with shared native leaves in
// both modes. Repeating a tree must reproduce its output or error, with
// the allocation cap and step budget applied on every call. Serialization
// failures must agree with json:dump-bytes unless the render budget wins.
func FuzzELPSEncode(f *testing.F) {
	addContextSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			t.Skip()
		}
		g := &contextFuzzGen{data: data}
		tpl := contextFuzzTemplates[g.n(len(contextFuzzTemplates))]
		maxAlloc, budget := 32+g.n(256)*16, int64(32+g.n(256)*64)
		x, native := g.tree(0), lisp.Native(g.goValue(0))
		ctx := sortedMap("x", x, "n", native, "items", lisp.QExpr([]*lisp.LVal{x, native, native}))
		env := newEnvWith(t, mustLoader(t, libhandlebars.WithMaxOutputBytes(256), libhandlebars.WithMaxSteps(2048)))
		env.Runtime.MaxAlloc = maxAlloc
		env.Runtime.SetStepBudget(1 << 60)
		dump := libjson.DefaultSerializer().DumpBytesBuiltin(env,
			lisp.SExpr([]*lisp.LVal{ctx, lisp.Bool(false)}))
		for _, fn := range []string{"render", strictMode} {
			var first string
			for i := range 2 {
				env.Runtime.SetStepBudget(budget)
				res, _ := renderIn(t, env, fn, tpl, ctx)
				if res.Type == lisp.LError {
					require.NotEqual(t, lisp.CondInternalPanic, res.Str, "%.300v", res)
					if dump.Type == lisp.LError && res.Str != lisp.CondStepBudgetExceeded {
						require.Contains(t, res.Cells[0].Str, dump.Cells[0].Str)
					}
				}
				require.Contains(t, []lisp.LType{lisp.LString, lisp.LError}, res.Type)
				_, used := env.Runtime.StepBudget()
				if used > budget {
					require.Equal(t, lisp.CondStepBudgetExceeded, res.Str)
				}
				if res.Type == lisp.LString {
					require.LessOrEqual(t, len(res.Str), 256)
					// A successful render must have encoded within the cap.
					require.Equal(t, lisp.LBytes, dump.Type, "%.300v", dump)
					require.LessOrEqual(t, len(dump.Bytes()), maxAlloc)
				}
				if i == 0 {
					first = res.String()
				} else {
					require.Equal(t, first, res.String())
				}
			}
		}
	})
}

var contextFuzzTemplates = []string{
	`{{x}}|{{n}}`, `{{to-str x}}`, `{{len x}}`, `{{prettyp-num-en x}}`,
	`{{#each x}}{{@key}}={{this}};{{/each}}`, `{{x.a}}|{{x.[0]}}`,
	`{{#if x includeZero=true}}{{x}}{{else}}empty{{/if}}`,
	`{{#with x}}{{a}}{{/with}}`, `{{#each items}}{{this}}{{/each}}`,
	`{{#each items}}{{#each ../items}}{{this}}{{/each}}{{/each}}`,
	`{{global "fuzz" key="x" val=x}}{{global "fuzz" key="x"}}`,
}

func addContextSeeds(f *testing.F) {
	f.Helper()
	f.Add([]byte{})
	for i := range 32 {
		seed := []byte{byte(i), 255, 255, 15, byte(i), byte(i)}
		for j := range 48 {
			seed = append(seed, byte((i*31+j*17)&255))
		}
		f.Add(seed)
		f.Add(append([]byte{byte(i), 64, 255, byte(i)}, seed...))
	}
	f.Add([]byte(strings.Repeat("\xff", 1024)))
	f.Add([]byte("\x00\x01\x02\xff<&>\x00\xe2\x80\xa8"))
	// A string that spells a condition name is valid render output.
	const text = lisp.CondInternalPanic
	f.Add(append([]byte{1, 64, 255, 2, byte(len(text))}, text...))
}

// contextFuzzGen consumes decisions without cycling the input. Exhaustion
// picks nil, and depth and fanout bounds keep each generated tree small.
type contextFuzzGen struct {
	data    []byte
	natives []*lisp.LVal
}

func (g *contextFuzzGen) n(k int) int {
	if len(g.data) == 0 {
		return 0
	}
	b := g.data[0]
	g.data = g.data[1:]
	return int(b) % k
}

func (g *contextFuzzGen) text() string {
	n := min(g.n(65), len(g.data))
	s := string(g.data[:n])
	g.data = g.data[n:]
	return s
}

type contextFuzzStruct struct {
	A      any
	Tagged string `handlebars:"tagged" json:"tagged,omitempty"`
	N      int    `json:",string"`
}

func (g *contextFuzzGen) goValue(depth int) any {
	kind := g.n(20)
	if depth >= 4 {
		kind %= 10
	}
	switch kind {
	case 0:
		return nil
	case 1:
		return g.n(2) != 0
	case 2:
		return g.n(256) - 128
	case 3:
		return int64(1)<<53 + int64(g.n(256))
	case 4:
		return []float64{0, -0.5, math.NaN(), math.Inf(1), math.MaxFloat64}[g.n(5)]
	case 5:
		return float32(g.n(256)) / 3
	case 6:
		return g.text()
	case 7:
		return []byte(g.text())
	case 8:
		return json.Number([]string{"0", "1e1000", "-3", "invalid"}[g.n(4)])
	case 9:
		return new(big.Int).Lsh(big.NewInt(1), uint(g.n(256)))
	case 10:
		v := make([]any, g.n(5))
		for i := range v {
			v[i] = g.goValue(depth + 1)
		}
		return v
	case 11:
		m := map[string]any{}
		for range g.n(5) {
			m[g.text()] = g.goValue(depth + 1)
		}
		return m
	case 12:
		return &contextFuzzStruct{A: g.goValue(depth + 1), Tagged: g.text(), N: g.n(256)}
	case 13:
		v := g.goValue(depth + 1)
		return &v
	case 14:
		m := map[string]any{"a": g.n(256)}
		m["self"] = m
		return m
	case 15:
		v := []any{nil, g.n(256)}
		v[0] = v
		return v
	case 16:
		return json.RawMessage(g.text())
	case 17:
		return make(chan int)
	case 18:
		return [2]any{g.goValue(depth + 1), g.goValue(depth + 1)}
	default:
		return map[int]any{g.n(256): g.goValue(depth + 1)}
	}
}

func (g *contextFuzzGen) tree(depth int) *lisp.LVal {
	kind := g.n(14)
	if depth >= 5 {
		kind %= 6
	}
	switch kind {
	case 0:
		return lisp.Nil()
	case 1:
		return lisp.Int(g.n(256) - 128)
	case 2:
		return lisp.String(g.text())
	case 3:
		return lisp.Float([]float64{0, 1.5, math.NaN(), math.Inf(-1)}[g.n(4)])
	case 4:
		return lisp.Symbol([]string{lisp.TrueSymbol, lisp.FalseSymbol, "json:null", "symbol"}[g.n(4)])
	case 5:
		v := lisp.Native(g.goValue(0))
		g.natives = append(g.natives, v)
		return v
	case 6:
		cells := make([]*lisp.LVal, g.n(5))
		for i := range cells {
			cells[i] = g.tree(depth + 1)
		}
		if g.n(2) == 0 {
			return lisp.QExpr(cells)
		}
		return lisp.Array(nil, cells)
	case 7:
		m := lisp.SortedMap()
		for i := range g.n(5) {
			m.MapSetString(strconv.Itoa(i)+g.text(), g.tree(depth+1))
		}
		return m
	case 8:
		return scalarArrays(g.tree(depth+1), 1+g.n(128))
	case 9:
		return &lisp.LVal{Type: lisp.LQuote, Cells: []*lisp.LVal{g.tree(depth + 1)}}
	case 10:
		if len(g.natives) > 0 {
			return g.natives[g.n(len(g.natives))]
		}
		return lisp.Bytes([]byte(g.text()))
	case 11:
		return &lisp.LVal{Type: lisp.LTaggedVal, Str: "fuzz", Cells: []*lisp.LVal{g.tree(depth + 1)}}
	case 12:
		return lisp.Array(lisp.QExpr([]*lisp.LVal{lisp.Int(1), lisp.Int(1)}), []*lisp.LVal{g.tree(depth + 1)})
	default:
		v := lisp.QExpr([]*lisp.LVal{nil})
		v.Cells[0] = v
		return v
	}
}
