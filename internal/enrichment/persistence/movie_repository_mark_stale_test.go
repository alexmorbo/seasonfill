package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestMovieRepository_MarkStaleForReenrich covers the Ф1.2 on-read hydration
// marker: it bumps tmdb_changed_at for a re-enrichable movie, is idempotent for
// an already-changed-pending movie (no clock reset), does NOT bump updated_at,
// and rejects a zero id.
func TestMovieRepository_MarkStaleForReenrich(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

			// --- cold movie: changed NULL, sync NULL → bumped ---
			cold := seedMovie(t, db, 100, nil, nil)
			beforeCold, err := repo.Get(ctx, cold)
			require.NoError(t, err)
			require.Nil(t, beforeCold.TMDBChangedAt)

			require.NoError(t, repo.MarkStaleForReenrich(ctx, cold, now))

			gotCold, err := repo.Get(ctx, cold)
			require.NoError(t, err)
			require.NotNil(t, gotCold.TMDBChangedAt, "cold movie must be marked")
			assert.WithinDuration(t, now, *gotCold.TMDBChangedAt, time.Second)
			assert.WithinDuration(t, beforeCold.UpdatedAt, gotCold.UpdatedAt, time.Second,
				"marker must NOT bump updated_at")

			// --- already changed-pending: changed set, sync NULL → NO-OP ---
			tOld := now.Add(-1 * time.Hour)
			pending := seedMovie(t, db, 200, nil, new(tOld)) // sync nil, changed=tOld
			tNew := now.Add(2 * time.Hour)

			require.NoError(t, repo.MarkStaleForReenrich(ctx, pending, tNew))

			gotPending, err := repo.Get(ctx, pending)
			require.NoError(t, err)
			require.NotNil(t, gotPending.TMDBChangedAt)
			assert.WithinDuration(t, tOld, *gotPending.TMDBChangedAt, time.Second,
				"already-pending movie must NOT reset the clock forward")

			// --- enriched-but-section-empty: sync recent, changed NULL → bumped ---
			tRecent := now.Add(-30 * time.Minute)
			enriched := seedMovie(t, db, 300, new(tRecent), nil) // sync recent, changed nil
			require.NoError(t, repo.MarkStaleForReenrich(ctx, enriched, tNew))

			gotEnriched, err := repo.Get(ctx, enriched)
			require.NoError(t, err)
			require.NotNil(t, gotEnriched.TMDBChangedAt, "not changed-pending → must be marked")
			assert.WithinDuration(t, tNew, *gotEnriched.TMDBChangedAt, time.Second)

			// --- zero id → error ---
			err = repo.MarkStaleForReenrich(ctx, domain.MovieID(0), now)
			require.Error(t, err)
		})
	}
}
