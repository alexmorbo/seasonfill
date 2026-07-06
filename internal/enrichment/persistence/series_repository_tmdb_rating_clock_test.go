package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeriesRepository_TMDBRatingClock_IsolatedFromEnrichmentClock is the W18-11
// (F-01) regression: the on-view /ratings TMDB writers must stamp the dedicated
// tmdb_rating_synced_at column WITHOUT re-timing the SHARED
// enrichment_tmdb_synced_at (the full-enrichment TTL gate — series worker
// fresh-skip, tier-refresh selector, skeleton probe, cold-start backfill).
//
// Before the fix, UpdateTMDBRatingColumns and the no-rating stamp both wrote
// enrichment_tmdb_synced_at, so a once-per-TTL rating view perpetually reset the
// full re-sync clock (missed status flips / new seasons / cast) and could strand
// never-enriched stubs. This test runs against a real DB so the actual UPDATE SQL
// is exercised.
//
//	(a) UpdateTMDBRatingColumns advances tmdb_rating_synced_at, leaves
//	    enrichment_tmdb_synced_at UNCHANGED (stale sentinel intact);
//	(b) MarkTMDBRatingSynced (no-rating branch) advances tmdb_rating_synced_at,
//	    leaves enrichment_tmdb_synced_at UNCHANGED.
func TestSeriesRepository_TMDBRatingClock_IsolatedFromEnrichmentClock(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			id, err := repo.Upsert(ctx, sampleCanon("W18-11 TMDB Rating Clock"))
			require.NoError(t, err)
			require.NotZero(t, id)

			// Seed a KNOWN-STALE full-enrichment clock via the raw handle
			// (sampleCanon leaves it NULL). This is the sentinel that must NOT move.
			stale := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			require.NoError(t, gdb.WithContext(ctx).
				Table("series").
				Where("id = ?", int64(id)).
				Update("enrichment_tmdb_synced_at", stale).Error)

			// (a) owner-write a real rating.
			now := time.Now().UTC().Truncate(time.Second)
			rating := 8.4
			votes := 12345
			require.NoError(t, repo.UpdateTMDBRatingColumns(ctx, id, &rating, &votes, now))

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, got.TMDBRating)
			assert.InDelta(t, 8.4, *got.TMDBRating, 1e-9)
			require.NotNil(t, got.TMDBRatingSyncedAt, "rating clock must be stamped")
			assert.WithinDuration(t, now, got.TMDBRatingSyncedAt.UTC(), 2*time.Second,
				"tmdb_rating_synced_at must advance to now")
			require.NotNil(t, got.EnrichmentTMDBSyncedAt, "full-enrichment clock must be untouched, not nulled")
			assert.WithinDuration(t, stale, got.EnrichmentTMDBSyncedAt.UTC(), time.Second,
				"UpdateTMDBRatingColumns must NOT re-time enrichment_tmdb_synced_at (F-01)")

			// (b) no-rating stamp branch — reset the rating clock stale, re-seed the
			//     enrichment sentinel, then MarkTMDBRatingSynced.
			require.NoError(t, gdb.WithContext(ctx).
				Table("series").
				Where("id = ?", int64(id)).
				Updates(map[string]any{
					"tmdb_rating_synced_at":     stale,
					"enrichment_tmdb_synced_at": stale,
				}).Error)

			now2 := time.Now().UTC().Truncate(time.Second)
			require.NoError(t, repo.MarkTMDBRatingSynced(ctx, id, now2))

			marked, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, marked.TMDBRatingSyncedAt)
			assert.WithinDuration(t, now2, marked.TMDBRatingSyncedAt.UTC(), 2*time.Second,
				"MarkTMDBRatingSynced must advance tmdb_rating_synced_at")
			require.NotNil(t, marked.EnrichmentTMDBSyncedAt, "full-enrichment clock must survive the no-rating stamp")
			assert.WithinDuration(t, stale, marked.EnrichmentTMDBSyncedAt.UTC(), time.Second,
				"MarkTMDBRatingSynced must NOT touch enrichment_tmdb_synced_at (F-01)")
			// rating value preserved (no-rating branch never nulls it).
			require.NotNil(t, marked.TMDBRating, "no-rating stamp must not null an existing rating")
			assert.InDelta(t, 8.4, *marked.TMDBRating, 1e-9)
		})
	}
}
