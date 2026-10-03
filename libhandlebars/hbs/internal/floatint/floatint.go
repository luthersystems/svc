// Copyright © 2026 Luther Systems, Ltd. All right reserved.

// Package floatint converts float64 to int the same way on every CPU.
package floatint

import "math"

// ToInt is Go's int(f) as compiled for amd64, computed the same way on
// every CPU. It is used in both modes (see DETERMINISM.md).
//
// The Go spec leaves a float-to-int conversion implementation-defined when
// the value does not fit, so svc's int(f), and raymond's int literal conversion, depended on the CPU. amd64 compiles
// it to CVTTSD2SQ, which truncates toward zero and returns the "integer
// indefinite" value 0x8000000000000000 (math.MinInt64) for NaN, +-Inf and
// anything outside [-2^63, 2^63). arm64's FCVTZS saturates instead (+Inf and
// 1e19 give math.MaxInt64) and gives 0 for NaN. The amd64 results are the
// pinned ones:
//
//	NaN, +Inf, -Inf, 1e19, -1e19, 2^63   -> math.MinInt64
//	-2^63                                -> math.MinInt64 (in range, exact)
//	finite f in (-2^63, 2^63)            -> f truncated toward zero
//
// Only in-range values reach the hardware conversion, where the result is
// defined by the spec. A float32 widens to float64 exactly, so the same rule
// matches amd64's CVTTSS2SQ. On a 32-bit CPU int(int64) wraps, which is
// deterministic but not the amd64 result (the out-of-range value is
// math.MinInt32 there); svc does not run there.
func ToInt(f float64) int {
	if f >= -0x1p63 && f < 0x1p63 { // false for NaN
		return int(int64(f))
	}
	return math.MinInt // math.MinInt64 on 64-bit CPUs
}
