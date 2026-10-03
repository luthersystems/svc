// Copyright © 2026 Luther Systems, Ltd. All right reserved.

// Command hbdiff is the private mode of the handlebars differential
// harness (libhandlebars/internal/hbdiff). It renders templates and
// contexts that must never enter this repository through the frozen
// reference (internal/hbref) and the native engine, and reports every
// difference.
//
//	hbdiff -out <dir> [-phylum <dir>] [-ctx <dir>] [-cases <dir>]
//	       [-allow <file>|none] [-runs 3] [-max-files 200] [-ref-only] [template ...]
//
// Templates come from -phylum (every .html file and every ELPS string
// literal containing "{{" in a non-test .lisp file), from the files named
// as arguments, and from -cases, a directory in the shape-corpus layout
// (<name>/template.hbs with contexts in <name>/ctx/*.json).
//
// Contexts for -phylum and argument templates come from -ctx:
//
//	<ctx>/*.json, <ctx>/*.jsonl          every template
//	<ctx>/<key>/*.json, <key>/*.jsonl    one template; <key> is listed in
//	                                     <out>/templates.tsv
//
// A .json file is one context, taken whole (invalid JSON is a valid test
// of the unmarshal error); a .jsonl file holds one context per non-blank
// line. A template with no context renders with {}.
//
// Every input path must lie outside any svc git work tree. Results are
// written only under -out, which must also lie outside one and must be
// empty or not exist. Standard output gets the summary table only, never
// template text, context data or case names. The checked-in allowlist
// (internal/hbdiff/testdata/allowed-diffs.txt) is built in; -allow
// replaces it with another file, and -allow none disables it.
//
// The exit status is 0 when every difference is explained, 1 when any is
// not, and 2 on a usage or I/O error. -ref-only runs the reference alone,
// for example to check that a private corpus is safe to render with it.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luthersystems/svc/libhandlebars/internal/hbdiff"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, hbdiff.DefaultCandidate))
}

// Exit statuses.
const (
	exitOK    = 0
	exitDiff  = 1
	exitError = 2
)

type config struct {
	out      string
	phylum   string
	ctxDir   string
	casesDir string
	allow    string
	runs     int
	maxFiles int
	refOnly  bool
	files    []string
}

// run is main without the process exit, so tests can drive it with a fake
// candidate.
func run(args []string, stdout, stderr io.Writer, cand hbdiff.Candidate) int {
	cfg, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		_, _ = fmt.Fprintln(stderr, "hbdiff:", err)
		return exitError
	}
	if cfg.refOnly {
		cand = nil
	}
	code, err := diff(cfg, stdout, stderr, cand)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hbdiff:", err)
		return exitError
	}
	return code
}

func parseFlags(args []string, stderr io.Writer) (*config, error) {
	cfg := &config{}
	fl := flag.NewFlagSet("hbdiff", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.StringVar(&cfg.out, "out", "", "results directory (required); nothing is written outside it")
	fl.StringVar(&cfg.phylum, "phylum", "", "phylum directory: .html files and ELPS string literals containing {{")
	fl.StringVar(&cfg.ctxDir, "ctx", "", "context directory: *.json and *.jsonl for every template, <key>/*.json for one")
	fl.StringVar(&cfg.casesDir, "cases", "", "case directory in the shape-corpus layout: <name>/template.hbs, <name>/ctx/*.json")
	fl.StringVar(&cfg.allow, "allow", "", `allowlist file; empty means the built-in one, "none" means no allowlist`)
	fl.IntVar(&cfg.runs, "runs", 3, "determinism repeat count per case and engine")
	fl.IntVar(&cfg.maxFiles, "max-files", 200, "most cases whose inputs and outputs are written under -out/cases")
	fl.BoolVar(&cfg.refOnly, "ref-only", false, "run the reference alone, with no candidate")
	if err := fl.Parse(args); err != nil {
		return nil, err
	}
	cfg.files = fl.Args()
	switch {
	case cfg.out == "":
		return nil, errors.New("-out is required")
	case cfg.phylum == "" && cfg.casesDir == "" && len(cfg.files) == 0:
		return nil, errors.New("no templates: give -phylum, -cases or template files")
	case cfg.runs < 1:
		return nil, errors.New("-runs must be at least 1")
	case cfg.maxFiles < 0:
		return nil, errors.New("-max-files must not be negative")
	}
	return cfg, nil
}

// diff checks the paths, loads the cases, runs them and writes the
// results. It returns the exit status.
func diff(cfg *config, stdout, stderr io.Writer, cand hbdiff.Candidate) (int, error) {
	if err := checkPaths(cfg); err != nil {
		return exitError, err
	}
	allow, allowDesc, err := loadAllow(cfg.allow)
	if err != nil {
		return exitError, err
	}
	for _, e := range allow.Expired() {
		_, _ = fmt.Fprintf(stderr, "hbdiff: allowlist line %d expired %s; it waives nothing\n", e.Line, e.Expires)
	}
	in, err := loadInputs(cfg)
	if err != nil {
		return exitError, err
	}
	if len(in.cases) == 0 {
		return exitError, errors.New("no cases")
	}
	if in.unmatched > 0 {
		_, _ = fmt.Fprintf(stderr, "hbdiff: %d context directories match no template; see %s\n",
			in.unmatched, filepath.Join(cfg.out, "templates.tsv"))
	}

	rep := hbdiff.Run(in.cases, hbdiff.Options{Runs: cfg.runs, Candidate: cand, Allow: allow})

	var sum bytes.Buffer
	if err := writeSummary(&sum, rep, summaryInfo{
		templates: in.templates,
		runs:      cfg.runs,
		candidate: cand != nil,
		allow:     allowDesc,
	}); err != nil {
		return exitError, err
	}
	if err := writeResults(cfg, rep, in, sum.Bytes()); err != nil {
		return exitError, err
	}
	if _, err := stdout.Write(sum.Bytes()); err != nil {
		return exitError, err
	}
	if rep.Failed() {
		_, _ = fmt.Fprintf(stderr, "hbdiff: %d unexplained differences; see %s\n",
			rep.Counts[hbdiff.Diff], filepath.Join(cfg.out, "details.txt"))
		return exitDiff, nil
	}
	return exitOK, nil
}

func loadAllow(file string) (*hbdiff.Allowlist, string, error) {
	now := time.Now()
	switch file {
	case "none":
		return nil, "none", nil
	case "":
		al, err := hbdiff.CheckedInAllowlist(now)
		if err != nil {
			return nil, "", fmt.Errorf("built-in allowlist: %w", err)
		}
		return al, fmt.Sprintf("built in (%d entries)", len(al.Entries)), nil
	}
	al, err := hbdiff.LoadAllowlist(file, now)
	if err != nil {
		return nil, "", fmt.Errorf("allowlist: %w", err)
	}
	return al, fmt.Sprintf("-allow file (%d entries)", len(al.Entries)), nil
}

// inputs are the loaded cases and how they were built.
type inputs struct {
	cases     []hbdiff.Case
	templates int
	// index is templates.tsv: one row per -phylum or argument template.
	index     []byte
	unmatched int
}

// ctxFile is one context and the name it gives its cases.
type ctxFile struct {
	name string
	data []byte
}

func loadInputs(cfg *config) (*inputs, error) {
	var tpls []hbdiff.Template
	if cfg.phylum != "" {
		found, err := hbdiff.ExtractPhylum(cfg.phylum, outsideSvc)
		if err != nil {
			return nil, fmt.Errorf("-phylum: %w", err)
		}
		tpls = append(tpls, found...)
	}
	for _, f := range cfg.files {
		b, err := os.ReadFile(f) //nolint:gosec // operator-named input, checked by checkPaths
		if err != nil {
			return nil, err
		}
		tpls = append(tpls, hbdiff.Template{Name: f, Source: string(b)})
	}
	var shared []ctxFile
	byKey := map[string][]ctxFile{}
	if cfg.ctxDir != "" {
		var err error
		if shared, byKey, err = loadContexts(cfg.ctxDir); err != nil {
			return nil, err
		}
	}
	in := &inputs{templates: len(tpls)}
	used := map[string]bool{}
	var index bytes.Buffer
	index.WriteString("template\tctx-key\tcontexts\n")
	for _, t := range tpls {
		key := hbdiff.SafeName(t.Name)
		ctxs := append(append([]ctxFile{}, shared...), byKey[key]...)
		used[key] = true
		if len(ctxs) == 0 {
			ctxs = []ctxFile{{name: "{}", data: []byte("{}")}}
		}
		fmt.Fprintf(&index, "%s\t%s\t%d\n", t.Name, key, len(ctxs))
		for _, c := range ctxs {
			in.cases = append(in.cases, hbdiff.Case{Name: t.Name + "#" + c.name, Template: t.Source, Context: c.data})
		}
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		if !used[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&index, "-\t%s\t%d unmatched\n", k, len(byKey[k]))
	}
	in.unmatched = len(keys)
	in.index = index.Bytes()
	if cfg.casesDir != "" {
		cs, err := hbdiff.LoadShapes(cfg.casesDir, outsideSvc)
		if err != nil {
			return nil, fmt.Errorf("-cases: %w", err)
		}
		seen := map[string]bool{}
		for _, c := range cs {
			if !seen[c.Template] {
				seen[c.Template] = true
				in.templates++
			}
		}
		in.cases = append(in.cases, cs...)
	}
	return in, nil
}

// loadContexts reads a -ctx directory: its own .json and .jsonl files are
// shared by every template; those in a subdirectory belong to the template
// whose key is the subdirectory's name. Symlinks are followed, after the
// same outside-the-svc-work-tree check as the flag's own path. A directory
// below a key directory is an error, not silently skipped.
func loadContexts(dir string) ([]ctxFile, map[string][]ctxFile, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("-ctx: %w", err)
	}
	var shared []ctxFile
	byKey := map[string][]ctxFile{}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		isDir, derr := ctxEntryIsDir(dir, e)
		if derr != nil {
			return nil, nil, derr
		}
		if !isDir {
			cs, err := readContextFile(dir, "", e.Name())
			if err != nil {
				return nil, nil, err
			}
			shared = append(shared, cs...)
			continue
		}
		keyDir := filepath.Join(dir, e.Name())
		sub, err := os.ReadDir(keyDir)
		if err != nil {
			return nil, nil, fmt.Errorf("-ctx: %w", err)
		}
		for _, s := range sub {
			if strings.HasPrefix(s.Name(), ".") {
				continue
			}
			isDir, err := ctxEntryIsDir(keyDir, s)
			if err != nil {
				return nil, nil, err
			}
			if isDir {
				return nil, nil, fmt.Errorf("-ctx: %s is a directory; contexts are <ctx>/*.json(l) or <ctx>/<key>/*.json(l)",
					filepath.Join(keyDir, s.Name()))
			}
			cs, err := readContextFile(dir, e.Name(), s.Name())
			if err != nil {
				return nil, nil, err
			}
			byKey[e.Name()] = append(byKey[e.Name()], cs...)
		}
	}
	return shared, byKey, nil
}

// ctxEntryIsDir reports whether the -ctx entry e of dir is a directory,
// following a symlink once outsideSvc accepts its target.
func ctxEntryIsDir(dir string, e fs.DirEntry) (bool, error) {
	info, err := hbdiff.FollowEntry(dir, e, outsideSvc)
	if err != nil {
		return false, fmt.Errorf("-ctx: %w", err)
	}
	return info.IsDir(), nil
}

// outsideSvc is the LinkCheck for every input: a symlink's target must lie
// outside any svc work tree, like the input paths themselves.
func outsideSvc(resolved string) error {
	if root := svcWorkTree(resolved); root != "" {
		return fmt.Errorf("refusing a symlink into the svc work tree %s; private inputs stay outside the repository", root)
	}
	return nil
}

// readContextFile reads <dir>/<sub>/<name>. Files that are not .json or
// .jsonl are ignored.
func readContextFile(dir, sub, name string) ([]ctxFile, error) {
	isJSONL := strings.HasSuffix(name, ".jsonl")
	if !isJSONL && !strings.HasSuffix(name, ".json") {
		return nil, nil
	}
	label := name
	if sub != "" {
		label = sub + "/" + name
	}
	b, err := os.ReadFile(filepath.Join(dir, sub, name)) //nolint:gosec // operator-named input, checked by checkPaths
	if err != nil {
		return nil, fmt.Errorf("-ctx: %w", err)
	}
	if !isJSONL {
		return []ctxFile{{name: label, data: bytes.TrimRight(b, "\r\n")}}, nil
	}
	var out []ctxFile
	for i, l := range bytes.Split(b, []byte("\n")) {
		l = bytes.TrimRight(l, "\r")
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		out = append(out, ctxFile{name: fmt.Sprintf("%s:%d", label, i+1), data: l})
	}
	return out, nil
}

// checkPaths refuses inputs and -out inside an svc work tree, -out inside
// an input directory, and a non-empty -out.
func checkPaths(cfg *config) error {
	type named struct{ flag, path string }
	var ins []named
	for _, p := range []named{{"-phylum", cfg.phylum}, {"-ctx", cfg.ctxDir}, {"-cases", cfg.casesDir}} {
		if p.path != "" {
			ins = append(ins, p)
		}
	}
	for _, f := range cfg.files {
		ins = append(ins, named{"template", f})
	}
	out, err := resolve(cfg.out)
	if err != nil {
		return fmt.Errorf("-out: %w", err)
	}
	if root := svcWorkTree(out); root != "" {
		return fmt.Errorf("refusing -out %s: it is inside the svc work tree %s", cfg.out, root)
	}
	for _, in := range ins {
		p, err := resolve(in.path)
		if err != nil {
			return fmt.Errorf("%s: %w", in.flag, err)
		}
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("%s: %w", in.flag, err)
		}
		if root := svcWorkTree(p); root != "" {
			return fmt.Errorf("refusing %s %s: it is inside the svc work tree %s; private inputs stay outside the repository",
				in.flag, in.path, root)
		}
		if in.flag != "template" && within(p, out) {
			return fmt.Errorf("refusing -out %s: it is inside %s %s", cfg.out, in.flag, in.path)
		}
	}
	switch ents, err := os.ReadDir(out); {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("-out: %w", err)
	case len(ents) > 0:
		return fmt.Errorf("refusing -out %s: it is not empty", cfg.out)
	}
	return nil
}

// resolve makes p absolute and resolves symlinks in the longest prefix of
// it that exists.
func resolve(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rest := ""
	for {
		r, err := filepath.EvalSymlinks(abs)
		if err == nil {
			return filepath.Join(r, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", err
		}
		rest = filepath.Join(filepath.Base(abs), rest)
		abs = parent
	}
}

// within reports whether p is dir or below it. Both must be resolved.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && filepath.IsLocal(rel)
}

const svcModule = "github.com/luthersystems/svc"

// svcWorkTree returns the nearest ancestor of the resolved path p (or p
// itself) that is the root of a git work tree of this module, or "". It
// checks every ancestor, so a private checkout nested inside svc is still
// refused.
func svcWorkTree(p string) string {
	for d := p; ; d = filepath.Dir(d) {
		if isSvcRoot(d) {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

func isSvcRoot(dir string) bool {
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil { //nolint:gosec // probing an ancestor of an input path
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, "go.mod")) //nolint:gosec // probing an ancestor of an input path
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if mod, ok := strings.CutPrefix(strings.TrimSpace(line), "module"); ok {
			mod = strings.Trim(strings.TrimSpace(mod), `"`)
			return mod == svcModule
		}
	}
	return false
}

type summaryInfo struct {
	allow     string
	templates int
	runs      int
	candidate bool
}

var errKinds = []hbdiff.ErrKind{hbdiff.KindUnmarshal, hbdiff.KindParse, hbdiff.KindRender, hbdiff.KindPanic, hbdiff.KindLimit}

var diffFields = []string{"kind", "msg", "out", "determinism"}

// writeSummary prints the counts table. It names no case and shows no
// template or context text, so it is safe for a terminal or a CI log.
func writeSummary(w io.Writer, rep *hbdiff.Report, info summaryInfo) error {
	cand := "hbs (ModeCompat, DefaultLimits())"
	if !info.candidate {
		cand = "none (reference only)"
	}
	var refErr, candErr [6]int // by errKinds, then the total
	fields := map[hbdiff.Outcome]map[string]int{hbdiff.Allowlisted: {}, hbdiff.Diff: {}}
	for _, cr := range rep.Cases {
		if cr.Outcome != hbdiff.RefSkipped {
			countKind(&refErr, cr.Ref.ErrKind)
		}
		if info.candidate {
			countKind(&candErr, cr.Cand.ErrKind)
		}
		if m, ok := fields[cr.Outcome]; ok && cr.Mismatch != nil {
			m[cr.Mismatch.Field]++
		}
	}

	ew := &errWriter{w: w}
	ew.printf("hbdiff: %d templates, %d cases, %d runs per engine\n", info.templates, len(rep.Cases), info.runs)
	ew.printf("candidate: %s\nallowlist: %s\n\n", cand, info.allow)

	tw := &table{}
	row := tw.row
	row("outcome", "cases")
	row("identical", rep.Counts[hbdiff.Equal])
	row("allowed diff", rep.Counts[hbdiff.Allowlisted])
	row("diff", rep.Counts[hbdiff.Diff])
	row("ref nondeterministic", rep.Counts[hbdiff.RefNondeterministic])
	row("ref skipped (fatal)", rep.Counts[hbdiff.RefSkipped])
	if !info.candidate {
		row("ref only", rep.Counts[hbdiff.RefOnly])
	}
	row("total", len(rep.Cases))
	row()
	head := []any{"errors"}
	for _, k := range errKinds {
		head = append(head, string(k))
	}
	row(append(head, "total")...)
	row(intsRow("reference", refErr[:])...)
	if info.candidate {
		row(intsRow("candidate", candErr[:])...)
		row()
		head = []any{"differing field"}
		for _, f := range diffFields {
			head = append(head, f)
		}
		row(head...)
		for _, o := range []hbdiff.Outcome{hbdiff.Allowlisted, hbdiff.Diff} {
			cells := []any{map[hbdiff.Outcome]string{hbdiff.Allowlisted: "allowed diff", hbdiff.Diff: "diff"}[o]}
			for _, f := range diffFields {
				cells = append(cells, fields[o][f])
			}
			row(cells...)
		}
	}
	tw.write(ew)
	return ew.err
}

// table lays out blank-line-separated blocks of rows: the first column
// left-aligned, the others right-aligned, each column as wide as its
// widest cell across all blocks.
type table struct {
	rows [][]string
}

func (t *table) row(cells ...any) {
	r := make([]string, len(cells))
	for i, c := range cells {
		r[i] = fmt.Sprint(c)
	}
	t.rows = append(t.rows, r)
}

func (t *table) write(w io.Writer) {
	var width []int
	for _, r := range t.rows {
		for i, c := range r {
			if i == len(width) {
				width = append(width, 0)
			}
			width[i] = max(width[i], len(c))
		}
	}
	for _, r := range t.rows {
		var b strings.Builder
		for i, c := range r {
			if i == 0 {
				fmt.Fprintf(&b, "%-*s", width[0], c)
			} else {
				fmt.Fprintf(&b, "  %*s", width[i], c)
			}
		}
		_, _ = fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

func countKind(counts *[6]int, k hbdiff.ErrKind) {
	if k == hbdiff.KindNone {
		return
	}
	for i, ek := range errKinds {
		if ek == k {
			counts[i]++
		}
	}
	counts[len(errKinds)]++
}

func intsRow(label string, n []int) []any {
	cells := []any{label}
	for _, v := range n {
		cells = append(cells, v)
	}
	return cells
}

// errWriter keeps the first write error.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	var n int
	n, e.err = e.w.Write(p)
	return n, e.err
}

func (e *errWriter) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(e, format, args...)
}

// caseLine is one row of cases.jsonl.
type caseLine struct {
	Case      string `json:"case"`
	Outcome   string `json:"outcome"`
	Field     string `json:"field,omitempty"`
	RefKind   string `json:"ref_kind,omitempty"`
	CandKind  string `json:"cand_kind,omitempty"`
	Files     string `json:"files,omitempty"`
	AllowLine int    `json:"allow_line,omitempty"`
}

// writeResults writes everything under cfg.out:
//
//	summary.txt     the table printed on stdout
//	details.txt     every case that is not identical, with clipped text
//	cases.jsonl     one line per case: name, outcome, differing field
//	templates.tsv   template names, context keys, context counts
//	cases/NNNN/     for up to -max-files such cases: template.hbs,
//	                context.json, ref.out or ref.err, cand.out or cand.err
func writeResults(cfg *config, rep *hbdiff.Report, in *inputs, summary []byte) error {
	put := func(rel string, data []byte) error { return hbdiff.WriteUnder(cfg.out, rel, data) }
	if err := put("summary.txt", summary); err != nil {
		return err
	}
	if err := put("templates.tsv", in.index); err != nil {
		return err
	}
	var det bytes.Buffer
	if err := rep.WriteDetails(&det); err != nil {
		return err
	}
	if err := put("details.txt", det.Bytes()); err != nil {
		return err
	}
	var lines bytes.Buffer
	enc := json.NewEncoder(&lines)
	enc.SetEscapeHTML(false)
	written := 0
	for i, cr := range rep.Cases {
		cl := caseLine{
			Case:     cr.Case.Name,
			Outcome:  string(cr.Outcome),
			RefKind:  string(cr.Ref.ErrKind),
			CandKind: string(cr.Cand.ErrKind),
		}
		if cr.Mismatch != nil {
			cl.Field = cr.Mismatch.Field
		}
		if cr.AllowedBy != nil {
			cl.AllowLine = cr.AllowedBy.Line
		}
		if keepFiles(cr.Outcome) && written < cfg.maxFiles {
			written++
			cl.Files = fmt.Sprintf("cases/%04d", i)
			if err := writeCaseFiles(put, cl.Files, cr); err != nil {
				return err
			}
		}
		if err := enc.Encode(cl); err != nil {
			return err
		}
	}
	return put("cases.jsonl", lines.Bytes())
}

func keepFiles(o hbdiff.Outcome) bool {
	switch o {
	case hbdiff.Diff, hbdiff.Allowlisted, hbdiff.RefNondeterministic, hbdiff.RefSkipped:
		return true
	default:
		return false
	}
}

func writeCaseFiles(put func(string, []byte) error, dir string, cr hbdiff.CaseReport) error {
	files := []outFile{
		{"case.txt", []byte(fmt.Sprintf("%s\n%s\n%s\n", cr.Case.Name, cr.Outcome, mismatchText(cr.Mismatch)))},
		{"template.hbs", []byte(cr.Case.Template)},
		{"context.json", cr.Case.Context},
	}
	if cr.Outcome != hbdiff.RefSkipped {
		files = append(files, resultFile("ref", cr.Ref))
	}
	files = append(files, resultFile("cand", cr.Cand))
	for _, f := range files {
		if err := put(dir+"/"+f.name, f.data); err != nil {
			return err
		}
	}
	return nil
}

func mismatchText(m *hbdiff.Mismatch) string {
	if m == nil {
		return ""
	}
	return m.String()
}

// outFile is one file of a case directory.
type outFile struct {
	name string
	data []byte
}

func resultFile(prefix string, r hbdiff.Result) outFile {
	if r.ErrKind != hbdiff.KindNone {
		return outFile{prefix + ".err", []byte(string(r.ErrKind) + "\n" + r.ErrMsg + "\n")}
	}
	return outFile{prefix + ".out", []byte(r.Out)}
}
