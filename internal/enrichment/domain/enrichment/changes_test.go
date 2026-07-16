package enrichment

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestPlanWindows(t *testing.T) {
	t.Parallel()

	// Fixed "now" mid-day so utcMidnight truncation is exercised (not a
	// midnight boundary). today = 2026-06-25.
	now := time.Date(2026, 6, 25, 13, 30, 0, 0, time.UTC)
	today := day(2026, 6, 25)

	tests := []struct {
		name            string
		cursor          ChangeCursor
		overlapDays     int
		maxLookbackDays int
		want            []Window
	}{
		{
			name:            "empty cursor → cold-start [today-1, today]",
			cursor:          ChangeCursor{},
			overlapDays:     1,
			maxLookbackDays: 14,
			want:            []Window{{Start: today.AddDate(0, 0, -1), End: today}},
		},
		{
			name:            "lag 3d + overlap 1 → single window [today-4, today]",
			cursor:          ChangeCursor{LastWindowEnd: today.AddDate(0, 0, -3)},
			overlapDays:     1,
			maxLookbackDays: 14,
			want:            []Window{{Start: today.AddDate(0, 0, -4), End: today}},
		},
		{
			name:            "overlap 0 → start not shifted (Start == LastWindowEnd day)",
			cursor:          ChangeCursor{LastWindowEnd: today.AddDate(0, 0, -3)},
			overlapDays:     0,
			maxLookbackDays: 14,
			want:            []Window{{Start: today.AddDate(0, 0, -3), End: today}},
		},
		{
			name:            "chunk exactly 14 inclusive days (diff 13, overlap 0) → ONE window",
			cursor:          ChangeCursor{LastWindowEnd: today.AddDate(0, 0, -13)},
			overlapDays:     0,
			maxLookbackDays: 14,
			want:            []Window{{Start: today.AddDate(0, 0, -13), End: today}},
		},
		{
			name:            "lag 13d + overlap 1 → 15 inclusive days → 2 chunked windows",
			cursor:          ChangeCursor{LastWindowEnd: today.AddDate(0, 0, -13)},
			overlapDays:     1,
			maxLookbackDays: 14,
			want: []Window{
				{Start: today.AddDate(0, 0, -14), End: today.AddDate(0, 0, -1)}, // 14 inclusive days
				{Start: today.AddDate(0, 0, -1), End: today},                    // remainder, shared boundary
			},
		},
		{
			name:            "lag 20d → stale beyond lookback → reset cold-start",
			cursor:          ChangeCursor{LastWindowEnd: today.AddDate(0, 0, -20)},
			overlapDays:     1,
			maxLookbackDays: 14,
			want:            []Window{{Start: today.AddDate(0, 0, -1), End: today}},
		},
		{
			name:            "lag exactly 14d (not stale) + overlap 0 → 2 windows",
			cursor:          ChangeCursor{LastWindowEnd: today.AddDate(0, 0, -14)},
			overlapDays:     0,
			maxLookbackDays: 14,
			want: []Window{
				{Start: today.AddDate(0, 0, -14), End: today.AddDate(0, 0, -1)},
				{Start: today.AddDate(0, 0, -1), End: today},
			},
		},
		{
			name:            "future cursor (now < LastWindowEnd) → reset cold-start",
			cursor:          ChangeCursor{LastWindowEnd: today.AddDate(0, 0, 5)},
			overlapDays:     1,
			maxLookbackDays: 14,
			want:            []Window{{Start: today.AddDate(0, 0, -1), End: today}},
		},
		{
			name:            "cursor at today + overlap 0 → single same-day window [today, today]",
			cursor:          ChangeCursor{LastWindowEnd: now}, // same calendar day as now
			overlapDays:     0,
			maxLookbackDays: 14,
			want:            []Window{{Start: today, End: today}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := PlanWindows(tc.cursor, now, tc.overlapDays, tc.maxLookbackDays)
			require.Len(t, got, len(tc.want), "window count")
			for i := range tc.want {
				assert.Truef(t, tc.want[i].Start.Equal(got[i].Start),
					"window[%d].Start want %s got %s", i, tc.want[i].Start, got[i].Start)
				assert.Truef(t, tc.want[i].End.Equal(got[i].End),
					"window[%d].End want %s got %s", i, tc.want[i].End, got[i].End)
				// Every window honors the 14-inclusive-day (13-day diff) API cap.
				assert.LessOrEqualf(t, got[i].End.Sub(got[i].Start), 13*24*time.Hour,
					"window[%d] exceeds 14 inclusive days", i)
				// Bounds are UTC-midnight.
				assert.Equal(t, utcMidnight(got[i].Start), got[i].Start, "Start midnight")
				assert.Equal(t, utcMidnight(got[i].End), got[i].End, "End midnight")
			}
			// Chronological, oldest-first.
			for i := 1; i < len(got); i++ {
				assert.Falsef(t, got[i].Start.Before(got[i-1].Start),
					"windows not chronological at %d", i)
			}
		})
	}
}

// TestPlanWindows_LastWindowEndReadableForDedupBoundary documents the contract
// W2-4 relies on: cursor.LastWindowEnd is a plain field the poller reads BEFORE
// PlanWindows to derive dedupBoundary (= prevLastWindowEnd), unmodified.
func TestPlanWindows_LastWindowEndReadableForDedupBoundary(t *testing.T) {
	t.Parallel()
	boundary := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	c := ChangeCursor{LastWindowEnd: boundary}
	// The value the poller would pass to MarkChangedByTMDBIDs is exactly this,
	// with no overlap shift.
	assert.True(t, c.LastWindowEnd.Equal(boundary))
	_ = PlanWindows(c, boundary.AddDate(0, 0, 2), 1, 14)
	assert.True(t, c.LastWindowEnd.Equal(boundary), "PlanWindows must not mutate the cursor")
}
