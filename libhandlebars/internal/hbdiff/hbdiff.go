// Copyright © 2026 Luther Systems, Ltd. All right reserved.

// Package hbdiff is the differential harness for the native handlebars
// engine (libhandlebars/hbs). It renders each case through the frozen
// reference pipeline (internal/hbref) and through a Candidate, and reports
// every difference in output, error kind or error text.
//
// The checked-in corpora live in testdata: corpus/ (literal cases, with
// goldens in golden/), shapes/ (skeletons of production templates with
// generated contexts). FuzzDiff generates more. The cmd/hbdiff tool runs
// private templates and contexts that are never committed.
package hbdiff

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/luthersystems/svc/libhandlebars/internal/hbref"
)

// ErrKind classifies a failed render. The empty kind means success.
type ErrKind string

const (
	KindNone      ErrKind = ""
	KindUnmarshal ErrKind = "unmarshal" // context JSON is not an object
	KindParse     ErrKind = "parse"     // handlebars-parse
	KindRender    ErrKind = "render"    // handlebars-render
	KindPanic     ErrKind = "panic"     // internal-panic in ELPS
	KindLimit     ErrKind = "limit"     // candidate only: a Limits cap
)

// Case is one template rendered with one JSON context.
type Case struct {
	Name     string
	Template string
	Context  []byte // JSON
}

// Result is what one engine produced for a Case. ErrMsg is the full text
// the ELPS builtin shows, prefix included ("error while rendering
// template: ...").
type Result struct {
	Out     string  `json:"out"`
	ErrKind ErrKind `json:"kind,omitempty"`
	ErrMsg  string  `json:"msg,omitempty"`
}

// Candidate renders tpl with ctxJSON through the engine under test. It
// must not panic; a panic should be reported as KindPanic.
type Candidate func(tpl string, ctxJSON []byte) Result

// ParseCandidate validates tpl as handlebars:must-parse would.
type ParseCandidate func(tpl string) Result

// DefaultCandidate and DefaultParseCandidate are the native engine
// (candidate_hbs.go).
var (
	DefaultCandidate      Candidate      = HBSCandidate
	DefaultParseCandidate ParseCandidate = HBSParseCandidate
)

// Ref renders c through the frozen reference.
func Ref(tpl string, ctxJSON []byte) Result {
	out, err := hbref.RenderJSON(tpl, ctxJSON)
	return refResult(out, err)
}

// RefParse runs the reference must-parse.
func RefParse(tpl string) Result {
	return refResult("", hbref.MustParse(tpl))
}

func refResult(out string, err error) Result {
	if err == nil {
		return Result{Out: out}
	}
	var e *hbref.Error
	if !errors.As(err, &e) {
		return Result{ErrKind: KindPanic, ErrMsg: err.Error()}
	}
	return Result{ErrKind: ErrKind(e.Stage), ErrMsg: e.ELPS}
}

// Mismatch describes how a candidate Result differs from the reference.
type Mismatch struct {
	Field  string // "kind", "msg" or "out"
	Detail string
}

func (m *Mismatch) String() string { return m.Field + ": " + m.Detail }

// Compare returns nil when cand equals ref exactly, or the first field
// that differs.
func Compare(ref, cand Result) *Mismatch {
	switch {
	case ref.ErrKind != cand.ErrKind:
		return &Mismatch{Field: "kind", Detail: fmt.Sprintf("ref %s, candidate %s (%s)",
			kindName(ref.ErrKind), kindName(cand.ErrKind), firstLine(cand.ErrMsg+cand.Out))}
	case ref.ErrMsg != cand.ErrMsg:
		return &Mismatch{Field: "msg", Detail: diffAt(ref.ErrMsg, cand.ErrMsg)}
	case ref.Out != cand.Out:
		return &Mismatch{Field: "out", Detail: diffAt(ref.Out, cand.Out)}
	}
	return nil
}

func kindName(k ErrKind) string {
	if k == KindNone {
		return "ok"
	}
	return string(k)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clip(s, 80)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}

// diffAt reports the first byte offset where a and b differ, with a
// window of each.
func diffAt(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	lo := max(0, i-30)
	return fmt.Sprintf("first difference at byte %d (lengths %d, %d): ref %q, candidate %q",
		i, len(a), len(b), clip(a[lo:], 90), clip(b[lo:], 90))
}
