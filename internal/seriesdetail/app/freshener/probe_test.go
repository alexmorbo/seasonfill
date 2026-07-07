package freshener_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	catalogseries "github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	dataports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

// ---- stubs ----

type stubSeries struct {
	canon catalogseries.Canon
	err   error
}

func (s *stubSeries) Get(_ context.Context, _ domain.SeriesID) (catalogseries.Canon, error) {
	if s.err != nil {
		return catalogseries.Canon{}, s.err
	}
	return s.canon, nil
}

type stubTexts struct {
	row catalogseries.SeriesText
	err error
}

func (s *stubTexts) GetWithFallback(_ context.Context, _ domain.SeriesID, _ string) (catalogseries.SeriesText, error) {
	if s.err != nil {
		return catalogseries.SeriesText{}, s.err
	}
	return s.row, nil
}

type stubEpisodeTexts struct {
	covered, total int
	bySeason       map[int]struct{ covered, total int }
	err            error
}

func (s *stubEpisodeTexts) CoverageBySeriesSeason(_ context.Context, _ domain.SeriesID, seasonNumber int, _ string) (int, int, error) {
	if s.bySeason != nil {
		if c, ok := s.bySeason[seasonNumber]; ok {
			return c.covered, c.total, s.err
		}
	}
	return s.covered, s.total, s.err
}

type stubSeriesTextsCoverage struct {
	covered, total int
	err            error
}

func (s *stubSeriesTextsCoverage) RecommendationsCoverage(_ context.Context, _ domain.SeriesID, _ string) (int, int, error) {
	return s.covered, s.total, s.err
}

type stubSeasons struct {
	syncedByNumber map[int]*time.Time
	notFound       map[int]bool
	ioErr          map[int]error
}

func (s *stubSeasons) GetEpisodesSyncedAt(_ context.Context, _ domain.SeriesID, n int) (*time.Time, error) {
	if e, ok := s.ioErr[n]; ok {
		return nil, e
	}
	if s.notFound[n] {
		return nil, dataports.ErrNotFound
	}
	return s.syncedByNumber[n], nil
}

// ---- helpers ----

func mustLang(t *testing.T, s string) values.LanguageTag {
	t.Helper()
	if s == "" {
		return values.LanguageTag{}
	}
	v, err := values.NewLanguageTag(s)
	require.NoError(t, err)
	return v
}

func mustProbe(t *testing.T, cfg freshener.DBProbeConfig) *freshener.DBProbe {
	t.Helper()
	p, err := freshener.NewDBProbe(cfg)
	require.NoError(t, err)
	return p
}

// ---- tests ----

func TestNewDBProbe_RequiredFields(t *testing.T) {
	t.Parallel()
	_, err := freshener.NewDBProbe(freshener.DBProbeConfig{})
	require.Error(t, err)
}

// Story 548-style fresh-and-all-good baseline.
func TestProbe_AllFresh_DenseOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		ID:                      1,
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		SkeletonSyncedAt:        &fresh, // W18-16: skeleton gate now reads this clock
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: canon},
		SeriesTexts: &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:     &stubSeasons{},
		Now:         func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	require.Len(t, verdicts, 5)
	for _, v := range verdicts {
		assert.False(t, v.Stale, "section %q expected fresh, got reason=%q", v.Section, v.Reason)
		assert.Equal(t, "fresh", v.Reason, "section %q", v.Section)
	}
}

func TestProbe_DenseOrder(t *testing.T) {
	t.Parallel()
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: catalogseries.Canon{Hydration: catalogseries.HydrationStub}},
		SeriesTexts: &stubTexts{},
		Seasons:     &stubSeasons{},
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	require.Len(t, verdicts, 5)
	for i, want := range freshener.FixedSections {
		assert.Equal(t, want, verdicts[i].Section, "i=%d", i)
	}
}

func TestProbe_StubCanon_FailOpen(t *testing.T) {
	t.Parallel()
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: catalogseries.Canon{Hydration: catalogseries.HydrationStub}},
		SeriesTexts: &stubTexts{},
		Seasons:     &stubSeasons{},
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), []int{1, 2})
	require.NoError(t, err)
	require.Len(t, verdicts, 7)
	for _, v := range verdicts {
		assert.True(t, v.Stale)
		assert.Equal(t, "stub", v.Reason)
	}
}

func TestProbe_CanonNotFound_FailOpenStub(t *testing.T) {
	t.Parallel()
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{err: dataports.ErrNotFound},
		SeriesTexts: &stubTexts{},
		Seasons:     &stubSeasons{},
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	require.Len(t, verdicts, 5)
	for _, v := range verdicts {
		assert.True(t, v.Stale)
		assert.Equal(t, "stub", v.Reason)
	}
}

func TestProbe_CanonReadError_FailOpenProbeError(t *testing.T) {
	t.Parallel()
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{err: errors.New("db boom")},
		SeriesTexts: &stubTexts{},
		Seasons:     &stubSeasons{},
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	require.Len(t, verdicts, 5)
	for _, v := range verdicts {
		assert.True(t, v.Stale)
		assert.Equal(t, "probe_error", v.Reason)
	}
}

func TestProbe_OverviewNever(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:              catalogseries.HydrationFull,
		Status:                 &status,
		EnrichmentTMDBSyncedAt: &fresh,
		SkeletonSyncedAt:       &fresh, // W18-16: skeleton fresh via dedicated clock
		// EnrichmentTextSyncedAt nil → overview never
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: canon},
		SeriesTexts: &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:     &stubSeasons{},
		Now:         func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	require.Len(t, verdicts, 5)
	assertVerdict(t, verdicts, freshener.SectionSkeleton, false, "fresh")
	assertVerdict(t, verdicts, freshener.SectionOverview, true, "never")
	assertVerdict(t, verdicts, freshener.SectionCast, false, "fresh")
}

func TestProbe_OverviewExpired(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	old := now.Add(-25 * time.Hour) // > 24h floor, BUT status-aware needs Returning
	stale := now.Add(-8 * 24 * time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &old,   // 25h ended → ttl status-aware says fresh
		SkeletonSyncedAt:        &old,   // W18-16: skeleton gate reads this clock
		EnrichmentTextSyncedAt:  &stale, // 8d > 7d ceiling → expired
		EnrichmentCastSyncedAt:  &old,
		EnrichmentRecsSyncedAt:  &old,
		EnrichmentMediaSyncedAt: &old,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: canon},
		SeriesTexts: &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:     &stubSeasons{},
		Now:         func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	assertVerdict(t, verdicts, freshener.SectionOverview, true, "expired")
	assertVerdict(t, verdicts, freshener.SectionSkeleton, false, "fresh") // status-aware: ended + 25h floor not crossed via status path
}

func TestProbe_StatusAware_ReturningTriggersEarly(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	floorCrossed := now.Add(-25 * time.Hour)
	status := "Returning Series"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &floorCrossed,
		SkeletonSyncedAt:        &floorCrossed, // W18-16: skeleton gate reads this clock
		EnrichmentTextSyncedAt:  &floorCrossed,
		EnrichmentCastSyncedAt:  &floorCrossed,
		EnrichmentRecsSyncedAt:  &floorCrossed,
		EnrichmentMediaSyncedAt: &floorCrossed,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: canon},
		SeriesTexts: &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:     &stubSeasons{},
		Now:         func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	assertVerdict(t, verdicts, freshener.SectionSkeleton, true, "status")
	assertVerdict(t, verdicts, freshener.SectionOverview, true, "status")
	// Cast policy floor=7d, so 25h does not cross — fresh.
	assertVerdict(t, verdicts, freshener.SectionCast, false, "fresh")
}

func TestProbe_OverviewMissingLang_OverridesFreshTTL(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh, // TTL says fresh, but ru-RU row absent
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: canon},
		SeriesTexts: &stubTexts{err: dataports.ErrNotFound},
		Seasons:     &stubSeasons{},
		Now:         func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	assertVerdict(t, verdicts, freshener.SectionOverview, true, "missing_lang")
	assertVerdict(t, verdicts, freshener.SectionCast, true, "missing_lang")
}

func TestProbe_MissingLang_FiresForEnUS_WhenBaseRowMissing(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: canon},
		SeriesTexts: &stubTexts{err: dataports.ErrNotFound},
		Seasons:     &stubSeasons{},
		Now:         func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "en-US"), nil)
	require.NoError(t, err)
	// W15-4: en-US no longer skips the missing-lang check — a missing
	// base-lang row now marks overview+cast stale to re-localize.
	assertVerdict(t, verdicts, freshener.SectionOverview, true, "missing_lang")
	assertVerdict(t, verdicts, freshener.SectionCast, true, "missing_lang")
}

func TestProbe_MissingLang_EnUS_RowPresent_NotStale(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: canon},
		SeriesTexts: &stubTexts{row: catalogseries.SeriesText{Language: "en-US"}},
		Seasons:     &stubSeasons{},
		Now:         func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "en-US"), nil)
	require.NoError(t, err)
	// en-US row present → no false missing_lang trigger.
	assertVerdict(t, verdicts, freshener.SectionOverview, false, "fresh")
	assertVerdict(t, verdicts, freshener.SectionCast, false, "fresh")
}

func TestProbe_MissingLang_SkippedForEmptyLang(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: canon},
		SeriesTexts: &stubTexts{err: dataports.ErrNotFound},
		Seasons:     &stubSeasons{},
		Now:         func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, ""), nil)
	require.NoError(t, err)
	assertVerdict(t, verdicts, freshener.SectionOverview, false, "fresh")
}

func TestProbe_SparseSeasonVerdicts(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	stale := now.Add(-30 * 24 * time.Hour) // > ceiling
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	seasons := &stubSeasons{
		syncedByNumber: map[int]*time.Time{
			8: &fresh, // fresh
			9: &stale, // expired
		},
		notFound: map[int]bool{
			10: true, // never
		},
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: canon},
		SeriesTexts: &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:     seasons,
		Now:         func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), []int{8, 9, 10})
	require.NoError(t, err)
	require.Len(t, verdicts, 8) // 5 fixed + 3 sparse
	assertVerdict(t, verdicts, freshener.SeasonSection(8), false, "fresh")
	assertVerdict(t, verdicts, freshener.SeasonSection(9), true, "expired")
	assertVerdict(t, verdicts, freshener.SeasonSection(10), true, "never")
}

func TestProbe_SeasonMissingEpisodesLang(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	seasons := &stubSeasons{
		syncedByNumber: map[int]*time.Time{8: &fresh},
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:               &stubSeries{canon: canon},
		SeriesTexts:          &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:              seasons,
		EpisodeTextsCoverage: &stubEpisodeTexts{covered: 18, total: 100}, // 18% < 80%
		Now:                  func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), []int{8})
	require.NoError(t, err)
	assertVerdict(t, verdicts, freshener.SeasonSection(8), true, "missing_episodes_lang")
}

func TestProbe_SeasonMissingEpisodesLang_EnUS(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	seasons := &stubSeasons{
		syncedByNumber: map[int]*time.Time{8: &fresh},
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:               &stubSeries{canon: canon},
		SeriesTexts:          &stubTexts{row: catalogseries.SeriesText{Language: "en-US"}},
		Seasons:              seasons,
		EpisodeTextsCoverage: &stubEpisodeTexts{covered: 0, total: 100}, // 0% < 80%
		Now:                  func() time.Time { return now },
	})
	// W15-4: en-US now enters the episode-coverage path — a genuine gap
	// marks the season stale to re-localize episode_texts.
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "en-US"), []int{8})
	require.NoError(t, err)
	assertVerdict(t, verdicts, freshener.SeasonSection(8), true, "missing_episodes_lang")
}

func TestProbe_SeasonEpisodesLang_EnUS_HighCoverage_Fresh(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	seasons := &stubSeasons{
		syncedByNumber: map[int]*time.Time{8: &fresh},
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:               &stubSeries{canon: canon},
		SeriesTexts:          &stubTexts{row: catalogseries.SeriesText{Language: "en-US"}},
		Seasons:              seasons,
		EpisodeTextsCoverage: &stubEpisodeTexts{covered: 100, total: 100}, // ~100% scan-seeded
		Now:                  func() time.Time { return now },
	})
	// NEGATIVE: en-US episode_texts are scan-seeded ~100% → no false storm.
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "en-US"), []int{8})
	require.NoError(t, err)
	assertVerdict(t, verdicts, freshener.SeasonSection(8), false, "fresh")
}

// W16-7: coverage is now per-season. A fully localized season 1 must be
// fresh even while season 2 is empty — the old series-wide query kept
// re-flagging season 1 until the whole series crossed the threshold.
func TestProbe_PerSeasonCoverage_Season1Fresh_Season2Stale(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	seasons := &stubSeasons{
		syncedByNumber: map[int]*time.Time{1: &fresh, 2: &fresh},
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: canon},
		SeriesTexts: &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:     seasons,
		EpisodeTextsCoverage: &stubEpisodeTexts{
			bySeason: map[int]struct{ covered, total int }{
				1: {covered: 100, total: 100}, // fully localized
				2: {covered: 0, total: 100},   // empty
			},
		},
		Now: func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), []int{1, 2})
	require.NoError(t, err)
	assertVerdict(t, verdicts, freshener.SeasonSection(1), false, "fresh")
	assertVerdict(t, verdicts, freshener.SeasonSection(2), true, "missing_episodes_lang")
}

func TestProbe_NoSeasonNumbers_EmitsFixedOnly(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{canon: canon},
		SeriesTexts: &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:     &stubSeasons{},
		Now:         func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	assert.Len(t, verdicts, 5)
}

func TestProbe_ContextCanceledSurfacedAsError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:      &stubSeries{},
		SeriesTexts: &stubTexts{},
		Seasons:     &stubSeasons{},
	})
	_, err := probe.IsStale(ctx, 1, mustLang(t, "ru-RU"), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// Story 566 — SectionRecommendations coverage check parity with
// SectionOverview/Cast missing_lang and SeasonMissingEpisodesLang.

func TestProbe_RecommendationsCoverageHigh_Fresh(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:              &stubSeries{canon: canon},
		SeriesTexts:         &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:             &stubSeasons{},
		SeriesTextsCoverage: &stubSeriesTextsCoverage{covered: 20, total: 20}, // 100% ≥ 80%
		Now:                 func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	assertVerdict(t, verdicts, freshener.SectionRecommendations, false, "fresh")
}

func TestProbe_RecommendationsCoverageLow_StaleMissingRecsLang(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh, // TTL says fresh — coverage must override
		EnrichmentMediaSyncedAt: &fresh,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:              &stubSeries{canon: canon},
		SeriesTexts:         &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:             &stubSeasons{},
		SeriesTextsCoverage: &stubSeriesTextsCoverage{covered: 4, total: 20}, // 20% < 80% (series 691 live)
		Now:                 func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	assertVerdict(t, verdicts, freshener.SectionRecommendations, true, "missing_recs_lang")
}

func TestProbe_RecommendationsCoverage_EnUS_EntersCoverage(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:              &stubSeries{canon: canon},
		SeriesTexts:         &stubTexts{row: catalogseries.SeriesText{Language: "en-US"}},
		Seasons:             &stubSeasons{},
		SeriesTextsCoverage: &stubSeriesTextsCoverage{covered: 0, total: 20}, // 0% < 80%
		Now:                 func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "en-US"), nil)
	require.NoError(t, err)
	// W15-3: en-US no longer skips the recs coverage path — it enters it and
	// fires A3b for its own uncovered language.
	assertVerdict(t, verdicts, freshener.SectionRecommendations, true, "missing_recs_lang")
}

func TestProbe_RecommendationsCoverage_ZeroRecs_Fresh(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:              &stubSeries{canon: canon},
		SeriesTexts:         &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:             &stubSeasons{},
		SeriesTextsCoverage: &stubSeriesTextsCoverage{covered: 0, total: 0}, // no recs at all
		Now:                 func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	// total == 0 → skip check → TTL-only → fresh.
	assertVerdict(t, verdicts, freshener.SectionRecommendations, false, "fresh")
}

func TestProbe_RecommendationsCoverage_QueryError_FailOpenProbeError(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}
	probe := mustProbe(t, freshener.DBProbeConfig{
		Series:              &stubSeries{canon: canon},
		SeriesTexts:         &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:             &stubSeasons{},
		SeriesTextsCoverage: &stubSeriesTextsCoverage{err: errors.New("db boom")},
		Now:                 func() time.Time { return now },
	})
	verdicts, err := probe.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	// Fail-open → Stale probe_error.
	assertVerdict(t, verdicts, freshener.SectionRecommendations, true, "probe_error")
}

func TestProbe_RecommendationsCoverage_ThresholdBoundary(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour)
	status := "Ended"
	canon := catalogseries.Canon{
		Hydration:               catalogseries.HydrationFull,
		Status:                  &status,
		EnrichmentTMDBSyncedAt:  &fresh,
		EnrichmentTextSyncedAt:  &fresh,
		EnrichmentCastSyncedAt:  &fresh,
		EnrichmentRecsSyncedAt:  &fresh,
		EnrichmentMediaSyncedAt: &fresh,
	}

	// Exactly 80% (16/20) → NOT stale (strict < comparison).
	probeAt := mustProbe(t, freshener.DBProbeConfig{
		Series:              &stubSeries{canon: canon},
		SeriesTexts:         &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:             &stubSeasons{},
		SeriesTextsCoverage: &stubSeriesTextsCoverage{covered: 16, total: 20},
		Now:                 func() time.Time { return now },
	})
	v, err := probeAt.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	assertVerdict(t, v, freshener.SectionRecommendations, false, "fresh")

	// 79% (15/19 → 78.9%) → Stale.
	probeBelow := mustProbe(t, freshener.DBProbeConfig{
		Series:              &stubSeries{canon: canon},
		SeriesTexts:         &stubTexts{row: catalogseries.SeriesText{Language: "ru-RU"}},
		Seasons:             &stubSeasons{},
		SeriesTextsCoverage: &stubSeriesTextsCoverage{covered: 15, total: 19},
		Now:                 func() time.Time { return now },
	})
	v2, err := probeBelow.IsStale(context.Background(), 1, mustLang(t, "ru-RU"), nil)
	require.NoError(t, err)
	assertVerdict(t, v2, freshener.SectionRecommendations, true, "missing_recs_lang")
}

// assertVerdict finds the (single) verdict for section in verdicts and
// asserts (stale, reason). Helps the table tests stay readable when the
// DENSE+SPARSE order is the system-under-test.
func assertVerdict(t *testing.T, verdicts []freshener.SectionVerdict, section freshener.Section, wantStale bool, wantReason string) {
	t.Helper()
	for _, v := range verdicts {
		if v.Section == section {
			assert.Equal(t, wantStale, v.Stale, "section=%q reason=%q", section, v.Reason)
			assert.Equal(t, wantReason, v.Reason, "section=%q", section)
			return
		}
	}
	t.Fatalf("no verdict for section %q", section)
}
