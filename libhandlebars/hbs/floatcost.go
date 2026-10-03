// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

// floatCost is the steps parsing s as a float costs: a step per started
// scanUnit bytes, plus slowFloatPremium and a step per byte when s is in the
// class strconv.ParseFloat parses slowly (see slowFloat). The slow path
// takes about 20 us for a subnormal like 5e-324 however short, and grows
// with the digits.
func floatCost(s string) int64 { return floatCostBits(s, 64) }

// floatCostBits is floatCost for strconv.ParseFloat(s, bitSize): float32's
// fast paths give up much closer to zero and infinity.
func floatCostBits(s string, bitSize int) int64 {
	c := max(1, units(len(s), scanUnit))
	if slowFloatBits(s, bitSize) {
		c += slowFloatPremium + int64(len(s))
	}
	return c
}

const slowFloatPremium = 512

// slowFloat reports whether s, a decimal number as JSON or a template
// writes it, is outside strconv.ParseFloat's fast paths: more than 19
// significant digits, or a decimal exponent near or past float64's range
// (subnormals and overflow). Anything that is not a plain decimal (hex,
// Inf, NaN, malformed) is fast or fails fast.
func slowFloat(s string) bool { return slowFloatBits(s, 64) }

// slowFloatBits is slowFloat for a bitSize of 32 or 64. float32's fast
// paths (exact and Eisel-Lemire) give up on float32 subnormals, underflow
// and overflow, and the fallback then works through a long decimal (about
// 20 us for "1e-300"): a first significant digit at 10^-37 or below, or at
// 10^38 or above, is slow.
func slowFloatBits(s string, bitSize int) bool {
	// ParseFloat converts the longest numeric prefix before it rejects a
	// malformed suffix, so the prefix is what is classified.
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	sig, intDigits, leadFrac := 0, 0, 0
	seenDot, seenDigit := false, false
mantissa:
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			seenDigit = true
			if c == '0' && sig == 0 {
				if seenDot {
					leadFrac++
				}
				continue
			}
			sig++
			if !seenDot {
				intDigits++
			}
		case c == '.' && !seenDot:
			seenDot = true
		default:
			break mantissa
		}
	}
	if !seenDigit || sig == 0 {
		return false // no number, or zero
	}
	e := 0
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		neg := false
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			neg = s[j] == '-'
			j++
		}
		digits := 0
		for ; j < len(s) && s[j] >= '0' && s[j] <= '9'; j++ {
			digits++
			if digits > 6 {
				return true // an exponent this long is out of range
			}
			e = e*10 + int(s[j]-'0')
		}
		if neg {
			e = -e
		}
	}
	// The decimal exponent of the first significant digit, plus one.
	dec := e - leadFrac
	if intDigits > 0 {
		dec = e + intDigits
	}
	if bitSize == 32 && (dec <= -37 || dec >= 39) {
		return true
	}
	return sig > 19 || dec <= -307 || dec >= 309
}
