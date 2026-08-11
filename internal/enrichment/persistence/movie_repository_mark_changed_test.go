package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestMovieRepository_MarkChangedByTMDBIDs covers the sole-writer of
// movies.tmdb_changed_at: it marks only matching tmdb_ids, respects the
// dedupBoundary, is idempotent, does NOT touch updated_at, and is NOT reachable
// via Upsert (a canon write leaves tmdb_changed_at untouched).
func TestMovieRepository_MarkChangedByTMDBIDs(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieRepository(db)
			ctx := context.Background()

			id100 := seedMovie(t, db, 100, nil, nil)
			id200 := seedMovie(t, db, 200, nil, nil)
			id300 := seedMovie(t, db, 300, nil, nil)

			// capture updated_at baseline for id200 (marker must NOT bump it).
			before, err := repo.Get(ctx, id200)
			require.NoError(t, err)

			markedAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			boundary := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

			// Mark 100 and 200 (duplicate 100 in input → deduped).
			n, err := repo.MarkChangedByTMDBIDs(ctx, []int64{100, 200, 100}, markedAt, boundary)
			require.NoError(t, err)
			assert.Equal(t, int64(2), n, "two distinct rows marked")

			got100, err := repo.Get(ctx, id100)
			require.NoError(t, err)
			require.NotNil(t, got100.TMDBChangedAt)
			assert.WithinDuration(t, markedAt, *got100.TMDBChangedAt, time.Second)

			got200, err := repo.Get(ctx, id200)
			require.NoError(t, err)
			require.NotNil(t, got200.TMDBChangedAt)
			// updated_at NOT bumped by the marker (UpdateColumn skips autoUpdateTime).
			assert.WithinDuration(t, before.UpdatedAt, got200.UpdatedAt, time.Second,
				"marker must not touch updated_at")

			// 300 was NOT in the id list → untouched.
			got300, err := repo.Get(ctx, id300)
			require.NoError(t, err)
			assert.Nil(t, got300.TMDBChangedAt, "unmatched movie must stay NULL")

			// Idempotent + dedupBoundary: re-mark with boundary <= existing stamp
			// → predicate (tmdb_changed_at < boundary) fails, nothing re-marked.
			n2, err := repo.MarkChangedByTMDBIDs(ctx, []int64{100, 200}, markedAt.Add(time.Hour), boundary)
			require.NoError(t, err)
			assert.Equal(t, int64(0), n2, "already-marked rows past boundary are not re-marked")

			// Upsert does NOT write tmdb_changed_at (grep-AC invariant): a canon
			// stub write on the marked movie preserves the stamp.
			tid := domain.TMDBID(100)
			_, err = repo.Upsert(ctx, movie.Canon{TMDBID: &tid, Title: "m100", Hydration: movie.HydrationStub})
			require.NoError(t, err)
			afterUpsert, err := repo.Get(ctx, id100)
			require.NoError(t, err)
			require.NotNil(t, afterUpsert.TMDBChangedAt, "Upsert must not null tmdb_changed_at")
			assert.WithinDuration(t, markedAt, *afterUpsert.TMDBChangedAt, time.Second)

			// Empty ids → (0, nil), no query.
			n3, err := repo.MarkChangedByTMDBIDs(ctx, nil, markedAt, boundary)
			require.NoError(t, err)
			assert.Equal(t, int64(0), n3)
		})
	}
}
