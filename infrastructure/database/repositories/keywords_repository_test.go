package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestKeywordsRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewKeywordsRepository(db)
			i18n := NewKeywordsI18nRepository(db)

			id, err := repo.Upsert(ctx, taxonomy.Keyword{TMDBID: ptrTMDBID(6075)})
			require.NoError(t, err)
			require.NoError(t, i18n.Upsert(ctx, taxonomy.KeywordI18n{
				KeywordID: id, Language: "en-US", Name: "post-apocalyptic future",
			}))

			got, err := repo.Get(ctx, id, "en-US")
			require.NoError(t, err)
			assert.Equal(t, "post-apocalyptic future", got.Name)
			assert.Equal(t, "en-US", got.Language)
		})
	}
}

func TestKeywordsRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewKeywordsRepository(db)
			_, err := repo.Get(context.Background(), 9999, "en-US")
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestKeywordsRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewKeywordsRepository(db)

			k := taxonomy.Keyword{TMDBID: ptrTMDBID(818)}
			id1, err := repo.Upsert(ctx, k)
			require.NoError(t, err)
			id2, err := repo.Upsert(ctx, k)
			require.NoError(t, err)
			assert.Equal(t, id1, id2)
		})
	}
}

// TestKeywordsRepository_Get_EmptyRURUFallsBackToEnUS documents the
// expected v1 state: TMDB does not localise keywords, so only en-US
// rows exist. Requesting ru-RU MUST return the en-US row via the
// shared §5.6 fallback helper — composer surfaces the Language field
// so UI can render an "EN" tag.
func TestKeywordsRepository_Get_EmptyRURUFallsBackToEnUS(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewKeywordsRepository(db)
			i18n := NewKeywordsI18nRepository(db)

			id, err := repo.Upsert(ctx, taxonomy.Keyword{TMDBID: ptrTMDBID(818)})
			require.NoError(t, err)
			require.NoError(t, i18n.Upsert(ctx, taxonomy.KeywordI18n{
				KeywordID: id, Language: "en-US", Name: "based on novel",
			}))

			got, err := repo.Get(ctx, id, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, "en-US", got.Language,
				"v1: TMDB does not localise keywords — ru-RU MUST fall back to en-US")
			assert.Equal(t, "based on novel", got.Name)
		})
	}
}

// TestKeywordsRepository_ResolveByName covers the forward-compat
// shape — v1 only has en-US rows but the method MUST work today so
// E-1 (Sonarr) and future RU sources have a single resolve path.
func TestKeywordsRepository_ResolveByName(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewKeywordsRepository(db)
			i18n := NewKeywordsI18nRepository(db)

			id, err := repo.Upsert(ctx, taxonomy.Keyword{TMDBID: ptrTMDBID(6075)})
			require.NoError(t, err)
			require.NoError(t, i18n.Upsert(ctx, taxonomy.KeywordI18n{
				KeywordID: id, Language: "en-US", Name: "post-apocalyptic future",
			}))

			resolved, err := repo.ResolveByName(ctx, "en-US", "post-apocalyptic future")
			require.NoError(t, err)
			assert.Equal(t, id, resolved)

			_, err = repo.ResolveByName(ctx, "en-US", "nonexistent")
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

// TestKeywordsRepository_Upsert_OrphanBranch covers the no-PK +
// no-natural-key path (story 424a). Pre-fix this branch emitted a bare
// `ON CONFLICT DO UPDATE` which SQLite tolerated but Postgres rejected
// with SQLSTATE 42601. Two NULL-tmdb_id rows MUST coexist.
func TestKeywordsRepository_Upsert_OrphanBranch(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewKeywordsRepository(db)
			ctx := context.Background()

			id1, err := repo.Upsert(ctx, taxonomy.Keyword{TMDBID: nil})
			require.NoError(t, err, "default branch must issue a pure INSERT")
			require.NotZero(t, id1)

			id2, err := repo.Upsert(ctx, taxonomy.Keyword{TMDBID: nil})
			require.NoError(t, err)
			assert.NotEqual(t, id1, id2,
				"two NULL-tmdb_id rows must coexist — partial unique excludes them")
		})
	}
}

func TestKeywordsRepository_Set_ReplacesAndIdempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewKeywordsRepository(db)

			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
			require.NoError(t, err)
			kID1, err := repo.Upsert(ctx, taxonomy.Keyword{TMDBID: ptrTMDBID(818)})
			require.NoError(t, err)
			kID2, err := repo.Upsert(ctx, taxonomy.Keyword{TMDBID: ptrTMDBID(6075)})
			require.NoError(t, err)

			require.NoError(t, repo.Set(ctx, seriesID, []int64{kID1, kID2}))
			require.NoError(t, repo.Set(ctx, seriesID, []int64{kID1, kID2}))
			rows, err := repo.ListBySeries(ctx, seriesID)
			require.NoError(t, err)
			// Keywords order by keyword_id ASC (no position column).
			assert.Len(t, rows, 2)
			assert.Contains(t, rows, kID1)
			assert.Contains(t, rows, kID2)

			// Re-Set with a subset — orphans gone.
			require.NoError(t, repo.Set(ctx, seriesID, []int64{kID1}))
			rows, err = repo.ListBySeries(ctx, seriesID)
			require.NoError(t, err)
			assert.Equal(t, []int64{kID1}, rows)
		})
	}
}
