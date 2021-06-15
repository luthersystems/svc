package grpclogging

import "time"

// Time provides time (real or simulated).
type Time interface {
	// Now returns the current time.
	Now() time.Time
}

type timefn func() time.Time

func (fn timefn) Now() time.Time {
	return fn()
}

// RealTime returns a Time implementation that calls time.Now to determine the
// current time.
func RealTime() Time {
	return timefn(time.Now)
}

// Timer acts as a stopwatch.  The StartTimer function returns a function
// which, when called, returns the duration since StartTimer was called.  There
// are no restrictions on the value returned when the function produced by
// StartTimer is called.
type Timer interface {
	StartTimer(now func() time.Time) func() time.Duration
}

// SimpleTimer returns a basic timer that returns the difference between the
// StartTimer time and the 'stop' time, when the function returned by
// StartTimer is called.
//
// If now is nil SimpleTimer will use the function time.Time to determine the
// current time.
func SimpleTimer() Timer {
	return timerFn(func(now func() time.Time) func() time.Duration {
		if now == nil {
			now = time.Now
		}

		t1 := now()
		return func() time.Duration {
			t2 := now()
			return t2.Sub(t1)
		}
	})
}

// UpperBoundTimer returns a Timer that rounds durations up to a multiple of
// the resolution. If resolution is zero a default value of time.Millisecond
// will be used. If resolution is negative a runtime panic will occur.
func UpperBoundTimer(resolution time.Duration) Timer {
	if resolution < 0 {
		panic("invalid resolution for UpperBoundTimer")
	}
	if resolution == 0 {
		resolution = time.Millisecond
	}

	return timerFn(func(now func() time.Time) func() time.Duration {
		stop := SimpleTimer().StartTimer(now)
		return func() time.Duration {
			d := stop()
			// Adding (resolution - 1) to d ensures that integer division by
			// resolution yields the ceiling of the floating point computation
			// of d/resolution.
			return ((d + resolution - 1) / resolution) * resolution
		}
	})
}

type timerFn func(func() time.Time) func() time.Duration

func (fn timerFn) StartTimer(now func() time.Time) func() time.Duration {
	return fn(now)
}
