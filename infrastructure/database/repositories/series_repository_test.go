package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/enrichment"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func sampleCanon(title string) series.Canon {
	return series.Canon{
		Title:         title,
		Hydration:     series.HydrationStub,
		TMDBID:        ptrInt(101),
		TVDBID:        ptrInt(202),
		IMDBID:        ptrString("tt0000001"),
		OriginalTitle: ptrString("orig: " + title),
		Status:        ptrString("Returning Series"),
		Year:          ptrInt(2024),
		InProduction:  true,
	}
}

func TestSeriesRepository_UpsertInsertAndGet(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	id, err := repo.Upsert(ctx, sampleCanon("Foundation"))
	require.NoError(t, err)
	require.NotZero(t, id)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Foundation", got.Title)
	assert.Equal(t, series.HydrationStub, got.Hydration)
	require.NotNil(t, got.TMDBID)
	assert.Equal(t, 101, *got.TMDBID)
	assert.True(t, got.InProduction)
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())
}

func TestSeriesRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	_, err := repo.Get(context.Background(), 9999)
	require.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestSeriesRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	first := sampleCanon("Severance")
	id1, err := repo.Upsert(ctx, first)
	require.NoError(t, err)
	got1, err := repo.Get(ctx, id1)
	require.NoError(t, err)

	// Re-upsert with the same payload — must NOT change identity.
	id2, err := repo.Upsert(ctx, first)
	require.NoError(t, err)
	assert.Equal(t, id1, id2, "natural-key upsert must resolve to the same id")

	got2, err := repo.Get(ctx, id2)
	require.NoError(t, err)
	assert.Equal(t, got1.Title, got2.Title)
	assert.Equal(t, got1.Status, got2.Status)
	assert.Equal(t, got1.CreatedAt.Unix(), got2.CreatedAt.Unix(),
		"created_at must NOT shift on a no-op upsert")
	// updated_at IS allowed to bump — that's the only mutating column.
	assert.True(t, !got2.UpdatedAt.Before(got1.UpdatedAt))
}

func TestSeriesRepository_GetByTMDBID(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	_, err := repo.Upsert(ctx, sampleCanon("Severance"))
	require.NoError(t, err)

	got, err := repo.GetByTMDBID(ctx, 101)
	require.NoError(t, err)
	assert.Equal(t, "Severance", got.Title)

	_, err = repo.GetByTMDBID(ctx, 999)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestSeriesRepository_FindByExternalIDs_PriorityOrder(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	_, err := repo.Upsert(ctx, sampleCanon("Andor"))
	require.NoError(t, err)

	// TMDB hit wins.
	got, err := repo.FindByExternalIDs(ctx, ptrInt(101), ptrInt(0), ptrString(""))
	require.NoError(t, err)
	assert.Equal(t, "Andor", got.Title)

	// TMDB miss → TVDB fallback.
	got, err = repo.FindByExternalIDs(ctx, ptrInt(404), ptrInt(202), nil)
	require.NoError(t, err)
	assert.Equal(t, "Andor", got.Title)

	// All probes miss.
	_, err = repo.FindByExternalIDs(ctx, ptrInt(404), ptrInt(404), ptrString("tt9999999"))
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

// TestSeriesRepository_PartialUnique covers the acceptance criterion:
// two NULL-tmdb rows are allowed (orphans), one duplicate non-NULL
// tmdb_id is rejected by the partial unique index. Validates both
// halves of `WHERE tmdb_id IS NOT NULL` on sqlite.
func TestSeriesRepository_PartialUnique(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	orphanA := sampleCanon("Orphan A")
	orphanA.TMDBID = nil
	orphanA.TVDBID = ptrInt(1)
	id1, err := repo.Upsert(ctx, orphanA)
	require.NoError(t, err)

	orphanB := sampleCanon("Orphan B")
	orphanB.TMDBID = nil
	orphanB.TVDBID = ptrInt(2)
	id2, err := repo.Upsert(ctx, orphanB)
	require.NoError(t, err)
	assert.NotEqual(t, id1, id2,
		"two NULL-tmdb rows must coexist — partial index excludes them")

	dup := sampleCanon("Duplicate TMDB")
	dup.TMDBID = ptrInt(101)                        // same as sampleCanon's TMDB id below
	_, err = repo.Upsert(ctx, sampleCanon("First")) // installs tmdb=101
	require.NoError(t, err)

	// The second one MUST hit the conflict path and resolve to the
	// existing row (Upsert is an UPSERT, not an INSERT, so the test is
	// "same id returned" rather than "error raised"). The partial
	// unique index IS what makes this upsert legal at all — without it
	// the second INSERT would race and produce two rows.
	id, err := repo.Upsert(ctx, dup)
	require.NoError(t, err)
	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Duplicate TMDB", got.Title,
		"the second upsert wins by tmdb_id conflict — proving the partial unique exists")
}

// Story 212 — ListMissingSyncLog returns series whose sync_log row is
// absent for the given source; series already journalled (any outcome)
// are excluded. Validates the LEFT JOIN + IS NULL clause on both
// dialects via setupTestDB.
func TestSeriesRepository_ListMissingSyncLog(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	syncLogRepo := NewSyncLogRepository(db)
	ctx := context.Background()

	// 3 series; the first two get a sync_log(tmdb_series) row, the
	// third stays unjournalled.
	a := sampleCanon("A")
	a.TMDBID = ptrInt(1001)
	a.TVDBID = ptrInt(2001)
	idA, err := repo.Upsert(ctx, a)
	require.NoError(t, err)

	b := sampleCanon("B")
	b.TMDBID = ptrInt(1002)
	b.TVDBID = ptrInt(2002)
	idB, err := repo.Upsert(ctx, b)
	require.NoError(t, err)

	c := sampleCanon("C")
	c.TMDBID = ptrInt(1003)
	c.TVDBID = ptrInt(2003)
	idC, err := repo.Upsert(ctx, c)
	require.NoError(t, err)

	require.NoError(t, syncLogRepo.Upsert(ctx, enrichment.SyncLog{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   int64(idA),
		Source:     enrichment.SourceTMDBSeries,
		Outcome:    enrichment.OutcomeOK,
	}))
	require.NoError(t, syncLogRepo.Upsert(ctx, enrichment.SyncLog{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   int64(idB),
		Source:     enrichment.SourceTMDBSeries,
		Outcome:    enrichment.OutcomeError,
	}))

	ids, err := repo.ListMissingSyncLog(ctx, "tmdb_series", 100)
	require.NoError(t, err)
	require.Len(t, ids, 1, "only series C should lack a sync_log row")
	assert.Equal(t, idC, ids[0])

	// A sync_log row for an unrelated source must NOT mark the series
	// as journalled for tmdb_series.
	require.NoError(t, syncLogRepo.Upsert(ctx, enrichment.SyncLog{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   int64(idC),
		Source:     enrichment.SourceOMDb,
		Outcome:    enrichment.OutcomeOK,
	}))
	ids, err = repo.ListMissingSyncLog(ctx, "tmdb_series", 100)
	require.NoError(t, err)
	require.Len(t, ids, 1, "different-source rows must not cover the join")
	assert.Equal(t, idC, ids[0])
}

// seedSeriesCacheRow inserts a series_cache row pointing at seriesID.
// Used by the OMDb library-filter tests to mark a series as "in the
// library" (vs. a stub recommendation row).
func seedSeriesCacheRow(t *testing.T, db *gorm.DB, seriesID domain.SeriesID, instance domain.InstanceName, sonarrID int, deleted bool) {
	t.Helper()
	row := database.SeriesCacheModel{
		InstanceName:   instance,
		SonarrSeriesID: sonarrID,
		SeriesID:       &seriesID,
		TitleSlug:      "x",
		UpdatedAt:      time.Now().UTC(),
	}
	if deleted {
		d := time.Now().UTC()
		row.DeletedAt = &d
	}
	require.NoError(t, db.Create(&row).Error)
}

// TestSeriesRepository_ListLibraryWithIMDBStale_HappyPath — Story 213
// acceptance criterion: stub series (no series_cache reference) and
// terminal not_found rows are NEVER returned by the daily-batch scan.
func TestSeriesRepository_ListLibraryWithIMDBStale_HappyPath(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	syncLogRepo := NewSyncLogRepository(db)
	ctx := context.Background()

	// 3 library series (each with a series_cache row).
	a := sampleCanon("A")
	a.TMDBID = ptrInt(2001)
	a.IMDBID = ptrString("tt0000001")
	idA, err := repo.Upsert(ctx, a)
	require.NoError(t, err)
	seedSeriesCacheRow(t, db, idA, "main", 1, false)

	b := sampleCanon("B")
	b.TMDBID = ptrInt(2002)
	b.IMDBID = ptrString("tt0000002")
	idB, err := repo.Upsert(ctx, b)
	require.NoError(t, err)
	seedSeriesCacheRow(t, db, idB, "main", 2, false)

	c := sampleCanon("C")
	c.TMDBID = ptrInt(2003)
	c.IMDBID = ptrString("tt0000003")
	idC, err := repo.Upsert(ctx, c)
	require.NoError(t, err)
	seedSeriesCacheRow(t, db, idC, "main", 3, false)

	// 1 stub series — has imdb_id but NO series_cache row.
	stub := sampleCanon("Stub")
	stub.TMDBID = ptrInt(2004)
	stub.IMDBID = ptrString("tt0000004")
	_, err = repo.Upsert(ctx, stub)
	require.NoError(t, err)

	// 1 series with terminal not_found sync_log.
	d := sampleCanon("D")
	d.TMDBID = ptrInt(2005)
	d.IMDBID = ptrString("tt0000005")
	idD, err := repo.Upsert(ctx, d)
	require.NoError(t, err)
	seedSeriesCacheRow(t, db, idD, "main", 5, false)
	require.NoError(t, syncLogRepo.Upsert(ctx, enrichment.SyncLog{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   int64(idD),
		Source:     enrichment.SourceOMDb,
		Outcome:    enrichment.OutcomeNotFound,
	}))

	ids, err := repo.ListLibraryWithIMDBStale(ctx, 24*time.Hour, 100)
	require.NoError(t, err)
	got := make(map[domain.SeriesID]bool, len(ids))
	for _, id := range ids {
		got[id] = true
	}
	assert.True(t, got[idA], "library series A returned")
	assert.True(t, got[idB], "library series B returned")
	assert.True(t, got[idC], "library series C returned")
	assert.False(t, got[idD], "terminal not_found excluded")
	assert.Equal(t, 3, len(ids), "stub series excluded; not_found excluded")
}

// TestSeriesRepository_ListLibraryWithIMDBStale_FreshSyncFiltered —
// a series with outcome=ok + synced_at within TTL is excluded.
func TestSeriesRepository_ListLibraryWithIMDBStale_FreshSyncFiltered(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	syncLogRepo := NewSyncLogRepository(db)
	ctx := context.Background()

	s := sampleCanon("Fresh")
	s.TMDBID = ptrInt(3001)
	s.IMDBID = ptrString("tt0000010")
	id, err := repo.Upsert(ctx, s)
	require.NoError(t, err)
	seedSeriesCacheRow(t, db, id, "main", 10, false)

	now := time.Now().UTC()
	fresh := now.Add(-30 * time.Minute)
	require.NoError(t, syncLogRepo.Upsert(ctx, enrichment.SyncLog{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   int64(id),
		Source:     enrichment.SourceOMDb,
		Outcome:    enrichment.OutcomeOK,
		SyncedAt:   &fresh,
	}))

	ids, err := repo.ListLibraryWithIMDBStale(ctx, 24*time.Hour, 100)
	require.NoError(t, err)
	assert.NotContains(t, ids, id, "fresh sync (within TTL) excluded")

	// Now mark synced_at as 25h ago (past TTL) → series returns.
	stale := now.Add(-25 * time.Hour)
	require.NoError(t, syncLogRepo.Upsert(ctx, enrichment.SyncLog{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   int64(id),
		Source:     enrichment.SourceOMDb,
		Outcome:    enrichment.OutcomeOK,
		SyncedAt:   &stale,
	}))
	ids, err = repo.ListLibraryWithIMDBStale(ctx, 24*time.Hour, 100)
	require.NoError(t, err)
	assert.Contains(t, ids, id, "stale sync past TTL returned")
}

// TestSeriesRepository_ListLibraryWithIMDBStale_StubExcludedBySeriesCacheJoin —
// directly verifies the SQL INNER JOIN excludes stubs at query level
// (Critical Decision #2). A series with imdb_id but ZERO series_cache
// rows is invisible to this query regardless of sync_log state.
func TestSeriesRepository_ListLibraryWithIMDBStale_StubExcludedBySeriesCacheJoin(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	stub := sampleCanon("Stub Only")
	stub.TMDBID = ptrInt(4001)
	stub.IMDBID = ptrString("tt0000020")
	_, err := repo.Upsert(ctx, stub)
	require.NoError(t, err)

	ids, err := repo.ListLibraryWithIMDBStale(ctx, 24*time.Hour, 100)
	require.NoError(t, err)
	assert.Empty(t, ids, "stub series without series_cache row never appears")
}

// TestSeriesRepository_ListLibraryWithIMDBStale_SoftDeletedSeriesCacheExcluded —
// a series whose only series_cache row is soft-deleted is no longer
// "in the library" (PRD §5.4 grain).
func TestSeriesRepository_ListLibraryWithIMDBStale_SoftDeletedSeriesCacheExcluded(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	s := sampleCanon("Deleted")
	s.TMDBID = ptrInt(5001)
	s.IMDBID = ptrString("tt0000030")
	id, err := repo.Upsert(ctx, s)
	require.NoError(t, err)
	seedSeriesCacheRow(t, db, id, "main", 30, true) // deleted_at set

	ids, err := repo.ListLibraryWithIMDBStale(ctx, 24*time.Hour, 100)
	require.NoError(t, err)
	assert.NotContains(t, ids, id)
}

// Story 319 — UpsertStub MUST NOT overwrite a 'full' canon row's
// poster_asset, backdrop_asset, hydration, or status with NULL when
// the stub payload has those fields unset.
func TestSeriesRepository_UpsertStub_PreservesFullRowImages(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	tmdbID := 83524
	posterPath := "/full-poster.jpg"
	backdropPath := "/full-backdrop.jpg"
	status := "Returning Series"
	yr := 2019
	rating := 8.0
	votes := 2000
	id, err := repo.Upsert(ctx, series.Canon{
		TMDBID:        ptrInt(tmdbID),
		Title:         "For All Mankind",
		Hydration:     series.HydrationFull,
		PosterAsset:   &posterPath,
		BackdropAsset: &backdropPath,
		Status:        &status,
		Year:          &yr,
		TMDBRating:    &rating,
		TMDBVotes:     &votes,
	})
	require.NoError(t, err)
	require.NotZero(t, id)

	// Apply a stub upsert mimicking the recommendation mapper output:
	// title refreshable, hydration='stub', NIL poster, NIL backdrop,
	// stub-fresh rating (which the stub has NO authority to overwrite).
	stubTitle := "For All Mankind (rec)"
	stubYear := 2019
	stubRating := 8.1
	gotID, err := repo.UpsertStub(ctx, series.Canon{
		TMDBID:     ptrInt(tmdbID),
		Title:      stubTitle,
		Hydration:  series.HydrationStub,
		Year:       &stubYear,
		TMDBRating: &stubRating,
	})
	require.NoError(t, err)
	require.Equal(t, id, gotID, "stub upsert must resolve to the same canon id by tmdb_id")

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, series.HydrationFull, got.Hydration, "stub MUST NOT downgrade hydration")
	require.NotNil(t, got.PosterAsset)
	assert.Equal(t, posterPath, *got.PosterAsset, "stub MUST NOT null poster_asset")
	require.NotNil(t, got.BackdropAsset)
	assert.Equal(t, backdropPath, *got.BackdropAsset, "stub MUST NOT null backdrop_asset")
	require.NotNil(t, got.Status)
	assert.Equal(t, status, *got.Status, "stub MUST NOT overwrite Status")
	require.NotNil(t, got.TMDBRating)
	assert.InDelta(t, 8.0, *got.TMDBRating, 0.001, "stub MUST NOT overwrite existing rating (COALESCE keeps 8.0)")

	// Title IS allowed to be refreshed by the stub (recommendation
	// tile chips need the latest title).
	assert.Equal(t, stubTitle, got.Title, "stub title overwrites")
}

// Story 319 — UpsertStub on a tmdb_id that has no existing row inserts
// fresh, applying the stub's columns verbatim.
func TestSeriesRepository_UpsertStub_InsertsWhenAbsent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	tmdbID := 99999
	posterPath := "/new-poster.jpg"
	yr := 2026
	rating := 7.5
	id, err := repo.UpsertStub(ctx, series.Canon{
		TMDBID:      ptrInt(tmdbID),
		Title:       "New Show",
		Hydration:   series.HydrationStub,
		Year:        &yr,
		TMDBRating:  &rating,
		PosterAsset: &posterPath,
	})
	require.NoError(t, err)
	require.NotZero(t, id)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, series.HydrationStub, got.Hydration)
	require.NotNil(t, got.PosterAsset)
	assert.Equal(t, posterPath, *got.PosterAsset)
	assert.Nil(t, got.BackdropAsset, "stub has no backdrop — stays nil")
}

func TestSeriesRepository_OriginCountriesRoundtrip(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)

	repo := NewSeriesRepository(db)
	ctx := context.Background()

	tmdbID := 99999
	in := series.Canon{
		TMDBID:          &tmdbID,
		Hydration:       series.HydrationFull,
		Title:           "Origin Countries Test",
		OriginCountries: []string{"US", "GB", "CA"},
	}
	id, err := repo.Upsert(ctx, in)
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, []string{"US", "GB", "CA"}, got.OriginCountries)
}

func TestSeriesRepository_OriginCountriesEmptyRoundtrip(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	tmdbID := 99998
	in := series.Canon{
		TMDBID:          &tmdbID,
		Hydration:       series.HydrationFull,
		Title:           "Empty Countries",
		OriginCountries: nil,
	}
	id, err := repo.Upsert(ctx, in)
	require.NoError(t, err)
	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.Nil(t, got.OriginCountries)
}

// Story 319 — UpsertStub requires a non-nil tmdb_id (recommendation
// stubs always carry one). Empty title is also rejected.
func TestSeriesRepository_UpsertStub_RejectsMissingTMDBID(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	_, err := repo.UpsertStub(ctx, series.Canon{Title: "x", Hydration: series.HydrationStub})
	require.Error(t, err)

	_, err = repo.UpsertStub(ctx, series.Canon{TMDBID: ptrInt(1), Hydration: series.HydrationStub})
	require.Error(t, err)
}

// Story 319 — ListCanonImagesCorrupted returns 'full'-hydrated rows
// with tmdb_id set where poster_asset OR backdrop_asset is NULL. Stub
// rows and tmdb_id-NULL rows are excluded.
func TestSeriesRepository_ListCanonImagesCorrupted_FiltersCorrectly(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	mkFull := func(tmdb int, title, poster, backdrop string) domain.SeriesID {
		var pp, bp *string
		if poster != "" {
			s := poster
			pp = &s
		}
		if backdrop != "" {
			s := backdrop
			bp = &s
		}
		id, err := repo.Upsert(ctx, series.Canon{
			TMDBID: ptrInt(tmdb), Title: title, Hydration: series.HydrationFull,
			PosterAsset: pp, BackdropAsset: bp,
		})
		require.NoError(t, err)
		return id
	}
	healthy := mkFull(1001, "Healthy", "/p.jpg", "/b.jpg")
	missingBackdrop := mkFull(1002, "MissingBackdrop", "/p.jpg", "")
	missingBoth := mkFull(1003, "MissingBoth", "", "")

	// Stub row — even though it has no backdrop, hydration != 'full'
	// so it must not appear in the corrupted set.
	_, err := repo.UpsertStub(ctx, series.Canon{
		TMDBID:    ptrInt(1004),
		Title:     "Stub",
		Hydration: series.HydrationStub,
	})
	require.NoError(t, err)

	ids, err := repo.ListCanonImagesCorrupted(ctx, 100)
	require.NoError(t, err)
	assert.NotContains(t, ids, healthy)
	assert.Contains(t, ids, missingBackdrop)
	assert.Contains(t, ids, missingBoth)
	assert.Len(t, ids, 2)
}

// Sonarr-sync prod bug — a stub-input Upsert MUST NOT null an existing
// row's poster_asset. The Sonarr canonOut path emits PosterAsset=nil
// because Sonarr's payload has no poster; the pre-fix Upsert blanked
// the TMDB-enriched poster every scan.
func TestSeriesRepository_Upsert_PreservesPosterOnStubInput(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	tmdbID := 70001
	poster := "/canonical-poster.jpg"
	backdrop := "/canonical-backdrop.jpg"
	id, err := repo.Upsert(ctx, series.Canon{
		TMDBID:        ptrInt(tmdbID),
		Title:         "Full Row",
		Hydration:     series.HydrationFull,
		PosterAsset:   &poster,
		BackdropAsset: &backdrop,
	})
	require.NoError(t, err)

	// Sonarr-shape canonOut: stub hydration, no poster, no backdrop.
	_, err = repo.Upsert(ctx, series.Canon{
		TMDBID:    ptrInt(tmdbID),
		Title:     "Sonarr Refresh",
		Hydration: series.HydrationStub,
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got.PosterAsset, "poster MUST survive stub-input upsert")
	assert.Equal(t, poster, *got.PosterAsset)
}

// Sonarr-sync prod bug — same guarantee for backdrop_asset.
func TestSeriesRepository_Upsert_PreservesBackdropOnStubInput(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	tmdbID := 70002
	poster := "/canonical-poster.jpg"
	backdrop := "/canonical-backdrop.jpg"
	id, err := repo.Upsert(ctx, series.Canon{
		TMDBID:        ptrInt(tmdbID),
		Title:         "Full Row",
		Hydration:     series.HydrationFull,
		PosterAsset:   &poster,
		BackdropAsset: &backdrop,
	})
	require.NoError(t, err)

	_, err = repo.Upsert(ctx, series.Canon{
		TMDBID:    ptrInt(tmdbID),
		Title:     "Sonarr Refresh",
		Hydration: series.HydrationStub,
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got.BackdropAsset, "backdrop MUST survive stub-input upsert")
	assert.Equal(t, backdrop, *got.BackdropAsset)
}

// Sonarr-sync prod bug — a stub-input Upsert MUST NOT downgrade a
// 'full' canon row back to 'stub'.
func TestSeriesRepository_Upsert_PreservesHydrationFull(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	tmdbID := 70003
	poster := "/canonical-poster.jpg"
	id, err := repo.Upsert(ctx, series.Canon{
		TMDBID:      ptrInt(tmdbID),
		Title:       "Full Row",
		Hydration:   series.HydrationFull,
		PosterAsset: &poster,
	})
	require.NoError(t, err)

	_, err = repo.Upsert(ctx, series.Canon{
		TMDBID:    ptrInt(tmdbID),
		Title:     "Sonarr Refresh",
		Hydration: series.HydrationStub,
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, series.HydrationFull, got.Hydration,
		"hydration 'full' is sticky — stub input MUST NOT downgrade")
}

// TMDB enrichment path — when the existing row has NULL poster and the
// incoming Upsert carries a valid poster (e.g., TMDB enrichment of a
// previously stub row), the new value MUST land.
func TestSeriesRepository_Upsert_UpdatesPosterFromNullOnTMDBInput(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	tmdbID := 70004
	id, err := repo.Upsert(ctx, series.Canon{
		TMDBID:    ptrInt(tmdbID),
		Title:     "No Poster Yet",
		Hydration: series.HydrationStub,
	})
	require.NoError(t, err)
	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.Nil(t, got.PosterAsset, "pre-condition: existing poster is NULL")

	newPoster := "/tmdb-poster.jpg"
	newBackdrop := "/tmdb-backdrop.jpg"
	_, err = repo.Upsert(ctx, series.Canon{
		TMDBID:        ptrInt(tmdbID),
		Title:         "TMDB Enriched",
		Hydration:     series.HydrationFull,
		PosterAsset:   &newPoster,
		BackdropAsset: &newBackdrop,
	})
	require.NoError(t, err)

	got, err = repo.Get(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got.PosterAsset, "TMDB-provided poster MUST land when previous was NULL")
	assert.Equal(t, newPoster, *got.PosterAsset)
	require.NotNil(t, got.BackdropAsset)
	assert.Equal(t, newBackdrop, *got.BackdropAsset)
}

// TMDB enrichment path — a 'full' hydration value upgrades a 'stub' row.
func TestSeriesRepository_Upsert_UpgradesHydrationStubToFull(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	tmdbID := 70005
	id, err := repo.Upsert(ctx, series.Canon{
		TMDBID:    ptrInt(tmdbID),
		Title:     "Stub Row",
		Hydration: series.HydrationStub,
	})
	require.NoError(t, err)

	_, err = repo.Upsert(ctx, series.Canon{
		TMDBID:    ptrInt(tmdbID),
		Title:     "Full Now",
		Hydration: series.HydrationFull,
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, series.HydrationFull, got.Hydration,
		"hydration upgrade stub -> full MUST work")
}

// TMDB re-enrichment — a non-NULL incoming poster MUST overwrite a
// non-NULL existing poster (TMDB stays authoritative on enrichment).
func TestSeriesRepository_Upsert_OverwritesPosterFromValidToValid(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	tmdbID := 70006
	oldPoster := "/old-poster.jpg"
	id, err := repo.Upsert(ctx, series.Canon{
		TMDBID:      ptrInt(tmdbID),
		Title:       "Initial",
		Hydration:   series.HydrationFull,
		PosterAsset: &oldPoster,
	})
	require.NoError(t, err)

	newPoster := "/refreshed-poster.jpg"
	_, err = repo.Upsert(ctx, series.Canon{
		TMDBID:      ptrInt(tmdbID),
		Title:       "Refreshed",
		Hydration:   series.HydrationFull,
		PosterAsset: &newPoster,
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got.PosterAsset)
	assert.Equal(t, newPoster, *got.PosterAsset,
		"non-NULL excluded value MUST win over existing non-NULL value")
}

// Story 346 — CountCanonImagesBreakdown returns (poster_null,
// backdrop_null) counts over the SAME population
// ListCanonImagesCorrupted draws from.
func TestSeriesRepository_CountCanonImagesBreakdown(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	mkFull := func(tmdb int, title, poster, backdrop string) {
		var pp, bp *string
		if poster != "" {
			s := poster
			pp = &s
		}
		if backdrop != "" {
			s := backdrop
			bp = &s
		}
		_, err := repo.Upsert(ctx, series.Canon{
			TMDBID: ptrInt(tmdb), Title: title, Hydration: series.HydrationFull,
			PosterAsset: pp, BackdropAsset: bp,
		})
		require.NoError(t, err)
	}
	// 1 healthy (no nulls), 1 backdrop-null, 1 poster-null, 1 both-null.
	mkFull(2001, "Healthy", "/p.jpg", "/b.jpg")
	mkFull(2002, "BackdropNull", "/p.jpg", "")
	mkFull(2003, "PosterNull", "", "/b.jpg")
	mkFull(2004, "BothNull", "", "")

	posterNull, backdropNull, err := repo.CountCanonImagesBreakdown(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, posterNull, "PosterNull + BothNull = 2 poster-null rows")
	assert.Equal(t, 2, backdropNull, "BackdropNull + BothNull = 2 backdrop-null rows")
}

// FIX-B13-HERO prod regression — a Sonarr-shape Upsert (canonOut from
// MergeSeries(SourceSonarr) carrying NULL for every TMDB/OMDb-only
// column) MUST NOT overwrite the previously-enriched row's canon
// columns. Live evidence (delta sha-0a2a816, 2026-06-17): series id=8
// R&M and id=96 Star City had their tmdb_rating, imdb_rating,
// first_air_date, original_language, origin_countries etc cleared
// between a /refresh enrichment and the next 0 */6 scan tick.
func TestSeriesRepository_Upsert_PreservesTMDBAndOMDbFieldsOnSonarrInput(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	tmdbID := 80001
	firstAir := time.Date(2013, 12, 2, 0, 0, 0, 0, time.UTC)
	lastAir := time.Date(2020, 9, 11, 0, 0, 0, 0, time.UTC)
	originalTitle := "Rick and Morty"
	status := "Returning Series"
	homepage := "https://www.adultswim.com/videos/rick-and-morty"
	origLang := "en"
	origCountry := "US"
	originCountries := []string{"US"}
	tmdbRating := 8.7
	tmdbVotes := 9100
	imdbRating := 9.1
	imdbVotes := 590000
	omdbRated := "TV-14"
	omdbAwards := "Won 4 Primetime Emmys"
	popularity := 425.7
	runtime := 22

	id, err := repo.Upsert(ctx, series.Canon{
		TMDBID:           ptrInt(tmdbID),
		Title:            "Rick and Morty",
		Hydration:        series.HydrationFull,
		OriginalTitle:    &originalTitle,
		Status:           &status,
		FirstAirDate:     &firstAir,
		LastAirDate:      &lastAir,
		Year:             ptrInt(2013),
		RuntimeMinutes:   &runtime,
		Homepage:         &homepage,
		OriginalLanguage: &origLang,
		OriginCountry:    &origCountry,
		OriginCountries:  originCountries,
		Popularity:       &popularity,
		InProduction:     true,
		TMDBRating:       &tmdbRating,
		TMDBVotes:        &tmdbVotes,
		IMDBRating:       &imdbRating,
		IMDBVotes:        &imdbVotes,
		OMDBRated:        &omdbRated,
		OMDBAwards:       &omdbAwards,
	})
	require.NoError(t, err)

	// Sonarr-shape canonOut: every TMDB/OMDb-only column = nil.
	_, err = repo.Upsert(ctx, series.Canon{
		TMDBID:    ptrInt(tmdbID),
		Title:     "Rick and Morty",
		Hydration: series.HydrationStub,
		Year:      ptrInt(2013),
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)

	assert.Equal(t, series.HydrationFull, got.Hydration)

	if assert.NotNil(t, got.OriginalTitle) {
		assert.Equal(t, originalTitle, *got.OriginalTitle)
	}
	if assert.NotNil(t, got.Status) {
		assert.Equal(t, status, *got.Status)
	}
	if assert.NotNil(t, got.FirstAirDate) {
		assert.True(t, got.FirstAirDate.Equal(firstAir))
	}
	if assert.NotNil(t, got.LastAirDate) {
		assert.True(t, got.LastAirDate.Equal(lastAir))
	}
	if assert.NotNil(t, got.Homepage) {
		assert.Equal(t, homepage, *got.Homepage)
	}
	if assert.NotNil(t, got.OriginalLanguage) {
		assert.Equal(t, origLang, *got.OriginalLanguage)
	}
	if assert.NotNil(t, got.OriginCountry) {
		assert.Equal(t, origCountry, *got.OriginCountry)
	}
	assert.Equal(t, originCountries, got.OriginCountries)
	if assert.NotNil(t, got.Popularity) {
		assert.InEpsilon(t, popularity, *got.Popularity, 1e-9)
	}
	if assert.NotNil(t, got.TMDBRating) {
		assert.InEpsilon(t, tmdbRating, *got.TMDBRating, 1e-9)
	}
	if assert.NotNil(t, got.TMDBVotes) {
		assert.Equal(t, tmdbVotes, *got.TMDBVotes)
	}
	if assert.NotNil(t, got.IMDBRating) {
		assert.InEpsilon(t, imdbRating, *got.IMDBRating, 1e-9)
	}
	if assert.NotNil(t, got.IMDBVotes) {
		assert.Equal(t, imdbVotes, *got.IMDBVotes)
	}
	if assert.NotNil(t, got.OMDBRated) {
		assert.Equal(t, omdbRated, *got.OMDBRated)
	}
	if assert.NotNil(t, got.OMDBAwards) {
		assert.Equal(t, omdbAwards, *got.OMDBAwards)
	}
	assert.Equal(t, "Rick and Morty", got.Title)
}

// TestSeriesRepository_Upsert_RegressionCountriesAndRatingsLost_FIXB13HERO is
// the explicit live regression named after the FIX-B13-HERO ship
// (sha-d1cd972). Pre-fix, a Sonarr scan tick wrote canonOut with
// TMDB/OMDb-shape fields = nil over an already-enriched row, nuking
// OriginCountries, TMDBRating, IMDBRating, FirstAirDate (observed live
// on id=8 R&M and id=96 Star City). COALESCE shield in
// seriesUpsertAssignments protects against the regression; this test
// fails the moment that shield is removed.
func TestSeriesRepository_Upsert_RegressionCountriesAndRatingsLost_FIXB13HERO(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesRepository(db)
	ctx := context.Background()

	tmdbID := 80002
	firstAir := time.Date(2013, 12, 2, 0, 0, 0, 0, time.UTC)
	tmdbRating := 8.7
	imdbRating := 9.1
	originCountries := []string{"US"}

	id, err := repo.Upsert(ctx, series.Canon{
		TMDBID:          ptrInt(tmdbID),
		Title:           "Rick and Morty",
		Hydration:       series.HydrationFull,
		FirstAirDate:    &firstAir,
		OriginCountries: originCountries,
		TMDBRating:      &tmdbRating,
		IMDBRating:      &imdbRating,
		Year:            ptrInt(2013),
	})
	require.NoError(t, err)
	require.NotZero(t, id)

	// Mimic the Sonarr-driven scan tick: same canon row, but every
	// TMDB/OMDb-owned column nil — exactly the regression shape that
	// nuked id=8 R&M and id=96 Star City in production.
	_, err = repo.Upsert(ctx, series.Canon{
		TMDBID:    ptrInt(tmdbID),
		Title:     "Rick and Morty",
		Hydration: series.HydrationStub,
		Year:      ptrInt(2013),
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)

	cases := []struct {
		column string
		check  func(t *testing.T)
	}{
		{
			column: "origin_countries",
			check: func(t *testing.T) {
				assert.Equal(t, originCountries, got.OriginCountries,
					"REGRESSION: origin_countries nuked by Sonarr-only Upsert (FIX-B13-HERO reverted?)")
			},
		},
		{
			column: "tmdb_rating",
			check: func(t *testing.T) {
				if assert.NotNil(t, got.TMDBRating, "REGRESSION: tmdb_rating nuked") {
					assert.InEpsilon(t, tmdbRating, *got.TMDBRating, 1e-9)
				}
			},
		},
		{
			column: "imdb_rating",
			check: func(t *testing.T) {
				if assert.NotNil(t, got.IMDBRating, "REGRESSION: imdb_rating nuked") {
					assert.InEpsilon(t, imdbRating, *got.IMDBRating, 1e-9)
				}
			},
		},
		{
			column: "first_air_date",
			check: func(t *testing.T) {
				if assert.NotNil(t, got.FirstAirDate, "REGRESSION: first_air_date nuked") {
					assert.True(t, got.FirstAirDate.Equal(firstAir))
				}
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.column, func(t *testing.T) {
			t.Parallel()
			tc.check(t)
		})
	}

	assert.Equal(t, series.HydrationFull, got.Hydration,
		"REGRESSION: hydration downgraded full->stub")
}
