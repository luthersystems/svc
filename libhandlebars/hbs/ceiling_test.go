// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/svc/libhandlebars/hbs"
)

// ceilingCases put each helper and value walk on the slowest input class
// known for it, repeated by an #each over a. Cases with once set end the
// render with an error, so they run once.
//
// steps pins each case's exact charge with 200 items, so a charge that is
// removed or changed fails deterministically, not only by timing.
var ceilingCases = []struct {
	name, tpl string
	ctx       map[string]any
	once      bool
	steps     int64
}{
	{"toFloat subnormal", `{{#each a}}{{gt ../x "1"}}{{/each}}`, map[string]any{"x": "4.9406564584124654e-324"}, false, 110411},
	{"toFloat long halfway", `{{#each a}}{{gt ../x "1"}}{{/each}}`, map[string]any{"x": "1." + strings.Repeat("0", 1060) + "5e-1"}, false, 332811},
	{"plus 50 subnormal keys", `{{#each a}}{{plus ` + plusKeys(50) + `}}{{/each}}`, map[string]any{"x": "5e-324"}, false, 5329674},
	{"times subnormal printed", `{{#each a}}{{times ../den 1}}{{/each}}`, map[string]any{"den": "5e-324"}, false, 114874},
	{"round-to-nth subnormal", `{{#each a}}{{round-to-nth ../x "2"}}{{/each}}`, map[string]any{"x": "1e-320"}, false, 106812},
	{"round-to-nth long zeros n", `{{#each a}}{{round-to-nth "1" ../z}}{{/each}}`, map[string]any{"z": strings.Repeat("0", 1<<14) + "2"}, false, 220812},
	{"to-int long zeros", `{{#each a}}{{to-int ../z}}{{/each}}`, map[string]any{"z": strings.Repeat("0", 1<<16) + "1"}, false, 821611},
	{"prettyp long zeros", `{{#each a}}{{prettyp-num-en ../z}}{{/each}}`, map[string]any{"z": strings.Repeat("0", 1<<16) + "1"}, false, 822412},
	{"prettyp 1e308", `{{#each a}}{{prettyp-num-en ../f}}{{/each}}`, map[string]any{"f": 1e308}, false, 90172},
	{"to-str 1e308", `{{#each a}}{{to-str ../f}}{{/each}}`, map[string]any{"f": 1e308}, false, 37734},
	{"print array of 1e308", `{{#each a}}{{../big}}{{/each}}`, map[string]any{"big": repeatAny(1e308, 100)}, false, 807646},
	{"possessive trailing spaces", `{{#each a}}{{possessive ../sp}}{{/each}}`, map[string]any{"sp": "x" + strings.Repeat(" ", 1<<16)}, false, 873013},
	{"format-phone-gb", `{{#each a}}{{format-phone-gb ../p}}{{/each}}`, map[string]any{"p": "+44 (0)20 7946 0000 ext. 1234567"}, false, 412219},
	{"format-phone-gb 250", `{{#each a}}{{format-phone-gb ../p}}{{/each}}`, map[string]any{"p": strings.Repeat("9", 250)}, false, 415108},
	{"is-after long tail", `{{#each a}}{{is-after ../d ../d}}{{/each}}`, map[string]any{"d": "2020-01-01" + strings.Repeat("\x01", 1<<16)}, false, 105811},
	{"date-diff-month long tail", `{{#each a}}{{date-diff-month ../d ../d}}{{/each}}`, map[string]any{"d": "2020-01-01" + strings.Repeat("x", 1<<16)}, false, 105811},
	{"date-add-months long tail", `{{#each a}}{{date-add-months ../d 1}}{{/each}}`, map[string]any{"d": "2020-01-01" + strings.Repeat("x", 1<<16)}, false, 898614},
	{"date-beautify error", `{{date-beautify d}}`, map[string]any{"d": "2020-01-01" + strings.Repeat("\x01", 1<<16)}, true, 66325},
	{"escape-uri", `{{#each a}}{{escape-uri-component ../s}}{{/each}}`, map[string]any{"s": strings.Repeat("/", 1<<16)}, false, 4226010},
	{"escaped output", `{{#each a}}{{../amp}}{{/each}}`, map[string]any{"amp": strings.Repeat("&", 1<<16)}, false, 4161610},
	{"plain output", `{{#each a}}{{../plain}}{{/each}}`, map[string]any{"plain": strings.Repeat("p", 1<<16)}, false, 833610},
	{"eq long", `{{#each a}}{{eq ../l ../m}}{{/each}}`, map[string]any{"l": strings.Repeat("e", 1<<16) + "1", "m": strings.Repeat("e", 1<<16) + "2"}, false, 105811},
	{"equal long", `{{#each a}}{{#equal ../l ../m}}x{{/equal}}{{/each}}`, map[string]any{"l": strings.Repeat("e", 1<<16) + "1", "m": strings.Repeat("e", 1<<16) + "2"}, false, 105810},
	{"global long key", `{{#each a}}{{global "n" key=../k}}{{/each}}`, map[string]any{"k": strings.Repeat("g", 1<<16)}, false, 54410},
	{"%v error of a large object", `{{prettyp-num-en o}}`, map[string]any{"o": manyKeys(20000, 64)}, true, 361318},
	{"select long", `{{#each a}}{{#select from=../items where=../w}}x{{/select}}{{/each}}`, map[string]any{"w": strings.Repeat("k", 1<<12) + "=v", "items": repeatAny(map[string]any{strings.Repeat("k", 1<<12): "u"}, 20)}, false, 75410},
	{"in-string-array long", `{{#each a}}{{in-string-array haystack=../h needle=../n}}{{/each}}`, map[string]any{"h": repeatAny(strings.Repeat("y", 1<<14)+"1", 16), "n": strings.Repeat("y", 1<<14) + "2"}, false, 228211},
	{"each over object, long keys", `{{#each a}}{{#each ../m}}{{/each}}{{/each}}`, map[string]any{"m": manyKeys(64, 1<<12)}, false, 603810},
	{"str of array", `{{#each a}}{{eq ../arr "x"}}{{/each}}`, map[string]any{"arr": repeatAny(1e308, 100)}, false, 808647},
	{"array index long", `{{#each a}}{{../arr.[` + strings.Repeat("0", 1<<14) + `1]}}{{/each}}`, map[string]any{"arr": []any{1.0, 2.0}}, false, 207011},
}

func plusKeys(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, " k%02d=../x", i)
	}
	return b.String()
}

func repeatAny(v any, n int) []any {
	a := make([]any, n)
	for i := range a {
		a[i] = v
	}
	return a
}

func manyKeys(n, size int) map[string]any {
	m := make(map[string]any, n)
	for i := range n {
		m[fmt.Sprintf("%0*d", size, i)] = 1.0
	}
	return m
}

// ceilingNs is the most a charged step may take on its slowest known
// input, in nanoseconds. The cases measure at most about 76 ns on the
// reference machine (4 vCPU, 2.1 GHz), so the default MaxSteps (2^25) is
// exhausted within about 2.5 s there, a tenth of the peer's 30 s execute
// timeout. The test allows 200 to absorb CI noise and parallel tests.
const ceilingNs = 200

// TestCostCeiling checks that no operation is much slower per charged step
// than the evaluator itself (DETERMINISM.md, "Cost model").
func TestCostCeiling(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing guard: skipped under -race and -short")
	}
	items := repeatAny(1.0, 200)
	worst, worstName := 0.0, ""
	for _, c := range ceilingCases {
		p, err := hbs.Parse(c.tpl, hbs.DefaultLimits())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		ctx := map[string]any{"a": items}
		for k, v := range c.ctx {
			ctx[k] = v
		}
		// Render through JSON values, as handlebars:render does.
		b, err := json.Marshal(ctx)
		if err != nil {
			t.Fatal(err)
		}
		v, err := hbs.FromJSON(b)
		if err != nil {
			t.Fatal(err)
		}
		best, steps := 0.0, int64(0)
		for range 5 {
			m := &stepMeter{}
			start := time.Now()
			_, rerr := p.Render(v, hbs.Options{Meter: m, Limits: hbs.Limits{MaxOutputBytes: 1 << 30}})
			ns := float64(time.Since(start).Nanoseconds()) / float64(m.used)
			if !c.once && rerr != nil {
				t.Fatalf("%s: %v", c.name, rerr)
			}
			if best == 0 || ns < best {
				best, steps = ns, m.used
			}
		}
		t.Logf("%-28s %9d steps %6.0f ns/step", c.name, steps, best)
		if steps != c.steps {
			t.Errorf("%s: %d steps charged, want %d", c.name, steps, c.steps)
		}
		if best > worst {
			worst, worstName = best, c.name
		}
		if best > ceilingNs {
			t.Errorf("%s: %.0f ns per charged step, want at most %d", c.name, best, ceilingNs)
		}
	}
	t.Logf("worst: %s, %.0f ns/step", worstName, worst)

	// Decoding a context: subnormal numbers take ParseFloat's slow path.
	data := []byte(`{"a": [` + strings.TrimSuffix(strings.Repeat("5e-324,", 100_000), ",") + `]}`)
	best := 0.0
	for range 3 {
		m := &stepMeter{}
		start := time.Now()
		if _, err := hbs.FromJSONMetered(data, m); err != nil {
			t.Fatal(err)
		}
		if ns := float64(time.Since(start).Nanoseconds()) / float64(m.used); best == 0 || ns < best {
			best = ns
		}
	}
	t.Logf("%-28s %6.0f ns/step", "FromJSON subnormals", best)
	if best > ceilingNs {
		t.Errorf("FromJSON subnormals: %.0f ns per charged step, want at most %d", best, ceilingNs)
	}
}
