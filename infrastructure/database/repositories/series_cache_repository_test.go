package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
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
