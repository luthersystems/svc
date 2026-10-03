// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
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
	{"toFloat subnormal underscores", `{{#each a}}{{gt ../x "1"}}{{/each}}`, map[string]any{"x": "5e-3_24"}, false, 107011},
	{"toFloat mantissa underscores", `{{#each a}}{{gt ../x "1"}}{{/each}}`, map[string]any{"x": "4_9.4_0e-3_25"}, false, 108211},
	{"escape-uri-component rejected 48 MiB", `{{escape-uri-component x}}`, map[string]any{"x": strings.Repeat("/", 48<<20)}, true, 25657353},
	{"toFloat long halfway", `{{#each a}}{{gt ../x "1"}}{{/each}}`, map[string]any{"x": "1." + strings.Repeat("0", 1060) + "5e-1"}, false, 332811},
	{"plus 50 subnormal keys", `{{#each a}}{{plus ` + plusKeys(50) + `}}{{/each}}`, map[string]any{"x": "5e-324"}, false, 5329674},
	{"times subnormal printed", `{{#each a}}{{times ../den 1}}{{/each}}`, map[string]any{"den": "5e-324"}, false, 114874},
	{"times float64 overflow band", `{{#each a}}{{times ../x 1}}{{/each}}`, map[string]any{"x": "2.8e308"}, false, 107011},
	{"gt float64 overflow band", `{{#each a}}{{gt ../x 1}}{{/each}}`, map[string]any{"x": "9.9e308"}, false, 107012},
	{"round-to-nth float32 underflow", `{{#each a}}{{round-to-nth ../x "2"}}{{/each}}`, map[string]any{"x": "1e-300"}, false, 107012},
	{"round-to-nth float32 overflow", `{{#each a}}{{round-to-nth ../x "2"}}{{/each}}`, map[string]any{"x": "1e300"}, true, 541},
	{"round-to-nth float32 subnormal", `{{#each a}}{{round-to-nth ../x "2"}}{{/each}}`, map[string]any{"x": "1e-40"}, false, 106812},
	{"partial error 512 KiB name", `{{> ` + strings.Repeat("p", 512<<10) + `}}`, nil, true, 66570},
	{"round-to-nth subnormal", `{{#each a}}{{round-to-nth ../x "2"}}{{/each}}`, map[string]any{"x": "1e-320"}, false, 107012},
	{"round-to-nth long zeros n", `{{#each a}}{{round-to-nth "1" ../z}}{{/each}}`, map[string]any{"z": strings.Repeat("0", 1<<14) + "2"}, false, 221012},
	{"to-int long zeros", `{{#each a}}{{to-int ../z}}{{/each}}`, map[string]any{"z": strings.Repeat("0", 1<<16) + "1"}, false, 821611},
	{"prettyp long zeros", `{{#each a}}{{prettyp-num-en ../z}}{{/each}}`, map[string]any{"z": strings.Repeat("0", 1<<16) + "1"}, false, 822412},
	{"prettyp 1e308", `{{#each a}}{{prettyp-num-en ../f}}{{/each}}`, map[string]any{"f": 1e308}, false, 95172},
	{"to-str 1e308", `{{#each a}}{{to-str ../f}}{{/each}}`, map[string]any{"f": 1e308}, false, 41534},
	{"print array of 1e308", `{{#each a}}{{../big}}{{/each}}`, map[string]any{"big": repeatAny(1e308, 100)}, false, 807646},
	{"possessive trailing spaces", `{{#each a}}{{possessive ../sp}}{{/each}}`, map[string]any{"sp": "x" + strings.Repeat(" ", 1<<16)}, false, 873213},
	{"format-phone-gb", `{{#each a}}{{format-phone-gb ../p}}{{/each}}`, map[string]any{"p": "+44 (0)20 7946 0000 ext. 1234567"}, false, 412419},
	{"format-phone-gb 250", `{{#each a}}{{format-phone-gb ../p}}{{/each}}`, map[string]any{"p": strings.Repeat("9", 250)}, false, 418108},
	{"is-after long tail", `{{#each a}}{{is-after ../d ../d}}{{/each}}`, map[string]any{"d": "2020-01-01" + strings.Repeat("\x01", 1<<16)}, false, 105811},
	{"date-diff-month long tail", `{{#each a}}{{date-diff-month ../d ../d}}{{/each}}`, map[string]any{"d": "2020-01-01" + strings.Repeat("x", 1<<16)}, false, 105811},
	{"date-add-months long tail", `{{#each a}}{{date-add-months ../d 1}}{{/each}}`, map[string]any{"d": "2020-01-01" + strings.Repeat("x", 1<<16)}, false, 1717814},
	{"date-beautify error", `{{date-beautify d}}`, map[string]any{"d": "2020-01-01" + strings.Repeat("\x01", 1<<16)}, true, 66325},
	{"escape-uri", `{{#each a}}{{escape-uri-component ../s}}{{/each}}`, map[string]any{"s": strings.Repeat("/", 1<<16)}, false, 6683610},
	{"escaped output", `{{#each a}}{{../amp}}{{/each}}`, map[string]any{"amp": strings.Repeat("&", 1<<16)}, false, 4980810},
	{"plain output", `{{#each a}}{{../plain}}{{/each}}`, map[string]any{"plain": strings.Repeat("p", 1<<16)}, false, 1652810},
	{"eq long", `{{#each a}}{{eq ../l ../m}}{{/each}}`, map[string]any{"l": strings.Repeat("e", 1<<16) + "1", "m": strings.Repeat("e", 1<<16) + "2"}, false, 105811},
	{"equal long", `{{#each a}}{{#equal ../l ../m}}x{{/equal}}{{/each}}`, map[string]any{"l": strings.Repeat("e", 1<<16) + "1", "m": strings.Repeat("e", 1<<16) + "2"}, false, 105810},
	{"global long key", `{{#each a}}{{global "n" key=../k}}{{/each}}`, map[string]any{"k": strings.Repeat("g", 1<<16)}, false, 54410},
	{"%v error of a large object", `{{prettyp-num-en o}}`, map[string]any{"o": manyKeys(20000, 64)}, true, 381318},
	{"select long", `{{#each a}}{{#select from=../items where=../w}}x{{/select}}{{/each}}`, map[string]any{"w": strings.Repeat("k", 1<<12) + "=v", "items": repeatAny(map[string]any{strings.Repeat("k", 1<<12): "u"}, 20)}, false, 75410},
	{"in-string-array long", `{{#each a}}{{in-string-array haystack=../h needle=../n}}{{/each}}`, map[string]any{"h": repeatAny(strings.Repeat("y", 1<<14)+"1", 16), "n": strings.Repeat("y", 1<<14) + "2"}, false, 228211},
	{"each over object, long keys", `{{#each a}}{{#each ../m}}{{/each}}{{/each}}`, map[string]any{"m": manyKeys(64, 1<<12)}, false, 1679010},
	{"str of array", `{{#each a}}{{eq ../arr "x"}}{{/each}}`, map[string]any{"arr": repeatAny(1e308, 100)}, false, 808647},
	{"toFloat implicit exponent", `{{#each a}}{{gt ../x "1"}}{{/each}}`, map[string]any{"x": "0." + strings.Repeat("0", 400) + "1"}, false, 191411},
	{"toFloat malformed suffix", `{{#each a}}{{gt ../x "1"}}{{/each}}`, map[string]any{"x": "4.9406564584124654e-324z"}, false, 110611},
	{"mod exponent reduction", `{{#each a}}{{mod ../big ../small}}{{/each}}`, map[string]any{"big": "1e300", "small": "1e-300"}, false, 61473},
	{"number literal lookup", `{{#each a}}{{#with ../o}}{{1e-320}}{{/with}}{{/each}}`, map[string]any{"o": map[string]any{"x": 1.0}}, false, 11410},
	{"long literal content", `{{#each a}}` + strings.Repeat("c", 512<<10) + `{{/each}}`, nil, false, 6656610},
	{"triple-stash 1 MiB", `{{#each a}}{{{../big}}}{{/each}}`, map[string]any{"big": strings.Repeat("t", 1<<20)}, false, 13313610},
	{"%v error with a 1 MiB key", `{{prettyp-num-en o}}`, map[string]any{"o": map[string]any{strings.Repeat("k", 1<<20): 1.0}}, true, 9229},
	{"data path climb", `{{#each a}}{{#each ../a}}{{@../index}}{{/each}}{{/each}}`, nil, false, 242306},
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
		// Five runs, and up to three more while over the ceiling, so a
		// moment of CPU contention does not fail the case.
		for i := 0; i < 5 || (i < 8 && best > ceilingNs); i++ {
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

	// Whitespace: every pass reads it, so it is charged by length.
	for name, data := range map[string][]byte{
		"FromJSON 1 MiB whitespace":         []byte("{" + strings.Repeat(" ", 1<<20) + "}"),
		"FromJSON 1 MiB whitespace invalid": []byte("{" + strings.Repeat(" ", 1<<20)),
	} {
		best := 0.0
		for range 3 {
			m := &stepMeter{}
			start := time.Now()
			_, _ = hbs.FromJSONMetered(data, m)
			if ns := float64(time.Since(start).Nanoseconds()) / float64(m.used); best == 0 || ns < best {
				best = ns
			}
		}
		t.Logf("%-28s %6.0f ns/step", name, best)
		if best > ceilingNs {
			t.Errorf("%s: %.0f ns per charged step, want at most %d", name, best, ceilingNs)
		}
	}
}

// TestPhoneColdCeiling: format-phone-gb's first calls in a fresh process
// stay within the ceiling, since the package warms phonenumbers (whose
// first call compiles its regular expressions) at init. The measurement
// runs in a subprocess, so nothing earlier in this one has warmed it.
func TestPhoneColdCeiling(t *testing.T) {
	if os.Getenv("HBS_PHONE_COLD") == "1" {
		// One call: the first one in the process.
		p, err := hbs.Parse(`{{format-phone-gb p}}`, hbs.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		v, err := hbs.FromJSON([]byte(`{"p": ` + strconv.Quote(os.Getenv("HBS_PHONE_INPUT")) + `}`))
		if err != nil {
			t.Fatal(err)
		}
		m := &stepMeter{}
		start := time.Now()
		if _, err := p.Render(v, hbs.Options{Meter: m}); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("COLD %d %d\n", time.Since(start).Nanoseconds(), m.used)
		return
	}
	if raceEnabled || testing.Short() {
		t.Skip("timing guard: skipped under -race and -short")
	}
	for _, in := range []string{"07700900123", "+44 20 7946 0958", "01632 960983 x7", "+1 650 253 0000", "+49 30 1234567", "12", "not a number", "+44 7700 900123"} {
		phoneCold(t, in)
	}
}

func phoneCold(t *testing.T, in string) {
	t.Helper()
	best := 0.0
	for range 3 {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPhoneColdCeiling$") //nolint:gosec // this test binary
		cmd.Env = append(os.Environ(), "HBS_PHONE_COLD=1", "HBS_PHONE_INPUT="+in)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		var ns, steps int64
		for _, l := range strings.Split(string(out), "\n") {
			if _, err := fmt.Sscanf(l, "COLD %d %d", &ns, &steps); err == nil {
				break
			}
		}
		if steps == 0 {
			t.Fatalf("no measurement in %q", out)
		}
		if per := float64(ns) / float64(steps); best == 0 || per < best {
			best = per
		}
	}
	t.Logf("format-phone-gb cold %-20q %.0f ns/step", in, best)
	if best > ceilingNs {
		t.Errorf("format-phone-gb cold %q: %.0f ns per charged step, want at most %d", in, best, ceilingNs)
	}
}
