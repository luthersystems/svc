package parser

import (
	"strings"

	"github.com/luthersystems/svc/libhandlebars/hbs/ast"
	"github.com/luthersystems/svc/libhandlebars/hbs/lexer"
)

// whitespaceVisitor walks through the AST to perform whitespace control
//
// The logic was shamelessly borrowed from:
//
//	https://github.com/wycats/handlebars.js/blob/master/lib/handlebars/compiler/whitespace-control.js
type whitespaceVisitor struct {
	isRootSeen bool
}

// The helpers below replace raymond's regular expressions. Each examines only
// a run of whitespace at one end of the string, so whitespace control costs
// time linear in the content it trims, not in the content's length per
// regexp call. \s is ASCII [\t\n\f\r ] (lexer.IsSpace), as in Go's RE2.

// trimLeft removes ^[ \t]*\r?\n?.
func trimLeft(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i < len(s) && s[i] == '\r' {
		i++
	}
	if i < len(s) && s[i] == '\n' {
		i++
	}
	return s[i:]
}

// trimLeftMultiple removes ^\s+.
func trimLeftMultiple(s string) string {
	i := 0
	for i < len(s) && lexer.IsSpace(s[i]) {
		i++
	}
	return s[i:]
}

// trimRight removes [ \t]+$.
func trimRight(s string) string {
	return strings.TrimRight(s, " \t")
}

// trimRightMultiple removes \s+$.
func trimRightMultiple(s string) string {
	i := len(s)
	for i > 0 && lexer.IsSpace(s[i-1]) {
		i--
	}
	return s[:i]
}

// trailingSpace returns the start of the trailing \s run of s and whether
// that run holds a newline.
func trailingSpace(s string) (int, bool) {
	nl := false
	i := len(s)
	for i > 0 && lexer.IsSpace(s[i-1]) {
		i--
		if s[i] == '\n' {
			nl = true
		}
	}
	return i, nl
}

// leadingSpace returns the end of the leading \s run of s and whether that
// run holds a newline.
func leadingSpace(s string) (int, bool) {
	nl := false
	i := 0
	for i < len(s) && lexer.IsSpace(s[i]) {
		if s[i] == '\n' {
			nl = true
		}
		i++
	}
	return i, nl
}

// isPrevWhitespaceStr matches \r?\n\s*?$, or (^|\r?\n)\s*?$ when start.
func isPrevWhitespaceStr(s string, start bool) bool {
	i, nl := trailingSpace(s)
	return nl || (start && i == 0)
}

// isNextWhitespaceStr matches ^\s*?\r?\n, or ^\s*?(\r?\n|$) when end.
func isNextWhitespaceStr(s string, end bool) bool {
	i, nl := leadingSpace(s)
	return nl || (end && i == len(s))
}

// partialIndent finds ([ \t]+$).
func partialIndent(s string) string {
	return s[len(trimRight(s)):]
}

// newWhitespaceVisitor instanciates a new whitespaceVisitor
func newWhitespaceVisitor() *whitespaceVisitor {
	return &whitespaceVisitor{}
}

// processWhitespaces performs whitespace control on given AST
//
// WARNING: It must be called only once on AST.
func processWhitespaces(node ast.Node) {
	node.Accept(newWhitespaceVisitor())
}

func omitRightFirst(body []ast.Node, multiple bool) {
	omitRight(body, -1, multiple)
}

func omitRight(body []ast.Node, i int, multiple bool) {
	if i+1 >= len(body) {
		return
	}

	current := body[i+1]

	node, ok := current.(*ast.ContentStatement)
	if !ok {
		return
	}

	if !multiple && node.RightStripped {
		return
	}

	original := node.Value

	if multiple {
		node.Value = trimLeftMultiple(node.Value)
	} else {
		node.Value = trimLeft(node.Value)
	}

	node.RightStripped = (original != node.Value)
}

func omitLeftLast(body []ast.Node, multiple bool) {
	omitLeft(body, len(body), multiple)
}

func omitLeft(body []ast.Node, i int, multiple bool) bool {
	if i-1 < 0 {
		return false
	}

	current := body[i-1]

	node, ok := current.(*ast.ContentStatement)
	if !ok {
		return false
	}

	if !multiple && node.LeftStripped {
		return false
	}

	original := node.Value

	if multiple {
		node.Value = trimRightMultiple(node.Value)
	} else {
		node.Value = trimRight(node.Value)
	}

	node.LeftStripped = (original != node.Value)

	return node.LeftStripped
}

func isPrevWhitespace(body []ast.Node) bool {
	return isPrevWhitespaceProgram(body, len(body), false)
}

func isPrevWhitespaceProgram(body []ast.Node, i int, isRoot bool) bool {
	if i < 1 {
		return isRoot
	}

	prev := body[i-1]

	if node, ok := prev.(*ast.ContentStatement); ok {
		if (node.Value == "") && node.RightStripped {
			// already stripped, so it may be an empty string not catched by regexp
			return true
		}

		return isPrevWhitespaceStr(node.Value, (i <= 1) && isRoot)
	}

	return false
}

func isNextWhitespace(body []ast.Node) bool {
	return isNextWhitespaceProgram(body, -1, false)
}

func isNextWhitespaceProgram(body []ast.Node, i int, isRoot bool) bool {
	if i+1 >= len(body) {
		return isRoot
	}

	next := body[i+1]

	if node, ok := next.(*ast.ContentStatement); ok {
		if (node.Value == "") && node.LeftStripped {
			// already stripped, so it may be an empty string not catched by regexp
			return true
		}

		return isNextWhitespaceStr(node.Value, (i+2 <= len(body)) && isRoot)
	}

	return false
}

//
// Visitor interface
//

func (v *whitespaceVisitor) VisitProgram(program *ast.Program) interface{} {
	isRoot := !v.isRootSeen
	v.isRootSeen = true

	body := program.Body
	for i, current := range body {
		strip, _ := current.Accept(v).(*ast.Strip)
		if strip == nil {
			continue
		}

		_isPrevWhitespace := isPrevWhitespaceProgram(body, i, isRoot)
		_isNextWhitespace := isNextWhitespaceProgram(body, i, isRoot)

		openStandalone := strip.OpenStandalone && _isPrevWhitespace
		closeStandalone := strip.CloseStandalone && _isNextWhitespace
		inlineStandalone := strip.InlineStandalone && _isPrevWhitespace && _isNextWhitespace

		if strip.Close {
			omitRight(body, i, true)
		}

		if strip.Open && (i > 0) {
			omitLeft(body, i, true)
		}

		if inlineStandalone {
			omitRight(body, i, false)

			if omitLeft(body, i, false) {
				// If we are on a standalone node, save the indent info for partials
				if partial, ok := current.(*ast.PartialStatement); ok {
					// Pull out the whitespace from the final line
					if i > 0 {
						if prevContent, ok := body[i-1].(*ast.ContentStatement); ok {
							partial.Indent = partialIndent(prevContent.Original)
						}
					}
				}
			}
		}

		if b, ok := current.(*ast.BlockStatement); ok {
			if openStandalone {
				prog := b.Program
				if prog == nil {
					prog = b.Inverse
				}

				omitRightFirst(prog.Body, false)

				// Strip out the previous content node if it's whitespace only
				omitLeft(body, i, false)
			}

			if closeStandalone {
				prog := b.Inverse
				if prog == nil {
					prog = b.Program
				}

				// Always strip the next node
				omitRight(body, i, false)

				omitLeftLast(prog.Body, false)
			}

		}
	}

	return nil
}

func (v *whitespaceVisitor) VisitBlock(block *ast.BlockStatement) interface{} {
	if block.Program != nil {
		block.Program.Accept(v)
	}

	if block.Inverse != nil {
		block.Inverse.Accept(v)
	}

	program := block.Program
	inverse := block.Inverse

	if program == nil {
		program = inverse
		inverse = nil
	}

	firstInverse := inverse
	lastInverse := inverse

	if (inverse != nil) && inverse.Chained {
		b, _ := inverse.Body[0].(*ast.BlockStatement)
		firstInverse = b.Program

		for lastInverse.Chained {
			b, _ := lastInverse.Body[len(lastInverse.Body)-1].(*ast.BlockStatement)
			lastInverse = b.Program
		}
	}

	closeProg := firstInverse
	if closeProg == nil {
		closeProg = program
	}

	strip := &ast.Strip{
		Open:  (block.OpenStrip != nil) && block.OpenStrip.Open,
		Close: (block.CloseStrip != nil) && block.CloseStrip.Close,

		OpenStandalone:  isNextWhitespace(program.Body),
		CloseStandalone: isPrevWhitespace(closeProg.Body),
	}

	if (block.OpenStrip != nil) && block.OpenStrip.Close {
		omitRightFirst(program.Body, true)
	}

	if inverse != nil {
		if block.InverseStrip != nil {
			inverseStrip := block.InverseStrip

			if inverseStrip.Open {
				omitLeftLast(program.Body, true)
			}

			if inverseStrip.Close {
				omitRightFirst(firstInverse.Body, true)
			}
		}

		if (block.CloseStrip != nil) && block.CloseStrip.Open {
			omitLeftLast(lastInverse.Body, true)
		}

		// Find standalone else statements
		if isPrevWhitespace(program.Body) && isNextWhitespace(firstInverse.Body) {
			omitLeftLast(program.Body, false)

			omitRightFirst(firstInverse.Body, false)
		}
	} else if (block.CloseStrip != nil) && block.CloseStrip.Open {
		omitLeftLast(program.Body, true)
	}

	return strip
}

func (v *whitespaceVisitor) VisitMustache(mustache *ast.MustacheStatement) interface{} {
	return mustache.Strip
}

func _inlineStandalone(strip *ast.Strip) interface{} {
	return &ast.Strip{
		Open:             strip.Open,
		Close:            strip.Close,
		InlineStandalone: true,
	}
}

func (v *whitespaceVisitor) VisitPartial(node *ast.PartialStatement) interface{} {
	strip := node.Strip
	if strip == nil {
		strip = &ast.Strip{}
	}

	return _inlineStandalone(strip)
}

func (v *whitespaceVisitor) VisitComment(node *ast.CommentStatement) interface{} {
	strip := node.Strip
	if strip == nil {
		strip = &ast.Strip{}
	}

	return _inlineStandalone(strip)
}

// NOOP
func (v *whitespaceVisitor) VisitContent(node *ast.ContentStatement) interface{}    { return nil }
func (v *whitespaceVisitor) VisitExpression(node *ast.Expression) interface{}       { return nil }
func (v *whitespaceVisitor) VisitSubExpression(node *ast.SubExpression) interface{} { return nil }
func (v *whitespaceVisitor) VisitPath(node *ast.PathExpression) interface{}         { return nil }
func (v *whitespaceVisitor) VisitString(node *ast.StringLiteral) interface{}        { return nil }
func (v *whitespaceVisitor) VisitBoolean(node *ast.BooleanLiteral) interface{}      { return nil }
func (v *whitespaceVisitor) VisitNumber(node *ast.NumberLiteral) interface{}        { return nil }
func (v *whitespaceVisitor) VisitHash(node *ast.Hash) interface{}                   { return nil }
func (v *whitespaceVisitor) VisitHashPair(node *ast.HashPair) interface{}           { return nil }
