// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbdiff

import "github.com/luthersystems/svc/libhandlebars/internal/raymondref/lexer"

// RefFatal reports whether tpl may kill the reference process, so a
// harness must not render it through hbref. Two shapes are known:
//
//   - A helper that evaluates its block (if, unless, with, each, equal,
//     select) called outside block form, as {{if a}} or (if a), inside any
//     open block: raymond's Options.Fn and Inverse evaluate the innermost
//     enclosing block's program or inverse, which holds the call itself,
//     so the call re-enters forever and the Go stack overflows (fatal, not
//     a panic). Whether it does depends on the context, so the check is
//     conservative: any such call inside a block is refused. Outside every
//     block it renders "".
//   - Nesting deep enough to overflow the Go stack: raymond's parser,
//     whitespace pass and evaluator recurse once per level. Templates
//     nested deeper than maxRefDepth are refused. Depth is counted as
//     hbs/parser.Depth counts it (open blocks, each else-if link, raw
//     blocks, open subexpressions), not by the number of tags, so large
//     shallow templates are still compared.
func RefFatal(tpl string) bool {
	// stack holds, per open block, how many else-if links it has (each
	// nests one level deeper).
	type frame struct{ links int }
	var stack []frame
	depth, sexprs, raw := 0, 0, false
	toks := lexer.Collect(tpl)
	for i, tok := range toks {
		switch tok.Kind { //nolint:exhaustive // only block structure and identifiers matter
		case lexer.TokenOpenBlock, lexer.TokenOpenInverse:
			stack = append(stack, frame{})
			depth++
		case lexer.TokenOpenInverseChain:
			if len(stack) > 0 {
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
			case "if", "unless", "with", "each", "equal", "select":
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
			if len(stack) > 0 {
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
