// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package bigcost

import (
	"math"
	"math/big"
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
