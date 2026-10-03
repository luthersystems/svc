package hbdiff

import (
	"errors"
	"fmt"

	"github.com/luthersystems/svc/libhandlebars/hbs"
)

// This file binds the harness to the native engine.
//
// Contract:
//
//	hbs.Parse(tpl string, lim hbs.Limits) (*hbs.Program, error)
//	hbs.FromJSON(ctxJSON []byte) (hbs.Value, error)
//	(*hbs.Program).Render(v hbs.Value, o hbs.Options) (string, error)
//
// Parse and Render errors are *hbs.Error whose Msg is raymond's message
// without the ELPS prefix. A FromJSON error's text is exactly what
// json.Unmarshal into map[string]interface{} reports. The order matches the
// builtin: decode the context, then parse, then render.

// HBSCandidate renders through hbs in ModeCompat with DefaultLimits().
func HBSCandidate(tpl string, ctxJSON []byte) Result {
	var r Result
	func() {
		defer recoverCandidate(&r)
		r = hbsRender(tpl, ctxJSON)
	}()
	return r
}

func hbsRender(tpl string, ctxJSON []byte) Result {
	v, err := hbs.FromJSON(ctxJSON)
	if err != nil {
		return Result{ErrKind: KindUnmarshal, ErrMsg: "error while unmarshaling: " + err.Error()}
	}
	p, err := hbs.Parse(tpl, hbs.DefaultLimits())
	if err != nil {
		return hbsError(err, "error parsing template: ")
	}
	out, err := p.Render(v, hbs.Options{Mode: hbs.ModeCompat, Limits: hbs.DefaultLimits()})
	if err != nil {
		return hbsError(err, "error while rendering template: ")
	}
	return Result{Out: out}
}

// HBSParseCandidate validates through hbs.Parse, as must-parse does.
func HBSParseCandidate(tpl string) Result {
	var r Result
	func() {
		defer recoverCandidate(&r)
		if _, err := hbs.Parse(tpl, hbs.DefaultLimits()); err != nil {
			r = hbsError(err, "error parsing template: ")
		}
	}()
	return r
}

func hbsError(err error, prefix string) Result {
	var he *hbs.Error
	if !errors.As(err, &he) {
		return Result{ErrKind: KindRender, ErrMsg: "non-hbs error: " + err.Error()}
	}
	switch he.Kind {
	case hbs.KindParse:
		return Result{ErrKind: KindParse, ErrMsg: "error parsing template: " + he.Msg}
	case hbs.KindRender:
		return Result{ErrKind: KindRender, ErrMsg: "error while rendering template: " + he.Msg}
	case hbs.KindLimit:
		return Result{ErrKind: KindLimit, ErrMsg: prefix + he.Msg}
	default:
		return Result{ErrKind: KindRender, ErrMsg: fmt.Sprintf("unknown hbs error kind %d: %s", he.Kind, he.Msg)}
	}
}

func recoverCandidate(r *Result) {
	if p := recover(); p != nil {
		*r = Result{ErrKind: KindPanic, ErrMsg: fmt.Sprintf("candidate panic: %v", p)}
	}
}
