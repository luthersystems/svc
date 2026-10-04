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

import "math/big"

// Steps returns the steps printing x takes, and whether x is a math/big
// value whose methods this package knows (*big.Int, *big.Rat, *big.Float).
func Steps(x any) (int64, bool) {
	switch v := x.(type) {
	case *big.Int:
		if v == nil {
			return 1, true
		}
		return IntSteps(v.BitLen()), true
	case *big.Rat:
		if v == nil {
			return 1, true
		}
		return IntSteps(v.Num().BitLen()) + IntSteps(v.Denom().BitLen()), true
	case *big.Float:
		if v == nil {
			return 1, true
		}
		return FloatSteps(v), true
	default:
		return 0, false
	}
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
