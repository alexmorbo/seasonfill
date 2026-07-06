package seriesdetail

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
)

// F-11: the degraded projection's OMDb TTL must be the W18-5/W18-10 progressive
// curve (via omdbDegradedTTL == TMDBRatingTTL), NOT the flat enrichment.TTL(
// SourceOMDb, KindOMDb) 24h. This proves the composer selects the curve.
func TestOMDbDegradedTTL_UsesProgressiveCurve(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	yearsAgo := func(y int) *time.Time { v := now.AddDate(-y, 0, 0); return &v }

	cases := []struct {
		name  string
		canon series.Canon
		want  time.Duration
	}{
		{"in production → 2d", series.Canon{InProduction: true}, 2 * day},
		{"continuing status → 2d", series.Canon{Status: new("Returning Series")}, 2 * day},
		{"ended recent <1y → 7d", series.Canon{Status: new("Ended"), LastAirDate: at(-180 * day)}, 7 * day},
		{"ended 2y → 30d", series.Canon{Status: new("Ended"), LastAirDate: yearsAgo(2)}, 30 * day},
		{"ended 5y → 90d", series.Canon{Status: new("Ended"), LastAirDate: yearsAgo(5)}, 90 * day},
		{"ended 12y → 180d", series.Canon{Status: new("Ended"), LastAirDate: yearsAgo(12)}, 180 * day},
		{"unknown age → 30d", series.Canon{Status: new("Ended")}, 30 * day},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := omdbDegradedTTL(tc.canon, now)
			assert.Equal(t, tc.want, got)
			// The flat composer TTL (pre-W18-13) was always 24h; the curve
			// only equals it for no case here — confirming the change bites.
			assert.NotEqual(t, enrichment.TTL(enrichment.SourceOMDb, enrichment.KindOMDb), got,
				"progressive TTL must differ from the flat 24h it replaced")
		})
	}
}

// F-11: end-to-end through enrichment.Degraded — an ended-recent OMDb series
// synced 100h ago is flagged stale by the OLD flat 24h TTL (2×24h=48h cutoff)
// but NOT by the NEW progressive 7d TTL (2×7d=336h cutoff), matching what the
// /ratings endpoint's OMDbRatingStale reports (fresh: 100h < 1×7d=168h).
func TestDegraded_OMDb_ProgressiveTTL_NotStaleWhereFlatWas(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
	syncedAt := now.Add(-100 * time.Hour)
	lastAir := now.Add(-180 * 24 * time.Hour) // ended ~6 months ago → 7d tier
	canon := series.Canon{Status: new("Ended"), LastAirDate: &lastAir}

	base := func(ttl time.Duration) enrichment.DegradedInput {
		return enrichment.DegradedInput{
			SyncedAt:        map[enrichment.Source]*time.Time{enrichment.SourceOMDb: &syncedAt},
			Errors:          map[enrichment.Source]*enrichment.EnrichmentError{},
			TTLs:            map[enrichment.Source]time.Duration{enrichment.SourceOMDb: ttl},
			SonarrReachable: true,
			QbitReachable:   true,
		}
	}

	// OLD behaviour: flat 24h → stale badge fires at 100h.
	flat := enrichment.TTL(enrichment.SourceOMDb, enrichment.KindOMDb)
	require.Equal(t, 24*time.Hour, flat)
	oldOut := enrichment.Degraded(base(flat), now)
	assert.Contains(t, oldOut, enrichment.SourceOMDb, "flat 24h TTL wrongly flags OMDb stale at 100h")

	// NEW behaviour: progressive curve → NOT stale at 100h (agrees with /ratings).
	prog := omdbDegradedTTL(canon, now)
	require.Equal(t, 7*24*time.Hour, prog)
	newOut := enrichment.Degraded(base(prog), now)
	assert.NotContains(t, newOut, enrichment.SourceOMDb, "progressive TTL must NOT flag OMDb stale at 100h")

	// And /ratings agrees the series is fresh at 100h.
	assert.False(t, OMDbRatingStale(now, &syncedAt, canon.InProduction, canon.Status, canon.LastAirDate, canon.FirstAirDate),
		"/ratings OMDbRatingStale must report fresh at 100h — badge now matches")
}
