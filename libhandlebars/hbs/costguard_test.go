// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/svc/libhandlebars/hbs"
)

type stepMeter struct{ used int64 }

func (m *stepMeter) Charge(n int64) error { m.used += n; return nil }

// guardRun renders 300 templates from the grammar generator with every key,
// string and path padded to pad bytes (sharing a prefix, so hashing,
// comparing and sorting them is slow), inside an #each that repeats the
// work. It returns the total steps charged, nanoseconds and bytes allocated.
func guardRun(t *testing.T, pad int) (int64, int64, uint64) {
	t.Helper()
	long := func(s string) string { return strings.Repeat("q", pad) + s }
	mapAll := func(xs []string, f func(string) string) []string {
		out := make([]string, len(xs))
		for i, x := range xs {
			out[i] = f(x)
		}
		return out
	}
	saved := [][]string{genKeys, genHashKeys, genStrings, genPaths}
	defer func() { genKeys, genHashKeys, genStrings, genPaths = saved[0], saved[1], saved[2], saved[3] }()
	genKeys = mapAll(saved[0], long)
	genHashKeys = append(append([]string{}, saved[1]...), mapAll(saved[1], long)...)
	genStrings = append(append([]string{}, saved[2]...), mapAll(saved[2], long)...)
	genPaths = append(append(mapAll(saved[0], long), mapAll(saved[0], func(k string) string { return "[" + long(k) + "]" })...),
		"this", ".", "@index", "@key", "../"+long("name"), long("sub")+"."+long("name"), "items.[0]")

	var big strings.Builder
	big.WriteString(`[`)
	for i := range 64 {
		if i > 0 {
			big.WriteByte(',')
		}
		fmt.Fprint(&big, i)
	}
	big.WriteString(`]`)

	var steps, ns int64
	var alloc uint64
	for seed := range uint64(300) {
		g := newGen(seed, 11)
		tpl := "{{#each big}}" + g.template() + "{{/each}}"
		ctx := g.context()
		ctx = ctx[:len(ctx)-1] + `,"big":` + big.String() + `}`
		p, err := hbs.Parse(tpl, hbs.Limits{MaxTemplateBytes: 64 << 20})
		if err != nil {
			continue
		}
		v, err := hbs.FromJSON([]byte(ctx))
		if err != nil {
			continue
		}
		m := &stepMeter{}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		_, _ = p.Render(v, hbs.Options{Meter: m, Limits: hbs.Limits{MaxOutputBytes: 1 << 30, MaxSteps: 1 << 40}})
		ns += time.Since(start).Nanoseconds()
		runtime.ReadMemStats(&after)
		steps += m.used
		got := after.TotalAlloc - before.TotalAlloc
		alloc += got
		if limit := allocBound(m.used, len(tpl)+len(ctx)); got > limit {
			t.Errorf("seed %d (pad %d): %d bytes allocated for %d steps, want at most %d: something allocates before it is charged", seed, pad, got, m.used, limit)
		}
	}
	return steps, ns, alloc
}

// allocBound is the most a render may allocate: 8 bytes for each byte of
// its charged work (a step stands for at most about a KiB) and of its input,
// plus 1 MiB of fixed overhead.
func allocBound(steps int64, input int) uint64 {
	return 8*(uint64(steps)*1024+uint64(input)) + 1<<20 //nolint:gosec // steps and input are non-negative
}

// TestCostModelGuard checks that the time and memory a render takes per
// charged step do not grow with the length of the strings involved: it
// renders the same generated templates with 64-byte and with 128 KiB keys,
// strings and paths. An operation whose work grows with a string's length
// but is not charged makes the long run's time per step grow with the
// length, and fails here.
func TestCostModelGuard(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing guard: skipped under -race and -short")
	}
	sSteps, sNs, sAlloc := guardRun(t, 64)
	lSteps, lNs, lAlloc := guardRun(t, 128<<10)
	small, large := float64(sNs)/float64(sSteps), float64(lNs)/float64(lSteps)
	t.Logf("64 B: %d steps, %.0f ns/step, %.0f B/step; 128 KiB: %d steps, %.0f ns/step, %.0f B/step",
		sSteps, small, float64(sAlloc)/float64(sSteps), lSteps, large, float64(lAlloc)/float64(lSteps))
	if large > 4*small+500 {
		t.Errorf("%.0f ns per step with long strings, %.0f with short ones: work that grows with string length is not charged", large, small)
	}
	if per := float64(lAlloc) / float64(lSteps); per > 4096 {
		t.Errorf("%.0f bytes allocated per step with long strings, want at most 4096", per)
	}
}

// costSites isolate one operation whose work grows with a string's length
// each, repeated by an #each over a. Writing output is not here: it is
// charged per KiB written and bounded by MaxOutputBytes, and timing it at
// these sizes measures the memory system as much as the engine. build returns the template and context
// for strings of n bytes.
var costSites = []struct {
	name  string
	build func(n int) (tpl, ctx string)
	// factor is how much slower per step the long run may be. 3 by
	// default; sites whose charged work is copying bytes (a step per KiB
	// copied, like output) get 8, since a copied KiB takes several times
	// as long as a typical step and long strings leave the CPU caches. An
	// uncharged site is about 256x slower per step.
	factor float64
}{
	{"each over object keys", func(n int) (string, string) {
		k := strings.Repeat("k", n)
		var m strings.Builder
		for i := range 64 {
			fmt.Fprintf(&m, `,"%s%02d": %d`, k, i, i)
		}
		return `{{#each a}}{{#each ../m}}{{/each}}{{/each}}`, `{"m": {` + m.String()[1:] + `}`
	}, 0},
	{"global read", func(n int) (string, string) {
		return `{{#each a}}{{global "n" key=../k}}{{/each}}`, `{"k": "` + strings.Repeat("z", n) + `"`
	}, 0},
	{"global write", func(n int) (string, string) {
		return `{{#each a}}{{global "n" key=../k val="v"}}{{/each}}`, `{"k": "` + strings.Repeat("z", n) + `"`
	}, 0},
	{"hash pair key", func(n int) (string, string) {
		return `{{#each a}}{{and ` + strings.Repeat("h", n) + `=1}}{{/each}}`, `{`
	}, 0},
	{"string literal path", func(n int) (string, string) {
		return `{{#each a}}{{#with ../o}}{{"` + strings.Repeat("s", n) + `"}}{{/with}}{{/each}}`, `{"o": {"x": 1}`
	}, 0},
	{"helper name", func(n int) (string, string) {
		return `{{#each a}}{{` + strings.Repeat("q", n) + ` 1}}{{/each}}`, `{`
	}, 0},
	{"path key", func(n int) (string, string) {
		k := strings.Repeat("p", n)
		return `{{#each a}}{{../[` + k + `]}}{{/each}}`, `{"` + k + `": 1`
	}, 0},
	{"array index", func(n int) (string, string) {
		return `{{#each a}}{{../arr.[` + strings.Repeat("0", n) + `1]}}{{/each}}`, `{"arr": [1, 2]`
	}, 0},
	{"block param scan", func(n int) (string, string) {
		var b strings.Builder
		for i := range 2 {
			fmt.Fprintf(&b, `{{#each o as |%s%d|}}`, strings.Repeat("b", n), i)
		}
		b.WriteString(`{{#each a}}{{` + strings.Repeat("b", n) + `0}}{{/each}}`)
		b.WriteString(strings.Repeat(`{{/each}}`, 2))
		return b.String(), `{"o": [1]`
	}, 0},
	{"select key", func(n int) (string, string) {
		k := strings.Repeat("w", n)
		return `{{#each a}}{{#select from=../items where="` + k + `=v"}}x{{/select}}{{/each}}`, `{"items": [{"` + k + `": "u"}, {"` + k + `": "v"}]`
	}, 0},
	{"in-string-array compare", func(n int) (string, string) {
		s := strings.Repeat("y", n)
		var h strings.Builder
		for i := range 16 {
			fmt.Fprintf(&h, `,"%s%c"`, s, 'a'+i)
		}
		return `{{#each a}}{{in-string-array haystack=../h needle=../n}}{{/each}}`, `{"h": [` + h.String()[1:] + `], "n": "` + s + `z"`
	}, 0},
	{"eq compare", func(n int) (string, string) {
		s := strings.Repeat("e", n)
		return `{{#each a}}{{eq ../x ../y}}{{/each}}`, `{"x": "` + s + `1", "y": "` + s + `2"`
	}, 0},
	{"array stringified", func(n int) (string, string) {
		return `{{#each a}}{{eq ../arr "x"}}{{/each}}`, `{"arr": ["` + strings.Repeat("l", n) + `", "` + strings.Repeat("m", n) + `"]`
	}, 8},
	{"sorted hash keys", func(n int) (string, string) {
		k := strings.Repeat("s", n)
		return `{{#each a}}{{plus ` + k + `1=1 ` + k + `2=2 ` + k + `3=3}}{{/each}}`, `{`
	}, 0},
}

// TestCostModelSites checks each operation in costSites: its time per
// charged step with 256 KiB strings must stay within the site's factor
// (plus 100 ns of noise) of that with 1 KiB strings. An uncharged operation
// is about 256x slower per step instead.
func TestCostModelSites(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing guard: skipped under -race and -short")
	}
	items := `"a": [` + strings.TrimSuffix(strings.Repeat("1,", 64), ",") + `]}`
	perStep := func(tpl, ctx string) (float64, int64) {
		p, err := hbs.Parse(tpl, hbs.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		v, err := hbs.FromJSON([]byte(ctx))
		if err != nil {
			t.Fatal(err)
		}
		best := 0.0
		var steps int64
		for range 3 {
			m := &stepMeter{}
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			_, _ = p.Render(v, hbs.Options{Meter: m, Limits: hbs.Limits{MaxOutputBytes: 1 << 30}})
			per := float64(time.Since(start).Nanoseconds()) / float64(m.used)
			runtime.ReadMemStats(&after)
			if got, limit := after.TotalAlloc-before.TotalAlloc, allocBound(m.used, len(tpl)+len(ctx)); got > limit {
				t.Errorf("%d bytes allocated for %d steps, want at most %d: something allocates before it is charged", got, m.used, limit)
			}
			if best == 0 || per < best {
				best = per
			}
			steps = m.used
		}
		return best, steps
	}
	for _, site := range costSites {
		join := func(tpl, ctx string) (string, string) {
			if !strings.HasSuffix(ctx, "{") {
				ctx += ","
			}
			return tpl, ctx + items
		}
		small, sSteps := perStep(join(site.build(1 << 10)))
		large, lSteps := perStep(join(site.build(256 << 10)))
		t.Logf("%-24s 1 KiB: %6d steps %5.0f ns/step; 256 KiB: %8d steps %5.0f ns/step", site.name, sSteps, small, lSteps, large)
		factor := site.factor
		if factor == 0 {
			factor = 3
		}
		if large > factor*small+100 {
			t.Errorf("%s: %.0f ns per step with 256 KiB strings, %.0f with 1 KiB: its work is not charged by length", site.name, large, small)
		}
	}
}
