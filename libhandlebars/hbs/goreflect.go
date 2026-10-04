// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"cmp"
	"fmt"
	"math"
	"math/bits"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/luthersystems/svc/libhandlebars/internal/bigcost"
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
//     (raymond called it; lookups never call Go code), then an exported
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
// The one place Go code runs is fmt's %v (prettyp-num-en's error text),
// which calls a value's String or Error method, as raymond's did.
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
	reflectValueType = reflect.TypeFor[reflect.Value]()
	errorType        = reflect.TypeFor[error]()
	stringerType     = reflect.TypeFor[fmt.Stringer]()
	formatterType    = reflect.TypeFor[fmt.Formatter]()
	anySlice         = reflect.TypeFor[[]any]()
	stringType       = reflect.TypeFor[string]()
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
	byTag    map[string]int // a field with no handlebars tag is under ""
	exported []int
}

var structPlans sync.Map // reflect.Type -> *structPlan

func planFor(t reflect.Type) *structPlan {
	if p, ok := structPlans.Load(t); ok {
		return p.(*structPlan) //nolint:forcetypeassert // only *structPlan is stored
	}
	p := &structPlan{byName: fieldsByName(t)}
	for i := range t.NumField() {
		f := t.Field(i)
		// raymond compared every field's tag, an absent one being "", so
		// the empty name finds the first field without one.
		tag := f.Tag.Get("handlebars")
		if p.byTag == nil {
			p.byTag = map[string]int{}
		}
		if _, dup := p.byTag[tag]; !dup {
			p.byTag[tag] = i
		}
		if f.IsExported() {
			p.exported = append(p.exported, i)
		}
	}
	actual, _ := structPlans.LoadOrStore(t, p)
	return actual.(*structPlan) //nolint:forcetypeassert // only *structPlan is stored
}

// plan returns t's plan, charging its build the first time this render
// uses t, whether or not another render has built it (so the charge does
// not depend on the process cache): planFieldCost a field, its embedded
// structs' fields included, and a step per started scanUnit bytes of tags,
// counted and charged field by field before the plan is built (a cold
// build takes 450-900 ns a field).
func (r *renderer) plan(t reflect.Type) *structPlan {
	if !r.planned[t] {
		if r.planned == nil {
			r.planned = map[reflect.Type]bool{}
		}
		r.planned[t] = true
		r.chargePlan(t, map[reflect.Type]bool{}, 0)
		r.flush()
	}
	return planFor(t)
}

const planFieldCost = 12

// boxUnit is the bytes of a copied Go value a step covers: boxing
// allocates (zeroing) and copies, about 0.5 ns a byte for large values.
const boxUnit = 128

func (r *renderer) chargePlan(t reflect.Type, seen map[reflect.Type]bool, depth int) {
	if seen[t] {
		return
	}
	seen[t] = true
	for i := range t.NumField() {
		// Resolving the field copies its index path, depth+1 long.
		r.steps1(planFieldCost + int64(depth))
		f := t.Field(i)
		// Its name is hashed into the plan and its tag parsed.
		r.steps1(units(len(f.Name), scanUnit) + units(len(f.Tag), scanUnit))
		if f.Anonymous {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				r.chargePlan(ft, seen, depth+1)
			}
		}
	}
}

// fieldsByName is what t.FieldByName finds for every name, computed in one
// breadth-first pass with reflect's own rules (FieldByNameFunc): the
// shallowest depth wins, two fields of one name at that depth (or one
// field of a struct type embedded twice at that depth) hide each other and
// everything deeper of that name, and each embedded struct type is visited
// once. reflect.VisibleFields instead revisits a type through every
// embedding path, which is exponential for diamonds.
func fieldsByName(t reflect.Type) map[string]reflect.StructField {
	found, _ := fieldsByNameCount(t)
	return found
}

// fieldsByNameCount is fieldsByName, also returning the fields it visited.
func fieldsByNameCount(t reflect.Type) (map[string]reflect.StructField, int) {
	visits := 0
	type scan struct {
		typ   reflect.Type
		index []int
	}
	found := map[string]reflect.StructField{}
	hidden := map[string]bool{}
	visited := map[reflect.Type]bool{}
	var current []scan
	next := []scan{{typ: t}}
	var count, nextCount map[reflect.Type]int
	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, map[reflect.Type]int{}
		hits := map[string][]reflect.StructField{}
		for _, s := range current {
			if visited[s.typ] {
				continue
			}
			visited[s.typ] = true
			for i := range s.typ.NumField() {
				visits++
				f := s.typ.Field(i)
				f.Index = append(append([]int(nil), s.index...), i)
				if _, done := found[f.Name]; !done && !hidden[f.Name] {
					hits[f.Name] = append(hits[f.Name], f)
					if count[s.typ] > 1 {
						hits[f.Name] = append(hits[f.Name], f) // the type twice: ambiguous
					}
				}
				if !f.Anonymous {
					continue
				}
				ft := f.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() != reflect.Struct {
					continue
				}
				if nextCount[ft] > 0 {
					nextCount[ft] = 2
					continue
				}
				nextCount[ft] = 1
				if count[s.typ] > 1 {
					nextCount[ft] = 2
				}
				next = append(next, scan{typ: ft, index: f.Index})
			}
		}
		for name, fs := range hits {
			if len(fs) == 1 {
				found[name] = fs[0]
			} else {
				hidden[name] = true
			}
		}
	}
	return found, visits
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
		p := r.plan(ctx.Type())
		r.hashKey(len(name))
		if f, ok := p.byName[r.title(name)]; ok && f.IsExported() {
			// A promoted field is len(f.Index) hops down embedded structs.
			r.steps1(units(len(f.Index), 8)) // about 9 ns a hop
			fv, err := ctx.FieldByIndexErr(f.Index)
			if err != nil {
				r.errorf("%s", err.Error())
			}
			result = fv
			break
		}
		// raymond's evalStructTag reads a copy (ctx.Interface()), so a
		// tagged field is not addressable and its pointer methods do not
		// count; and that copy fails, tag or not, for a struct reached
		// through an unexported field.
		if !ctx.CanInterface() {
			r.fail("reflect.Value.Interface: cannot return value obtained from unexported field or method")
		}
		if i, ok := p.byTag[name]; ok {
			result = reflect.ValueOf(r.goInterface(ctx)).Field(i)
		}
	case reflect.Map:
		if stringType.AssignableTo(ctx.Type().Key()) {
			r.hashKey(len(name))
			if size := ctx.Type().Elem().Size(); size > 8 {
				r.steps1(units(int(min(size, 1<<40)), boxUnit)) // capped; MapIndex copies the value
				r.flush()
			}
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

// typeString is fmt's %T of v, without copying the type's name (a Go
// type's name can hold its struct tags, megabytes long).
func typeString(v any) string {
	if v == nil {
		return "<nil>"
	}
	return reflect.TypeOf(v).String()
}

// failType fails with prefix and v's type, sized, checked and charged
// before the text is built.
func (r *renderer) failType(prefix string, v any) {
	ts := typeString(v)
	r.steps1(units(len(prefix)+len(ts), scanUnit))
	r.flush()
	r.failWith(prefix, ts)
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
		// raymond's MethodByName panics on a nil interface whose type has
		// the method.
		r.hashKey(len(name))
		if _, ok := v.Type().MethodByName(name); ok {
			r.fail("reflect: Method on nil interface value")
		}
		if _, ok := v.Type().MethodByName(r.title(name)); ok {
			r.fail("reflect: Method on nil interface value")
		}
		return
	}
	if v.Kind() != reflect.Interface && v.CanAddr() {
		v = v.Addr()
	}
	if v.NumMethod() == 0 {
		return
	}
	r.hashKey(len(name))
	// The type's name is passed as a string, so the error text is sized by
	// it (an anonymous struct type's name holds its field tags).
	if v.MethodByName(name).IsValid() {
		r.errorf("Go method calls are not supported: %s.%s", v.Type().String(), name)
	}
	if t := r.title(name); t != name && v.MethodByName(t).IsValid() {
		r.errorf("Go method calls are not supported: %s.%s", v.Type().String(), t)
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
//
// Boxing copies a value that is not pointer-shaped (a struct, an array):
// its size is charged first, a step per started boxUnit bytes.
func (r *renderer) goInterface(v reflect.Value) any {
	if !v.CanInterface() {
		r.fail("reflect.Value.Interface: cannot return value obtained from unexported field or method")
	}
	if size := v.Type().Size(); size > 8 {
		r.steps1(units(int(min(size, 1<<40)), boxUnit)) // capped
		r.flush()
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

// legacySafeStrings are the packages whose SafeString type raymond's
// strValue printed (and whose top-level value it did not escape): raymond
// itself, which Go callers built contexts with, and the frozen reference
// copy the differential harness renders with.
var legacySafeStrings = map[string]bool{
	"github.com/luthersystems/raymond":                               true,
	"github.com/luthersystems/svc/libhandlebars/internal/raymondref": true,
}

// isLegacySafeString reports whether t is raymond's SafeString, a string
// type it prints as its text and does not escape.
func isLegacySafeString(t reflect.Type) bool {
	return t.Kind() == reflect.String && t.Name() == "SafeString" && legacySafeStrings[t.PkgPath()]
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
		// CanAddr first: PointerTo builds the pointer type from t's name,
		// which a type built at run time can make long.
		if v.CanAddr() && (reflect.PointerTo(t).Implements(errorType) || reflect.PointerTo(t).Implements(stringerType)) {
			v = v.Addr()
		} else if k := v.Kind(); k == reflect.Chan || k == reflect.Func {
			ts := t.String()
			r.steps1(units(len(ts), scanUnit))
			r.failWith("Can't print value: ", ts)
		}
	}
	return r.goAppendKind(dst, reflect.ValueOf(r.goInterface(v)))
}

// goAppendElem appends raymond's strValue of a Go value held in an engine
// []any. raymond saw such an element as an interface value, so its
// printableValue neither followed a pointer nor refused a chan or func:
// those print UNPRINTABLE.
func (r *renderer) goAppendElem(dst []byte, x any) []byte {
	return r.goAppendKind(dst, reflect.ValueOf(x))
}

// goAppendKind is strValue's switch on the kind of a printable value.
func (r *renderer) goAppendKind(dst []byte, val reflect.Value) []byte {
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
		if val.Type() != stringType && !isLegacySafeString(val.Type()) {
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

// goAppendV appends fmt's %v of a Go value (prettyp-num-en's error text),
// nested (an element of an engine array or object, as fmt sees it inside
// the context raymond printed) or not. Where fmt would print a process
// address (a non-nil chan, func or unsafe pointer, or a pointer it does
// not follow), the text would differ between processes: the value is
// printed as "(T)", its type, instead.
// fmt recurses without bound into maps, slices and interfaces, and sorts
// map keys by reflection, so the value is sized first (goSizer), and only
// then does fmt print it. As raymond's did, fmt calls a value's String or
// Error method: a value holding such values is printed by goPrinter, which
// calls fmt on each alone and checks its text before the next.
func (r *renderer) goAppendV(dst []byte, v any, nested bool) []byte {
	// fmt prints a top-level reflect.Value as the value it holds (methods
	// honoured; one it cannot take the value of, by reflection alone: here
	// as its type), and a nested one by its String method.
	if rv, ok := v.(reflect.Value); ok {
		switch {
		case nested:
			// Nested, fmt calls its String method: "<T Value>", or a
			// string Value's string. fmt given it here would unwrap it.
			return r.appendValueString(dst, rv)
		case !rv.IsValid():
		case !rv.CanInterface():
			// fmt prints it by reflection alone, calling no method at
			// the top: size it so, and print it, unless that holds an
			// address.
			return r.goAppendFmt(dst, rv, rv.Type())
		default:
			v = rv.Interface()
			if inner, ok := v.(reflect.Value); ok {
				// fmt unwraps once, then calls the inner Value's String.
				return r.appendValueString(dst, inner)
			}
		}
	}
	depth := 0
	if nested {
		depth = 1 // fmt follows a pointer only at the top
	}
	return r.goFormat(dst, reflect.ValueOf(v), v, depth, reflect.TypeOf(v))
}

// goAppendFmt appends fmt's %v of rv, a reflect.Value fmt prints by
// reflection alone (its value cannot be taken), sized the same way.
func (r *renderer) goAppendFmt(dst []byte, rv reflect.Value, t reflect.Type) []byte {
	return r.goFormat(dst, rv, rv, 0, t)
}

// goFormat sizes sv (at depth), then appends fmt's %v of arg, or "(T)"
// for t where the text would hold an address.
func (r *renderer) goFormat(dst []byte, sv reflect.Value, arg any, depth int, t reflect.Type) []byte {
	z := &goSizer{r: r, limit: r.maxSteps - r.steps - r.pending + 1}
	size := z.size(sv, depth)
	// The walk's charge, size and outcome do not depend on Go's map
	// order: it counts every node up to MaxDepth, or stops once the count
	// passes what MaxSteps leaves, and reports depth only after.
	r.steps1(min(z.steps, z.limit))
	r.flush()
	if z.deep {
		panic(errorf(KindLimit, "template evaluation exceeds the maximum depth of %d", r.maxDepth))
	}
	if z.nanKeys {
		// fmt orders NaN keys among themselves by Go's map order, so the
		// text would differ from run to run.
		r.fail("Go map with more than one NaN key has no deterministic text")
	}
	if z.addr {
		return r.appendTypeName(dst, t)
	}
	r.checkProduced(len(dst) + size)
	n := len(dst)
	if z.methods {
		// size is exact but for the method results: grow once for it.
		p := goPrinter{r: r}
		dst = p.print(slices.Grow(dst, size), sv, depth)
	} else {
		dst = fmt.Appendf(dst, "%v", arg)
	}
	r.produced(len(dst) - n)
	return dst
}

// fmtMethod is what fmt's handleMethods finds on a type: whether it calls
// a method (Format, else Error, else String), and, for a pointer type,
// whether that method is declared on the value type (called through a
// nil pointer it panics, and fmt prints "<nil>").
type fmtMethod struct{ has, onValue bool }

var fmtMethods sync.Map // reflect.Type -> fmtMethod

// fmtMethodOf is t's fmtMethod, found once per type (Implements scans the
// method set).
func fmtMethodOf(t reflect.Type) fmtMethod {
	if m, ok := fmtMethods.Load(t); ok {
		return m.(fmtMethod) //nolint:forcetypeassert // stored below
	}
	var m fmtMethod
	for _, it := range []reflect.Type{formatterType, errorType, stringerType} {
		if t.Implements(it) {
			m = fmtMethod{has: true, onValue: t.Kind() == reflect.Pointer && t.Elem().Implements(it)}
			break
		}
	}
	fmtMethods.Store(t, m)
	return m
}

// nilValueMethod reports whether v is a nil pointer whose method fmt would
// call is declared on the value type: fmt prints "<nil>".
func nilValueMethod(v reflect.Value) bool {
	return v.Kind() == reflect.Pointer && v.IsNil() && fmtMethodOf(v.Type()).onValue
}

// goPrinter prints fmt's %v of a value holding values with a Format, Error
// or String method.
type goPrinter struct {
	r       *renderer
	scratch []byte // a method's text, before it is checked
}

// print appends fmt's %v of v at depth, walking it as fmt's printValue
// does (goSizer's walk), and calling fmt only for each value with a
// Format, Error or String method. The bytes each such call produced are
// charged and checked against the produced-bytes bound before they are
// appended, so one method's result is the most it builds past the bound.
// goFormat has sized v first: it holds no address, no two NaN keys, and
// nothing past MaxDepth.
func (p *goPrinter) print(dst []byte, v reflect.Value, depth int) []byte {
	if v.IsValid() && v.Type() == reflectValueType && depth > 0 && v.CanInterface() {
		rv, _ := v.Interface().(reflect.Value)
		return append(dst, rv.String()...)
	}
	if v.IsValid() && v.Kind() != reflect.Interface && v.CanInterface() {
		if fmtMethodOf(v.Type()).has {
			if nilValueMethod(v) {
				p.scratch = append(p.scratch[:0], "<nil>"...)
			} else {
				p.scratch = fmt.Appendf(p.scratch[:0], "%v", v.Interface())
			}
			n := len(p.scratch)
			p.r.scanBytes(n)
			p.r.checkProduced(len(dst) + n)
			if n > cap(dst)-len(dst) {
				// Double (these may be many), but not past the bound.
				room := int(min(p.r.maxProduced-p.r.written, math.MaxInt)) - len(dst)
				dst = slices.Grow(dst, max(n, min(cap(dst), room)))
			}
			return append(dst, p.scratch...)
		}
	}
	switch v.Kind() {
	case reflect.Invalid:
		return append(dst, "<nil>"...)
	case reflect.String:
		return append(dst, v.String()...)
	case reflect.Interface:
		if v.IsNil() {
			return append(dst, "<nil>"...)
		}
		return p.print(dst, v.Elem(), depth+1)
	case reflect.Pointer:
		// Not an address (goSizer): nil, or followed at the top.
		if v.IsNil() {
			return append(dst, "<nil>"...)
		}
		return p.print(append(dst, '&'), v.Elem(), depth+1)
	case reflect.Array, reflect.Slice:
		dst = append(dst, '[')
		for i := range v.Len() {
			if i > 0 {
				dst = append(dst, ' ')
			}
			dst = p.print(dst, v.Index(i), depth+1)
		}
		return append(dst, ']')
	case reflect.Map:
		type entry struct{ k, v reflect.Value }
		entries := make([]entry, 0, v.Len())
		it := v.MapRange()
		for it.Next() {
			entries = append(entries, entry{it.Key(), it.Value()})
		}
		// fmtsort's stable sort. goSizer refused keys whose order Go's
		// map order or an address decides.
		var byAddr bool
		slices.SortStableFunc(entries, func(a, b entry) int { return fmtsortCompare(a.k, b.k, &byAddr) })
		dst = append(dst, "map["...)
		for i, e := range entries {
			if i > 0 {
				dst = append(dst, ' ')
			}
			dst = append(p.print(dst, e.k, depth+1), ':')
			dst = p.print(dst, e.v, depth+1)
		}
		return append(dst, ']')
	case reflect.Struct:
		dst = append(dst, '{')
		for i := range v.NumField() {
			if i > 0 {
				dst = append(dst, ' ')
			}
			dst = p.print(dst, v.Field(i), depth+1)
		}
		return append(dst, '}')
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return append(dst, "<nil>"...) // not an address (goSizer): nil
	case reflect.Bool:
		return strconv.AppendBool(dst, v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.AppendInt(dst, v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.AppendUint(dst, v.Uint(), 10)
	case reflect.Float32:
		return fmt.Appendf(dst, "%v", float32(v.Float()))
	case reflect.Float64:
		return fmt.Appendf(dst, "%v", v.Float())
	case reflect.Complex64:
		return fmt.Appendf(dst, "%v", complex64(v.Complex()))
	default:
		return fmt.Appendf(dst, "%v", v.Complex())
	}
}

// appendValueString appends rv.String(), as fmt prints a reflect.Value it
// does not unwrap: "<T Value>", or a string Value's string.
func (r *renderer) appendValueString(dst []byte, rv reflect.Value) []byte {
	str := rv.String()
	r.steps1(units(len(str), hashUnit))
	r.checkProduced(len(dst) + len(str))
	r.produced(len(str))
	return append(dst, str...)
}

// appendTypeName appends "(T)", t's name in parentheses: the deterministic
// text printed for a value whose %v would hold a process address.
func (r *renderer) appendTypeName(dst []byte, t reflect.Type) []byte {
	ts := t.String()
	r.steps1(units(len(ts), scanUnit))
	r.checkProduced(len(dst) + len(ts) + 2)
	dst = append(append(append(dst, '('), ts...), ')')
	r.produced(len(ts) + 2)
	return dst
}

// goSizer walks a Go value as fmt's %v does, counting steps and bounding
// the bytes it prints.
type goSizer struct {
	r            *renderer
	steps, limit int64
	deep         bool
	nanKeys      bool // a map with more than one key not equal to itself (NaN)
	methods      bool // fmt would call a Format, Error or String method (sized as 0)
	addr         bool // fmt would print an address (a chan, func, unsafe or nested pointer)
}

func (z *goSizer) over() bool { return z.steps >= z.limit }

func (z *goSizer) size(v reflect.Value, depth int) int {
	z.steps++
	if z.over() {
		return 0
	}
	if depth > z.r.maxDepth {
		z.deep = true
		return 0
	}
	// A reflect.Value below the top prints by its String method (fmt
	// unwraps only a top-level one, which goAppendV does too): "<T Value>",
	// or a string Value's string.
	// (One fmt cannot take, it prints by reflection, as the struct it is:
	// the Struct case below walks it, finding its pointers' addresses.)
	if v.IsValid() && v.Type() == reflectValueType && depth > 0 && v.CanInterface() {
		rv, _ := v.Interface().(reflect.Value)
		if rv.Kind() == reflect.String {
			z.steps += units(rv.Len(), hashUnit)
			return rv.Len()
		}
		if !rv.IsValid() {
			return len("<invalid Value>")
		}
		ts := rv.Type().String()
		z.steps += units(len(ts), scanUnit)
		return len(ts) + len("< Value>")
	}
	// fmt prints a value with a Format, Error or String method by calling
	// it (wherever it can take the value: at the top always, but for a
	// reflect.Value of an unexported field), so it does
	// not look inside: neither does the walk. Its text is counted as it
	// is printed (goPrinter); math/big's methods, whose work grows faster
	// than their text, are charged here, before they run. Another
	// method's own cost is the caller's.
	if v.IsValid() && v.Kind() != reflect.Interface && v.CanInterface() {
		if fmtMethodOf(v.Type()).has {
			z.methods = true
			if nilValueMethod(v) {
				return len("<nil>") // goPrinter writes it without the call
			}
			if c, ok := bigcost.Steps(v.Interface()); ok {
				z.steps += min(c, z.limit)
			}
			return 0
		}
	}
	switch v.Kind() {
	case reflect.Invalid:
		return 5
	case reflect.String:
		z.steps += units(v.Len(), hashUnit)
		return v.Len()
	case reflect.Interface:
		if v.IsNil() {
			return 5
		}
		return z.size(v.Elem(), depth+1)
	case reflect.Pointer:
		if depth == 0 && !v.IsNil() {
			switch v.Elem().Kind() {
			case reflect.Array, reflect.Slice, reflect.Struct, reflect.Map:
				return 1 + z.size(v.Elem(), depth+1)
			default: // fmt prints other pointers as an address
			}
		}
		if v.IsNil() {
			return len("<nil>")
		}
		z.addr = true // fmt prints its address
		return 20
	case reflect.Array, reflect.Slice:
		n := 2 // the brackets, and a space between elements
		for i := 0; i < v.Len() && !z.over(); i++ {
			if i > 0 {
				n++
			}
			n += z.size(v.Index(i), depth+1)
		}
		return n
	case reflect.Map:
		// fmt sorts the keys by reflection (internal/fmtsort): about
		// log2(n) comparisons a key, each as long as the key (see keyCmp).
		logn := int64(1 + bits.Len(uint(v.Len())))
		// MapRange copies each key and value out of the map.
		z.steps += int64(v.Len()) * units(int(min(v.Type().Key().Size()+v.Type().Elem().Size(), 1<<40)), boxUnit)
		n := 5
		nans := 0
		kw := &keyWalk{limit: z.limit, maxDepth: z.r.maxDepth}
		it := v.MapRange()
		for it.Next() && !z.over() {
			k := it.Key()
			if kw.nan(k, depth+1) {
				nans++
			}
			if kw.visits > kw.limit {
				z.steps = max(z.steps, z.limit) // past MaxSteps: a step-limit error
				break
			}
			z.steps += logn * kw.cmp(k, depth+1)
			if kw.visits > kw.limit {
				z.steps = max(z.steps, z.limit)
				break
			}
			if n > 5 {
				n++ // the space before this entry
			}
			n += 1 + z.size(k, depth+1) + z.size(it.Value(), depth+1)
		}
		if nans > 1 {
			z.nanKeys = true
		}
		// fmt compares keys by reflection all the way down, past any
		// String or Error method (where z.size stops), so a key deeper
		// than MaxDepth is a depth error before fmt runs, as the size
		// walk's own.
		if kw.deep && v.Len() > 1 {
			z.deep = true
		}
		// Keys holding pointers (behind a String or Error method, say,
		// where the size walk stops) may be ordered by address: then the
		// text would differ between runs, and it prints as its type.
		if kw.sawPtr && v.Len() > 1 && !z.deep && !z.over() && keysOrderedByAddress(v) {
			z.addr = true
		}
		return n
	case reflect.Struct:
		n := 2 // the braces, and a space between fields
		for i := 0; i < v.NumField() && !z.over(); i++ {
			if i > 0 {
				n++
			}
			n += z.size(v.Field(i), depth+1)
		}
		return n
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		if v.IsNil() {
			return len("<nil>")
		}
		z.addr = true // fmt prints its address
		return 64
	case reflect.Bool:
		if v.Bool() {
			return 4
		}
		return 5
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var buf [24]byte
		return len(strconv.AppendInt(buf[:0], v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var buf [24]byte
		return len(strconv.AppendUint(buf[:0], v.Uint(), 10))
	case reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		// fmt's own %v text, exactly (about 100 ns: a step more), from the
		// value itself: an unexported field's cannot be taken (Interface).
		z.steps++
		var buf [64]byte
		var x any
		switch v.Kind() {
		case reflect.Float32:
			x = float32(v.Float())
		case reflect.Float64:
			x = v.Float()
		case reflect.Complex64:
			x = complex64(v.Complex())
		default:
			x = v.Complex()
		}
		return len(fmt.Appendf(buf[:0], "%v", x))
	default:
		return 64
	}
}

// cmpUnit is the bytes of two strings' common prefix a step of comparing
// them covers (memory compared at a few GB/s).
const cmpUnit = 256

// keyWalk walks map keys as fmt's sort compares them (internal/fmtsort,
// which follows interfaces, arrays and structs whatever methods they
// have), stopping at maxDepth and recording that it did.
type keyWalk struct {
	limit    int64 // stop counting past it
	visits   int64 // values nan and cmp have visited, all keys together
	maxDepth int
	deep     bool // a key goes past maxDepth
	sawPtr   bool // a key holds a non-nil pointer, chan or unsafe pointer
}

// past reports whether depth is past maxDepth, recording it.
func (kw *keyWalk) past(depth int) bool {
	if depth > kw.maxDepth {
		kw.deep = true
		return true
	}
	return false
}

// cmp is the steps fmtsort takes comparing k with another key, by k's
// length: a string by its bytes, an array or struct element by element
// (each a reflection call, a quarter step for a scalar), an interface or
// float by its slower path.
func (kw *keyWalk) cmp(k reflect.Value, depth int) int64 {
	if kw.visits++; kw.visits > kw.limit {
		return 1
	}
	if kw.past(depth) {
		return 1
	}
	switch k.Kind() {
	case reflect.String:
		return 1 + int64(k.Len()/cmpUnit)
	case reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return 4
	case reflect.Interface:
		if k.IsNil() {
			return 4
		}
		return 4 + kw.cmp(k.Elem(), depth+1)
	case reflect.Array:
		switch k.Type().Elem().Kind() {
		case reflect.String, reflect.Interface, reflect.Array, reflect.Struct,
			reflect.Pointer, reflect.Chan, reflect.UnsafePointer: // each looked at (sawPtr)
		default: // elements of one fixed cost, about 5 ns each
			return 1 + int64(k.Len())*kw.cmp(reflect.Zero(k.Type().Elem()), depth+1)/4
		}
		n := int64(1)
		for i := 0; i < k.Len() && n <= kw.limit && kw.visits <= kw.limit; i++ {
			n += kw.cmp(k.Index(i), depth+1)
		}
		return n
	case reflect.Struct:
		n := int64(1)
		for i := 0; i < k.NumField() && n <= kw.limit && kw.visits <= kw.limit; i++ {
			n += kw.cmp(k.Field(i), depth+1)
		}
		return n
	case reflect.Pointer, reflect.Chan, reflect.UnsafePointer:
		if !k.IsNil() {
			kw.sawPtr = true // fmtsort compares these by address
		}
		return 1
	default:
		return 1
	}
}

// keysOrderedByAddress reports whether fmt's sort of map m's keys would be
// decided, for some pair, by an address (fmtsort compares pointers, chans
// and unsafe pointers by address: their order follows the heap, so the
// text would differ between runs). It sorts the keys with fmtsort's
// comparison, noting where an address decides: two keys whose order an
// address decides are compared at it by any comparison sort, directly or
// through a key equal to both up to it. The comparisons are charged with
// the sizer's key costs (keyWalk.cmp).
func keysOrderedByAddress(m reflect.Value) bool {
	keys := make([]reflect.Value, 0, m.Len())
	it := m.MapRange()
	for it.Next() {
		keys = append(keys, it.Key())
	}
	byAddr := false
	slices.SortFunc(keys, func(a, b reflect.Value) int { return fmtsortCompare(a, b, &byAddr) })
	return byAddr
}

// fmtsortCompare is internal/fmtsort's compare, setting *byAddr where two
// different addresses decide it. (An interface's dynamic types are
// ordered by their type descriptors' addresses too, which are fixed in a
// build: not noted.)
func fmtsortCompare(a, b reflect.Value, byAddr *bool) int {
	switch a.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cmp.Compare(a.Int(), b.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return cmp.Compare(a.Uint(), b.Uint())
	case reflect.String:
		return cmp.Compare(a.String(), b.String())
	case reflect.Float32, reflect.Float64:
		return cmp.Compare(a.Float(), b.Float())
	case reflect.Complex64, reflect.Complex128:
		ac, bc := a.Complex(), b.Complex()
		if c := cmp.Compare(real(ac), real(bc)); c != 0 {
			return c
		}
		return cmp.Compare(imag(ac), imag(bc))
	case reflect.Bool:
		switch {
		case a.Bool() == b.Bool():
			return 0
		case a.Bool():
			return 1
		default:
			return -1
		}
	case reflect.Pointer, reflect.UnsafePointer, reflect.Chan:
		ap, bp := a.Pointer(), b.Pointer()
		if ap != bp && ap != 0 && bp != 0 {
			*byAddr = true
		}
		return cmp.Compare(ap, bp)
	case reflect.Struct:
		for i := range a.NumField() {
			if c := fmtsortCompare(a.Field(i), b.Field(i), byAddr); c != 0 {
				return c
			}
		}
		return 0
	case reflect.Array:
		for i := range a.Len() {
			if c := fmtsortCompare(a.Index(i), b.Index(i), byAddr); c != 0 {
				return c
			}
		}
		return 0
	case reflect.Interface:
		switch an, bn := a.IsNil(), b.IsNil(); {
		case an && bn:
			return 0
		case an:
			return -1
		case bn:
			return 1
		}
		at, bt := a.Elem().Type(), b.Elem().Type()
		if at != bt {
			return cmp.Compare(reflect.ValueOf(at).Pointer(), reflect.ValueOf(bt).Pointer())
		}
		return fmtsortCompare(a.Elem(), b.Elem(), byAddr)
	default:
		return 0
	}
}

// nan reports whether map key k is unequal to itself (k.Equal(k) is
// false): a NaN, or an array, struct or interface holding one. Unlike
// Value.Equal, it stops past maxDepth.
//
// Its visits are uncharged (cmp charges the same values), but bounded
// with cmp's: past the limit both stop, and the caller reports it.
func (kw *keyWalk) nan(k reflect.Value, depth int) bool {
	if kw.visits++; kw.visits > kw.limit || kw.past(depth) {
		return false
	}
	switch k.Kind() {
	case reflect.Float32, reflect.Float64:
		return math.IsNaN(k.Float())
	case reflect.Complex64, reflect.Complex128:
		c := k.Complex()
		return math.IsNaN(real(c)) || math.IsNaN(imag(c))
	case reflect.Interface:
		return !k.IsNil() && kw.nan(k.Elem(), depth+1)
	case reflect.Array:
		switch k.Type().Elem().Kind() {
		case reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128,
			reflect.Interface, reflect.Array, reflect.Struct:
		default: // no element can hold a NaN
			return false
		}
		for i := 0; i < k.Len() && kw.visits <= kw.limit; i++ {
			if kw.nan(k.Index(i), depth+1) {
				return true
			}
		}
		return false
	case reflect.Struct:
		for i := 0; i < k.NumField() && kw.visits <= kw.limit; i++ {
			if kw.nan(k.Field(i), depth+1) {
				return true
			}
		}
		return false
	default:
		return false
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
		// Only a string or an interface key can hold a string: iterating
		// any other key type would copy each key for nothing.
		if kt := val.Type().Key(); kt == stringType || kt.Kind() == reflect.Interface {
			it := val.MapRange()
			for it.Next() {
				if k, ok := it.Key().Interface().(string); ok {
					keys = append(keys, k)
				}
			}
		}
		r.sortKeys(keys)
		frame := &dataFrame{parent: r.frame, iter: true}
		for i, k := range keys {
			r.step()
			r.hashKey(len(k))
			if size := val.Type().Elem().Size(); size > 8 {
				r.steps1(units(int(min(size, 1<<40)), boxUnit)) // capped; MapIndex copies the value
			}
			frame.setIter(total, i, k)
			c.evalBlock(r.goInterface(val.MapIndex(reflect.ValueOf(k))), frame, k)
		}
	case reflect.Struct:
		p := r.plan(val.Type())
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
