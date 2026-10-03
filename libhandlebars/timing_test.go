// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/svc/libhandlebars/hbs"
)

// ceilingFails reports whether perNs, a measured time per charged step,
// fails the cost model's ceiling of 200 ns (hbs/DETERMINISM.md): it fails
// past 400 ns always, and past 200 ns unless the plain evaluator, measured
// now, is slow too (within 6x), as when other packages' tests run in
// parallel. Real cost-model gaps are hundreds of times slower per step.
func ceilingFails(t *testing.T, perNs float64) bool {
	t.Helper()
	switch {
	case perNs <= 200:
		return false
	case perNs > 400:
		return true
	default:
		base := baselineNs(t)
		t.Logf("%.0f ns/step over the ceiling; plain evaluator now %.0f ns/step", perNs, base)
		return perNs > 6*base
	}
}

// baselineNs is the plain evaluator's time per step now, best of five.
func baselineNs(t *testing.T) float64 {
	t.Helper()
	p, err := hbs.Parse(`{{#each a}}{{x}}{{/each}}`, hbs.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	v, err := hbs.FromJSON([]byte(`{"x": "abc", "a": [` + strings.TrimSuffix(strings.Repeat("1,", 20000), ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	best := 0.0
	for range 5 {
		m := &countMeter{}
		start := time.Now()
		if _, err := p.Render(v, hbs.Options{Meter: m}); err != nil {
			t.Fatal(err)
		}
		if ns := float64(time.Since(start).Nanoseconds()) / float64(m.n); best == 0 || ns < best {
			best = ns
		}
	}
	return best
}
