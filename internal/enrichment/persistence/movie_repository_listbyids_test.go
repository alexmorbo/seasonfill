package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestMovieRepository_ListByIDs_DualBackend(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewMovieRepository(db)

			t1, t2 := domain.TMDBID(101), domain.TMDBID(102)
			id1, err := repo.Upsert(ctx, movie.Canon{TMDBID: &t1, Hydration: movie.HydrationFull, Title: "A"})
			require.NoError(t, err)
			id2, err := repo.Upsert(ctx, movie.Canon{TMDBID: &t2, Hydration: movie.HydrationFull, Title: "B"})
			require.NoError(t, err)

			// Request in reverse order + one missing id (99999) — result is id-ASC,
			// missing dropped.
			got, err := repo.ListByIDs(ctx, []domain.MovieID{id2, 99999, id1})
			require.NoError(t, err)
			require.Len(t, got, 2)
			assert.Equal(t, id1, got[0].ID)
			assert.Equal(t, id2, got[1].ID)
			assert.Equal(t, "A", got[0].Title)

			// Empty input → nil, no error.
			empty, err := repo.ListByIDs(ctx, nil)
			require.NoError(t, err)
			assert.Nil(t, empty)
		})
	}
}
