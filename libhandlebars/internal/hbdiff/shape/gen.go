// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package shape

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"sort"
)

// Edge-class values the generator draws from. They are generic, never
// taken from a source template.
var (
	edgeStrings = []string{"", "0", "1", "a", "A b", "true", "é😀", `&<>"'`, "  padded  ", "line\nbreak", "x=y"}
	edgeNumbers = []string{"0", "1", "2", "-1", "0.1", "2.5", "12.34", "1234.56", "1234567.89", "0.005", "100", "9007199254740991", "9007199254740993", "1e21", "-0.01"}
	edgeDates   = []string{"2020-01-31", "2024-02-29", "2019-12-31", "2023-06-15", "", "31/01/2020", "2020-13-01"}
	edgePhones  = []string{"+447700900123", "07700 900123", "020 7946 0958", "+18882378289", "123"}
)

// Class is the edge class a generated context leans towards.
type Class int

const (
	ClassTypical Class = iota // every field present with a typical value
	ClassMissing              // empty object
	ClassNull                 // every leaf null
	ClassEmpty                // "", 0, empty arrays and objects
	ClassStringy              // numbers as strings ("0", "1.50")
	ClassRandom               // a seeded mix of everything
)

// Generate returns n contexts for schema, deterministically from seed.
// The first contexts walk the fixed classes; the rest are ClassRandom.
// pool holds string values to prefer (the skeleton's string literals, so
// select/global/eq comparisons can hit).
func Generate(schema *Node, n int, seed uint64, pool []string) [][]byte {
	sort.Strings(pool)
	out := make([][]byte, 0, n)
	for i := range n {
		cls := ClassRandom
		if i <= int(ClassStringy) {
			cls = Class(i)
		}
		g := &gen{r: rand.New(rand.NewPCG(seed, uint64(i))), cls: cls, pool: pool} //nolint:gosec // reproducible test data, not secrets
		var v any = map[string]any{}
		if cls != ClassMissing {
			v = g.object(schema, 0)
		}
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			panic(err) // only json.Number and plain values are generated
		}
		out = append(out, bytes.TrimRight(b.Bytes(), "\n"))
	}
	return out
}

type gen struct {
	r    *rand.Rand
	cls  Class
	pool []string
}

const maxGenDepth = 12

func (g *gen) object(n *Node, depth int) map[string]any {
	m := map[string]any{}
	for _, name := range n.FieldNames() {
		f := n.Fields[name]
		if g.cls == ClassRandom && g.r.IntN(8) == 0 {
			continue // missing
		}
		m[name] = g.value(f, depth+1)
	}
	return m
}

func (g *gen) value(n *Node, depth int) any {
	if g.cls == ClassNull && (n.Kind&(KObject|KArray) == 0 || depth > 1) {
		return nil
	}
	if g.cls == ClassRandom && g.r.IntN(12) == 0 {
		return nil
	}
	if depth > maxGenDepth {
		return nil
	}
	switch {
	case n.Kind&KArray != 0:
		return g.array(n, depth)
	case n.Kind&(KDate|KNumber|KString) == 0 && (n.Kind&KObject != 0 || len(n.Fields) > 0):
		// A node used both as a helper argument and with fields is an
		// each element whose other names climb to the parent: keep it a
		// scalar.
		if g.cls == ClassEmpty {
			return map[string]any{}
		}
		return g.object(n, depth)
	case n.Kind&KDate != 0:
		return g.pick(edgeDates, n.Strings)
	case n.Kind&KNumber != 0:
		if g.cls == ClassStringy || (g.cls == ClassRandom && g.r.IntN(4) == 0) {
			return g.pick(edgeNumbers, n.Numbers)
		}
		return num(g.pick(edgeNumbers, n.Numbers))
	case n.Kind&KString != 0:
		if g.cls == ClassRandom && g.r.IntN(3) == 0 {
			return g.pick(edgePhones, nil)
		}
		return g.pickStr(edgeStrings, n.Strings)
	case n.Kind&KScalar != 0:
		if len(n.Numbers) > 0 && g.r.IntN(2) == 0 {
			return num(g.pick(edgeNumbers, n.Numbers))
		}
		if g.cls == ClassRandom && g.r.IntN(3) == 0 {
			return num(g.pick(edgeNumbers, nil))
		}
		return g.pickStr(edgeStrings, n.Strings)
	default: // truthy or unknown use: any class of value
		switch g.cls {
		case ClassTypical:
			return true
		case ClassEmpty:
			return ""
		case ClassStringy:
			return "0"
		case ClassMissing, ClassNull, ClassRandom:
		}
		switch g.r.IntN(7) {
		case 0:
			return false
		case 1:
			return json.Number("0")
		case 2:
			return "0"
		case 3:
			return []any{}
		case 4:
			return g.pickStr(edgeStrings, n.Strings)
		case 5:
			return map[string]any{}
		default:
			return true
		}
	}
}

func (g *gen) array(n *Node, depth int) []any {
	var l int
	switch g.cls {
	case ClassEmpty:
		return []any{}
	case ClassTypical:
		l = 2
	case ClassMissing, ClassNull, ClassStringy, ClassRandom:
		l = g.r.IntN(4)
	}
	el := n.Elem
	if el == nil {
		el = &Node{Kind: KString}
	}
	out := make([]any, 0, l)
	for range l {
		out = append(out, g.value(el, depth+1))
	}
	return out
}

// pick prefers the template's own literals for this field, then the
// shared pool, then the edge set. Typical contexts take the first
// preferred value so comparisons in the template hit.
func (g *gen) pick(edge, own []string) string {
	return g.pickFrom(edge, own, false)
}

// pickStr is pick for string values, which may also come from the pool.
func (g *gen) pickStr(edge, own []string) string {
	return g.pickFrom(edge, own, true)
}

func (g *gen) pickFrom(edge, own []string, usePool bool) string {
	if len(own) > 0 && (g.cls == ClassTypical || g.r.IntN(2) == 0) {
		return own[g.r.IntN(len(own))]
	}
	if usePool && len(g.pool) > 0 && g.r.IntN(4) == 0 {
		return g.pool[g.r.IntN(len(g.pool))]
	}
	if g.cls == ClassEmpty {
		return edge[0]
	}
	return edge[g.r.IntN(len(edge))]
}

// num returns s as a JSON number when it is one, else as a string.
func num(s string) any {
	if json.Valid([]byte(s)) {
		return json.Number(s)
	}
	return s
}
