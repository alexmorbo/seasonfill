package repositories

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

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func sampleEntry(instance domain.InstanceName, id int) series.CacheEntry {
	// Post B-1b cutover: every cache row resolves to a distinct canon
	// row via natural-key dedup (TMDB > TVDB > IMDB). To preserve
	// per-(instance, sonarr_id) test isolation we derive unique
	// external ids from the sonarr id so two test rows never collapse
	// into one canon row by accident. Tests that need shared canon
	// (cutover dedup scenarios) override TMDBID/TVDBID explicitly.
	tvdb := 12345 + id
	tmdb := 54321 + id
	return series.CacheEntry{
		InstanceName:   instance,
		SonarrSeriesID: id,
		Title:          "Test Series",
		TitleSlug:      "test-series",
		Year:           ptrInt(2024),
		TVDBID:         &tvdb,
		IMDBID:         ptrString(fmt.Sprintf("tt%07d", 9000000+id)),
		TMDBID:         &tmdb,
		Status:         ptrString("continuing"),
		Genres:         []string{"Drama", "Comedy"},
		RuntimeMinutes: ptrInt(60),
		Monitored:      true,
		Overview:       ptrString("Overview text."),
		FanartPath:     ptrString("/MediaCover/12/fanart.jpg"),
		BannerPath:     ptrString("/MediaCover/12/banner.jpg"),
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

func TestSeriesCacheRepository_Upsert_Insert_Get(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 12)))
	got, err := repo.Get(ctx, "main", 12)
	require.NoError(t, err)
	assert.Equal(t, domain.InstanceName("main"), got.InstanceName)
	assert.Equal(t, 12, got.SonarrSeriesID)
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
}

func TestSeriesCacheRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	_, err := repo.Get(context.Background(), "main", 999)
	require.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestSeriesCacheRepository_Upsert_Replaces_AndResurrects(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 12)))

	// Replace.
	second := sampleEntry("main", 12)
	second.Title = "Renamed"
	second.Monitored = false
	second.Genres = []string{"Thriller"}
	require.NoError(t, repo.Upsert(ctx, second))
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
}

func TestSeriesCacheRepository_SoftDelete_Idempotent_AndMissing(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
}

func TestSeriesCacheRepository_ListActiveByInstance(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
}

// Post B-1b cutover: Genres no longer round-trip via series_cache —
// canon stores them in the series_genres join (deferred to E-1).
// Whatever the caller writes is dropped at the repo edge and the read
// path always returns nil. This regression-locks that contract.
func TestSeriesCacheRepository_GenresAlwaysNilPostCutover(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()
	entry := sampleEntry("main", 100)
	entry.Genres = []string{"Drama", "Sci-Fi"}
	require.NoError(t, repo.Upsert(ctx, entry))
	got, err := repo.Get(ctx, "main", 100)
	require.NoError(t, err)
	assert.Nil(t, got.Genres,
		"post B-1b cutover: genres are not stored on series_cache; canon's series_genres join is the source")
}

func TestSeriesCacheRepository_NilPointerFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()
	require.NoError(t, repo.Upsert(ctx, series.CacheEntry{
		InstanceName: "main", SonarrSeriesID: 7,
		Title: "Minimal", TitleSlug: "minimal",
	}))
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
}

func TestSeriesCacheRepository_Upsert_RejectsZeroPK(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()
	require.Error(t, repo.Upsert(ctx, sampleEntry("", 1)))
	require.Error(t, repo.Upsert(ctx, sampleEntry("main", 0)))
}

func TestSeriesCacheRepository_Upsert_StampsUpdatedAt(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
}

func TestSeriesCacheRepository_ListByFilter_StateAll_UpdatedDesc(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	now := time.Now().UTC()
	for i := 1; i <= 5; i++ {
		entry := sampleEntry("main", i)
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
	assert.Equal(t, 5, items[0].SonarrSeriesID)
	assert.Equal(t, 1, items[4].SonarrSeriesID)
}

func TestSeriesCacheRepository_ListByFilter_StateMissing(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		entry := sampleEntry("main", i)
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
}

func TestSeriesCacheRepository_ListByFilter_StateImported_SubqueryWindow(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	grabs := NewGrabRepository(db)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		require.NoError(t, repo.Upsert(ctx, sampleEntry("main", i)))
	}

	now := time.Now().UTC()
	require.NoError(t, grabs.Create(ctx, grab.Record{
		ID: uuid.New(), InstanceName: "main", SeriesID: 1, SeasonNumber: 1,
		ScanRunID: uuid.New(), Status: grab.StatusImported,
		CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-48 * time.Hour),
	}))
	require.NoError(t, grabs.Create(ctx, grab.Record{
		ID: uuid.New(), InstanceName: "main", SeriesID: 2, SeasonNumber: 1,
		ScanRunID: uuid.New(), Status: grab.StatusImported,
		CreatedAt: now.Add(-10 * 24 * time.Hour), UpdatedAt: now.Add(-10 * 24 * time.Hour),
	}))
	require.NoError(t, grabs.Create(ctx, grab.Record{
		ID: uuid.New(), InstanceName: "main", SeriesID: 3, SeasonNumber: 1,
		ScanRunID: uuid.New(), Status: grab.StatusGrabbed,
		CreatedAt: now.Add(-1 * time.Hour), UpdatedAt: now.Add(-1 * time.Hour),
	}))

	items, total, _, _, err := repo.ListByFilter(ctx, "main",
		ports.SeriesCacheFilter{State: ports.SeriesCacheStateImported},
		ports.SeriesCacheSortUpdatedDesc,
		ports.Pagination{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, 1, items[0].SonarrSeriesID)
}

func TestSeriesCacheRepository_ListByFilter_KeysetPagination(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	now := time.Now().UTC()
	for i := 1; i <= 30; i++ {
		entry := sampleEntry("main", i)
		entry.Title = fmt.Sprintf("Series %02d", i)
		require.NoError(t, repo.Upsert(ctx, entry))
		require.NoError(t, db.Model(&database.SeriesCacheModel{}).
			Where("instance_name = ? AND sonarr_series_id = ?", "main", i).
			Update("updated_at", now.Add(time.Duration(i)*time.Minute)).Error)
	}

	seen := map[int]bool{}
	page := ports.Pagination{Limit: 12}
	for iter := 0; iter < 4; iter++ {
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
}

func TestSeriesCacheRepository_ListByFilter_Search_MatchesTitleCaseInsensitive(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	cases := []struct {
		id    int
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
		tc := tc
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
				gotIDs = append(gotIDs, it.SonarrSeriesID)
			}
			assert.ElementsMatch(t, tc.wantIDs, gotIDs)
		})
	}
}

func TestSeriesCacheRepository_ListByFilter_Search_MatchesSlug(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
	assert.Equal(t, 1, items[0].SonarrSeriesID)
}

func TestSeriesCacheRepository_ListByFilter_Search_EscapesWildcards(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	// One row with a literal `%` and `_` in the title; one without.
	a := sampleEntry("main", 1)
	a.Title = "100% Wolf"
	a.TitleSlug = "100-percent-wolf"
	require.NoError(t, repo.Upsert(ctx, a))

	b := sampleEntry("main", 2)
	b.Title = "Severance"
	b.TitleSlug = "severance"
	require.NoError(t, repo.Upsert(ctx, b))

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
	assert.Equal(t, 1, items[0].SonarrSeriesID)

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
	assert.Equal(t, 1, items[0].SonarrSeriesID)
}

func TestSeriesCacheRepository_ListByFilter_TitleAsc(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	titles := []string{"Zulu", "Alpha", "Mike", "bravo", "charlie"}
	for i, title := range titles {
		entry := sampleEntry("main", i+1)
		entry.Title = title
		require.NoError(t, repo.Upsert(ctx, entry))
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
}

func TestSeriesCacheRepository_ListByFilter_InvalidState(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	_, _, _, _, err := repo.ListByFilter(context.Background(), "main",
		ports.SeriesCacheFilter{State: ports.SeriesCacheState("bogus")},
		ports.SeriesCacheSortUpdatedDesc,
		ports.Pagination{Limit: 10})
	require.Error(t, err)
}

func TestSeriesCacheRepository_ListByFilter_SkipsSoftDeleted(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
	assert.Equal(t, 1, items[0].SonarrSeriesID)
}

func TestSeriesCacheRepository_FetchLastGrabInfo(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	grabs := NewGrabRepository(db)
	ctx := context.Background()

	now := time.Now().UTC()
	require.NoError(t, grabs.Create(ctx, grab.Record{
		ID: uuid.New(), InstanceName: "main", SeriesID: 1, SeasonNumber: 3,
		ScanRunID: uuid.New(), Status: grab.StatusImported,
		CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-3 * time.Hour),
	}))
	require.NoError(t, grabs.Create(ctx, grab.Record{
		ID: uuid.New(), InstanceName: "main", SeriesID: 1, SeasonNumber: 5,
		ScanRunID: uuid.New(), Status: grab.StatusImported,
		CreatedAt: now.Add(-1 * time.Hour), UpdatedAt: now.Add(-1 * time.Hour),
	}))

	out, err := repo.FetchLastGrabInfo(ctx, "main", []int{1, 2})
	require.NoError(t, err)
	require.Contains(t, out, 1)
	assert.Equal(t, "S05", out[1].LastImportedEpisode)
	assert.WithinDuration(t, now.Add(-1*time.Hour), out[1].LastGrabAt, time.Second)
	assert.NotContains(t, out, 2)
	_ = errors.New // keep errors import used in existing file
}

func TestSeriesCacheRepository_Upsert_PersistsLastAiredAt(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	aired := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	entry := sampleEntry("main", 42)
	entry.LastAiredAt = &aired
	require.NoError(t, repo.Upsert(ctx, entry))

	got, err := repo.Get(ctx, "main", 42)
	require.NoError(t, err)
	require.NotNil(t, got.LastAiredAt)
	assert.True(t, got.LastAiredAt.Equal(aired))
}

// TestSeriesCacheRepository_ListByFilter_MonitoredOnly — Story 121a §A
func TestSeriesCacheRepository_ListByFilter_MonitoredOnly(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			items, total, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll, MonitoredOnly: tc.ptr},
				ports.SeriesCacheSortTitleAsc,
				ports.Pagination{Limit: 50})
			require.NoError(t, err)
			assert.Equal(t, len(tc.wantIDs), total)
			gotIDs := make([]int, 0, len(items))
			for _, it := range items {
				gotIDs = append(gotIDs, it.SonarrSeriesID)
			}
			assert.ElementsMatch(t, tc.wantIDs, gotIDs)
		})
	}
}

// TestSeriesCacheRepository_ListByFilter_Networks — Story 121a §A,
// updated for E-1 (Story 210): network membership lives in
// series_networks; tests seed the join via seedNetworkJoinForCache.
func TestSeriesCacheRepository_ListByFilter_Networks(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	seed := []struct {
		id      int
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
		seedNetworkJoinForCache(t, db, "main", s.id, s.network)
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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			items, total, _, _, err := repo.ListByFilter(ctx, "main",
				ports.SeriesCacheFilter{State: ports.SeriesCacheStateAll, Networks: tc.nets},
				ports.SeriesCacheSortTitleAsc,
				ports.Pagination{Limit: 50})
			require.NoError(t, err)
			assert.Equal(t, len(tc.wantIDs), total)
			gotIDs := make([]int, 0, len(items))
			for _, it := range items {
				gotIDs = append(gotIDs, it.SonarrSeriesID)
			}
			assert.ElementsMatch(t, tc.wantIDs, gotIDs)
		})
	}
}

// TestSeriesCacheRepository_ListByFilter_CombinedFilters — Story 121a §A
// Tests that state + search + monitored + networks intersect correctly.
func TestSeriesCacheRepository_ListByFilter_CombinedFilters(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	seed := []struct {
		id        int
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
		seedNetworkJoinForCache(t, db, "main", s.id, s.network)
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
	assert.Equal(t, 1, items[0].SonarrSeriesID)
}

// TestSeriesCacheRepository_ListDistinctNetworks — Story 121a §A
func TestSeriesCacheRepository_ListDistinctNetworks(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	seed := []struct {
		id      int
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
		seedNetworkJoinForCache(t, db, "main", s.id, s.network)
	}

	got, err := repo.ListDistinctNetworks(ctx, "main")
	require.NoError(t, err)
	assert.Equal(t, []string{"Apple TV+", "HBO", "Netflix"}, got,
		"result must be distinct, non-empty, alphabetically sorted")

	// Wrong instance → empty result, not error.
	got, err = repo.ListDistinctNetworks(ctx, "other")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSeriesCacheRepository_ListByFilter_AirDateDesc(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
	assert.Equal(t, 2, items[0].SonarrSeriesID, "newest aired first")
	assert.Equal(t, 1, items[1].SonarrSeriesID, "older aired second")
	assert.Equal(t, 3, items[2].SonarrSeriesID, "nil aired last (NULLS LAST)")
}

// The repository projects the raw canon poster path (s.poster_asset)
// onto every read path so the handler layer can derive the content-
// addressed media hash without waiting for the media_assets row to
// catch up. The hash derivation itself lives in interface/http/
// handlers/media_hash.go and is unit-tested at the handler level.
//
// seedPosterAssetOnCanon stamps `s.poster_asset = path` on the canon
// row resolved for (instance, sonarrID). It deliberately does NOT
// write a media_assets row — the previous projection's dependency on
// status='stored' is the bug these tests guard against, and the new
// behavior is "path on canon → PosterAsset projected regardless of
// media_assets state".
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
	require.NoError(t, db.Model(&database.SeriesModel{}).
		Where("id = ?", *sc.SeriesID).
		Update("poster_asset", path).Error)
}

func TestSeriesCacheRepository_ProjectsRawPosterAsset(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 12)))
	seedPosterAssetOnCanon(t, db, "main", 12, "/abc.jpg")

	got, err := repo.Get(ctx, "main", 12)
	require.NoError(t, err)
	require.NotNil(t, got.PosterAsset, "canon poster path must project")
	assert.Equal(t, "/abc.jpg", *got.PosterAsset)
}

func TestSeriesCacheRepository_ProjectsPosterAsset_RegardlessOfMediaStatus(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
}

func TestSeriesCacheRepository_ProjectsPosterAsset_RegardlessOfFailedMediaRow(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
}

func TestSeriesCacheRepository_NullCanonPoster_NilAsset(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 15)))
	// Don't seed poster_asset — canon row leaves it NULL.
	got, err := repo.Get(ctx, "main", 15)
	require.NoError(t, err)
	assert.Nil(t, got.PosterAsset, "NULL s.poster_asset → nil PosterAsset")
}

func TestSeriesCacheRepository_NoMediaRow_PosterAssetStillProjected(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
}

// Cardinality: one series_cache row in, one out — no fanout, regardless
// of media_assets state. The previous LEFT JOIN risk (multiple matching
// rows) is gone now that we project raw s.poster_asset only.
func TestSeriesCacheRepository_CardinalityPreservedWithoutMediaJoin(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
	byID := make(map[int]series.CacheEntry, len(active))
	for _, e := range active {
		byID[e.SonarrSeriesID] = e
	}
	require.NotNil(t, byID[21].PosterAsset)
	assert.Equal(t, "/pqr.jpg", *byID[21].PosterAsset)
	require.NotNil(t, byID[22].PosterAsset)
	assert.Equal(t, "/mno.jpg", *byID[22].PosterAsset)
	assert.Nil(t, byID[23].PosterAsset)
}

// Single SQL statement with no LEFT JOIN on media_assets — proves the
// projection no longer depends on the media-assets row reaching a
// 'stored' state before tiles can render.
func TestSeriesCacheRepository_SingleSQL_NoMediaAssetsJoin(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	for i := 1; i <= 4; i++ {
		require.NoError(t, repo.Upsert(ctx, sampleEntry("main", i)))
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
	assert.Equal(t, 1, strings.Count(strings.ToLower(sql), "select "),
		"exactly one SELECT; got: %s", sql)
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
}

// Story 374: EpisodeFileCount + SizeOnDiskBytes round-trip through Upsert/Get.
// These power the LibraryStrip hero tile straight off the cache row.
func TestSeriesCacheRepository_LibraryStats_RoundTrip(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
}

// Story 374: defaults of 0/0 for entries that don't set the fields.
func TestSeriesCacheRepository_LibraryStats_DefaultZero(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 902)))
	got, err := repo.Get(ctx, "main", 902)
	require.NoError(t, err)
	require.Equal(t, 0, got.EpisodeFileCount)
	require.Equal(t, int64(0), got.SizeOnDiskBytes)
}

// Story 376: AiredEpisodeCount round-trips through Upsert/Get and powers
// the LibraryStrip denominator (so unaired future episodes don't depress
// the headline percentage).
func TestSeriesCacheRepository_AiredEpisodeCount_RoundTrip(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
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
}

// Story 376: default 0 for entries that don't set AiredEpisodeCount.
func TestSeriesCacheRepository_AiredEpisodeCount_DefaultZero(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
	ctx := context.Background()
	require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 904)))
	got, err := repo.Get(ctx, "main", 904)
	require.NoError(t, err)
	require.Equal(t, 0, got.AiredEpisodeCount)
}
