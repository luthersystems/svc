package hbs_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/luthersystems/svc/libhandlebars/hbs"
	raymond "github.com/luthersystems/svc/libhandlebars/internal/raymondref"
)

// Synthetic benchmark inputs, shaped like document templates: no production
// text.

func benchLetter() (string, string) {
	var b strings.Builder
	for i := range 40 {
		fmt.Fprintf(&b, "<p>Section %d for {{possessive name}} on {{date-beautify date}}.</p>\n", i)
		b.WriteString("{{#if (gt amount 100)}}<b>{{prettyp-num-en amount}}</b>{{else}}{{to-str amount}}{{/if}}\n")
		b.WriteString("{{#each items}}<li>{{@index}} {{label}} {{#if on}}yes{{else}}no{{/if}}</li>{{/each}}\n")
	}
	ctx := `{"name": "Alex", "date": "2024-02-29", "amount": 1234.5,
		"items": [{"label": "a<b", "on": true}, {"label": "c", "on": false}, {"label": "d&e"}]}`
	return b.String(), ctx
}

func benchTable() (string, string) {
	tpl := `<table>{{#each rows}}<tr>{{#each this}}<td>{{@key}}={{this}}</td>{{/each}}<td>{{plus a=c0 b=c1}}</td></tr>{{/each}}</table>`
	rows := make([]map[string]any, 1000)
	for i := range rows {
		r := map[string]any{}
		for j := range 10 {
			r[fmt.Sprintf("c%d", j)] = float64(i * j)
		}
		rows[i] = r
	}
	b, err := json.Marshal(map[string]any{"rows": rows})
	if err != nil {
		panic(err)
	}
	return tpl, string(b)
}

var benchInputs = []struct {
	name string
	in   func() (string, string)
}{
	{"Small", func() (string, string) { return `Hello {{name}}, you owe {{to-str n}}!`, `{"name": "Alex", "n": 3}` }},
	{"Letter", benchLetter},
	{"Table", benchTable},
	{"NestedEach300", func() (string, string) { return nestedEach(300) }},
}

func BenchmarkRender(b *testing.B) {
	for _, in := range benchInputs {
		tpl, ctxJSON := in.in()
		b.Run(in.name+"/engine", func(b *testing.B) {
			p := mustParse(b, tpl)
			ctx := mustCtx(b, ctxJSON)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := p.Render(ctx, hbs.Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(in.name+"/reference", func(b *testing.B) {
			t, err := raymond.Parse(tpl)
			if err != nil {
				b.Fatal(err)
			}
			refAddHelpers(t)
			var m map[string]any
			if err := json.Unmarshal([]byte(ctxJSON), &m); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := t.Exec(m); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestBenchInputsAgree(t *testing.T) {
	for _, in := range benchInputs {
		tpl, ctx := in.in()
		if d := diff(tpl, ctx); d != "" {
			t.Errorf("%s: %s", in.name, d)
		}
	}
}
