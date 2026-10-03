// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbdiff

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	update = flag.Bool("update", false, "regenerate testdata/golden from hbref")
	runs   = flag.Int("runs", DefaultRuns, "determinism repeat count per case and engine")
)

const (
	corpusDir     = "testdata/corpus"
	goldenDir     = "testdata/golden"
	allowlistFile = "testdata/allowed-diffs.txt"
)

func loadCorpus(t testing.TB) (map[string][]Case, []string) {
	t.Helper()
	cases, groups, err := LoadCorpusDir(corpusDir)
	require.NoError(t, err)
	require.NotEmpty(t, groups)
	return cases, groups
}

// TestCorpusGolden checks the reference still produces the committed
// goldens. A failure without a change to hbref means a toolchain or
// dependency bump changed svc's rendering: review it before running
// `go test ./libhandlebars/internal/hbdiff -run TestCorpusGolden -update`.
func TestCorpusGolden(t *testing.T) {
	cases, groups := loadCorpus(t)
	n := min(*runs, 5) // TestCorpusDiff does the full determinism check
	if *update {
		n = max(n, 100)
	}
	total := 0
	for _, g := range groups {
		file := filepath.Join(goldenDir, g+".json")
		if strings.HasSuffix(g, ".arch") {
			// Architecture-dependent reference output: one golden per GOARCH.
			file = filepath.Join(goldenDir, g+"."+runtime.GOARCH+".json")
			if _, err := os.Stat(file); err != nil && !*update {
				t.Logf("%s: no golden for %s; not checked", g, runtime.GOARCH)
				continue
			}
		}
		want, err := ReadGoldens(file)
		require.NoError(t, err)
		got := map[string]Golden{}
		for _, c := range cases[g] {
			_, dup := got[c.Name]
			require.False(t, dup, "duplicate case %s", c.Name)
			ref, alt := repeat(n, func() Result { return Ref(c.Template, c.Context) })
			gold := GoldenOf(ref, alt, false)
			got[c.Name] = gold
			total++
			if *update {
				continue
			}
			w, ok := want[c.Name]
			if !ok {
				t.Errorf("%s: no golden; run with -update", c.Name)
				continue
			}
			if len(alt) > 0 && !w.Nondet {
				t.Errorf("%s: reference is nondeterministic but the golden is not: %s", c.Name, describe(alt[0]))
				continue
			}
			if !w.Matches(ref) {
				t.Errorf("%s: reference drifted from golden\n got: %s\nwant: %+v", c.Name, describe(ref), w)
			}
		}
		if *update {
			require.NoError(t, WriteGoldens(file, got))
			continue
		}
		for name := range want {
			if _, ok := got[name]; !ok {
				t.Errorf("%s: golden for a case that no longer exists; run with -update", name)
			}
		}
	}
	t.Logf("%d corpus cases in %d files", total, len(groups))
}

// TestCorpusDiff compares the candidate engine with the reference on the
// literal corpus.
func TestCorpusDiff(t *testing.T) {
	cases, groups := loadCorpus(t)
	allow := loadAllowlist(t)
	var all []Case
	for _, g := range groups {
		all = append(all, cases[g]...)
	}
	rep := Run(all, Options{Runs: *runs, Candidate: DefaultCandidate, Allow: allow})
	reportT(t, rep)
}

// TestCorpusParseDiff compares must-parse on every distinct corpus
// template.
func TestCorpusParseDiff(t *testing.T) {
	cases, groups := loadCorpus(t)
	allow := loadAllowlist(t)
	seen := map[string]bool{}
	for _, g := range groups {
		for _, c := range cases[g] {
			if seen[c.Template] {
				continue
			}
			seen[c.Template] = true
			ref, cand := RefParse(c.Template), DefaultParseCandidate(c.Template)
			if m := Compare(ref, cand); m != nil && allow.Match(c, ref, cand) == nil {
				t.Errorf("must-parse %s: %s", c.Name, m)
			}
		}
	}
}

func reportT(t *testing.T, rep *Report) {
	t.Helper()
	var sum, det bytes.Buffer
	require.NoError(t, rep.WriteSummary(&sum))
	t.Logf("\n%s", sum.String())
	if rep.Failed() || rep.Counts[RefNondeterministic] > 0 || rep.Counts[Allowlisted] > 0 {
		require.NoError(t, rep.WriteDetails(&det))
		t.Logf("\n%s", det.String())
	}
	if rep.Failed() {
		t.Errorf("%d unexplained differences", rep.Counts[Diff])
	}
}

func loadAllowlist(t *testing.T) *Allowlist {
	t.Helper()
	al, err := LoadAllowlist(allowlistFile, time.Now())
	require.NoError(t, err)
	return al
}

func TestAllowlistFile(t *testing.T) {
	al := loadAllowlist(t)
	require.NotEmpty(t, al.Entries)
	for _, e := range al.Expired() {
		t.Errorf("allowlist line %d expired %s: %s", e.Line, e.Expires, e.Pattern)
	}
}

func TestAllowlistParse(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	al, err := ParseAllowlist([]byte(`
# comment
kinds:panic->render && tpl:@this | never | #1 | panic becomes an error
case:g/old* | 2025-01-01 | #2 | expired
field:out && case:g/* | 2027-01-01 | #3 | out only
`), now)
	require.NoError(t, err)
	require.Len(t, al.Entries, 3)
	require.Len(t, al.Expired(), 1)

	c := Case{Name: "g/x", Template: "{{@this}}"}
	ref := Result{ErrKind: KindPanic, ErrMsg: "p"}
	require.Equal(t, 1, al.Match(c, ref, Result{ErrKind: KindRender, ErrMsg: "r"}).Line-2)
	require.Nil(t, al.Match(Case{Name: "g/old"}, Result{Out: "a"}, Result{ErrKind: KindRender}))
	require.NotNil(t, al.Match(Case{Name: "g/y"}, Result{Out: "a"}, Result{Out: "b"}))
	require.Nil(t, al.Match(Case{Name: "h/y"}, Result{Out: "a"}, Result{Out: "b"}))

	for _, bad := range []string{
		"x | never | #1 | r",
		"case:a | never | | r",
		"case:a | soon | #1 | r",
		"kinds:a | never | #1 | r",
		"case:a | never | #1",
		"arch:! | never | #1 | r",
	} {
		_, err := ParseAllowlist([]byte(bad), now)
		require.Error(t, err, bad)
	}
}

func TestAllowlistArch(t *testing.T) {
	al, err := ParseAllowlist([]byte(`
arch:!amd64 && candout:-9 | never | #1 | not amd64
arch:riscv64 && tpl:r | never | #2 | riscv64 only
`), time.Now())
	require.NoError(t, err)
	defer func(a string) { goarch = a }(goarch)
	c := Case{Name: "g/x", Template: "{{to-int x}}"}
	pinned := Result{Out: "-9223372036854775808"}
	goarch = "amd64"
	require.Nil(t, al.Match(c, Result{Out: "0"}, pinned))
	goarch = "arm64"
	require.Equal(t, "#1", al.Match(c, Result{Out: "0"}, pinned).Issue)
	require.Nil(t, al.Match(c, Result{Out: "0"}, Result{Out: "1"}), "candout must hold")
	goarch = "riscv64"
	require.Equal(t, "#2", al.Match(Case{Template: "r"}, Result{Out: "0"}, Result{Out: "1"}).Issue)

	// The checked-in to-int entry: arm64 saturates +Inf where the engine
	// pins the amd64 result.
	builtin, err := CheckedInAllowlist(time.Now())
	require.NoError(t, err)
	inf := Case{Name: "g/inf", Template: "{{to-int (div 1 0)}}"}
	arm := Result{Out: "9223372036854775807"}
	goarch = "arm64"
	require.NotNil(t, builtin.Match(inf, arm, pinned))
	goarch = "amd64"
	require.Nil(t, builtin.Match(inf, arm, pinned))
}

func TestTxtarRoundTrip(t *testing.T) {
	in := []byte("comment\n-- a.hbs --\n{{x}}\n\n-- a.json --\n{}\n-- b.hbs --\nno newline")
	comment, files := ParseTxtar(in)
	require.Equal(t, "comment\n", string(comment))
	require.Len(t, files, 3)
	require.Equal(t, "{{x}}\n\n", string(files[0].Data))
	require.Equal(t, "no newline\n", string(files[2].Data))
	_, again := ParseTxtar(FormatTxtar(comment, files))
	require.Equal(t, files, again)

	cs, err := CorpusCases("g", in)
	require.NoError(t, err)
	require.Len(t, cs, 2)
	require.Equal(t, "{{x}}\n", cs[0].Template)
	require.Equal(t, "{}", string(cs[1].Context))
}

func TestCompare(t *testing.T) {
	require.Nil(t, Compare(Result{Out: "a"}, Result{Out: "a"}))
	m := Compare(Result{Out: "abc"}, Result{Out: "abd"})
	require.Equal(t, "out", m.Field)
	require.Contains(t, m.Detail, "byte 2")
	require.Equal(t, "kind", Compare(Result{}, Result{ErrKind: KindRender}).Field)
	require.Equal(t, "msg", Compare(Result{ErrKind: KindRender, ErrMsg: "a"}, Result{ErrKind: KindRender, ErrMsg: "b"}).Field)
}

// TestRunReportsNondeterminism checks the runner separates a varying
// reference (plus over three float hash values sums in map order) from a
// varying candidate.
func TestRunReportsNondeterminism(t *testing.T) {
	c := Case{Name: "n", Template: "{{plus a=x b=y c=z}}", Context: []byte(`{"x":0.1,"y":0.2,"z":0.3}`)}
	cr := RunCase(c, 200, func(string, []byte) Result { return Result{Out: "0.6"} }, nil)
	require.Equal(t, RefNondeterministic, cr.Outcome)

	n := 0
	flaky := func(string, []byte) Result { n++; return Result{Out: strings.Repeat("x", n%2)} }
	cr = RunCase(Case{Name: "d", Template: "x", Context: []byte("{}")}, 4, flaky, nil)
	require.Equal(t, Diff, cr.Outcome)
	require.Equal(t, "determinism", cr.Mismatch.Field)
}

// TestRefFatal checks the templates that crash the reference: RefFatal
// must flag each (so no harness renders it through hbref), and the
// candidate must answer each with an error.
func TestRefFatal(t *testing.T) {
	cases, err := LoadCorpusFile("testdata/ref-fatal.txtar")
	require.NoError(t, err)
	require.NotEmpty(t, cases)
	for _, c := range cases {
		require.True(t, RefFatal(c.Template), c.Name)
		r := DefaultCandidate(c.Template, c.Context)
		if r.ErrKind != KindRender && r.ErrKind != KindLimit {
			t.Errorf("%s: candidate gave %s, want a render or limit error", c.Name, describe(r))
		}
	}
	// The corpus itself must hold no fatal template.
	all, groups := loadCorpus(t)
	for _, g := range groups {
		for _, c := range all[g] {
			require.False(t, RefFatal(c.Template), "%s would crash the reference; move it to ref-fatal.txtar", c.Name)
		}
	}
}

// TestRunCaseRefSkipped checks a RefFatal template never reaches the
// reference: the candidate runs alone and is still checked for
// determinism.
func TestRunCaseRefSkipped(t *testing.T) {
	cases, err := LoadCorpusFile("testdata/ref-fatal.txtar")
	require.NoError(t, err)
	c := cases[0]
	cr := RunCase(c, 3, nil, nil)
	require.Equal(t, RefSkipped, cr.Outcome)
	require.Equal(t, Result{}, cr.Ref)

	want := Result{ErrKind: KindLimit, ErrMsg: "limit"}
	cr = RunCase(c, 3, func(string, []byte) Result { return want }, nil)
	require.Equal(t, RefSkipped, cr.Outcome)
	require.Equal(t, want, cr.Cand)
	require.Nil(t, cr.Mismatch)

	n := 0
	cr = RunCase(c, 3, func(string, []byte) Result { n++; return Result{Out: strings.Repeat("x", n%2)} }, nil)
	require.Equal(t, Diff, cr.Outcome)
	require.Equal(t, "determinism", cr.Mismatch.Field)
}

// TestCheckedInAllowlist checks the built-in copy is the file on disk.
func TestCheckedInAllowlist(t *testing.T) {
	now := time.Now()
	built, err := CheckedInAllowlist(now)
	require.NoError(t, err)
	file, err := LoadAllowlist(allowlistFile, now)
	require.NoError(t, err)
	require.Len(t, built.Entries, len(file.Entries))
	for i := range file.Entries {
		require.Equal(t, file.Entries[i].Pattern, built.Entries[i].Pattern)
	}
}
