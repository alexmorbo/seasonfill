package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/omdb"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestMovieRepository_UpdateMovieOMDbColumns exercises the plain-value sole-owner
// writer: a first write lands the four OMDb columns + stamps
// enrichment_omdb_synced_at, votes narrow *int64→*int, and TMDB-owned columns +
// title are left untouched; a second all-N/A (all-nil) Enrichment CLEARS them
// back to NULL (the inverse of the COALESCE Upsert path).
func TestMovieRepository_UpdateMovieOMDbColumns(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieRepository(db)
			ctx := context.Background()

			id := seedMovie(t, db, 693134, nil, nil)
			// Pre-set imdb_id + a TMDB-owned rating so we can prove the OMDb writer
			// touches ONLY the four OMDb columns.
			require.NoError(t, db.Model(&database.MovieModel{}).Where("id = ?", id).
				UpdateColumns(map[string]any{
					"imdb_id":     "tt15239678",
					"tmdb_rating": 8.2,
					"tmdb_votes":  1234,
				}).Error)

			now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

			// 1. First write: full OMDb payload (votes as *int64 upstream).
			enr := omdb.Enrichment{
				IMDBRating: new(8.4),
				IMDBVotes:  new(int64(2034123)),
				OMDbRated:  new("PG-13"),
				OMDbAwards: new("Won 2 Oscars"),
			}
			require.NoError(t, repo.UpdateMovieOMDbColumns(ctx, id, enr, now))

			var m database.MovieModel
			require.NoError(t, db.Where("id = ?", id).First(&m).Error)
			require.NotNil(t, m.IMDBRating)
			assert.InDelta(t, 8.4, *m.IMDBRating, 1e-9)
			require.NotNil(t, m.IMDBVotes)
			assert.Equal(t, 2034123, *m.IMDBVotes) // *int64 → *int narrowed
			require.NotNil(t, m.OMDBRated)
			assert.Equal(t, "PG-13", *m.OMDBRated)
			require.NotNil(t, m.OMDBAwards)
			assert.Equal(t, "Won 2 Oscars", *m.OMDBAwards)
			// freshness stamp folded in.
			require.NotNil(t, m.EnrichmentOMDBSyncedAt)
			assert.WithinDuration(t, now, m.EnrichmentOMDBSyncedAt.UTC(), time.Second)
			// TMDB-owned columns + title untouched.
			require.NotNil(t, m.TMDBRating)
			assert.InDelta(t, 8.2, *m.TMDBRating, 1e-9)
			require.NotNil(t, m.TMDBVotes)
			assert.Equal(t, 1234, *m.TMDBVotes)
			assert.Equal(t, "m693134", m.Title)
			require.NotNil(t, m.IMDBID)
			assert.Equal(t, domain.IMDBID("tt15239678"), *m.IMDBID)

			// 2. Second write: all-N/A → all-nil Enrichment CLEARS the four columns.
			later := now.Add(48 * time.Hour)
			require.NoError(t, repo.UpdateMovieOMDbColumns(ctx, id, omdb.Enrichment{}, later))

			var m2 database.MovieModel
			require.NoError(t, db.Where("id = ?", id).First(&m2).Error)
			assert.Nil(t, m2.IMDBRating, "N/A response clears imdb_rating")
			assert.Nil(t, m2.IMDBVotes, "N/A response clears imdb_votes")
			assert.Nil(t, m2.OMDBRated, "N/A response clears omdb_rated")
			assert.Nil(t, m2.OMDBAwards, "N/A response clears omdb_awards")
			// stamp advanced; TMDB columns STILL untouched.
			require.NotNil(t, m2.EnrichmentOMDBSyncedAt)
			assert.WithinDuration(t, later, m2.EnrichmentOMDBSyncedAt.UTC(), time.Second)
			require.NotNil(t, m2.TMDBRating)
			assert.InDelta(t, 8.2, *m2.TMDBRating, 1e-9)
		})
	}
}

// TestMovieRepository_UpdateMovieOMDbColumns_ZeroID rejects a zero movie id.
func TestMovieRepository_UpdateMovieOMDbColumns_ZeroID(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewMovieRepository(backend.NewDB(t))
			err := repo.UpdateMovieOMDbColumns(context.Background(), 0, omdb.Enrichment{}, time.Now())
			require.Error(t, err)
		})
	}
}

// TestInt64PtrToIntPtr covers the nil-preserving narrowing helper.
func TestInt64PtrToIntPtr(t *testing.T) {
	t.Parallel()
	assert.Nil(t, int64PtrToIntPtr(nil))
	got := int64PtrToIntPtr(new(int64(2034123)))
	require.NotNil(t, got)
	assert.Equal(t, 2034123, *got)
}
