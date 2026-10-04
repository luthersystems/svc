// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package shape

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	raymond "github.com/luthersystems/svc/libhandlebars/internal/raymondref"
	"github.com/luthersystems/svc/libhandlebars/internal/raymondref/lexer"
)

const sample = `<h1>Statement for {{customer.name}}</h1>
  {{#each accounts as |acct idx|}}
  <p>{{idx}}: {{{acct.label}}} {{prettyp-num-en (div acct.balance 100)}}</p>
    {{~#if (eq acct.kind "savings")~}} S {{~else if acct.closed~}} C {{~else~}} O {{~/if}}
  {{../customer.name}} {{@index}} {{[odd key]}}
  {{/each}}
{{#select from=rows where="status=open"}}{{amount}}{{/select}}
{{global "labels" key="open" val="Open now"}}{{global "labels" key=state}}
{{! private note }}\{{literal}} {{date-add-months start 12345}}
`

func kinds(t *testing.T, src string) []lexer.TokenKind {
	t.Helper()
	var out []lexer.TokenKind
	for _, tok := range lexer.Collect(src) {
		out = append(out, tok.Kind)
	}
	return out
}

func TestSkeletonKeepsStructure(t *testing.T) {
	a := NewAnonymizer([]byte("test salt, not secret"))
	skel, err := a.Skeleton(sample)
	require.NoError(t, err)
	require.Equal(t, kinds(t, sample), kinds(t, skel))
	require.Equal(t, strings.Count(sample, "\n"), strings.Count(skel, "\n"))
	_, err = raymond.Parse(skel)
	require.NoError(t, err)
	require.NoError(t, CheckSkeleton(skel))
	require.Error(t, CheckSkeleton(sample))
	require.Empty(t, Leaks(SourceTokens(sample), skel))

	for _, kept := range []string{"#each", "as |", "../", "{{{", "~#if", "else if", "prettyp-num-en", "(div", "@index", "where=\"", "global \"", "key=", "val=", "\\{{"} {
		require.Contains(t, skel, kept)
	}
	require.Contains(t, skel, a.Ident("acct")+"."+a.Ident("label"))
	require.Contains(t, skel, "["+a.Ident("odd key")+"]")
	require.Contains(t, skel, `where="`+a.Ident("status")+"="+a.String("open")+`"`)
	// key="open" and the where value share one mapping, so lookups still meet.
	require.Contains(t, skel, `key="`+a.String("open")+`"`)

	again, err := a.Skeleton(sample)
	require.NoError(t, err)
	require.Equal(t, skel, again, "renaming is stable within a run")
}

func TestStringClasses(t *testing.T) {
	a := NewAnonymizer([]byte("salt"))
	require.Empty(t, a.String(""))
	require.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, a.String("2021-07-09"))
	require.Regexp(t, `^s[0-9a-f]{6,}$`, a.String("hello"))
	require.Equal(t, "12", a.String("12"))
	n := a.Number("123456.75")
	require.Regexp(t, `^[1-9]\d{5}\.\d\d$`, n)
	require.Equal(t, "-1", a.Number("-1"))
}

func TestFiller(t *testing.T) {
	require.Equal(t, "  xxxxxxxxxxxxxxxx  \n\txxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n", Filler("  Dear Ms Smith,  \n\tYour balance is due on Friday.\n"))
	require.Equal(t, "x { x0 } \\", Filler("a { b1 } \\"))
}

func TestInferAndGenerate(t *testing.T) {
	schema, err := Infer(`{{#each items}}{{name}}{{gt price 2}}{{/each}}{{#with owner}}{{date-beautify dob}}{{/with}}{{#select from=rows where="k=v"}}{{n}}{{/select}}{{len tags}}`)
	require.NoError(t, err)
	items := schema.Fields["items"]
	require.NotZero(t, items.Kind&KArray)
	require.NotZero(t, items.Elem.Fields["price"].Kind&KNumber)
	require.NotZero(t, schema.Fields["owner"].Fields["dob"].Kind&KDate)
	require.Equal(t, []string{"v"}, schema.Fields["rows"].Elem.Fields["k"].Strings)
	require.NotZero(t, schema.Fields["tags"].Kind&KArray)

	a := Generate(schema, 10, 7, nil)
	b := Generate(schema, 10, 7, nil)
	require.Equal(t, a, b, "generation is deterministic")
	require.JSONEq(t, `{}`, string(a[ClassMissing]))
	require.Contains(t, string(a[ClassTypical]), `"k":"v"`)
}
