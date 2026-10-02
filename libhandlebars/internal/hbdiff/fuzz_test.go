package hbdiff

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"
)

// FuzzDiff renders fuzzer-made templates and contexts through the
// reference and, when built in, the candidate. The first input byte picks
// the mode: even bytes drive a grammar-aware generator (every construct
// raymond lexes, nesting to depth 8); odd bytes use the rest of the input
// as a raw template, seeded from the literal corpus, for byte mutation.
// The context is generated from the same input.
//
//	go test ./libhandlebars/internal/hbdiff -run '^$' -fuzz FuzzDiff -fuzztime 30s
//
// The reference's lexer leaks a goroutine per parse error (fixed in the
// engine), so a long fuzz run grows in memory; keep -fuzztime bounded.
func FuzzDiff(f *testing.F) {
	cases, groups, err := LoadCorpusDir(corpusDir)
	if err != nil {
		f.Fatal(err)
	}
	seen := map[string]bool{}
	for _, g := range groups {
		for _, c := range cases[g] {
			if seen[c.Template] || len(c.Template) > maxFuzzTemplate {
				continue
			}
			seen[c.Template] = true
			f.Add(append([]byte{1}, c.Template...))
		}
	}
	for i := range 64 {
		seed := []byte{0}
		for j := range 48 {
			seed = append(seed, byte((i*31+j*17+i*j)&0xff))
		}
		f.Add(seed)
	}
	allow, err := LoadAllowlist(allowlistFile, time.Now())
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		tpl, ctx, ok := fuzzInput(data)
		if !ok || RefFatal(tpl) {
			t.Skip()
		}
		checkFuzzCase(t, Case{Name: "fuzz", Template: tpl, Context: ctx}, allow)
	})
}

const maxFuzzTemplate = 4096

func fuzzInput(data []byte) (string, []byte, bool) {
	if len(data) == 0 {
		return "", nil, false
	}
	s := &stream{b: data[1:]}
	if data[0]%2 == 1 {
		tpl := string(data[1:])
		if len(tpl) > maxFuzzTemplate {
			return "", nil, false
		}
		// The context comes from the template bytes reversed, so a
		// mutation of either changes it.
		rev := make([]byte, len(data)-1)
		for i, c := range data[1:] {
			rev[len(rev)-1-i] = c
		}
		return tpl, genContext(&stream{b: rev}), true
	}
	g := &tplGen{s: s}
	g.program(0)
	return g.b.String(), genContext(s), true
}

func checkFuzzCase(t *testing.T, c Case, allow *Allowlist) {
	t.Helper()
	ref, alt := repeat(3, func() Result { return Ref(c.Template, c.Context) })
	sumsInMapOrder := strings.Contains(c.Template, "plus") || strings.Contains(c.Template, "minus")
	if len(alt) > 0 && !sumsInMapOrder {
		t.Fatalf("reference nondeterministic\ntemplate: %q\ncontext: %s\n%s\n%s", c.Template, c.Context, describe(ref), describe(alt[0]))
	}
	cand, calt := repeat(3, func() Result { return DefaultCandidate(c.Template, c.Context) })
	if len(calt) > 0 {
		t.Fatalf("candidate nondeterministic\ntemplate: %q\ncontext: %s", c.Template, c.Context)
	}
	m := Compare(ref, cand)
	if m == nil || allow.Match(c, ref, cand) != nil {
		return
	}
	if sumsInMapOrder {
		// Accept the candidate's sorted-order sum when the reference can
		// produce it in some map order.
		for range 200 {
			if Ref(c.Template, c.Context) == cand {
				return
			}
		}
	}
	t.Fatalf("%s\ntemplate: %q\ncontext: %s\nref:  %s\ncand: %s", m, c.Template, c.Context, describe(ref), describe(cand))
}

// stream hands out decisions from fuzz bytes; past the end every
// decision is 0, which always picks the shortest production.
type stream struct {
	b []byte
	i int
}

func (s *stream) n(k int) int {
	if k <= 1 || s.i >= len(s.b) {
		return 0
	}
	v := int(s.b[s.i])
	s.i++
	return v % k
}

func (s *stream) pick(xs []string) string { return xs[s.n(len(xs))] }

var (
	fzContent = []string{"", "a", " ", "\n", "  \n", "x y", "<p>", "&amp;", "{", "}", "\\", "\t", "\r\n", "   ", "é", "~", "}}"}
	fzPaths   = []string{"a", "b", "c", "x", "items", "m", "name", "this", ".", "..", "../a", "../../b", "a.b", "a.[0]", "items.[1].name", "[a b]", "this.a", "./a", "a/b",
		"@index", "@key", "@first", "@last", "@root.a", "@root", "@../index", "@this", "true", "false", "null", "undefined", "@unknown"}
	fzStrings = []string{`""`, `"s"`, `"k=v"`, `"name=a"`, `"2020-01-31"`, `"3"`, `"3.5"`, `"0"`, `"a\"b"`, `'q'`, `"<&>"`, `"ns"`, `"key"`, `"\\"`, `"a\\"`, `"é"`, `"+447700900123"`}
	fzNumbers = []string{"0", "1", "-1", "2", "1.5", "1.5e3", "0x10", "1i", "007", "100", "-0", "1e21", "9007199254740993", "1.", "+1", "1abc"}
	fzHelpers = []string{"eq", "len", "not", "and", "or", "gt", "gte", "lt", "lte", "times", "div", "mod",
		"date-diff-month", "is-after", "date-add-months", "to-int", "plus", "minus", "select", "global",
		"round-to-nth", "in-string-array", "prettyp-num-en", "possessive", "date-beautify",
		"date-DDMMYY-slash", "date-DDMMYYYY-slash", "date-DDMMYYYY", "format-phone-gb",
		"escape-uri-component", "to-str", "if", "unless", "each", "with", "lookup", "log", "nosuch"}
	fzBlocks   = []string{"if", "unless", "each", "with", "select", "global", "a", "items", "eq", "not", "and", "nosuch"}
	fzHashKeys = []string{"a", "b", "c", "from", "where", "key", "val", "haystack", "needle", "includeZero", "x"}
)

type tplGen struct {
	s     *stream
	b     strings.Builder
	nodes int
}

const fzMaxDepth = 8

func (g *tplGen) w(s string) { g.b.WriteString(s) }

func (g *tplGen) program(depth int) {
	for range g.s.n(6) {
		if g.nodes > 60 {
			return
		}
		g.nodes++
		g.statement(depth)
	}
}

func (g *tplGen) tilde() string {
	if g.s.n(5) == 0 {
		return "~"
	}
	return ""
}

func (g *tplGen) statement(depth int) {
	switch g.s.n(14) {
	case 0, 1, 2:
		g.w(g.s.pick(fzContent))
	case 3, 4:
		g.w("{{" + g.tilde())
		g.expr(depth)
		g.w(g.tilde() + "}}")
	case 5:
		g.w("{{" + g.tilde() + "{")
		g.expr(depth)
		g.w("}" + g.tilde() + "}}")
	case 6:
		g.w("{{&")
		g.expr(depth)
		g.w("}}")
	case 7, 8, 9:
		if depth < fzMaxDepth {
			g.block(depth)
		}
	case 10:
		g.w(g.s.pick([]string{"{{! c }}", "{{!-- c }} --}}", "{{!}}", "{{~! c ~}}", "\\{{a}}", "\\\\{{a}}", "{{> p}}", "{{{{raw}}}}{{x}}{{{{/raw}}}}", "{{#*inline \"p\"}}x{{/inline}}", "{{else}}", "{{/if}}", "{{", "}}"}))
	case 11:
		if depth < fzMaxDepth {
			p := g.s.pick(fzPaths)
			g.w("{{^" + p + "}}")
			g.program(depth + 1)
			g.w("{{/" + p + "}}")
		}
	default:
		g.w(g.s.pick([]string{"\n", "  ", "\n  "}))
	}
}

func (g *tplGen) block(depth int) {
	name := g.s.pick(fzBlocks)
	g.w("{{" + g.tilde() + "#" + name)
	g.args(depth, name)
	if g.s.n(6) == 0 {
		g.w(" as |" + g.s.pick([]string{"v", "v i", "x y"}) + "|")
	}
	g.w(g.tilde() + "}}")
	g.program(depth + 1)
	for g.s.n(3) == 0 && g.nodes < 60 {
		g.nodes++
		switch g.s.n(4) {
		case 0:
			g.w("{{" + g.tilde() + "else" + g.tilde() + "}}")
		case 1:
			g.w("{{^}}")
		default:
			g.w("{{" + g.tilde() + "else " + g.s.pick([]string{"if", "unless", "each", "with"}))
			g.args(depth, "")
			g.w(g.tilde() + "}}")
		}
		g.program(depth + 1)
	}
	if g.s.n(25) == 0 {
		name = g.s.pick(fzBlocks) // mismatched close
	}
	g.w("{{" + g.tilde() + "/" + name + g.tilde() + "}}")
}

func (g *tplGen) expr(depth int) {
	if g.s.n(3) == 0 {
		g.w(g.s.pick(fzPaths))
		return
	}
	name := g.s.pick(fzHelpers)
	g.w(name)
	g.args(depth, name)
}

func (g *tplGen) args(depth int, name string) {
	np := g.s.n(4)
	if name == "select" || name == "and" || name == "or" || name == "plus" {
		np = g.s.n(2)
	}
	for range np {
		g.w(" ")
		g.param(depth)
	}
	nh := g.s.n(4)
	switch name {
	case "select":
		g.w(" from=" + g.s.pick([]string{"items", "a", "m", "x"}) + " where=" + g.s.pick(fzStrings))
	case "global":
		g.w(" key=")
		g.param(depth)
		if g.s.n(2) == 0 {
			g.w(" val=")
			g.param(depth)
		}
	}
	for range nh {
		g.w(" " + g.s.pick(fzHashKeys) + "=")
		g.param(depth)
	}
}

func (g *tplGen) param(depth int) {
	switch g.s.n(7) {
	case 0, 1:
		g.w(g.s.pick(fzPaths))
	case 2:
		g.w(g.s.pick(fzStrings))
	case 3:
		g.w(g.s.pick(fzNumbers))
	case 4:
		g.w(g.s.pick([]string{"true", "false"}))
	default:
		if depth < fzMaxDepth {
			g.w("(")
			g.expr(depth + 1)
			g.w(")")
		} else {
			g.w("a")
		}
	}
}

var (
	fzKeys     = []string{"a", "b", "c", "x", "items", "m", "name", "k", "n", "a b", "ns", "key"}
	fzScalars  = []string{"null", "true", "false", "0", "1", "-1", "2.5", "0.1", "12.34", "1e21", "9007199254740993", `""`, `"0"`, `"v"`, `"a"`, `"k=v"`, `"2020-01-31"`, `"2024-02-29"`, `"<&>\"'"`, `"é😀"`, `"3.50"`, `"+447700900123"`}
	fzCtxDepth = 3
)

// genContext builds a JSON object context from the stream.
func genContext(s *stream) []byte {
	var b bytes.Buffer
	if s.n(16) == 15 {
		b.WriteString(s.pick([]string{"null", "[]", `"s"`, "1", "{", "{}"}))
		return b.Bytes()
	}
	genObject(s, &b, 0)
	return b.Bytes()
}

func genObject(s *stream, b *bytes.Buffer, depth int) {
	b.WriteByte('{')
	n := 1 + s.n(5)
	used := map[string]bool{}
	first := true
	for range n {
		k := s.pick(fzKeys)
		if used[k] {
			continue
		}
		used[k] = true
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(strconv.Quote(k)) // keys are plain ASCII
		b.WriteByte(':')
		genValue(s, b, depth+1)
	}
	b.WriteByte('}')
}

func genValue(s *stream, b *bytes.Buffer, depth int) {
	kind := s.n(6)
	if depth >= fzCtxDepth {
		kind = 0
	}
	switch kind {
	case 0, 1, 2:
		b.WriteString(s.pick(fzScalars))
	case 3:
		genObject(s, b, depth)
	default:
		b.WriteByte('[')
		for i := range s.n(4) {
			if i > 0 {
				b.WriteByte(',')
			}
			if s.n(2) == 0 {
				genObject(s, b, depth)
			} else {
				genValue(s, b, depth+1)
			}
		}
		b.WriteByte(']')
	}
}
