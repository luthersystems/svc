// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// Go values in a render context.
//
// A context built by FromJSON holds only the types listed under Value. The
// Go API (libhandlebars.Render) passes a caller's Go value as the context
// instead, and raymond read Go values lazily, by reflection, as the template
// reached them. The functions here are that reading, ported from raymond
// (internal/raymondref: evalField, indirect, isTrueValue, strValue,
// eachHelper) with every step charged and every loop bounded:
//
//   - A path into a Go value resolves each part as raymond's evalField did:
//     a method of that name (or of its strings.Title form) is a render error
//     (raymond called it; the engine never calls Go code), then an exported
//     struct field (promoted fields included) named strings.Title(name),
//     then the first field whose `handlebars` tag is name; a map key, when
//     a string is assignable to the key type; a slice or array index. A
//     func reached this way is a render error too (raymond called it).
//   - Pointers and interfaces are followed as raymond's indirect did, at
//     most MaxDepth at a time (raymond looped forever on a pointer cycle).
//   - Truthiness, printing, #each and array blocks follow the value's kind,
//     so named types print and test like their underlying kind, but a
//     helper's type switch sees the named type, as svc's helpers did.
//
// Only what the template touches is read, so a cyclic or huge Go value
// costs what the template does with it. Where raymond panicked (an
// unexported value, a nil embedded pointer, printing a func) the render
// fails instead.

// isGo reports whether v is a Go value outside the engine's own types, to
// be read by reflection.
func isGo(v any) bool {
	switch v.(type) {
	case nil, bool, string, int, float64, []any, map[string]any, bpContext:
		return false
	default:
		return true
	}
}

var (
	errorType    = reflect.TypeFor[error]()
	stringerType = reflect.TypeFor[fmt.Stringer]()
	anySlice     = reflect.TypeFor[[]any]()
	stringType   = reflect.TypeFor[string]()
)

// goIndirect is raymond's indirect: it follows pointers and empty
// interfaces, stopping at a nil one (returned with isNil) or at an
// interface with methods. Each hop is a step; more than MaxDepth hops is a
// limit error.
func (r *renderer) goIndirect(v reflect.Value) (reflect.Value, bool) {
	for hops := 0; v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface; v = v.Elem() {
		if v.IsNil() {
			return v, true
		}
		if v.Kind() == reflect.Interface && v.NumMethod() > 0 {
			break
		}
		r.step()
		if hops++; hops > r.maxDepth {
			panic(errorf(KindLimit, "Go value has more than %d nested pointers", r.maxDepth))
		}
	}
	return v, false
}

// structPlan is what raymond's struct lookups need of a struct type,
// computed once per type: the fields FieldByName finds (reflect.VisibleFields
// keeps exactly those), the first field holding each handlebars tag, and
// the exported fields #each visits.
type structPlan struct {
	byName   map[string]reflect.StructField
	byTag    map[string]int
	exported []int
	cost     int64 // steps a lookup costs, the same whether the plan was cached
}

var structPlans sync.Map // reflect.Type -> *structPlan

func planFor(t reflect.Type) *structPlan {
	if p, ok := structPlans.Load(t); ok {
		return p.(*structPlan) //nolint:forcetypeassert // only *structPlan is stored
	}
	visible := reflect.VisibleFields(t)
	p := &structPlan{byName: make(map[string]reflect.StructField, len(visible))}
	for _, f := range visible {
		p.byName[f.Name] = f
	}
	for i := range t.NumField() {
		f := t.Field(i)
		if tag := f.Tag.Get("handlebars"); tag != "" {
			if p.byTag == nil {
				p.byTag = map[string]int{}
			}
			if _, dup := p.byTag[tag]; !dup {
				p.byTag[tag] = i
			}
		}
		if f.IsExported() {
			p.exported = append(p.exported, i)
		}
	}
	// Building the plan is linear in the fields; charging it on every
	// lookup keeps a cache hit and a miss alike.
	p.cost = 1 + units(len(visible)+t.NumField(), scanUnit)
	actual, _ := structPlans.LoadOrStore(t, p)
	return actual.(*structPlan) //nolint:forcetypeassert // only *structPlan is stored
}

// title is strings.Title, the casing raymond applied to field and method
// names, charged by length (Unicode case mapping, a few ns a byte).
func (r *renderer) title(name string) string {
	r.steps1(max(1, units(len(name), fmtUnit))) // about 4 ns a byte of non-ASCII
	return strings.Title(name)                  //nolint:staticcheck // raymond's exact casing rule
}

// goField is raymond's evalField on a Go value. It returns an invalid
// Value when there is no such field.
func (r *renderer) goField(ctx reflect.Value, name string) reflect.Value {
	ctx, _ = r.goIndirect(ctx)
	if !ctx.IsValid() {
		return reflect.Value{}
	}
	r.goNoMethod(ctx, name)
	var result reflect.Value
	switch ctx.Kind() {
	case reflect.Struct:
		p := planFor(ctx.Type())
		r.steps1(p.cost)
		r.hashKey(len(name))
		if f, ok := p.byName[r.title(name)]; ok && f.IsExported() {
			fv, err := ctx.FieldByIndexErr(f.Index)
			if err != nil {
				r.errorf("%s", err.Error())
			}
			result = fv
			break
		}
		if i, ok := p.byTag[name]; ok {
			result = ctx.Field(i)
		}
	case reflect.Map:
		if stringType.AssignableTo(ctx.Type().Key()) {
			r.hashKey(len(name))
			result = ctx.MapIndex(reflect.ValueOf(name))
		}
	case reflect.Array, reflect.Slice:
		r.scanBytes(len(name))
		if i, err := strconv.Atoi(name); err == nil && i < ctx.Len() {
			if i < 0 {
				r.errorf("array index out of range: %d", i)
			}
			result = ctx.Index(i)
		}
	default:
		// raymond looks into structs, maps, arrays and slices only.
	}
	result, _ = r.goIndirect(result)
	if result.Kind() == reflect.Func {
		r.errorf("Go func values are not supported: %s", name)
	}
	return result
}

// goResult finishes a lookup that found v as raymond's evalField did: a Go
// pointer is followed (a nil one stays), and a func fails the render.
func (r *renderer) goResult(v any, name string) any {
	if !isGo(v) {
		return v
	}
	rv, _ := r.goIndirect(reflect.ValueOf(v))
	if rv.Kind() == reflect.Func {
		r.errorf("Go func values are not supported: %s", name)
	}
	return r.goInterface(rv)
}

// goNoMethod fails the render where raymond would have called a method of
// v named name or strings.Title(name).
func (r *renderer) goNoMethod(v reflect.Value, name string) {
	if v.Kind() == reflect.Interface && v.IsNil() {
		return
	}
	if v.Kind() != reflect.Interface && v.CanAddr() {
		v = v.Addr()
	}
	if v.NumMethod() == 0 {
		return
	}
	r.hashKey(len(name))
	if v.MethodByName(name).IsValid() {
		r.errorf("Go method calls are not supported: %s.%s", v.Type(), name)
	}
	if t := r.title(name); t != name && v.MethodByName(t).IsValid() {
		r.errorf("Go method calls are not supported: %s.%s", v.Type(), t)
	}
}

// goPath resolves parts from the Go value v, keeping the reflect.Value
// between parts as raymond did (so a field reached through a pointer is
// addressable and its pointer methods count). The first part's step is the
// caller's.
func (r *renderer) goPath(v reflect.Value, parts []string, resolved bool) (any, bool, bool) {
	for i, part := range parts {
		if i > 0 {
			r.step()
		}
		v = r.goField(v, stripBrackets(part))
		if !v.IsValid() {
			return nil, false, resolved
		}
		resolved = true
	}
	return r.goInterface(v), true, resolved
}

// goInterface is v.Interface(), which raymond called on every result.
func (r *renderer) goInterface(v reflect.Value) any {
	if !v.CanInterface() {
		r.fail("reflect.Value.Interface: cannot return value obtained from unexported field or method")
	}
	return v.Interface()
}

// goList returns v as a reflect.Value when it is a Go slice or array other
// than []any, which raymond iterated and mapped paths over by kind.
func goList(v any) (reflect.Value, bool) {
	if !isGo(v) {
		return reflect.Value{}, false
	}
	rv := reflect.ValueOf(v)
	k := rv.Kind()
	return rv, k == reflect.Slice || k == reflect.Array
}

// goTruth is raymond's IsTrue (text/template's isTrueValue) for a Go value.
func goTruth(v any) bool {
	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Invalid:
		return false
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return val.Len() > 0
	case reflect.Bool:
		return val.Bool()
	case reflect.Complex64, reflect.Complex128:
		return val.Complex() != 0
	case reflect.Chan, reflect.Func, reflect.Pointer, reflect.Interface:
		return !val.IsNil()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return val.Int() != 0
	case reflect.Float32, reflect.Float64:
		return val.Float() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return val.Uint() != 0
	case reflect.Struct:
		return true
	default:
		return false
	}
}

// goAppendStr appends raymond's strValue of a Go value: arrays and slices
// concatenate their elements (a step each, nested ones against MaxDepth),
// numbers print by kind, strings print, and anything else is UNPRINTABLE.
func (r *renderer) goAppendStr(dst []byte, v reflect.Value) []byte {
	// printableValue
	if v.Kind() == reflect.Pointer {
		v, _ = r.goIndirect(v)
	}
	if !v.IsValid() {
		return dst
	}
	if t := v.Type(); !t.Implements(errorType) && !t.Implements(stringerType) {
		pt := reflect.PointerTo(t)
		if v.CanAddr() && (pt.Implements(errorType) || pt.Implements(stringerType)) {
			v = v.Addr()
		} else if k := v.Kind(); k == reflect.Chan || k == reflect.Func {
			r.fail("Can't print value: " + t.String())
		}
	}
	val := reflect.ValueOf(r.goInterface(v))
	n := len(dst)
	switch val.Kind() {
	case reflect.Invalid:
		return dst
	case reflect.Array, reflect.Slice:
		r.enter()
		for i := range val.Len() {
			r.step()
			dst = r.goAppendStr(dst, val.Index(i))
			r.checkProduced(len(dst))
		}
		r.leave()
		return dst
	case reflect.Bool:
		return strconv.AppendBool(dst, val.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		dst = strconv.AppendInt(dst, val.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		dst = strconv.AppendUint(dst, val.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		dst = strconv.AppendFloat(dst, val.Float(), 'f', -1, 64)
	default:
		if val.Type() != stringType {
			return append(dst, "UNPRINTABLE"...)
		}
		s := val.String()
		r.checkProduced(len(dst) + len(s))
		r.read(len(s))
		return append(dst, s...)
	}
	r.formatted(len(dst) - n)
	return dst
}

// goAppendV appends fmt's %v of a Go value (prettyp-num-en's error text).
// fmt recurses without bound into maps, slices and interfaces, so the
// value is walked first, a step per element and bounded by MaxDepth, with
// the text's size estimated and checked; only then does fmt print it.
func (r *renderer) goAppendV(dst []byte, v any) []byte {
	size := r.goSize(reflect.ValueOf(v), 0)
	r.flush()
	r.checkProduced(len(dst) + size)
	n := len(dst)
	dst = fmt.Appendf(dst, "%v", v)
	r.produced(len(dst) - n)
	return dst
}

// goSize walks v as fmt's %v does and returns a bound on the bytes it
// prints, failing past MaxDepth.
func (r *renderer) goSize(v reflect.Value, depth int) int {
	r.step()
	if depth > r.maxDepth {
		panic(errorf(KindLimit, "template evaluation exceeds the maximum depth of %d", r.maxDepth))
	}
	switch v.Kind() {
	case reflect.Invalid:
		return 5
	case reflect.String:
		r.read(v.Len())
		return v.Len()
	case reflect.Interface:
		if v.IsNil() {
			return 5
		}
		return r.goSize(v.Elem(), depth+1)
	case reflect.Pointer:
		if depth == 0 && !v.IsNil() {
			switch v.Elem().Kind() {
			case reflect.Array, reflect.Slice, reflect.Struct, reflect.Map:
				return 1 + r.goSize(v.Elem(), depth+1)
			default: // fmt prints other pointers as an address
			}
		}
		return 20
	case reflect.Array, reflect.Slice:
		n := 2
		for i := range v.Len() {
			n += 1 + r.goSize(v.Index(i), depth+1)
		}
		return n
	case reflect.Map:
		n := 5
		it := v.MapRange()
		for it.Next() {
			n += 2 + r.goSize(it.Key(), depth+1) + r.goSize(it.Value(), depth+1)
		}
		return n
	case reflect.Struct:
		n := 2
		for i := range v.NumField() {
			n += 1 + r.goSize(v.Field(i), depth+1)
		}
		return n
	default:
		return 64
	}
}

// goEach is raymond's eachHelper on a Go value: slices and arrays by
// index, maps with their string keys sorted (@last counts every key, as
// raymond's did), structs over their exported fields in declaration order
// with @key the field name. Other kinds iterate nothing.
func (c *hcall) goEach(ctx any) {
	r := c.r
	val := reflect.ValueOf(ctx)
	switch val.Kind() {
	case reflect.Array, reflect.Slice:
		frame := &dataFrame{parent: r.frame, iter: true}
		boxKey := c.wantsKey()
		for i := range val.Len() {
			r.step()
			frame.setIter(val.Len(), i, nil)
			var key any
			if boxKey {
				key = i
			}
			c.evalBlock(r.goInterface(val.Index(i)), frame, key)
		}
	case reflect.Map:
		total := val.Len()
		r.steps1(int64(total)) // collecting the keys, before allocating
		r.flush()
		keys := make([]string, 0, total)
		it := val.MapRange()
		for it.Next() {
			if k, ok := it.Key().Interface().(string); ok {
				keys = append(keys, k)
			}
		}
		r.sortKeys(keys)
		frame := &dataFrame{parent: r.frame, iter: true}
		for i, k := range keys {
			r.step()
			r.hashKey(len(k))
			frame.setIter(total, i, k)
			c.evalBlock(r.goInterface(val.MapIndex(reflect.ValueOf(k))), frame, k)
		}
	case reflect.Struct:
		p := planFor(val.Type())
		r.steps1(p.cost)
		frame := &dataFrame{parent: r.frame, iter: true}
		for i, fi := range p.exported {
			r.step()
			name := val.Type().Field(fi).Name
			frame.setIter(len(p.exported), i, name)
			c.evalBlock(r.goInterface(val.Field(fi)), frame, name)
		}
	default:
		// raymond iterates arrays, maps and structs only.
	}
}
