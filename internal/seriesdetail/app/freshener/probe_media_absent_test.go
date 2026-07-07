package freshener_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	catalogseries "github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeMediaPresence is the Story 1081b test double for
// freshener.SeriesMediaPresenceReader. Fixed (absent, checkedAt, err) —
// the table tests below don't need per-call variation.
type fakeMediaPresence struct {
	absent    bool
	checkedAt *time.Time
	err       error
}

func (f *fakeMediaPresence) PosterMarker(_ context.Context, _ domain.SeriesID, _ string) (bool, *time.Time, error) {
	return f.absent, f.checkedAt, f.err
}

// TestProbe_MediaAbsentRecheck_GeometricCurve proves the confirmed-absent
// poster re-check reuses the shipped seriesdetail.TMDBRatingStale curve
// (2d continuing / 7d <1y / 30d 1-3y / 90d 3-8y / 180d >8y), keyed on
// poster_checked_at age — NOT a consecutive-absent counter. The interval
// organically grows as the show ages past its last-air-date.
func TestProbe_MediaAbsentRecheck_GeometricCurve(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	returning := "Returning Series"
	ended := "Ended"
	freshMedia := now.Add(-time.Hour) // base SectionMedia TTL (7d/30d) stays Fresh in every case

	sixMonthsAgo := now.AddDate(0, -6, 0)
	nineYearsAgo := now.AddDate(-9, 0, 0)

	cases := []struct {
		name       string
		canon      catalogseries.Canon
		checkedAt  time.Time
		wantStale  bool
		wantReason string
	}{
		{
			name: "continuing, checked 3d ago -> stale (past 2d step)",
			canon: catalogseries.Canon{
				Hydration: catalogseries.HydrationFull, InProduction: true, Status: &returning,
				EnrichmentMediaSyncedAt: &freshMedia,
			},
			checkedAt:  now.Add(-3 * 24 * time.Hour),
			wantStale:  true,
			wantReason: "absent_poster_recheck",
		},
		{
			name: "continuing, checked 1d ago -> fresh (within 2d step)",
			canon: catalogseries.Canon{
				Hydration: catalogseries.HydrationFull, InProduction: true, Status: &returning,
				EnrichmentMediaSyncedAt: &freshMedia,
			},
			checkedAt:  now.Add(-24 * time.Hour),
			wantStale:  false,
			wantReason: "fresh",
		},
		{
			name: "ended <1y, checked 8d ago -> stale (past 7d step)",
			canon: catalogseries.Canon{
				Hydration: catalogseries.HydrationFull, Status: &ended, LastAirDate: &sixMonthsAgo,
				EnrichmentMediaSyncedAt: &freshMedia,
			},
			checkedAt:  now.Add(-8 * 24 * time.Hour),
			wantStale:  true,
			wantReason: "absent_poster_recheck",
		},
		{
			name: "ended <1y, checked 6d ago -> fresh (within 7d step)",
			canon: catalogseries.Canon{
				Hydration: catalogseries.HydrationFull, Status: &ended, LastAirDate: &sixMonthsAgo,
				EnrichmentMediaSyncedAt: &freshMedia,
			},
			checkedAt:  now.Add(-6 * 24 * time.Hour),
			wantStale:  false,
			wantReason: "fresh",
		},
		{
			name: "ended >8y, checked 100d ago -> fresh (180d step not yet elapsed)",
			canon: catalogseries.Canon{
				Hydration: catalogseries.HydrationFull, Status: &ended, LastAirDate: &nineYearsAgo,
				EnrichmentMediaSyncedAt: &freshMedia,
			},
			checkedAt:  now.Add(-100 * 24 * time.Hour),
			wantStale:  false,
			wantReason: "fresh",
		},
		{
			name: "ended >8y, checked 200d ago -> stale (interval grew to 180d cap)",
			canon: catalogseries.Canon{
				Hydration: catalogseries.HydrationFull, Status: &ended, LastAirDate: &nineYearsAgo,
				EnrichmentMediaSyncedAt: &freshMedia,
			},
			checkedAt:  now.Add(-200 * 24 * time.Hour),
			wantStale:  true,
			wantReason: "absent_poster_recheck",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkedAt := tc.checkedAt
			probe := mustProbe(t, freshener.DBProbeConfig{
				Series:           &stubSeries{canon: tc.canon},
				SeriesTexts:      &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
				Seasons:          &stubSeasons{},
				Now:              func() time.Time { return now },
				MediaPresence:    &fakeMediaPresence{absent: true, checkedAt: &checkedAt},
				MediaAbsentStale: seriesdetail.TMDBRatingStale,
			})
			verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
			require.NoError(t, err)
			assertVerdict(t, verdicts, freshener.SectionMedia, tc.wantStale, tc.wantReason)
		})
	}
}

// TestProbe_MediaPresent_NeverEscalates proves the re-check ONLY escalates
// the confirmed-absent case — a present poster keeps SectionMedia Fresh
// regardless of how old poster_checked_at is (normal present-media
// freshness is unaffected by Story 1081b).
func TestProbe_MediaPresent_NeverEscalates(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	returning := "Returning Series"
	freshMedia := now.Add(-time.Hour)
	veryOldChecked := now.Add(-200 * 24 * time.Hour)

	probe := mustProbe(t, freshener.DBProbeConfig{
		Series: &stubSeries{canon: catalogseries.Canon{
			Hydration: catalogseries.HydrationFull, InProduction: true, Status: &returning,
			EnrichmentMediaSyncedAt: &freshMedia,
		}},
		SeriesTexts:      &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:          &stubSeasons{},
		Now:              func() time.Time { return now },
		MediaPresence:    &fakeMediaPresence{absent: false, checkedAt: &veryOldChecked},
		MediaAbsentStale: seriesdetail.TMDBRatingStale,
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	assertVerdict(t, verdicts, freshener.SectionMedia, false, "fresh")
}

// TestProbe_MediaAbsentRecheck_FeatureFlagOff proves a nil MediaPresence OR
// a nil MediaAbsentStale disables the re-check entirely — SectionMedia
// falls back to the plain TTL verdict (production minimal-boot / test
// wiring that doesn't inject either func).
func TestProbe_MediaAbsentRecheck_FeatureFlagOff(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	ended := "Ended"
	nineYearsAgo := now.AddDate(-9, 0, 0)
	freshMedia := now.Add(-time.Hour)
	veryOldChecked := now.Add(-200 * 24 * time.Hour)
	canon := catalogseries.Canon{
		Hydration: catalogseries.HydrationFull, Status: &ended, LastAirDate: &nineYearsAgo,
		EnrichmentMediaSyncedAt: &freshMedia,
	}

	t.Run("MediaPresence nil", func(t *testing.T) {
		t.Parallel()
		probe := mustProbe(t, freshener.DBProbeConfig{
			Series:           &stubSeries{canon: canon},
			SeriesTexts:      &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
			Seasons:          &stubSeasons{},
			Now:              func() time.Time { return now },
			MediaAbsentStale: seriesdetail.TMDBRatingStale,
		})
		verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
		require.NoError(t, err)
		assertVerdict(t, verdicts, freshener.SectionMedia, false, "fresh")
	})

	t.Run("MediaAbsentStale nil", func(t *testing.T) {
		t.Parallel()
		probe := mustProbe(t, freshener.DBProbeConfig{
			Series:        &stubSeries{canon: canon},
			SeriesTexts:   &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
			Seasons:       &stubSeasons{},
			Now:           func() time.Time { return now },
			MediaPresence: &fakeMediaPresence{absent: true, checkedAt: &veryOldChecked},
		})
		verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
		require.NoError(t, err)
		assertVerdict(t, verdicts, freshener.SectionMedia, false, "fresh")
	})
}

// TestProbe_MediaPresenceError_FailOpenProbeError proves a PosterMarker IO
// error fails OPEN (Radarr lesson) — never look Fresh on an error.
func TestProbe_MediaPresenceError_FailOpenProbeError(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	returning := "Returning Series"
	freshMedia := now.Add(-time.Hour)

	probe := mustProbe(t, freshener.DBProbeConfig{
		Series: &stubSeries{canon: catalogseries.Canon{
			Hydration: catalogseries.HydrationFull, InProduction: true, Status: &returning,
			EnrichmentMediaSyncedAt: &freshMedia,
		}},
		SeriesTexts:      &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:          &stubSeasons{},
		Now:              func() time.Time { return now },
		MediaPresence:    &fakeMediaPresence{err: errors.New("boom")},
		MediaAbsentStale: seriesdetail.TMDBRatingStale,
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	assertVerdict(t, verdicts, freshener.SectionMedia, true, "probe_error")
}
