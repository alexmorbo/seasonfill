package seriesdetail

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ref is a fixed reference "now" so age math never drifts under test.
var ratingRef = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

func TestTMDBRatingTTL(t *testing.T) {
	t.Parallel()

	const day = 24 * time.Hour

	cases := []struct {
		name         string
		inProduction bool
		status       *string
		lastAir      *time.Time
		firstAir     *time.Time
		want         time.Duration
	}{
		// --- five tiers, representative age each ---
		{
			name:         "in_production → 2d",
			inProduction: true,
			lastAir:      new(ratingRef.AddDate(-10, 0, 0)), // old date ignored
			want:         2 * day,
		},
		{
			name:   "continuing status → 2d",
			status: new("Returning Series"),
			want:   2 * day,
		},
		{
			name:    "ended, last air 6mo ago (<1y) → 7d",
			status:  new("Ended"),
			lastAir: new(ratingRef.AddDate(0, -6, 0)),
			want:    7 * day,
		},
		{
			name:    "ended, last air 2y ago (1y–3y) → 30d",
			status:  new("Ended"),
			lastAir: new(ratingRef.AddDate(-2, 0, 0)),
			want:    30 * day,
		},
		{
			name:    "ended, last air 5y ago (3y–8y) → 90d",
			status:  new("Ended"),
			lastAir: new(ratingRef.AddDate(-5, 0, 0)),
			want:    90 * day,
		},
		{
			name:    "ended, last air 10y ago (>8y) → 180d",
			status:  new("Ended"),
			lastAir: new(ratingRef.AddDate(-10, 0, 0)),
			want:    180 * day,
		},

		// --- both dates NULL → 30d Mid (W18-5 parity) ---
		{
			name:   "both dates nil → 30d",
			status: new("Ended"),
			want:   30 * day,
		},
		{
			name: "both dates nil, nil status → 30d",
			want: 30 * day,
		},

		// --- boundary semantics (strict After → older tier) ---
		{
			name:    "exactly 1y → 30d (not 7d)",
			status:  new("Ended"),
			lastAir: new(ratingRef.AddDate(-1, 0, 0)),
			want:    30 * day,
		},
		{
			name:    "exactly 3y → 90d (not 30d)",
			status:  new("Ended"),
			lastAir: new(ratingRef.AddDate(-3, 0, 0)),
			want:    90 * day,
		},
		{
			name:    "exactly 8y → 180d (not 90d)",
			status:  new("Ended"),
			lastAir: new(ratingRef.AddDate(-8, 0, 0)),
			want:    180 * day,
		},

		// --- in_production overrides ended-age ---
		{
			name:         "in_production overrides 10y-old ended-age → 2d",
			inProduction: true,
			status:       new("Ended"),
			lastAir:      new(ratingRef.AddDate(-10, 0, 0)),
			want:         2 * day,
		},

		// --- firstAir fallback when lastAir nil ---
		{
			name:     "lastAir nil, firstAir 6mo ago → 7d",
			status:   new("Ended"),
			firstAir: new(ratingRef.AddDate(0, -6, 0)),
			want:     7 * day,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := TMDBRatingTTL(ratingRef, tc.inProduction, tc.status, tc.lastAir, tc.firstAir)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTMDBRatingStale(t *testing.T) {
	t.Parallel()

	const day = 24 * time.Hour
	endedStatus := new("Ended")
	lastAir5y := new(ratingRef.AddDate(-5, 0, 0)) // → 90d TTL

	cases := []struct {
		name          string
		ratingUpdated *time.Time
		inProduction  bool
		status        *string
		lastAir       *time.Time
		firstAir      *time.Time
		wantStale     bool
	}{
		{
			name:          "nil updated_at → stale (never enriched)",
			ratingUpdated: nil,
			status:        endedStatus,
			lastAir:       lastAir5y,
			wantStale:     true,
		},
		{
			name:          "updated just now → not stale",
			ratingUpdated: new(ratingRef),
			status:        endedStatus,
			lastAir:       lastAir5y,
			wantStale:     false,
		},
		{
			name:          "updated 30d ago vs 90d TTL → not stale",
			ratingUpdated: new(ratingRef.Add(-30 * day)),
			status:        endedStatus,
			lastAir:       lastAir5y,
			wantStale:     false,
		},
		{
			name:          "updated 100d ago vs 90d TTL → stale",
			ratingUpdated: new(ratingRef.Add(-100 * day)),
			status:        endedStatus,
			lastAir:       lastAir5y,
			wantStale:     true,
		},
		{
			name:          "age exactly == TTL → stale (>=)",
			ratingUpdated: new(ratingRef.Add(-90 * day)),
			status:        endedStatus,
			lastAir:       lastAir5y,
			wantStale:     true,
		},
		{
			name:          "in_production 2d TTL, updated 3d ago → stale",
			ratingUpdated: new(ratingRef.Add(-3 * day)),
			inProduction:  true,
			wantStale:     true,
		},
		{
			name:          "in_production 2d TTL, updated 1d ago → not stale",
			ratingUpdated: new(ratingRef.Add(-1 * day)),
			inProduction:  true,
			wantStale:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := TMDBRatingStale(ratingRef, tc.ratingUpdated, tc.inProduction, tc.status, tc.lastAir, tc.firstAir)
			assert.Equal(t, tc.wantStale, got)
		})
	}
}
