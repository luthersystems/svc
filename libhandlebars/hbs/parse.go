package hbs

import (
	"errors"

	"github.com/luthersystems/svc/libhandlebars/hbs/ast"
	"github.com/luthersystems/svc/libhandlebars/hbs/parser"
)

// Program is a parsed template. It is immutable after Parse returns: the
// AST, including its whitespace processing, is complete, and nothing in
// the engine writes to it afterwards, so one Program may be rendered from
// many goroutines at once.
type Program struct {
	ast    *ast.Program
	srcLen int
}

// AST returns the processed syntax tree. Callers must not modify it.
func (p *Program) AST() *ast.Program { return p.ast }

// SourceLen returns the length in bytes of the template source.
func (p *Program) SourceLen() int { return p.srcLen }

// parseLimits returns the template size and depth limits Parse applies:
// a non-positive field takes its DefaultLimits value. A zero Limits
// therefore means the production limits, never "unlimited": an unlimited
// depth would let a template overflow the Go stack.
func parseLimits(lim Limits) (int, int) {
	maxBytes, maxDepth := lim.MaxTemplateBytes, lim.MaxDepth
	if maxBytes <= 0 {
		maxBytes = DefaultLimits.MaxTemplateBytes
	}
	if maxDepth <= 0 {
		maxDepth = DefaultLimits.MaxDepth
	}
	return maxBytes, maxDepth
}

// Parse parses a template.
//
// It checks the limits before any recursion: first the source length
// against lim.MaxTemplateBytes, then, in one linear non-recursive scan of
// the tokens, the nesting depth against lim.MaxDepth. Depth counts open
// blocks (each else-if link of a chain counts as one more level, because
// it nests in the AST), raw blocks and open subexpressions. Either excess
// returns an *Error of KindLimit; the parser keeps its own depth counter
// with the same limit as a second line of defence.
//
// A template within the limits parses exactly as raymond parsed it: the
// same AST, and on a syntax error an *Error of KindParse whose Msg is
// raymond's message. A template over a limit may have failed differently
// under raymond (it reports the first problem it meets; Parse reports the
// limit), or crashed the process.
//
// Limit errors are parse-time errors: the ELPS binding reports them as
// handlebars-parse "error parsing template: <Msg>", like syntax errors.
func Parse(src string, lim Limits) (*Program, error) {
	maxBytes, maxDepth := parseLimits(lim)

	if len(src) > maxBytes {
		return nil, errorf(KindLimit, "template is %d bytes, limit is %d", len(src), maxBytes)
	}

	if err := parser.Depth(src, maxDepth); err != nil {
		return nil, toError(err)
	}

	prog, err := parser.ParseLimit(src, maxDepth)
	if err != nil {
		return nil, toError(err)
	}

	return &Program{ast: prog, srcLen: len(src)}, nil
}

// toError classifies a parser error.
func toError(err error) *Error {
	var lerr *parser.LimitError
	if errors.As(err, &lerr) {
		return &Error{Kind: KindLimit, Msg: lerr.Msg}
	}
	return &Error{Kind: KindParse, Msg: err.Error()}
}
