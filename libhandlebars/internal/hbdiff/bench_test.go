// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbdiff

import (
	"flag"
	"fmt"
	"strings"
	"testing"
)

// The benchmarks compare the reference pipeline (hbref: JSON decode,
// raymond parse, svc's helpers, exec) with the candidate (hbs.FromJSON,
// Parse, Render) end to end, on the checked-in corpora. Each op is one
// pass over every case. Compare with benchstat:
//
//	go test -run '^$' -bench . -count 10 ./libhandlebars/internal/hbdiff

var bigRef = flag.Bool("bigref", false, "also run the reference on the 9 MB large-output case (about 30 s per op)")

type benchEngine struct {
	name   string
	render Candidate
	parse  ParseCandidate
}

func benchEngines() []benchEngine {
	return []benchEngine{
		{name: "ref", render: Ref, parse: RefParse},
		{name: "cand", render: DefaultCandidate, parse: DefaultParseCandidate},
	}
}

func benchCorpusCases(b *testing.B) []Case {
	b.Helper()
	cases, groups := loadCorpus(b)
	var all []Case
	for _, g := range groups {
		all = append(all, cases[g]...)
	}
	return all
}

// distinctTemplates returns each template of cases once.
func distinctTemplates(cases []Case) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cases {
		if !seen[c.Template] {
			seen[c.Template] = true
			out = append(out, c.Template)
		}
	}
	return out
}

func benchParse(b *testing.B, tpls []string) {
	b.Helper()
	size := 0
	for _, t := range tpls {
		size += len(t)
	}
	for _, e := range benchEngines() {
		b.Run(e.name, func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				for _, t := range tpls {
					e.parse(t)
				}
			}
		})
	}
}

func benchRender(b *testing.B, cases []Case) {
	b.Helper()
	for _, e := range benchEngines() {
		b.Run(e.name, func(b *testing.B) {
			out := 0
			for _, c := range cases {
				out += len(e.render(c.Template, c.Context).Out)
			}
			b.SetBytes(int64(out))
			b.ReportAllocs()
			for b.Loop() {
				for _, c := range cases {
					e.render(c.Template, c.Context)
				}
			}
		})
	}
}

// BenchmarkCorpusParse runs must-parse over every distinct template of
// the literal corpus.
func BenchmarkCorpusParse(b *testing.B) {
	benchParse(b, distinctTemplates(benchCorpusCases(b)))
}

// BenchmarkCorpusRender renders every case of the literal corpus.
func BenchmarkCorpusRender(b *testing.B) {
	benchRender(b, benchCorpusCases(b))
}

// BenchmarkShapesParse runs must-parse over every shape skeleton.
func BenchmarkShapesParse(b *testing.B) {
	benchParse(b, distinctTemplates(loadShapes(b)))
}

// BenchmarkShapesRender renders every shape case.
func BenchmarkShapesRender(b *testing.B) {
	benchRender(b, loadShapes(b))
}

// BenchmarkLargeOutput renders a nested #each that writes n*n bytes: the
// quadratic output build of D3 in the reference (9 MB took about 30 s),
// linear in the candidate.
func BenchmarkLargeOutput(b *testing.B) {
	const tpl = "{{#each a}}{{#each ../a}}x{{/each}}{{/each}}"
	for _, n := range []int{300, 1000, 3000} {
		ctx := []byte(`{"a":[` + strings.TrimSuffix(strings.Repeat("0,", n), ",") + `]}`)
		for _, e := range benchEngines() {
			b.Run(fmt.Sprintf("%s/n=%d", e.name, n), func(b *testing.B) {
				if e.name == "ref" && n >= 3000 && !*bigRef {
					b.Skip("reference takes about 30 s per op; run with -bigref")
				}
				if r := e.render(tpl, ctx); r.ErrKind != KindNone || len(r.Out) != n*n {
					b.Fatalf("got %s, want %d bytes of output", describe(r), n*n)
				}
				b.SetBytes(int64(n * n))
				b.ReportAllocs()
				for b.Loop() {
					e.render(tpl, ctx)
				}
			})
		}
	}
}
