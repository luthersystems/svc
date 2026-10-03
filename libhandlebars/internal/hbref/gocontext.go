// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbref

import raymond "github.com/luthersystems/svc/libhandlebars/internal/raymondref"

// RenderGo reproduces svc's Go API, libhandlebars.Render: raymond.Parse,
// addHelpers, then Exec with the Go value itself, no JSON. The error, when
// non-nil, is an *Error (Stage parse, render or panic).
//
// It is an addition to the frozen package for the Go-context differential
// tests; it calls the frozen code unchanged.
func RenderGo(tpl string, ctx any) (string, error) {
	var (
		out string
		err error
	)
	func() {
		defer recoverPanic(&err)
		t, perr := raymond.Parse(tpl)
		if perr != nil {
			err = newError(StageParse, perr.Error())
			return
		}
		addHelpers(t)
		res, rerr := t.Exec(ctx)
		if rerr != nil {
			err = newError(StageRender, rerr.Error())
			return
		}
		out = res
	}()
	if err != nil {
		return "", err
	}
	return out, nil
}
