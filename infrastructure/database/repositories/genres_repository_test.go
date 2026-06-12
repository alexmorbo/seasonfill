package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
)

func TestGenresRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewGenresRepository(db)
	i18n := NewGenresI18nRepository(db)

	id, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrInt(18)})
	require.NoError(t, err)
	require.NotZero(t, id)

	require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{
		GenreID: id, Language: "en-US", Name: "Drama",
	}))

	got, err := repo.Get(ctx, id, "en-US")
	require.NoError(t, err)
	assert.Equal(t, "Drama", got.Name)
	assert.Equal(t, "en-US", got.Language)
}

func TestGenresRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGenresRepository(db)
	_, err := repo.Get(context.Background(), 9999, "en-US")
	require.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestGenresRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewGenresRepository(db)

	g := taxonomy.Genre{TMDBID: ptrInt(35)}
	id1, err := repo.Upsert(ctx, g)
	require.NoError(t, err)
	id2, err := repo.Upsert(ctx, g)
	require.NoError(t, err)
	assert.Equal(t, id1, id2, "natural-key upsert must resolve to the same id")
}

// TestGenresRepository_ResolveByName_PRD54Fallback covers the PRD §5.4
// Sonarr-genre fallback contract: an en-US name resolves to the same
// canonical genres.id as a TMDB-sourced upsert.
func TestGenresRepository_ResolveByName_PRD54Fallback(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewGenresRepository(db)
	i18n := NewGenresI18nRepository(db)

	// Simulate the C-2 enrichment path: TMDB upserts genre id=18 with
	// en-US name "Drama".
	id, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrInt(18)})
	require.NoError(t, err)
	require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{
		GenreID: id, Language: "en-US", Name: "Drama",
	}))

	// E-1 (future) writes a Sonarr-grabbed series whose genres list
	// contains the string "Drama". It calls ResolveByName, which MUST
	// return the same canonical id.
	resolved, err := repo.ResolveByName(ctx, "en-US", "Drama")
	require.NoError(t, err)
	assert.Equal(t, id, resolved,
		"PRD §5.4 fallback: Sonarr-genre string must resolve to the canonical TMDB-sourced row")

	// Negative case — unknown name returns ErrNotFound.
	_, err = repo.ResolveByName(ctx, "en-US", "Nonexistent")
	require.True(t, errors.Is(err, ports.ErrNotFound))

	// Negative case — wrong language returns ErrNotFound (v1 case-
	// sensitive, language-exact match).
	_, err = repo.ResolveByName(ctx, "ru-RU", "Drama")
	require.True(t, errors.Is(err, ports.ErrNotFound),
		"v1 ResolveByName is language-exact; no fallback on the resolve path")
}

func TestGenresRepository_Get_FallbackToEnUS(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewGenresRepository(db)
	i18n := NewGenresI18nRepository(db)

	id, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrInt(10765)})
	require.NoError(t, err)
	require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{
		GenreID: id, Language: "en-US", Name: "Sci-Fi & Fantasy",
	}))

	// Request ru-RU — only en-US exists; helper falls back.
	got, err := repo.Get(ctx, id, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "en-US", got.Language)
	assert.Equal(t, "Sci-Fi & Fantasy", got.Name)
}

func TestGenresRepository_Get_NoI18nRows(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewGenresRepository(db)

	// Bare genre stub with no i18n rows — Get returns the row with
	// empty Name / Language (NOT an error).
	id, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrInt(99)})
	require.NoError(t, err)

	got, err := repo.Get(ctx, id, "en-US")
	require.NoError(t, err)
	assert.Empty(t, got.Name)
	assert.Empty(t, got.Language)
}

func TestGenresRepository_Set_ReplacesAndIdempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewGenresRepository(db)

	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
	require.NoError(t, err)
	gID1, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrInt(18)})
	require.NoError(t, err)
	gID2, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrInt(10765)})
	require.NoError(t, err)

	require.NoError(t, repo.Set(ctx, seriesID, []int64{gID1, gID2}))
	require.NoError(t, repo.Set(ctx, seriesID, []int64{gID1, gID2}))
	rows, err := repo.ListBySeries(ctx, seriesID)
	require.NoError(t, err)
	assert.Equal(t, []int64{gID1, gID2}, rows)
}
