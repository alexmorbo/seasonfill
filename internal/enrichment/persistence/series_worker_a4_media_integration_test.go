package persistence

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	enrichmentapp "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// a4FakeTMDB serves a fixed GetTVAllLangs payload for the A4 media-writer
// integration test. Only GetTVAllLangs is exercised (A4's single call); the
// others panic so any drift surfaces loud. Implements enrichmentapp.TMDBClient.
type a4FakeTMDB struct {
	resp *tmdb.TVResponse
}

func (f *a4FakeTMDB) GetTV(_ context.Context, _ int64, _ string) (*tmdb.TVResponse, error) {
	return f.resp, nil
}
func (f *a4FakeTMDB) GetTVAllLangs(_ context.Context, _ int64) (*tmdb.TVResponse, error) {
	return f.resp, nil
}
func (f *a4FakeTMDB) GetSeason(_ context.Context, _ int64, _ int, _ string) (*tmdb.SeasonResponse, error) {
	panic("a4FakeTMDB.GetSeason should not be called in A4 integration test")
}
func (f *a4FakeTMDB) GetPerson(_ context.Context, _ int64, _ string) (*tmdb.PersonResponse, error) {
	panic("a4FakeTMDB.GetPerson should not be called in A4 integration test")
}
func (f *a4FakeTMDB) FindByTVDB(_ context.Context, _ domain.TVDBID) (*tmdb.FindResponse, error) {
	panic("a4FakeTMDB.FindByTVDB should not be called in A4 integration test")
}

// a4FakeMediaResolver mints a deterministic hash per non-empty rawPath (Story
// 347 unified-resolve shape). No DB write here — the integration test asserts
// on the raw poster/backdrop paths A4 persists, not the media_assets pending row.
type a4FakeMediaResolver struct{}

func (a4FakeMediaResolver) Resolve(_ context.Context, rawPath *string, _, _ string) *string {
	if rawPath == nil || *rawPath == "" {
		return nil
	}
	h := "sha256:mock:" + *rawPath
	return &h
}

// a4WorkerDeps wires a SeriesWorker with REAL series/seasons/series_media_texts/
// season_media_texts repositories + a real gorm Tx, and nop fakes (from
// a3b_integration_helpers_test.go) for the ports A4 never touches. Seasons is
// REAL (A4 upserts seasons), unlike the A3b harness.
func a4WorkerDeps(
	t *testing.T,
	fakeTMDB enrichmentapp.TMDBClient,
	seriesRepo *SeriesRepository,
	seasonsRepo *SeasonsRepository,
	textsRepo *SeriesTextsRepository,
	seriesMedia *SeriesMediaTextsRepository,
	seasonMedia *SeasonMediaTextsRepository,
	tx enrichmentapp.Transactor,
	clock func() time.Time,
) enrichmentapp.SeriesWorkerDeps {
	t.Helper()
	return enrichmentapp.SeriesWorkerDeps{
		TMDB:             fakeTMDB,
		Tx:               tx,
		Languages:        []string{"en-US", "ru-RU"},
		Series:           seriesRepo,
		SeriesTexts:      textsRepo,
		Seasons:          seasonsRepo,
		SeriesMediaTexts: seriesMedia,
		SeasonMediaTexts: seasonMedia,
		MediaResolver:    a4FakeMediaResolver{},
		Episodes:         nopEpisodesRepo{},
		EpisodeTexts:     nopEpisodeTextsRepo{},
		People:           nopPeopleRepo{},
		PersonCredits:    nopPersonCredits{},
		Genres:           nopGenres{},
		Keywords:         nopKeywords{},
		Networks:         nopNetworks{},
		Companies:        nopCompanies{},
		Videos:           nopVideos{},
		ContentRatings:   nopContentRatings{},
		ExternalIDs:      nopExternalIDs{},
		Recommendations:  nopRecommendations{},
		EnrichmentErrors: nopEnrichmentErrors{},
		Logger:           slog.Default(),
		Clock:            clock,
	}
}

// nopRecommendations satisfies RecommendationsRepoPort — A4 never calls Set.
type nopRecommendations struct{}

func (nopRecommendations) Set(_ context.Context, _ domain.SeriesID, _ []domain.SeriesID) error {
	panic("nopRecommendations.Set called")
}

// TestA4RefreshMediaAssets_WritesPerLangArt_Integration — W15-7 MANDATORY
// ACCEPTANCE. Post-E3b, A4 wrote NO visible art (canon media columns dropped,
// side-tables never populated) yet stamped MarkMediaSynced → SectionMedia
// self-suppressed on a silent no-media hole. This end-to-end test closes that
// via the CI loop: it asserts A4 now persists series_media_texts (all langs
// with a strict ru poster) + base-lang season_media_texts, and the "no-media"
// count for the series is zero after the pass.
func TestA4RefreshMediaAssets_WritesPerLangArt_Integration(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			ctx := context.Background()

			seriesRepo := NewSeriesRepository(gdb)
			seasonsRepo := NewSeasonsRepository(gdb)
			textsRepo := NewSeriesTextsRepository(gdb)
			seriesMedia := NewSeriesMediaTextsRepository(gdb)
			seasonMedia := NewSeasonMediaTextsRepository(gdb)
			tx := &inlineTransactor{db: gdb}
			fixedClock := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

			// 1. Seed a series with tmdb_id + a pre-existing MarkMediaSynced
			//    stamp and ZERO media rows (mimics the silent-hole live state).
			tmdbID := domain.TMDBID(31)
			canon := sampleCanon("A4 Media Series")
			canon.TMDBID = &tmdbID
			seriesID, err := seriesRepo.Upsert(ctx, canon)
			require.NoError(t, err)
			require.NoError(t, seriesRepo.MarkMediaSynced(ctx, seriesID, fixedClock.Add(-time.Hour)))

			// Sanity — zero media rows before the pass.
			require.Equal(t, 0, countSeriesMediaRows(t, gdb, seriesID), "pre-state: no series_media_texts rows")

			// 2. TMDB payload: root poster/backdrop + ru strict poster/backdrop +
			//    2 seasons with posters (+ 1 empty-poster season → filtered).
			en := "en"
			ru := "ru"
			resp := &tmdb.TVResponse{
				ID:           int64(tmdbID),
				Name:         "A4 Show",
				Overview:     "ov",
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
						{FilePath: "/ru_bd.jpg", ISO6391: &ru, VoteAverage: 6.0, VoteCount: 10},
					},
				},
				Seasons: []tmdb.TVSeasonStub{
					{ID: 9001, SeasonNumber: 1, AirDate: "2020-01-01", PosterPath: "/s1.jpg"},
					{ID: 9002, SeasonNumber: 2, AirDate: "2020-02-01", PosterPath: ""}, // filtered
					{ID: 9003, SeasonNumber: 3, AirDate: "2020-03-01", PosterPath: "/s3.jpg"},
				},
			}
			worker, err := enrichmentapp.NewSeriesWorker(a4WorkerDeps(
				t, &a4FakeTMDB{resp: resp},
				seriesRepo, seasonsRepo, textsRepo, seriesMedia, seasonMedia,
				tx, func() time.Time { return fixedClock },
			))
			require.NoError(t, err)

			// 3. EXECUTE.
			require.NoError(t, worker.RefreshMediaAssets(ctx, seriesID, "en-US", true))

			// 4a. series_media_texts — en-US (root fallback / en pick) + ru-RU
			//     (strict ru pick) rows both present with a poster.
			enRow, err := seriesMedia.Get(ctx, seriesID, "en-US")
			require.NoError(t, err)
			require.NotNil(t, enRow.PosterAsset, "en-US series_media_texts poster present")
			assert.Equal(t, "/en_poster.jpg", *enRow.PosterAsset)

			ruRow, err := seriesMedia.Get(ctx, seriesID, "ru-RU")
			require.NoError(t, err, "ru-RU row written from strict ru images")
			require.NotNil(t, ruRow.PosterAsset)
			assert.Equal(t, "/ru_poster.jpg", *ruRow.PosterAsset)
			require.NotNil(t, ruRow.BackdropAsset)
			assert.Equal(t, "/ru_bd.jpg", *ruRow.BackdropAsset)

			// 4b. "no-media" count for the series = 0 after the pass.
			assert.Positive(t, countSeriesMediaRows(t, gdb, seriesID),
				"series_media_texts populated → no silent no-media hole")

			// 4c. season_media_texts — base-lang en-US rows for the 2
			//     non-empty-poster seasons; season 2 (empty poster) absent.
			s1, err := seasonMedia.Get(ctx, seriesID, 1, "en-US")
			require.NoError(t, err)
			require.NotNil(t, s1.PosterAsset)
			assert.Equal(t, "/s1.jpg", *s1.PosterAsset)
			s3, err := seasonMedia.Get(ctx, seriesID, 3, "en-US")
			require.NoError(t, err)
			require.NotNil(t, s3.PosterAsset)
			assert.Equal(t, "/s3.jpg", *s3.PosterAsset)
			_, err = seasonMedia.Get(ctx, seriesID, 2, "en-US")
			require.ErrorIs(t, err, ports.ErrNotFound, "empty-poster season 2 → no season_media_texts row")

			// 4d. Stamp still present (re-stamped by the pass).
			after, err := seriesRepo.Get(ctx, seriesID)
			require.NoError(t, err)
			require.NotNil(t, after.EnrichmentMediaSyncedAt)
			assert.Equal(t, fixedClock.Unix(), after.EnrichmentMediaSyncedAt.Unix())
		})
	}
}

// TestA4RefreshMediaAssets_EmptyPayload_NoRowsNoCrash_Integration — NEGATIVE:
// a TMDB payload with empty poster/backdrop, nil images, and empty-poster
// seasons must write ZERO media rows (a base row with no art is useless) and
// NOT crash; the stamp still fires (no-work-needed sync).
func TestA4RefreshMediaAssets_EmptyPayload_NoRowsNoCrash_Integration(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			ctx := context.Background()

			seriesRepo := NewSeriesRepository(gdb)
			seasonsRepo := NewSeasonsRepository(gdb)
			textsRepo := NewSeriesTextsRepository(gdb)
			seriesMedia := NewSeriesMediaTextsRepository(gdb)
			seasonMedia := NewSeasonMediaTextsRepository(gdb)
			tx := &inlineTransactor{db: gdb}
			fixedClock := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

			tmdbID := domain.TMDBID(77)
			canon := sampleCanon("A4 Empty Series")
			canon.TMDBID = &tmdbID
			seriesID, err := seriesRepo.Upsert(ctx, canon)
			require.NoError(t, err)

			resp := &tmdb.TVResponse{
				ID:           int64(tmdbID),
				Name:         "Empty",
				Status:       "Returning Series",
				FirstAirDate: "2020-01-01",
				PosterPath:   "",
				BackdropPath: "",
				Images:       nil,
				Seasons: []tmdb.TVSeasonStub{
					{ID: 8001, SeasonNumber: 1, AirDate: "2020-01-01", PosterPath: ""},
				},
			}
			worker, err := enrichmentapp.NewSeriesWorker(a4WorkerDeps(
				t, &a4FakeTMDB{resp: resp},
				seriesRepo, seasonsRepo, textsRepo, seriesMedia, seasonMedia,
				tx, func() time.Time { return fixedClock },
			))
			require.NoError(t, err)

			require.NoError(t, worker.RefreshMediaAssets(ctx, seriesID, "en-US", true))

			assert.Equal(t, 0, countSeriesMediaRows(t, gdb, seriesID), "empty payload → zero series_media_texts rows")
			_, err = seriesMedia.Get(ctx, seriesID, "en-US")
			require.ErrorIs(t, err, ports.ErrNotFound)

			// Stamp still fired (no-work-needed sync).
			after, err := seriesRepo.Get(ctx, seriesID)
			require.NoError(t, err)
			require.NotNil(t, after.EnrichmentMediaSyncedAt)
			assert.Equal(t, fixedClock.Unix(), after.EnrichmentMediaSyncedAt.Unix())
		})
	}
}

// countSeriesMediaRows returns the number of series_media_texts rows for a
// series across all languages — the "no-media hole" audit metric.
func countSeriesMediaRows(t *testing.T, gdb *gorm.DB, id domain.SeriesID) int {
	t.Helper()
	var n int64
	require.NoError(t, gdb.Table("series_media_texts").
		Where("series_id = ?", int64(id)).
		Count(&n).Error)
	return int(n)
}
