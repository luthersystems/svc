package hbs

import "github.com/luthersystems/svc/libhandlebars/hbs/ast"

// Render evaluates the program with ctx (see Value) and returns the output.
//
// Errors: an *Error of KindRender whose Msg is the text raymond produced for
// the same failure (helper failures, "Evaluation error: ...\nCurrent node:
// ..." for evaluator failures, "Partial not found: NAME" for partials); an
// *Error of KindLimit when the output or the evaluation depth exceeds
// o.Limits; or the Meter's error, unchanged. A zero Limits field means the
// DefaultLimits value.
//
// Steps charged to o.Meter (nil: none): 1 per AST node evaluated, 1 per
// #each iteration, 1 per helper call and 1 per started KiB of output
// written, batched in groups of 64 and flushed at the end. Parse is charged
// by the caller. The charges depend only on (template, context, options).
//
// ModeFixed's differences are listed in helpers_svc.go.
//
// Render does not modify the Program or ctx and keeps all per-render state
// (data frames, the global helper's map, the output) in the call.
func (p *Program) Render(ctx Value, o Options) (string, error) {
	return render(p.ast, ctx, o)
}

func render(prog *ast.Program, ctx Value, o Options) (string, error) {
	lim := o.Limits
	if lim.MaxDepth <= 0 {
		lim.MaxDepth = DefaultLimits.MaxDepth
	}
	if lim.MaxOutputBytes <= 0 {
		lim.MaxOutputBytes = DefaultLimits.MaxOutputBytes
	}
	r := &renderer{
		meter:       o.Meter,
		mode:        o.Mode,
		maxDepth:    lim.MaxDepth,
		maxOut:      lim.MaxOutputBytes,
		frame:       &dataFrame{},
		ctx:         make([]any, 1, 16),
		rootInvalid: ctx == nil,
	}
	r.ctx[0] = ctx
	if err := r.run(prog); err != nil {
		return "", err
	}
	return string(r.out), nil
}
