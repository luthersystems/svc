package libhandlebars

import (
	"testing"
)

// TestRoundToNth tests rounding decimals represented as float64s.
func TestRoundToNth(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    string
		out  string
	}{
		{
			"round2-1",
			"1.55806543",
			"2",
			"1.56",
		},
		{
			"round2-2",
			"1.2304",
			"2",
			"1.23",
		},
		{
			"round2-4",
			"1.0001",
			"2",
			"1.00",
		},
		{
			"round2-4",
			"1.999",
			"2",
			"2.00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := roundToNthStrings(tt.in, tt.n)
			if got != tt.out {
				t.Fatalf("unexpected: got [%s] != expected [%s]", got, tt.out)
			}
		})
	}
}
