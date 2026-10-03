// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
	"encoding"
	"encoding/json"
	"fmt"
	"math/bits"
	"reflect"

	"github.com/luthersystems/svc/libhandlebars/hbs"
)

// jsonCoster receives the charges of encoding/json's reflective walk of a
// Go value (goJSONCost).
type jsonCoster interface {
	value(bytes int64) error // one value writing about bytes of JSON
	steps(n int64) error
}

var (
	marshalerType     = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// goJSONCost charges encoding/json's walk of v through c, before
// json.Marshal runs: a value at a time with its estimated bytes (strings at
// their escaped length, after a scan charged a step per started 64 bytes),
// map keys' sort, and a pointer cycle at the thousand levels encoding/json
// descends before it reports one. A value implementing json.Marshaler or
// encoding.TextMarshaler is one value: its method's work is the caller's.
// It returns tooDeep past maxDepth, which encoding/json would recurse
// through, and c's error, unchanged.
func goJSONCost(c jsonCoster, v reflect.Value, depth, maxDepth int, onPath map[uintptr]bool) (error, error) {
	if depth > maxDepth {
		return fmt.Errorf("json: Go value nests deeper than %d", maxDepth), nil
	}
	if !v.IsValid() {
		return nil, c.value(4)
	}
	if t := v.Type(); t.Implements(marshalerType) || t.Implements(textMarshalerType) {
		return nil, c.value(4)
	}
	var n int64
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil, c.value(4)
		}
		if v.Kind() == reflect.Pointer {
			p := v.Pointer()
			if onPath[p] {
				return nil, c.value(1000 * 16) // encoding/json reports it after 1000 levels
			}
			onPath[p] = true
			defer delete(onPath, p)
		}
		if err := c.value(1); err != nil {
			return nil, err
		}
		return goJSONCost(c, v.Elem(), depth+1, maxDepth, onPath)
	case reflect.String:
		if v.Len() > 0 {
			if err := c.steps(int64((v.Len()-1)/64 + 1)); err != nil {
				return nil, err
			}
		}
		n = jsonStringLen(v.String())
	case reflect.Struct:
		if err := c.value(2); err != nil {
			return nil, err
		}
		for i := range v.NumField() {
			if deep, err := goJSONCost(c, v.Field(i), depth+1, maxDepth, onPath); deep != nil || err != nil {
				return deep, err
			}
		}
		return nil, nil
	case reflect.Map:
		if err := c.steps(int64(v.Len()) * int64(1+bits.Len(uint(v.Len())))); err != nil {
			return nil, err
		}
		if err := c.value(2); err != nil {
			return nil, err
		}
		it := v.MapRange()
		for it.Next() {
			if deep, err := goJSONCost(c, it.Key(), depth+1, maxDepth, onPath); deep != nil || err != nil {
				return deep, err
			}
			if deep, err := goJSONCost(c, it.Value(), depth+1, maxDepth, onPath); deep != nil || err != nil {
				return deep, err
			}
		}
		return nil, nil
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			n = int64(v.Len())*4/3 + 4 // base64
			break
		}
		if err := c.value(2); err != nil {
			return nil, err
		}
		for i := range v.Len() {
			if deep, err := goJSONCost(c, v.Index(i), depth+1, maxDepth, onPath); deep != nil || err != nil {
				return deep, err
			}
		}
		return nil, nil
	default:
		n = 24
	}
	return nil, c.value(n)
}

// goBudget counts the Go API's conversion steps against MaxSteps, as the
// ELPS path's encode walk charges json:dump-bytes's: 3 a value and a step
// per started KiB of JSON. It is also the hbs.Meter for decoding.
type goBudget struct {
	used, max, size, kib int64
}

func (b *goBudget) Charge(n int64) error {
	b.used += n
	if b.used > b.max {
		return &hbs.Error{Kind: hbs.KindLimit, Msg: fmt.Sprintf("template evaluation exceeds the maximum of %d steps", b.max)}
	}
	return nil
}

func (b *goBudget) steps(n int64) error { return b.Charge(n) }

func (b *goBudget) value(bytes int64) error {
	b.size += bytes
	n := int64(3)
	if kib := (b.size + 1023) >> 10; kib > b.kib {
		n += kib - b.kib
		b.kib = kib
	}
	return b.Charge(n)
}

// jsonGoMaxDepth bounds the nesting of a Go context converted through JSON
// (encoding/json itself has no bound for acyclic values).
const jsonGoMaxDepth = 1024
