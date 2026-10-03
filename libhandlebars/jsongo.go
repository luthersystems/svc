// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
	"bytes"
	"cmp"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/luthersystems/svc/libhandlebars/hbs"
)

// The cost of json.Marshal on a Go value, charged before it runs.
//
// goJSONCost walks a value exactly as encoding/json's encoder does: the
// same encoder for each type (Marshaler and TextMarshaler, by pointer when
// the value is addressable; base64 for []byte; the struct fields
// encoding/json selects, with its embedding, tag, "-", omitempty and
// omitzero rules; map keys in its sorted order), and the same errors, in
// the same order: unsupported types, NaN and infinite floats, invalid
// json.Number literals, map key encoding errors, and pointer, map and
// slice cycles. A cycle is reported with encoding/json's text without
// running json.Marshal, which would encode a thousand levels of it first.
// A subtree reached again through the same pointer, map or slice (a DAG)
// is charged again, as encoding/json encodes it again, from a memo rather
// than by walking it again.
//
// The charges: 3 steps a value plus a step per started KiB of estimated
// JSON (strings at their escaped length, after a scan of a step per started
// 64 bytes), and n(1 + log2 n) per map for the key sort. A Marshaler's or
// TextMarshaler's method is the caller's work, not walked (its output is
// charged by its consumer: JSONCost or FromJSONMetered).

// jsonCoster receives the charges.
type jsonCoster interface {
	charge(values, bytes int64) error
	steps(n int64) error
}

var (
	marshalerType     = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	jsonNumberType    = reflect.TypeFor[json.Number]()
	isZeroerType      = reflect.TypeFor[interface{ IsZero() bool }]()
)

// jsonFailure is an error encoding/json would return; the walk stops there.
type jsonFailure struct{ msg string }

func (e *jsonFailure) Error() string { return e.msg }

// jsonTotals are a subtree's charges, kept to charge a DAG's shared
// subtrees again without walking them.
type jsonTotals struct{ values, bytes, steps int64 }

func (t *jsonTotals) add(o jsonTotals) {
	t.values += o.values
	t.bytes += o.bytes
	t.steps += o.steps
}

type jsonWalker struct {
	c        jsonCoster
	path     map[any]int
	memo     map[jsonMemoKey]jsonTotals
	typed1   map[reflect.Type]bool // struct types whose field list this walk has charged
	pathType []reflect.Type        // the types of the pointer-like values on the path
	skipped  []skippedMarshaler    // methods passed over, in encoding/json's order
	maxDepth int                   // container nesting allowed (0: none, as encoding/json)
}

// skippedMarshaler is a MarshalJSON or MarshalText the walk did not call.
type skippedMarshaler struct {
	v    reflect.Value
	text bool
}

// marshalerLeaf records a Marshaler or TextMarshaler value encoding/json
// would call (a nil pointer it writes as null, without calling).
func (w *jsonWalker) marshalerLeaf(v reflect.Value, text bool) (jsonTotals, error) {
	if v.Kind() != reflect.Pointer || !v.IsNil() {
		w.skipped = append(w.skipped, skippedMarshaler{v, text})
	}
	return w.leaf(1) // its output is not known here: at least a byte
}

// fail returns encoding/json's error text made of parts, charging its
// length first: a type's name can hold megabytes of struct tags.
func (w *jsonWalker) fail(parts ...string) error {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	if err := w.c.steps(int64(n/16 + 1)); err != nil {
		return err
	}
	return &jsonFailure{strings.Join(parts, "")}
}

// firstMarshalerError calls the methods the walk passed over, in
// encoding/json's order, and returns the first error encoding/json would
// report for one (with its text), or nil. The walk failed after them, so
// encoding/json would have called each before reaching that failure.
func (w *jsonWalker) firstMarshalerError() error {
	for _, m := range w.skipped {
		if m.text {
			if _, err := m.v.Interface().(encoding.TextMarshaler).MarshalText(); err != nil { //nolint:forcetypeassert // recorded as one
				return w.fail("json: error calling MarshalText for type ", m.v.Type().String(), ": ", err.Error())
			}
			continue
		}
		b, err := m.v.Interface().(json.Marshaler).MarshalJSON() //nolint:forcetypeassert // recorded as one
		if err == nil {
			// encoding/json compacts the bytes, which checks them.
			var buf bytes.Buffer
			err = json.Compact(&buf, b)
		}
		if err != nil {
			return w.fail("json: error calling MarshalJSON for type ", m.v.Type().String(), ": ", err.Error())
		}
	}
	return nil
}

type jsonMemoKey struct {
	t     reflect.Type
	p     any
	depth int // with a depth bound, a subtree's outcome depends on where it starts
}

// goJSONCost charges json.Marshal(v) to c. It returns a *jsonFailure where
// encoding/json fails, and c's errors unchanged.
func goJSONCost(c jsonCoster, v reflect.Value, maxDepth int) error {
	w := &jsonWalker{c: c, maxDepth: maxDepth, path: map[any]int{}, memo: map[jsonMemoKey]jsonTotals{}}
	_, err := w.value(v, 0)
	var fail *jsonFailure
	if errors.As(err, &fail) && len(w.skipped) > 0 {
		if merr := w.firstMarshalerError(); merr != nil {
			return merr
		}
	}
	return err
}

func (w *jsonWalker) leaf(bytes int64) (jsonTotals, error) {
	return jsonTotals{values: 1, bytes: bytes}, w.c.charge(1, bytes)
}

func (w *jsonWalker) value(v reflect.Value, depth int) (jsonTotals, error) {
	if !v.IsValid() {
		return w.leaf(4)
	}
	return w.typed(v, v.Type(), true, depth)
}

// typed is newTypeEncoder(t, allowAddr) applied to v.
func (w *jsonWalker) typed(v reflect.Value, t reflect.Type, allowAddr bool, depth int) (jsonTotals, error) {
	if t.Kind() != reflect.Pointer && allowAddr && reflect.PointerTo(t).Implements(marshalerType) && v.CanAddr() {
		return w.marshalerLeaf(v.Addr(), false)
	}
	if t.Implements(marshalerType) {
		return w.marshalerLeaf(v, false)
	}
	if t.Kind() != reflect.Pointer && allowAddr && reflect.PointerTo(t).Implements(textMarshalerType) && v.CanAddr() {
		return w.marshalerLeaf(v.Addr(), true)
	}
	if t.Implements(textMarshalerType) {
		return w.marshalerLeaf(v, true)
	}
	// Leaves are sized at the bytes encoding/json surely writes (exact, or
	// a lower bound for a float), so a sum past the allocation cap means
	// the encoder would pass it too.
	switch t.Kind() {
	case reflect.Bool:
		if v.Bool() {
			return w.leaf(4)
		}
		return w.leaf(5)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var buf [24]byte
		return w.leaf(int64(len(strconv.AppendInt(buf[:0], v.Int(), 10))))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var buf [24]byte
		return w.leaf(int64(len(strconv.AppendUint(buf[:0], v.Uint(), 10))))
	case reflect.Float32, reflect.Float64:
		if f := v.Float(); math.IsInf(f, 0) || math.IsNaN(f) {
			return jsonTotals{}, &jsonFailure{"json: unsupported value: " + strconv.FormatFloat(f, 'g', -1, t.Bits())}
		}
		return w.leaf(1)
	case reflect.String:
		s := v.String()
		if t == jsonNumberType {
			num := s
			if num == "" {
				num = "0"
			}
			if !validNumber(num) {
				return jsonTotals{}, &jsonFailure{fmt.Sprintf("json: invalid number literal %q", num)}
			}
			return w.leaf(int64(len(num))) // written unquoted
		}
		scan := int64(0)
		if len(s) > 0 {
			scan = int64((len(s)-1)/64 + 1)
			if err := w.c.steps(scan); err != nil {
				return jsonTotals{}, err
			}
		}
		tot, err := w.leaf(jsonStringLen(s))
		tot.steps += scan
		return tot, err
	case reflect.Interface:
		if v.IsNil() {
			return w.leaf(4)
		}
		return w.value(v.Elem(), depth)
	case reflect.Struct:
		return w.structValue(v, t, depth)
	case reflect.Map:
		return w.mapValue(v, t, depth)
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			p := reflect.PointerTo(t.Elem())
			if !p.Implements(marshalerType) && !p.Implements(textMarshalerType) {
				if v.IsNil() {
					return w.leaf(4)
				}
				return w.leaf(int64(base64.StdEncoding.EncodedLen(v.Len())) + 2) // base64, quoted
			}
		}
		if v.IsNil() {
			return w.leaf(4)
		}
		return w.pointerLike(v, struct {
			p any
			n int
		}{v.UnsafePointer(), v.Len()}, func() (jsonTotals, error) { return w.array(v, t, depth) })
	case reflect.Array:
		return w.array(v, t, depth)
	case reflect.Pointer:
		if v.IsNil() {
			return w.leaf(4)
		}
		// encoding/json keys a pointer by v.Interface(): its type and
		// address, so a pointer to a struct's first field is not the
		// struct's pointer. Maps and slices are keyed by address alone.
		return w.pointerLike(v, struct {
			t reflect.Type
			p any
		}{t, v.UnsafePointer()}, func() (jsonTotals, error) {
			return w.typed(v.Elem(), t.Elem(), true, depth)
		})
	default: // Complex, Chan, Func, UnsafePointer
		return jsonTotals{}, w.fail("json: unsupported type: ", t.String())
	}
}

// pointerLike walks a pointer, map or slice: a cycle back to a value on the
// path is the error encoding/json reports, and a value walked before (not
// on the path) is charged from the memo.
func (w *jsonWalker) pointerLike(v reflect.Value, key any, walk func() (jsonTotals, error)) (jsonTotals, error) {
	if start, ok := w.path[key]; ok {
		// encoding/json starts recording the path once its pointer level
		// passes 1000 (the value at index 1000) and reports the first
		// recorded value seen again: in this cycle (path[start:]), the one
		// at index 1000.
		n := len(w.pathType) - start
		at := max(1000, start)
		typ := w.pathType[start+(at-start)%n]
		return jsonTotals{}, w.fail("json: unsupported value: encountered a cycle via ", typ.String())
	}
	mk := jsonMemoKey{t: v.Type(), p: key}
	if w.maxDepth > 0 {
		mk.depth = len(w.pathType) + 1
	}
	if tot, ok := w.memo[mk]; ok {
		if err := w.c.charge(tot.values, tot.bytes); err != nil {
			return jsonTotals{}, err
		}
		return tot, w.c.steps(tot.steps)
	}
	w.path[key] = len(w.pathType)
	w.pathType = append(w.pathType, v.Type())
	tot, err := walk()
	w.pathType = w.pathType[:len(w.pathType)-1]
	delete(w.path, key)
	if err == nil {
		w.memo[mk] = tot
	}
	return tot, err
}

func (w *jsonWalker) nest(depth int) error {
	if w.maxDepth > 0 && depth >= w.maxDepth {
		return &jsonFailure{fmt.Sprintf("json: Go value nests deeper than %d", w.maxDepth)}
	}
	return nil
}

func (w *jsonWalker) array(v reflect.Value, t reflect.Type, depth int) (jsonTotals, error) {
	if err := w.nest(depth); err != nil {
		return jsonTotals{}, err
	}
	tot, err := w.leaf(2)
	if err != nil {
		return tot, err
	}
	for i := range v.Len() {
		sub, err := w.typed(v.Index(i), t.Elem(), true, depth+1)
		tot.add(sub)
		if err != nil {
			return tot, err
		}
	}
	return tot, nil
}

func (w *jsonWalker) mapValue(v reflect.Value, t reflect.Type, depth int) (jsonTotals, error) {
	switch t.Key().Kind() {
	case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
	default:
		if !t.Key().Implements(textMarshalerType) {
			return jsonTotals{}, w.fail("json: unsupported type: ", t.String())
		}
	}
	if v.IsNil() {
		return w.leaf(4)
	}
	return w.pointerLike(v, v.UnsafePointer(), func() (jsonTotals, error) {
		if err := w.nest(depth); err != nil {
			return jsonTotals{}, err
		}
		n := v.Len()
		sort := int64(n) * int64(1+bits.Len(uint(n)))
		if err := w.c.steps(sort); err != nil {
			return jsonTotals{}, err
		}
		tot, err := w.leaf(2)
		tot.steps += sort
		if err != nil {
			return tot, err
		}
		type kv struct {
			ks string
			v  reflect.Value
		}
		kvs := make([]kv, 0, n)
		it := v.MapRange()
		for it.Next() {
			ks, err := mapKeyString(it.Key())
			if err != nil {
				return tot, w.fail("json: encoding error for type ", strconv.Quote(t.String()), ": ", strconv.Quote(err.Error()))
			}
			kvs = append(kvs, kv{ks, it.Value()})
		}
		slices.SortFunc(kvs, func(a, b kv) int { return strings.Compare(a.ks, b.ks) })
		for i := 1; i < len(kvs); i++ {
			if kvs[i].ks == kvs[i-1].ks {
				// encoding/json writes both, in Go's map order, and a
				// decoder keeps the last: the result would vary by run.
				return tot, w.fail("json: map ", t.String(), " has two keys that encode as ", strconv.Quote(kvs[i].ks))
			}
		}
		for _, e := range kvs {
			sub, err := w.leaf(jsonStringLen(e.ks) + 1)
			tot.add(sub)
			if err != nil {
				return tot, err
			}
			sub, err = w.typed(e.v, t.Elem(), true, depth+1)
			tot.add(sub)
			if err != nil {
				return tot, err
			}
		}
		return tot, nil
	})
}

// mapKeyString is encoding/json's reflectWithString.resolve.
func mapKeyString(k reflect.Value) (string, error) {
	if k.Kind() == reflect.String {
		return k.String(), nil
	}
	if tm, ok := k.Interface().(encoding.TextMarshaler); ok {
		if k.Kind() == reflect.Pointer && k.IsNil() {
			return "", nil
		}
		buf, err := tm.MarshalText()
		return string(buf), err
	}
	switch k.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10), nil
	default:
		return strconv.FormatUint(k.Uint(), 10), nil
	}
}

func (w *jsonWalker) structValue(v reflect.Value, t reflect.Type, depth int) (jsonTotals, error) {
	if err := w.nest(depth); err != nil {
		return jsonTotals{}, err
	}
	fields, err := w.fields(t)
	if err != nil {
		return jsonTotals{}, err
	}
	tot, err := w.leaf(2)
	if err != nil {
		return tot, err
	}
FieldLoop:
	for _, f := range fields {
		// Every field costs a visit and its index hops, skipped or not:
		// encoding/json follows f.index and tests omitempty for each (and
		// the walk did too): about 2 ns a hop in all.
		visit := 1 + int64(len(f.index)/4)
		tot.steps += visit
		if err := w.c.steps(visit); err != nil {
			return tot, err
		}
		fv := v
		for _, i := range f.index {
			if fv.Kind() == reflect.Pointer {
				if fv.IsNil() {
					continue FieldLoop
				}
				fv = fv.Elem()
			}
			fv = fv.Field(i)
		}
		if (f.omitEmpty && isEmptyValue(fv)) || (f.omitZero && isZeroValue(fv)) {
			continue
		}
		sub, err := w.leaf(int64(len(f.name)) + 3)
		tot.add(sub)
		if err != nil {
			return tot, err
		}
		sub, err = w.typed(fv, fv.Type(), true, depth+1)
		tot.add(sub)
		if err != nil {
			return tot, err
		}
	}
	return tot, nil
}

// fields returns jsonFields(t), charging building the list the first time
// this walk meets t, cached or not: 12 steps a field (embedded structs'
// included) and a step per started 16 bytes of tags.
func (w *jsonWalker) fields(t reflect.Type) ([]jsonField, error) {
	if !w.typed1[t] {
		if w.typed1 == nil {
			w.typed1 = map[reflect.Type]bool{}
		}
		w.typed1[t] = true
		var n int64
		var count func(t reflect.Type, seen map[reflect.Type]bool)
		count = func(t reflect.Type, seen map[reflect.Type]bool) {
			if seen[t] {
				return
			}
			seen[t] = true
			for i := range t.NumField() {
				f := t.Field(i)
				n += 12 + int64((len(f.Tag)+15)/16)
				if f.Anonymous {
					ft := f.Type
					if ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					if ft.Kind() == reflect.Struct {
						count(ft, seen)
					}
				}
			}
		}
		count(t, map[reflect.Type]bool{})
		if err := w.c.steps(n); err != nil {
			return nil, err
		}
	}
	return jsonFields(t), nil
}

// isEmptyValue is encoding/json's omitempty test.
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Interface, reflect.Pointer:
		return v.IsZero()
	default:
		return false
	}
}

// isZeroValue is encoding/json's omitzero test: the value's IsZero method
// where it has one, else reflect's zero value.
func isZeroValue(v reflect.Value) bool {
	t := v.Type()
	switch {
	case t.Kind() == reflect.Interface && t.Implements(isZeroerType):
		return v.IsNil() || (v.Elem().Kind() == reflect.Pointer && v.Elem().IsNil()) ||
			v.Interface().(interface{ IsZero() bool }).IsZero() //nolint:forcetypeassert // checked by Implements
	case t.Kind() == reflect.Pointer && t.Implements(isZeroerType):
		return v.IsNil() || v.Interface().(interface{ IsZero() bool }).IsZero() //nolint:forcetypeassert // checked by Implements
	case t.Implements(isZeroerType):
		return v.Interface().(interface{ IsZero() bool }).IsZero() //nolint:forcetypeassert // checked by Implements
	case reflect.PointerTo(t).Implements(isZeroerType):
		if !v.CanAddr() {
			v2 := reflect.New(t).Elem()
			v2.Set(v)
			v = v2
		}
		return v.Addr().Interface().(interface{ IsZero() bool }).IsZero() //nolint:forcetypeassert // checked by Implements
	default:
		return v.IsZero()
	}
}

// validNumber reports whether s is a JSON number literal, as encoding/json
// requires of a json.Number.
func validNumber(s string) bool {
	if s == "" || (s[0] != '-' && (s[0] < '0' || s[0] > '9')) {
		return false
	}
	return json.Valid([]byte(s))
}

// jsonField is a struct field encoding/json encodes.
type jsonField struct {
	name                string
	index               []int
	tag                 bool
	omitEmpty, omitZero bool
}

var jsonFieldCache sync.Map // reflect.Type -> []jsonField

// jsonFields is encoding/json's typeFields: the fields it encodes, in
// order.
func jsonFields(t reflect.Type) []jsonField {
	if f, ok := jsonFieldCache.Load(t); ok {
		return f.([]jsonField) //nolint:forcetypeassert // only []jsonField is stored
	}
	type queued struct {
		typ   reflect.Type
		index []int
	}
	current := []queued{}
	next := []queued{{typ: t}}
	var count, nextCount map[reflect.Type]int
	visited := map[reflect.Type]bool{}
	var fields []jsonField
	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, map[reflect.Type]int{}
		for _, f := range current {
			if visited[f.typ] {
				continue
			}
			visited[f.typ] = true
			for i := range f.typ.NumField() {
				sf := f.typ.Field(i)
				if sf.Anonymous {
					st := sf.Type
					if st.Kind() == reflect.Pointer {
						st = st.Elem()
					}
					if !sf.IsExported() && st.Kind() != reflect.Struct {
						continue
					}
				} else if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, opts, _ := strings.Cut(tag, ",")
				if !validTag(name) {
					name = ""
				}
				index := make([]int, len(f.index)+1)
				copy(index, f.index)
				index[len(f.index)] = i
				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					tagged := name != ""
					if name == "" {
						name = sf.Name
					}
					field := jsonField{name: name, tag: tagged, index: index,
						omitEmpty: hasOpt(opts, "omitempty"), omitZero: hasOpt(opts, "omitzero")}
					fields = append(fields, field)
					if count[f.typ] > 1 {
						fields = append(fields, field)
					}
					continue
				}
				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, queued{typ: ft, index: index})
				}
			}
		}
	}
	slices.SortFunc(fields, func(a, b jsonField) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.index), len(b.index)); c != 0 {
			return c
		}
		if a.tag != b.tag {
			if a.tag {
				return -1
			}
			return +1
		}
		return slices.Compare(a.index, b.index)
	})
	out := fields[:0]
	for i := 0; i < len(fields); {
		fi := fields[i]
		advance := 1
		for ; i+advance < len(fields); advance++ {
			if fields[i+advance].name != fi.name {
				break
			}
		}
		switch fs := fields[i : i+advance]; {
		case advance == 1:
			out = append(out, fi)
		case len(fs[0].index) != len(fs[1].index) || fs[0].tag != fs[1].tag:
			out = append(out, fs[0]) // dominantField: the first, unless the first two tie
		}
		i += advance
	}
	slices.SortFunc(out, func(a, b jsonField) int { return slices.Compare(a.index, b.index) })
	got, _ := jsonFieldCache.LoadOrStore(t, out)
	return got.([]jsonField) //nolint:forcetypeassert // only []jsonField is stored
}

func hasOpt(opts, want string) bool {
	for opts != "" {
		var o string
		o, opts, _ = strings.Cut(opts, ",")
		if o == want {
			return true
		}
	}
	return false
}

// validTag is encoding/json's isValidTag.
func validTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

// goBudget counts the Go API's conversion steps against MaxSteps, as the
// ELPS path's encode walk charges json:dump-bytes's: 3 a value and a step
// per started KiB of JSON. It is also the hbs.Meter for decoding.
type goBudget struct {
	used, max, size, kib int64
}

func (b *goBudget) Charge(n int64) error {
	b.used += n
	if b.used > b.max {
		return &hbs.Error{Kind: hbs.KindLimit, Msg: fmt.Sprintf("template evaluation exceeds the maximum of %d steps", b.max)}
	}
	return nil
}

func (b *goBudget) steps(n int64) error { return b.Charge(n) }

func (b *goBudget) charge(values, bytes int64) error {
	b.size += bytes
	n := 3 * values
	if kib := (b.size + 1023) >> 10; kib > b.kib {
		n += kib - b.kib
		b.kib = kib
	}
	return b.Charge(n)
}

// jsonGoMaxDepth bounds the container nesting of a Go context converted
// through JSON (encoding/json itself has no bound for acyclic values).
const jsonGoMaxDepth = 1024
