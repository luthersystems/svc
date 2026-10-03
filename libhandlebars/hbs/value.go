// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"encoding/json"
	"strconv"
)

// Value is a template context value: the Go value json.Unmarshal produces
// for a JSON document, plus the numbers a template literal produces.
//
// A Value is one of these dynamic types, and nothing else:
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
// the context renders "3.000000". A context therefore never holds an int.
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
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
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
		return true
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
