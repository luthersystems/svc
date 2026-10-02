package hbdiff

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Outcome classifies one case.
type Outcome string

const (
	// Equal: the candidate matched the reference exactly.
	Equal Outcome = "equal"
	// Allowlisted: the results differ and an allowlist entry covers it.
	Allowlisted Outcome = "allowlisted"
	// RefNondeterministic: the reference itself gave different results
	// across runs (expected for plus and minus, which sum a hash in Go map
	// order). Reported, not failed.
	RefNondeterministic Outcome = "ref-nondeterministic"
	// RefOnly: no candidate was configured; the reference ran alone.
	RefOnly Outcome = "ref-only"
	// Diff: an unexplained difference, or a nondeterministic candidate.
	Diff Outcome = "DIFF"
)

// Options configure Run.
type Options struct {
	// Runs is how many times each case runs through each engine to check
	// determinism. Zero means DefaultRuns.
	Runs int
	// Candidate is the engine under test. Nil runs the reference only.
	Candidate Candidate
	// Allow waives intended differences. Nil allows none.
	Allow *Allowlist
}

// DefaultRuns is the default determinism repeat count.
const DefaultRuns = 20

// CaseReport is the result of one case.
type CaseReport struct {
	Case      Case
	Outcome   Outcome
	Ref       Result
	RefAlt    []Result // other reference results seen, when nondeterministic
	Cand      Result
	Mismatch  *Mismatch
	AllowedBy *AllowEntry
}

// Report is the result of a run.
type Report struct {
	Cases  []CaseReport
	Counts map[Outcome]int
}

// Failed reports whether any case is a Diff.
func (r *Report) Failed() bool { return r.Counts[Diff] > 0 }

// Run renders every case through the reference and the candidate.
func Run(cases []Case, opts Options) *Report {
	runs := opts.Runs
	if runs <= 0 {
		runs = DefaultRuns
	}
	rep := &Report{Counts: map[Outcome]int{}}
	for _, c := range cases {
		cr := RunCase(c, runs, opts.Candidate, opts.Allow)
		rep.Counts[cr.Outcome]++
		rep.Cases = append(rep.Cases, cr)
	}
	return rep
}

// RunCase runs one case runs times through each engine.
func RunCase(c Case, runs int, cand Candidate, allow *Allowlist) CaseReport {
	cr := CaseReport{Case: c}
	cr.Ref, cr.RefAlt = repeat(runs, func() Result { return Ref(c.Template, c.Context) })
	if cand == nil {
		cr.Outcome = RefOnly
		if len(cr.RefAlt) > 0 {
			cr.Outcome = RefNondeterministic
		}
		return cr
	}
	var candAlt []Result
	cr.Cand, candAlt = repeat(runs, func() Result { return cand(c.Template, c.Context) })
	if len(candAlt) > 0 {
		cr.Outcome = Diff
		cr.Mismatch = &Mismatch{Field: "determinism",
			Detail: fmt.Sprintf("candidate gave %d different results; %s", len(candAlt)+1, Compare(cr.Cand, candAlt[0]))}
		return cr
	}
	if len(cr.RefAlt) > 0 {
		cr.Outcome = RefNondeterministic
		cr.Mismatch = Compare(cr.Ref, cr.Cand)
		return cr
	}
	cr.Mismatch = Compare(cr.Ref, cr.Cand)
	switch {
	case cr.Mismatch == nil:
		cr.Outcome = Equal
	default:
		if e := allow.Match(c, cr.Ref, cr.Cand); e != nil {
			cr.Outcome = Allowlisted
			cr.AllowedBy = e
		} else {
			cr.Outcome = Diff
		}
	}
	return cr
}

// repeat calls f n times and returns the first result and every distinct
// other result.
func repeat(n int, f func() Result) (Result, []Result) {
	first := f()
	var alt []Result
	for i := 1; i < n; i++ {
		r := f()
		if r == first {
			continue
		}
		seen := false
		for _, a := range alt {
			if a == r {
				seen = true
				break
			}
		}
		if !seen {
			alt = append(alt, r)
		}
	}
	return first, alt
}

// WriteSummary prints a one-table summary.
func (r *Report) WriteSummary(w io.Writer) {
	fmt.Fprintf(w, "%-22s %d\n", "cases", len(r.Cases))
	for _, o := range []Outcome{Equal, Allowlisted, RefNondeterministic, RefOnly, Diff} {
		fmt.Fprintf(w, "%-22s %d\n", o, r.Counts[o])
	}
}

// WriteDetails prints every case whose outcome is not Equal or RefOnly.
func (r *Report) WriteDetails(w io.Writer) {
	reps := make([]CaseReport, 0, len(r.Cases))
	for _, cr := range r.Cases {
		if cr.Outcome != Equal && cr.Outcome != RefOnly {
			reps = append(reps, cr)
		}
	}
	sort.SliceStable(reps, func(i, j int) bool { return reps[i].Outcome < reps[j].Outcome })
	for _, cr := range reps {
		fmt.Fprintf(w, "=== %s %s\n", cr.Outcome, cr.Case.Name)
		fmt.Fprintf(w, "template: %q\n", clip(cr.Case.Template, 400))
		fmt.Fprintf(w, "context:  %s\n", clip(string(cr.Case.Context), 400))
		if cr.Mismatch != nil {
			fmt.Fprintf(w, "mismatch: %s\n", cr.Mismatch)
		}
		if cr.AllowedBy != nil {
			fmt.Fprintf(w, "allowed:  %s (%s) %s\n", cr.AllowedBy.Pattern, cr.AllowedBy.Issue, cr.AllowedBy.Reason)
		}
		fmt.Fprintf(w, "ref:      %s\n", describe(cr.Ref))
		for _, a := range cr.RefAlt {
			fmt.Fprintf(w, "ref alt:  %s\n", describe(a))
		}
		if cr.Outcome != RefNondeterministic || cr.Cand != (Result{}) {
			fmt.Fprintf(w, "cand:     %s\n", describe(cr.Cand))
		}
		fmt.Fprintln(w)
	}
}

func describe(r Result) string {
	if r.ErrKind != KindNone {
		return string(r.ErrKind) + " " + strings.ReplaceAll(clip(r.ErrMsg, 300), "\n", `\n`)
	}
	return fmt.Sprintf("%q", clip(r.Out, 300))
}
