// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"errors"

	"github.com/luthersystems/svc/libhandlebars/hbs/internal/ast"
	"github.com/luthersystems/svc/libhandlebars/hbs/internal/parser"
)

// Program is a parsed template. It is immutable after Parse returns: the
// AST, including its whitespace processing, is complete, and nothing in
// the engine writes to it afterwards, so one Program may be rendered from
// many goroutines at once.
type Program struct {
	ast    *ast.Program
	srcLen int
	tokens int // lexer tokens: the parse cache's weight for the AST
}

// SourceLen returns the length in bytes of the template source.
func (p *Program) SourceLen() int { return p.srcLen }

// parseLimits returns the template size and depth limits Parse applies:
// a non-positive field takes its DefaultLimits() value. A zero Limits
// therefore means the production limits, never "unlimited": an unlimited
// depth would let a template overflow the Go stack.
func parseLimits(lim Limits) (int, int) {
	maxBytes, maxDepth := lim.MaxTemplateBytes, lim.MaxDepth
	if maxBytes <= 0 {
		maxBytes = DefaultLimits().MaxTemplateBytes
	}
	if maxDepth <= 0 {
		maxDepth = DefaultLimits().MaxDepth
	}
	return maxBytes, min(maxDepth, MaxDepthCeiling)
}

// Parse parses a template.
//
// It checks the limits before any recursion: first the source length
// against lim.MaxTemplateBytes, then, in one linear non-recursive scan of
// the tokens, the nesting depth against lim.MaxDepth. Depth counts open
// blocks (each else-if link of a chain counts as one more level, because
// it nests in the AST), raw blocks and open subexpressions. Either excess
// returns an *Error of KindLimit. The parser keeps its own depth counter
// with the same limit, so its recursion is bounded on every input.
//
// A template within the limits parses exactly as raymond parsed it: the
// same AST, and on a syntax error an *Error of KindParse whose Msg is
// raymond's message. A syntax error earlier in the source than a depth
// excess is reported as raymond reported it. A template over a limit may
// otherwise have failed differently under raymond, or crashed the process.
//
// Limit errors are parse-time errors: the ELPS binding reports them as
// handlebars-parse "error parsing template: <Msg>", like syntax errors.
func Parse(src string, lim Limits) (*Program, error) {
	p, _, err := parseMetered(src, lim, nil)
	return p, err
}

// ParseCost is the steps parsing an n-byte template of the given lexer
// tokens costs: about 450 ns a token at worst (tag-dense templates) and
// about 2 ns a byte (plain text, and the SHA-256 the cache keys on).
func ParseCost(n, tokens int) int64 {
	return parseTokenCost*int64(tokens) + units(n, parseByteUnit)
}

const (
	parseTokenCost = 6
	parseByteUnit  = 16
)

// parseMetered is Parse charging ParseCost to m (nil: none), plus each
// number literal's floatCost, once the prescan has counted the tokens and
// before the recursive parse. It returns the steps charged, which the cache
// keeps so a hit costs what a miss did. A template over the size limit is
// charged ParseCost(len, 0).
func parseMetered(src string, lim Limits, m Meter) (*Program, int64, error) {
	maxBytes, maxDepth := parseLimits(lim)

	if len(src) > maxBytes {
		cost := ParseCost(len(src), 0)
		if err := charge(m, cost); err != nil {
			return nil, cost, err
		}
		return nil, cost, errorf(KindLimit, "template is %d bytes, limit is %d", len(src), maxBytes)
	}

	// The prescan finds a depth excess without recursion; the parser then
	// runs with the same limit, so its recursion is bounded too, and reports
	// whichever comes first in the source: a syntax error, as raymond did,
	// or the depth limit.
	var numbers int64 // template number literals are parsed with ParseFloat
	tokens, derr := parser.DepthFunc(src, maxDepth, func(lit string) { numbers += floatCost(lit) })
	cost := ParseCost(len(src), tokens) + numbers
	if err := charge(m, cost); err != nil {
		return nil, cost, err
	}
	prog, err := parser.ParseLimit(src, maxDepth)
	if err != nil {
		return nil, cost, toError(err)
	}
	if derr != nil {
		return nil, cost, toError(derr)
	}

	return &Program{ast: prog, srcLen: len(src), tokens: tokens}, cost, nil
}

// charge charges n steps to m, if any.
func charge(m Meter, n int64) error {
	if m == nil || n == 0 {
		return nil
	}
	return m.Charge(n)
}

// toError classifies a parser error.
func toError(err error) *Error {
	var lerr *parser.LimitError
	if errors.As(err, &lerr) {
		return &Error{Kind: KindLimit, Msg: lerr.Msg}
	}
	return &Error{Kind: KindParse, Msg: err.Error()}
}
