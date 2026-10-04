// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/luthersystems/svc/libhandlebars/hbs"
	"github.com/stretchr/testify/require"
)

// TestHugeLimits: limits as large as 2^60 or math.MaxInt64 render as
// unbounded ones do; no product or difference derived from them overflows.
func TestHugeLimits(t *testing.T) {
	ctx, err := hbs.FromJSON([]byte(`{"a": [1, 2], "s": "x", "g": {"k": [1.5]}}`))
	require.NoError(t, err)

	for _, tpl := range []string{`x{{#each a}}{{this}}{{/each}}{{s}}{{{g}}}`, `{{prettyp-num-en a}}`, `{{prettyp-num-en g}}`} {
		checkHugeLimits(t, tpl, ctx)
	}
}

func checkHugeLimits(t *testing.T, tpl string, ctx hbs.Value) {
	t.Helper()
	for _, huge := range []int64{1 << 60, math.MaxInt64} {
		for _, lim := range []hbs.Limits{
			{MaxOutputBytes: int(huge)}, {MaxSteps: huge}, {MaxDepth: int(huge)}, {MaxTemplateBytes: int(huge)},
			{MaxOutputBytes: int(huge), MaxSteps: huge, MaxDepth: int(huge), MaxTemplateBytes: int(huge)},
		} {
			p, err := hbs.Parse(tpl, lim)
			require.NoError(t, err, "%s %+v", tpl, lim)
			want, werr := p.Render(ctx, hbs.Options{})
			got, gerr := p.Render(ctx, hbs.Options{Limits: lim, Meter: positiveMeter{}})
			require.Equal(t, werr, gerr, "%+v", lim)
			require.Equal(t, want, got, "%+v", lim)
			// The Go value path sizes against MaxSteps too.
			goCtx := map[string]any{"a": []int{1, 2}, "s": "x", "g": map[string]any{"k": []float64{1.5}}}
			want, werr = p.Render(goCtx, hbs.Options{})
			got, gerr = p.Render(goCtx, hbs.Options{Limits: lim, Meter: positiveMeter{}})
			require.Equal(t, werr, gerr, "%+v", lim)
			require.Equal(t, want, got, "%+v", lim)
		}
	}
}

// positiveMeter refuses a negative charge: an overflowed bound would make one.
type positiveMeter struct{}

func (positiveMeter) Charge(n int64) error {
	if n < 0 {
		return fmt.Errorf("negative charge %d", n)
	}
	return nil
}
