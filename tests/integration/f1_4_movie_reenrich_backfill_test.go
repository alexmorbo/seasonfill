//go:build integration

// Ф1.4 — one-shot movie re-enrichment backfill (audit F-Ф1-07). Proves the
// bulk marker MarkAllMoviesChanged stamps tmdb_changed_at on every tmdb_id
// movie (leaving tmdb_id-NULL rows untouched), drops each into the picker's
// CHANGED tier (so the throttled scheduler re-enriches them once), returns the
// correct RowsAffected, and is idempotent across a 2nd call. Reuses the Ф1.3
// harness (see f1_3_movie_changes_reenrich_test.go).
package integration

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestF1_4_MovieReenrichBackfill_Postgres(t *testing.T) {
	for _, b := range allD1Backends(t) {
		if b.name != "postgres" {
			continue
		}
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
			require.NoError(t, err)

			baseNow := time.Now().UTC().Truncate(time.Microsecond)
			// "Fresh" enrichment but comfortably older than the 15m race guard,
			// mirroring the F-Ф1-07 population (enriched hours/days ago).
			oldSync := baseNow.Add(-72 * time.Hour)

			movieRepo := enrichpersistence.NewMovieRepository(gdb)

			// Seed 3 fully-enriched movies WITH tmdb_id, recent synced_at, no
			// tmdb_changed_at (the pre-Ф1.1 state: fresh sync, never flagged).
			withTMDB := make([]shareddomain.MovieID, 0, 3)
			for i, tmdbID := range []int64{603, 604, 605} {
				tid := shareddomain.TMDBID(tmdbID)
				id, uerr := movieRepo.Upsert(ctx, movie.Canon{
					TMDBID:    &tid,
					Title:     fmt.Sprintf("m%d", i),
					Hydration: movie.HydrationFull,
				})
				require.NoError(t, uerr)
				require.NotZero(t, id)
				require.NoError(t, gdb.WithContext(ctx).Table("movies").
					Where("id = ?", id).
					Update("enrichment_tmdb_synced_at", oldSync).Error)
				withTMDB = append(withTMDB, id)
			}

			// Seed 1 movie WITHOUT tmdb_id — must never be marked or picked.
			nullID, uerr := movieRepo.Upsert(ctx, movie.Canon{
				Title:     "no-tmdb",
				Hydration: movie.HydrationStub,
			})
			require.NoError(t, uerr)
			require.NotZero(t, nullID)

			// 1. First backfill call marks exactly the 3 tmdb_id movies.
			marked, err := movieRepo.MarkAllMoviesChanged(ctx, baseNow)
			require.NoError(t, err)
			require.Equal(t, int64(3), marked, "only tmdb_id movies are marked")

			// 2. Each tmdb_id movie now carries tmdb_changed_at == baseNow.
			for _, id := range withTMDB {
				var changed *time.Time
				require.NoError(t, gdb.WithContext(ctx).Table("movies").
					Select("tmdb_changed_at").Where("id = ?", id).Scan(&changed).Error)
				require.NotNilf(t, changed, "movie %d must be marked changed", id)
				require.WithinDuration(t, baseNow, *changed, time.Second)
			}

			// 3. The tmdb_id-NULL row is untouched (tmdb_changed_at stays NULL).
			var nullChanged sql.NullTime
			require.NoError(t, gdb.WithContext(ctx).Table("movies").
				Select("tmdb_changed_at").Where("id = ?", nullID).Scan(&nullChanged).Error)
			require.False(t, nullChanged.Valid, "tmdb_id IS NULL movie must be left untouched")

			// 4. Picker re-picks the 3 movies in the CHANGED tier (the whole
			// point of the backfill). The NULL movie never appears.
			ttl := enrichdomain.DefaultRefreshTTL()
			cands, err := movieRepo.PickMovieRefreshCandidates(ctx, baseNow, ttl, 50)
			require.NoError(t, err)
			tierByID := make(map[shareddomain.MovieID]enrichdomain.RefreshTier, len(cands))
			for _, c := range cands {
				tierByID[c.MovieID] = c.Tier
			}
			for _, id := range withTMDB {
				tier, ok := tierByID[id]
				require.Truef(t, ok, "movie %d must re-pick after backfill", id)
				require.Equalf(t, enrichdomain.RefreshTierChanged, tier,
					"movie %d must land in the CHANGED tier", id)
			}
			_, ok := tierByID[nullID]
			require.False(t, ok, "tmdb_id IS NULL movie must never be a candidate")

			// 5. Idempotent: a 2nd call succeeds, still marks 3, and advances
			// tmdb_changed_at to the newer timestamp.
			later := baseNow.Add(1 * time.Hour)
			marked2, err := movieRepo.MarkAllMoviesChanged(ctx, later)
			require.NoError(t, err)
			require.Equal(t, int64(3), marked2, "2nd call re-stamps all tmdb_id movies")
			for _, id := range withTMDB {
				var changed *time.Time
				require.NoError(t, gdb.WithContext(ctx).Table("movies").
					Select("tmdb_changed_at").Where("id = ?", id).Scan(&changed).Error)
				require.NotNil(t, changed)
				require.WithinDuration(t, later, *changed, time.Second,
					"2nd call must advance tmdb_changed_at")
			}
		})
	}
}
