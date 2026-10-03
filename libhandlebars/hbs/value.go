// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

// Value is a template context value: the Go value json.Unmarshal produces
// for a JSON document, plus the numbers a template literal produces.
//
// A Value built by FromJSON is one of these dynamic types, and nothing else:
//
//	nil                    JSON null, or a missing value
//	bool                   JSON true / false, template literals true / false
//	float64                every JSON number
//	int                    template integer literals ({{to-str 3}}) only
//	string                 JSON strings, template string literals
//	[]any                  JSON arrays (elements are Values)
//	map[string]any         JSON objects (values are Values)
//
// The int / float64 split is part of the language that raymond rendered, and
// helpers observe it: {{to-str 3}} renders "3" but {{to-str n}} with n=3 from
// the context renders "3.000000". A context from FromJSON therefore never
// holds an int (render-fixed's ELPS adapter puts ELPS ints back as ints).
//
// Render also accepts any Go value as the context, and reads it lazily by
// reflection with raymond's Go semantics (see goreflect.go): that is how the
// Go API renders a caller's value.
// Objects are iterated in sorted key order; the engine never depends on Go map
// order.
//
// The engine reads Values and never modifies them, so one context may be
// rendered by several goroutines at once.
type Value = any

// FromJSON decodes a render context exactly as svc's handlebars:render did:
// json.Unmarshal into a map[string]any. It accepts and rejects the same
// documents and returns the same error text. A top-level array, string,
// number or boolean is an error from encoding/json; a top-level null decodes
// to a nil map, which renders like an empty object.
//
// # Producing Values without JSON
//
// An adapter that builds Values straight from ELPS values must produce what
// libjson's serializer followed by FromJSON produces, so that a render does
// not depend on which path built its context:
//
//   - The root must be a sorted-map (an object) or '() (null: a nil
//     map[string]any). Any other root is the encoding/json error above.
//   - '() anywhere is nil (JSON null). An empty sorted-map is an empty,
//     non-nil map[string]any. Lists and vectors are []any.
//   - Every number is a float64. An ELPS int i becomes float64(i), rounded to
//     nearest even like the JSON decoder (above 2^53 precision is lost). A
//     float keeps its value. +Inf, -Inf and NaN are a serialization error.
//   - Strings are strings; invalid UTF-8 is replaced by U+FFFD as
//     encoding/json does. Bytes are their standard base64 encoding (as a
//     string). Symbols and keywords are strings, as libjson writes them.
//   - Map keys are strings as libjson writes them; when two keys serialize to
//     the same string the later one wins, as in json.Unmarshal.
//   - true / false are bools. No int, and no other Go type, may appear.
func FromJSON(data []byte) (Value, error) {
	return FromJSONMetered(data, nil)
}

// FromJSONMetered is FromJSON with its decoding charged to m (nil: none),
// by a pass over the bytes before json decodes them: JSON's structure and
// strings cost tens of ns a token, and strconv.ParseFloat takes about 20 us
// on some 6-byte inputs (see floatCost). The result and the error text are
// FromJSON's; a Meter error is returned unchanged, before decoding.
//
// Charges, in document order: 2 steps per { or [, 1 per , or : and per
// null, true or false, 1 plus a step per started 8 bytes per string, and
// numberCost plus floatCost per number token. Each distinct number literal
// is parsed once. A document that is invalid or not an object, which json
// rejects without decoding its numbers, costs a step per started 8 bytes.
func FromJSONMetered(data []byte, m Meter) (Value, error) {
	if !json.Valid(data) || !objectRoot(data) {
		if err := charge(m, units(len(data), 8)); err != nil {
			return nil, err
		}
		var v map[string]any
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return v, nil
	}
	nums, err := scanJSON(data, m)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	convertNumbers(v, nums)
	return v, nil
}

// numberCost is the steps a number token costs beyond floatCost: the
// decoder boxes it, and a distinct literal is parsed and stored (about
// 900 ns in all).
const numberCost = 8

// objectRoot reports whether valid JSON data is an object.
func objectRoot(data []byte) bool {
	for _, c := range data {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return c == '{'
		}
	}
	return false
}

// scanJSON charges valid JSON data as FromJSONMetered describes and parses
// its number tokens in document order. The first literal out of float64's
// range is json.Unmarshal's error for it.
func scanJSON(data []byte, m Meter) (map[string]float64, error) {
	nums := map[string]float64{}
	var pending int64
	flush := func(force bool) error {
		if m == nil || pending == 0 || (!force && pending < meterBatch) {
			return nil
		}
		n := pending
		pending = 0
		return m.Charge(n)
	}
	for i := 0; i < len(data); i++ {
		switch c := data[i]; {
		case c == '"':
			start := i
			for i++; i < len(data) && data[i] != '"'; i++ {
				if data[i] == '\\' {
					i++
				}
			}
			pending += 1 + units(i-start-1, 8)
		case c == '{' || c == '[':
			pending += 2
		case c == ',' || c == ':' || c == 'n' || c == 't' || c == 'f':
			pending++
			if c != ',' && c != ':' {
				for i+1 < len(data) && data[i+1] >= 'a' && data[i+1] <= 'z' {
					i++ // the rest of null, true or false
				}
			}
		case c == '-' || (c >= '0' && c <= '9'):
			j := i
			for j < len(data) && strings.IndexByte("+-.eE0123456789", data[j]) >= 0 {
				j++
			}
			lit := data[i:j]
			pending += numberCost + floatCost(string(lit))
			if err := flush(false); err != nil {
				return nil, err
			}
			if _, done := nums[string(lit)]; !done {
				f, err := strconv.ParseFloat(string(lit), 64)
				if err != nil {
					return nil, &json.UnmarshalTypeError{Value: "number " + string(lit), Type: reflect.TypeFor[float64](), Offset: int64(j)}
				}
				nums[string(lit)] = f
			}
			i = j - 1
			continue
		}
		if err := flush(false); err != nil {
			return nil, err
		}
	}
	return nums, flush(true)
}

// convertNumbers replaces the json.Number values of v with their parsed
// float64.
func convertNumbers(v any, nums map[string]float64) any {
	switch x := v.(type) {
	case json.Number:
		return nums[string(x)]
	case map[string]any:
		for k, e := range x {
			x[k] = convertNumbers(e, nums)
		}
	case []any:
		for i, e := range x {
			x[i] = convertNumbers(e, nums)
		}
	}
	return v
}

// isTrue reports raymond's truthiness (text/template's isTrue): nil, false,
// zero numbers and empty strings, arrays and maps are false.
func isTrue(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case int:
		return x != 0
	case float64:
		return x != 0
	case int8:
		return x != 0
	case int16:
		return x != 0
	case int32:
		return x != 0
	case int64:
		return x != 0
	case uint:
		return x != 0
	case uint8:
		return x != 0
	case uint16:
		return x != 0
	case uint32:
		return x != 0
	case uint64:
		return x != 0
	case float32:
		return x != 0
	default:
		return goTruth(v)
	}
}

// str returns raymond's string form of a value (raymond.Str).
func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	}
	return string(appendStr(nil, v))
}

// appendStr appends raymond's string form of v: arrays concatenate their
// elements, objects print UNPRINTABLE, floats use FormatFloat('f', -1).
func appendStr(dst []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return dst
	case string:
		return append(dst, x...)
	case []any:
		for _, e := range x {
			dst = appendStr(dst, e)
		}
		return dst
	case bool:
		return strconv.AppendBool(dst, x)
	case int:
		return strconv.AppendInt(dst, int64(x), 10)
	case float64:
		return strconv.AppendFloat(dst, x, 'f', -1, 64)
	case int8:
		return strconv.AppendInt(dst, int64(x), 10)
	case int16:
		return strconv.AppendInt(dst, int64(x), 10)
	case int32:
		return strconv.AppendInt(dst, int64(x), 10)
	case int64:
		return strconv.AppendInt(dst, x, 10)
	case uint:
		return strconv.AppendUint(dst, uint64(x), 10)
	case uint8:
		return strconv.AppendUint(dst, uint64(x), 10)
	case uint16:
		return strconv.AppendUint(dst, uint64(x), 10)
	case uint32:
		return strconv.AppendUint(dst, uint64(x), 10)
	case uint64:
		return strconv.AppendUint(dst, x, 10)
	case float32:
		return strconv.AppendFloat(dst, float64(x), 'f', -1, 64)
	default:
		return append(dst, "UNPRINTABLE"...)
	}
}
