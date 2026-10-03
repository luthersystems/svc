// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"fmt"
	"math"
	"math/bits"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/luthersystems/svc/libhandlebars/hbs/internal/floatint"
	"github.com/nyaruka/phonenumbers"
)

// svc's helpers (svc/libhandlebars addHelpers), ported without reflection.
// Each entry's args are the Go parameter types of svc's helper function; see
// callFunc for how parameters are converted.
//
// # ModeFixed
//
// ModeCompat keeps every helper bug, so output does not change. ModeFixed
// differs only here:
//
//	helper / rule        ModeCompat (raymond + svc)                           ModeFixed
//	to-str               context numbers print "%f" (3 -> "3.000000");       numbers print as {{x}} does:
//	                     int8..int32, uints, float32 print ""                 FormatFloat('f', -1) / decimal ints, every kind
//	mod                  guard `!ok1 && !ok2`: {{mod 7 "x"}} is NaN          guard `!ok1 || !ok2`: 0
//	toFloat (gt, plus..) float32 is "not a number"                           float32 converts
//	round-to-nth         parses x as a 32-bit float                           64-bit parse
//	                     ("123456789.12" -> 123456792.00)                     ("123456789.12" -> 123456789.12)
//	int parameters       a context number is a type error                     an integral float64 is accepted
//	(date-add-months)    ("type float64 but it should be int")
//	if/unless includeZero only the template literal 0                        also a context number 0
//	to-int float32       panics in svc: a render error                        converts like a float64
//
// Both modes add plus/minus hash values in sorted key order (raymond added
// them in Go map order, so the result was not deterministic). Both modes
// convert NaN, +-Inf and out-of-range floats in to-int to the amd64 result,
// math.MinInt64, on every CPU (raymond used Go's int(f), which differs
// between CPU types); see floatToInt and DETERMINISM.md.

const (
	layoutISO           = "2006-01-02"
	layoutUK            = "02 January 2006"
	layoutDMYSlashShort = "02/01/06"
	layoutDMYSlashLong  = "02/01/2006"
	layoutDMYLong       = "02-01-2006"
)

// globalKey is a key of the global helper's per-render map.
type globalKey struct {
	ns, k string
}

func svcHelpers() map[string]*helper {
	s2 := []argKind{argString, argString}
	s1 := []argKind{argString}
	a1 := []argKind{argAny}
	return map[string]*helper{
		"eq":                   {fn: hEq, args: s2},
		"len":                  {fn: hLen, args: []argKind{argSlice}},
		"not":                  {fn: hNot, args: []argKind{argBool}},
		"and":                  {fn: hAnd},
		"or":                   {fn: hOr},
		"gt":                   {fn: cmpHelper(func(a, b float64) bool { return a > b }), args: s2},
		"gte":                  {fn: cmpHelper(func(a, b float64) bool { return a >= b }), args: s2},
		"lt":                   {fn: cmpHelper(func(a, b float64) bool { return a < b }), args: s2},
		"lte":                  {fn: cmpHelper(func(a, b float64) bool { return a <= b }), args: s2},
		"times":                {fn: hTimes, args: s2},
		"div":                  {fn: hDiv, args: s2},
		"mod":                  {fn: hMod, args: s2},
		"date-diff-month":      {fn: hDateDiffMonth, args: s2},
		"is-after":             {fn: hIsAfter, args: s2},
		"date-add-months":      {fn: hDateAddMonths, args: []argKind{argString, argInt}},
		"to-int":               {fn: hToInt, args: a1},
		"plus":                 {fn: hPlus},
		"minus":                {fn: hMinus, args: s1},
		"select":               {fn: hSelect, streams: true},
		"global":               {fn: hGlobal, args: s1},
		"round-to-nth":         {fn: hRoundToNth, args: s2},
		"in-string-array":      {fn: hInStringArray},
		"prettyp-num-en":       {fn: hPrettyNumEn, args: a1},
		"possessive":           {fn: hPossessive, args: s1},
		"date-beautify":        {fn: dateFormatHelper("date-beautify", layoutUK), args: s1},
		"date-DDMMYY-slash":    {fn: dateFormatHelper("date-DDMMYY-slash", layoutDMYSlashShort), args: s1},
		"date-DDMMYYYY-slash":  {fn: dateFormatHelper("date-DDMMYYYY-slash", layoutDMYSlashLong), args: s1},
		"date-DDMMYYYY":        {fn: dateFormatHelper("date-DDMMYYYY", layoutDMYLong), args: s1},
		"format-phone-gb":      {fn: hFormatPhoneGB, args: s1},
		"escape-uri-component": {fn: hEscapeURIComponent, args: s1},
		"to-str":               {fn: hToStr, args: a1},
	}
}

func hEq(c *hcall) any { return c.argStr(0) == c.argStr(1) }

func hLen(c *hcall) any {
	a, _ := c.args[0].([]any)
	return len(a)
}

func hNot(c *hcall) any {
	b, _ := c.args[0].(bool)
	return !b
}

func hAnd(c *hcall) any {
	result := true
	for _, v := range c.hash {
		result = result && isTrue(v)
	}
	return result
}

func hOr(c *hcall) any {
	result := false
	for _, v := range c.hash {
		result = result || isTrue(v)
	}
	return result
}

func cmpHelper(cmp func(a, b float64) bool) func(c *hcall) any {
	return func(c *hcall) any {
		f1, ok1 := c.r.toFloat(c.args[0])
		f2, ok2 := c.r.toFloat(c.args[1])
		return ok1 && ok2 && cmp(f1, f2)
	}
}

func hTimes(c *hcall) any {
	f1, ok1 := c.r.toFloat(c.args[0])
	f2, ok2 := c.r.toFloat(c.args[1])
	if !ok1 || !ok2 {
		return float64(0)
	}
	return f1 * f2
}

func hDiv(c *hcall) any {
	f1, ok1 := c.r.toFloat(c.args[0])
	f2, ok2 := c.r.toFloat(c.args[1])
	if !ok1 || !ok2 {
		return float64(0)
	}
	return f1 / f2
}

func hMod(c *hcall) any {
	f1, ok1 := c.r.toFloat(c.args[0])
	f2, ok2 := c.r.toFloat(c.args[1])
	if c.r.mode == ModeFixed {
		if !ok1 || !ok2 {
			return float64(0)
		}
	} else if !ok1 && !ok2 {
		return float64(0)
	}
	return math.Mod(f1, f2)
}

func hToInt(c *hcall) any {
	n, ok := c.r.toInt(c.args[0])
	if !ok {
		return 0
	}
	return n
}

// hPlus adds the hash values in sorted key order.
func hPlus(c *hcall) any {
	var result float64
	for _, v := range c.sortedHashValues() {
		if f, ok := c.r.toFloat(v); ok {
			result += f
		}
	}
	return result
}

// hMinus subtracts the hash values in sorted key order.
func hMinus(c *hcall) any {
	result, _ := c.r.toFloat(c.args[0])
	for _, v := range c.sortedHashValues() {
		if f, ok := c.r.toFloat(v); ok {
			result -= f
		}
	}
	return result
}

// hSelect renders the block once for each object in from whose field K
// equals the string V (where="K=V"). It streams its sections.
func hSelect(c *hcall) any {
	from := c.hash["from"]
	items, ok := from.([]any)
	if !ok {
		c.r.fail(fmt.Sprintf("select: 'from' must be an array: %T", from))
	}
	where := c.hashStr("where")
	// svc split on "=" and wanted exactly two parts: exactly one "=".
	// Cut and Count allocate nothing, whatever where holds.
	key, val, _ := strings.Cut(where, "=")
	if strings.Count(where, "=") != 1 {
		c.r.failWith("select: 'where' not in K=V format: ", where)
	}
	for _, mi := range items {
		c.r.stepKiB(len(key))
		m, isMap := mi.(map[string]any)
		if !isMap {
			continue
		}
		// svc compares interfaces: only a string field can match.
		if s, isStr := m[key].(string); isStr && c.r.compare(s, val) {
			c.fnWith(m)
		}
	}
	return nil
}

// hGlobal reads or writes the per-render global map.
func hGlobal(c *hcall) any {
	ns := c.argStr(0)
	ki, ok := c.hash["key"]
	if !ok {
		c.r.fail("global: missing key")
	}
	k, ok := ki.(string)
	if !ok {
		c.r.fail(fmt.Sprintf("global: invalid key type: %T", ki))
	}
	// The global map hashes the namespace and key on every read or write.
	c.r.hashKey(len(ns) + len(k))
	vi, ok := c.hash["val"]
	if !ok {
		return c.r.global[globalKey{ns, k}]
	}
	v, ok := vi.(string)
	if !ok {
		c.r.fail(fmt.Sprintf("global: invalid val type: %T", vi))
	}
	if c.r.global == nil {
		c.r.global = make(map[globalKey]string)
	}
	c.r.global[globalKey{ns, k}] = v
	return ""
}

func hRoundToNth(c *hcall) any {
	x, n := c.argStr(0), c.argStr(1)
	bitSize := 32
	if c.r.mode == ModeFixed {
		bitSize = 64
	}
	xf, err := strconv.ParseFloat(x, bitSize)
	if err != nil {
		c.r.failWith("round-to-n: 'x' must be convertable to float: ", x)
	}
	nn, err := strconv.ParseInt(n, 10, 32)
	if err != nil {
		c.r.failWith("round-to-n: 'n' must be convertable to int: ", n)
	}
	// The output holds nn digits after the point: bound them before
	// formatting. fmt ignores a precision it cannot parse (see
	// fmtPrecisionOK) and prints a short error string instead, so only a
	// precision it accepts is charged.
	// The result is then charged at its exact length, as every helper
	// result is (callFunc).
	if fmtPrecisionOK(nn) {
		c.r.reserveProduced(int(nn))
	}
	return fmt.Sprintf(fmt.Sprintf("%%.%df", nn), xf)
}

// fmtPrecisionOK reports whether fmt accepts n as a format precision. fmt's
// parsenum stops reading digits once the number read so far exceeds 1e6, so
// it accepts exactly the n whose digits but the last form at most 1e6:
// n <= 10,000,009. Above that, Sprintf prints "%!(NOVERB)%!(EXTRA ...)".
func fmtPrecisionOK(n int64) bool { return n/10 <= 1e6 }

func hInStringArray(c *hcall) any {
	items, ok := c.hash["haystack"].([]any)
	if !ok {
		// svc prints the type of the failed assertion's zero value.
		c.r.fail("in-string-array: 'haystack' must be a string array, got: []interface {}")
	}
	needle := c.hashStr("needle")
	for _, i := range items {
		c.r.step()
		if s, isStr := i.(string); isStr && c.r.compare(s, needle) {
			return true
		}
	}
	return false
}

func hPrettyNumEn(c *hcall) any {
	num := c.args[0]
	f, ok := c.r.toFloat(num)
	if !ok {
		// The message holds the whole value as fmt's %v prints it, built by a
		// charged, depth-bounded walk instead of fmt's recursion.
		c.r.fail(string(c.r.appendV([]byte("value passed in must be a number, got: "), num)))
	}
	return humanize.FormatFloat("#,###.##", f)
}

func hPossessive(c *hcall) any {
	name := strings.TrimRight(c.argStr(0), " ")
	if name == "" {
		return ""
	}
	if name[len(name)-1] == 's' {
		return name + "'"
	}
	return name + "'s"
}

func dateFormatHelper(name, layout string) func(c *hcall) any {
	return func(c *hcall) any {
		date := c.argStr(0)
		if date == "" {
			return ""
		}
		d, err := time.Parse(layoutISO, date)
		if err != nil {
			c.r.fail(fmt.Sprintf("%s: expecting date format YYYY-MM-DD, got: %v", name, err))
		}
		return d.Format(layout)
	}
}

func hFormatPhoneGB(c *hcall) any {
	rawNum := c.argStr(0)
	if rawNum == "" {
		return ""
	}
	formattedNum, err := phonenumbers.Parse(rawNum, "GB")
	if err != nil {
		return rawNum
	}
	countryCode := phonenumbers.GetCountryCodeForRegion("GB")
	if countryCode > math.MaxInt32 || countryCode < math.MinInt32 {
		return rawNum
	}
	if phonenumbers.IsValidNumber(formattedNum) && formattedNum.GetCountryCode() == int32(countryCode) {
		return phonenumbers.Format(formattedNum, phonenumbers.NATIONAL)
	}
	return rawNum
}

func hEscapeURIComponent(c *hcall) any {
	s := c.argStr(0)
	// Bound the result before building it: its exact length is the input's
	// plus two bytes for each byte QueryEscape writes as %XX.
	c.r.reserveProduced(queryEscapedLen(s))
	return url.QueryEscape(s)
}

// queryEscapedLen is len(url.QueryEscape(s)): letters, digits and -_.~ stay,
// a space becomes +, and every other byte becomes %XX.
func queryEscapedLen(s string) int {
	n := len(s)
	for i := range len(s) {
		switch c := s[i]; {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == ' ':
		default:
			n += 2
		}
	}
	return n
}

func hToStr(c *hcall) any {
	if c.r.mode == ModeFixed {
		return toStrFixed(c.args[0])
	}
	switch i := c.args[0].(type) {
	case string:
		return i
	case int:
		return strconv.Itoa(i)
	case int64:
		return strconv.Itoa(int(i))
	case float64:
		return fmt.Sprintf("%f", i)
	default:
		// svc's switch has empty int8, int16, int32 and float32 cases.
		return ""
	}
}

func toStrFixed(v any) string {
	switch i := v.(type) {
	case string:
		return i
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return str(i)
	case float32:
		return strconv.FormatFloat(float64(i), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(i, 'f', -1, 64)
	default:
		return ""
	}
}

// Dates.

func hDateDiffMonth(c *hcall) any {
	start, err := time.Parse(layoutISO, c.argStr(0))
	if err != nil {
		return 0
	}
	end, err := time.Parse(layoutISO, c.argStr(1))
	if err != nil {
		return 0
	}
	return dateDifferenceInMonths(start, end)
}

func hIsAfter(c *hcall) any {
	date, err := time.Parse(layoutISO, c.argStr(0))
	if err != nil {
		return false
	}
	ref, err := time.Parse(layoutISO, c.argStr(1))
	if err != nil {
		return false
	}
	return date.After(ref)
}

func hDateAddMonths(c *hcall) any {
	start := c.argStr(0)
	months, _ := c.args[1].(int)
	if date, err := time.Parse(layoutISO, start); err == nil {
		return date.AddDate(0, months, 0).Format(layoutISO)
	}
	return start
}

func dateDifferenceInMonths(startDate, endDate time.Time) int {
	y, m, d, hour, mins, sec := dateDifference(startDate, endDate)
	months := 12*y + m
	if d > 0 || hour > 0 || mins > 0 || sec > 0 {
		months++
	}
	return months
}

// dateDifference is svc's dateDifference, unchanged.
func dateDifference(a, b time.Time) (int, int, int, int, int, int) {
	if a.Location() != b.Location() {
		b = b.In(a.Location())
	}
	if a.After(b) {
		a, b = b, a
	}
	y1, M1, d1 := a.Date()
	y2, M2, d2 := b.Date()

	h1, m1, s1 := a.Clock()
	h2, m2, s2 := b.Clock()

	year := y2 - y1
	month := int(M2 - M1)
	day := d2 - d1
	hour := h2 - h1
	mins := m2 - m1
	sec := s2 - s1

	if sec < 0 {
		sec += 60
		mins--
	}
	if mins < 0 {
		mins += 60
		hour--
	}
	if hour < 0 {
		hour += 24
		day--
	}
	if day < 0 {
		t := time.Date(y1, M1, 32, 0, 0, 0, 0, time.UTC)
		day += 32 - t.Day()
		month--
	}
	if month < 0 {
		month += 12
		year--
	}
	return year, month, day, hour, mins, sec
}

// Numbers.

// toFloat is svc's toFloat. In ModeCompat a float32 is not a number (svc's
// `f, ok = v.(float64)` fails for it).
func (r *renderer) toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case string:
		r.read(len(x))
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case float32:
		if r.mode == ModeFixed {
			return float64(x), true
		}
		return 0, false
	default:
		return 0, false
	}
}

// toInt is svc's toInt.
func (r *renderer) toInt(v any) (int, bool) {
	switch x := v.(type) {
	case string:
		r.read(len(x))
		n, err := strconv.ParseInt(x, 10, bits.UintSize)
		if err != nil {
			return int(n), false
		}
		return int(n), true
	case int:
		return x, true
	case int8:
		return int(x), true
	case int16:
		return int(x), true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float64:
		return floatToInt(x), true
	case float32:
		if r.mode == ModeFixed {
			return floatToInt(float64(x)), true
		}
		// svc's v.(float64) assertion panics for a float32.
		r.fail("to-int: float32 value")
		return 0, false
	default:
		return 0, false
	}
}

// floatToInt is floatint.ToInt: Go's int(f) as compiled for amd64, on every
// CPU (see DETERMINISM.md).
func floatToInt(f float64) int { return floatint.ToInt(f) }
