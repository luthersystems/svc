package svc

import (
	"context"
	"time"
)

type noCancel struct {
	ctx context.Context
}

func (c noCancel) Deadline() (time.Time, bool)       { return time.Time{}, false }
func (c noCancel) Done() <-chan struct{}             { return nil }
func (c noCancel) Err() error                        { return nil }
func (c noCancel) Value(key interface{}) interface{} { return c.ctx.Value(key) }

// WithoutCancel returns a context that is never canceled.
// This is primarily used to re-use a context across a request that would
// otherwise be canceled (e.g., SNS publish).
func WithoutCancel(ctx context.Context) context.Context {
	return noCancel{ctx: ctx}
}
