package persistence

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichmentapp "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// inlineTransactor mirrors catalogpersistence.GormTransactor without the
// cross-package import (would form an import cycle since catalog/persistence
// already imports enrichment/persistence via series_cache_repository.go).
// Drops the deadlock-retry — testcontainers Postgres rarely deadlocks under
// the test fixture's single-writer load.
type inlineTransactor struct {
	db *gorm.DB
}

func (t *inlineTransactor) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return t.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(WithTx(ctx, tx))
	})
}

// TestSeriesRepository_MarkRecsSynced — single-column UPDATE stamps the
// right row. Idempotent on re-call; missing row → no error (defensive).
func TestSeriesRepository_MarkRecsSynced(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			seriesID, err := repo.Upsert(ctx, sampleCanon("Mark Recs"))
			require.NoError(t, err)

			now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
			require.NoError(t, repo.MarkRecsSynced(ctx, seriesID, now))

			canon, err := repo.Get(ctx, seriesID)
			require.NoError(t, err)
			require.NotNil(t, canon.EnrichmentRecsSyncedAt)
			assert.Equal(t, now.Unix(), canon.EnrichmentRecsSyncedAt.Unix())

			// Idempotent re-call.
			now2 := now.Add(5 * time.Minute)
			require.NoError(t, repo.MarkRecsSynced(ctx, seriesID, now2))
			canon2, err := repo.Get(ctx, seriesID)
			require.NoError(t, err)
			require.NotNil(t, canon2.EnrichmentRecsSyncedAt)
			assert.Equal(t, now2.Unix(), canon2.EnrichmentRecsSyncedAt.Unix())

			// Missing row → no error (defensive).
			require.NoError(t, repo.MarkRecsSynced(ctx, domain.SeriesID(999_999), now))
		})
	}
}

// TestSeriesRepository_UpsertStub_PreservesOriginalFieldsOnNilReupsert —
// W15-3: A3b now writes original_title + original_language into the rec
// stub via UpsertStub. A later stub re-upsert whose payload omits them
// (a subsequent recs refresh without originals, or a Sonarr-shaped stub)
// MUST NOT blank the stored originals — UpsertStub COALESCEs both columns
// (series_repository.go:331 original_title, :339 original_language).
func TestSeriesRepository_UpsertStub_PreservesOriginalFieldsOnNilReupsert(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			tmdbID := domain.TMDBID(778899)
			id, err := repo.UpsertStub(ctx, series.Canon{
				TMDBID:           &tmdbID,
				Hydration:        series.HydrationStub,
				OriginalTitle:    new("Breaking Bad"),
				OriginalLanguage: new("en"),
			})
			require.NoError(t, err)
			require.NotZero(t, id)

			// Re-upsert the SAME tmdb_id stub omitting both originals.
			gotID, err := repo.UpsertStub(ctx, series.Canon{
				TMDBID:    &tmdbID,
				Hydration: series.HydrationStub,
			})
			require.NoError(t, err)
			require.Equal(t, id, gotID, "stub re-upsert must resolve to the same canon id by tmdb_id")

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, got.OriginalTitle,
				"original_title must survive nil re-upsert (UpsertStub COALESCE)")
			assert.Equal(t, "Breaking Bad", *got.OriginalTitle)
			require.NotNil(t, got.OriginalLanguage,
				"original_language must survive nil re-upsert (UpsertStub COALESCE)")
			assert.Equal(t, "en", *got.OriginalLanguage)
		})
	}
}

// TestSeriesRepository_RecsSyncedAtSurvivesSonarrUpsert_BareWrite — Test A
// (excluded.X regression class) on enrichment_recs_synced_at column.
//
// Scenario: A3b narrow writer stamps. Then Sonarr's 6h scan triggers
// Series.Upsert with a canonOut payload that has EnrichmentRecsSyncedAt nil.
//
// EXPECTED: COALESCE on enrichment_recs_synced_at (seriesUpsertAssignments
// line 795 — shipped A2) preserves the stamp.
//
// This test alone doesn't prove the fix actively defends — Test B does.
// Kept here as the column-present regression test.
func TestSeriesRepository_RecsSyncedAtSurvivesSonarrUpsert_BareWrite(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			// 1. Seed canon + stamp.
			seriesID, err := repo.Upsert(ctx, sampleCanon("A3b Recs Test A"))
			require.NoError(t, err)
			stampT := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
			require.NoError(t, repo.MarkRecsSynced(ctx, seriesID, stampT))

			// 2. Simulate Sonarr-driven scan: re-Upsert via production
			//    Upsert path with a Canon that has EnrichmentRecsSyncedAt nil.
			c := sampleCanon("A3b Recs Test A (Sonarr resync)")
			c.ID = seriesID
			_, err = repo.Upsert(ctx, c)
			require.NoError(t, err)

			// 3. Assert stamp PRESERVED.
			canon, err := repo.Get(ctx, seriesID)
			require.NoError(t, err)
			require.NotNil(t, canon.EnrichmentRecsSyncedAt,
				"enrichment_recs_synced_at must survive Sonarr-driven Upsert")
			assert.Equal(t, stampT.Unix(), canon.EnrichmentRecsSyncedAt.Unix(),
				"stamp value must be unchanged")
		})
	}
}

// TestSeriesRepository_RecsSyncedAtSurvivesSonarrUpsert_ColumnInclude —
// Test B (column-removed-from-map regression class).
//
// Simulates a future contributor removing the COALESCE entry from
// seriesUpsertAssignments by issuing a raw GORM UPSERT with explicit
// clause.AssignmentColumns([all-incl-enrichment_recs_synced_at]). If
// COALESCE-by-accident-only protected the stamp, this would nuke it.
// Then re-stamps + runs the production Upsert path to prove COALESCE
// actively defends.
func TestSeriesRepository_RecsSyncedAtSurvivesSonarrUpsert_ColumnInclude(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gormDB := backend.NewDB(t)
			repo := NewSeriesRepository(gormDB)
			ctx := context.Background()

			// 1. Seed canon + stamp.
			seriesID, err := repo.Upsert(ctx, sampleCanon("A3b Recs Test B"))
			require.NoError(t, err)
			stampT := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
			require.NoError(t, repo.MarkRecsSynced(ctx, seriesID, stampT))

			// 2a. REGRESSION SIMULATION — raw UPSERT with explicit
			//     AssignmentColumns including the stamp. Bypasses production
			//     COALESCE map (uses a different Clauses() call). Proves
			//     the vulnerability is real if a future contributor edits
			//     the production map.
			now := time.Now().UTC()
			tmdbID := domain.TMDBID(424242)
			sonarrPayload := database.SeriesModel{
				ID:              seriesID,
				OriginalTitle:   new("A3b Recs Test B (force-include sim)"),
				TMDBID:          &tmdbID,
				Hydration:       "full",
				OriginCountries: []byte("[]"),
				CreatedAt:       now,
				UpdatedAt:       now,
				// EnrichmentRecsSyncedAt nil — Sonarr never writes it.
			}
			err = gormDB.WithContext(ctx).Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "id"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"original_title", "tmdb_id", "hydration",
					"enrichment_recs_synced_at", // FORCE-INCLUDE — proves the regression
					"updated_at",
				}),
			}).Create(&sonarrPayload).Error
			require.NoError(t, err)

			// 2b. Assert regression: stamp nuked by force-include path.
			canon, err := repo.Get(ctx, seriesID)
			require.NoError(t, err)
			assert.Nil(t, canon.EnrichmentRecsSyncedAt,
				"regression simulation must nuke stamp — proves AssignmentColumns([all]) bypasses COALESCE")

			// 2c. Re-stamp and run production Upsert → COALESCE defends.
			require.NoError(t, repo.MarkRecsSynced(ctx, seriesID, stampT))
			c := sampleCanon("A3b Recs Test B (production Upsert)")
			c.ID = seriesID
			_, err = repo.Upsert(ctx, c)
			require.NoError(t, err)

			// 3. Assert stamp PRESERVED via production COALESCE path.
			canon2, err := repo.Get(ctx, seriesID)
			require.NoError(t, err)
			require.NotNil(t, canon2.EnrichmentRecsSyncedAt,
				"production Upsert COALESCE must preserve stamp")
			assert.Equal(t, stampT.Unix(), canon2.EnrichmentRecsSyncedAt.Unix(),
				"stamp value must be unchanged by production Upsert")
		})
	}
}

// TestSeriesTextsRepository_TitleSurvivesSubsequentSideEffect_BareWrite —
// universal narrow-writer audit applied to series_texts.Upsert.
//
// A3b's side-effect leaves enriched_at nil deliberately. Asserts that a
// PRIOR series_texts.Upsert(rec_id, lang) from another writer (e.g.
// RefreshSeriesText that DID stamp enriched_at) is NOT undone by a
// subsequent A3b side-effect write.
//
// The series_texts.Upsert COALESCE on enriched_at (i18n_texts.go:130)
// already protects this — this test proves the protection actively
// defends rather than passing by accident.
func TestSeriesTextsRepository_TitleSurvivesSubsequentSideEffect_BareWrite(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			seriesRepo := NewSeriesRepository(gdb)
			textsRepo := NewSeriesTextsRepository(gdb)
			ctx := context.Background()

			recID, err := seriesRepo.UpsertStub(ctx, sampleCanon("A3b TextsAudit Rec"))
			require.NoError(t, err)

			// 1. Prior writer stamps enriched_at.
			stampT := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
			titlePre := "Старый перевод"
			overviewPre := "Старое описание"
			require.NoError(t, textsRepo.Upsert(ctx, series.SeriesText{
				SeriesID:   recID,
				Language:   "ru-RU",
				Title:      &titlePre,
				Overview:   &overviewPre,
				EnrichedAt: &stampT,
			}))

			// 2. A3b side-effect — title/overview refreshed, enriched_at LEFT NIL.
			titleNew := "Новый перевод от A3b"
			overviewNew := "Новое описание от A3b"
			require.NoError(t, textsRepo.Upsert(ctx, series.SeriesText{
				SeriesID: recID,
				Language: "ru-RU",
				Title:    &titleNew,
				Overview: &overviewNew,
				// EnrichedAt deliberately nil — A3b side-effect contract
			}))

			// 3. Assert: title/overview UPDATED (TMDB refresh), enriched_at PRESERVED.
			got, err := textsRepo.Get(ctx, recID, "ru-RU")
			require.NoError(t, err)
			require.NotNil(t, got.Title)
			assert.Equal(t, "Новый перевод от A3b", *got.Title, "title should refresh on A3b write")
			require.NotNil(t, got.EnrichedAt, "enriched_at must survive A3b nil-write — series_texts COALESCE defense")
			assert.Equal(t, stampT.Unix(), got.EnrichedAt.Unix())
		})
	}
}

// TestRecommendationsRepository_CheckConstraintRejectsSelfRef —
// A3b DB-level defense: the CHECK constraint added in migration 000023
// rejects rows with series_id == recommended_series_id. Third layer of
// the triple-defense (skip in worker loop + drop from recIDs + this
// CHECK). Exercised via a raw INSERT that bypasses the app-side
// RecommendationsRepository.Set/Upsert guards.
func TestRecommendationsRepository_CheckConstraintRejectsSelfRef(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			seriesRepo := NewSeriesRepository(gdb)
			ctx := context.Background()

			seriesID, err := seriesRepo.Upsert(ctx, sampleCanon("Self-Ref Check"))
			require.NoError(t, err)

			// Raw INSERT bypassing the app-side guards — must be rejected
			// by the DB CHECK constraint.
			err = gdb.WithContext(ctx).Exec(
				`INSERT INTO series_recommendations
					(series_id, recommended_series_id, position, updated_at)
				 VALUES (?, ?, 0, ?)`,
				seriesID, seriesID, time.Now().UTC(),
			).Error
			require.Error(t, err, "DB CHECK constraint must reject self-reference row")
		})
	}
}

// TestRefreshRecommendations_SideEffectChildTexts_Integration — F-R2-3
// MANDATORY ACCEPTANCE (plan-review Round 2). This is the test that
// closes operator smoke symptom #3 in the CI loop. If Impl agent
// silently drops the N×UPSERT side-effect loop, THIS test fails.
//
// Scenario: full integration via testcontainers Postgres (and SQLite).
//  1. Seed parent series with TMDB ID.
//  2. Stub a fake TMDB client that returns 3 recommendations (rec_tmdb_ids
//     1001/1002/1003 with Russian translated names).
//  3. Wire SeriesWorker dependencies against the live DB.
//  4. Call worker.RefreshRecommendations(parent_id, "ru-RU", force=true).
//  5. ASSERT: series_recommendations table has exactly 3 rows for parent.
//  6. ASSERT (F-R2-3): SQL COUNT verbatim per PLAN §6.3 — every rec child
//     has a series_texts.{rec_child_id, "ru-RU"} row.
//  7. ASSERT: parent's enrichment_recs_synced_at is set.
func TestRefreshRecommendations_SideEffectChildTexts_Integration(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			ctx := context.Background()

			// Wire production repositories against the live DB.
			seriesRepo := NewSeriesRepository(gdb)
			textsRepo := NewSeriesTextsRepository(gdb)
			recsRepo := NewRecommendationsRepository(gdb)
			tx := &inlineTransactor{db: gdb}

			// Seed parent canon с tmdb_id.
			parent := sampleCanon("Integration A3b Parent")
			parentTMDB := domain.TMDBID(31)
			parent.TMDBID = &parentTMDB
			parentID, err := seriesRepo.Upsert(ctx, parent)
			require.NoError(t, err)

			// Stub TMDB client returning 3 recs (ru-RU titles).
			fakeTMDB := newA3bFakeTMDB(&tmdbResponse{
				Recommendations: []recPayload{
					{ID: 1001, Name: "Невозможно поверить", Overview: "Описание 1", PosterPath: "/p1.jpg"},
					{ID: 1002, Name: "Лучший друг", Overview: "Описание 2", PosterPath: "/p2.jpg"},
					{ID: 1003, Name: "Большая надежда", Overview: "Описание 3", PosterPath: "/p3.jpg"},
				},
			})

			// Wire worker против live DB + fake TMDB. Most ports use
			// production wiring; ones the worker does NOT touch in
			// RefreshRecommendations remain nil-safe via the constructor's
			// required-field validation (we satisfy all required fields).
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
				EnrichmentErrors: nopEnrichmentErrors{},
				Logger:           slog.Default(),
				Clock:            func() time.Time { return fixedClock },
			})
			require.NoError(t, err)

			// EXECUTE.
			err = worker.RefreshRecommendations(ctx, parentID, "ru-RU", true)
			require.NoError(t, err)

			// 1. series_recommendations links count.
			var linkCount int64
			require.NoError(t, gdb.WithContext(ctx).
				Table("series_recommendations").
				Where("series_id = ?", parentID).
				Count(&linkCount).Error)
			assert.Equal(t, int64(3), linkCount, "expected 3 series_recommendations rows")

			// 2. F-R2-3 SIDE-EFFECT ASSERTION — every rec child has a
			//    series_texts row for the requested lang. SQL verbatim
			//    per PLAN §6.3 smoke spec.
			//
			//    Using GORM Table()+Joins() rather than Raw() so the same
			//    statement parses on SQLite + Postgres (Raw bind-parameter
			//    rules differ between backends on `Scan(&int64)` for
			//    aggregates).
			var textsCount int64
			require.NoError(t, gdb.WithContext(ctx).
				Table("series_texts t").
				Joins("JOIN series_recommendations r ON r.recommended_series_id = t.series_id").
				Where("t.language = ?", "ru-RU").
				Where("r.series_id = ?", parentID).
				Count(&textsCount).Error)
			assert.Equal(t, int64(3), textsCount,
				"F-R2-3 MANDATORY: every rec child must have series_texts.{rec_id, 'ru-RU'} row written by the side-effect — silent N×UPSERT omission blocks ship")

			// 3. Spot-check одна из rec's title content (TMDB-translated text persisted verbatim).
			recCanon, err := seriesRepo.GetByTMDBID(ctx, domain.TMDBID(1001))
			require.NoError(t, err)
			gotText, err := textsRepo.Get(ctx, recCanon.ID, "ru-RU")
			require.NoError(t, err)
			require.NotNil(t, gotText.Title)
			assert.Equal(t, "Невозможно поверить", *gotText.Title,
				"TMDB-translated rec name must persist verbatim per §4.2 (trust blindly)")

			// 4. Parent stamp set.
			gotParent, err := seriesRepo.Get(ctx, parentID)
			require.NoError(t, err)
			require.NotNil(t, gotParent.EnrichmentRecsSyncedAt)
			assert.Equal(t, fixedClock.Unix(), gotParent.EnrichmentRecsSyncedAt.Unix())
		})
	}
}
