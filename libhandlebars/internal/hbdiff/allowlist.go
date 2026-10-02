package hbdiff

import (
	"bufio"
	"bytes"
	_ "embed" // the checked-in allowlist
	"fmt"
	"os"
	"path"
	"runtime"
	"strings"
	"time"
)

// Allowlist waives intended differences between the reference and the
// candidate. Its file format is one entry per line:
//
//	pattern | expires | issue | reason
//
// Blank lines and lines starting with # are ignored. expires is a date
// (2006-01-02) after which the entry stops waiving, or "never". issue and
// reason must be non-empty. pattern is one or more conditions joined by
// "&&", all of which must hold:
//
//	case:<glob>            the case name matches the path.Match glob
//	kinds:<ref>-><cand>    error kinds; "ok" is success, "*" is any kind
//	tpl:<substring>        the template contains substring
//	field:<kind|msg|out>   the first differing field
//	candout:<substring>    the candidate's output contains substring
//	arch:<goarch>          the harness runs on GOARCH goarch; arch:!<goarch>
//	                       means any other GOARCH
type Allowlist struct {
	Entries []*AllowEntry
}

// AllowEntry is one allowlist line.
type AllowEntry struct {
	Pattern string
	Expires string
	Issue   string
	Reason  string
	Line    int

	conds   []cond
	expired bool
}

type cond struct {
	op, arg string
}

// allowedDiffs is testdata/allowed-diffs.txt, built in so tools run from
// any directory honour the checked-in allowlist.
//
//go:embed testdata/allowed-diffs.txt
var allowedDiffs []byte

// CheckedInAllowlist parses the allowlist committed with the harness,
// testdata/allowed-diffs.txt.
func CheckedInAllowlist(now time.Time) (*Allowlist, error) {
	return ParseAllowlist(allowedDiffs, now)
}

// LoadAllowlist reads an allowlist file. Entries whose expiry is before
// now are kept but never match; Expired lists them.
func LoadAllowlist(file string, now time.Time) (*Allowlist, error) {
	b, err := os.ReadFile(file) //nolint:gosec // path is the harness's own testdata or a CLI flag
	if err != nil {
		return nil, err
	}
	return ParseAllowlist(b, now)
}

// ParseAllowlist parses allowlist text.
func ParseAllowlist(b []byte, now time.Time) (*Allowlist, error) {
	al := &Allowlist{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 4 {
			return nil, fmt.Errorf("allowlist line %d: want 4 |-separated fields, got %d", n, len(f))
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		e := &AllowEntry{Pattern: f[0], Expires: f[1], Issue: f[2], Reason: f[3], Line: n}
		if e.Pattern == "" || e.Issue == "" || e.Reason == "" {
			return nil, fmt.Errorf("allowlist line %d: empty pattern, issue or reason", n)
		}
		if e.Expires != "never" {
			t, err := time.Parse("2006-01-02", e.Expires)
			if err != nil {
				return nil, fmt.Errorf("allowlist line %d: expires: %w", n, err)
			}
			e.expired = now.After(t.Add(24 * time.Hour))
		}
		for _, part := range strings.Split(e.Pattern, "&&") {
			part = strings.TrimSpace(part)
			op, arg, ok := strings.Cut(part, ":")
			if !ok {
				return nil, fmt.Errorf("allowlist line %d: condition %q has no op: prefix", n, part)
			}
			switch op {
			case "case":
				if _, err := path.Match(arg, ""); err != nil {
					return nil, fmt.Errorf("allowlist line %d: %w", n, err)
				}
			case "kinds":
				if _, _, ok := strings.Cut(arg, "->"); !ok {
					return nil, fmt.Errorf("allowlist line %d: kinds wants ref->cand", n)
				}
			case "tpl", "candout":
			case "arch":
				if strings.TrimPrefix(arg, "!") == "" {
					return nil, fmt.Errorf("allowlist line %d: arch wants a GOARCH", n)
				}
			case "field":
				if arg != "kind" && arg != "msg" && arg != "out" {
					return nil, fmt.Errorf("allowlist line %d: field wants kind, msg or out", n)
				}
			default:
				return nil, fmt.Errorf("allowlist line %d: unknown condition %q", n, op)
			}
			e.conds = append(e.conds, cond{op: op, arg: arg})
		}
		al.Entries = append(al.Entries, e)
	}
	return al, sc.Err()
}

// Expired returns the entries past their expiry date.
func (al *Allowlist) Expired() []*AllowEntry {
	if al == nil {
		return nil
	}
	var out []*AllowEntry
	for _, e := range al.Entries {
		if e.expired {
			out = append(out, e)
		}
	}
	return out
}

// Match returns the first unexpired entry covering this difference.
func (al *Allowlist) Match(c Case, ref, cand Result) *AllowEntry {
	if al == nil {
		return nil
	}
	m := Compare(ref, cand)
	if m == nil {
		return nil
	}
	for _, e := range al.Entries {
		if !e.expired && e.matches(c, ref, cand, m) {
			return e
		}
	}
	return nil
}

func (e *AllowEntry) matches(c Case, ref, cand Result, m *Mismatch) bool {
	for _, cd := range e.conds {
		ok := false
		switch cd.op {
		case "case":
			ok, _ = path.Match(cd.arg, c.Name)
		case "kinds":
			r, k, _ := strings.Cut(cd.arg, "->")
			ok = kindMatch(r, ref.ErrKind) && kindMatch(k, cand.ErrKind)
		case "tpl":
			ok = strings.Contains(c.Template, cd.arg)
		case "field":
			ok = m.Field == cd.arg
		case "candout":
			ok = strings.Contains(cand.Out, cd.arg)
		case "arch":
			if a, neg := strings.CutPrefix(cd.arg, "!"); neg {
				ok = goarch != a
			} else {
				ok = goarch == a
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// goarch is the architecture arch: conditions test; tests override it.
var goarch = runtime.GOARCH

func kindMatch(pat string, k ErrKind) bool {
	return pat == "*" || pat == kindName(k)
}
