package hbs

// TEMPORARY (delete at integration): the parser stream defines Program in
// parse.go. This minimal copy lets the evaluator build and test on its own
// branch. render.go reads only p.prog.

import "github.com/luthersystems/svc/libhandlebars/hbs/ast"

// Program is a parsed template.
type Program struct {
	prog *ast.Program
}
