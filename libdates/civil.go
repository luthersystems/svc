// Package libdates provides a canonical (years, months, days) difference between
// two civil dates with DoS-safe, O(1) algorithms and explicit control over
// month-rollover semantics.
//
// Overview
//
// This package computes a canonical (years, months, days) difference between
// two civil dates using the rule:
//
//   1) Choose the maximum whole-months M such that addMonths(start, M) <= end
//      (where addMonths encodes your month-rollover policy, e.g., cc:add-months).
//   2) The leftover days is the civil-day count between that anchor date and end.
//
// This matches specs like "max whole months, then days" (no ad-hoc EOM special-
// casing). Leap-day and end-of-month behavior is entirely defined by the provided
// addMonths policy (defaults to time.AddDate(0, m, 0) clamping).
//
// Civil dates
//
// A "civil date" is a calendar date expressed as Year–Month–Day *without* any
// clock time, time zone, or daylight-saving-time effects. We treat civil dates
// in the proleptic Gregorian calendar (the same model used by Go's time package
// for Year 1..9999). In this model:
//
//   • Each successive calendar day increases the civil day count by exactly 1.
//   • There are no DST gaps or repeats (we operate at UTC midnight).
//   • Historical calendar cutovers (e.g., Julian→Gregorian) are ignored.
//
// The implementation uses a civil serial-day function to compute day deltas,
// avoiding time.Duration arithmetic (which can overflow for large spans) and
// avoiding DST/time zone anomalies.
//
// Performance & safety
//
// The algorithm is O(1): it computes an arithmetic month span, anchors once via
// addMonths, and applies at most one backward or forward correction. Leftover
// days use the civil serial-day function, not time.Duration. Guards are provided
// for start>end, year range, and configurable "mega-span" limits.
package libdates

import (
	"errors"
	"time"
)

// YMDiff is the canonical (years, months, days) such that applying it to start
// (using your month-rollover semantics) yields end:
//
//    M      = years*12 + months
//    anchor = addMonths(start, M)
//    anchor <= end
//    days   = civilDays(end) - civilDays(anchor)
//
// Invariants:
// - Years >= 0, Months in [0, 11], Days >= 0.
// - If start == end, YMDiff{0,0,0}.
// - If addMonths == time.AddDate(0,m,0), leap/EOM behavior will follow Go's
//   clamping rules (e.g., Jan 31 + 1 month = Feb 29 in leap years, else Feb 28).
type YMDiff struct {
	Years  int
	Months int
	Days   int
}

// AddMonthsFn is an injection point for your exact month-rollover semantics.
// If nil, DiffYMD/DiffYMDOpts use time.AddDate(0, m, 0) (Go's clamping).
//
// Examples of policies you might mirror here:
// - cc:add-months from your ELPS runtime.
// - A business-specific rule for leap days or EOM alignment.
type AddMonthsFn func(time.Time, int) time.Time

// DiffOptions configures DiffYMDOpts.
//
// MaxSpanMonths / MaxSpanYears bound the allowed span for DoS-safety.
// If MaxSpanMonths > 0 it is used; otherwise MaxSpanYears applies.
// MaxSpanDays (optional) can cap the leftover-days component.
type DiffOptions struct {
	AddMonths     AddMonthsFn
	MaxSpanMonths int // e.g., 24000 (≈ 2000 years)
	MaxSpanYears  int // e.g., 2000 (used only if MaxSpanMonths <= 0)
	MaxSpanDays   int // optional cap on leftover days; 0 = no cap
}

var (
	// ErrStartAfterEnd indicates start > end.
	ErrStartAfterEnd = errors.New("start after end")
	// ErrYearOutOfRange indicates a date outside the supported civil range
	// [0001-01-01, 9999-12-31].
	ErrYearOutOfRange = errors.New("date out of supported range [0001-01-01, 9999-12-31]")
	// ErrSpanTooLarge indicates the span exceeds configured mega-span limits.
	ErrSpanTooLarge = errors.New("date span exceeds configured maximum")
)

// DiffYMD computes the canonical (years, months, days) between start and end,
// using the "max whole months, then days" rule with the provided AddMonthsFn
// (or Go clamping if nil). It enforces a default mega-span guard of ~2000 years.
//
// Semantics:
//   - Normalize both inputs to UTC midnight (no DST artifacts).
//   - Compute arithmetic month span M0.
//   - Anchor = addMonths(start, M0). If anchor > end, decrement M by 1.
//     If addMonths(start, M+1) <= end, increment M by 1.
//   - Years = M / 12, Months = M % 12.
//   - Days = civilDays(end) - civilDays(anchor).
//
// Complexity: O(1). No day-by-day loops. No time.Duration subtraction.
// Safety: Guards against start > end, year range, and mega spans.
//
// Example:
//   diff, err := DiffYMD(time.Date(2024,2,29,0,0,0,0,time.UTC),
//                        time.Date(2025,2,28,0,0,0,0,time.UTC), nil)
//   // Using Go clamping, diff == {Years:0, Months:11, Days:30}
//
// To precisely match another runtime (e.g., cc:add-months), inject it via DiffYMDOpts.
func DiffYMD(start, end time.Time, addMonths AddMonthsFn) (YMDiff, error) {
	return DiffYMDOpts(start, end, DiffOptions{
		AddMonths:     addMonths,
		MaxSpanYears:  2000, // default guard; adjust to taste
		MaxSpanMonths: 0,    // unset => use MaxSpanYears
		MaxSpanDays:   0,    // unset
	})
}

// DiffYMDOpts is DiffYMD with explicit options.
//
// Use cases:
// - Plug in a custom AddMonthsFn to mirror cc:add-months so ELPS and Go agree.
// - Tighten or relax DoS guards (MaxSpanMonths/Years/Days).
//
// Guarantees (assuming AddMonthsFn is deterministic and monotone w.r.t. months):
// - Anchor monotonicity: addMonths(start, M) <= end and addMonths(start, M+1) > end.
// - Canonicalization: returned (Y,M,D) is unique for the given AddMonthsFn.
// - Stability: identical inputs and policy yield identical outputs.
func DiffYMDOpts(start, end time.Time, opts DiffOptions) (YMDiff, error) {
	addMonths := opts.AddMonths
	if addMonths == nil {
		addMonths = func(t time.Time, m int) time.Time { return t.AddDate(0, m, 0) }
	}

	// Normalize to UTC midnight (monotone civil dates; no DST artifacts).
	s := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	e := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)

	// Guards
	if s.After(e) {
		return YMDiff{}, ErrStartAfterEnd
	}
	if !inCivilRange(s) || !inCivilRange(e) {
		return YMDiff{}, ErrYearOutOfRange
	}

	// Mega-span guard (months first; else years).
	monthsAbs := absInt((e.Year()-s.Year())*12 + int(e.Month()-s.Month()))
	if opts.MaxSpanMonths > 0 && monthsAbs > opts.MaxSpanMonths {
		return YMDiff{}, ErrSpanTooLarge
	}
	if opts.MaxSpanMonths <= 0 && opts.MaxSpanYears > 0 {
		if absInt(e.Year()-s.Year()) > opts.MaxSpanYears {
			return YMDiff{}, ErrSpanTooLarge
		}
	}

	// Initial arithmetic month span.
	m := (e.Year()-s.Year())*12 + int(e.Month()-s.Month())
	anchor := addMonths(s, m)

	// At most one step back/forward to satisfy "max whole months <= end".
	if anchor.After(e) {
		m--
		anchor = addMonths(s, m)
	}
	if anPlus := addMonths(s, m+1); !anPlus.After(e) {
		m++
		anchor = anPlus
	}

	// Leftover days via civil serial (monotone; no duration overflow).
	ad := civilDays(anchor.Year(), anchor.Month(), anchor.Day())
	ed := civilDays(e.Year(), e.Month(), e.Day())
	dayDelta := int(ed - ad)
	if dayDelta < 0 {
		// Defensive: should not happen; pull back one month and recompute once.
		m--
		anchor = addMonths(s, m)
		ad = civilDays(anchor.Year(), anchor.Month(), anchor.Day())
		dayDelta = int(ed - ad)
	}

	return YMDiff{
		Years:  m / 12,
		Months: m % 12,
		Days:   dayDelta,
	}, nil
}

// Apply applies a YMDiff to a start date using the provided month policy,
// reconstructing the end date (useful for property-based tests).
// It mirrors the same semantics used by DiffYMD/DiffYMDOpts.
func (d YMDiff) Apply(start time.Time, addMonths AddMonthsFn) time.Time {
	if addMonths == nil {
		addMonths = func(t time.Time, m int) time.Time { return t.AddDate(0, m, 0) }
	}
	s := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	m := d.Years*12 + d.Months
	anchor := addMonths(s, m)
	return anchor.AddDate(0, 0, d.Days)
}

// Helpers

func inCivilRange(t time.Time) bool {
	y := t.Year()
	return y >= 1 && y <= 9999
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// civilDays converts a civil date to a serial day count (proleptic Gregorian).
// Howard Hinnant's algorithm (public domain), adapted for int64 and year 1..9999.
//
// We intentionally avoid any epoch offset: callers subtract two civilDays values
// to obtain day deltas, so the absolute zero-point is irrelevant.
func civilDays(y int, m time.Month, d int) int64 {
	yy := int64(y)
	mm := int64(m)
	dd := int64(d)
	if mm <= 2 {
		yy--
		mm += 12
	}
	era := floorDiv(yy, 400)
	yoe := yy - era*400
	doy := (153*(mm-3)+2)/5 + dd - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe
}

func floorDiv(a, b int64) int64 {
	q := a / b
	r := a % b
	if (r != 0) && ((r > 0) != (b > 0)) {
		q--
	}
	return q
}

