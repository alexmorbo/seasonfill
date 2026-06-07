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

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

func sampleEntry(instance string, id int) series.CacheEntry {
	return series.CacheEntry{
		InstanceName:   instance,
		SonarrSeriesID: id,
		Title:          "Test Series",
		TitleSlug:      "test-series",
		Year:           ptrInt(2024),
		TVDBID:         ptrInt(12345),
		IMDBID:         ptrString("tt9999999"),
		TMDBID:         ptrInt(54321),
		Status:         ptrString("continuing"),
		Network:        ptrString("HBO"),
		Genres:         []string{"Drama", "Comedy"},
		RuntimeMinutes: ptrInt(60),
		Monitored:      true,
		Overview:       ptrString("Overview text."),
		PosterPath:     ptrString("/MediaCover/12/poster.jpg?lastWrite=999"),
		FanartPath:     ptrString("/MediaCover/12/fanart.jpg"),
		BannerPath:     ptrString("/MediaCover/12/banner.jpg"),
	}
}

func TestSeriesCacheRepository_Upsert_Insert_Get(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, sampleEntry("main", 12)))
	got, err := repo.Get(ctx, "main", 12)
	require.NoError(t, err)
	assert.Equal(t, "main", got.InstanceName)
	assert.Equal(t, 12, got.SonarrSeriesID)
	assert.Equal(t, "Test Series", got.Title)
	assert.Equal(t, "test-series", got.TitleSlug)
	require.NotNil(t, got.Year)
	assert.Equal(t, 2024, *got.Year)
	assert.Equal(t, []string{"Drama", "Comedy"}, got.Genres)
	assert.True(t, got.Monitored)
	require.NotNil(t, got.PosterPath)
	assert.Equal(t, "/MediaCover/12/poster.jpg?lastWrite=999", *got.PosterPath)
	assert.False(t, got.UpdatedAt.IsZero())
	assert.Nil(t, got.DeletedAt)
	assert.True(t, got.IsActive())
}

func TestSeriesCacheRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db)
	_, err := repo.Get(context.Background(), "main", 999)
	require.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestSeriesCacheRepository_Upsert_Replaces_AndResurrects(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db)
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
	assert.Equal(t, []string{"Thriller"}, got.Genres)

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
	repo := NewSeriesCacheRepository(db)
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
	repo := NewSeriesCacheRepository(db)
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
		assert.Equal(t, "main", e.InstanceName)
		assert.NotEqual(t, 2, e.SonarrSeriesID)
	}

	// Empty result is non-nil.
	got, err := repo.ListActiveByInstance(ctx, "nonexistent")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got)
}

func TestSeriesCacheRepository_GenresRoundTrip(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db)
	ctx := context.Background()
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, nil},
		{"empty maps to nil", []string{}, nil},
		{"single", []string{"Drama"}, []string{"Drama"}},
		{"multi", []string{"Drama", "Sci-Fi"}, []string{"Drama", "Sci-Fi"}},
		{"unicode", []string{"Документальный"}, []string{"Документальный"}},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry := sampleEntry("main", 100+i)
			entry.Genres = tc.in
			require.NoError(t, repo.Upsert(ctx, entry))
			got, err := repo.Get(ctx, "main", 100+i)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Genres)
		})
	}
}

func TestSeriesCacheRepository_NilPointerFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.Upsert(ctx, series.CacheEntry{
		InstanceName: "main", SonarrSeriesID: 7,
		Title: "Minimal", TitleSlug: "minimal",
	}))
	got, err := repo.Get(ctx, "main", 7)
	require.NoError(t, err)
	assert.Equal(t, "Minimal", got.Title)
	for _, p := range []interface{}{
		got.Year, got.TVDBID, got.IMDBID, got.TMDBID,
		got.Status, got.Network, got.Genres,
		got.RuntimeMinutes, got.Overview,
		got.PosterPath, got.FanartPath, got.BannerPath,
	} {
		assert.Nil(t, p)
	}
}

func TestSeriesCacheRepository_Upsert_RejectsZeroPK(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db)
	ctx := context.Background()
	require.Error(t, repo.Upsert(ctx, sampleEntry("", 1)))
	require.Error(t, repo.Upsert(ctx, sampleEntry("main", 0)))
}

func TestSeriesCacheRepository_Upsert_StampsUpdatedAt(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db)
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
	repo := NewSeriesCacheRepository(db)
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
	repo := NewSeriesCacheRepository(db)
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
	repo := NewSeriesCacheRepository(db)
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
	repo := NewSeriesCacheRepository(db)
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

func TestSeriesCacheRepository_ListByFilter_TitleAsc(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db)
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
	repo := NewSeriesCacheRepository(db)
	_, _, _, _, err := repo.ListByFilter(context.Background(), "main",
		ports.SeriesCacheFilter{State: ports.SeriesCacheState("bogus")},
		ports.SeriesCacheSortUpdatedDesc,
		ports.Pagination{Limit: 10})
	require.Error(t, err)
}

func TestSeriesCacheRepository_ListByFilter_SkipsSoftDeleted(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesCacheRepository(db)
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
	repo := NewSeriesCacheRepository(db)
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
