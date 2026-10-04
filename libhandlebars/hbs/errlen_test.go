// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"reflect"
	"strings"
	"testing"

	"github.com/luthersystems/svc/libhandlebars/hbs/internal/ast"
	"github.com/stretchr/testify/require"
)

// TestDumpLen: dumpLen is exactly len(String()) for every node of templates
// using every node type.
func TestDumpLen(t *testing.T) {
	long := strings.Repeat("é", 300)
	tpls := []string{
		`a {{b}} {{! c }} {{!-- d --}} {{{e}}} {{> p}} {{> (lookup x "y") z k=1}}`,
		`{{#if (eq a 1.5 true "s" -2 1e3)}}x{{else if b}}y{{else}}z{{/if}}`,
		`{{#each items as |v i|}}{{@index}}{{../name}}{{this.x}}{{[a b].c}}{{/each}}`,
		`{{helper a=1 b="two" c=(sub d e=false) f=g.h}} {{^x}}n{{/x}}`,
		`{{` + long + `}} {{"` + long + `"}} {{> ` + long + `}} ` + long,
		`{{1e-300}} {{123456789012345678901234567890}} {{-0.5}} {{true}} {{false}}`,
	}
	n := 0
	for _, src := range tpls {
		p, err := Parse(src, DefaultLimits())
		require.NoError(t, err, src)
		walkNodes(reflect.ValueOf(p.ast), func(node ast.Node) {
			n++
			require.Equal(t, len(node.String()), dumpLen(node), "%T %s", node, node)
		})
	}
	require.Greater(t, n, 100)
	require.Equal(t, len("%!s(<nil>)"), dumpLen(nil))
}

var nodeType = reflect.TypeFor[ast.Node]()

// walkNodes calls f on every ast.Node reachable from v.
func walkNodes(v reflect.Value, f func(ast.Node)) {
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return
		}
		if v.Type().Implements(nodeType) {
			if node, ok := v.Interface().(ast.Node); ok && v.Kind() == reflect.Pointer {
				f(node)
			}
		}
		walkNodes(v.Elem(), f)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				walkNodes(v.Field(i), f)
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			walkNodes(v.Index(i), f)
		}
	default:
	}
}
