package hbdiff

import "github.com/luthersystems/svc/libhandlebars/internal/raymondref/lexer"

// RefFatal reports whether tpl may kill the reference process, so a
// harness must not render it through hbref. Two shapes are known:
//
//   - A built-in block helper (if, unless, each, with) called outside block
//     form, as {{if a}} or (if a), whose inverse runs inside an else or
//     {{^}} branch: raymond's Options.Inverse re-enters the enclosing
//     inverse program forever and the Go stack overflows (fatal, not a
//     panic). The check is conservative: such a call anywhere under an
//     open inverse branch is refused; outside one it renders "".
//   - Nesting deep enough to overflow the Go stack: raymond's parser,
//     whitespace pass and evaluator recurse once per level. Templates
//     nested deeper than maxRefDepth are refused. Depth is counted as
//     hbs/parser.Depth counts it (open blocks, each else-if link, raw
//     blocks, open subexpressions), not by the number of tags, so large
//     shallow templates are still compared.
func RefFatal(tpl string) bool {
	// stack holds, per open block, whether its inverse branch is open, and
	// how many else-if links it has (each nests one level deeper).
	type frame struct {
		inverse bool
		links   int
	}
	var stack []frame
	depth, sexprs, raw := 0, 0, false
	inInverse := func() bool {
		for _, f := range stack {
			if f.inverse {
				return true
			}
		}
		return false
	}
	toks := lexer.Collect(tpl)
	for i, tok := range toks {
		switch tok.Kind { //nolint:exhaustive // only block structure and identifiers matter
		case lexer.TokenOpenBlock:
			stack = append(stack, frame{})
			depth++
		case lexer.TokenOpenInverse:
			stack = append(stack, frame{inverse: true})
			depth++
		case lexer.TokenInverse:
			if len(stack) > 0 {
				stack[len(stack)-1].inverse = true
			}
		case lexer.TokenOpenInverseChain:
			if len(stack) > 0 {
				stack[len(stack)-1].inverse = true
				stack[len(stack)-1].links++
			}
			depth++
		case lexer.TokenOpenEndBlock:
			if len(stack) > 0 {
				depth -= 1 + stack[len(stack)-1].links
				stack = stack[:len(stack)-1]
			}
		case lexer.TokenOpenRawBlock:
			raw = true
			depth++
		case lexer.TokenOpenEndRawBlock:
			if raw {
				raw = false
				depth--
			}
		case lexer.TokenOpenSexpr:
			sexprs++
			depth++
		case lexer.TokenCloseSexpr:
			if sexprs > 0 {
				sexprs--
				depth--
			}
		case lexer.TokenID:
			switch tok.Val {
			case "if", "unless", "each", "with":
			default:
				continue
			}
			if i > 0 {
				switch toks[i-1].Kind {
				case lexer.TokenOpenBlock, lexer.TokenOpenInverse, lexer.TokenOpenEndBlock, lexer.TokenOpenInverseChain:
					// block form ({{#if}}, {{^if}}, {{/if}}, {{else if}})
					continue
				default:
				}
			}
			if inInverse() {
				return true
			}
		}
		if depth > maxRefDepth {
			return true
		}
	}
	return false
}

// maxRefDepth is the deepest nesting the harness renders through the
// reference. It is well above the engine's MaxDepth (256), so the cap and
// cap+1 are still compared, and TestRefDepthBound renders every deep shape
// at this depth through the reference on a reduced stack.
const maxRefDepth = 1000
