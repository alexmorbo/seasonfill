package persistence

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedSeries inserts N rows in the series table with deterministic
// titles + a per-row tmdb_id (NIL on the first row to exercise the
// pointer branch in Item.TMDBID). Returns the assigned ids.
func seedSeries(t *testing.T, db *gorm.DB, n int) []shareddomain.SeriesID {
	t.Helper()
	out := make([]shareddomain.SeriesID, 0, n)
	for i := range n {
		title := "disc-" + uuid.NewString()[:8]
		m := database.SeriesModel{
			Title:           title,
			Hydration:       "stub",
			InProduction:    false,
			OriginCountries: datatypes.JSON("[]"),
		}
		if i > 0 {
			id := shareddomain.TMDBID(100000 + i)
			m.TMDBID = &id
		}
		require.NoError(t, db.Create(&m).Error)
		require.NotZero(t, m.ID)
		out = append(out, m.ID)
	}
	return out
}

func itemsFor(ids []shareddomain.SeriesID) []disco.Item {
	out := make([]disco.Item, 0, len(ids))
	for _, id := range ids {
		out = append(out, disco.Item{SeriesID: id})
	}
	return out
}

func TestListRepository_ReplaceAndGetRanked_RoundTripsPositions(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewListRepository(db)
			ctx := context.Background()

			ids := seedSeries(t, db, 5)
			lang := "en-US-" + uuid.NewString()[:6]

			require.NoError(t, repo.ReplaceList(ctx,
				disco.KindTrendingDay, "", lang, itemsFor(ids)))

			page, err := repo.GetRanked(ctx, disco.KindTrendingDay, "", lang, 1, 50)
			require.NoError(t, err)
			require.Len(t, page.Items, 5)
			assert.Equal(t, 5, page.Total)
			for i, item := range page.Items {
				assert.Equal(t, ids[i], item.SeriesID,
					"position %d must map to series id at the same input index", i+1)
			}
			assert.False(t, page.RefreshedAt.IsZero())
		})
	}
}

func TestListRepository_ReplaceList_ClearsOldRows(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewListRepository(db)
			ctx := context.Background()

			ids := seedSeries(t, db, 5)
			lang := "en-US-" + uuid.NewString()[:6]

			require.NoError(t, repo.ReplaceList(ctx,
				disco.KindPopular, "", lang, itemsFor(ids)))
			// Replace with a shorter list — DELETE step must wipe the
			// orphaned positions 4 + 5 before INSERT.
			require.NoError(t, repo.ReplaceList(ctx,
				disco.KindPopular, "", lang, itemsFor(ids[:3])))

			page, err := repo.GetRanked(ctx, disco.KindPopular, "", lang, 1, 50)
			require.NoError(t, err)
			assert.Len(t, page.Items, 3)
			assert.Equal(t, 3, page.Total)
		})
	}
}

func TestListRepository_ReplaceList_EmptyClearsList(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewListRepository(db)
			ctx := context.Background()

			ids := seedSeries(t, db, 3)
			lang := "en-US-" + uuid.NewString()[:6]

			require.NoError(t, repo.ReplaceList(ctx,
				disco.KindTrendingWeek, "", lang, itemsFor(ids)))
			require.NoError(t, repo.ReplaceList(ctx,
				disco.KindTrendingWeek, "", lang, nil))

			page, err := repo.GetRanked(ctx, disco.KindTrendingWeek, "", lang, 1, 50)
			require.NoError(t, err)
			assert.Empty(t, page.Items)
			assert.Equal(t, 0, page.Total)
		})
	}
}

func TestListRepository_GetRanked_Paginates(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewListRepository(db)
			ctx := context.Background()

			ids := seedSeries(t, db, 5)
			lang := "en-US-" + uuid.NewString()[:6]

			require.NoError(t, repo.ReplaceList(ctx,
				disco.KindByGenre, "28", lang, itemsFor(ids)))

			// LIMIT 2 OFFSET 1 ⇒ positions 2 and 3.
			page, err := repo.GetRanked(ctx, disco.KindByGenre, "28", lang, 2, 2)
			require.NoError(t, err)
			require.Len(t, page.Items, 2)
			assert.Equal(t, 5, page.Total, "Total is the unpaged count")
			assert.Equal(t, ids[2], page.Items[0].SeriesID, "page=2 perPage=2 ⇒ position 3 first")
			assert.Equal(t, ids[3], page.Items[1].SeriesID, "page=2 perPage=2 ⇒ position 4 second")
		})
	}
}

func TestListRepository_IsStale_FreshFalse_NeverRefreshedTrue(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewListRepository(db)
			ctx := context.Background()

			lang := "en-US-" + uuid.NewString()[:6]
			// Never refreshed → stale.
			stale, err := repo.IsStale(ctx, disco.KindTrendingDay, "", lang, time.Hour)
			require.NoError(t, err)
			assert.True(t, stale, "never-refreshed list must be stale")

			ids := seedSeries(t, db, 2)
			require.NoError(t, repo.ReplaceList(ctx,
				disco.KindTrendingDay, "", lang, itemsFor(ids)))

			// Just refreshed → fresh.
			stale, err = repo.IsStale(ctx, disco.KindTrendingDay, "", lang, time.Hour)
			require.NoError(t, err)
			assert.False(t, stale, "freshly-refreshed list must NOT be stale")
		})
	}
}

func TestListRepository_IsStale_PastTTLTrue(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewListRepository(db)
			ctx := context.Background()

			ids := seedSeries(t, db, 2)
			lang := "en-US-" + uuid.NewString()[:6]
			require.NoError(t, repo.ReplaceList(ctx,
				disco.KindByNetwork, "9", lang, itemsFor(ids)))

			// Force refreshed_at back 2h via direct UPDATE.
			twoHoursAgo := time.Now().UTC().Add(-2 * time.Hour)
			require.NoError(t, db.Exec(
				`UPDATE discovery_lists SET refreshed_at = ?
				 WHERE kind = ? AND param = ? AND language = ?`,
				twoHoursAgo, string(disco.KindByNetwork), "9", lang,
			).Error)

			stale, err := repo.IsStale(ctx, disco.KindByNetwork, "9", lang, time.Hour)
			require.NoError(t, err)
			assert.True(t, stale, "refreshed 2h ago with ttl=1h must be stale")
		})
	}
}

func TestListRepository_LastRefreshedAt_ZeroWhenEmpty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewListRepository(db)
			lang := "en-US-" + uuid.NewString()[:6]
			at, err := repo.LastRefreshedAt(context.Background(),
				disco.KindByKeyword, "abc", lang)
			require.NoError(t, err)
			assert.True(t, at.IsZero(), "empty result must be the zero time, not now()")
		})
	}
}

func TestListRepository_GetRanked_NullTMDBIDPreserved(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewListRepository(db)
			ctx := context.Background()

			ids := seedSeries(t, db, 3) // ids[0] has NULL tmdb_id by construction.
			lang := "en-US-" + uuid.NewString()[:6]
			require.NoError(t, repo.ReplaceList(ctx,
				disco.KindPopular, "", lang, itemsFor(ids)))

			page, err := repo.GetRanked(ctx, disco.KindPopular, "", lang, 1, 50)
			require.NoError(t, err)
			require.Len(t, page.Items, 3)
			assert.Nil(t, page.Items[0].TMDBID, "position 1 series has NULL tmdb_id → Item.TMDBID nil")
			require.NotNil(t, page.Items[1].TMDBID, "position 2 series has tmdb_id → Item.TMDBID populated")
		})
	}
}

// TestListRepository_GetRanked_TVDBIDAndOriginalLanguage — story 523.
// One seeded series carries tvdb_id + original_language, the other
// leaves both NULL. Both round-trip through GetRanked: the populated
// row hydrates the pointer fields, the NULL row keeps them nil. Pins
// the N-4 unblock contract — the FE AddToSonarr modal reads tvdb_id
// straight off the discovery list response.
func TestListRepository_GetRanked_TVDBIDAndOriginalLanguage(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewListRepository(db)
			ctx := context.Background()

			lang := "en-US-" + uuid.NewString()[:6]

			// Row A — populated tvdb_id + original_language.
			tvdb := shareddomain.TVDBID(81189)
			ol := "en"
			a := database.SeriesModel{
				Title:            "with-tvdb-" + uuid.NewString()[:6],
				Hydration:        "stub",
				InProduction:     false,
				OriginCountries:  datatypes.JSON("[]"),
				TVDBID:           &tvdb,
				OriginalLanguage: &ol,
			}
			require.NoError(t, db.Create(&a).Error)

			// Row B — NULL tvdb_id, NULL original_language.
			b := database.SeriesModel{
				Title:           "no-tvdb-" + uuid.NewString()[:6],
				Hydration:       "stub",
				InProduction:    false,
				OriginCountries: datatypes.JSON("[]"),
			}
			require.NoError(t, db.Create(&b).Error)

			ids := []shareddomain.SeriesID{a.ID, b.ID}
			require.NoError(t, repo.ReplaceList(ctx,
				disco.KindTrendingDay, "", lang, itemsFor(ids)))

			page, err := repo.GetRanked(ctx, disco.KindTrendingDay, "", lang, 1, 50)
			require.NoError(t, err)
			require.Len(t, page.Items, 2)

			require.NotNil(t, page.Items[0].TVDBID,
				"position 1 series has tvdb_id → Item.TVDBID populated")
			assert.Equal(t, shareddomain.TVDBID(81189), *page.Items[0].TVDBID)
			require.NotNil(t, page.Items[0].OriginalLanguage,
				"position 1 series has original_language → field populated")
			assert.Equal(t, "en", *page.Items[0].OriginalLanguage)

			assert.Nil(t, page.Items[1].TVDBID,
				"position 2 series has NULL tvdb_id → Item.TVDBID nil")
			assert.Nil(t, page.Items[1].OriginalLanguage,
				"position 2 series has NULL original_language → field nil")
		})
	}
}

func TestListRepository_ReplaceList_OrphanSeriesIDErrors(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			// SQLite shadow (testhelpers) does NOT enable
			// `_pragma=foreign_keys(1)` — FK enforcement only fires on
			// the Postgres backend (and on the integration test SQLite
			// handle, which opens with the pragma explicitly). Skip the
			// SQLite assertion here; the Postgres lane proves the
			// invariant.
			if backend.Name == "sqlite" {
				t.Skip("sqlite shadow has FK enforcement off; postgres lane proves the invariant")
			}
			db := backend.NewDB(t)
			repo := NewListRepository(db)
			lang := "en-US-" + uuid.NewString()[:6]

			err := repo.ReplaceList(context.Background(),
				disco.KindTrendingDay, "", lang,
				[]disco.Item{{SeriesID: 9_999_999}})
			require.Error(t, err, "orphan series_id must trigger FK error")
		})
	}
}

func TestListRepository_InvalidKindErrors(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewListRepository(db)
			ctx := context.Background()

			_, err := repo.GetRanked(ctx, disco.Kind("bogus"), "", "en-US", 1, 10)
			require.Error(t, err)
			_, err = repo.IsStale(ctx, disco.Kind("bogus"), "", "en-US", time.Hour)
			require.Error(t, err)
			_, err = repo.LastRefreshedAt(ctx, disco.Kind("bogus"), "", "en-US")
			require.Error(t, err)
			err = repo.ReplaceList(ctx, disco.Kind("bogus"), "", "en-US", nil)
			require.Error(t, err)
		})
	}
}

// postgresEnabledForConcurrency mirrors the testhelpers gate. We check
// the env vars directly rather than going through AllBackends so the
// gate check does not require a *testing.T to invoke (the helper's
// signature requires it).
func postgresEnabledForConcurrency() bool {
	if v := os.Getenv("SEASONFILL_TEST_POSTGRES_ENABLE"); v != "" && v != "0" && v != "false" {
		return true
	}
	if v := os.Getenv("SEASONFILL_TEST_POSTGRES_DSN"); v != "" {
		return true
	}
	return false
}

func TestListRepository_ReplaceList_ConcurrentSerializes(t *testing.T) {
	// Skip SQLite under -race: SQLite serialises writes via the
	// single-connection pool the testhelpers cache pins, so the
	// concurrency story is trivially "one writes, the other waits".
	// Postgres exercises the real row-level lock path.
	if !postgresEnabledForConcurrency() {
		t.Skip("postgres-only test (concurrency is trivial on the SQLite single-connection pool)")
	}
	for _, backend := range testhelpers.AllBackends(t) {
		if backend.Name != "postgres" {
			continue
		}
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.NewDB(t)
			repo := NewListRepository(db)
			ctx := context.Background()

			ids := seedSeries(t, db, 5)
			lang := "en-US-" + uuid.NewString()[:6]
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = repo.ReplaceList(ctx, disco.KindPopular, "", lang, itemsFor(ids[:3]))
			}()
			go func() {
				defer wg.Done()
				_ = repo.ReplaceList(ctx, disco.KindPopular, "", lang, itemsFor(ids[2:]))
			}()
			wg.Wait()

			page, err := repo.GetRanked(ctx, disco.KindPopular, "", lang, 1, 50)
			require.NoError(t, err)
			// One writer wins — either the 3-item or the 3-item list,
			// not a merged 6-item set.
			assert.Equal(t, 3, page.Total, "exactly one ReplaceList must have won (got Total=%d)", page.Total)
		})
	}
}
