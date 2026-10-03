// Copyright © 2026 Luther Systems, Ltd. All right reserved.

// Package hbs is a Handlebars engine for ELPS embedding in Fabric chaincode.
//
// It renders the Handlebars 3 dialect that luthersystems/raymond rendered,
// byte for byte, in ModeCompat. ModeFixed is the same engine with the known
// helper bugs fixed; a phylum must opt in to it. Output, errors and step
// charges are a pure function of (template, context, options): no map order,
// clock, locale or process state is read.
//
// A Program is immutable after Parse and is safe to share between goroutines
// and ELPS VMs. Per-render state (data frames, the global helper's map,
// output) lives in the render call.
package hbs

import "fmt"

// Mode selects helper behaviour.
type Mode uint8

const (
	// ModeCompat reproduces raymond and svc's helpers exactly, including
	// their bugs. The one exception: helpers that combined hash values in Go
	// map order (plus, minus) use sorted key order, since the old output was
	// not deterministic.
	ModeCompat Mode = iota
	// ModeFixed fixes the helper bugs listed in docs/design/raymond-replacement.md.
	ModeFixed
)

// Limits bound the work one template can cause. They are code constants for
// production: every endorser must apply the same values.
type Limits struct {
	MaxTemplateBytes int   // template source length
	MaxDepth         int   // nesting of blocks, subexpressions and paths
	MaxOutputBytes   int   // rendered output length
	MaxSteps         int64 // evaluation steps (the Meter's units), with or without a Meter
}

// DefaultLimits returns the production limits. It is a function, not a
// variable, so no package can change the limits every caller applies.
func DefaultLimits() Limits {
	return Limits{
		MaxTemplateBytes: 1 << 20,
		MaxDepth:         256,
		MaxOutputBytes:   16 << 20,
		MaxSteps:         1 << 25,
	}
}

// Meter charges evaluation work against the caller's budget (the ELPS step
// budget in production). Charge returns a non-nil error when the budget is
// exhausted; the render stops and returns that error unchanged.
type Meter interface {
	Charge(steps int64) error
}

// ErrorKind classifies an engine error.
type ErrorKind uint8

const (
	KindParse  ErrorKind = iota + 1 // template syntax; handlebars-parse
	KindRender                      // evaluation or helper failure; handlebars-render
	KindLimit                       // a Limits cap was exceeded
)

// Error is returned by Parse and Render. Msg is the text raymond produced
// for the same failure, so ELPS condition messages do not change.
type Error struct {
	Msg  string
	Kind ErrorKind
}

func (e *Error) Error() string { return e.Msg }

// Options configure one render.
type Options struct {
	Meter  Meter // nil: no charge
	Limits Limits
	Mode   Mode
}

func errorf(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}
