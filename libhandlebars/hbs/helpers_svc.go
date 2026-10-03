// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"errors"
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
	// math.Mod reduces the dividend one exponent bit at a time: about 6 ns
	// for each bit the exponents differ by.
	_, e1 := math.Frexp(f1)
	_, e2 := math.Frexp(f2)
	c.r.steps1(int64(max(0, e1-e2)) / 8)
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
		c.r.hashKey(len(key))
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
	xf, err := c.r.parseFloat(x, bitSize)
	if err != nil {
		c.r.failWith("round-to-n: 'x' must be convertable to float: ", x)
	}
	c.r.scanBytes(len(n))
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
		c.r.steps1(units(int(nn), fmtUnit)) // formatting nn digits
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
	s := humanize.FormatFloat("#,###.##", f)
	c.r.steps1(int64(len(s))) // humanize of a large float: about 100 ns a byte
	return s
}

func hPossessive(c *hcall) any {
	c.r.scanBytes(len(c.argStr(0))) // TrimRight scans byte by byte
	name := strings.TrimRight(c.argStr(0), " ")
	if name == "" {
		return ""
	}
	c.r.reserveProduced(len(name) + 2) // before building the result
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
		// Only a 10-byte input can parse. On any other, time.Parse quotes
		// part of it into its error, and the text quotes it again, up to
		// about 80 ns a byte in all: charge that first.
		prefix := name + ": expecting date format YYYY-MM-DD, got: "
		if len(date) != len(layoutISO) {
			c.r.steps1(int64(len(date)))
			// The message quotes the whole input at least once: refuse
			// before time.Parse copies it if even that cannot fit.
			c.r.reserveProduced(len(prefix) + len("parsing time ") + len(date) + 2)
		}
		d, err := time.Parse(layoutISO, date)
		if err != nil {
			// Bound the exact message before building it.
			var pe *time.ParseError
			if errors.As(err, &pe) {
				c.r.reserveProduced(len(prefix) + parseErrorLen(pe))
			}
			c.r.fail(prefix + err.Error())
		}
		return d.Format(layout)
	}
}

func hFormatPhoneGB(c *hcall) any {
	rawNum := c.argStr(0)
	if rawNum == "" {
		return ""
	}
	// phonenumbers takes 40-175 us a call (it caps its input at 250 bytes):
	// a fixed charge keeps that within the base cost per step.
	c.r.steps1(phoneCallCost)
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
	c.r.steps1(units(len(s), 8)) // QueryEscape: about 5 ns a byte
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
		s := toStrFixed(c.args[0])
		c.r.formatted(len(s))
		return s
	}
	switch i := c.args[0].(type) {
	case string:
		return i
	case int:
		return strconv.Itoa(i)
	case int64:
		return strconv.Itoa(int(i))
	case float64:
		s := fmt.Sprintf("%f", i)
		c.r.steps1(units(len(s), 2)) // %f of a large float: about 25 ns a byte
		return s
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
	start, err := parseISODate(c.argStr(0))
	if err != nil {
		return 0
	}
	end, err := parseISODate(c.argStr(1))
	if err != nil {
		return 0
	}
	return dateDifferenceInMonths(start, end)
}

func hIsAfter(c *hcall) any {
	date, err := parseISODate(c.argStr(0))
	if err != nil {
		return false
	}
	ref, err := parseISODate(c.argStr(1))
	if err != nil {
		return false
	}
	return date.After(ref)
}

func hDateAddMonths(c *hcall) any {
	start := c.argStr(0)
	months, _ := c.args[1].(int)
	if date, err := parseISODate(start); err == nil {
		return date.AddDate(0, months, 0).Format(layoutISO)
	}
	return start
}

// errNotISODate stands for time.Parse's error on an input that is not 10
// bytes long, for the helpers that discard the error: only a 10-byte input
// can match layoutISO, and time.Parse builds its error (quoting the input)
// eagerly, at about 20 ns a byte.
var errNotISODate = errors.New("not a YYYY-MM-DD date")

// parseISODate is time.Parse(layoutISO, s) for callers that discard the
// error text.
func parseISODate(s string) (time.Time, error) {
	if len(s) != len(layoutISO) {
		return time.Time{}, errNotISODate
	}
	return time.Parse(layoutISO, s)
}

// parseErrorLen is len(e.Error()), computed without building it.
func parseErrorLen(e *time.ParseError) int {
	if e.Message == "" {
		return len("parsing time ") + quotedLen(e.Value) + len(" as ") + quotedLen(e.Layout) +
			len(": cannot parse ") + quotedLen(e.ValueElem) + len(" as ") + quotedLen(e.LayoutElem)
	}
	return len("parsing time ") + quotedLen(e.Value) + len(e.Message)
}

// quotedLen is the length of the time package's quote(s): a byte below
// 0x20 or from 0x80 up is written \xHH, a quote or backslash is escaped,
// and the text is enclosed in double quotes.
func quotedLen(s string) int {
	n := 2
	for i := range len(s) {
		switch b := s[i]; {
		case b < ' ' || b >= 0x80:
			n += 4
		case b == '"' || b == '\\':
			n += 2
		default:
			n++
		}
	}
	return n
}

// phoneCallCost is the steps a format-phone-gb call on a non-empty input
// costs.
const phoneCallCost = 2048

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
		f, err := r.parseFloat(x, 64)
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
		r.scanBytes(len(x))
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
