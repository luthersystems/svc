package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/svc/libhandlebars/internal/hbdiff"
)

// secret marks private text. It appears in every synthetic template and
// context and must never reach stdout or stderr.
const secret = "PRIVATEWORD"

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
}

// fixture builds a synthetic phylum and context directory under a temp
// directory, which is outside any svc work tree.
type fixture struct {
	root, phylum, ctx, out string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{
		root:   root,
		phylum: filepath.Join(root, "phylum"),
		ctx:    filepath.Join(root, "ctx"),
		out:    filepath.Join(root, "out"),
	}
	writeFile(t, filepath.Join(f.phylum, "templates", "letter.html"),
		"<p>"+secret+" {{name}}</p>{{#each items}}<li>{{this}}</li>{{/each}}")
	writeFile(t, filepath.Join(f.phylum, "templates", "change.html"), "DIFFME "+secret+" {{name}}")
	writeFile(t, filepath.Join(f.phylum, "templates", "broken.html"), secret+" {{#if}}")
	writeFile(t, filepath.Join(f.phylum, "src", "labels.lisp"),
		`(defun label (x) (handlebars:render "`+secret+` {{to-str n}}" x))`+"\n")
	writeFile(t, filepath.Join(f.phylum, "src", "labels_test.lisp"), `(assert-string= "{{ignored}}" "x")`+"\n")
	writeFile(t, filepath.Join(f.ctx, "a.json"), `{"name":"`+secret+`<&>","items":["x",1],"n":3}`)
	writeFile(t, filepath.Join(f.ctx, "more.jsonl"), `{"name":"b"}`+"\n\n"+`[1]`+"\n")
	writeFile(t, filepath.Join(f.ctx, "README.md"), "ignored")
	writeFile(t, filepath.Join(f.ctx, "templates_letter.html", "only.json"), `{"items":[]}`)
	return f
}

// diffCand is the reference itself, except that templates containing
// DIFFME render differently.
func diffCand(tpl string, ctx []byte) hbdiff.Result {
	r := hbdiff.Ref(tpl, ctx)
	if strings.Contains(tpl, "DIFFME") && r.ErrKind == hbdiff.KindNone {
		r.Out += "!"
	}
	return r
}

func runT(t *testing.T, cand hbdiff.Candidate, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, cand)
	assert.NotContains(t, stdout.String(), secret, "stdout leaked private text")
	assert.NotContains(t, stderr.String(), secret, "stderr leaked private text")
	return code, stdout.String(), stderr.String()
}

// tree lists every path under dir.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, rel)
		return nil
	}))
	return out
}

func readCases(t *testing.T, out string) map[string]caseLine {
	t.Helper()
	f, err := os.Open(filepath.Join(out, "cases.jsonl")) //nolint:gosec // test temp dir
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	m := map[string]caseLine{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var cl caseLine
		require.NoError(t, json.Unmarshal(sc.Bytes(), &cl))
		m[cl.Case] = cl
	}
	require.NoError(t, sc.Err())
	return m
}

// rows returns the fields of every summary line labelled label: the
// outcome table first, then the differing-field table.
func rows(t *testing.T, summary, label string) [][]string {
	t.Helper()
	var out [][]string
	for _, l := range strings.Split(summary, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, label+"  ") {
			out = append(out, strings.Fields(strings.TrimPrefix(l, label)))
		}
	}
	require.NotEmpty(t, out, "no %q row in summary:\n%s", label, summary)
	return out
}

// row returns the first summary line labelled label.
func row(t *testing.T, summary, label string) []string {
	t.Helper()
	return rows(t, summary, label)[0]
}

func TestPrivateRun(t *testing.T) {
	f := newFixture(t)
	before := tree(t, f.root)
	code, stdout, stderr := runT(t, diffCand, "-out", f.out, "-phylum", f.phylum, "-ctx", f.ctx, "-runs", "2", "-allow", "none")
	require.Equal(t, exitDiff, code, stderr)
	require.Contains(t, stderr, "unexplained differences")

	// 4 templates (the _test.lisp literal is skipped); letter has 4
	// contexts (3 shared, 1 own), the others 3 each.
	require.Contains(t, stdout, "hbdiff: 4 templates, 13 cases, 2 runs per engine")
	assert.Equal(t, []string{"11"}, row(t, stdout, "identical"))
	assert.Equal(t, []string{"2"}, row(t, stdout, "diff"))
	assert.Equal(t, []string{"0"}, row(t, stdout, "allowed diff"))
	assert.Equal(t, []string{"13"}, row(t, stdout, "total"))
	// unmarshal, parse, render, panic, limit, total. The [1] context
	// fails to unmarshal for every template; broken.html fails to parse
	// with the other two.
	assert.Equal(t, []string{"4", "2", "0", "0", "0", "6"}, row(t, stdout, "reference"))
	assert.Equal(t, []string{"4", "2", "0", "0", "0", "6"}, row(t, stdout, "candidate"))
	// kind, msg, out, determinism
	assert.Equal(t, []string{"0", "0", "2", "0"}, rows(t, stdout, "diff")[1])

	// Only -out is new.
	after := tree(t, f.root)
	var added []string
	for _, p := range after {
		if !strings.HasPrefix(p, "out") {
			added = append(added, p)
		}
	}
	require.ElementsMatch(t, before, added)

	sum, err := os.ReadFile(filepath.Join(f.out, "summary.txt"))
	require.NoError(t, err)
	require.Equal(t, stdout, string(sum))
	idx, err := os.ReadFile(filepath.Join(f.out, "templates.tsv"))
	require.NoError(t, err)
	require.Contains(t, string(idx), "templates/letter.html\ttemplates_letter.html\t4\n")
	det, err := os.ReadFile(filepath.Join(f.out, "details.txt"))
	require.NoError(t, err)
	require.Contains(t, string(det), "DIFF templates/change.html#a.json")

	cases := readCases(t, f.out)
	require.Len(t, cases, 13)
	cl := cases["templates/change.html#a.json"]
	require.Equal(t, "DIFF", cl.Outcome)
	require.Equal(t, "out", cl.Field)
	require.NotEmpty(t, cl.Files)
	ref, err := os.ReadFile(filepath.Join(f.out, cl.Files, "ref.out"))
	require.NoError(t, err)
	cand, err := os.ReadFile(filepath.Join(f.out, cl.Files, "cand.out"))
	require.NoError(t, err)
	require.Equal(t, string(ref)+"!", string(cand))
	tpl, err := os.ReadFile(filepath.Join(f.out, cl.Files, "template.hbs"))
	require.NoError(t, err)
	require.Equal(t, "DIFFME "+secret+" {{name}}", string(tpl))

	require.Equal(t, "unmarshal", cases["src/labels.lisp:1#more.jsonl:3"].RefKind)
	require.Equal(t, "equal", cases["templates/letter.html#templates_letter.html/only.json"].Outcome)
	require.Empty(t, cases["templates/letter.html#a.json"].Files, "identical cases get no files")
}

func TestAllowlistWaives(t *testing.T) {
	f := newFixture(t)
	allow := filepath.Join(f.root, "allow.txt")
	writeFile(t, allow, "case:templates/change.html#* && field:out | never | #0 | test\n")
	code, stdout, stderr := runT(t, diffCand, "-out", f.out, "-phylum", f.phylum, "-ctx", f.ctx, "-runs", "1", "-allow", allow)
	require.Equal(t, exitOK, code, stderr)
	assert.Equal(t, []string{"2"}, row(t, stdout, "allowed diff"))
	assert.Equal(t, []string{"0", "0", "2", "0"}, rows(t, stdout, "allowed diff")[1])
	require.Contains(t, stdout, "allowlist: -allow file (1 entries)")
	require.Equal(t, 1, readCases(t, f.out)["templates/change.html#a.json"].AllowLine)
}

func TestBuiltInAllowlist(t *testing.T) {
	f := newFixture(t)
	// {{@this}} panics in the reference; a render error from the candidate
	// is waived by the checked-in allowlist (kinds:panic->render).
	tplFile := filepath.Join(f.root, "this.hbs")
	writeFile(t, tplFile, secret+"{{@this}}")
	cand := func(tpl string, ctx []byte) hbdiff.Result {
		if strings.Contains(tpl, "@this") {
			return hbdiff.Result{ErrKind: hbdiff.KindRender, ErrMsg: "error while rendering template: @this"}
		}
		return hbdiff.Ref(tpl, ctx)
	}
	code, stdout, stderr := runT(t, cand, "-out", f.out, "-runs", "1", tplFile)
	require.Equal(t, exitOK, code, stderr)
	require.Contains(t, stdout, "allowlist: built in (")
	assert.Equal(t, []string{"1"}, row(t, stdout, "allowed diff"))
	assert.Equal(t, []string{"0", "0", "0", "1", "0", "1"}, row(t, stdout, "reference"))
	assert.Equal(t, []string{"0", "0", "1", "0", "0", "1"}, row(t, stdout, "candidate"))
}

func TestRefFatalSkipped(t *testing.T) {
	f := newFixture(t)
	tplFile := filepath.Join(f.root, "deep.hbs")
	writeFile(t, tplFile, secret+strings.Repeat("{{#if a}}", 1100)+strings.Repeat("{{/if}}", 1100)) // past hbdiff.maxRefDepth
	called := 0
	cand := func(string, []byte) hbdiff.Result {
		called++
		return hbdiff.Result{ErrKind: hbdiff.KindLimit, ErrMsg: "error parsing template: too deep"}
	}
	code, stdout, stderr := runT(t, cand, "-out", f.out, "-runs", "2", tplFile)
	require.Equal(t, exitOK, code, stderr)
	require.Equal(t, 2, called)
	assert.Equal(t, []string{"1"}, row(t, stdout, "ref skipped (fatal)"))
	assert.Equal(t, []string{"0", "0", "0", "0", "0", "0"}, row(t, stdout, "reference"))
	assert.Equal(t, []string{"0", "0", "0", "0", "1", "1"}, row(t, stdout, "candidate"))
	cl := readCases(t, f.out)[tplFile+"#{}"]
	require.Equal(t, "ref-skipped", cl.Outcome)
	_, err := os.Stat(filepath.Join(f.out, cl.Files, "ref.out"))
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = os.Stat(filepath.Join(f.out, cl.Files, "cand.err"))
	require.NoError(t, err)
}

func TestNondeterministicCandidate(t *testing.T) {
	f := newFixture(t)
	n := 0
	cand := func(tpl string, ctx []byte) hbdiff.Result {
		n++
		r := hbdiff.Ref(tpl, ctx)
		r.Out += strings.Repeat("x", n%2)
		return r
	}
	tplFile := filepath.Join(f.root, "t.hbs")
	writeFile(t, tplFile, "{{a}}")
	code, stdout, _ := runT(t, cand, "-out", f.out, "-runs", "3", "-allow", "none", tplFile)
	require.Equal(t, exitDiff, code)
	assert.Equal(t, []string{"0", "0", "0", "1"}, rows(t, stdout, "diff")[1])
	require.Equal(t, "determinism", readCases(t, f.out)[tplFile+"#{}"].Field)
}

func TestCasesLayout(t *testing.T) {
	f := newFixture(t)
	cases := filepath.Join(f.root, "cases")
	writeFile(t, filepath.Join(cases, "one", "template.hbs"), secret+"{{a}}")
	writeFile(t, filepath.Join(cases, "one", "ctx", "00.json"), `{"a":1}`)
	writeFile(t, filepath.Join(cases, "one", "ctx", "01.json"), `{"a":"`+secret+`"}`)
	code, stdout, stderr := runT(t, hbdiff.Ref, "-out", f.out, "-cases", cases, "-runs", "1")
	require.Equal(t, exitOK, code, stderr)
	require.Contains(t, stdout, "hbdiff: 1 templates, 2 cases")
	assert.Equal(t, []string{"2"}, row(t, stdout, "identical"))
}

func TestRefOnly(t *testing.T) {
	f := newFixture(t)
	code, stdout, stderr := runT(t, hbdiff.DefaultCandidate, "-out", f.out, "-phylum", f.phylum, "-ref-only", "-runs", "1")
	require.Equal(t, exitOK, code, stderr)
	require.Contains(t, stdout, "candidate: none (reference only)")
	assert.Equal(t, []string{"4"}, row(t, stdout, "ref only"))
	require.NotContains(t, stdout, "differing field")
}

// fakeSvc makes dir look like an svc work tree root.
func fakeSvc(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o700))
	writeFile(t, filepath.Join(dir, "go.mod"), "// x\nmodule "+svcModule+"\n\ngo 1.26.0\n")
}

func TestRefusesPaths(t *testing.T) {
	f := newFixture(t)
	svc := filepath.Join(f.root, "svc")
	fakeSvc(t, svc)
	inSvc := filepath.Join(svc, "libhandlebars", "private")
	writeFile(t, filepath.Join(inSvc, "a.html"), secret+"{{a}}")
	// A non-svc repository nested inside svc is still inside svc.
	nested := filepath.Join(svc, "vendor", "other")
	require.NoError(t, os.MkdirAll(filepath.Join(nested, ".git"), 0o700))
	writeFile(t, filepath.Join(nested, "go.mod"), "module example.com/other\n")
	writeFile(t, filepath.Join(nested, "t.html"), secret)
	link := filepath.Join(f.root, "link")
	require.NoError(t, os.Symlink(inSvc, link))
	full := filepath.Join(f.root, "full")
	writeFile(t, filepath.Join(full, "x"), "")
	wd, err := os.Getwd()
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"phylum in svc", []string{"-out", f.out, "-phylum", inSvc}, "inside the svc work tree"},
		{"phylum nested repo", []string{"-out", f.out, "-phylum", nested}, "inside the svc work tree"},
		{"phylum via symlink", []string{"-out", f.out, "-phylum", link}, "inside the svc work tree"},
		{"template in svc", []string{"-out", f.out, filepath.Join(inSvc, "a.html")}, "inside the svc work tree"},
		{"ctx in svc", []string{"-out", f.out, "-phylum", f.phylum, "-ctx", inSvc}, "inside the svc work tree"},
		{"cases in svc", []string{"-out", f.out, "-cases", inSvc}, "inside the svc work tree"},
		{"this checkout", []string{"-out", f.out, "-phylum", wd}, "inside the svc work tree"},
		{"out in svc", []string{"-out", filepath.Join(svc, "results"), "-phylum", f.phylum}, "inside the svc work tree"},
		{"out in this checkout", []string{"-out", filepath.Join(wd, "results"), "-phylum", f.phylum}, "inside the svc work tree"},
		{"out via symlink", []string{"-out", filepath.Join(link, "results"), "-phylum", f.phylum}, "inside the svc work tree"},
		{"out inside phylum", []string{"-out", filepath.Join(f.phylum, "results"), "-phylum", f.phylum}, "inside -phylum"},
		{"out inside ctx", []string{"-out", f.ctx, "-phylum", f.phylum, "-ctx", f.ctx}, "inside -ctx"},
		{"out not empty", []string{"-out", full, "-phylum", f.phylum}, "not empty"},
		{"missing input", []string{"-out", f.out, "-phylum", filepath.Join(f.root, "nope")}, "no such file"},
		{"no out", []string{"-phylum", f.phylum}, "-out is required"},
		{"no templates", []string{"-out", f.out}, "no templates"},
		{"bad runs", []string{"-out", f.out, "-phylum", f.phylum, "-runs", "0"}, "-runs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tree(t, f.root)
			code, stdout, stderr := runT(t, hbdiff.Ref, tc.args...)
			require.Equal(t, exitError, code)
			require.Contains(t, stderr, tc.want)
			require.Empty(t, stdout)
			require.Equal(t, before, tree(t, f.root), "a refused run wrote something")
		})
	}
	_, err = os.Stat(filepath.Join(wd, "results"))
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestReadContextFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.jsonl"), "{\"a\":1}\r\n\n  \n{\"b\":2}")
	writeFile(t, filepath.Join(dir, "b.json"), "{\"c\":3}\n\n")
	cs, err := readContextFile(dir, "", "a.jsonl")
	require.NoError(t, err)
	require.Equal(t, []ctxFile{{"a.jsonl:1", []byte(`{"a":1}`)}, {"a.jsonl:4", []byte(`{"b":2}`)}}, cs)
	cs, err = readContextFile(dir, "", "b.json")
	require.NoError(t, err)
	require.Equal(t, []ctxFile{{"b.json", []byte(`{"c":3}`)}}, cs)
	cs, err = readContextFile(dir, "", "notes.txt")
	require.NoError(t, err)
	require.Empty(t, cs)
}

func TestWithin(t *testing.T) {
	require.True(t, within("/a/b", "/a/b"))
	require.True(t, within("/a/b", "/a/b/c"))
	require.False(t, within("/a/b", "/a/bc"))
	require.False(t, within("/a/b", "/a"))
}
