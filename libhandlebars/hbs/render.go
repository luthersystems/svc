// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"math"

	"github.com/luthersystems/svc/libhandlebars/hbs/internal/ast"
)

// Render evaluates the program with ctx (see Value) and returns the output.
//
// Errors: an *Error of KindRender whose Msg is the text raymond produced for
// the same failure (helper failures, "Evaluation error: ...\nCurrent node:
// ..." for evaluator failures, "Partial not found: NAME" for partials); an
// *Error of KindLimit when the output or the evaluation depth exceeds
// o.Limits; or the Meter's error, unchanged. A zero Limits field means the
// DefaultLimits() value.
//
// Steps charged to o.Meter (nil: none): 1 per AST node evaluated, 1 per
// path segment resolved, 1 per context a lookup tries, 1 per array element
// a path is mapped over, 1 per #each iteration, 1 per helper call, 1 per element select and
// in-string-array scan, 1 per started 256 bytes of each string a helper reads
// (its string arguments), and 1 per started KiB of everything produced:
// output written (captured sections included), strings helpers build, and
// round-to-nth's precision, charged before it formats. Steps are batched in
// groups of 64 and flushed at the end. Parse is charged by the caller. The
// charges depend only on (template, context, options).
//
// Limits beyond the output size: the render fails with KindLimit when its
// steps pass o.Limits.MaxSteps (whether or not a Meter is set), or when its
// produced bytes pass producedFactor times MaxOutputBytes.
//
// ModeFixed's differences are listed in helpers_svc.go.
//
// Render does not modify the Program or ctx and keeps all per-render state
// (data frames, the global helper's map, the output) in the call.
func (p *Program) Render(ctx Value, o Options) (string, error) {
	return render(p.ast, ctx, o)
}

// producedFactor bounds the bytes a render may produce, captured sections
// and helper strings included, as a multiple of MaxOutputBytes.
const producedFactor = 8

func render(prog *ast.Program, ctx Value, o Options) (string, error) {
	lim := o.Limits
	if lim.MaxDepth <= 0 {
		lim.MaxDepth = DefaultLimits().MaxDepth
	}
	if lim.MaxOutputBytes <= 0 {
		lim.MaxOutputBytes = DefaultLimits().MaxOutputBytes
	}
	if lim.MaxSteps <= 0 {
		lim.MaxSteps = DefaultLimits().MaxSteps
	}
	r := &renderer{
		meter:       o.Meter,
		mode:        o.Mode,
		maxDepth:    lim.MaxDepth,
		maxOut:      lim.MaxOutputBytes,
		maxProduced: int64(min(lim.MaxOutputBytes, math.MaxInt64/producedFactor)) * producedFactor, // saturated
		maxSteps:    lim.MaxSteps,
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
