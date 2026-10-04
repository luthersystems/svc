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
	"math/big"
	"reflect"
	"sync"
)

var (
	intType   = reflect.TypeFor[big.Int]()
	ratType   = reflect.TypeFor[big.Rat]()
	floatType = reflect.TypeFor[big.Float]()
)

// Steps returns the steps printing x takes, and whether x is a math/big
// value whose methods this package knows (*big.Int, *big.Rat, *big.Float),
// or embeds one whose methods it promotes (see ValueSteps).
func Steps(x any) (int64, bool) { return ValueSteps(reflect.ValueOf(x)) }

// ValueSteps is Steps for v. A struct, or a pointer to one, whose methods
// are promoted from an embedded math/big field runs math/big's work when
// they are called: the field is found by Go's selector rules (the
// shallowest embedded *big.X, or addressable big.X, unique at its depth)
// and charged. A type that declares its own method over such a field is
// charged as if it did not (an overcharge). Values reached through
// unexported fields are read too: their promoted methods run all the same.
func ValueSteps(v reflect.Value) (int64, bool) {
	if n, ok := direct(v); ok {
		return n, true
	}
	if !v.IsValid() || !mayEmbed(v.Type()) {
		return 0, false
	}
	return embedded(v)
}

// direct charges v if it is a *big.Int, *big.Rat or *big.Float.
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

// The embedding search's bounds: deeper or wider embeddings are not
// followed (Go code that builds them is the caller's).
const (
	maxEmbedDepth  = 16
	maxEmbedFields = 1 << 10
)

// embeds caches mayEmbed per type.
var embeds sync.Map // reflect.Type -> bool

// mayEmbed reports whether t, or what it points to, embeds a math/big
// type within the search's bounds, looking at types only.
func mayEmbed(t reflect.Type) bool {
	if b, ok := embeds.Load(t); ok {
		return b.(bool) //nolint:forcetypeassert // stored below
	}
	found := false
	level, scanned := []reflect.Type{t}, 0
	for depth := 0; depth < maxEmbedDepth && len(level) > 0 && !found; depth++ {
		var next []reflect.Type
		for _, st := range level {
			if st.Kind() == reflect.Pointer {
				st = st.Elem()
			}
			if st.Kind() != reflect.Struct {
				continue
			}
			for i := 0; i < st.NumField() && scanned < maxEmbedFields; i++ {
				scanned++
				f := st.Field(i)
				if !f.Anonymous {
					continue
				}
				ft := f.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft == intType || ft == ratType || ft == floatType {
					found = true
				}
				next = append(next, f.Type)
			}
		}
		level = next
	}
	embeds.Store(t, found)
	return found
}

// embedded finds the math/big field v's promoted methods come from, by
// Go's selector rules, and charges it.
func embedded(v reflect.Value) (int64, bool) {
	level, scanned := []reflect.Value{v}, 0
	for depth := 0; depth < maxEmbedDepth && len(level) > 0; depth++ {
		var found, next []reflect.Value
		for _, s := range level {
			if s.Kind() == reflect.Pointer {
				if s.IsNil() {
					continue
				}
				s = s.Elem()
			}
			if s.Kind() != reflect.Struct {
				continue
			}
			for i := range s.NumField() {
				if scanned++; scanned > maxEmbedFields {
					return 0, false
				}
				f := s.Type().Field(i)
				if !f.Anonymous {
					continue
				}
				fv := s.Field(i)
				switch f.Type {
				case intType, ratType, floatType:
					// Its methods are the pointer's: promoted only where
					// the struct is addressable.
					if fv.CanAddr() {
						found = append(found, fv.Addr())
					}
					continue
				}
				if _, ok := direct(reflect.Zero(f.Type)); ok {
					found = append(found, fv)
					continue
				}
				next = append(next, fv)
			}
		}
		switch len(found) {
		case 0:
			level = next
		case 1:
			return direct(found[0])
		default:
			return 0, false // ambiguous at this depth: nothing promoted
		}
	}
	return 0, false
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
