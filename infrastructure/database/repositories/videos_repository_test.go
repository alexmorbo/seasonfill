package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

func sampleVideo(seriesID int64, name, videoType string, official bool) database.VideoModel {
	pub := time.Now().UTC().Add(-30 * 24 * time.Hour)
	return database.VideoModel{
		SeriesID:    seriesID,
		Name:        name,
		Site:        ptrString("YouTube"),
		Key:         ptrString("abc123"),
		Type:        ptrString(videoType),
		Official:    official,
		Language:    ptrString("en"),
		PublishedAt: &pub,
	}
}

func TestVideosRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Andor"))
	require.NoError(t, err)
	repo := NewVideosRepository(db)

	v := sampleVideo(seriesID, "Official Trailer", "Trailer", true)
	v.TMDBVideoID = ptrString("vid-001")
	id, err := repo.Upsert(ctx, v)
	require.NoError(t, err)
	require.NotZero(t, id)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Official Trailer", got.Name)
	assert.True(t, got.Official)
}

func TestVideosRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewVideosRepository(db)
	_, err := repo.Get(context.Background(), 9999)
	require.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestVideosRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
	require.NoError(t, err)
	repo := NewVideosRepository(db)

	v := sampleVideo(seriesID, "Teaser", "Teaser", false)
	v.TMDBVideoID = ptrString("vid-002")

	id1, err := repo.Upsert(ctx, v)
	require.NoError(t, err)
	id2, err := repo.Upsert(ctx, v)
	require.NoError(t, err)
	assert.Equal(t, id1, id2, "natural-key upsert must resolve to the same id")
}

func TestVideosRepository_PartialUnique_AllowsNullTMDB(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
	require.NoError(t, err)
	repo := NewVideosRepository(db)

	a := sampleVideo(seriesID, "Curated A", "Featurette", false)
	a.TMDBVideoID = nil
	id1, err := repo.Upsert(ctx, a)
	require.NoError(t, err)

	b := sampleVideo(seriesID, "Curated B", "Featurette", false)
	b.TMDBVideoID = nil
	id2, err := repo.Upsert(ctx, b)
	require.NoError(t, err)
	assert.NotEqual(t, id1, id2,
		"two NULL-tmdb_video_id rows must coexist — partial unique excludes them")
}

func TestVideosRepository_ListBySeriesAndType(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Andor"))
	require.NoError(t, err)
	repo := NewVideosRepository(db)

	trailer := sampleVideo(seriesID, "Official Trailer", "Trailer", true)
	trailer.TMDBVideoID = ptrString("vid-t1")
	_, err = repo.Upsert(ctx, trailer)
	require.NoError(t, err)

	teaser := sampleVideo(seriesID, "Teaser", "Teaser", true)
	teaser.TMDBVideoID = ptrString("vid-tz1")
	_, err = repo.Upsert(ctx, teaser)
	require.NoError(t, err)

	trailers, err := repo.ListBySeriesAndType(ctx, seriesID, "Trailer")
	require.NoError(t, err)
	require.Len(t, trailers, 1)
	assert.Equal(t, "Official Trailer", trailers[0].Name)

	all, err := repo.ListBySeries(ctx, seriesID)
	require.NoError(t, err)
	assert.Len(t, all, 2)
}
