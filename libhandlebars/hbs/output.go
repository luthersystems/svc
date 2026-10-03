// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"fmt"
	"math/bits"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Output and step accounting for one render.
//
// The whole render writes into one byte slice, r.out. Block helpers append
// their sections to it as they evaluate them; a helper whose result is a
// value (a subexpression or a {{mustache}} call) renders into the tail of the
// same slice, takes that tail as a string and truncates the slice back. No
// string is ever built by repeated concatenation, so render time is linear in
// the bytes written.

// meterBatch is how many pending steps the renderer collects before it
// charges the Meter. The batch points depend only on the evaluation order, so
// they are deterministic.
const meterBatch = 64

// meterError carries a Meter error through the evaluator's panic unwinding,
// so Render returns it unchanged.
type meterError struct{ err error }

// step records one unit of evaluation work.
func (r *renderer) step() {
	r.pending++
	if r.pending >= meterBatch {
		r.flush()
	}
}

// flush charges the pending steps, then applies MaxSteps. A Meter error wins
// over the step limit when one batch passes both.
func (r *renderer) flush() {
	if r.pending == 0 {
		return
	}
	n := r.pending
	r.pending = 0
	r.steps += n
	if r.meter != nil {
		if err := r.meter.Charge(n); err != nil {
			panic(meterError{err})
		}
	}
	if r.steps > r.maxSteps {
		panic(errorf(KindLimit, "template evaluation exceeds the maximum of %d steps", r.maxSteps))
	}
}

// reserve fails the render if n more bytes would pass MaxOutputBytes, or
// would take the bytes the render has produced past maxProduced.
func (r *renderer) reserve(n int) {
	if n > r.maxOut-len(r.out) {
		panic(errorf(KindLimit, "rendered output exceeds the maximum of %d bytes", r.maxOut))
	}
	r.reserveProduced(n)
}

// reserveProduced fails the render if n more produced bytes would pass
// maxProduced. Produced bytes are everything the render has written to the
// output, including sections a helper captured as a string and later
// dropped, plus every string a helper built. They bound the heap a render
// can hold, since captured strings and helper results can stay alive in
// hash arguments after the output is truncated.
func (r *renderer) reserveProduced(n int) {
	if int64(n) > r.maxProduced-r.written {
		panic(errorf(KindLimit, "template evaluation produces more than %d bytes", r.maxProduced))
	}
}

// produced accounts for a string of n bytes built by a helper, as wrote does
// for output.
func (r *renderer) produced(n int) {
	if n <= 0 {
		return
	}
	r.reserveProduced(n)
	r.wrote(n)
}

// Bytes per step for work done byte by byte. The units are sized from
// measurement so that no operation takes much more than the evaluator's
// base cost per step (DETERMINISM.md, "Cost model").
const (
	hashUnit = 256 // hashing, comparing or copying: well under 1 ns a byte
	scanUnit = 16  // parsing or scanning a byte at a time: about 2-3 ns a byte
	fmtUnit  = 8   // formatting a float: about 2-4 ns a byte of output
)

// formatted charges formatting n bytes of a number.
func (r *renderer) formatted(n int) { r.steps1(max(1, units(n, fmtUnit))) }

// units is n bytes in steps of per bytes, rounded up.
func units(n, per int) int64 {
	if n <= 0 {
		return 0
	}
	return int64((n-1)/per + 1)
}

// read charges a string a helper reads or compares: a step per started
// hashUnit bytes.
func (r *renderer) read(n int) { r.steps1(units(n, hashUnit)) }

// scanBytes charges parsing or scanning n bytes one at a time: a step per
// started scanUnit bytes, at least one.
func (r *renderer) scanBytes(n int) { r.steps1(max(1, units(n, scanUnit))) }

// steps1 records n units of evaluation work.
func (r *renderer) steps1(n int64) {
	r.pending += n
	if r.pending >= meterBatch {
		r.flush()
	}
}

// Cost model primitives. Every operation whose work grows with the length
// of a string or key goes through one of these (see DETERMINISM.md, "Cost
// model").

// hashKey charges hashing an n-byte key once: a step per started hashUnit
// bytes, at least one.
func (r *renderer) hashKey(n int) { r.steps1(max(1, units(n, hashUnit))) }

// parseFloat is strconv.ParseFloat, charged by floatCost.
func (r *renderer) parseFloat(s string, bitSize int) (float64, error) {
	r.steps1(floatCostBits(s, bitSize))
	return strconv.ParseFloat(s, bitSize)
}

// lookup is m[k], charged as hashKey(len(k)).
func (r *renderer) lookup(m map[string]any, k string) (any, bool) {
	r.hashKey(len(k))
	v, ok := m[k]
	return v, ok
}

// sortKeys sorts keys, charged up front by a deterministic bound on the
// comparisons: each key's hashKey cost times ceil(log2(n+1)). The charge
// depends only on the keys, not on the order they arrive in.
func (r *renderer) sortKeys(keys []string) {
	rounds := int64(bits.Len(uint(len(keys))))
	var cost int64
	for _, k := range keys {
		cost += max(1, units(len(k), hashUnit))
	}
	r.steps1(cost * max(1, rounds))
	sort.Strings(keys)
}

// appendV appends fmt's %v form of v (a Value or a template literal), as
// fmt.Sprintf("%v", v) writes it, without fmt's recursion: nested arrays
// and objects count against MaxDepth, each element costs a step, and the
// produced-bytes bound is checked as it grows.
func (r *renderer) appendV(dst []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(dst, "<nil>"...)
	case string:
		r.checkProduced(len(dst) + len(x))
		r.read(len(x))
		return append(dst, x...)
	case bool:
		return strconv.AppendBool(dst, x)
	case int:
		return strconv.AppendInt(dst, int64(x), 10)
	case float64:
		n := len(dst)
		dst = strconv.AppendFloat(dst, x, 'g', -1, 64)
		r.formatted(len(dst) - n)
		return dst
	case []any:
		r.enter()
		dst = append(dst, '[')
		for i, e := range x {
			r.step()
			if i > 0 {
				dst = append(dst, ' ')
			}
			dst = r.appendV(dst, e)
			r.checkProduced(len(dst))
		}
		r.leave()
		return append(dst, ']')
	case map[string]any:
		r.enter()
		r.steps1(int64(len(x))) // collecting the keys, before allocating
		r.flush()
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		r.sortKeys(keys)
		dst = append(dst, "map["...)
		for i, k := range keys {
			r.step()
			if i > 0 {
				dst = append(dst, ' ')
			}
			r.checkProduced(len(dst) + len(k))
			r.read(len(k))
			dst = append(dst, k...)
			dst = append(dst, ':')
			dst = r.appendV(dst, x[k])
			r.checkProduced(len(dst))
		}
		r.leave()
		return append(dst, ']')
	default:
		switch reflect.ValueOf(v).Kind() {
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
			reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
			// fmt has no recursion here.
			return fmt.Appendf(dst, "%v", v)
		default:
			return r.goAppendV(dst, v) // a Go composite
		}
	}
}

// appendStrR is appendStr for any value a render meets: a Go value is
// printed by reflection, as raymond's strValue did, charged and bounded.
func (r *renderer) appendStrR(dst []byte, v any) []byte {
	if isGo(v) {
		return r.goAppendStr(dst, reflect.ValueOf(v))
	}
	return appendStr(dst, v)
}

// checkProduced fails the render if a string being built has n bytes,
// more than the produced-bytes bound leaves room for.
func (r *renderer) checkProduced(n int) {
	if int64(n) > r.maxProduced-r.written {
		r.reserveProduced(n)
	}
}

// compare reports a == b, charging a step per started KiB compared when the
// lengths are equal (unequal lengths compare in constant time).
func (r *renderer) compare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	r.read(len(a))
	return a == b
}

// str is str(v) with its cost: reading a string, or building one. An
// array is built in two passes. The first is charged (a step per element,
// a read per string leaf, nested arrays against MaxDepth) and measures the
// string leaves; once its steps are applied and the result's size is
// checked against the produced-bytes bound, the second copies each leaf
// once into a builder of that size.
func (r *renderer) str(v any) string {
	if s, ok := v.(string); ok {
		r.read(len(s))
		return s
	}
	a, ok := v.([]any)
	if !ok {
		r.scratch = r.appendStrR(r.scratch[:0], v)
		r.formatted(len(r.scratch))
		r.produced(len(r.scratch))
		return string(r.scratch)
	}
	leaves := r.measureLeaves(a, 0)
	r.flush()
	r.reserveProduced(leaves)
	var b strings.Builder
	b.Grow(leaves)
	r.copyLeaves(&b, a)
	r.produced(b.Len())
	return b.String()
}

// measureLeaves charges a walk of a and returns the total length of its
// string leaves plus n, failing the render as soon as that passes the
// produced-bytes bound.
func (r *renderer) measureLeaves(a []any, n int) int {
	r.enter()
	for _, e := range a {
		r.step()
		switch x := e.(type) {
		case string:
			r.read(len(x))
			n += len(x)
			r.checkProduced(n)
		case []any:
			n = r.measureLeaves(x, n)
		}
	}
	r.leave()
	return n
}

// copyLeaves appends str(a) to b. measureLeaves has charged the walk and
// bounded its depth and string leaves; numbers and booleans are checked as
// they are added.
func (r *renderer) copyLeaves(b *strings.Builder, a []any) {
	for _, e := range a {
		switch x := e.(type) {
		case string:
			b.WriteString(x)
		case []any:
			r.copyLeaves(b, x)
		default:
			if isGo(x) {
				r.scratch = r.goAppendElem(r.scratch[:0], x)
			} else {
				r.scratch = appendStr(r.scratch[:0], x)
			}
			r.formatted(len(r.scratch))
			r.checkProduced(b.Len() + len(r.scratch))
			b.Write(r.scratch)
		}
	}
}

// wrote accounts for n bytes produced: one step for every started KiB of
// everything produced so far.
func (r *renderer) wrote(n int) {
	r.written += int64(n)
	kib := (r.written + 1023) >> 10
	if kib > r.chargedKiB {
		r.pending += kib - r.chargedKiB
		r.chargedKiB = kib
		if r.pending >= meterBatch {
			r.flush()
		}
	}
}

// writeString appends s unescaped.
func (r *renderer) writeString(s string) {
	if s == "" {
		return
	}
	r.reserve(len(s))
	// Copying: a step per whole scanUnit bytes. A large output outgrows the
	// CPU caches and costs about 4 ns a byte to write; a short write is
	// covered by the step of the node that makes it.
	r.steps1(int64(len(s) / scanUnit))
	r.out = append(r.out, s...)
	r.wrote(len(s))
}

// writeEscaped appends s with raymond's HTML escaping:
// & ' < > " become &amp; &apos; &lt; &gt; &quot;. Nothing else is escaped.
func (r *renderer) writeEscaped(s string) {
	if s == "" {
		return
	}
	// Escaping only lengthens s: reject a string that cannot fit before
	// scanning it.
	r.reserve(len(s))
	// The scan for the five bytes, and the count below, read s byte by
	// byte: charge that before scanning.
	r.scanBytes(len(s))
	i := strings.IndexAny(s, escapedChars)
	if i < 0 {
		r.writeString(s)
		return
	}
	n := len(s)
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '&':
			n += 4 // &amp;
		case '\'':
			n += 5 // &apos;
		case '<', '>':
			n += 3 // &lt; &gt;
		case '"':
			n += 5 // &quot;
		default:
		}
	}
	r.reserve(n)
	// Writing the escaped text is byte-by-byte work over its length.
	r.scanBytes(n)
	before := len(r.out)
	r.out = appendEscaped(r.out, s, i)
	r.wrote(len(r.out) - before)
}

const escapedChars = `&'<>"`

// appendEscaped appends s escaped; i is the index of the first byte to
// escape, or -1.
// It makes one pass over s from i.
func appendEscaped(dst []byte, s string, i int) []byte {
	dst = append(dst, s[:i]...)
	start := i
	for j := i; j < len(s); j++ {
		var rep string
		switch s[j] {
		case '&':
			rep = "&amp;"
		case '\'':
			rep = "&apos;"
		case '<':
			rep = "&lt;"
		case '>':
			rep = "&gt;"
		case '"':
			rep = "&quot;"
		default:
			continue
		}
		dst = append(dst, s[start:j]...)
		dst = append(dst, rep...)
		start = j + 1
	}
	return append(dst, s[start:]...)
}

// writeValue appends raymond's string form of v, escaped when esc is set.
func (r *renderer) writeValue(v any, esc bool) {
	switch x := v.(type) {
	case nil:
		return
	case string:
		if esc {
			r.writeEscaped(x)
		} else {
			r.writeString(x)
		}
	case []any:
		// Each element costs a step; nested arrays count against MaxDepth.
		r.enter()
		for _, e := range x {
			r.step()
			if isGo(e) {
				// An element: printed by raymond's element rule.
				r.writeGo(string(r.goAppendElem(r.scratch[:0], e)), esc)
				continue
			}
			r.writeValue(e, esc)
		}
		r.leave()
	default:
		if isGo(v) {
			// A Go slice of strings prints them; escape as a string.
			r.writeGo(string(r.goAppendStr(r.scratch[:0], reflect.ValueOf(v))), esc)
			return
		}
		// Numbers, booleans and UNPRINTABLE contain no escapable byte.
		r.scratch = appendStr(r.scratch[:0], v)
		r.formatted(len(r.scratch)) // a float can print hundreds of digits
		r.writeBytes(r.scratch)
	}
}

// writeGo writes the printed form of a Go value, escaped when esc is set.
func (r *renderer) writeGo(s string, esc bool) {
	if esc {
		r.writeEscaped(s)
	} else {
		r.writeString(s)
	}
}

func (r *renderer) writeBytes(b []byte) {
	if len(b) == 0 {
		return
	}
	r.reserve(len(b))
	r.out = append(r.out, b...)
	r.wrote(len(b))
}

// capture returns the output written since start and removes it.
func (r *renderer) capture(start int) string {
	s := string(r.out[start:])
	r.out = r.out[:start]
	return s
}
