package hbs

import "strings"

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

// flush charges the pending steps.
func (r *renderer) flush() {
	if r.pending == 0 {
		return
	}
	n := r.pending
	r.pending = 0
	if r.meter == nil {
		return
	}
	if err := r.meter.Charge(n); err != nil {
		panic(meterError{err})
	}
}

// reserve fails the render if n more bytes would pass MaxOutputBytes.
func (r *renderer) reserve(n int) {
	if n > r.maxOut-len(r.out) {
		panic(errorf(KindLimit, "rendered output exceeds the maximum of %d bytes", r.maxOut))
	}
}

// wrote accounts for n bytes appended to the output: one step for every
// started KiB of everything written so far.
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
	r.out = append(r.out, s...)
	r.wrote(len(s))
}

// writeEscaped appends s with raymond's HTML escaping:
// & ' < > " become &amp; &apos; &lt; &gt; &quot;. Nothing else is escaped.
func (r *renderer) writeEscaped(s string) {
	if s == "" {
		return
	}
	i := strings.IndexAny(s, escapedChars)
	if i < 0 {
		r.writeString(s)
		return
	}
	n := len(s)
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '&', '\'':
			n += 5 // &amp; &apos;
		case '<', '>':
			n += 3 // &lt; &gt;
		case '"':
			n += 5 // &quot;
		default:
		}
	}
	r.reserve(n)
	before := len(r.out)
	r.out = appendEscaped(r.out, s, i)
	r.wrote(len(r.out) - before)
}

const escapedChars = `&'<>"`

// appendEscaped appends s escaped; i is the index of the first byte to
// escape, or -1.
func appendEscaped(dst []byte, s string, i int) []byte {
	for i >= 0 {
		dst = append(dst, s[:i]...)
		switch s[i] {
		case '&':
			dst = append(dst, "&amp;"...)
		case '\'':
			dst = append(dst, "&apos;"...)
		case '<':
			dst = append(dst, "&lt;"...)
		case '>':
			dst = append(dst, "&gt;"...)
		default: // '"'
			dst = append(dst, "&quot;"...)
		}
		s = s[i+1:]
		i = strings.IndexAny(s, escapedChars)
	}
	return append(dst, s...)
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
		for _, e := range x {
			r.writeValue(e, esc)
		}
	default:
		// Numbers, booleans and UNPRINTABLE contain no escapable byte.
		var buf [32]byte
		r.writeBytes(appendStr(buf[:0], v))
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
