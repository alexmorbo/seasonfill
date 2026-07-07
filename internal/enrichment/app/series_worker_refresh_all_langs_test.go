package enrichment

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ---- fixtures -------------------------------------------------------

// allLangsTV builds a GetTVAllLangs payload: root fields in en-US (base
// call lang), a translations[] array with en + ru entries, and images[]
// with per-lang posters + a neutral backdrop. ruOverview lets a test blank
// the ru data.overview to exercise the per-field root fallback.
func allLangsTV(ruOverview string) *tmdb.TVResponse {
	en := "en"
	ru := "ru"
	return &tmdb.TVResponse{
		ID:           42,
		Name:         "Show EN",
		Overview:     "overview en",
		Tagline:      "tag en",
		Status:       "Returning Series",
		FirstAirDate: "2020-01-01",
		PosterPath:   "/root_poster.jpg",
		BackdropPath: "/root_bd.jpg",
		Images: &tmdb.TVImages{
			Posters: []tmdb.TVImage{
				{FilePath: "/en_poster.jpg", ISO6391: &en, VoteAverage: 8.0, VoteCount: 40},
				{FilePath: "/ru_poster.jpg", ISO6391: &ru, VoteAverage: 7.0, VoteCount: 20},
			},
			Backdrops: []tmdb.TVImage{
				{FilePath: "/neutral_bd.jpg", ISO6391: nil, VoteAverage: 6.0, VoteCount: 10},
			},
		},
		Translations: &tmdb.TVTranslations{
			Translations: []tmdb.TVTranslation{
				{ISO6391: "en", ISO31661: "US", Data: tmdb.TVTranslationData{
					Name: "Show EN", Overview: "overview en", Tagline: "tag en",
				}},
				{ISO6391: "ru", ISO31661: "RU", Data: tmdb.TVTranslationData{
					Name: "Шоу", Overview: ruOverview, Tagline: "слоган",
				}},
			},
		},
	}
}

// newAllLangsFixture wires a worker fixture for the S-B path: seeds canon at
// id=1 with the given tmdb_id and attaches Probe / MediaResolver /
// SeriesMediaTexts (any may be nil to exercise the nil-OK branches).
func newAllLangsFixture(
	t *testing.T,
	tmdbID *domain.TMDBID,
	tv *tmdb.TVResponse,
	probe freshener.Probe,
	resolver MediaResolver,
	mediaTexts SeriesMediaTextsRepo,
) *workerFixture {
	t.Helper()
	f := newWorkerFixture(t, tv, map[int]*tmdb.SeasonResponse{})
	deps := f.worker.deps
	deps.Probe = probe
	deps.MediaResolver = resolver
	deps.SeriesMediaTexts = mediaTexts
	w, err := NewSeriesWorker(deps)
	require.NoError(t, err)
	f.worker = w
	f.seedCanon(1, tmdbID)
	return f
}

// ---- tests ----------------------------------------------------------

// Both supported langs are upserted from ONE TMDB-fake call, each with its
// own localised title/overview.
func TestRefreshSeriesAllLangs_BothLangs_OneCall(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	media := &fakeSeriesMediaTextsRepo{}
	f := newAllLangsFixture(t, &tmdbID, allLangsTV("описание ру"), nil, &fakeMediaResolver{}, media)
	media.rec = f.rec

	require.NoError(t, f.worker.RefreshSeriesAllLangs(context.Background(), 1, true))

	assert.Equal(t, 1, f.tmdb.getTVHit, "exactly ONE TMDB call for all langs")
	require.Len(t, f.seriesTexts.rows, 2, "en-US + ru-RU rows")

	byLang := map[string]int{}
	for i, r := range f.seriesTexts.rows {
		byLang[r.Language] = i
	}
	require.Contains(t, byLang, "en-US")
	require.Contains(t, byLang, "ru-RU")

	en := f.seriesTexts.rows[byLang["en-US"]]
	require.NotNil(t, en.Title)
	assert.Equal(t, "Show EN", *en.Title)
	require.NotNil(t, en.Overview)
	assert.Equal(t, "overview en", *en.Overview)

	ru := f.seriesTexts.rows[byLang["ru-RU"]]
	require.NotNil(t, ru.Title)
	assert.Equal(t, "Шоу", *ru.Title)
	require.NotNil(t, ru.Overview)
	assert.Equal(t, "описание ру", *ru.Overview)

	// Per-lang posters from images[]; stamp fired once.
	require.Len(t, media.rows, 2)
	mByLang := map[string]series.SeriesMediaText{}
	for _, m := range media.rows {
		mByLang[m.Language] = m
	}
	require.NotNil(t, mByLang["en-US"].PosterAsset)
	assert.Equal(t, "/en_poster.jpg", *mByLang["en-US"].PosterAsset)
	require.NotNil(t, mByLang["ru-RU"].PosterAsset)
	assert.Equal(t, "/ru_poster.jpg", *mByLang["ru-RU"].PosterAsset)
	assert.True(t, hasCall(f.rec.list(), "Series.MarkTextSynced"))
	require.NotNil(t, f.series.rows[1].EnrichmentTextSyncedAt)
}

// Empty ru data.overview → ru Overview falls back to the root en overview
// (per-field fallback), ru Title stays Russian.
func TestRefreshSeriesAllLangs_EmptyRuOverview_RootFallback(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newAllLangsFixture(t, &tmdbID, allLangsTV(""), nil, nil, nil)

	require.NoError(t, f.worker.RefreshSeriesAllLangs(context.Background(), 1, true))

	require.Len(t, f.seriesTexts.rows, 2)
	for _, r := range f.seriesTexts.rows {
		if r.Language != "ru-RU" {
			continue
		}
		require.NotNil(t, r.Title)
		assert.Equal(t, "Шоу", *r.Title, "ru title stays Russian")
		require.NotNil(t, r.Overview)
		assert.Equal(t, "overview en", *r.Overview, "empty ru overview → root en fallback")
	}
}

// Empty translations[] → only the en-US base row is written (from root); the
// ru-RU row is absent so the probe stays stale. No error.
func TestRefreshSeriesAllLangs_EmptyTranslations_OnlyBaseRow(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := allLangsTV("описание ру")
	tv.Translations = &tmdb.TVTranslations{Translations: nil}
	media := &fakeSeriesMediaTextsRepo{}
	f := newAllLangsFixture(t, &tmdbID, tv, nil, &fakeMediaResolver{}, media)
	media.rec = f.rec

	require.NoError(t, f.worker.RefreshSeriesAllLangs(context.Background(), 1, true))

	require.Len(t, f.seriesTexts.rows, 1, "only base row when translations empty")
	assert.Equal(t, "en-US", f.seriesTexts.rows[0].Language)
	require.NotNil(t, f.seriesTexts.rows[0].Title)
	assert.Equal(t, "Show EN", *f.seriesTexts.rows[0].Title, "base row from root")
	require.Len(t, media.rows, 1, "only base media row")
	assert.Equal(t, "en-US", media.rows[0].Language)
	assert.True(t, hasCall(f.rec.list(), "Series.MarkTextSynced"), "stamp fires on base-only write")
}

// nil Translations pointer → same base-only behavior, no panic.
func TestRefreshSeriesAllLangs_NilTranslations_OnlyBaseRow(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := allLangsTV("описание ру")
	tv.Translations = nil
	f := newAllLangsFixture(t, &tmdbID, tv, nil, nil, nil)

	require.NoError(t, f.worker.RefreshSeriesAllLangs(context.Background(), 1, true))
	require.Len(t, f.seriesTexts.rows, 1)
	assert.Equal(t, "en-US", f.seriesTexts.rows[0].Language)
}

// No tmdb_id → debug no-op: no TMDB call, no upserts, no stamp, nil error.
func TestRefreshSeriesAllLangs_NoTMDBID_Skip(t *testing.T) {
	t.Parallel()
	f := newAllLangsFixture(t, nil, allLangsTV("описание ру"), nil, nil, nil)

	require.NoError(t, f.worker.RefreshSeriesAllLangs(context.Background(), 1, false))
	assert.Zero(t, f.tmdb.getTVHit, "no TMDB call when canon.TMDBID is nil")
	assert.Empty(t, f.seriesTexts.rows)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkTextSynced"))
}

// Series row missing → warn + nil, no TMDB call.
func TestRefreshSeriesAllLangs_SeriesMissing_Skip(t *testing.T) {
	t.Parallel()
	f := newAllLangsFixture(t, nil, allLangsTV("описание ру"), nil, nil, nil)
	f.worker.deps.Series = &seriesNotFoundRepo{inner: f.series}

	require.NoError(t, f.worker.RefreshSeriesAllLangs(context.Background(), 1, false))
	assert.Zero(t, f.tmdb.getTVHit)
	assert.Empty(t, f.seriesTexts.rows)
}

// Probe fresh (SectionOverview not stale) + force=false → skip TMDB.
func TestRefreshSeriesAllLangs_ProbeFresh_Skip(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SectionOverview, Stale: false, Reason: "fresh"},
	}}
	f := newAllLangsFixture(t, &tmdbID, allLangsTV("описание ру"), probe, nil, nil)

	require.NoError(t, f.worker.RefreshSeriesAllLangs(context.Background(), 1, false))
	assert.Equal(t, 1, probe.calls, "probe consulted")
	assert.Zero(t, f.tmdb.getTVHit, "fresh + force=false → skip")
	assert.Empty(t, f.seriesTexts.rows)
}

// Probe fresh but force=true → bypass gate, fetch + write.
func TestRefreshSeriesAllLangs_ForceTrue_ProbeBypass(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SectionOverview, Stale: false, Reason: "fresh"},
	}}
	f := newAllLangsFixture(t, &tmdbID, allLangsTV("описание ру"), probe, nil, nil)

	require.NoError(t, f.worker.RefreshSeriesAllLangs(context.Background(), 1, true))
	assert.Zero(t, probe.calls, "force=true MUST NOT consult probe")
	assert.Equal(t, 1, f.tmdb.getTVHit)
	require.NotEmpty(t, f.seriesTexts.rows)
}

// TMDB error → no writes, no stamp, error propagated.
func TestRefreshSeriesAllLangs_TMDBError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newAllLangsFixture(t, &tmdbID, allLangsTV("описание ру"), nil, nil, nil)
	f.tmdb.tvErr = errors.New("tmdb 500")

	err := f.worker.RefreshSeriesAllLangs(context.Background(), 1, true)
	require.Error(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.Empty(t, f.seriesTexts.rows, "tx never started")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkTextSynced"))
	assert.Nil(t, f.series.rows[1].EnrichmentTextSyncedAt)
}

// series_texts Upsert error → tx rollback drops the stamp.
func TestRefreshSeriesAllLangs_UpsertError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newAllLangsFixture(t, &tmdbID, allLangsTV("описание ру"), nil, nil, nil)
	f.worker.deps.SeriesTexts = &errorSeriesTextsRepo{
		fakeSeriesTextsRepo: f.seriesTexts,
		err:                 errors.New("upsert boom"),
	}

	err := f.worker.RefreshSeriesAllLangs(context.Background(), 1, true)
	require.Error(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkTextSynced"), "stamp NEVER written without successful UPSERT")
	assert.Nil(t, f.series.rows[1].EnrichmentTextSyncedAt)
}

// nil-safety: nil Images + nil MediaResolver + nil SeriesMediaTexts → no
// panic; base + ru text rows still written (translations present).
func TestRefreshSeriesAllLangs_NilMediaDeps_NoPanic(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := allLangsTV("описание ру")
	tv.Images = nil
	// resolver + mediaTexts nil.
	f := newAllLangsFixture(t, &tmdbID, tv, nil, nil, nil)

	require.NoError(t, f.worker.RefreshSeriesAllLangs(context.Background(), 1, true))
	require.Len(t, f.seriesTexts.rows, 2, "text rows written despite nil media deps")
	assert.False(t, hasCall(f.rec.list(), "SeriesMediaTexts.Upsert"), "media skipped when port nil")
	assert.True(t, hasCall(f.rec.list(), "Series.MarkTextSynced"))
}

// nil Images, media port present. Story 1081a — base (en-US) still gets the
// root poster/backdrop fallback; NON-base (ru-RU) poster is now STRICT
// (Images nil → no ru art → nil, not the en/root poster) to kill root-cause
// #1 (a generic root poster poisoning the ru row). Backdrop stays non-strict
// for non-base (W18-15 parity), so ru still gets the root backdrop. Both
// rows are still written (absence row persisted) with *_checked_at stamped.
func TestRefreshSeriesAllLangs_NilImages_RootPosterFallback(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := allLangsTV("описание ру")
	tv.Images = nil
	media := &fakeSeriesMediaTextsRepo{}
	f := newAllLangsFixture(t, &tmdbID, tv, nil, &fakeMediaResolver{}, media)
	media.rec = f.rec

	require.NoError(t, f.worker.RefreshSeriesAllLangs(context.Background(), 1, true))
	require.Len(t, media.rows, 2)
	byLang := map[string]series.SeriesMediaText{}
	for _, m := range media.rows {
		byLang[m.Language] = m
	}
	require.Contains(t, byLang, "en-US")
	require.Contains(t, byLang, "ru-RU")

	en := byLang["en-US"]
	require.NotNil(t, en.PosterAsset)
	assert.Equal(t, "/root_poster.jpg", *en.PosterAsset, "base poster still root-fallback")
	require.NotNil(t, en.BackdropAsset)
	assert.Equal(t, "/root_bd.jpg", *en.BackdropAsset)
	require.NotNil(t, en.PosterCheckedAt)
	require.NotNil(t, en.BackdropCheckedAt)

	ru := byLang["ru-RU"]
	assert.Nil(t, ru.PosterAsset, "Story 1081a — non-base poster is strict; no images[] → nil, NOT root")
	require.NotNil(t, ru.BackdropAsset, "non-base backdrop still root-fallback (W18-15 parity)")
	assert.Equal(t, "/root_bd.jpg", *ru.BackdropAsset)
	require.NotNil(t, ru.PosterCheckedAt, "confirmed-absent marker stamped even though poster is nil")
	require.NotNil(t, ru.BackdropCheckedAt)
}
