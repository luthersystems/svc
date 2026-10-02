package hbs

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/luthersystems/svc/libhandlebars/hbs/ast"
	refast "github.com/luthersystems/svc/libhandlebars/internal/raymondref/ast"
	refparser "github.com/luthersystems/svc/libhandlebars/internal/raymondref/parser"
)

// dumpValue prints every exported field of an AST, by field name in sorted
// order, so trees from the engine and from raymondref compare equal exactly
// when they hold the same data (Print omits positions, strip flags and the
// pre-whitespace text).
func dumpValue(b *strings.Builder, v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		if v.Kind() == reflect.Pointer {
			b.WriteString("&")
		}
		dumpValue(b, v.Elem())
	case reflect.Struct:
		t := v.Type()
		var names []string
		for i := range t.NumField() {
			if t.Field(i).IsExported() {
				names = append(names, t.Field(i).Name)
			}
		}
		sort.Strings(names)
		b.WriteString(t.Name())
		b.WriteString("{")
		for _, n := range names {
			b.WriteString(n)
			b.WriteString(":")
			dumpValue(b, v.FieldByName(n))
			b.WriteString(" ")
		}
		b.WriteString("}")
	case reflect.Slice:
		if v.IsNil() {
			b.WriteString("nilslice")
			return
		}
		b.WriteString("[")
		for i := range v.Len() {
			dumpValue(b, v.Index(i))
			b.WriteString(",")
		}
		b.WriteString("]")
	case reflect.String:
		fmt.Fprintf(b, "%q", v.String())
	default:
		fmt.Fprintf(b, "%v", v.Interface())
	}
}

func dump(node any) string {
	var b strings.Builder
	dumpValue(&b, reflect.ValueOf(node))
	return b.String()
}

// parseResult is one parser's verdict on a template.
type parseResult struct {
	print string // ast.Print output
	tree  string // dump of every field
	err   string // error text
	panic string // reference only: a recovered panic
}

func refParse(src string) parseResult {
	var res parseResult
	func() {
		defer func() {
			if e := recover(); e != nil {
				res = parseResult{panic: fmt.Sprint(e)}
			}
		}()
		prog, err := refparser.Parse(src)
		if err != nil {
			res = parseResult{err: err.Error()}
			return
		}
		res = parseResult{tree: dump(prog)}
		if len(src) <= refPrintMaxBytes {
			res.print = refast.Print(prog)
		}
	}()
	return res
}

// refPrintMaxBytes bounds the templates whose raymond Print is compared:
// raymond's Print is quadratic (about 30s for a 46KB template). Larger
// templates are compared by tree dump, which covers every field Print shows.
const refPrintMaxBytes = 16 << 10

// engineParse parses with the production limits. ok is false when the
// template is over a limit, where raymond's behaviour is not reproduced.
func engineParse(t testing.TB, src string) (parseResult, bool) {
	prog, err := Parse(src, DefaultLimits)
	if err != nil {
		var herr *Error
		if !errors.As(err, &herr) {
			t.Fatalf("Parse returned %T, want *Error", err)
		}
		if herr.Kind == KindLimit {
			return parseResult{}, false
		}
		if herr.Kind != KindParse {
			t.Fatalf("Parse error kind %d, want KindParse", herr.Kind)
		}
		return parseResult{err: herr.Msg}, true
	}
	if prog.SourceLen() != len(src) {
		t.Fatalf("SourceLen %d, want %d", prog.SourceLen(), len(src))
	}
	return parseResult{print: ast.Print(prog.AST()), tree: dump(prog.AST())}, true
}

// checkSame fails t when the engine and raymondref disagree on src.
func checkSame(t testing.TB, src string) {
	t.Helper()
	got, ok := engineParse(t, src)
	if !ok {
		return
	}
	want := refParse(src)
	if want.panic != "" {
		// raymond crashed; the engine must at least fail cleanly
		if got.err == "" {
			t.Fatalf("template %q: raymond panicked (%s), engine parsed it", src, want.panic)
		}
		return
	}
	if got.err != want.err {
		t.Fatalf("template %q: error\n got: %q\nwant: %q", src, got.err, want.err)
	}
	if len(src) <= refPrintMaxBytes && got.print != want.print {
		t.Fatalf("template %q: Print\n got: %q\nwant: %q", src, got.print, want.print)
	}
	if got.tree != want.tree {
		t.Fatalf("template %q: tree\n got: %s\nwant: %s", src, got.tree, want.tree)
	}
}

// parseFragments are templates and template pieces covering every token,
// whitespace rule and error path of the lexer and parser.
var parseFragments = []string{
	"", "x", "hello world", "\n", " \n ", "\r\n", "\t\f\r\n",
	"{{x}}", "{{ x }}", "{{x.y}}", "{{x/y}}", "{{x.[y z]}}", "{{x.[5]}}", "{{x.5}}",
	"{{[a b]}}", "{{[a\nb]}}", "{{[unterminated", "{{../x}}", "{{../../x.y}}", "{{./x}}",
	"{{this}}", "{{this.x}}", "{{x.this}}", "{{x/..}}", "{{x/.}}", "{{.}}", "{{..}}", "{{...}}",
	"{{@index}}", "{{@root.x}}", "{{@this}}", "{{@.}}", "{{@../index}}", "{{@}}",
	"{{{x}}}", "{{{ x }}}", "{{{x}}", "{{x}}}", "{{&x}}", "{{~&x~}}", "{{{x}~}}", "{{~{x}~}}",
	"{{x}}}}", "{{~x~}}", "{{~ x ~}}", "{{x~}}", "{{~x}}",
	"{{f a b}}", "{{f a b c=d}}", "{{f a=b c=d}}", "{{f a=(g b) c=1}}", "{{f k=}}", "{{f =x}}",
	"{{f (g a)}}", "{{f (g (h a) b)}}", "{{(f a)}}", "{{f (g a}}", "{{f g a)}}", "{{f ()}}",
	"{{f \"s\"}}", "{{f 's'}}", "{{f \"a\\\"b\"}}", "{{f 'a\\'b'}}", "{{f \"a\\\\\"}}", "{{f \"a\nb\"}}",
	"{{f \"unterminated}}", "{{f \"\"}}", "{{f ''}}", "{{\"s\"}}", "{{'s' x}}",
	"{{f 1}}", "{{f -1}}", "{{f +1}}", "{{f 1.5}}", "{{f 1e3}}", "{{f 1E-3}}", "{{f 0x10}}",
	"{{f 0X1f}}", "{{f 1i}}", "{{f 1+2i}}", "{{f 1-}}", "{{f 1a}}", "{{f 089}}", "{{f .5}}",
	"{{f 99999999999999999999}}", "{{f -}}", "{{f -x}}", "{{f 1.2.3}}", "{{1}}", "{{f 0}}",
	"{{f true}}", "{{f false}}", "{{true}}", "{{false}}", "{{f true)}}", "{{truex}}", "{{f trueish}}",
	"{{f null}}", "{{f undefined}}", "{{true~}}", "{{f true}}x",
	"{{#if x}}y{{/if}}", "{{#if x}}y{{else}}z{{/if}}", "{{#if x}}y{{^}}z{{/if}}",
	"{{#if x}}y{{else if z}}w{{/if}}", "{{#if x}}y{{else if z}}w{{else}}v{{/if}}",
	"{{#if a}}1{{else if b}}2{{else if c}}3{{else}}4{{/if}}", "{{#if x}}{{/unless}}",
	"{{#if x}}", "{{/if}}", "{{#if}}{{/if}}", "{{#}}{{/}}", "{{#if x}}{{else}}{{else}}{{/if}}",
	"{{^x}}y{{/x}}", "{{^x}}y{{else}}z{{/x}}", "{{^x}}y{{^}}z{{/x}}", "{{^}}", "{{else}}", "{{ else }}",
	"{{elsewhere}}", "{{else x}}", "{{#x}}{{elsewhere}}{{/x}}", "{{#x}}{{else~}}{{/x}}",
	"{{#each items as |item i|}}{{item}}{{/each}}", "{{#each items as |item|}}x{{/each}}",
	"{{#each items as ||}}x{{/each}}", "{{#each items as |a b}}x{{/each}}", "{{#each as |x|}}{{/each}}",
	"{{#with x as |y|}}{{y}}{{/with}}", "{{#each x as   |y|}}{{/each}}", "{{as |x|}}", "{{x as}}",
	"{{#x.y}}{{/x.y}}", "{{#x.y}}{{/x}}", "{{#\"s\"}}{{/\"s\"}}", "{{#1}}{{/1}}", "{{#true}}{{/true}}",
	"{{#x}}{{/x y}}", "{{#x}}{{/(x)}}", "{{#(x)}}{{/x}}", "{{#x}}{{/@x}}", "{{#@x}}{{/@x}}",
	"{{#if (eq a b)}}y{{/if}}", "{{#each (f a) as |x|}}{{/each}}",
	"{{{{raw}}}}{{x}}{{{{/raw}}}}", "{{{{raw}}}} {{{{/raw}}}}", "{{{{raw}}}}{{{{/raw}}}}",
	"{{{{raw}}}}x{{{{/other}}}}", "{{{{raw}}}}x", "{{{{raw a b=c}}}}x{{{{/raw}}}}", "{{{{/raw}}}}",
	"{{{{raw}}}}x{{{{/raw x}}}}",
	"{{> p}}", "{{> p x}}", "{{> p x a=b}}", "{{> (f x)}}", "{{>p}}", "  {{> p}}  \n", "{{> \"s\"}}",
	"{{#*inline \"p\"}}x{{/inline}}", "{{#> p}}x{{/p}}",
	"{{! comment }}", "{{!comment}}", "{{!-- comment --}}", "{{!--}}", "{{!-- }} --}}", "{{!--x}}",
	"{{!-}}", "{{!---}}", "{{!-- a --~}}", "{{~!-- a --}}", "{{~! a ~}}", "{{! a", "{{!-- a",
	"{{!-- a --}", "{{!    }}", "{{!--    --}}", "{{!-- --  --}}", "{{! a }} b", " {{! a }} \n",
	"\\{{x}}", "\\\\{{x}}", "\\{{{x}}}", "a\\{{x", "\\", "\\\\", "{", "}", "}}", "{{", "{{{", "{{}}",
	"{{ }}", "{{~}}", "{{~~}}", "{{x y z}", "{{x\ny}}", "{{x\n}}\n{{y z\n}}",
	"{{x=y}}", "{{x | y}}", "{{x|}}", "{{=}}", "{{!}}", "{{#}}", "{{/}}", "{{>}}", "{{^}}x",
	"{{x;}}", "{{x,y}}", "{{x*y}}", "{{x<y}}", "{{x#}}", "{{x%}}", "{{x`}}", "{{x^}}", "{{x]}}",
	"{{x\ty}}", "{{x\ry}}", "{{x\fy}}", "{{é}}", "{{ }}", "{{x\xff}}", "\xff{{x}}", "{{\xc3}}",
	"日本語 {{名前}}", "{{f \"日本\"}}",
	"{{#if x}}\n  y\n{{/if}}\n", "  {{#if x}}  \n  y\n  {{else}}  \n  z\n  {{/if}}  \n",
	"{{#if x}}\r\ny\r\n{{/if}}\r\n", "a\n  {{#if x}}\n  b\n  {{/if}}\nc",
	"{{#if x}}\n{{#if y}}\nz\n{{/if}}\n{{/if}}\n", "\n{{#if x}}\n\n{{else}}\n\n{{/if}}\n\n",
	"{{#if a}}\n1\n{{else if b}}\n2\n{{else}}\n3\n{{/if}}\n",
	"  {{~#if x~}}  y  {{~else~}}  z  {{~/if~}}  ", "x {{~ y ~}} z", "x\n\n{{~y~}}\n\nz",
	"{{#if x~}}\n  a\n{{~/if}}", "{{#if x}}  {{~^~}}  {{/if}}", " {{!a}} ", "\n{{!a}}\n",
	"x {{!a}}\n", "  {{^x}}\n  y\n  {{/x}}\n", "{{#x}}\n{{/x}}", "{{#x}}{{/x}}\n",
	"\t{{#x}}\t\n\t{{/x}}\t\n", "\f{{#x}}\f\n", " \r\n{{#x}}\r\n{{/x}}",
	"<p>{{#each items}}\n  <li>{{name}}</li>\n{{/each}}</p>\n",
}

// parseCases expands the fragments: every fragment alone, every fragment
// in a whitespace frame, and a fixed pseudo-random sample of 2- to
// 4-fragment concatenations.
func parseCases() []string {
	var cases []string
	cases = append(cases, parseFragments...)
	frames := [][2]string{{"\n", "\n"}, {"  ", "  \n"}, {"a\n  ", "\n  b"}, {"{{#if c}}\n", "\n{{/if}}"},
		{"{{#each l}}", "{{else}}\n{{/each}}"}, {"{{#if c}}{{else if d}}\n", "\n{{/if}}"}}
	for _, f := range parseFragments {
		for _, fr := range frames {
			cases = append(cases, fr[0]+f+fr[1])
		}
	}
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // a fixed, reproducible test sample
	for range 6000 {
		n := 2 + r.IntN(3)
		var b strings.Builder
		for range n {
			b.WriteString(parseFragments[r.IntN(len(parseFragments))])
			if r.IntN(3) == 0 {
				b.WriteString([]string{"\n", " ", "  \n", "\r\n", "\t"}[r.IntN(5)])
			}
		}
		cases = append(cases, b.String())
	}
	return cases
}

func TestParseMatchesRaymond(t *testing.T) {
	cases := parseCases()
	for _, src := range cases {
		checkSame(t, src)
	}
	t.Logf("%d templates", len(cases))
}

// TestParseMatchesRaymondCorpus compares every .html/.hbs file under
// $HBS_PARSE_CORPUS_DIR. It is how templates kept outside the repository
// are checked; it is skipped when the variable is unset.
func TestParseMatchesRaymondCorpus(t *testing.T) {
	dir := os.Getenv("HBS_PARSE_CORPUS_DIR")
	if dir == "" {
		t.Skip("HBS_PARSE_CORPUS_DIR not set")
	}
	n := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error { //nolint:gosec // walks the directory the developer names
		if err != nil {
			return err
		}
		if ext := filepath.Ext(path); d.IsDir() || (ext != ".html" && ext != ".hbs") {
			return nil
		}
		b, err := os.ReadFile(path) //nolint:gosec // reads the directory the developer names
		if err != nil {
			return err
		}
		n++
		checkSame(t, string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d corpus templates", n)
}

func FuzzParse(f *testing.F) {
	for _, src := range parseFragments {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		checkSame(t, src)
	})
}
