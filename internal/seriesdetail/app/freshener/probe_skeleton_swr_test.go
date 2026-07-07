package freshener_test

import (
	"context"
	"testing"
	"time"

	catalogseries "github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
)

// W18-16: SectionSkeleton must gate on the dedicated skeleton_synced_at clock via
// the SHARED progressive age curve (seriesdetail.TMDBRatingStale) — NOT the
// permanently-stale enrichment_tmdb_synced_at. Wiring the real curve here proves
// the probe honours the injected fn against skeleton_synced_at.
func TestProbe_Skeleton_ProgressiveTTL_OnSkeletonClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	returning := "Returning Series"
	ended := "Ended"
	longAgo := now.AddDate(-10, 0, 0) // >8y ended → 180d TTL band

	cases := []struct {
		name       string
		canon      catalogseries.Canon
		wantStale  bool
		wantReason string
	}{
		{
			name: "continuing, skeleton synced 1d ago → fresh (2d TTL)",
			canon: catalogseries.Canon{
				Hydration: catalogseries.HydrationFull, InProduction: true, Status: &returning,
				SkeletonSyncedAt: new(now.Add(-24 * time.Hour)),
				// stale sentinel on the OLD column proves it's ignored:
				EnrichmentTMDBSyncedAt: new(now.AddDate(-1, 0, 0)),
			},
			wantStale: false, wantReason: "fresh",
		},
		{
			name: "continuing, skeleton synced 3d ago → stale (past 2d TTL)",
			canon: catalogseries.Canon{
				Hydration: catalogseries.HydrationFull, InProduction: true, Status: &returning,
				SkeletonSyncedAt: new(now.Add(-72 * time.Hour)),
			},
			wantStale: true, wantReason: "expired",
		},
		{
			name: "ended >8y, skeleton synced 100d ago → fresh (180d TTL band)",
			canon: catalogseries.Canon{
				Hydration: catalogseries.HydrationFull, Status: &ended, LastAirDate: &longAgo,
				SkeletonSyncedAt: new(now.Add(-100 * 24 * time.Hour)),
			},
			wantStale: false, wantReason: "fresh",
		},
		{
			name: "ended >8y, skeleton synced 200d ago → stale (past 180d TTL)",
			canon: catalogseries.Canon{
				Hydration: catalogseries.HydrationFull, Status: &ended, LastAirDate: &longAgo,
				SkeletonSyncedAt: new(now.Add(-200 * 24 * time.Hour)),
			},
			wantStale: true, wantReason: "expired",
		},
		{
			name: "never skeleton-synced → stale/never (nil clock)",
			canon: catalogseries.Canon{
				Hydration: catalogseries.HydrationFull, InProduction: true, Status: &returning,
				EnrichmentTMDBSyncedAt: new(now.Add(-time.Hour)), // fresh on OLD col — must NOT save it
			},
			wantStale: true, wantReason: "never",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			probe := mustProbe(t, freshener.DBProbeConfig{
				Series:        &stubSeries{canon: tc.canon},
				SeriesTexts:   &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
				Seasons:       &stubSeasons{},
				Now:           func() time.Time { return now },
				SkeletonStale: seriesdetail.TMDBRatingStale, // the shipped shared curve
			})
			verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
			if err != nil {
				t.Fatalf("IsStale: %v", err)
			}
			assertVerdict(t, verdicts, freshener.SectionSkeleton, tc.wantStale, tc.wantReason)
		})
	}
}
