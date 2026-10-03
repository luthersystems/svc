// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"strings"
	"testing"

	refparser "github.com/luthersystems/svc/libhandlebars/internal/raymondref/parser"
)

// benchLabel is a small one-line template.
const benchLabel = `{{title}} {{first-name}} {{last-name}}, {{to-upper postcode}}`

// benchLetter builds a letter of about 5KB: paragraphs with fields,
// conditionals and a list.
func benchLetter() string {
	var b strings.Builder
	b.WriteString("<html><body>\n<p>Dear {{title}} {{last-name}},</p>\n")
	for i := 0; b.Len() < 5<<10; i++ {
		b.WriteString("<p>Thank you for your enquiry of {{format-date date \"2006-01-02\"}}. ")
		b.WriteString("We have reviewed the details you provided and set out our findings below.</p>\n")
		b.WriteString("{{#if (eq status \"open\")}}\n  <p>Your reference is {{ref}}.</p>\n{{else}}\n  <p>Closed.</p>\n{{/if}}\n")
		b.WriteString("<ul>\n{{#each items as |item idx|}}\n  <li>{{idx}}: {{item.name}} - {{prettyp-num item.amount}}</li>\n{{/each}}\n</ul>\n")
		b.WriteString("{{!-- section --}}\n")
	}
	b.WriteString("<p>Yours sincerely,</p>\n</body></html>\n")
	return b.String()
}

// benchReport builds a report of about 100KB with nested blocks, else-if
// chains, subexpressions and whitespace control.
func benchReport() string {
	var b strings.Builder
	for i := 0; b.Len() < 100<<10; i++ {
		b.WriteString("<section>\n  <h2>{{section.title}}</h2>\n")
		b.WriteString("  {{#each section.rows as |row|}}\n")
		b.WriteString("    {{#if (and row.visible (gt (len row.values) 0))}}\n")
		b.WriteString("      <tr>{{#each row.values}}<td>{{~format-num this 2~}}</td>{{/each}}</tr>\n")
		b.WriteString("    {{else if (eq row.kind \"note\")}}\n      <tr><td>{{row.note}}</td></tr>\n")
		b.WriteString("    {{else}}\n      <tr><td>{{default row.label \"-\"}}</td></tr>\n    {{/if}}\n")
		b.WriteString("  {{/each}}\n")
		b.WriteString("  {{#with section.summary}}\n    <p>{{total}} of {{count}} ({{percent (div total count) 1}}%)</p>\n  {{/with}}\n")
		b.WriteString("</section>\n")
	}
	return b.String()
}

var benchTemplates = []struct {
	name string
	src  string
}{
	{"label", benchLabel},
	{"letter5KB", benchLetter()},
	{"report100KB", benchReport()},
}

func BenchmarkParse(b *testing.B) {
	for _, tc := range benchTemplates {
		b.Run(tc.name+"/engine", func(b *testing.B) {
			b.SetBytes(int64(len(tc.src)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Parse(tc.src, DefaultLimits()); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/raymond", func(b *testing.B) {
			b.SetBytes(int64(len(tc.src)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := refparser.Parse(tc.src); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/cached", func(b *testing.B) {
			b.SetBytes(int64(len(tc.src)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := ParseCached(tc.src, DefaultLimits()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestBenchTemplatesMatchRaymond keeps the benchmark inputs valid.
func TestBenchTemplatesMatchRaymond(t *testing.T) {
	for _, tc := range benchTemplates {
		checkSame(t, tc.src)
	}
}
