package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestBlocklistRepository_Insert_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewBlocklistRepository(db)
			ctx := context.Background()

			first, err := repo.Insert(ctx, disco.BlocklistKindTMDB, 1399, nil)
			require.NoError(t, err)
			assert.Positive(t, first.ID)
			assert.Equal(t, int64(1399), first.RefID)
			// Duplicate (kind, ref_id) → no error, same id, still one row.
			again, err := repo.Insert(ctx, disco.BlocklistKindTMDB, 1399, nil)
			require.NoError(t, err)
			assert.Equal(t, first.ID, again.ID)

			tmdbIDs, kwIDs, err := repo.LoadBlockSets(ctx)
			require.NoError(t, err)
			assert.Equal(t, []int64{1399}, tmdbIDs)
			assert.Empty(t, kwIDs)
		})
	}
}

func TestBlocklistRepository_Insert_KeywordAndDelete(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewBlocklistRepository(db)
			ctx := context.Background()

			label := "anime"
			kw, err := repo.Insert(ctx, disco.BlocklistKindKeyword, 210024, &label)
			require.NoError(t, err)
			require.NotNil(t, kw.Label)
			assert.Equal(t, "anime", *kw.Label)
			_, err = repo.Insert(ctx, disco.BlocklistKindTMDB, 500, nil)
			require.NoError(t, err)

			rows, err := repo.ListResolved(ctx, "en-US")
			require.NoError(t, err)
			require.Len(t, rows, 2)
			// Newest-first: tmdb 500 was inserted last → id DESC.
			assert.Equal(t, "tmdb", rows[0].Kind)
			assert.Equal(t, int64(500), rows[0].RefID)
			assert.Nil(t, rows[0].Title) // no series row for tmdb_id=500

			// Keyword row carries the label; no title/poster join.
			assert.Equal(t, "keyword", rows[1].Kind)
			require.NotNil(t, rows[1].Label)
			assert.Equal(t, "anime", *rows[1].Label)
			assert.Nil(t, rows[1].Title)

			// Delete the tmdb row; keyword remains.
			require.NoError(t, repo.DeleteByID(ctx, rows[0].ID))
			// Idempotent delete of a gone id → no error.
			require.NoError(t, repo.DeleteByID(ctx, rows[0].ID))

			tmdbIDs, kwIDs, err := repo.LoadBlockSets(ctx)
			require.NoError(t, err)
			assert.Empty(t, tmdbIDs)
			assert.Equal(t, []int64{210024}, kwIDs)
		})
	}
}

func TestBlocklistRepository_ListResolved_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewBlocklistRepository(db)
			rows, err := repo.ListResolved(context.Background(), "en-US")
			require.NoError(t, err)
			assert.Empty(t, rows)
		})
	}
}
