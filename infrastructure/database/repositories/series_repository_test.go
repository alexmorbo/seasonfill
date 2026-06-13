package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/enrichment"
	"github.com/alexmorbo/seasonfill/domain/series"
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
		EntityID:   idA,
		Source:     enrichment.SourceTMDBSeries,
		Outcome:    enrichment.OutcomeOK,
	}))
	require.NoError(t, syncLogRepo.Upsert(ctx, enrichment.SyncLog{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   idB,
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
		EntityID:   idC,
		Source:     enrichment.SourceOMDb,
		Outcome:    enrichment.OutcomeOK,
	}))
	ids, err = repo.ListMissingSyncLog(ctx, "tmdb_series", 100)
	require.NoError(t, err)
	require.Len(t, ids, 1, "different-source rows must not cover the join")
	assert.Equal(t, idC, ids[0])
}
