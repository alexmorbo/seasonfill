package persistence

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichmentapp "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestA3bRefreshRecommendations_OverwritesRecChildEnPoster_Integration —
// Story 571 B-54 MANDATORY ACCEPTANCE. This end-to-end integration test
// closes the operator-surfaced bug (rec posters stay EN на cold /series/
// {parent}/recommendations?lang=ru-RU visit) via the CI loop. If Impl
// agent silently drops the UpdateRecCanonMedia wire in the A3b tx step
// 6b, THIS test fails.
//
// Scenario mirrors the operator's screenshot 4 case (series 540 recs):
//  1. Seed parent canon with tmdb_id.
//  2. Seed rec child canon с existing EN poster + backdrop (typical
//     state after Sonarr scan + first en-US enrichment).
//  3. Stub TMDB /tv/{parent}?language=ru-RU returning rec with RU
//     poster_path + backdrop_path.
//  4. Wire SeriesWorker with production repositories AND the concrete
//     *SeriesRepository as RecCanonWriter.
//  5. Call worker.RefreshRecommendations(parent, "ru-RU", force=true).
//  6. ASSERT: rec child's canon row now has the RU poster + backdrop.
//     The pre-571 bug would have left the EN values (UpsertStub's
//     COALESCE preserves existing non-NULL).
func TestA3bRefreshRecommendations_OverwritesRecChildEnPoster_Integration(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			ctx := context.Background()

			seriesRepo := NewSeriesRepository(gdb)
			textsRepo := NewSeriesTextsRepository(gdb)
			recsRepo := NewRecommendationsRepository(gdb)
			tx := &inlineTransactor{db: gdb}

			// 1. Parent canon (tmdb=31, e.g. "Rick & Morty" fixture id).
			parentTMDB := domain.TMDBID(31)
			parent := sampleCanon("Parent 540")
			parent.TMDBID = &parentTMDB
			parentID, err := seriesRepo.Upsert(ctx, parent)
			require.NoError(t, err)

			// 2. Rec child pre-seeded с EN poster (мимикируем Sonarr scan
			// followed by first en-US enrichment).
			recChildTMDB := domain.TMDBID(1022)
			recEnPoster := "/wW7rimhnokiQ8V6ha6VKIwHg8IV.jpg" // operator's screenshot EN case
			recEnBackdrop := "/en_backdrop.jpg"
			recChild := series.Canon{
				OriginalTitle: new("Imperfect Women"),
				TMDBID:        &recChildTMDB,
				Hydration:     series.HydrationFull,
			}
			recChildID, err := seriesRepo.Upsert(ctx, recChild)
			require.NoError(t, err)
			require.NotEqual(t, parentID, recChildID, "rec child must be a separate row")

			// S-E3b — rec-card art lives in series_media_texts en-US; seed that
			// row directly to mimic the post-Sonarr-scan + first-en-US-enrichment
			// state. The A3b UpdateRecCanonMedia Upsert COALESCE-overwrites it.
			seedRecCanonMedia(t, gdb, recChildID, recEnPoster, recEnBackdrop)

			// Sanity — verify pre-state matches EN paths (media-texts en-US row).
			beforePoster, _ := readRecCanonMedia(t, gdb, recChildID)
			require.NotNil(t, beforePoster)
			require.Equal(t, recEnPoster, *beforePoster)

			// 3. Stub TMDB — parent's ru-RU response carries the rec's
			// RU-preferred paths.
			ruPoster := "/1FDUQPgaHqRLC0ZJWRjLPr5Z9u8.jpg" // operator's screenshot RU case
			ruBackdrop := "/ru_backdrop.jpg"
			fakeTMDB := newA3bFakeTMDB(&tmdbResponse{
				Recommendations: []recPayload{
					{
						ID:           int64(recChildTMDB),
						Name:         "Неидеальные женщины",
						Overview:     "Локализованное описание",
						PosterPath:   ruPoster,
						BackdropPath: ruBackdrop,
					},
				},
			})

			// 4. Wire worker with production repositories, injecting the
			// concrete *SeriesRepository as BOTH SeriesRepo and
			// SeriesRecCanonWriter (production wiring shape).
			fixedClock := time.Date(2026, 7, 1, 14, 0, 0, 0, time.UTC)
			worker, err := enrichmentapp.NewSeriesWorker(enrichmentapp.SeriesWorkerDeps{
				TMDB:             fakeTMDB,
				Tx:               tx,
				Languages:        []string{"ru-RU"},
				Series:           seriesRepo,
				SeriesTexts:      textsRepo,
				Seasons:          nopSeasonsRepo{},
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
				Recommendations:  recsRepo,
				RecCanonWriter:   seriesRepo, // Story 571 B-54 — production wiring shape
				EnrichmentErrors: nopEnrichmentErrors{},
				Logger:           slog.Default(),
				Clock:            func() time.Time { return fixedClock },
			})
			require.NoError(t, err)

			// 5. EXECUTE.
			err = worker.RefreshRecommendations(ctx, parentID, "ru-RU", true)
			require.NoError(t, err)

			// 6. ASSERTIONS — rec child's en-US series_media_texts row has RU
			// media now.
			afterPoster, afterBackdrop := readRecCanonMedia(t, gdb, recChildID)
			require.NotNil(t, afterPoster)
			assert.Equal(t, ruPoster, *afterPoster,
				"Story 571 B-54 root fix: rec child en-US media poster MUST be overwritten with TMDB ru-RU-preferred path")
			require.NotNil(t, afterBackdrop)
			assert.Equal(t, ruBackdrop, *afterBackdrop,
				"rec child en-US media backdrop MUST be overwritten with TMDB ru-RU-preferred path")

			// Sanity — hydration NOT downgraded (media write doesn't
			// touch the series row).
			after, err := seriesRepo.Get(ctx, recChildID)
			require.NoError(t, err)
			assert.Equal(t, series.HydrationFull, after.Hydration,
				"hydration MUST remain 'full' — UpdateRecCanonMedia writes only series_media_texts")

			// Sanity — series_texts still landed для the rec.
			gotText, err := textsRepo.Get(ctx, recChildID, "ru-RU")
			require.NoError(t, err)
			require.NotNil(t, gotText.Title)
			assert.Equal(t, "Неидеальные женщины", *gotText.Title)

			// Sanity — parent recs stamp landed (whole tx committed).
			gotParent, err := seriesRepo.Get(ctx, parentID)
			require.NoError(t, err)
			require.NotNil(t, gotParent.EnrichmentRecsSyncedAt)
			assert.Equal(t, fixedClock.Unix(), gotParent.EnrichmentRecsSyncedAt.Unix())
		})
	}
}

// TestA3bRefreshRecommendations_RecCanonWriter_NilBackwardsCompat_Integration —
// asserts A3b still works when RecCanonWriter is nil (legacy behavior).
// Rec child's EN poster stays locked in (this is the pre-571 bug being
// captured as a regression baseline).
func TestA3bRefreshRecommendations_RecCanonWriter_NilBackwardsCompat_Integration(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			ctx := context.Background()

			seriesRepo := NewSeriesRepository(gdb)
			textsRepo := NewSeriesTextsRepository(gdb)
			recsRepo := NewRecommendationsRepository(gdb)
			tx := &inlineTransactor{db: gdb}

			parentTMDB := domain.TMDBID(31)
			parent := sampleCanon("Parent Nil-Writer")
			parent.TMDBID = &parentTMDB
			parentID, err := seriesRepo.Upsert(ctx, parent)
			require.NoError(t, err)

			recChildTMDB := domain.TMDBID(1022)
			recEnPoster := "/en_locked_in.jpg"
			recChild := series.Canon{
				OriginalTitle: new("Rec Child"),
				TMDBID:        &recChildTMDB,
				Hydration:     series.HydrationFull,
			}
			recChildID, err := seriesRepo.Upsert(ctx, recChild)
			require.NoError(t, err)
			// S-E3b — seed the en-US series_media_texts row directly; the A3b
			// nil-writer path leaves it untouched.
			seedRecCanonMedia(t, gdb, recChildID, recEnPoster, "")

			fakeTMDB := newA3bFakeTMDB(&tmdbResponse{
				Recommendations: []recPayload{
					{ID: int64(recChildTMDB), Name: "RU Name", PosterPath: "/ru_would_be.jpg"},
				},
			})

			fixedClock := time.Date(2026, 7, 1, 14, 0, 0, 0, time.UTC)
			worker, err := enrichmentapp.NewSeriesWorker(enrichmentapp.SeriesWorkerDeps{
				TMDB:             fakeTMDB,
				Tx:               tx,
				Languages:        []string{"ru-RU"},
				Series:           seriesRepo,
				SeriesTexts:      textsRepo,
				Seasons:          nopSeasonsRepo{},
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
				Recommendations:  recsRepo,
				RecCanonWriter:   nil, // explicit — pre-571 behavior
				EnrichmentErrors: nopEnrichmentErrors{},
				Logger:           slog.Default(),
				Clock:            func() time.Time { return fixedClock },
			})
			require.NoError(t, err)

			require.NoError(t, worker.RefreshRecommendations(ctx, parentID, "ru-RU", true))

			// Rec child still carries EN poster (pre-571 behavior) — read the
			// en-US series_media_texts row.
			afterPoster, _ := readRecCanonMedia(t, gdb, recChildID)
			require.NotNil(t, afterPoster)
			assert.Equal(t, recEnPoster, *afterPoster,
				"nil RecCanonWriter = pre-571 behavior — EN media poster stays as seeded")

			// series_texts still landed (that's the Story 566 path).
			gotText, err := textsRepo.Get(ctx, recChildID, "ru-RU")
			require.NoError(t, err)
			require.NotNil(t, gotText.Title)
			assert.Equal(t, "RU Name", *gotText.Title)
		})
	}
}

// seedRecCanonMedia writes a rec child's en-US series_media_texts row directly.
// S-E3b dropped the canon series.poster_asset/backdrop_asset columns; rec-card
// art now lives in series_media_texts (read via 584b, written via
// UpdateRecCanonMedia). An empty string leaves that column NULL. The series row
// MUST already exist (FK series_media_texts.series_id → series.id).
func seedRecCanonMedia(t *testing.T, gdb *gorm.DB, id domain.SeriesID, poster, backdrop string) {
	t.Helper()
	row := map[string]any{
		"series_id":  int64(id),
		"language":   "en-US",
		"updated_at": time.Now().UTC(),
	}
	if poster != "" {
		row["poster_asset"] = poster
	}
	if backdrop != "" {
		row["backdrop_asset"] = backdrop
	}
	require.NoError(t, gdb.Table("series_media_texts").Create(row).Error)
}

// readRecCanonMedia reads the rec child's en-US series_media_texts poster/backdrop.
// Returns (nil, nil) when no en-US row exists.
func readRecCanonMedia(t *testing.T, gdb *gorm.DB, id domain.SeriesID) (poster, backdrop *string) {
	t.Helper()
	var row struct {
		PosterAsset   *string
		BackdropAsset *string
	}
	require.NoError(t, gdb.Table("series_media_texts").
		Select("poster_asset", "backdrop_asset").
		Where("series_id = ? AND language = ?", int64(id), "en-US").
		Scan(&row).Error)
	return row.PosterAsset, row.BackdropAsset
}

// readRecCanonMediaUpdatedAt returns the en-US series_media_texts row's updated_at
// (zero time when absent) — used by the timestamp-advance test.
func readRecCanonMediaUpdatedAt(t *testing.T, gdb *gorm.DB, id domain.SeriesID) time.Time {
	t.Helper()
	var row struct {
		UpdatedAt time.Time
	}
	require.NoError(t, gdb.Table("series_media_texts").
		Select("updated_at").
		Where("series_id = ? AND language = ?", int64(id), "en-US").
		Scan(&row).Error)
	return row.UpdatedAt
}
