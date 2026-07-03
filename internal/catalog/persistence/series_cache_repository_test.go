package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func sampleEntry(instance domain.InstanceName, id domain.SonarrSeriesID) series.CacheEntry {
	// Post B-1b cutover: every cache row resolves to a distinct canon
	// row via natural-key dedup (TMDB > TVDB > IMDB). To preserve
	// per-(instance, sonarr_id) test isolation we derive unique
	// external ids from the sonarr id so two test rows never collapse
	// into one canon row by accident. Tests that need shared canon
	// (cutover dedup scenarios) override TMDBID/TVDBID explicitly.
	tvdb := domain.TVDBID(12345 + int(id))
	tmdb := domain.TMDBID(54321 + int(id))
	return series.CacheEntry{
		InstanceName:   instance,
		SonarrSeriesID: id,
		Title:          "Test Series",
		TitleSlug:      "test-series",
		Year:           new(2024),
		TVDBID:         &tvdb,
		IMDBID:         ptrIMDBID(fmt.Sprintf("tt%07d", 9000000+int(id))),
		TMDBID:         &tmdb,
		Status:         new("continuing"),
		Genres:         []string{"Drama", "Comedy"},
		RuntimeMinutes: new(60),
		Monitored:      true,
		Overview:       new("Overview text."),
		FanartPath:     new("/MediaCover/12/fanart.jpg"),
		BannerPath:     new("/MediaCover/12/banner.jpg"),
	}
}

// seedNetworkJoinForCache wires (networks, series_networks) for the
// series_cache row resolved by (instance, sonarrID). E-1: post-cutover
// network membership lives in series_networks; this helper is the
// minimal-invasive bridge for tests that previously seeded via
// CacheEntry.Network. Empty `name` is a no-op (clears nothing — just
// skips so the row stays without a network join).
func seedNetworkJoinForCache(t *testing.T, db *gorm.DB, instance domain.InstanceName, sonarrID int, name string) {
	t.Helper()
	if name == "" {
		return
	}
	var sc database.SeriesCacheModel
	require.NoError(t, db.Where(
		"instance_name = ? AND sonarr_series_id = ?", instance, sonarrID,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID, "series_cache row must have a resolved series_id")
	repo := NewNetworksRepository(db)
	id, err := repo.ResolveByName(context.Background(), name)
	if err != nil {
		id, err = repo.Upsert(context.Background(), taxonomy.Network{Name: name})
		require.NoError(t, err)
	}
	require.NoError(t, db.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.SeriesNetworkModel{
		SeriesID:  *sc.SeriesID,
		NetworkID: id,
	}).Error)
}

// seedSeriesTextForCache writes a series_texts row for the canon series
// resolved by (instance, sonarrID). S-E2 tests use it to give the list
// query a localized title to resolve/search/sort on (post-repoint the
// catalog no longer reads canon series.title). Idempotent upsert.
func seedSeriesTextForCache(t *testing.T, db *gorm.DB, instance domain.InstanceName, sonarrID int, lang, title string) {
	t.Helper()
	var sc database.SeriesCacheModel
	require.NoError(t, db.Where(
		"instance_name = ? AND sonarr_series_id = ?", instance, sonarrID,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID, "series_cache row must have a resolved series_id")
	require.NoError(t, db.Exec(
		`INSERT INTO series_texts (series_id, language, title, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (series_id, language) DO UPDATE SET title = excluded.title`,
		int64(*sc.SeriesID), lang, title, time.Now().UTC(),
	).Error)
}

func TestSeriesCacheRepository_Upsert_Insert_Get(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 12)))
			// S-E3a — the point read resolves s_title from series_texts (en-US),
			// no longer canon series.title. Seed the base-lang row.
			seedSeriesTextForCache(t, db, "main", 12, "en-US", "Test Series")
			got, err := repo.Get(ctx, "main", 12)
			require.NoError(t, err)
			assert.Equal(t, domain.InstanceName("main"), got.InstanceName)
			assert.Equal(t, domain.SonarrSeriesID(12), got.SonarrSeriesID)
			assert.Equal(t, "Test Series", got.Title)
			assert.Equal(t, "test-series", got.TitleSlug)
			require.NotNil(t, got.Year)
			assert.Equal(t, 2024, *got.Year)
			// Post B-1b cutover: genres / overview / fanart / banner project nil
			// from the repo; canon stores them in joined tables (series_genres,
			// series_texts, media_assets). Production DTO already drops them.
			assert.Nil(t, got.Genres)
			assert.True(t, got.Monitored)
			assert.False(t, got.UpdatedAt.IsZero())
			assert.Nil(t, got.DeletedAt)
			assert.True(t, got.IsActive())
		})
	}
}

func TestSeriesCacheRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			_, err := repo.Get(context.Background(), "main", 999)
			require.Error(t, err)

			var typedErr *sharedErrors.SeriesCacheNotFoundError
			require.True(t, errors.As(err, &typedErr),
				"Get NotFound must expose typed SeriesCacheNotFoundError via errors.As")
			assert.Equal(t, domain.InstanceName("main"), typedErr.InstanceName)
			assert.Equal(t, domain.SonarrSeriesID(999), typedErr.SonarrSeriesID)
		})
	}
}

func TestSeriesCacheRepository_Upsert_Replaces_AndResurrects(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 12)))

			// Replace.
			second := sampleEntry("main", 12)
			second.Title = "Renamed"
			second.Monitored = false
			second.Genres = []string{"Thriller"}
			require.NoError(t, repo.Upsert(ctx, second))
			// S-E3a — display title resolves from series_texts (en-US); the cache
			// rename must be reflected via the side-table row.
			seedSeriesTextForCache(t, db, "main", 12, "en-US", "Renamed")
			got, err := repo.Get(ctx, "main", 12)
			require.NoError(t, err)
			assert.Equal(t, "Renamed", got.Title)
			assert.False(t, got.Monitored)
			// Post B-1b: Genres not persisted on the thin cache row; canon has
			// the series_genres join (deferred to E-1). Repo returns nil.
			assert.Nil(t, got.Genres)

			// Resurrect: soft-delete then upsert clears deleted_at.
			require.NoError(t, repo.SoftDelete(ctx, "main", 12))
			gotSoft, err := repo.Get(ctx, "main", 12)
			require.NoError(t, err)
			require.NotNil(t, gotSoft.DeletedAt)
			assert.False(t, gotSoft.IsActive())

			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 12)))
			gotAlive, err := repo.Get(ctx, "main", 12)
			require.NoError(t, err)
			assert.Nil(t, gotAlive.DeletedAt)
			assert.True(t, gotAlive.IsActive())
		})
	}
}

func TestSeriesCacheRepository_SoftDelete_Idempotent_AndMissing(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			// Missing row → nil (webhook out-of-order safety).
			require.NoError(t, repo.SoftDelete(ctx, "main", 9999))

			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 12)))
			require.NoError(t, repo.SoftDelete(ctx, "main", 12))
			time.Sleep(2 * time.Millisecond)
			require.NoError(t, repo.SoftDelete(ctx, "main", 12),
				"already-deleted row → nil")
			got, err := repo.Get(ctx, "main", 12)
			require.NoError(t, err)
			require.NotNil(t, got.DeletedAt)
		})
	}
}

func TestSeriesCacheRepository_ListActiveByInstance(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()
			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 1)))
			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 2)))
			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 3)))
			require.NoError(t, repo.Upsert(ctx, sampleEntry("other", 1)))
			require.NoError(t, repo.SoftDelete(ctx, "main", 2))

			active, err := repo.ListActiveByInstance(ctx, "main")
			require.NoError(t, err)
			assert.Len(t, active, 2)
			for _, e := range active {
				assert.True(t, e.IsActive())
				assert.Equal(t, domain.InstanceName("main"), e.InstanceName)
				assert.NotEqual(t, 2, e.SonarrSeriesID)
			}

			// Empty result is non-nil.
			got, err := repo.ListActiveByInstance(ctx, "nonexistent")
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Empty(t, got)
		})
	}
}

// Post B-1b cutover: Genres no longer round-trip via series_cache —
// canon stores them in the series_genres join (deferred to E-1).
// Whatever the caller writes is dropped at the repo edge and the read
// path always returns nil. This regression-locks that contract.
func TestSeriesCacheRepository_GenresAlwaysNilPostCutover(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()
			entry := sampleEntry("main", 100)
			entry.Genres = []string{"Drama", "Sci-Fi"}
			require.NoError(t, repo.Upsert(ctx, entry))
			got, err := repo.Get(ctx, "main", 100)
			require.NoError(t, err)
			assert.Nil(t, got.Genres,
				"post B-1b cutover: genres are not stored on series_cache; canon's series_genres join is the source")
		})
	}
}

func TestSeriesCacheRepository_NilPointerFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()
			require.NoError(t, repo.Upsert(ctx, series.CacheEntry{
				InstanceName: "main", SonarrSeriesID: 7,
				Title: "Minimal", TitleSlug: "minimal",
			}))
			// S-E3a — title resolves from series_texts (en-US). Poster is left
			// unseeded so series_media_texts stays absent → nil poster (asserted).
			seedSeriesTextForCache(t, db, "main", 7, "en-US", "Minimal")
			got, err := repo.Get(ctx, "main", 7)
			require.NoError(t, err)
			assert.Equal(t, "Minimal", got.Title)
			for _, p := range []any{
				got.Year, got.TVDBID, got.IMDBID, got.TMDBID,
				got.Status, got.Genres,
				got.RuntimeMinutes, got.Overview,
				got.FanartPath, got.BannerPath,
			} {
				assert.Nil(t, p)
			}
		})
	}
}

func TestSeriesCacheRepository_Upsert_RejectsZeroPK(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()
			require.Error(t, repo.Upsert(ctx, sampleEntry("", 1)))
			require.Error(t, repo.Upsert(ctx, sampleEntry("main", 0)))
		})
	}
}

func TestSeriesCacheRepository_Upsert_StampsUpdatedAt(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()
			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 12)))
			first, err := repo.Get(ctx, "main", 12)
			require.NoError(t, err)
			time.Sleep(5 * time.Millisecond)
			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 12)))
			second, err := repo.Get(ctx, "main", 12)
			require.NoError(t, err)
			assert.True(t, second.UpdatedAt.After(first.UpdatedAt))
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_StateAll_UpdatedDesc(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			now := time.Now().UTC()
			for i := 1; i <= 5; i++ {
				entry := sampleEntry("main", domain.SonarrSeriesID(i))
				entry.Title = fmt.Sprintf("Series %d", i)
				require.NoError(t, repo.Upsert(ctx, entry))
				require.NoError(t, db.Model(&database.SeriesCacheModel{}).
					Where("instance_name = ? AND sonarr_series_id = ?", "main", i).
					Update("updated_at", now.Add(time.Duration(i)*time.Minute)).Error)
			}

			items, total, hasMore, next, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll},
				ports.SeriesCacheSortUpdatedDesc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			assert.Equal(t, 5, total)
			assert.False(t, hasMore)
			assert.Nil(t, next)
			require.Len(t, items, 5)
			assert.Equal(t, domain.SonarrSeriesID(5), items[0].SonarrSeriesID)
			assert.Equal(t, domain.SonarrSeriesID(1), items[4].SonarrSeriesID)
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_StateMissing(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			for i := 1; i <= 5; i++ {
				entry := sampleEntry("main", domain.SonarrSeriesID(i))
				if i%2 == 0 {
					entry.MissingCount = i
				}
				require.NoError(t, repo.Upsert(ctx, entry))
			}

			items, total, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateMissing},
				ports.SeriesCacheSortUpdatedDesc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			assert.Equal(t, 2, total)
			require.Len(t, items, 2)
			for _, it := range items {
				assert.Greater(t, it.MissingCount, 0)
			}
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_StateImported_SubqueryWindow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			grabs := grabpersistence.NewGrabRepository(db)
			ctx := context.Background()

			for i := 1; i <= 3; i++ {
				require.NoError(t, repo.Upsert(ctx, sampleEntry("main", domain.SonarrSeriesID(i))))
			}
			// Seed the FK targets so the direct grab inserts below
			// satisfy grab_records_instance_name_fkey AND
			// grab_records_scan_run_id_fkey on Postgres.
			seedSonarrInstance(t, db, "main")
			scanRunID := seedScanRun(t, db, "main")

			now := time.Now().UTC()
			require.NoError(t, grabs.Create(ctx, grab.Record{
				ID: uuid.New(), InstanceName: "main", SeriesID: 1, SeasonNumber: 1,
				ScanRunID: scanRunID, Status: grab.StatusImported,
				CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-48 * time.Hour),
			}))
			require.NoError(t, grabs.Create(ctx, grab.Record{
				ID: uuid.New(), InstanceName: "main", SeriesID: 2, SeasonNumber: 1,
				ScanRunID: scanRunID, Status: grab.StatusImported,
				CreatedAt: now.Add(-10 * 24 * time.Hour), UpdatedAt: now.Add(-10 * 24 * time.Hour),
			}))
			require.NoError(t, grabs.Create(ctx, grab.Record{
				ID: uuid.New(), InstanceName: "main", SeriesID: 3, SeasonNumber: 1,
				ScanRunID: scanRunID, Status: grab.StatusGrabbed,
				CreatedAt: now.Add(-1 * time.Hour), UpdatedAt: now.Add(-1 * time.Hour),
			}))

			items, total, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateImported},
				ports.SeriesCacheSortUpdatedDesc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			assert.Equal(t, 1, total)
			require.Len(t, items, 1)
			assert.Equal(t, domain.SonarrSeriesID(1), items[0].SonarrSeriesID)
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_KeysetPagination(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			now := time.Now().UTC()
			for i := 1; i <= 30; i++ {
				entry := sampleEntry("main", domain.SonarrSeriesID(i))
				entry.Title = fmt.Sprintf("Series %02d", i)
				require.NoError(t, repo.Upsert(ctx, entry))
				require.NoError(t, db.Model(&database.SeriesCacheModel{}).
					Where("instance_name = ? AND sonarr_series_id = ?", "main", i).
					Update("updated_at", now.Add(time.Duration(i)*time.Minute)).Error)
			}

			seen := map[domain.SonarrSeriesID]bool{}
			page := ports.Pagination{Limit: 12}
			for range 4 {
				items, total, hasMore, next, err := repo.ListByFilter(ctx, "main",
					ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll},
					ports.SeriesCacheSortUpdatedDesc,
					page)
				require.NoError(t, err)
				assert.Equal(t, 30, total)
				for _, it := range items {
					assert.False(t, seen[it.SonarrSeriesID], "no duplicates across pages")
					seen[it.SonarrSeriesID] = true
				}
				if !hasMore {
					break
				}
				page.Cursor = next
			}
			assert.Len(t, seen, 30)
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_Search_MatchesTitleCaseInsensitive(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			cases := []struct {
				id    domain.SonarrSeriesID
				title string
				slug  string
			}{
				{1, "Rick and Morty", "rick-and-morty"},
				{2, "Severance", "severance"},
				{3, "For All Mankind", "for-all-mankind"},
				{4, "Foundation", "foundation"},
			}
			for _, c := range cases {
				entry := sampleEntry("main", c.id)
				entry.Title = c.title
				entry.TitleSlug = c.slug
				require.NoError(t, repo.Upsert(ctx, entry))
				seedSeriesTextForCache(t, db, "main", int(c.id), "en-US", c.title)
			}

			queries := []struct {
				q       string
				wantIDs []int
			}{
				{"rick", []int{1}},
				{"RICK", []int{1}},
				{"Rick and Morty", []int{1}},
				{"and", []int{1}},
				{"foundation", []int{4}},
				{"  Severance  ", []int{2}}, // trimmed
				{"nope", []int{}},
				{"", []int{1, 2, 3, 4}}, // empty ⇒ no filter
			}
			for _, tc := range queries {
				t.Run(fmt.Sprintf("q=%q", tc.q), func(t *testing.T) {
					items, total, _, _, err := repo.ListByFilter(ctx, "main",
						ports.SeriesCacheFilter{
							State:  ports.SeriesCacheStateAll,
							Search: tc.q,
						},
						ports.SeriesCacheSortTitleAsc,
						ports.Pagination{Limit: 50})
					require.NoError(t, err)
					assert.Equal(t, len(tc.wantIDs), total, "total reflects post-q count")
					gotIDs := make([]int, 0, len(items))
					for _, it := range items {
						gotIDs = append(gotIDs, int(it.SonarrSeriesID))
					}
					assert.ElementsMatch(t, tc.wantIDs, gotIDs)
				})
			}
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_Search_MatchesSlug(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			// Title doesn't contain the term; slug does.
			entry := sampleEntry("main", 1)
			entry.Title = "Severance"
			entry.TitleSlug = "severance-2022"
			require.NoError(t, repo.Upsert(ctx, entry))

			items, total, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{
					State:  ports.SeriesCacheStateAll,
					Search: "2022",
				},
				ports.SeriesCacheSortUpdatedDesc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			assert.Equal(t, 1, total)
			require.Len(t, items, 1)
			assert.Equal(t, domain.SonarrSeriesID(1), items[0].SonarrSeriesID)
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_Search_EscapesWildcards(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			// One row with a literal `%` and `_` in the title; one without.
			a := sampleEntry("main", 1)
			a.Title = "100% Wolf"
			a.TitleSlug = "100-percent-wolf"
			require.NoError(t, repo.Upsert(ctx, a))
			seedSeriesTextForCache(t, db, "main", 1, "en-US", "100% Wolf")

			b := sampleEntry("main", 2)
			b.Title = "Severance"
			b.TitleSlug = "severance"
			require.NoError(t, repo.Upsert(ctx, b))
			seedSeriesTextForCache(t, db, "main", 2, "en-US", "Severance")

			// `%` in user input must match the literal `%` row only — NOT
			// degenerate to "match anything" (which is what unescaped LIKE does).
			items, total, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{
					State:  ports.SeriesCacheStateAll,
					Search: "%",
				},
				ports.SeriesCacheSortUpdatedDesc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			assert.Equal(t, 1, total, "%% must be escaped — only the literal-%% row matches")
			require.Len(t, items, 1)
			assert.Equal(t, domain.SonarrSeriesID(1), items[0].SonarrSeriesID)

			// `_` in user input — same story; LIKE-meaningful underscore must NOT
			// match every single-char row. Re-use the `100%_Wolf` row by searching
			// for the literal `% ` substring of its title.
			items, total, _, _, err = repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{
					State:  ports.SeriesCacheStateAll,
					Search: "% W",
				},
				ports.SeriesCacheSortUpdatedDesc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			assert.Equal(t, 1, total)
			assert.Equal(t, domain.SonarrSeriesID(1), items[0].SonarrSeriesID)
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_TitleAsc(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			titles := []string{"Zulu", "Alpha", "Mike", "bravo", "charlie"}
			for i, title := range titles {
				entry := sampleEntry("main", domain.SonarrSeriesID(i+1))
				entry.Title = title
				require.NoError(t, repo.Upsert(ctx, entry))
				seedSeriesTextForCache(t, db, "main", i+1, "en-US", title)
			}

			items, _, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll},
				ports.SeriesCacheSortTitleAsc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			require.Len(t, items, 5)
			got := make([]string, 0, 5)
			for _, it := range items {
				got = append(got, strings.ToLower(it.Title))
			}
			assert.Equal(t, []string{"alpha", "bravo", "charlie", "mike", "zulu"}, got)
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_Search_AllLanguages_569(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			// Row 1: en-US "Presumed Innocent" + ru-RU "Презумпция невиновности".
			e1 := sampleEntry("main", 1)
			e1.Title = "Presumed Innocent"
			e1.TitleSlug = "presumed-innocent"
			require.NoError(t, repo.Upsert(ctx, e1))
			seedSeriesTextForCache(t, db, "main", 1, "en-US", "Presumed Innocent")
			seedSeriesTextForCache(t, db, "main", 1, "ru-RU", "Презумпция невиновности")

			// Row 2: a distractor with no matching text.
			e2 := sampleEntry("main", 2)
			e2.Title = "Severance"
			e2.TitleSlug = "severance"
			require.NoError(t, repo.Upsert(ctx, e2))
			seedSeriesTextForCache(t, db, "main", 2, "en-US", "Severance")

			// Russian query (matching case — sqlite folds ASCII only;
			// Postgres would also match lowercase) finds the row whose
			// display/en-US title is English, via its ru-RU series_texts row.
			items, total, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll, Search: "Презумпция", Lang: "ru-RU"},
				ports.SeriesCacheSortTitleAsc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			require.Equal(t, 1, total)
			require.Len(t, items, 1)
			assert.Equal(t, domain.SonarrSeriesID(1), items[0].SonarrSeriesID)

			// English query still finds it too.
			items, total, _, _, err = repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll, Search: "presumed", Lang: "en-US"},
				ports.SeriesCacheSortTitleAsc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			assert.Equal(t, 1, total)
			require.Len(t, items, 1)
			assert.Equal(t, domain.SonarrSeriesID(1), items[0].SonarrSeriesID)
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_TitleAsc_PerLanguage(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			// Three series. en-US titles Alpha/Mike/Zulu; ru-RU titles chosen
			// so the Russian ordering differs, and series 3 has NO ru row
			// (en-US fallback). Pure-ASCII ru titles keep the order assertion
			// dialect-stable (sqlite byte-orders, Cyrillic collation differs).
			rows := []struct {
				id     int
				en, ru string
				seedRu bool
			}{
				{1, "Zulu", "AAA_ru", true},  // ru sorts FIRST under ru-RU
				{2, "Alpha", "BBB_ru", true}, // ru sorts SECOND
				{3, "Mike", "", false},       // no ru row → en-US "Mike"
			}
			for _, r := range rows {
				e := sampleEntry("main", domain.SonarrSeriesID(r.id))
				e.Title = r.en
				require.NoError(t, repo.Upsert(ctx, e))
				seedSeriesTextForCache(t, db, "main", r.id, "en-US", r.en)
				if r.seedRu {
					seedSeriesTextForCache(t, db, "main", r.id, "ru-RU", r.ru)
				}
			}

			// Under ru-RU: order by resolved title → AAA_ru(1), BBB_ru(2),
			// Mike(3, en-US fallback).
			items, _, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll, Lang: "ru-RU"},
				ports.SeriesCacheSortTitleAsc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			require.Len(t, items, 3)
			gotRu := []int{int(items[0].SonarrSeriesID), int(items[1].SonarrSeriesID), int(items[2].SonarrSeriesID)}
			assert.Equal(t, []int{1, 2, 3}, gotRu, "ru-RU order by russian titles, en-US fallback last")
			// Display title reflects the resolved language.
			assert.Equal(t, "AAA_ru", items[0].Title)
			assert.Equal(t, "Mike", items[2].Title, "series 3 falls back to en-US")

			// Under en-US: order by english titles → Alpha(2), Mike(3), Zulu(1).
			items, _, _, _, err = repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll, Lang: "en-US"},
				ports.SeriesCacheSortTitleAsc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			require.Len(t, items, 3)
			gotEn := []int{int(items[0].SonarrSeriesID), int(items[1].SonarrSeriesID), int(items[2].SonarrSeriesID)}
			assert.Equal(t, []int{2, 3, 1}, gotEn, "en-US order by english titles")
			assert.Equal(t, "Alpha", items[0].Title)
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_InvalidState(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			_, _, _, _, err := repo.ListByFilter(context.Background(), "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheState("bogus")},
				ports.SeriesCacheSortUpdatedDesc,
				ports.Pagination{Limit: 10})
			require.Error(t, err)
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_SkipsSoftDeleted(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 1)))
			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 2)))
			require.NoError(t, repo.SoftDelete(ctx, "main", 2))

			items, total, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll},
				ports.SeriesCacheSortUpdatedDesc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			assert.Equal(t, 1, total)
			require.Len(t, items, 1)
			assert.Equal(t, domain.SonarrSeriesID(1), items[0].SonarrSeriesID)
		})
	}
}

func TestSeriesCacheRepository_FetchLastGrabInfo(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			grabs := grabpersistence.NewGrabRepository(db)
			ctx := context.Background()
			// Seed the FK targets so the direct grab inserts below
			// satisfy grab_records_instance_name_fkey AND
			// grab_records_scan_run_id_fkey on Postgres.
			seedSonarrInstance(t, db, "main")
			scanRunID := seedScanRun(t, db, "main")

			now := time.Now().UTC()
			require.NoError(t, grabs.Create(ctx, grab.Record{
				ID: uuid.New(), InstanceName: "main", SeriesID: 1, SeasonNumber: 3,
				ScanRunID: scanRunID, Status: grab.StatusImported,
				CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-3 * time.Hour),
			}))
			require.NoError(t, grabs.Create(ctx, grab.Record{
				ID: uuid.New(), InstanceName: "main", SeriesID: 1, SeasonNumber: 5,
				ScanRunID: scanRunID, Status: grab.StatusImported,
				CreatedAt: now.Add(-1 * time.Hour), UpdatedAt: now.Add(-1 * time.Hour),
			}))

			out, err := repo.FetchLastGrabInfo(ctx, "main", []domain.SonarrSeriesID{1, 2})
			require.NoError(t, err)
			require.Contains(t, out, domain.SonarrSeriesID(1))
			assert.Equal(t, "S05", out[1].LastImportedEpisode)
			assert.WithinDuration(t, now.Add(-1*time.Hour), out[1].LastGrabAt, time.Second)
			assert.NotContains(t, out, domain.SonarrSeriesID(2))
			_ = errors.New // keep errors import used in existing file
		})
	}
}

func TestSeriesCacheRepository_Upsert_PersistsLastAiredAt(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			// Note: canon stores last_air_date as DATE on Postgres and
			// `datetime` on SQLite — the dialect divergence is intentional
			// (last-aired IS semantically a date). We use a date-aligned
			// midnight UTC value so the round-trip is byte-identical on
			// both backends. Pre-dual-backend this test set 12:00 UTC and
			// silently relied on SQLite tolerating the extra precision;
			// the postgres branch caught the latent contract drift.
			aired := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
			entry := sampleEntry("main", 42)
			entry.LastAiredAt = &aired
			require.NoError(t, repo.Upsert(ctx, entry))

			got, err := repo.Get(ctx, "main", 42)
			require.NoError(t, err)
			require.NotNil(t, got.LastAiredAt)
			assert.True(t, got.LastAiredAt.Equal(aired),
				"want=%v got=%v", aired, got.LastAiredAt)
		})
	}
}

// TestSeriesCacheRepository_ListByFilter_MonitoredOnly — Story 121a §A
func TestSeriesCacheRepository_ListByFilter_MonitoredOnly(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			m := sampleEntry("main", 1)
			m.Title = "Rick and Morty"
			m.Monitored = true
			require.NoError(t, repo.Upsert(ctx, m))

			u := sampleEntry("main", 2)
			u.Title = "Severance"
			u.Monitored = false
			require.NoError(t, repo.Upsert(ctx, u))

			tru := true
			fal := false
			cases := []struct {
				name    string
				ptr     *bool
				wantIDs []int
			}{
				{"nil = any", nil, []int{1, 2}},
				{"true = monitored only", &tru, []int{1}},
				{"false = unmonitored only", &fal, []int{2}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					items, total, _, _, err := repo.ListByFilter(ctx, "main",
						ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll, MonitoredOnly: tc.ptr},
						ports.SeriesCacheSortTitleAsc,
						ports.Pagination{Limit: 50})
					require.NoError(t, err)
					assert.Equal(t, len(tc.wantIDs), total)
					gotIDs := make([]int, 0, len(items))
					for _, it := range items {
						gotIDs = append(gotIDs, int(it.SonarrSeriesID))
					}
					assert.ElementsMatch(t, tc.wantIDs, gotIDs)
				})
			}
		})
	}
}

// TestSeriesCacheRepository_ListByFilter_Networks — Story 121a §A,
// updated for E-1 (Story 210): network membership lives in
// series_networks; tests seed the join via seedNetworkJoinForCache.
func TestSeriesCacheRepository_ListByFilter_Networks(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			seed := []struct {
				id      domain.SonarrSeriesID
				title   string
				network string
			}{
				{1, "Show A", "HBO"},
				{2, "Show B", "Apple TV+"},
				{3, "Show C", "Apple TV+"},
				{4, "Show D", ""},
				{5, "Show E", "Netflix"},
			}
			for _, s := range seed {
				e := sampleEntry("main", s.id)
				e.Title = s.title
				require.NoError(t, repo.Upsert(ctx, e))
				seedNetworkJoinForCache(t, db, "main", int(s.id), s.network)
			}

			cases := []struct {
				name    string
				nets    []string
				wantIDs []int
			}{
				{"empty = no filter", nil, []int{1, 2, 3, 4, 5}},
				{"single = HBO", []string{"HBO"}, []int{1}},
				{"set = HBO + Netflix", []string{"HBO", "Netflix"}, []int{1, 5}},
				{"unknown = none", []string{"NopeTV"}, nil},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					items, total, _, _, err := repo.ListByFilter(ctx, "main",
						ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll, Networks: tc.nets},
						ports.SeriesCacheSortTitleAsc,
						ports.Pagination{Limit: 50})
					require.NoError(t, err)
					assert.Equal(t, len(tc.wantIDs), total)
					gotIDs := make([]int, 0, len(items))
					for _, it := range items {
						gotIDs = append(gotIDs, int(it.SonarrSeriesID))
					}
					assert.ElementsMatch(t, tc.wantIDs, gotIDs)
				})
			}
		})
	}
}

// TestSeriesCacheRepository_ListByFilter_CombinedFilters — Story 121a §A
// Tests that state + search + monitored + networks intersect correctly.
func TestSeriesCacheRepository_ListByFilter_CombinedFilters(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			seed := []struct {
				id        domain.SonarrSeriesID
				title     string
				network   string
				monitored bool
				missing   int
			}{
				// matches: missing + monitored + Apple TV+ + "for"
				{1, "For All Mankind", "Apple TV+", true, 3},
				// wrong state (no missing)
				{2, "Foundation", "Apple TV+", true, 0},
				// wrong network
				{3, "For The Crown", "Netflix", true, 5},
				// wrong monitored
				{4, "For the Win", "Apple TV+", false, 7},
				// wrong search
				{5, "Severance", "Apple TV+", true, 2},
			}
			for _, s := range seed {
				e := sampleEntry("main", s.id)
				e.Title = s.title
				e.Monitored = s.monitored
				e.MissingCount = s.missing
				require.NoError(t, repo.Upsert(ctx, e))
				seedSeriesTextForCache(t, db, "main", int(s.id), "en-US", s.title)
				seedNetworkJoinForCache(t, db, "main", int(s.id), s.network)
			}

			tru := true
			items, total, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{
					State:         ports.SeriesCacheStateMissing,
					Search:        "for",
					MonitoredOnly: &tru,
					Networks:      []string{"Apple TV+"},
				},
				ports.SeriesCacheSortTitleAsc,
				ports.Pagination{Limit: 50})
			require.NoError(t, err)
			assert.Equal(t, 1, total)
			require.Len(t, items, 1)
			assert.Equal(t, domain.SonarrSeriesID(1), items[0].SonarrSeriesID)
		})
	}
}

// TestSeriesCacheRepository_ListDistinctNetworks — Story 121a §A
func TestSeriesCacheRepository_ListDistinctNetworks(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			seed := []struct {
				id      domain.SonarrSeriesID
				network string
			}{
				{1, "HBO"},
				{2, "Apple TV+"},
				{3, "Apple TV+"},
				{4, ""}, // empty → dropped
				{5, "Netflix"},
			}
			for _, s := range seed {
				e := sampleEntry("main", s.id)
				require.NoError(t, repo.Upsert(ctx, e))
				seedNetworkJoinForCache(t, db, "main", int(s.id), s.network)
			}

			got, err := repo.ListDistinctNetworks(ctx, "main")
			require.NoError(t, err)
			assert.Equal(t, []string{"Apple TV+", "HBO", "Netflix"}, got,
				"result must be distinct, non-empty, alphabetically sorted")

			// Wrong instance → empty result, not error.
			got, err = repo.ListDistinctNetworks(ctx, "other")
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_AirDateDesc(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			t2 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

			entryOld := sampleEntry("main", 1)
			entryOld.Title = "Old Airer"
			entryOld.LastAiredAt = &t1
			require.NoError(t, repo.Upsert(ctx, entryOld))

			entryNew := sampleEntry("main", 2)
			entryNew.Title = "New Airer"
			entryNew.LastAiredAt = &t2
			require.NoError(t, repo.Upsert(ctx, entryNew))

			entryNil := sampleEntry("main", 3)
			entryNil.Title = "Upcoming"
			entryNil.LastAiredAt = nil
			require.NoError(t, repo.Upsert(ctx, entryNil))

			items, _, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll},
				ports.SeriesCacheSortAirDateDesc,
				ports.Pagination{Limit: 10})
			require.NoError(t, err)
			require.Len(t, items, 3)
			assert.Equal(t, domain.SonarrSeriesID(2), items[0].SonarrSeriesID, "newest aired first")
			assert.Equal(t, domain.SonarrSeriesID(1), items[1].SonarrSeriesID, "older aired second")
			assert.Equal(t, domain.SonarrSeriesID(3), items[2].SonarrSeriesID, "nil aired last (NULLS LAST)")
		})
	}
}

// The repository projects the raw canon poster path (s.poster_asset)
// onto every read path so the handler layer can derive the content-
// addressed media hash without waiting for the media_assets row to
// catch up. The hash derivation itself lives in interface/http/
// handlers/media_hash.go and is unit-tested at the handler level.
//
// seedPosterAssetOnCanon writes an en-US `series_media_texts` row carrying the
// raw poster path for the canon row resolved for (instance, sonarrID). S-E3a —
// the projection now resolves s_poster_asset from series_media_texts (was canon
// series.poster_asset). It deliberately does NOT write a media_assets row — the
// previous projection's dependency on status='stored' is the bug these tests
// guard against, and the behavior under test is "raw path present → PosterAsset
// projected regardless of media_assets state". Idempotent upsert.
func seedPosterAssetOnCanon(
	t *testing.T,
	db *gorm.DB,
	instance domain.InstanceName,
	sonarrID int,
	path string,
) {
	t.Helper()
	var sc database.SeriesCacheModel
	require.NoError(t, db.Where(
		"instance_name = ? AND sonarr_series_id = ?", instance, sonarrID,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID, "series_cache row must resolve a canon series_id")
	require.NoError(t, db.Exec(
		`INSERT INTO series_media_texts (series_id, language, poster_asset, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (series_id, language) DO UPDATE SET poster_asset = excluded.poster_asset`,
		int64(*sc.SeriesID), "en-US", path, time.Now().UTC(),
	).Error)
}

func TestSeriesCacheRepository_ProjectsRawPosterAsset(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 12)))
			seedPosterAssetOnCanon(t, db, "main", 12, "/abc.jpg")

			got, err := repo.Get(ctx, "main", 12)
			require.NoError(t, err)
			require.NotNil(t, got.PosterAsset, "canon poster path must project")
			assert.Equal(t, "/abc.jpg", *got.PosterAsset)
		})
	}
}

func TestSeriesCacheRepository_ProjectsPosterAsset_RegardlessOfMediaStatus(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 13)))
			seedPosterAssetOnCanon(t, db, "main", 13, "/def.jpg")
			// Write a media_assets row with status='pending' — the projection
			// must NOT filter on this anymore. The canon path drives the wire
			// `poster_hash`; the media row state drives the bytes path only.
			require.NoError(t, db.Create(&database.MediaAssetModel{
				Hash:      "feedface11",
				SourceURL: "https://image.tmdb.org/t/p/w342/def.jpg",
				Kind:      "poster_w342",
				Status:    "pending",
				CreatedAt: time.Now().UTC(),
			}).Error)

			got, err := repo.Get(ctx, "main", 13)
			require.NoError(t, err)
			require.NotNil(t, got.PosterAsset, "pending media row must not suppress the canon path projection")
			assert.Equal(t, "/def.jpg", *got.PosterAsset)
		})
	}
}

func TestSeriesCacheRepository_ProjectsPosterAsset_RegardlessOfFailedMediaRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 14)))
			seedPosterAssetOnCanon(t, db, "main", 14, "/ghi.jpg")
			require.NoError(t, db.Create(&database.MediaAssetModel{
				Hash:      "cafebabe22",
				SourceURL: "https://image.tmdb.org/t/p/w342/ghi.jpg",
				Kind:      "poster_w342",
				Status:    "failed",
				CreatedAt: time.Now().UTC(),
			}).Error)

			got, err := repo.Get(ctx, "main", 14)
			require.NoError(t, err)
			require.NotNil(t, got.PosterAsset, "failed media row must not suppress the canon path projection")
			assert.Equal(t, "/ghi.jpg", *got.PosterAsset)
		})
	}
}

func TestSeriesCacheRepository_NullCanonPoster_NilAsset(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 15)))
			// Don't seed poster_asset — canon row leaves it NULL.
			got, err := repo.Get(ctx, "main", 15)
			require.NoError(t, err)
			assert.Nil(t, got.PosterAsset, "NULL s.poster_asset → nil PosterAsset")
		})
	}
}

func TestSeriesCacheRepository_NoMediaRow_PosterAssetStillProjected(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 16)))
			// poster_asset set on canon, NO media_assets row at all — the
			// FE still gets a deterministic hash via handler-side derivation.
			seedPosterAssetOnCanon(t, db, "main", 16, "/jkl.jpg")

			got, err := repo.Get(ctx, "main", 16)
			require.NoError(t, err)
			require.NotNil(t, got.PosterAsset,
				"canon path projects even without any media_assets row — handler derives hash")
			assert.Equal(t, "/jkl.jpg", *got.PosterAsset)
		})
	}
}

// Cardinality: one series_cache row in, one out — no fanout, regardless
// of media_assets state. The previous LEFT JOIN risk (multiple matching
// rows) is gone now that we project raw s.poster_asset only.
func TestSeriesCacheRepository_CardinalityPreservedWithoutMediaJoin(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 21)))
			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 22)))
			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 23)))
			seedPosterAssetOnCanon(t, db, "main", 22, "/mno.jpg")
			seedPosterAssetOnCanon(t, db, "main", 21, "/pqr.jpg")

			active, err := repo.ListActiveByInstance(ctx, "main")
			require.NoError(t, err)
			assert.Len(t, active, 3, "exactly 3 cache rows → exactly 3 result rows")
			byID := make(map[domain.SonarrSeriesID]series.CacheEntry, len(active))
			for _, e := range active {
				byID[e.SonarrSeriesID] = e
			}
			require.NotNil(t, byID[21].PosterAsset)
			assert.Equal(t, "/pqr.jpg", *byID[21].PosterAsset)
			require.NotNil(t, byID[22].PosterAsset)
			assert.Equal(t, "/mno.jpg", *byID[22].PosterAsset)
			assert.Nil(t, byID[23].PosterAsset)
		})
	}
}

// Single SQL statement with no LEFT JOIN on media_assets — proves the
// projection no longer depends on the media-assets row reaching a
// 'stored' state before tiles can render.
func TestSeriesCacheRepository_SingleSQL_NoMediaAssetsJoin(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			for i := 1; i <= 4; i++ {
				require.NoError(t, repo.Upsert(ctx, sampleEntry("main", domain.SonarrSeriesID(i))))
			}
			seedPosterAssetOnCanon(t, db, "main", 1, "/a.jpg")
			seedPosterAssetOnCanon(t, db, "main", 2, "/b.jpg")

			dry := db.Session(&gorm.Session{DryRun: true})
			stmt := dry.Table("series_cache").
				Joins(seriesCacheJoin).
				Select(seriesCacheSelect).
				Where("series_cache.instance_name = ? AND series_cache.deleted_at IS NULL", "main").
				Find(&[]cacheRow{}).Statement
			sql := stmt.SQL.String()
			// S-E3a — s_title / s_poster_asset now resolve via correlated
			// subqueries over series_texts / series_media_texts, so the statement
			// legitimately carries more than one SELECT keyword. The invariant
			// under test is the absence of a media_assets LEFT JOIN, not the SELECT
			// count.
			assert.NotContains(t, strings.ToLower(sql), "media_assets",
				"projection must not LEFT JOIN media_assets anymore: %s", sql)
			assert.Contains(t, sql, "s_poster_asset",
				"PosterAsset projected: %s", sql)

			// Verify the result carries the canon paths.
			active, err := repo.ListActiveByInstance(ctx, "main")
			require.NoError(t, err)
			withPath := 0
			for _, e := range active {
				if e.PosterAsset != nil {
					withPath++
				}
			}
			assert.Equal(t, 2, withPath, "two seeded canon paths → two PosterAsset values")

			items, _, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll},
				ports.SeriesCacheSortUpdatedDesc,
				ports.Pagination{Limit: 50})
			require.NoError(t, err)
			withPath = 0
			for _, e := range items {
				if e.PosterAsset != nil {
					withPath++
				}
			}
			assert.Equal(t, 2, withPath, "ListByFilter projects PosterAsset via the same SELECT")
		})
	}
}

// Story 374: EpisodeFileCount + SizeOnDiskBytes round-trip through Upsert/Get.
// These power the LibraryStrip hero tile straight off the cache row.
func TestSeriesCacheRepository_LibraryStats_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			e := sampleEntry("main", 901)
			e.EpisodeFileCount = 128
			e.SizeOnDiskBytes = 142_300_000_000

			require.NoError(t, repo.Upsert(ctx, e))
			got, err := repo.Get(ctx, "main", 901)
			require.NoError(t, err)
			require.Equal(t, 128, got.EpisodeFileCount)
			require.Equal(t, int64(142_300_000_000), got.SizeOnDiskBytes)

			e.EpisodeFileCount = 129
			e.SizeOnDiskBytes = 143_000_000_000
			require.NoError(t, repo.Upsert(ctx, e))
			got, err = repo.Get(ctx, "main", 901)
			require.NoError(t, err)
			require.Equal(t, 129, got.EpisodeFileCount)
			require.Equal(t, int64(143_000_000_000), got.SizeOnDiskBytes)
		})
	}
}

// Story 374: defaults of 0/0 for entries that don't set the fields.
func TestSeriesCacheRepository_LibraryStats_DefaultZero(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 902)))
			got, err := repo.Get(ctx, "main", 902)
			require.NoError(t, err)
			require.Equal(t, 0, got.EpisodeFileCount)
			require.Equal(t, int64(0), got.SizeOnDiskBytes)
		})
	}
}

// Story 376: AiredEpisodeCount round-trips through Upsert/Get and powers
// the LibraryStrip denominator (so unaired future episodes don't depress
// the headline percentage).
func TestSeriesCacheRepository_AiredEpisodeCount_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			e := sampleEntry("main", 903)
			e.AiredEpisodeCount = 85
			require.NoError(t, repo.Upsert(ctx, e))
			got, err := repo.Get(ctx, "main", 903)
			require.NoError(t, err)
			require.Equal(t, 85, got.AiredEpisodeCount)

			e.AiredEpisodeCount = 86
			require.NoError(t, repo.Upsert(ctx, e))
			got, err = repo.Get(ctx, "main", 903)
			require.NoError(t, err)
			require.Equal(t, 86, got.AiredEpisodeCount)
		})
	}
}

// Story 376: default 0 for entries that don't set AiredEpisodeCount.
func TestSeriesCacheRepository_AiredEpisodeCount_DefaultZero(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()
			require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 904)))
			got, err := repo.Get(ctx, "main", 904)
			require.NoError(t, err)
			require.Equal(t, 0, got.AiredEpisodeCount)
		})
	}
}

// Story 491 / N-1a: GetInstancesBySeriesID returns the sorted, distinct
// instance names that currently carry a canonical series.id. Verifies:
//   - 2 instances → sorted ASC list
//   - 1 instance → single-element list
//   - 0 instances → empty slice (non-nil)
//   - soft-deleted row is excluded
//   - invalid id (≤0) → error
func TestSeriesCacheRepository_GetInstancesBySeriesID(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			// Seed: two entries sharing the same TMDB id collapse to one
			// canon series row (alpha + beta). gamma uses default
			// sampleEntry external ids so it gets its own canon row.
			sharedTVDB := domain.TVDBID(99001)
			sharedTMDB := domain.TMDBID(99002)
			sharedIMDB := domain.IMDBID("tt9000001")
			alphaEntry := sampleEntry("alpha", 7)
			alphaEntry.TVDBID = &sharedTVDB
			alphaEntry.TMDBID = &sharedTMDB
			alphaEntry.IMDBID = &sharedIMDB
			betaEntry := sampleEntry("beta", 9)
			betaEntry.TVDBID = &sharedTVDB
			betaEntry.TMDBID = &sharedTMDB
			betaEntry.IMDBID = &sharedIMDB
			gammaEntry := sampleEntry("gamma", 11)

			require.NoError(t, repo.Upsert(ctx, alphaEntry))
			require.NoError(t, repo.Upsert(ctx, betaEntry))
			require.NoError(t, repo.Upsert(ctx, gammaEntry))

			// Resolve the shared canon series_id.
			got, err := repo.Get(ctx, "alpha", 7)
			require.NoError(t, err)
			require.NotNil(t, got.SeriesID, "alpha must have canon series_id")
			sharedID := *got.SeriesID

			gammaGot, err := repo.Get(ctx, "gamma", 11)
			require.NoError(t, err)
			require.NotNil(t, gammaGot.SeriesID, "gamma must have canon series_id")
			gammaID := *gammaGot.SeriesID

			// Verify alpha+beta collapsed onto the same canon row.
			betaGot, err := repo.Get(ctx, "beta", 9)
			require.NoError(t, err)
			require.NotNil(t, betaGot.SeriesID)
			require.Equal(t, sharedID, *betaGot.SeriesID, "alpha+beta must share canon row via shared TMDB id")

			// Case 1: 2 instances → sorted ["alpha", "beta"].
			instances, err := repo.GetInstancesBySeriesID(ctx, sharedID)
			require.NoError(t, err)
			assert.Equal(t, []domain.InstanceName{"alpha", "beta"}, instances)

			// Case 2: 1 instance → ["gamma"].
			instances, err = repo.GetInstancesBySeriesID(ctx, gammaID)
			require.NoError(t, err)
			assert.Equal(t, []domain.InstanceName{"gamma"}, instances)

			// Case 3: 0 instances (series_id with no cache rows).
			instances, err = repo.GetInstancesBySeriesID(ctx, 999999)
			require.NoError(t, err)
			assert.Empty(t, instances)
			assert.NotNil(t, instances, "must be empty slice, not nil")

			// Case 4: soft-deleted row is excluded.
			require.NoError(t, repo.SoftDelete(ctx, "alpha", 7))
			instances, err = repo.GetInstancesBySeriesID(ctx, sharedID)
			require.NoError(t, err)
			assert.Equal(t, []domain.InstanceName{"beta"}, instances, "soft-deleted alpha must be filtered out")

			// Case 5: invalid id → error.
			_, err = repo.GetInstancesBySeriesID(ctx, 0)
			assert.Error(t, err)

			_, err = repo.GetInstancesBySeriesID(ctx, -1)
			assert.Error(t, err)
		})
	}
}

// Story 527: GetInstancesBySeriesIDs is the batch sibling — returns
// the sorted, distinct active instance names per series id in ONE
// query. Verifies:
//   - empty input → empty (non-nil) map, no SQL run
//   - mixed input (valid + invalid ids) → only valid keys returned
//   - 2-id batch → both keys populated with sorted instance slices
//   - soft-deleted row excluded
//   - id with no cache rows → key absent from map (not present-but-empty)
func TestSeriesCacheRepository_GetInstancesBySeriesIDs(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			// Seed two canon-shared rows (alpha + beta) + one isolated (gamma).
			sharedTVDB := domain.TVDBID(880001)
			sharedTMDB := domain.TMDBID(880002)
			sharedIMDB := domain.IMDBID("tt8800001")
			alphaEntry := sampleEntry("alpha", 31)
			alphaEntry.TVDBID = &sharedTVDB
			alphaEntry.TMDBID = &sharedTMDB
			alphaEntry.IMDBID = &sharedIMDB
			betaEntry := sampleEntry("beta", 41)
			betaEntry.TVDBID = &sharedTVDB
			betaEntry.TMDBID = &sharedTMDB
			betaEntry.IMDBID = &sharedIMDB
			gammaEntry := sampleEntry("gamma", 51)

			require.NoError(t, repo.Upsert(ctx, alphaEntry))
			require.NoError(t, repo.Upsert(ctx, betaEntry))
			require.NoError(t, repo.Upsert(ctx, gammaEntry))

			alphaGot, err := repo.Get(ctx, "alpha", 31)
			require.NoError(t, err)
			require.NotNil(t, alphaGot.SeriesID)
			sharedID := *alphaGot.SeriesID

			gammaGot, err := repo.Get(ctx, "gamma", 51)
			require.NoError(t, err)
			require.NotNil(t, gammaGot.SeriesID)
			gammaID := *gammaGot.SeriesID

			// Case 1: empty input → empty (non-nil) map, no SQL.
			got, err := repo.GetInstancesBySeriesIDs(ctx, nil)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Empty(t, got)

			// Case 2: batch with valid + bogus + zero ids.
			got, err = repo.GetInstancesBySeriesIDs(ctx, []domain.SeriesID{
				sharedID, gammaID, 999999, 0, -1,
			})
			require.NoError(t, err)
			assert.Len(t, got, 2, "only the 2 valid ids with rows must populate")
			assert.Equal(t, []domain.InstanceName{"alpha", "beta"}, got[sharedID])
			assert.Equal(t, []domain.InstanceName{"gamma"}, got[gammaID])
			_, has := got[999999]
			assert.False(t, has, "missing id MUST be absent from map")

			// Case 3: soft-delete excludes the row.
			require.NoError(t, repo.SoftDelete(ctx, "alpha", 31))
			got, err = repo.GetInstancesBySeriesIDs(ctx, []domain.SeriesID{sharedID})
			require.NoError(t, err)
			assert.Equal(t, []domain.InstanceName{"beta"}, got[sharedID])

			// Case 4: only-invalid input → empty map, no SQL.
			got, err = repo.GetInstancesBySeriesIDs(ctx, []domain.SeriesID{0, -1})
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Empty(t, got)
		})
	}
}

// TestSeriesCacheRepository_ListBySeriesIDs_DualBackend is the D-0
// dual-backend regression net for Story 556 (E-1 Z7). The batch
// sibling of ListBySeriesID — bucketed map return, soft-deleted
// exclusion, presence-map semantics for missing ids.
func TestSeriesCacheRepository_ListBySeriesIDs_DualBackend(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			// Seed two canon-shared rows (alpha + beta on shared external ids)
			// plus an isolated gamma row.
			sharedTVDB := domain.TVDBID(990001)
			sharedTMDB := domain.TMDBID(990002)
			sharedIMDB := domain.IMDBID("tt9900001")
			alphaEntry := sampleEntry("alpha", 71)
			alphaEntry.TVDBID = &sharedTVDB
			alphaEntry.TMDBID = &sharedTMDB
			alphaEntry.IMDBID = &sharedIMDB
			betaEntry := sampleEntry("beta", 81)
			betaEntry.TVDBID = &sharedTVDB
			betaEntry.TMDBID = &sharedTMDB
			betaEntry.IMDBID = &sharedIMDB
			gammaEntry := sampleEntry("gamma", 91)

			require.NoError(t, repo.Upsert(ctx, alphaEntry))
			require.NoError(t, repo.Upsert(ctx, betaEntry))
			require.NoError(t, repo.Upsert(ctx, gammaEntry))

			alphaGot, err := repo.Get(ctx, "alpha", 71)
			require.NoError(t, err)
			require.NotNil(t, alphaGot.SeriesID)
			sharedID := *alphaGot.SeriesID

			gammaGot, err := repo.Get(ctx, "gamma", 91)
			require.NoError(t, err)
			require.NotNil(t, gammaGot.SeriesID)
			gammaID := *gammaGot.SeriesID

			t.Run("empty_input_returns_empty_non_nil_map", func(t *testing.T) {
				got, err := repo.ListBySeriesIDs(ctx, nil)
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Empty(t, got)

				got, err = repo.ListBySeriesIDs(ctx, []domain.SeriesID{})
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Empty(t, got)
			})

			t.Run("single_hit_returns_entry", func(t *testing.T) {
				got, err := repo.ListBySeriesIDs(ctx, []domain.SeriesID{gammaID})
				require.NoError(t, err)
				require.Len(t, got, 1)
				require.Len(t, got[gammaID], 1)
				assert.Equal(t, domain.InstanceName("gamma"), got[gammaID][0].InstanceName)
			})

			t.Run("multiple_ids_each_bucketed", func(t *testing.T) {
				got, err := repo.ListBySeriesIDs(ctx, []domain.SeriesID{sharedID, gammaID})
				require.NoError(t, err)
				require.Len(t, got, 2)
				assert.Len(t, got[sharedID], 2, "alpha + beta share canon row")
				assert.Len(t, got[gammaID], 1)
			})

			t.Run("missing_id_absent_from_map", func(t *testing.T) {
				got, err := repo.ListBySeriesIDs(ctx, []domain.SeriesID{sharedID, 999999})
				require.NoError(t, err)
				_, has := got[999999]
				assert.False(t, has, "missing id MUST be absent from map")
				assert.Len(t, got[sharedID], 2)
			})

			t.Run("invalid_ids_filtered", func(t *testing.T) {
				got, err := repo.ListBySeriesIDs(ctx, []domain.SeriesID{0, -1, sharedID})
				require.NoError(t, err)
				assert.Len(t, got[sharedID], 2)
				_, hasZero := got[0]
				assert.False(t, hasZero)
			})

			t.Run("only_invalid_short_circuits", func(t *testing.T) {
				got, err := repo.ListBySeriesIDs(ctx, []domain.SeriesID{0, -1})
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Empty(t, got)
			})

			t.Run("dedup_input_no_duplicate_entries", func(t *testing.T) {
				got, err := repo.ListBySeriesIDs(ctx, []domain.SeriesID{sharedID, sharedID, gammaID})
				require.NoError(t, err)
				assert.Len(t, got[sharedID], 2, "duplicate input id MUST NOT produce duplicate rows")
				assert.Len(t, got[gammaID], 1)
			})

			t.Run("soft_deleted_excluded", func(t *testing.T) {
				require.NoError(t, repo.SoftDelete(ctx, "alpha", 71))
				got, err := repo.ListBySeriesIDs(ctx, []domain.SeriesID{sharedID})
				require.NoError(t, err)
				require.Len(t, got[sharedID], 1, "alpha soft-deleted; only beta remains")
				assert.Equal(t, domain.InstanceName("beta"), got[sharedID][0].InstanceName)
			})
		})
	}
}
