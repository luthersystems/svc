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
	"unicode/utf8"

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
	quoted   bool                  // the value is a ",string" field's: a scalar is written as a JSON string
	levels   int                   // the walk's recursion depth
}

// hopCost is the steps a pointer or interface hop costs: the walk keeps
// path and memo entries for it and deepens the stack (about 0.5-2 us a
// hop on a long chain).
const hopCost = 16

// hop adds a pointer or interface hop to the totals of the walk below it.
// The hop is charged before it is followed (by its caller), so the charge
// stands however that walk ends.
func hop(tot jsonTotals, err error) (jsonTotals, error) {
	tot.steps += hopCost
	return tot, err
}

// q is 2 when the scalar being sized is written quoted (",string").
func (w *jsonWalker) q() int64 {
	if w.quoted {
		return 2
	}
	return 0
}

// skippedMarshaler is a MarshalJSON or MarshalText the walk did not call.
type skippedMarshaler struct {
	v    reflect.Value
	text bool
}

// marshalerLeaf records a Marshaler or TextMarshaler value encoding/json
// would call (a nil pointer it writes as null, without calling).
func (w *jsonWalker) marshalerLeaf(v reflect.Value, text bool) (jsonTotals, error) {
	// encoding/json writes a nil pointer or a nil interface as null,
	// without calling.
	if k := v.Kind(); (k != reflect.Pointer && k != reflect.Interface) || !v.IsNil() {
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
	t      reflect.Type
	p      any
	depth  int  // with a depth bound, a subtree's outcome depends on where it starts
	quoted bool // a ",string" field's pointer writes its string doubly escaped
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

// maxJSONLevels bounds the walk's recursion, pointer and interface hops
// included: a value nested deeper fails with a limit error. The walk uses
// about 1.3 KB of stack a level (several times encoding/json's), so this
// keeps it near 64 MB, far below the 1 GB at which Go aborts the process,
// and fails well before encoding/json itself would run out (about 300,000
// levels of nested slices). Levels past deepLevel are charged
// deepLevelCost each for the stack growth they cause.
const (
	maxJSONLevels = 50_000
	deepLevel     = 64
	deepLevelCost = 8
)

// typed is newTypeEncoder(t, allowAddr) applied to v.
func (w *jsonWalker) typed(v reflect.Value, t reflect.Type, allowAddr bool, depth int) (jsonTotals, error) {
	if w.levels >= maxJSONLevels {
		return jsonTotals{}, &jsonFailure{fmt.Sprintf("json: Go value nests deeper than %d", maxJSONLevels)}
	}
	// No defer: a deferred call per level makes each stack growth of a
	// deep walk adjust them all.
	if w.levels >= deepLevel {
		if err := w.c.steps(deepLevelCost); err != nil {
			return jsonTotals{}, err
		}
	}
	w.levels++
	tot, err := w.encode(v, t, allowAddr, depth)
	w.levels--
	return tot, err
}

func (w *jsonWalker) encode(v reflect.Value, t reflect.Type, allowAddr bool, depth int) (jsonTotals, error) {
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
	switch k := t.Kind(); k {
	case reflect.Struct, reflect.Map, reflect.Slice, reflect.Array:
		w.quoted = false // ",string" applies to scalars only
	default:
	}
	switch t.Kind() {
	case reflect.Bool:
		if v.Bool() {
			return w.leaf(4 + w.q())
		}
		return w.leaf(5 + w.q())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var buf [24]byte
		return w.leaf(int64(len(strconv.AppendInt(buf[:0], v.Int(), 10))) + w.q())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var buf [24]byte
		return w.leaf(int64(len(strconv.AppendUint(buf[:0], v.Uint(), 10))) + w.q())
	case reflect.Float32, reflect.Float64:
		if f := v.Float(); math.IsInf(f, 0) || math.IsNaN(f) {
			return jsonTotals{}, &jsonFailure{"json: unsupported value: " + strconv.FormatFloat(f, 'g', -1, t.Bits())}
		}
		return w.leaf(1 + w.q())
	case reflect.String:
		s := v.String()
		if t == jsonNumberType {
			num := s
			if num == "" {
				num = "0"
			}
			// Validating reads it once; the error quotes it.
			if err := w.c.steps(int64(len(num)/16 + 1)); err != nil {
				return jsonTotals{}, err
			}
			if !validNumber(num) {
				// strconv.Quote builds it about 10 ns a byte, and the error
				// is copied on its way out.
				if err := w.c.steps(int64(quotedLen(num)/4 + 1)); err != nil {
					return jsonTotals{}, err
				}
				const prefix = "json: invalid number literal "
				msg := make([]byte, 0, len(prefix)+quotedLen(num)) // built once, at its size
				msg = strconv.AppendQuote(append(msg, prefix...), num)
				return jsonTotals{}, &jsonFailure{string(msg)}
			}
			return w.leaf(int64(len(num)) + w.q()) // written unquoted, unless ",string"
		}
		scan := int64(0)
		if len(s) > 0 {
			scan = int64((len(s)-1)/64 + 1)
			if err := w.c.steps(scan); err != nil {
				return jsonTotals{}, err
			}
		}
		n := jsonStringLen(s)
		if w.quoted {
			n = jsonQuotedStringLen(s)
		}
		tot, err := w.leaf(n)
		tot.steps += scan
		return tot, err
	case reflect.Interface:
		if v.IsNil() {
			return w.leaf(4)
		}
		if err := w.c.steps(hopCost); err != nil {
			return jsonTotals{steps: hopCost}, err
		}
		return hop(w.value(v.Elem(), depth))
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
			if err := w.c.steps(hopCost); err != nil {
				return jsonTotals{steps: hopCost}, err
			}
			return hop(w.typed(v.Elem(), t.Elem(), true, depth))
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
	mk := jsonMemoKey{t: v.Type(), p: key, quoted: w.quoted}
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
		// MapRange copies each key and value out of the map, here and in
		// json.Marshal after: allocated, zeroed and copied twice.
		perEntry := units64(sizeOf(t.Key())+sizeOf(t.Elem()), 32)
		if err := w.c.steps(int64(n) * perEntry); err != nil {
			return tot, err
		}
		tot.steps += int64(n) * perEntry
		kvs := make([]kv, 0, n)
		var keyErr string
		var keyBytes, scan int64
		it := v.MapRange()
		for it.Next() {
			ks, err := mapKeyString(it.Key())
			if err != nil {
				// encoding/json reports the first failing key in Go's map
				// order; resolve them all and report the least error text,
				// so the error does not vary by run.
				if e := err.Error(); keyErr == "" || e < keyErr {
					keyErr = e
				}
				continue
			}
			// Its escaping is sized next, and it is compared in the sort:
			// reading it is charged with the others, below.
			scan += int64(len(ks)/16 + 1)
			keyBytes += int64(len(ks))
			kvs = append(kvs, kv{ks, it.Value()})
		}
		// Sorting compares keys: their bytes, log2(n) times. The keys'
		// charges are summed and applied once, so a budget failure does
		// not depend on Go's map order.
		scan += (keyBytes/256 + 1) * int64(1+bits.Len(uint(n)))
		if err := w.c.steps(scan); err != nil {
			return tot, err
		}
		tot.steps += scan
		if keyErr != "" {
			return tot, w.fail("json: encoding error for type ", strconv.Quote(t.String()), ": ", strconv.Quote(keyErr))
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
		visit := 1 + int64((len(f.index)+3)/4)
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
		if f.omitZero {
			// IsZero reads the whole value, here and again in Marshal (a
			// method's cost is the caller's).
			z := zeroTestCost(fv.Type())
			if err := w.c.steps(z); err != nil {
				return tot, err
			}
			tot.steps += z
		}
		if (f.omitEmpty && isEmptyValue(fv)) || (f.omitZero && isZeroValue(fv)) {
			continue
		}
		sub, err := w.leaf(int64(len(f.name)) + 3)
		tot.add(sub)
		if err != nil {
			return tot, err
		}
		w.quoted = f.quoted
		sub, err = w.typed(fv, fv.Type(), true, depth+1)
		w.quoted = false
		tot.add(sub)
		if err != nil {
			return tot, err
		}
	}
	return tot, nil
}

// fields returns jsonFields(t), charging building the list the first time
// this walk meets t, cached or not: 12 steps a field (embedded structs'
// included) and a step per started 16 bytes of names and of tags.
func (w *jsonWalker) fields(t reflect.Type) ([]jsonField, error) {
	if !w.typed1[t] {
		if w.typed1 == nil {
			w.typed1 = map[reflect.Type]bool{}
		}
		w.typed1[t] = true
		var n int64
		var count func(t reflect.Type, seen map[reflect.Type]bool, depth int)
		count = func(t reflect.Type, seen map[reflect.Type]bool, depth int) {
			if seen[t] {
				return
			}
			seen[t] = true
			for i := range t.NumField() {
				f := t.Field(i)
				// A field's index path, copied at its embedding depth, and
				// its name and tag, read and escaped.
				n += 12 + int64(depth) + int64((len(f.Name)+15)/16) + int64((len(f.Tag)+15)/16)
				if f.Anonymous {
					ft := f.Type
					if ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					if ft.Kind() == reflect.Struct {
						count(ft, seen, depth+1)
					}
				}
			}
		}
		count(t, map[reflect.Type]bool{}, 0)
		if err := w.c.steps(n); err != nil {
			return nil, err
		}
	}
	return jsonFields(t), nil
}

// zeroTestCost is the steps reflect.Value.IsZero takes on a value of t,
// twice (the walk tests it, and encoding/json again): plain memory is
// compared at once, a step per started 256 bytes; anything else (floats,
// strings, padded structs, arrays of them) is tested element by element,
// a step per 8 elements.
func zeroTestCost(t reflect.Type) int64 {
	if plainMemory(t) {
		return 2 * units64(sizeOf(t), 256)
	}
	return units64(2*zeroElems(t), 8)
}

// plainMemory reports whether a value of t is zero exactly when its bytes
// are, so IsZero compares memory: integers, booleans, pointers, and arrays
// and unpadded structs of them.
func plainMemory(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Pointer, reflect.UnsafePointer, reflect.Chan:
		return true
	case reflect.Array:
		return plainMemory(t.Elem())
	case reflect.Struct:
		var sum uintptr
		for i := range t.NumField() {
			f := t.Field(i)
			if !plainMemory(f.Type) {
				return false
			}
			sum += f.Type.Size()
		}
		return sum == t.Size()
	default:
		return false
	}
}

// zeroElems is the values IsZero visits in a value of t, saturating.
func zeroElems(t reflect.Type) int64 {
	switch t.Kind() {
	case reflect.Array:
		return min(int64(t.Len())*zeroElems(t.Elem()), 1<<50)
	case reflect.Struct:
		n := int64(1)
		for i := range t.NumField() {
			n = min(n+zeroElems(t.Field(i).Type), 1<<50)
		}
		return n
	default:
		return 1
	}
}

// sizeOf is t.Size() as an int64, capped (a Go type's size fits).
func sizeOf(t reflect.Type) int64 { return int64(min(t.Size(), 1<<40)) }

// units64 is n in units of per, rounded up.
func units64(n, per int64) int64 {
	if n <= 0 {
		return 0
	}
	return (n-1)/per + 1
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
// It reads s once and allocates nothing.
func validNumber(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	switch {
	case i < len(s) && s[i] == '0':
		i++
	case i < len(s) && s[i] >= '1' && s[i] <= '9':
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	default:
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	return i == len(s)
}

// quotedLen is len(strconv.Quote(s)), computed without building it.
func quotedLen(s string) int {
	n := 2
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			n += 4 // \xHH
		case r == '"' || r == '\\' || r == '\a' || r == '\b' || r == '\f' || r == '\n' || r == '\r' || r == '\t' || r == '\v':
			n += 2
		case strconv.IsPrint(r):
			n += size
		case r < ' ' || r == 0x7f:
			n += 4 // \xHH
		case r < 0x10000:
			n += 6 // \uHHHH
		default:
			n += 10 // \UHHHHHHHH
		}
		i += size
	}
	return n
}

// jsonField is a struct field encoding/json encodes.
type jsonField struct {
	name                        string
	index                       []int
	tag                         bool
	omitEmpty, omitZero, quoted bool
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
					quoted := false
					if hasOpt(opts, "string") {
						switch ft.Kind() {
						case reflect.Bool,
							reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
							reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
							reflect.Float32, reflect.Float64,
							reflect.String:
							quoted = true
						default:
						}
					}
					field := jsonField{name: name, tag: tagged, index: index, quoted: quoted,
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
