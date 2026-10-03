// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/elps/lisp"
)

// ceilingNs is the most a charged ELPS step may take on the entry points'
// slowest known inputs (see hbs/ceiling_test.go): about 80 ns at worst on
// the reference machine, with margin for CI.
const ceilingNs = 200

// TestBuiltinCostCeiling runs handlebars:must-parse and handlebars:render
// end to end, as substrate calls them, on the inputs that are slowest per
// charged step: tag-dense templates (cache miss and hit), structural JSON
// contexts, distinct number literals and an ELPS context that shares one
// value many times.
func TestBuiltinCostCeiling(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing guard: skipped under -race and -short")
	}
	env := newEnv(t)
	run := 0
	measure := func(name, src string, setup ...func()) {
		t.Helper()
		best := 0.0
		var steps int64
		// Three runs, and up to three more while over the ceiling, so a
		// moment of CPU contention does not fail the case.
		for i := 0; i < 3 || (i < 6 && best > ceilingNs); i++ {
			for _, f := range setup {
				f()
			}
			start := time.Now()
			v, n := eval(t, env, src)
			ns := float64(time.Since(start).Nanoseconds()) / float64(n)
			if v.Type == lisp.LError && !strings.Contains(name, "error") {
				t.Fatalf("%s: %v", name, v)
			}
			if best == 0 || ns < best {
				best, steps = ns, n
			}
		}
		t.Logf("%-34s %9d steps %6.0f ns/step", name, steps, best)
		if best > ceilingNs {
			t.Errorf("%s: %.0f ns per charged step, want at most %d", name, best, ceilingNs)
		}
	}
	put := func(name string, v *lisp.LVal) { env.Put(lisp.Symbol(name), v) }

	for name, unit := range map[string]string{
		"{{a}}": "{{a}}", "{{a 1 2 3 4 5}}": "{{a 1 2 3 4 5}}", "{{#a}}{{/a}}": "{{#a}}{{/a}}", "{{0}}": "{{0}}", "text": "lorem ipsum ",
	} {
		body := strings.Repeat(unit, (1<<20-64)/len(unit))
		// A new prefix per call is a cache miss every time.
		measure("must-parse miss "+name, `(handlebars:must-parse tpl)`, func() {
			run++
			put("tpl", lisp.String(strconv.Itoa(run)+body))
		})
		hit := fmt.Sprintf("hit%d", run)
		put("tplhit", lisp.String(hit+body))
		measure("must-parse hit "+name, `(handlebars:must-parse tplhit)`)
	}

	array := func(elem string, n int) []byte {
		return []byte(`{"a":[` + strings.TrimSuffix(strings.Repeat(elem+",", n), ",") + `]}`)
	}
	distinct := func(n int, f func(i int) string) []byte {
		var b strings.Builder
		b.WriteString(`{"a":[`)
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(f(i))
		}
		b.WriteString(`]}`)
		return []byte(b.String())
	}
	for name, data := range map[string][]byte{
		"objects":              array("{}", 1_000_000),
		"arrays":               array("[]", 1_000_000),
		"empty strings":        array(`""`, 1_000_000),
		"nulls":                array("null", 1_000_000),
		"objects with key":     array(`{"a":[]}`, 400_000),
		"distinct ints":        distinct(400_000, strconv.Itoa),
		"distinct 19 digits":   distinct(200_000, func(i int) string { return fmt.Sprintf("1%018d", i) }),
		"distinct near 1e-306": distinct(100_000, func(i int) string { return fmt.Sprintf("9.%06de-306", i) }),
		"repeated subnormal":   array("5e-324", 100_000),
		"long strings":         array(`"`+strings.Repeat("s", 1000)+`"`, 4000),
	} {
		put("ctx", lisp.Bytes(data))
		measure("render bytes context: "+name, `(handlebars:render "" ctx)`)
	}

	// One value shared a million times: little JSON, much walking.
	inner := lisp.SortedMap()
	row := make([]*lisp.LVal, 1000)
	for i := range row {
		row[i] = inner
	}
	rowv := lisp.QExpr(row)
	rows := make([]*lisp.LVal, 1000)
	for i := range rows {
		rows[i] = rowv
	}
	shared := lisp.SortedMap()
	shared.MapSetString("a", lisp.QExpr(rows))
	put("shared", shared)
	measure("render shared ELPS context", `(handlebars:render "" shared)`)
}
