package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// fullMovieCanon builds an enriched (hydration=full) canon with every
// TMDB/OMDb column populated so re-upsert regressions are observable.
func fullMovieCanon(tmdbID int, title string) movie.Canon {
	return movie.Canon{
		TMDBID:          new(domain.TMDBID(tmdbID)),
		Hydration:       movie.HydrationFull,
		Title:           title,
		Status:          new("Released"),
		CollectionID:    new(10),
		OriginCountries: []string{"US"},
		Popularity:      new(12.5),
		PosterAsset:     new("/p.jpg"),
		TMDBRating:      new(8.1),
		TMDBVotes:       new(2000),
		IMDBRating:      new(7.9),
	}
}

// TestMovieRepository_Upsert_EnrichedThenRadarrStubPreserves — the "два
// писателя" invariant: a Radarr-style stub write carrying nil in the
// enrichment columns MUST NOT blank a previously TMDB/OMDb-enriched row, and
// MUST NOT downgrade hydration from full.
func TestMovieRepository_Upsert_EnrichedThenRadarrStubPreserves(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewMovieRepository(backend.NewDB(t))
			ctx := context.Background()

			id, err := repo.Upsert(ctx, fullMovieCanon(42, "Dune"))
			require.NoError(t, err)
			require.NotZero(t, id)

			// Radarr-style stub: same tmdb_id, enrichment columns nil,
			// hydration=stub, only library-facing fields carried.
			_, err = repo.Upsert(ctx, movie.Canon{
				TMDBID:    new(domain.TMDBID(42)),
				Hydration: movie.HydrationStub,
				Title:     "Dune",
			})
			require.NoError(t, err)

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, got.TMDBRating)
			assert.InDelta(t, 8.1, *got.TMDBRating, 1e-9)
			require.NotNil(t, got.IMDBRating)
			assert.InDelta(t, 7.9, *got.IMDBRating, 1e-9)
			require.NotNil(t, got.PosterAsset)
			assert.Equal(t, "/p.jpg", *got.PosterAsset)
			require.NotNil(t, got.Status)
			assert.Equal(t, "Released", *got.Status)
			require.NotNil(t, got.CollectionID)
			assert.Equal(t, 10, *got.CollectionID)
			assert.Equal(t, movie.HydrationFull, got.Hydration, "hydration must stay full")
		})
	}
}

// TestMovieRepository_Upsert_OriginCountriesSentinelNotClobbered — the '[]'
// NOT NULL sentinel that a stub write carries must NOT overwrite enriched
// countries (NULLIF('[]') guard).
func TestMovieRepository_Upsert_OriginCountriesSentinelNotClobbered(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewMovieRepository(backend.NewDB(t))
			ctx := context.Background()

			id, err := repo.Upsert(ctx, fullMovieCanon(43, "Arrival"))
			require.NoError(t, err)

			_, err = repo.Upsert(ctx, movie.Canon{
				TMDBID:          new(domain.TMDBID(43)),
				Hydration:       movie.HydrationStub,
				Title:           "Arrival",
				OriginCountries: nil, // encodes to '[]'
			})
			require.NoError(t, err)

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, []string{"US"}, got.OriginCountries)
		})
	}
}

// TestMovieRepository_Upsert_EmptyTitleDoesNotBlank — a Radarr-stub empty
// title must NOT blank a previously-enriched title (NULLIF empty-string guard).
func TestMovieRepository_Upsert_EmptyTitleDoesNotBlank(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewMovieRepository(backend.NewDB(t))
			ctx := context.Background()

			id, err := repo.Upsert(ctx, fullMovieCanon(44, "Dune"))
			require.NoError(t, err)

			_, err = repo.Upsert(ctx, movie.Canon{
				TMDBID:    new(domain.TMDBID(44)),
				Hydration: movie.HydrationStub,
				Title:     "", // must not blank
			})
			require.NoError(t, err)

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, "Dune", got.Title)
		})
	}
}

// TestMovieRepository_Upsert_PlainEnrichmentOverwrite — full→full with a new
// non-nil rating overwrites the prior value (authoritative enrichment path).
func TestMovieRepository_Upsert_PlainEnrichmentOverwrite(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewMovieRepository(backend.NewDB(t))
			ctx := context.Background()

			id, err := repo.Upsert(ctx, fullMovieCanon(45, "Sicario"))
			require.NoError(t, err)

			updated := fullMovieCanon(45, "Sicario")
			updated.TMDBRating = new(9.3)
			_, err = repo.Upsert(ctx, updated)
			require.NoError(t, err)

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, got.TMDBRating)
			assert.InDelta(t, 9.3, *got.TMDBRating, 1e-9)
		})
	}
}

// TestMovieRepository_NullAndErrorPairs — invalid hydration → error;
// GetByTMDBID / Get misses → ports.ErrNotFound (D-0 NULL/error pair bar).
func TestMovieRepository_NullAndErrorPairs(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewMovieRepository(backend.NewDB(t))
			ctx := context.Background()

			_, err := repo.Upsert(ctx, movie.Canon{
				TMDBID:    new(domain.TMDBID(46)),
				Hydration: movie.Hydration("bogus"),
				Title:     "X",
			})
			require.Error(t, err)

			_, err = repo.GetByTMDBID(ctx, domain.TMDBID(999_999))
			require.True(t, errors.Is(err, ports.ErrNotFound))

			_, err = repo.Get(ctx, domain.MovieID(999_999))
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}
