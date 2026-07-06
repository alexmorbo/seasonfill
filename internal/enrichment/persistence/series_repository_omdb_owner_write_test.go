package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeriesRepository_UpdateOMDbColumns_OwnerWriteClearsNA is the W18-6
// owner-write regression: the OMDb worker is the SOLE owner of the four OMDb
// columns and must be able to CLEAR them (write NULL) when OMDb returns "N/A".
//
// The old worker path (applyOMDbToCanon + Series.Upsert) COALESCEs those
// columns via seriesUpsertAssignments, so a nil never overwrote a stored value
// — bug M-1. The in-memory fake test hid it. This test runs against a real DB
// (SQLite + Docker-gated Postgres) so the COALESCE SQL is actually exercised.
//
//	(a) plain-assign real values → all four persisted;
//	(b) plain-assign all-nil ("N/A") → all four NULL (the fix; would FAIL on
//	    the COALESCE Upsert path);
//	(c) preserve-guard: a Sonarr-style Series.Upsert with the four fields nil
//	    must NOT touch the stored OMDb values (COALESCE path intact for other
//	    writers) — proves the fix is scoped to the owner path only.
func TestSeriesRepository_UpdateOMDbColumns_OwnerWriteClearsNA(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			id, err := repo.Upsert(ctx, sampleCanon("W18-6 OMDb Owner"))
			require.NoError(t, err)
			require.NotZero(t, id)

			// (a) owner-write real values.
			rating := 9.5
			votes := 2034123
			rated := "TV-MA"
			awards := "Won 16 Primetime Emmys"
			require.NoError(t, repo.UpdateOMDbColumns(ctx, id, &rating, &votes, &rated, &awards))

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, got.IMDBRating)
			assert.InDelta(t, 9.5, *got.IMDBRating, 1e-9)
			require.NotNil(t, got.IMDBVotes)
			assert.Equal(t, 2034123, *got.IMDBVotes)
			require.NotNil(t, got.OMDBRated)
			assert.Equal(t, "TV-MA", *got.OMDBRated)
			require.NotNil(t, got.OMDBAwards)
			assert.Equal(t, "Won 16 Primetime Emmys", *got.OMDBAwards)

			// (b) owner-write all-nil ("N/A" from OMDb) → all four NULL.
			//     This is the M-1 fix: plain assign writes NULL, whereas the
			//     COALESCE Upsert path would keep the values from (a).
			require.NoError(t, repo.UpdateOMDbColumns(ctx, id, nil, nil, nil, nil))

			cleared, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Nil(t, cleared.IMDBRating, "imdb_rating must be cleared to NULL on N/A")
			assert.Nil(t, cleared.IMDBVotes, "imdb_votes must be cleared to NULL on N/A")
			assert.Nil(t, cleared.OMDBRated, "omdb_rated must be cleared to NULL on N/A")
			assert.Nil(t, cleared.OMDBAwards, "omdb_awards must be cleared to NULL on N/A")

			// (c) preserve-guard — re-seed values via the owner path, then run
			//     a Sonarr-sync-style Upsert (same id, OMDb fields nil). The
			//     COALESCE seriesUpsertAssignments path must leave the stored
			//     OMDb values UNCHANGED — proving the fix is scoped to the
			//     owner-write method and other writers still cannot clobber.
			require.NoError(t, repo.UpdateOMDbColumns(ctx, id, &rating, &votes, &rated, &awards))

			sonarrLike := sampleCanon("W18-6 OMDb Owner (Sonarr resync)")
			sonarrLike.ID = id // hits the id-conflict COALESCE branch
			// sampleCanon leaves the four OMDb fields nil — Sonarr never writes them.
			_, err = repo.Upsert(ctx, sonarrLike)
			require.NoError(t, err)

			preserved, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, preserved.IMDBRating, "Sonarr Upsert must NOT clobber OMDb imdb_rating (COALESCE intact)")
			assert.InDelta(t, 9.5, *preserved.IMDBRating, 1e-9)
			require.NotNil(t, preserved.IMDBVotes)
			assert.Equal(t, 2034123, *preserved.IMDBVotes)
			require.NotNil(t, preserved.OMDBRated)
			assert.Equal(t, "TV-MA", *preserved.OMDBRated)
			require.NotNil(t, preserved.OMDBAwards)
			assert.Equal(t, "Won 16 Primetime Emmys", *preserved.OMDBAwards)
		})
	}
}
