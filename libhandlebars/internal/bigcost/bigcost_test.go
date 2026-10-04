// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package bigcost

import (
	"encoding"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSteps(t *testing.T) {
	for _, x := range []any{(*big.Int)(nil), (*big.Rat)(nil), (*big.Float)(nil), new(big.Float), big.NewFloat(math.Inf(-1))} {
		n, ok := Steps(x)
		require.True(t, ok, "%T", x)
		require.Equal(t, int64(1), n, "%T", x)
	}
	for _, x := range []any{nil, 1, "s", *big.NewInt(1), big.Float{}} {
		_, ok := Steps(x)
		require.False(t, ok, "%T", x)
	}
	// Larger values cost more, faster than their size.
	prev := int64(0)
	for bits := 64; bits <= 1<<24; bits *= 4 {
		n, _ := Steps(new(big.Int).Lsh(big.NewInt(1), uint(bits)))
		require.Greater(t, n, prev, bits)
		if bits >= 1<<12 {
			require.Greater(t, n, 4*prev, bits)
		}
		prev = n
	}
	// A Float's fraction bits cost quadratically, its integer bits as an
	// Int's (four times).
	small, _ := Steps(new(big.Float).SetMantExp(big.NewFloat(1.5), -(1 << 10)))
	large, _ := Steps(new(big.Float).SetMantExp(big.NewFloat(1.5), -(1 << 12)))
	require.Greater(t, large, 12*small)
	pos, _ := Steps(new(big.Float).SetMantExp(big.NewFloat(1.5), 1<<20))
	require.Equal(t, 4*IntSteps(1<<20+1), pos)
	// The extremes stay within int64.
	maxExp, _ := Steps(new(big.Float).SetPrec(big.MaxPrec).SetMantExp(big.NewFloat(1), big.MinExp))
	require.Positive(t, maxExp)
	maxInt, _ := Steps(new(big.Float).SetMantExp(big.NewFloat(1), big.MaxExp))
	require.Positive(t, maxInt)
}

func TestIsqrt(t *testing.T) {
	for n := range int64(10000) {
		r := isqrt(n)
		require.LessOrEqual(t, r*r, n)
		require.Greater(t, (r+1)*(r+1), n)
	}
	require.Equal(t, int64(1<<31), isqrt(1<<62))
}

type wrapF struct{ *big.Float }

type inner struct{ *big.Int }

type outer struct {
	inner
	N int
}

type byValue struct{ big.Rat }

type floatRat struct {
	*big.Float
	*big.Rat
}

type floatInt struct {
	*big.Float
	*big.Int
}

type mine struct{}

func (mine) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "mineF") }
func (mine) String() string             { return "mineS" }

type shadow struct {
	mine
	*big.Float
}

type deep struct {
	mine
	inner2
}

type inner2 struct{ *big.Float }

type ownAll struct{ *big.Float }

func (ownAll) String() string { return "own" }

type fieldNamed struct {
	*big.Float
	String int
}

type ifaceEmbed struct{ fmt.Stringer }

type selfEmbed struct{ *selfEmbed }

// TestMethodSteps: the method a call reaches is found by Go's selector
// rules, per name: math/big's is charged, a type's own (or one Go does not
// promote: shadowed or ambiguous) is not.
func TestMethodSteps(t *testing.T) {
	f := new(big.Float).SetMantExp(big.NewFloat(1.5), -(1 << 12))
	fs, _ := Steps(f)
	i := new(big.Int).Lsh(big.NewInt(1), 1<<16)
	is, _ := Steps(i)
	r := big.NewRat(1, 3)
	rs, _ := Steps(r)
	var tm encoding.TextMarshaler = f
	for _, c := range []struct {
		x    any
		name string
		want int64
		ok   bool
	}{
		{f, "Format", fs, true}, {wrapF{f}, "Format", fs, true}, {&wrapF{f}, "String", fs, true}, {wrapF{}, "Format", 1, true},
		{outer{inner{i}, 1}, "Format", is, true}, {&outer{inner{i}, 1}, "MarshalJSON", is, true},
		{&byValue{*r}, "String", rs, true}, {byValue{}, "String", 0, false},
		{floatRat{f, r}, "Format", fs, true}, {floatRat{f, r}, "String", 0, false}, // String: ambiguous
		{floatInt{f, i}, "MarshalJSON", is, true}, {floatInt{f, i}, "MarshalText", 0, false},
		{shadow{mine{}, f}, "Format", 0, false}, {shadow{mine{}, f}, "String", 0, false},
		{deep{mine{}, inner2{f}}, "Format", 0, false}, {ownAll{f}, "String", 0, false}, {ownAll{f}, "Format", fs, true},
		{fieldNamed{f, 1}, "String", 0, false}, {fieldNamed{f, 1}, "Format", fs, true},
		{ifaceEmbed{f}, "String", fs + 1, true}, {ifaceEmbed{}, "String", 0, false}, {ifaceEmbed{ifaceEmbed{f}}, "String", fs + 2, true},
		{&selfEmbed{&selfEmbed{}}, "String", 0, false}, {(*wrapF)(nil), "Format", 0, false},
		{struct{ A *big.Int }{i}, "Format", 0, false},
	} {
		n, ok, err := MethodSteps(reflect.ValueOf(c.x), c.name)
		require.NoError(t, err)
		require.Equal(t, c.ok, ok, "%T %s", c.x, c.name)
		require.Equal(t, c.want, n, "%T %s", c.x, c.name)
	}
	// An interface's static type is followed to its dynamic value.
	tms := []encoding.TextMarshaler{tm}
	n, ok, err := MethodSteps(reflect.ValueOf(tms).Index(0), "MarshalText")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, fs+1, n) // a step for the interface
	holder := struct{ T encoding.TextMarshaler }{wrapF{f}}
	n, ok, err = MethodSteps(reflect.ValueOf(holder).Field(0), "MarshalText")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, fs+1, n)
}
