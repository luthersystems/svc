// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"fmt"
	"math"
	"reflect"

	"github.com/luthersystems/svc/libhandlebars/hbs/internal/ast"
)

// Helper calls.
//
// raymond calls helpers through reflection (eval.go callFunc) and converts
// each parameter to the Go type the helper function declares. The helpers
// here declare those types as argKinds, and callFunc applies raymond's rules
// for each:
//
//	declared type   nil parameter                     non-nil parameter of another type
//	interface{}     nil                               passed as is
//	[]interface{}   nil array                         error "Helper NAME called with argument I with type T but it should be []interface {}"
//	string          ""                                raymond.Str of the value (arrays concatenate, objects UNPRINTABLE)
//	bool            helper NOT called; result ""      truthiness of the value
//	int             helper NOT called; result ""      error "... with type T but it should be int"
//
// The parameter count must match exactly ("Helper 'NAME' called with wrong
// number of arguments, needed N but got M"). Parameters are converted in
// order, so the first nil bool/int parameter or the first type error wins.
// Hash values that evaluate to nil are dropped before the helper sees them.

type argKind uint8

const (
	argAny argKind = iota
	argString
	argBool
	argInt
	argSlice
)

func (k argKind) typeName() string {
	switch k {
	case argAny:
		return "interface {}"
	case argString:
		return "string"
	case argBool:
		return "bool"
	case argInt:
		return "int"
	default:
		return "[]interface {}"
	}
}

// helper is one registered helper. A streaming helper writes block sections
// to the output (Fn, Inverse) and its result is exactly what it wrote.
type helper struct {
	fn      func(c *hcall) any
	args    []argKind
	streams bool
}

// hcall is raymond's *Options for one helper call, plus the converted
// parameters.
type hcall struct {
	args   [2]any
	r      *renderer
	hash   map[string]any
	params []any
}

// helpers is the immutable helper table: raymond's built-in helpers and
// svc's. It is filled once by init and never written again.
var helpers map[string]*helper

func init() {
	helpers = map[string]*helper{
		"if":     {fn: helperIf, args: []argKind{argAny}, streams: true},
		"unless": {fn: helperUnless, args: []argKind{argAny}, streams: true},
		"with":   {fn: helperWith, args: []argKind{argAny}, streams: true},
		"each":   {fn: helperEach, args: []argKind{argAny}, streams: true},
		"equal":  {fn: helperEqual, args: []argKind{argAny, argAny}, streams: true},
		// lookup and log are not registered (luthersystems/raymond patch 3).
	}
	for name, h := range svcHelpers() {
		helpers[name] = h
	}
}

func findHelper(name string) *helper { return helpers[name] }

func (r *renderer) callHelper(name string, h *helper, node *ast.Expression, direct bool) any {
	r.step()
	var params []any
	if len(node.Params) > 0 {
		params = make([]any, len(node.Params))
		for i, p := range node.Params {
			params[i] = r.evalParam(p)
		}
	}
	var hash map[string]any
	if node.Hash != nil {
		hash = r.evalHash(node.Hash)
	}
	return r.callFunc(name, h, params, hash, direct)
}

func (r *renderer) callFunc(name string, h *helper, params []any, hash map[string]any, direct bool) any {
	if len(params) != len(h.args) {
		r.errorf("Helper '%s' called with wrong number of arguments, needed %d but got %d", name, len(h.args), len(params))
	}
	c := &hcall{r: r, params: params, hash: hash}
	for i, p := range params {
		kind := h.args[i]
		if p == nil {
			switch kind {
			case argAny:
				c.args[i] = nil
			case argSlice:
				c.args[i] = []any(nil)
			case argString:
				c.args[i] = ""
			default:
				// raymond returns reflect.Zero(string) without calling.
				return ""
			}
			continue
		}
		c.args[i] = r.convertArg(name, i, kind, p)
	}
	if !h.streams {
		res := h.fn(c)
		if s, ok := res.(string); ok {
			r.produced(len(s))
		}
		return res
	}
	start := len(r.out)
	h.fn(c)
	if direct {
		return streamed{}
	}
	return r.capture(start)
}

func (r *renderer) convertArg(name string, i int, kind argKind, p any) any {
	switch kind {
	case argAny:
		return p
	case argString:
		return r.str(p)
	case argBool:
		if b, ok := p.(bool); ok {
			return b
		}
		return isTrue(p)
	case argInt:
		if n, ok := p.(int); ok {
			return n
		}
		if r.mode == ModeFixed {
			if f, ok := p.(float64); ok && f == math.Trunc(f) && f >= math.MinInt64 && f < math.MaxInt64 {
				return int(f)
			}
		}
	default: // argSlice
		if a, ok := p.([]any); ok {
			return a
		}
		// A named []interface{} type is assignable to the parameter.
		if rv := reflect.ValueOf(p); rv.IsValid() && rv.Type().AssignableTo(anySlice) {
			return rv.Convert(anySlice).Interface()
		}
	}
	r.errorf("Helper %s called with argument %d with type %s but it should be %s", name, i, fmt.Sprintf("%T", p), kind.typeName())
	return nil
}

// Options accessors.

func (c *hcall) param(i int) any {
	if i < len(c.params) {
		return c.params[i]
	}
	return nil
}

func (c *hcall) argStr(i int) string {
	s, _ := c.args[i].(string)
	return s
}

func (c *hcall) hashStr(name string) string { return c.r.str(c.hash[name]) }

// sortedHashValues returns the hash values in sorted key order.
func (c *hcall) sortedHashValues() []any {
	keys := make([]string, 0, len(c.hash))
	for k := range c.hash {
		keys = append(keys, k)
	}
	c.r.sortKeys(keys)
	vals := make([]any, len(keys))
	for i, k := range keys {
		vals[i], _ = c.r.lookup(c.hash, k)
	}
	return vals
}

// evalBlock evaluates the current block's program (raymond evalBlock).
func (c *hcall) evalBlock(ctx any, data *dataFrame, key any) {
	if block := c.r.curBlock(); block != nil && block.Program != nil {
		c.r.evalProgram(block.Program, ctx, data, key)
	}
}

// wantsKey reports whether the current block names a second block
// parameter, the only reader of an iteration key. Boxing the key only then
// keeps #each over arrays allocation-free.
func (c *hcall) wantsKey() bool {
	block := c.r.curBlock()
	return block != nil && block.Program != nil && len(block.Program.BlockParams) > 1
}

func (c *hcall) fn()            { c.evalBlock(nil, nil, nil) }
func (c *hcall) fnWith(ctx any) { c.evalBlock(ctx, nil, nil) }

func (c *hcall) inverse() {
	if block := c.r.curBlock(); block != nil && block.Inverse != nil {
		c.r.evalInverse(block.Inverse)
	}
}

// includableZero is raymond's isIncludableZero: includeZero=true and a first
// parameter that is the template literal 0. ModeFixed also accepts a context
// number 0.
func (c *hcall) includableZero() bool {
	b, ok := c.hash["includeZero"].(bool)
	if !ok || !b {
		return false
	}
	switch n := c.param(0).(type) {
	case int:
		return n == 0
	case float64:
		return c.r.mode == ModeFixed && n == 0
	default:
		return false
	}
}

// Built-in block helpers.

func helperIf(c *hcall) any {
	if c.includableZero() || isTrue(c.args[0]) {
		c.fn()
	} else {
		c.inverse()
	}
	return nil
}

func helperUnless(c *hcall) any {
	if c.includableZero() || isTrue(c.args[0]) {
		c.inverse()
	} else {
		c.fn()
	}
	return nil
}

func helperWith(c *hcall) any {
	if isTrue(c.args[0]) {
		c.fnWith(c.args[0])
	} else {
		c.inverse()
	}
	return nil
}

func helperEach(c *hcall) any {
	ctx := c.args[0]
	if !isTrue(ctx) {
		c.inverse()
		return nil
	}
	r := c.r
	switch x := ctx.(type) {
	case []any:
		frame := &dataFrame{parent: r.frame, iter: true}
		boxKey := c.wantsKey()
		for i, e := range x {
			r.step()
			frame.setIter(len(x), i, nil)
			var key any
			if boxKey {
				key = i
			}
			c.evalBlock(e, frame, key)
		}
	case map[string]any:
		// Collecting the keys costs a step each, charged before the work;
		// sorting them and looking each one up are charged by key length.
		r.steps1(int64(len(x)))
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		r.sortKeys(keys)
		frame := &dataFrame{parent: r.frame, iter: true}
		for i, k := range keys {
			r.step()
			frame.setIter(len(keys), i, k)
			v, _ := r.lookup(x, k)
			c.evalBlock(v, frame, k)
		}
	default:
		// raymond iterates arrays, maps and structs only.
		c.goEach(ctx)
	}
	return nil
}

func helperEqual(c *hcall) any {
	if c.r.str(c.args[0]) == c.r.str(c.args[1]) {
		c.fn()
	}
	return nil
}
