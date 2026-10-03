// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"fmt"
	"reflect"
	"strings"
)

// FromGo converts a Go value into a render context with raymond's Go
// semantics, without JSON: Go ints stay ints, so {{to-str n}} and
// {{#if n includeZero=true}} behave as they did when raymond rendered the
// Go value itself. Pointers and interfaces are followed (nil is null).
//
//   - bool, string, int, int8..int64, uint, uint8..uint64, float32 and
//     float64 keep their Go type. A named type of one of these kinds other
//     than string becomes its builtin type (raymond printed it the same way).
//     A named string type prints "UNPRINTABLE", as raymond printed it.
//   - Slices and arrays iterate, index and print like []any, but only a
//     []interface{} is an array to a helper that takes one (len, select,
//     in-string-array); a []int is an error there, as it was. Maps whose key type is string become
//     map[string]any. Other maps print "UNPRINTABLE", are true when not
//     empty, have no fields and iterate nothing.
//   - A struct is looked up as raymond did: name, then strings.Title(name),
//     as an exported (possibly promoted) field, then the first field whose
//     `handlebars` tag is name. #each visits its exported fields in
//     declaration order with @key the field name. It is always true and
//     prints "UNPRINTABLE".
//   - raymond called methods, and funcs held in fields, maps or slices. That
//     is not supported: a lookup that raymond would have resolved to a
//     method or a func fails the render with an error naming it. So do
//     channels, complex numbers and unsafe pointers.
//
// The conversion visits each value once; a pointer, map or slice reached
// twice converts once, so shared and cyclic values are safe. Nesting
// deeper than lim.MaxDepth fails with KindLimit. It charges m (nil: none)
// goValueCost steps per value, plus hashing for each map key and field name,
// and fails with KindLimit past lim.MaxSteps, like a render.
func FromGo(v any, lim Limits, m Meter) (Value, error) {
	if lim.MaxDepth <= 0 {
		lim.MaxDepth = DefaultLimits().MaxDepth
	}
	if lim.MaxSteps <= 0 {
		lim.MaxSteps = DefaultLimits().MaxSteps
	}
	c := &goConv{
		renderer: renderer{meter: m, maxDepth: lim.MaxDepth, maxSteps: lim.MaxSteps},
		seen:     map[goKey]Value{},
	}
	var out Value
	err := c.run(func() { out = c.convert(reflect.ValueOf(v)) })
	if err != nil {
		return nil, err
	}
	return out, nil
}

// goValueCost is the steps converting one Go value costs: reflection and
// boxing take a few hundred ns.
const goValueCost = 4

type goConv struct {
	seen     map[goKey]Value
	renderer // for its step accounting, depth bound and panic recovery
}

// enter bounds the nesting of the context being converted.
func (c *goConv) enter() {
	c.depth++
	if c.depth > c.maxDepth {
		panic(errorf(KindLimit, "Go context nesting exceeds the maximum depth of %d", c.maxDepth))
	}
}

// goKey identifies a pointer, map or slice already converted.
type goKey struct {
	t   reflect.Type
	p   uintptr
	len int
}

// run runs f, recovering the conversion's failures as the renderer does.
func (c *goConv) run(f func()) error {
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = c.recovered(p)
			}
		}()
		f()
		c.flush()
	}()
	return err
}

func (c *goConv) convert(v reflect.Value) Value {
	c.steps1(goValueCost)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			if v.Kind() == reflect.Pointer {
				// raymond printed a nil pointer as UNPRINTABLE.
				return &goOpaque{typ: v.Type().String(), v: "<nil>"}
			}
			return nil
		}
		if v.Kind() == reflect.Pointer {
			k := goKey{t: v.Type(), p: v.Pointer()}
			if seen, ok := c.seen[k]; ok {
				return seen
			}
			if v.Elem().Kind() == reflect.Struct {
				// Registered before its fields convert, so a cycle back to
				// it ends here.
				s := &goStruct{}
				c.seen[k] = s
				c.fillStruct(s, v.Elem())
				return s
			}
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Invalid:
		return nil
	case reflect.Bool:
		return v.Bool()
	case reflect.Int:
		return int(v.Int())
	case reflect.Int8:
		return int8(v.Int()) //nolint:gosec // v is an int8
	case reflect.Int16:
		return int16(v.Int()) //nolint:gosec // v is an int16
	case reflect.Int32:
		return int32(v.Int()) //nolint:gosec // v is an int32
	case reflect.Int64:
		return v.Int()
	case reflect.Uint:
		return uint(v.Uint())
	case reflect.Uint8:
		return uint8(v.Uint()) //nolint:gosec // v is a uint8
	case reflect.Uint16:
		return uint16(v.Uint()) //nolint:gosec // v is a uint16
	case reflect.Uint32:
		return uint32(v.Uint()) //nolint:gosec // v is a uint32
	case reflect.Uint64:
		return v.Uint()
	case reflect.Float32:
		return float32(v.Float())
	case reflect.Float64:
		return v.Float()
	case reflect.String:
		if v.Type() == reflect.TypeFor[string]() {
			return v.String()
		}
		return &goOpaque{typ: v.Type().String(), truth: v.Len() > 0, v: v.String()}
	case reflect.Slice, reflect.Array:
		return c.convertList(v)
	case reflect.Map:
		return c.convertMap(v)
	case reflect.Struct:
		s := &goStruct{}
		c.fillStruct(s, v)
		return s
	default: // Func, Chan, Complex, Uintptr, UnsafePointer
		return &goUnsupported{typ: v.Type().String()}
	}
}

func (c *goConv) convertList(v reflect.Value) Value {
	var k goKey
	if v.Kind() == reflect.Slice {
		k = goKey{t: v.Type(), p: v.Pointer(), len: v.Len()}
		if seen, ok := c.seen[k]; ok && v.Len() > 0 {
			return seen
		}
	}
	out := make([]any, v.Len())
	var res Value = out
	if v.Type() != reflect.TypeFor[[]any]() {
		// svc's helpers took []interface{} and rejected other slice types.
		res = &goList{typ: v.Type().String(), elems: out}
	}
	if k.t != nil && v.Len() > 0 {
		c.seen[k] = res
	}
	c.enter()
	for i := range out {
		out[i] = c.convert(v.Index(i))
	}
	c.leave()
	return res
}

func (c *goConv) convertMap(v reflect.Value) Value {
	if v.Type().Key() != reflect.TypeFor[string]() {
		return &goOpaque{typ: v.Type().String(), truth: v.Len() > 0, v: "map[...]"}
	}
	k := goKey{t: v.Type(), p: v.Pointer()}
	if seen, ok := c.seen[k]; ok && !v.IsNil() {
		return seen
	}
	c.steps1(int64(v.Len()))
	out := make(map[string]any, v.Len())
	var res Value = out
	if v.Type() != reflect.TypeFor[map[string]any]() {
		res = &goMap{typ: v.Type().String(), m: out}
	}
	if !v.IsNil() {
		c.seen[k] = res
	}
	c.enter()
	it := v.MapRange()
	for it.Next() {
		key := it.Key().String()
		c.hashKey(len(key))
		out[key] = c.convert(it.Value())
	}
	c.leave()
	return res
}

// fillStruct converts the exported fields of struct v into s.
func (c *goConv) fillStruct(s *goStruct, v reflect.Value) {
	t := v.Type()
	s.typ = t.String()
	s.methods = t
	if v.CanAddr() {
		// raymond looked methods up on the address when it could take it.
		s.methods = reflect.PointerTo(t)
	}
	c.enter()
	c.steps1(int64(t.NumField()))
	s.byName = map[string]Value{}
	for i := range t.NumField() {
		f := t.Field(i)
		var fv Value
		if f.IsExported() {
			c.hashKey(len(f.Name))
			fv = c.convert(v.Field(i))
			s.fields = append(s.fields, goField{name: f.Name, val: fv})
			s.byName[f.Name] = fv
		} else {
			// raymond's tag lookup could reach it, then fail to read it.
			fv = &goUnsupported{typ: "unexported field " + t.String() + "." + f.Name}
		}
		if tag := f.Tag.Get("handlebars"); tag != "" {
			if s.byTag == nil {
				s.byTag = map[string]Value{}
			}
			if _, dup := s.byTag[tag]; !dup {
				s.byTag[tag] = fv
			}
		}
	}
	// Promoted fields: what FieldByName finds through embedded structs.
	for _, f := range reflect.VisibleFields(t) {
		if len(f.Index) < 2 || !f.IsExported() {
			continue
		}
		if _, ok := s.byName[f.Name]; ok {
			continue
		}
		if g, ok := t.FieldByName(f.Name); !ok || !equalIndex(g.Index, f.Index) {
			continue // ambiguous: FieldByName finds nothing
		}
		c.hashKey(len(f.Name))
		fv, err := v.FieldByIndexErr(f.Index)
		if err != nil {
			s.byName[f.Name] = &goUnsupported{typ: "field " + f.Name + " through a nil embedded pointer"}
			continue
		}
		s.byName[f.Name] = c.convert(fv)
	}
	c.leave()
}

func equalIndex(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// goStruct is a converted Go struct.
type goStruct struct {
	byName  map[string]Value
	byTag   map[string]Value
	methods reflect.Type // the method set raymond searched
	typ     string
	fields  []goField // exported fields, in declaration order
}

type goField struct {
	val  Value
	name string
}

// structField is raymond's evalField on a struct.
func (r *renderer) structField(s *goStruct, name string) (Value, bool) {
	r.hashKey(len(name))
	title := strings.Title(name) //nolint:staticcheck // raymond's exact casing rule
	if _, ok := s.methods.MethodByName(name); ok {
		r.errorf("Go method calls are not supported: %s.%s", s.typ, name)
	}
	if _, ok := s.methods.MethodByName(title); ok {
		r.errorf("Go method calls are not supported: %s.%s", s.typ, title)
	}
	if v, ok := s.byName[title]; ok {
		return r.goChecked(v), true
	}
	if v, ok := s.byTag[name]; ok {
		return r.goChecked(v), true
	}
	return nil, false
}

// goChecked fails the render on a value FromGo could not convert, where
// raymond would have called or printed it.
func (r *renderer) goChecked(v Value) Value {
	if u, ok := v.(*goUnsupported); ok {
		r.errorf("Go value not supported: %s", u.typ)
	}
	return v
}

// goList is a Go slice or array of a type other than []interface{}: it
// iterates, indexes and prints as an array, but a helper that takes an
// array (len, select's from, in-string-array's haystack) rejects it, as
// raymond's reflection did.
type goList struct {
	typ   string
	elems []any
}

// goMap is a Go map with string keys of a type other than
// map[string]interface{}: it looks up, iterates and prints as an object,
// but select, which took map[string]interface{} items, skips it, and helper
// errors name its type.
type goMap struct {
	m   map[string]any
	typ string
}

// unlist returns a goList's elements as an array and a goMap's entries as
// an object, and any other Value unchanged.
func unlist(v Value) Value {
	switch x := v.(type) {
	case *goList:
		return x.elems
	case *goMap:
		return x.m
	default:
		return v
	}
}

// goOpaque is a Go value raymond printed as "UNPRINTABLE" and could not
// look into: a named string type, or a map whose keys are not strings.
type goOpaque struct {
	typ   string
	v     string // its %v form, for error messages
	truth bool
}

// goUnsupported is a func, channel, complex number or unsafe pointer, which
// raymond called or printed; it fails the render when a lookup reaches it.
type goUnsupported struct{ typ string }

// typeName is fmt's %T of a Value, with converted Go values named by their
// original type.
func typeName(v Value) string {
	switch x := v.(type) {
	case *goStruct:
		return x.typ
	case *goList:
		return x.typ
	case *goMap:
		return x.typ
	case *goOpaque:
		return x.typ
	case *goUnsupported:
		return x.typ
	default:
		return fmt.Sprintf("%T", v)
	}
}
