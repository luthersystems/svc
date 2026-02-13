package libhandlebars

import (
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/luthersystems/elps/elpsutil"
	"github.com/luthersystems/elps/lisp"
	"github.com/luthersystems/elps/lisp/lisplib/libjson"
	"github.com/luthersystems/raymond"
	"github.com/nyaruka/phonenumbers"
)

// Template exposes the raymond template type for package users
type Template *raymond.Template

// DefaultPackageName is the package name used by LoadPackage.
const (
	DefaultPackageName  = "handlebars"
	layoutISO           = "2006-01-02"
	layoutUK            = "02 January 2006"
	layoutDMYSlashShort = "02/01/06"
	layoutDMYSlashLong  = "02/01/2006"
	layoutDMYLong       = "02-01-2006"
)

// handlebarsPackage implements the elpsutil Package interfaces for the
// handlebars ELPS package.
type handlebarsPackage struct{}

func (handlebarsPackage) PackageName() string { return DefaultPackageName }

func (handlebarsPackage) PackageDoc() string {
	return `Handlebars template rendering engine.

Provides functions for parsing and rendering Handlebars templates with
JSON context data, powered by the raymond library.`
}

func (handlebarsPackage) Builtins() []lisp.LBuiltinDef {
	return builtins
}

// documentedBuiltin wraps an LBuiltinDef with a docstring.
type documentedBuiltin struct {
	lisp.LBuiltinDef
	docs string
}

func (b *documentedBuiltin) Docstring() string { return b.docs }

// LoadPackage loads the package.
func LoadPackage(env *lisp.LEnv) *lisp.LVal {
	return elpsutil.PackageLoader(&handlebarsPackage{})(env)
}

// Render renders a raymond.Template given a ctx
func Render(tpl *raymond.Template, ctx interface{}) (string, error) {
	result, err := tpl.Exec(ctx)
	if err != nil {
		return "", err
	}

	return result, nil
}

// Parse parses a template string, returning a raymond.Template
func Parse(template string) (*raymond.Template, error) {
	tpl, err := raymond.Parse(template)
	if err != nil {
		return &raymond.Template{}, err
	}
	addHelpers(tpl)
	return tpl, nil
}

var builtins = []lisp.LBuiltinDef{
	&documentedBuiltin{
		elpsutil.Function("libname", lisp.Formals(), builtInLibname),
		`Returns the name of the underlying template library ("raymond").`,
	},
	&documentedBuiltin{
		elpsutil.Function("version", lisp.Formals(), builtInVersion),
		`Returns the version string of the raymond template library.`,
	},
	&documentedBuiltin{
		elpsutil.Function("render", lisp.Formals("tpl", "ctx"), builtInRender),
		`Renders a Handlebars template string with the given context.

tpl is a Handlebars template string and ctx is a JSON-serializable
value used as the template context. Returns the rendered string.
Signals handlebars-parse on template syntax errors and
handlebars-render on rendering errors.`,
	},
	&documentedBuiltin{
		elpsutil.Function("must-parse", lisp.Formals("tpl"), builtInMustParse),
		`Validates that tpl is a syntactically correct Handlebars template.

Returns nil on success. Signals handlebars-parse if the template
contains syntax errors. Use this to validate templates at load time
without rendering them.`,
	},
}

func builtInLibname(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
	return lisp.String("raymond")
}

func builtInVersion(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
	return lisp.String(raymondVersion)
}

// globalKeyspace is used to support the {{global}} helper.
type globalKeyspace struct {
	ns, k string
}

func builtInMustParse(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
	template := args.Cells[0]

	switch template.Type {
	case lisp.LString:
	default:
		return env.Errorf("non-string template: %v", template.Type)
	}

	_, err := raymond.Parse(template.Str)
	if err != nil {
		return env.ErrorConditionf("handlebars-parse", "error parsing template: %v", err)
	}

	return lisp.Nil()
}

func builtInRender(env *lisp.LEnv, args *lisp.LVal) *lisp.LVal {
	template, context := args.Cells[0], args.Cells[1]

	switch template.Type {
	case lisp.LString:
	default:
		return env.Errorf("non-string template: %v", template.Type)
	}

	var contextBytes []byte

	switch context.Type {
	case lisp.LBytes:
		contextBytes = context.Bytes()
	default:
		var err error
		contextBytes, err = libjson.DefaultSerializer().Dump(context, false)
		if err != nil {
			return env.Errorf("error while serializing: %v", err)
		}
	}

	var jsonContext map[string]interface{}
	err := json.Unmarshal(contextBytes, &jsonContext)
	if err != nil {
		return env.Errorf("error while unmarshaling: %v", err)
	}
	tpl, err := raymond.Parse(template.Str)
	if err != nil {
		return env.ErrorConditionf("handlebars-parse", "error parsing template: %v", err)
	}
	addHelpers(tpl)
	result, err := tpl.Exec(jsonContext)
	if err != nil {
		return env.ErrorConditionf("handlebars-render", "error while rendering template: %v", err)
	}

	return lisp.String(result)
}

func addHelpers(tpl *raymond.Template) {
	tpl.RegisterHelper("eq", func(v1, v2 string, options *raymond.Options) bool {
		return v1 == v2
	})

	tpl.RegisterHelper("len", func(array []interface{}) int {
		return len(array)
	})

	tpl.RegisterHelper("not", func(v bool) bool {
		return !v
	})

	tpl.RegisterHelper("and", func(options *raymond.Options) bool {
		result := true
		for _, v := range options.Hash() {
			result = result && raymond.IsTrue(v)
		}
		return result
	})

	tpl.RegisterHelper("or", func(options *raymond.Options) bool {
		result := false
		for _, v := range options.Hash() {
			result = result || raymond.IsTrue(v)
		}
		return result
	})

	tpl.RegisterHelper("gt", func(v1, v2 string) bool {
		f1, ok1 := toFloat(v1)
		f2, ok2 := toFloat(v2)

		return ok1 && ok2 && f1 > f2
	})

	tpl.RegisterHelper("gte", func(v1, v2 string) bool {
		f1, ok1 := toFloat(v1)
		f2, ok2 := toFloat(v2)

		return ok1 && ok2 && f1 >= f2
	})

	tpl.RegisterHelper("lt", func(v1, v2 string) bool {
		f1, ok1 := toFloat(v1)
		f2, ok2 := toFloat(v2)

		return ok1 && ok2 && f1 < f2
	})

	tpl.RegisterHelper("lte", func(v1, v2 string) bool {
		f1, ok1 := toFloat(v1)
		f2, ok2 := toFloat(v2)

		return ok1 && ok2 && f1 <= f2
	})

	tpl.RegisterHelper("times", func(v1, v2 string) float64 {
		f1, ok1 := toFloat(v1)
		f2, ok2 := toFloat(v2)

		if !ok1 || !ok2 {
			return 0
		}
		return f1 * f2
	})

	tpl.RegisterHelper("div", func(v1, v2 string) float64 {
		f1, ok1 := toFloat(v1)
		f2, ok2 := toFloat(v2)

		if !ok1 || !ok2 {
			return 0
		}
		return f1 / f2
	})

	tpl.RegisterHelper("mod", func(v1, v2 string) float64 {
		f1, ok1 := toFloat(v1)
		f2, ok2 := toFloat(v2)

		if !ok1 && !ok2 {
			return 0
		}
		return math.Mod(f1, f2)
	})
	tpl.RegisterHelper("date-diff-month", dateDifferenceInMonthsHelper)

	tpl.RegisterHelper("is-after", dateAfterHelper)

	tpl.RegisterHelper("date-add-months", dateAddMonthsHelper)

	tpl.RegisterHelper("to-int", func(v interface{}) int {
		v2, ok := toInt(v)
		if !ok {
			return 0
		}
		return v2
	})

	tpl.RegisterHelper("plus", func(options *raymond.Options) float64 {
		var result float64
		for _, v := range options.Hash() {
			if f, ok := toFloat(v); ok {
				result += f
			}
		}
		return result
	})

	tpl.RegisterHelper("minus", func(v string, options *raymond.Options) float64 {
		result, _ := toFloat(v)
		for _, v := range options.Hash() {
			if f, ok := toFloat(v); ok {
				result -= f
			}
		}
		return result
	})

	tpl.RegisterHelper("select", func(options *raymond.Options) interface{} {
		from := options.HashProp("from")
		items, ok := from.([]interface{})
		if !ok {
			panic(fmt.Errorf("select: 'from' must be an array: %T", from))
		}
		where := options.HashStr("where")
		kv := strings.Split(where, "=")
		if len(kv) != 2 {
			panic(fmt.Errorf("select: 'where' not in K=V format: %s", where))
		}
		key := kv[0]
		val := kv[1]
		var res string
		for _, mi := range items {
			m, ok := mi.(map[string]interface{})
			if !ok {
				continue
			}
			if m[key] == val {
				res += raymond.Str(options.FnWith(m))
			}
		}
		return res
	})

	global := make(map[globalKeyspace]string)

	tpl.RegisterHelper("global", func(ns string, options *raymond.Options) interface{} {
		h := options.Hash()
		ki, ok := h["key"]
		if !ok {
			panic(fmt.Errorf("global: missing key"))
		}
		k, ok := ki.(string)
		if !ok {
			panic(fmt.Errorf("global: invalid key type: %T", ki))
		}
		vi, ok := h["val"]
		if !ok {
			return global[globalKeyspace{ns, k}]
		}
		v, ok := vi.(string)
		if !ok {
			panic(fmt.Errorf("global: invalid val type: %T", vi))
		}
		global[globalKeyspace{ns, k}] = v
		return ""
	})

	tpl.RegisterHelper("round-to-nth", roundToNthStrings)

	tpl.RegisterHelper("in-string-array", func(options *raymond.Options) bool {
		h := options.HashProp("haystack")
		hInterfaceItems, ok := h.([]interface{})
		if !ok {
			panic(fmt.Errorf("in-string-array: 'haystack' must be a string array, got: %T", hInterfaceItems))
		}
		// convert interface array into string array
		var haystack []string
		for _, i := range hInterfaceItems {
			s, ok := i.(string)
			if !ok {
				continue
			}
			haystack = append(haystack, s)
		}
		needle := options.HashStr("needle")

		for _, e := range haystack {
			if e == needle {
				return true
			}
		}
		return false
	})

	tpl.RegisterHelper("prettyp-num-en", func(num interface{}) string {
		f, ok := toFloat(num)
		if !ok {
			panic(fmt.Errorf("value passed in must be a number, got: %v", num))
		}
		return humanize.FormatFloat("#,###.##", f)
	})

	tpl.RegisterHelper("possessive", func(name string) string {
		nameSenitized := strings.TrimRight(name, " ")
		if len(nameSenitized) == 0 {
			return ""
		}
		if nameSenitized[len(nameSenitized)-1:] == "s" {
			return nameSenitized + "'"
		}
		return nameSenitized + "'s"
	})

	tpl.RegisterHelper("date-beautify", func(date string) string {
		if date == "" {
			return ""
		}
		d, err := parseDate(date)
		if err != nil {
			panic(fmt.Errorf("date-beautify: expecting date format YYYY-MM-DD, got: %v", err))
		}
		return d.Format(layoutUK)
	})

	tpl.RegisterHelper("date-DDMMYY-slash", func(date string) string {
		if date == "" {
			return ""
		}
		d, err := parseDate(date)
		if err != nil {
			panic(fmt.Errorf("date-DDMMYY-slash: expecting date format YYYY-MM-DD, got: %v", err))
		}
		return d.Format(layoutDMYSlashShort)
	})

	tpl.RegisterHelper("date-DDMMYYYY-slash", func(date string) string {
		if date == "" {
			return ""
		}
		d, err := parseDate(date)
		if err != nil {
			panic(fmt.Errorf("date-DDMMYYYY-slash: expecting date format YYYY-MM-DD, got: %v", err))
		}
		return d.Format(layoutDMYSlashLong)
	})

	tpl.RegisterHelper("date-DDMMYYYY", func(date string) string {
		if date == "" {
			return ""
		}
		d, err := parseDate(date)
		if err != nil {
			panic(fmt.Errorf("date-DDMMYYYY: expecting date format YYYY-MM-DD, got: %v", err))
		}
		return d.Format(layoutDMYLong)
	})

	// Format all GB numbers national format i.e without country code
	tpl.RegisterHelper("format-phone-gb", func(rawNum string) string {
		if rawNum == "" {
			return ""
		}

		formattedNum, err := phonenumbers.Parse(rawNum, "GB") // Parses number to be GB if country code is not included
		if err != nil {
			return rawNum
		}

		countryCode := phonenumbers.GetCountryCodeForRegion("GB")

		// Explicit check to ensure safe conversion
		if countryCode > math.MaxInt32 || countryCode < math.MinInt32 {
			return rawNum // avoid unsafe conversion
		}
		countryCode32 := int32(countryCode)

		if phonenumbers.IsValidNumber(formattedNum) && formattedNum.GetCountryCode() == countryCode32 {
			return phonenumbers.Format(formattedNum, phonenumbers.NATIONAL)
		}

		return rawNum
	})

	tpl.RegisterHelper("escape-uri-component", func(unescapedString string) string {
		return url.QueryEscape(unescapedString)
	})

	// Return string from primitive or empty string
	tpl.RegisterHelper("to-str", func(v interface{}) string {
		switch i := v.(type) {
		case string:
			return i
		case int:
			return strconv.Itoa(i)
		case int8:
		case int16:
		case int32:
		case int64:
			return strconv.Itoa(int(i))
		case float32:
		case float64:
			return fmt.Sprintf("%f", i)
		}

		return ""
	})
}

func toFloat(v interface{}) (float64, bool) {
	var f float64
	ok := true
	switch v := v.(type) {
	case string:
		var e error
		f, e = strconv.ParseFloat(v, 64)
		if e != nil {
			ok = false
		}
	case int:
		f = float64(v)
	case int8:
		f = float64(v)
	case int16:
		f = float64(v)
	case int32:
		f = float64(v)
	case int64:
		f = float64(v)
	case float64, float32:
		f, ok = v.(float64)
	default:
		ok = false
	}
	return f, ok
}

func toInt(v interface{}) (int, bool) {
	var f int
	ok := true
	switch v := v.(type) {
	case string:
		var e error
		int64Value, e := strconv.ParseInt(v, 10, bits.UintSize)
		if e != nil {
			ok = false
		}
		f = int(int64Value)
	case int:
		f = v
	case int8:
		f = int(v)
	case int16:
		f = int(v)
	case int32:
		f = int(v)
	case int64:
		if bits.UintSize < 64 && v > int64(math.MaxInt32) {
			ok = false
		} else {
			f = int(v)
		}
	case float64, float32:
		f = int(v.(float64))
	default:
		ok = false
	}
	return f, ok
}

func parseDate(date string) (time.Time, error) {
	return time.Parse(layoutISO, date)
}

func formatDate(date time.Time) string {
	return date.Format(layoutISO)
}

func dateDifferenceInMonthsHelper(startDate string, endDate string) int {
	start, err := parseDate(startDate)
	if err != nil {
		return 0
	}
	end, err := parseDate(endDate)
	if err != nil {
		return 0
	}
	return dateDifferenceInMonths(start, end)
}

func dateAfter(date time.Time, referenceDate time.Time) bool {
	return date.After(referenceDate)
}

func dateAfterHelper(testDate, referenceDate string) bool {
	date, err := parseDate(testDate)
	if err != nil {
		return false
	}
	refDate, err := parseDate(referenceDate)
	if err != nil {
		return false
	}
	return dateAfter(date, refDate)
}

func dateDifferenceInMonths(startDate time.Time, endDate time.Time) int {
	y, m, d, hour, min, sec := dateDifference(startDate, endDate)
	months := 12*y + m
	if d > 0 || hour > 0 || min > 0 || sec > 0 {
		months++
	}
	return months
}

func dateDifference(a, b time.Time) (year, month, day, hour, min, sec int) {
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

	year = int(y2 - y1)
	month = int(M2 - M1)
	day = int(d2 - d1)
	hour = int(h2 - h1)
	min = int(m2 - m1)
	sec = int(s2 - s1)

	// Normalize negative values
	if sec < 0 {
		sec += 60
		min--
	}
	if min < 0 {
		min += 60
		hour--
	}
	if hour < 0 {
		hour += 24
		day--
	}
	if day < 0 {
		// days in month:
		t := time.Date(y1, M1, 32, 0, 0, 0, 0, time.UTC)
		day += 32 - t.Day()
		month--
	}
	if month < 0 {
		month += 12
		year--
	}

	return
}

func dateAddMonths(startDate time.Time, months int) time.Time {
	return startDate.AddDate(0, months, 0)
}

func dateAddMonthsHelper(startDate string, months int) string {
	if date, err := parseDate(startDate); err == nil {
		return formatDate(dateAddMonths(date, months))
	}
	return startDate
}

func roundToNthStrings(x string, n string) string {
	xf, err := strconv.ParseFloat(x, 32)
	if err != nil {
		panic(fmt.Errorf("round-to-n: 'x' must be convertable to float: %s", x))
	}
	nn, err := strconv.ParseInt(n, 10, 32)
	if err != nil {
		panic(fmt.Errorf("round-to-n: 'n' must be convertable to int: %s", n))
	}
	return fmt.Sprintf(fmt.Sprintf("%%.%df", nn), xf)
}
