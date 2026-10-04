// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/svc/libhandlebars/hbs"
)

// timingGuards reports whether the tests that assert wall-clock time per
// charged step run: only with HBS_TIMING=1, as their thresholds depend on
// the machine and its load (go test runs packages in parallel). Without
// it they log what they measured.
func timingGuards() bool { return os.Getenv("HBS_TIMING") == "1" }

// ceilingFails reports whether perNs, a measured time per charged step,
// fails the cost model's ceiling of 200 ns (hbs/DETERMINISM.md): it fails
// past 400 ns always, and past 200 ns unless the plain evaluator, measured
// now, is slow too (within 6x), as when other packages' tests run in
// parallel. Real cost-model gaps are hundreds of times slower per step.
func ceilingFails(t *testing.T, perNs float64) bool {
	t.Helper()
	if !timingGuards() {
		t.Logf("%.0f ns/step (timing guard off: HBS_TIMING=1 enables it)", perNs)
		return false
	}
	switch {
	case perNs <= 200:
		return false
	case perNs > 400:
		return true
	default:
		// The evaluator measured now, capped at 50 ns a step: however
		// loaded the machine, a case past 300 ns a step fails.
		base := min(baselineNs(t), 50)
		t.Logf("%.0f ns/step over the ceiling; plain evaluator now %.0f ns/step (capped)", perNs, base)
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
