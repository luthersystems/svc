// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"math"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// hardwareInt is Go's int(f) on the CPU running the test.
//
//go:noinline
func hardwareInt(f float64) int { return int(f) }

var floatToIntCases = []struct {
	name string
	f    float64
	want int64
}{
	{"NaN", math.NaN(), math.MinInt64},
	{"+Inf", math.Inf(1), math.MinInt64},
	{"-Inf", math.Inf(-1), math.MinInt64},
	{"1e19", 1e19, math.MinInt64},
	{"-1e19", -1e19, math.MinInt64},
	{"2^63", 0x1p63, math.MinInt64},
	{"-2^63", -0x1p63, math.MinInt64},
	{"MaxFloat64", math.MaxFloat64, math.MinInt64},
	{"largest below 2^63", math.Nextafter(0x1p63, 0), 9223372036854774784},
	{"smallest above -2^63", math.Nextafter(-0x1p63, 0), -9223372036854774784},
	{"2^53+2", 0x1p53 + 2, 9007199254740994},
	{"0", 0, 0},
	{"-0", math.Copysign(0, -1), 0},
	{"0.5", 0.5, 0},
	{"-0.5", -0.5, 0},
	{"3.9", 3.9, 3},
	{"-3.9", -3.9, -3},
	{"42", 42, 42},
	{"-1e18", -1e18, -1000000000000000000},
	{"SmallestNonzero", math.SmallestNonzeroFloat64, 0},
}

func TestFloatToInt(t *testing.T) {
	if math.MaxInt != math.MaxInt64 {
		t.Skip("int is not 64 bits")
	}
	for _, c := range floatToIntCases {
		got := floatToInt(c.f)
		assert.Equal(t, c.want, int64(got), c.name)
		if runtime.GOARCH == "amd64" {
			// The pinned values are the amd64 hardware's.
			assert.Equal(t, hardwareInt(c.f), got, "amd64 hardware: %s", c.name)
		}
	}
}

// TestFloatToIntMatchesAMD64 compares floatToInt with the hardware over many
// bit patterns, where the hardware is amd64.
func TestFloatToIntMatchesAMD64(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("the hardware conversion differs from the pinned one off amd64")
	}
	var x uint64 = 0x9e3779b97f4a7c15
	for range 1 << 16 {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		f := math.Float64frombits(x)
		if got, want := floatToInt(f), hardwareInt(f); got != want {
			t.Fatalf("floatToInt(%v) = %d; amd64 gives %d", f, got, want)
		}
	}
}

func FuzzFloatToInt(f *testing.F) {
	for _, c := range floatToIntCases {
		f.Add(c.f)
	}
	f.Fuzz(func(t *testing.T, x float64) {
		if math.MaxInt != math.MaxInt64 {
			t.Skip("int is not 64 bits")
		}
		got := floatToInt(x)
		if runtime.GOARCH == "amd64" {
			if want := hardwareInt(x); got != want {
				t.Fatalf("floatToInt(%v) = %d; amd64 gives %d", x, got, want)
			}
		}
		if x >= -0x1p63 && x < 0x1p63 {
			if want := int(int64(x)); got != want {
				t.Fatalf("floatToInt(%v) = %d; in range, want %d", x, got, want)
			}
		} else if int64(got) != math.MinInt64 {
			t.Fatalf("floatToInt(%v) = %d; out of range, want MinInt64", x, got)
		}
	})
}
