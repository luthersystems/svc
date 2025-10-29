package libdates

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseDate is a helper to parse YYYY-MM-DD format dates.
func parseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestYMDBetweenDates_MonthlyIncrements tests that adding N months and then
// computing the difference yields (0, N, 0) for various start dates.
// This mirrors the ELPS test that iterates over start/end dates of each month.
func TestYMDBetweenDates_MonthlyIncrements(t *testing.T) {
	startDates := []string{
		"2024-01-01",
		"2024-01-31",
		"2024-02-01",
		"2024-02-28",
		"2024-02-29", // leap year
		"2024-03-01",
		"2024-03-31",
		"2024-04-01",
		"2024-04-30",
		"2024-05-01",
		"2024-05-31",
		"2024-06-01",
		"2024-06-30",
		"2024-07-01",
		"2024-07-31",
		"2024-08-01",
		"2024-08-31",
		"2024-09-01",
		"2024-09-30",
		"2024-10-01",
		"2024-10-31",
		"2024-11-01",
		"2024-11-30",
		"2024-12-01",
		"2024-12-31",
	}

	for _, startDateStr := range startDates {
		startDate := parseDate(startDateStr)
		for monthsToAdd := 1; monthsToAdd <= 12; monthsToAdd++ {
			endDate := startDate.AddDate(0, monthsToAdd, 0)
			diff, err := DiffYMD(startDate, endDate, nil)
			require.NoError(t, err, "start=%s months=%d", startDateStr, monthsToAdd)
			
			// Expect Years=0, Months=monthsToAdd (or Years=1, Months=monthsToAdd-12 if >= 12), Days=0
			expectedYears := monthsToAdd / 12
			expectedMonths := monthsToAdd % 12
			assert.Equal(t, expectedYears, diff.Years, 
				"start=%s months=%d", startDateStr, monthsToAdd)
			assert.Equal(t, expectedMonths, diff.Months,
				"start=%s months=%d", startDateStr, monthsToAdd)
			assert.Equal(t, 0, diff.Days,
				"start=%s months=%d end=%s", startDateStr, monthsToAdd, endDate.Format("2006-01-02"))
		}
	}
}

// TestYMDBetweenDates_RandomCases tests various random date pairs with expected outputs.
func TestYMDBetweenDates_RandomCases(t *testing.T) {
	tests := []struct {
		start         string
		end           string
		expectedYears int
		expectedMonths int
		expectedDays  int
	}{
		// Same date
		{"2020-01-01", "2020-01-01", 0, 0, 0},
		// Multi-year span
		{"2025-10-31", "2030-12-31", 5, 2, 0},
		// Single month
		{"2020-02-28", "2020-03-28", 0, 1, 0},
		{"2020-07-31", "2020-08-31", 0, 1, 0},
		// Two months plus a day
		{"2020-06-30", "2020-08-31", 0, 2, 1},
		// Three months
		{"2020-06-30", "2020-09-30", 0, 3, 0},
		// Two months
		{"2020-01-31", "2020-03-31", 0, 2, 0},
		// Four years, two months
		{"2020-01-31", "2024-03-31", 4, 2, 0},
		// Four years, one month, 16 days
		{"2020-02-15", "2024-03-31", 4, 1, 16},
		// Leap day to next month
		{"2024-02-29", "2024-03-29", 0, 1, 0},
		// Multi-year span with days
		{"2017-07-14", "2024-01-24", 6, 6, 10},
	}

	for _, tt := range tests {
		t.Run(tt.start+"_to_"+tt.end, func(t *testing.T) {
			start := parseDate(tt.start)
			end := parseDate(tt.end)
			diff, err := DiffYMD(start, end, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedYears, diff.Years, "Years mismatch")
			assert.Equal(t, tt.expectedMonths, diff.Months, "Months mismatch")
			assert.Equal(t, tt.expectedDays, diff.Days, "Days mismatch")
		})
	}
}

// TestYMDBetweenDates_LeapYearEdgeCases tests edge cases around leap years,
// particularly Feb 29 + 1 year behavior.
func TestYMDBetweenDates_LeapYearEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		start         string
		end           string
		expectedYears int
		expectedMonths int
		expectedDays  int
	}{
		{
			name:          "Feb 29 2024 to Feb 28 2025",
			start:         "2024-02-29",
			end:           "2025-02-28",
			expectedYears: 0,
			expectedMonths: 11,
			expectedDays:  30,
		},
		{
			// Note: Go's AddDate(0, 13, 0) on Feb 29, 2024 gives Mar 29, 2025 exactly.
			// So the maximum whole months is 13 (1 year, 1 month), not 12 (1 year).
			// This differs from ELPS cc:add-months which may have different semantics.
			// Go: Feb 29, 2024 + 12 months = Mar 1, 2025 (overflow)
			// Go: Feb 29, 2024 + 13 months = Mar 29, 2025 (exact match)
			name:          "Feb 29 2024 to Mar 29 2025",
			start:         "2024-02-29",
			end:           "2025-03-29",
			expectedYears: 1,
			expectedMonths: 1,
			expectedDays:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := parseDate(tt.start)
			end := parseDate(tt.end)
			diff, err := DiffYMD(start, end, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedYears, diff.Years, "Years mismatch")
			assert.Equal(t, tt.expectedMonths, diff.Months, "Months mismatch")
			assert.Equal(t, tt.expectedDays, diff.Days, "Days mismatch")
		})
	}
}

// TestYMDiff_Apply tests the Apply method to ensure round-tripping.
func TestYMDiff_Apply(t *testing.T) {
	tests := []struct {
		start string
		end   string
	}{
		{"2020-01-01", "2020-01-01"},
		{"2020-01-15", "2024-05-20"},
		{"2024-02-29", "2025-02-28"},
		{"2017-07-14", "2024-01-24"},
	}

	for _, tt := range tests {
		t.Run(tt.start+"_to_"+tt.end, func(t *testing.T) {
			start := parseDate(tt.start)
			end := parseDate(tt.end)
			
			diff, err := DiffYMD(start, end, nil)
			require.NoError(t, err)
			
			reconstructed := diff.Apply(start, nil)
			assert.Equal(t, end, reconstructed, "Apply should reconstruct the end date")
		})
	}
}

// TestDiffYMD_Errors tests error conditions.
func TestDiffYMD_Errors(t *testing.T) {
	t.Run("StartAfterEnd", func(t *testing.T) {
		start := parseDate("2024-01-15")
		end := parseDate("2024-01-10")
		_, err := DiffYMD(start, end, nil)
		assert.ErrorIs(t, err, ErrStartAfterEnd)
	})

	t.Run("YearOutOfRange", func(t *testing.T) {
		start := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(10001, 1, 1, 0, 0, 0, 0, time.UTC)
		_, err := DiffYMD(start, end, nil)
		assert.ErrorIs(t, err, ErrYearOutOfRange)
	})

	t.Run("SpanTooLarge", func(t *testing.T) {
		start := parseDate("0100-01-01")
		end := parseDate("3000-01-01")
		_, err := DiffYMDOpts(start, end, DiffOptions{
			MaxSpanYears: 100,
		})
		assert.ErrorIs(t, err, ErrSpanTooLarge)
	})
}

// TestDiffYMDOpts_CustomAddMonths tests using a custom AddMonthsFn.
func TestDiffYMDOpts_CustomAddMonths(t *testing.T) {
	// Custom policy: always go to the 15th of the target month
	customAddMonths := func(t time.Time, months int) time.Time {
		result := t.AddDate(0, months, 0)
		return time.Date(result.Year(), result.Month(), 15, 0, 0, 0, 0, time.UTC)
	}

	start := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)

	diff, err := DiffYMDOpts(start, end, DiffOptions{
		AddMonths: customAddMonths,
	})
	require.NoError(t, err)
	
	// With custom policy that always lands on the 15th, we expect clean months
	assert.Equal(t, 0, diff.Years)
	assert.Equal(t, 2, diff.Months)
	assert.Equal(t, 0, diff.Days)
}

// TestCivilDays tests the civilDays helper function.
func TestCivilDays(t *testing.T) {
	tests := []struct {
		date1 string
		date2 string
		expectedDiff int64
	}{
		// Same date
		{"2024-01-01", "2024-01-01", 0},
		// One day apart
		{"2024-01-01", "2024-01-02", 1},
		// Across month boundary
		{"2024-01-31", "2024-02-01", 1},
		// Across year boundary
		{"2023-12-31", "2024-01-01", 1},
		// Leap year
		{"2024-02-28", "2024-03-01", 2}, // 2024 is a leap year
		{"2023-02-28", "2023-03-01", 1}, // 2023 is not
		// Multi-year span
		{"2020-01-01", "2024-01-01", 1461}, // 4 years with 1 leap year = 365*4 + 1
	}

	for _, tt := range tests {
		t.Run(tt.date1+"_to_"+tt.date2, func(t *testing.T) {
			d1 := parseDate(tt.date1)
			d2 := parseDate(tt.date2)
			
			days1 := civilDays(d1.Year(), d1.Month(), d1.Day())
			days2 := civilDays(d2.Year(), d2.Month(), d2.Day())
			
			diff := days2 - days1
			assert.Equal(t, tt.expectedDiff, diff)
		})
	}
}

// TestYMDiff_Invariants tests that the YMDiff maintains proper invariants.
func TestYMDiff_Invariants(t *testing.T) {
	tests := []string{
		"2020-01-01",
		"2024-02-29",
		"2023-12-31",
		"2025-06-15",
	}

	for _, startStr := range tests {
		start := parseDate(startStr)
		// Test various month offsets
		for months := 0; months <= 36; months++ {
			end := start.AddDate(0, months, 0)
			diff, err := DiffYMD(start, end, nil)
			require.NoError(t, err, "start=%s months=%d", startStr, months)
			
			// Invariant 1: Years >= 0, Months in [0, 11], Days >= 0
			assert.GreaterOrEqual(t, diff.Years, 0, "Years should be >= 0")
			assert.GreaterOrEqual(t, diff.Months, 0, "Months should be >= 0")
			assert.LessOrEqual(t, diff.Months, 11, "Months should be <= 11")
			assert.GreaterOrEqual(t, diff.Days, 0, "Days should be >= 0")
			
			// Invariant 2: Applying the diff should get us back to end
			reconstructed := diff.Apply(start, nil)
			assert.Equal(t, end, reconstructed,
				"start=%s months=%d end=%s", startStr, months, end.Format("2006-01-02"))
		}
	}
}

