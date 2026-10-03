// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

// floatCost is the steps parsing s as a float costs: a step per started
// scanUnit bytes, plus slowFloatPremium and a step per byte when s is in the
// class strconv.ParseFloat parses slowly (see slowFloat). The slow path
// takes about 20 us for a subnormal like 5e-324 however short, and grows
// with the digits.
func floatCost(s string) int64 {
	c := max(1, units(len(s), scanUnit))
	if slowFloat(s) {
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
func slowFloat(s string) bool {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	sig, intDigits, leadFrac := 0, 0, 0
	seenDot, seenDigit := false, false
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			if c == '0' && sig == 0 {
				if seenDot {
					leadFrac++
				}
				seenDigit = true
				continue
			}
			sig++
			seenDigit = true
			if !seenDot {
				intDigits++
			}
		case c == '.' && !seenDot:
			seenDot = true
		case c == 'e' || c == 'E':
			goto exp
		default:
			return false
		}
	}
	return seenDigit && sig > 19
exp:
	if !seenDigit {
		return false
	}
	i++
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	e, digits := 0, 0
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
		digits++
		if digits > 6 {
			return true // an exponent this long is out of range
		}
		e = e*10 + int(c-'0')
	}
	if neg {
		e = -e
	}
	if sig == 0 {
		return false // zero
	}
	dec := e - leadFrac
	if intDigits > 0 {
		dec = e + intDigits
	}
	return sig > 19 || dec <= -307 || dec >= 309
}
