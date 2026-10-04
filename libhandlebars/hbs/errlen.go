// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"strconv"

	"github.com/luthersystems/svc/libhandlebars/hbs/internal/ast"
)

// dumpLen is len(fmt.Sprint(n)), the node dump in an evaluation error,
// computed without building it: a node can hold a long name or literal,
// and the dump can repeat it.
func dumpLen(n ast.Node) int {
	digits := func(pos int) int { return len(strconv.Itoa(pos)) }
	switch x := n.(type) {
	case nil:
		return len("%!s(<nil>)")
	case *ast.Program:
		return len("Program{Pos: }") + digits(x.Pos)
	case *ast.MustacheStatement:
		return len("Mustache{Pos: }") + digits(x.Pos)
	case *ast.BlockStatement:
		return len("Block{Pos: }") + digits(x.Pos)
	case *ast.PartialStatement:
		return len("Partial{Name:, Pos:}") + dumpLen(x.Name) + digits(x.Pos)
	case *ast.ContentStatement:
		return len("Content{Value:'', Pos:}") + len(x.Value) + digits(x.Pos)
	case *ast.CommentStatement:
		return len("Comment{Value:'', Pos:}") + len(x.Value) + digits(x.Pos)
	case *ast.Expression:
		return len("Expr{Path:, Pos:}") + dumpLen(x.Path) + digits(x.Pos)
	case *ast.SubExpression:
		return len("Sexp{Path:, Pos:}") + dumpLen(x.Expression.Path) + digits(x.Pos)
	case *ast.PathExpression:
		return len("Path{Original:'', Pos:}") + len(x.Original) + digits(x.Pos)
	case *ast.StringLiteral:
		return len("String{Value:'', Pos:}") + len(x.Value) + digits(x.Pos)
	case *ast.BooleanLiteral:
		return len("Boolean{Value:, Pos:}") + len(x.Canonical()) + digits(x.Pos)
	case *ast.NumberLiteral:
		return len("Number{Value:, Pos:}") + len(x.Canonical()) + digits(x.Pos)
	case *ast.Hash:
		n := len("Hash{[], Pos:}") + 2*digits(x.Pos)
		for i, p := range x.Pairs {
			if i > 0 {
				n += 2
			}
			n += dumpLen(p)
		}
		return n
	case *ast.HashPair:
		return len(x.Key) + 1 + dumpLen(x.Val)
	default:
		return len(n.String())
	}
}
