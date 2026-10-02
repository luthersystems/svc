package hbdiff

import (
	"strings"

	"github.com/luthersystems/svc/libhandlebars/internal/raymondref/lexer"
)

// RefFatal reports whether tpl may kill the reference process, so a
// harness must not render it through hbref. Two shapes are known:
//
//   - A built-in block helper (if, unless, each, with) called outside block
//     form, as {{if a}} or (if a), whose inverse runs inside an else or
//     {{^}} branch: raymond's Options.Inverse re-enters the enclosing
//     inverse program forever and the Go stack overflows (fatal, not a
//     panic). The check is conservative: such a call anywhere under an
//     open inverse branch is refused; outside one it renders "".
//   - Nesting deep enough to overflow the Go stack. Inputs with more than
//     maxRefNesting "{{" or "(" are refused.
func RefFatal(tpl string) bool {
	if strings.Count(tpl, "{{")+strings.Count(tpl, "(") > maxRefNesting {
		return true
	}
	// stack holds, per open block, whether its inverse branch is open.
	var stack []bool
	inInverse := func() bool {
		for _, inv := range stack {
			if inv {
				return true
			}
		}
		return false
	}
	toks := lexer.Collect(tpl)
	for i, tok := range toks {
		switch tok.Kind { //nolint:exhaustive // only block structure and identifiers matter
		case lexer.TokenOpenBlock:
			stack = append(stack, false)
		case lexer.TokenOpenInverse:
			stack = append(stack, true)
		case lexer.TokenInverse, lexer.TokenOpenInverseChain:
			if len(stack) > 0 {
				stack[len(stack)-1] = true
			}
		case lexer.TokenOpenEndBlock:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case lexer.TokenID:
			switch tok.Val {
			case "if", "unless", "each", "with":
			default:
				continue
			}
			if i > 0 {
				switch toks[i-1].Kind {
				case lexer.TokenOpenBlock, lexer.TokenOpenEndBlock, lexer.TokenOpenInverseChain:
					continue
				default:
				}
			}
			if inInverse() {
				return true
			}
		}
	}
	return false
}

const maxRefNesting = 200
