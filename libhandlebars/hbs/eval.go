// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"fmt"
	"strconv"

	"github.com/luthersystems/svc/libhandlebars/hbs/internal/ast"
)

// The evaluator is a port of raymond's eval.go without reflection. It keeps
// raymond's observable behaviour, including:
//
//   - the "last visited node" that "Current node:" error text prints
//     (r.at is called exactly where raymond calls v.at);
//   - the mustache context climb of evalDepthPath;
//   - the difference between a missing value (not found) and a found nil, and
//     the typed empty array an array context yields;
//   - the block a helper's Fn/Inverse evaluate: the innermost block statement
//     being evaluated, which is an enclosing block when the helper is called
//     from a {{mustache}} or a subexpression.
//
// Errors unwind with panic and are recovered in run, as in raymond. Inputs
// that crash raymond (a data path with no name such as {{@this}}, a negative
// array index such as {{a.[-1]}}, unbounded helper recursion) are render
// errors here.

// renderer holds the state of one render. It is never shared.
type renderer struct {
	meter   Meter
	curNode ast.Node
	frame   *dataFrame
	global  map[globalKey]string
	out     []byte
	scratch []byte // formatting buffer for str
	ctx     []any
	blocks  []*ast.BlockStatement
	bparams []blockParams

	pending     int64
	written     int64
	chargedKiB  int64
	steps       int64
	maxSteps    int64
	maxProduced int64
	depth       int
	maxDepth    int
	maxOut      int

	mode        Mode
	rootInvalid bool
}

// dataFrame is raymond's private data frame (@index, @key, @first, @last).
// raymond copies a map per iteration; every iteration frame holds exactly
// these four names, so a struct is the same thing.
type dataFrame struct {
	parent *dataFrame
	key    any
	index  int
	iter   bool
	first  bool
	last   bool
}

func (f *dataFrame) get(name string) (any, bool) {
	if !f.iter {
		return nil, false
	}
	switch name {
	case "index":
		return f.index, true
	case "key":
		return f.key, true
	case "first":
		return f.first, true
	case "last":
		return f.last, true
	default:
		return nil, false
	}
}

// blockParams is one `as |a b|` frame. raymond stores a map; when both names
// are equal the second assignment wins, as here.
type blockParams struct {
	val0, val1   any
	name0, name1 string
	has1         bool
}

// bpContext is the one-entry context raymond pushes ({name: value}) to
// resolve a path that starts with a block parameter.
type bpContext struct {
	val  any
	name string
}

// streamed is what a block helper returns when it wrote its sections
// straight to the output.
type streamed struct{}

func (r *renderer) at(n ast.Node) {
	r.curNode = n
	r.step()
}

// errorf fails the render with raymond's evaluation error text.
//
// The message, which can hold template text, counts as produced bytes.
func (r *renderer) errorf(format string, args ...any) {
	// Bound the formatted arguments before building them (they can hold a
	// long name from the template); the node dump is then charged as built.
	n := len(format)
	for _, a := range args {
		if s, ok := a.(string); ok {
			n += len(s)
		}
	}
	r.reserveProduced(n)
	msg := fmt.Sprintf("Evaluation error: %s\nCurrent node:\n\t%s", fmt.Sprintf(format, args...), r.curNode)
	r.produced(len(msg))
	panic(&Error{Kind: KindRender, Msg: msg})
}

// fail fails the render with a helper's error text, which raymond returns
// unchanged.
// The message counts as produced bytes: it can hold a whole argument.
func (r *renderer) fail(msg string) {
	r.produced(len(msg))
	panic(&Error{Kind: KindRender, Msg: msg})
}

// failWith fails with prefix+s, checking the produced-bytes bound before
// the message is built.
func (r *renderer) failWith(prefix, s string) {
	r.reserveProduced(len(prefix) + len(s))
	r.fail(prefix + s)
}

func (r *renderer) enter() {
	r.depth++
	if r.depth > r.maxDepth {
		panic(errorf(KindLimit, "template evaluation exceeds the maximum depth of %d", r.maxDepth))
	}
}

func (r *renderer) leave() { r.depth-- }

// run evaluates prog and recovers the evaluation's failure, if any.
func (r *renderer) run(prog *ast.Program) error {
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = r.recovered(p)
			}
		}()
		r.visitProgram(prog)
		r.flush()
	}()
	return err
}

func (r *renderer) recovered(p any) error {
	switch e := p.(type) {
	case meterError:
		return e.err
	case *Error:
		// Charge the work done before the failure; a budget error wins.
		if ferr := r.safeFlush(); ferr != nil {
			return ferr
		}
		return e
	default:
		// Not reachable by design; never let a template crash the process.
		return errorf(KindRender, "Evaluation error: internal error: %v", e)
	}
}

func (r *renderer) safeFlush() error {
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				switch e := p.(type) {
				case meterError:
					err = e.err
				case *Error:
					err = e
				default:
					panic(p)
				}
			}
		}()
		r.flush()
	}()
	return err
}

// Context stack.

func (r *renderer) pushCtx(v any) { r.ctx = append(r.ctx, v) }

func (r *renderer) popCtx() { r.ctx = r.ctx[:len(r.ctx)-1] }

// ancestorCtx returns the context depth levels up, and whether it exists.
func (r *renderer) ancestorCtx(depth int) (any, bool) {
	i := len(r.ctx) - 1 - depth
	if i < 0 || (i == 0 && r.rootInvalid) {
		return nil, false
	}
	return r.ctx[i], true
}

func (r *renderer) curBlock() *ast.BlockStatement {
	if len(r.blocks) == 0 {
		return nil
	}
	return r.blocks[len(r.blocks)-1]
}

// blockParam finds name among the block parameters in scope, innermost
// first: a step per frame scanned, plus the cost of each compare.
func (r *renderer) blockParam(name string) any {
	for i := len(r.bparams) - 1; i >= 0; i-- {
		r.step()
		bp := &r.bparams[i]
		if bp.has1 && r.compare(bp.name1, name) {
			return bp.val1
		}
		if r.compare(bp.name0, name) {
			return bp.val0
		}
	}
	return nil
}

// evalProgram evaluates a block's program with a new context, data frame and
// block parameters (raymond evalProgram), writing to the output.
func (r *renderer) evalProgram(prog *ast.Program, ctx any, data *dataFrame, key any) {
	r.enter()
	hasBP := len(prog.BlockParams) > 0
	if hasBP {
		bp := blockParams{name0: prog.BlockParams[0], val0: ctx}
		if len(prog.BlockParams) > 1 && key != nil {
			bp.name1, bp.val1, bp.has1 = prog.BlockParams[1], key, true
		}
		r.bparams = append(r.bparams, bp)
	}
	if ctx != nil {
		r.pushCtx(ctx)
	}
	if data != nil {
		r.frame = data
	}

	r.visitProgram(prog)

	if data != nil {
		r.frame = r.frame.parent
	}
	if ctx != nil {
		r.popCtx()
	}
	if hasBP {
		r.bparams = r.bparams[:len(r.bparams)-1]
	}
	r.leave()
}

// evalInverse evaluates an else program in the current context.
func (r *renderer) evalInverse(prog *ast.Program) {
	r.enter()
	r.visitProgram(prog)
	r.leave()
}

// Paths.

func stripBrackets(part string) string {
	if len(part) >= 2 && part[0] == '[' && part[len(part)-1] == ']' {
		return part[1 : len(part)-1]
	}
	return part
}

// evalField looks name up in ctx. ok is false when there is no such field
// (raymond's invalid reflect.Value); a field holding null is (nil, true).
func (r *renderer) evalField(ctx any, name string) (any, bool) {
	switch c := ctx.(type) {
	case map[string]any:
		return r.lookup(c, name)
	case []any:
		r.scanBytes(len(name))
		i, err := strconv.Atoi(name)
		if err == nil && i < len(c) {
			if i < 0 {
				r.errorf("array index out of range: %d", i)
			}
			return c[i], true
		}
	case bpContext:
		if r.compare(c.name, name) {
			return c.val, true
		}
	default:
	}
	return nil, false
}

// evalPath resolves parts from ctx, one step per part. It reports whether
// the result exists and whether at least one part resolved.
func (r *renderer) evalPath(ctx any, parts []string) (any, bool, bool) {
	resolved := false
	for _, part := range parts {
		r.step()
		v, ok := r.evalField(ctx, stripBrackets(part))
		if !ok {
			return nil, false, resolved
		}
		ctx = v
		resolved = true
	}
	return ctx, true, resolved
}

// evalCtxPath is raymond's evalCtxPath. An array context maps the path over
// its elements and yields a []any, which is nil (but typed) when nothing
// resolved. Each element costs a step, even for an empty path.
func (r *renderer) evalCtxPath(ctx any, parts []string) (any, bool) {
	if arr, ok := ctx.([]any); ok {
		var results []any
		for _, e := range arr {
			r.step()
			if v, valid, _ := r.evalPath(e, parts); valid {
				results = append(results, v)
			}
		}
		return results, false
	}
	v, valid, resolved := r.evalPath(ctx, parts)
	if !valid {
		return nil, resolved
	}
	return v, resolved
}

// evalDepthPath tries each context from depth outwards until one resolves
// the first part of the path (mustache context precedence).
func (r *renderer) evalDepthPath(depth int, parts []string) any {
	var result any
	resolved := false
	ctx, ok := r.ancestorCtx(depth)
	for result == nil && ok && depth <= len(r.ctx) && !resolved {
		r.step()
		result, resolved = r.evalCtxPath(ctx, parts)
		if !resolved && result == nil {
			depth++
			ctx, ok = r.ancestorCtx(depth)
		}
	}
	return result
}

func (r *renderer) isDataRoot(node *ast.PathExpression) bool {
	if !node.Data {
		return false
	}
	if len(node.Parts) == 0 {
		// raymond indexes Parts[0] here and crashes.
		r.errorf("Invalid private data path: %s", node.Original)
	}
	return node.Parts[0] == "root"
}

func (r *renderer) evalCtxPathExpression(node *ast.PathExpression) any {
	r.at(node)
	if r.isDataRoot(node) {
		root, ok := r.ancestorCtx(len(r.ctx) - 1)
		if !ok {
			return nil
		}
		v, _ := r.evalCtxPath(root, node.Parts[1:])
		return v
	}
	return r.evalDepthPath(node.Depth, node.Parts)
}

func (r *renderer) evalDataPathExpression(node *ast.PathExpression) any {
	frame := r.frame
	for i := node.Depth; i > 0; i-- {
		r.step() // each @../ frame climbed
		if frame.parent == nil {
			return nil
		}
		frame = frame.parent
	}
	v, ok := frame.get(stripBrackets(node.Parts[0]))
	if !ok {
		return nil
	}
	v, valid, _ := r.evalPath(v, node.Parts[1:])
	if !valid {
		return nil
	}
	return v
}

func (r *renderer) evalPathExpression(node *ast.PathExpression) any {
	if len(node.Parts) > 0 {
		name := node.Parts[0]
		if value := r.blockParam(name); value != nil {
			r.pushCtx(bpContext{name: name, val: value})
			result := r.evalCtxPathExpression(node)
			r.popCtx()
			return result
		}
	}
	var result any
	ctxTried := false
	if r.isDataRoot(node) {
		result = r.evalCtxPathExpression(node)
		ctxTried = true
	}
	if result == nil && node.Data {
		result = r.evalDataPathExpression(node)
	}
	if result == nil && !ctxTried {
		result = r.evalCtxPathExpression(node)
	}
	return result
}

// Expressions.

// evalExpr evaluates an expression. direct is set for a block statement's
// own expression: a block helper then writes its sections to the output and
// returns streamed{}.
func (r *renderer) evalExpr(node *ast.Expression, direct bool) any {
	r.at(node)
	if name := node.HelperName(); name != "" {
		if h := r.findHelper(name); h != nil {
			return r.callHelper(name, h, node, direct)
		}
	}
	if lit, ok := node.LiteralStr(); ok {
		r.formatted(len(lit)) // a number literal is formatted to look it up
		cur, ok := r.ancestorCtx(0)
		if ok {
			if v, found := r.evalField(cur, lit); found {
				return v
			}
		}
		return nil
	}
	if path := node.FieldPath(); path != nil {
		return r.evalPathExpression(path)
	}
	return nil
}

// evalParam evaluates a helper parameter or hash value.
func (r *renderer) evalParam(n ast.Node) any {
	switch x := n.(type) {
	case *ast.PathExpression:
		return r.evalPathExpression(x)
	case *ast.SubExpression:
		r.at(x)
		r.enter()
		v := r.evalExpr(x.Expression, false)
		r.leave()
		return v
	case *ast.StringLiteral:
		r.at(x)
		return x.Value
	case *ast.BooleanLiteral:
		r.at(x)
		return x.Value
	case *ast.NumberLiteral:
		r.at(x)
		return x.Number()
	case *ast.Expression:
		return r.evalExpr(x, false)
	default:
		return nil
	}
}

func (r *renderer) evalHash(node *ast.Hash) map[string]any {
	r.at(node)
	hash := make(map[string]any, len(node.Pairs))
	for _, pair := range node.Pairs {
		r.at(pair)
		if v := r.evalParam(pair.Val); v != nil {
			r.hashKey(len(pair.Key))
			hash[pair.Key] = v
		}
	}
	return hash
}

// Statements.

func (r *renderer) visitProgram(node *ast.Program) {
	r.at(node)
	for _, n := range node.Body {
		switch s := n.(type) {
		case *ast.ContentStatement:
			r.at(s)
			r.writeString(s.Value)
		case *ast.MustacheStatement:
			r.visitMustache(s)
		case *ast.BlockStatement:
			r.visitBlock(s)
		case *ast.CommentStatement:
			r.at(s)
		case *ast.PartialStatement:
			r.visitPartial(s)
		default:
			r.errorf("unexpected statement: %s", n)
		}
	}
}

func (r *renderer) visitMustache(node *ast.MustacheStatement) {
	r.at(node)
	v := r.evalExpr(node.Expression, false)
	r.writeValue(v, !node.Unescaped)
}

func (r *renderer) visitBlock(node *ast.BlockStatement) {
	r.at(node)
	r.blocks = append(r.blocks, node)

	v := r.evalExpr(node.Expression, true)
	if r.isHelperCall(node.Expression) {
		if _, ok := v.(streamed); !ok {
			r.writeValue(v, false)
		}
	} else if isTrue(v) {
		if node.Program != nil {
			if arr, ok := v.([]any); ok {
				frame := &dataFrame{parent: r.frame, iter: true}
				boxKey := len(node.Program.BlockParams) > 1
				for i, e := range arr {
					r.step()
					frame.setIter(len(arr), i, nil)
					var key any
					if boxKey {
						key = i
					}
					r.evalProgram(node.Program, e, frame, key)
				}
			} else {
				r.evalProgram(node.Program, v, nil, nil)
			}
		}
	} else if node.Inverse != nil {
		r.evalInverse(node.Inverse)
	}

	r.blocks = r.blocks[:len(r.blocks)-1]
}

func (f *dataFrame) setIter(length, i int, key any) {
	f.index = i
	f.key = key
	f.first = i == 0
	f.last = i == length-1
}

func (r *renderer) isHelperCall(node *ast.Expression) bool {
	name := node.HelperName()
	return name != "" && r.findHelper(name) != nil
}

// findHelper is findHelper with the lookup charged.
func (r *renderer) findHelper(name string) *helper {
	r.hashKey(len(name))
	return findHelper(name)
}

// visitPartial reproduces raymond with no partials registered: every partial
// fails after its name is evaluated.
func (r *renderer) visitPartial(node *ast.PartialStatement) {
	r.at(node)
	name, ok := ast.HelperNameStr(node.Name)
	if !ok {
		if sub, isSub := node.Name.(*ast.SubExpression); isSub {
			name, _ = r.evalParam(sub).(string)
		}
	}
	if name == "" {
		r.errorf("Unexpected partial name: %q", node.Name)
	}
	r.errorf("Partial not found: %s", name)
}
