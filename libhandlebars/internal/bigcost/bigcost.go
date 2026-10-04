// Copyright © 2026 Luther Systems, Ltd. All right reserved.

// Package bigcost estimates the steps math/big's values take to print in
// decimal (fmt's %v, or their text marshaling). Their methods' work grows
// faster than their text: an Int's conversion is superlinear in its bits,
// and a Float's shortest form is quadratic in its precision and in the
// bits below its binary point, so a Float that prints as "1.5e-78913"
// can take seconds. The engine charges these steps before it calls the
// method, so MaxSteps stops a value that would take too long.
//
// The estimates are an upper bound on the measured work at well under the
// engine's base cost per step (DETERMINISM.md, "Cost model"); they are
// computed from the value alone, so they are deterministic.
package bigcost

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"runtime"
	"sync"
)

var (
	intType   = reflect.TypeFor[big.Int]()
	ratType   = reflect.TypeFor[big.Rat]()
	floatType = reflect.TypeFor[big.Float]()
)

// Steps returns the steps printing x takes, and whether x is a math/big
// value whose methods this package knows (*big.Int, *big.Rat, *big.Float).
func Steps(x any) (int64, bool) { return direct(reflect.ValueOf(x)) }

// MethodSteps returns the steps calling v's method name takes (fmt's
// Format, Error or String; encoding/json's MarshalJSON or MarshalText),
// and whether that call runs math/big's method: v is a *big.Int, *big.Rat
// or *big.Float, or the method is promoted to v's type from one by
// embedding, directly or through an embedded interface holding one. The
// method's supplier is found by Go's selector rules: a method the type
// declares itself is its own; else the shallowest embedded field
// declaring the name, unless another field or method of that name is at
// the same depth (then nothing is promoted). An interface (static type, or
// embedded field) is followed to its dynamic value, a step a hop (the
// returned steps count them, math/big's or not). Where the search passes its
// bounds before it decides, it fails closed with ErrUnresolved; a chain of
// interfaces that comes back to a pointer it passed fails with ErrCycle
// (the call would recurse until the stack overflows).
func MethodSteps(v reflect.Value, name string) (int64, bool, error) {
	var hops int64
	var seen map[ptrKey]bool
	for {
		for v.Kind() == reflect.Interface {
			if v.IsNil() {
				return hops, false, nil
			}
			v = v.Elem()
			hops++
			if seen == nil {
				seen = map[ptrKey]bool{}
			}
		}
		if !v.IsValid() {
			return hops, false, nil
		}
		if n, ok := direct(v); ok {
			return hops + n, true, nil
		}
		if _, ok := v.Type().MethodByName(name); !ok {
			return hops, false, nil // not called
		}
		r := resolve(v.Type(), name)
		switch r.kind {
		case supplierNone:
			return hops, false, nil
		case supplierUnresolved:
			return hops, false, ErrUnresolved
		default:
		}
		f, cycle := follow(v, r.path, seen)
		if cycle {
			return hops, false, ErrCycle
		}
		if !f.IsValid() {
			return hops, false, nil // behind a nil pointer: the call panics
		}
		if r.kind == supplierInterface {
			v = f
			continue
		}
		if f.Kind() != reflect.Pointer {
			if !f.CanAddr() {
				return hops, false, nil
			}
			f = f.Addr()
		}
		n, ok := direct(f)
		return hops + n, ok, nil
	}
}

// ErrUnresolved is the supplier search past its bounds: callers fail
// rather than leave a math/big method it could not rule out uncharged.
var ErrUnresolved = fmt.Errorf("method embedding nests deeper than %d levels or %d embedded fields", maxEmbedDepth, maxEmbedFields)

// ErrCycle is a method promoted through interfaces back to a value it came
// from: calling it would recurse until the stack overflows.
var ErrCycle = errors.New("method embedding cycles through an interface")

// ptrKey is a pointer a method's promotion passed through.
type ptrKey struct {
	t reflect.Type
	p uintptr
}

// direct charges v if it is a *big.Int, *big.Rat or *big.Float. A value
// reached through an unexported field is read too: its promoted methods
// run all the same.
func direct(v reflect.Value) (int64, bool) {
	if !v.IsValid() || v.Kind() != reflect.Pointer {
		return 0, false
	}
	et := v.Type().Elem()
	if et != intType && et != ratType && et != floatType {
		return 0, false
	}
	if v.IsNil() {
		return 1, true
	}
	switch x := reflect.NewAt(et, v.UnsafePointer()).Interface().(type) {
	case *big.Int:
		return IntSteps(x.BitLen()), true
	case *big.Rat:
		return IntSteps(x.Num().BitLen()) + IntSteps(x.Denom().BitLen()), true
	case *big.Float:
		return FloatSteps(x), true
	default:
		return 0, false
	}
}

// The bounds of the supplier search, a type's: deeper or wider embeddings
// fail closed (ErrUnresolved).
const (
	maxEmbedDepth  = 64      // as jsongo's embedding search (maxEmbedRaw)
	maxEmbedFields = 1 << 14 // embedded fields looked at, as jsongo's (maxEmbedScan)
)

type supplierKind uint8

const (
	supplierNone       supplierKind = iota // the type's own method, or none
	supplierBig                            // a math/big field
	supplierInterface                      // an embedded interface field
	supplierUnresolved                     // the search passed its bounds
)

// supplier is where a type's method comes from: the field path to it.
type supplier struct {
	path []int
	kind supplierKind
}

type resolveKey struct {
	t    reflect.Type
	name string
}

var resolved sync.Map // resolveKey -> supplier

// resolve finds the field t's method name is promoted from (t a struct or
// a pointer to one), by Go's selector rules, once per type and name.
func resolve(t reflect.Type, name string) supplier {
	key := resolveKey{t, name}
	if r, ok := resolved.Load(key); ok {
		return r.(supplier) //nolint:forcetypeassert // stored below
	}
	r := search(t, name)
	resolved.Store(key, r)
	return r
}

type embedNode struct {
	t    reflect.Type // a struct type
	path []int
}

func search(t reflect.Type, name string) supplier {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || declares(t, name) {
		return supplier{}
	}
	level, scanned := []embedNode{{t: t}}, 0
	for range maxEmbedDepth {
		var hits []supplier
		var next []embedNode
		shadowed := false
		for _, n := range level {
			for i := range n.t.NumField() {
				f := n.t.Field(i)
				if f.Name == name {
					shadowed = true // a field of that name at this depth
				}
				if !f.Anonymous {
					continue
				}
				if scanned++; scanned > maxEmbedFields {
					return supplier{kind: supplierUnresolved}
				}
				path := append(append([]int(nil), n.path...), i)
				ft := f.Type
				if ft.Kind() == reflect.Interface {
					if _, ok := ft.MethodByName(name); ok {
						hits = append(hits, supplier{path, supplierInterface})
					}
					continue
				}
				base := ft
				if base.Kind() == reflect.Pointer {
					base = base.Elem()
				}
				switch {
				case declares(base, name):
					kind := supplierNone // a type's own method
					if base == intType || base == ratType || base == floatType {
						kind = supplierBig
					}
					hits = append(hits, supplier{path, kind})
				case base.Kind() == reflect.Struct:
					next = append(next, embedNode{base, path})
				}
			}
		}
		switch {
		case shadowed || len(hits) > 1:
			return supplier{} // shadowed or ambiguous: not promoted
		case len(hits) == 1:
			return hits[0]
		}
		level = next
	}
	if len(level) > 0 {
		return supplier{kind: supplierUnresolved} // deeper embeddings not searched
	}
	return supplier{}
}

// declares reports whether t declares method name itself (on t or *t),
// rather than having it promoted from a field: a promoted method, like a
// value method seen through a pointer, is a compiler-generated wrapper.
func declares(t reflect.Type, name string) bool {
	for _, x := range []reflect.Type{t, reflect.PointerTo(t)} {
		if m, ok := x.MethodByName(name); ok && !autogenerated(m.Func) {
			return true
		}
	}
	return false
}

// autogenerated reports whether fn is a compiler-generated wrapper.
func autogenerated(fn reflect.Value) bool {
	f := runtime.FuncForPC(fn.Pointer())
	if f == nil {
		return false
	}
	file, _ := f.FileLine(f.Entry())
	return file == "<autogenerated>"
}

// follow walks v down path (field indices, through pointers), or returns
// the zero Value at a nil pointer. With seen (after an interface hop), it
// records the pointers it passes, and reports a cycle at one passed before.
func follow(v reflect.Value, path []int, seen map[ptrKey]bool) (reflect.Value, bool) {
	visit := func(p reflect.Value) bool {
		if seen == nil {
			return false
		}
		k := ptrKey{p.Type(), p.Pointer()}
		if seen[k] {
			return true
		}
		seen[k] = true
		return false
	}
	if v.Kind() == reflect.Pointer && !v.IsNil() && visit(v) {
		return reflect.Value{}, true
	}
	for _, i := range path {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}, false
			}
			v = v.Elem()
		}
		v = v.Field(i)
		if v.Kind() == reflect.Pointer && !v.IsNil() && visit(v) {
			return reflect.Value{}, true
		}
	}
	return v, false
}

// words is n bits in 64-bit words, at least one.
func words(n int64) int64 { return n/64 + 1 }

// IntSteps is the steps converting an integer of the given bits to
// decimal takes: math/big divides recursively, about w^1.6 word
// operations for w words (measured: 2^20 bits in 34 ms, 2^24 in about
// 2.4 s), charged as w + w·√w/4.
func IntSteps(bits int) int64 {
	w := words(int64(bits))
	return w + w*isqrt(w)/4
}

// FloatSteps is the steps a Float's shortest decimal form takes. Its
// integer part converts as an Int (four times over: the shortest search
// converts bounds too); the bits below the binary point, counted at the
// Float's precision, are shifted out a word at a time across all its
// digits: frac·(frac+prec) word operations (measured: 1.5 at precision
// 2^16 in 173 ms, 1.5·2^-(2^18) in 2.3 s).
func FloatSteps(v *big.Float) int64 {
	if v.IsInf() || v.Sign() == 0 {
		return 1
	}
	exp := int64(v.MantExp(nil)) // v = mant·2^exp, 0.5 ≤ |mant| < 1
	prec := int64(v.Prec())      //nolint:gosec // at most big.MaxPrec (2^32 - 1)
	n := 4 * IntSteps(int(min(max(exp, 0), 1<<40)))
	if frac := prec - exp; frac > 0 {
		n += 2 * words(frac) * words(frac+prec)
	}
	return n
}

// isqrt is ⌊√n⌋ for n ≥ 0.
func isqrt(n int64) int64 {
	if n < 2 {
		return n
	}
	x := n
	for y := (x + 1) / 2; y < x; y = (x + n/x) / 2 {
		x = y
	}
	return x
}
