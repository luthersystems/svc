// Package hbref is the frozen reference for svc's handlebars pipeline: the
// raymond fork in internal/raymondref plus svc's helper set, called the way
// libhandlebars's render and must-parse builtins call them. The differential
// harness (internal/hbdiff) compares the new engine against it.
//
// It is frozen. See NOTICE.md.
package hbref

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"strconv"

	raymond "github.com/luthersystems/svc/libhandlebars/internal/raymondref"
)

// Stage names the pipeline step that failed.
type Stage string

const (
	// StageUnmarshal: the context JSON did not decode into an object.
	// ELPS signals a plain error.
	StageUnmarshal Stage = "unmarshal"
	// StageParse: raymond rejected the template. ELPS signals
	// handlebars-parse.
	StageParse Stage = "parse"
	// StageRender: raymond or a helper returned or panicked with an error.
	// ELPS signals handlebars-render.
	StageRender Stage = "render"
	// StagePanic: evaluation panicked with a runtime error or a non-error
	// value, which raymond re-panics. ELPS reports an internal-panic.
	StagePanic Stage = "panic"
)

// Error is a failure of the reference pipeline.
type Error struct {
	Stage Stage
	// Raw is the underlying message (raymond's, encoding/json's or the
	// panic description) without the ELPS prefix.
	Raw string
	// ELPS is the exact message text the libhandlebars builtin shows.
	ELPS string
}

func (e *Error) Error() string { return e.ELPS }

func newError(stage Stage, raw string) *Error {
	var prefix string
	switch stage {
	case StageUnmarshal:
		prefix = "error while unmarshaling: "
	case StageParse:
		prefix = "error parsing template: "
	case StageRender:
		prefix = "error while rendering template: "
	case StagePanic:
		prefix = "internal error (recovered panic): "
	}
	return &Error{Stage: stage, Raw: raw, ELPS: prefix + raw}
}

// MustParse reproduces handlebars:must-parse: it parses tpl and discards
// the result. The error, when non-nil, is an *Error.
func MustParse(tpl string) error {
	var err error
	func() {
		defer recoverPanic(&err)
		if _, perr := raymond.Parse(tpl); perr != nil {
			err = newError(StageParse, perr.Error())
		}
	}()
	return err
}

// RenderJSON reproduces handlebars:render for a context already serialized
// to JSON (the bytes path of the builtin, and the ELPS-value path after
// libjson serialization). The error, when non-nil, is an *Error.
func RenderJSON(tpl string, ctxJSON []byte) (string, error) {
	var (
		out string
		err error
	)
	func() {
		defer recoverPanic(&err)
		out, err = renderJSON(tpl, ctxJSON)
	}()
	if err != nil {
		return "", err
	}
	return out, nil
}

func renderJSON(tpl string, ctxJSON []byte) (string, error) {
	var jsonContext map[string]interface{}
	if uerr := json.Unmarshal(ctxJSON, &jsonContext); uerr != nil {
		return "", newError(StageUnmarshal, uerr.Error())
	}
	t, perr := raymond.Parse(tpl)
	if perr != nil {
		return "", newError(StageParse, perr.Error())
	}
	addHelpers(t)
	result, rerr := t.Exec(jsonContext)
	if rerr != nil {
		return "", newError(StageRender, rerr.Error())
	}
	return result, nil
}

func recoverPanic(errp *error) {
	p := recover()
	if p == nil {
		return
	}
	*errp = newError(StagePanic, panicDescription(p))
}

// panicDescription mirrors elps's description of a recovered host panic
// (lisp/host_panic.go) for the value kinds raymond can panic with.
func panicDescription(p any) string {
	if err, ok := p.(runtime.Error); ok {
		return err.Error()
	}
	v := reflect.ValueOf(p)
	switch v.Kind() { //nolint:exhaustive // other kinds fall through to error/fmt below
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	}
	if err, ok := p.(error); ok {
		return err.Error()
	}
	return fmt.Sprint(p)
}
