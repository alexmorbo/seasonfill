package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/media"
)

const sampleHash = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

func TestMediaAssetsRepository_Upsert_EmptyHash(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewMediaAssetsRepository(db)
	err := repo.Upsert(context.Background(), media.Asset{Hash: "", UpstreamURL: "https://x", Status: media.StatusPending})
	require.Error(t, err)
}

func TestMediaAssetsRepository_PendingThenStored(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewMediaAssetsRepository(db)

	// 1. Insert pending row.
	require.NoError(t, repo.Upsert(ctx, media.Asset{
		Hash:        sampleHash,
		UpstreamURL: "https://image.tmdb.org/t/p/w342/abc.jpg",
		Kind:        "poster_w342",
		Status:      media.StatusPending,
	}))
	got, err := repo.Get(ctx, sampleHash)
	require.NoError(t, err)
	assert.Equal(t, media.StatusPending, got.Status)

	// 2. Upsert stored — same hash, with content-type + size.
	require.NoError(t, repo.Upsert(ctx, media.Asset{
		Hash:        sampleHash,
		UpstreamURL: "https://image.tmdb.org/t/p/w342/abc.jpg",
		Kind:        "poster_w342",
		ContentType: "image/jpeg",
		Size:        4321,
		Status:      media.StatusStored,
	}))
	got, err = repo.Get(ctx, sampleHash)
	require.NoError(t, err)
	assert.Equal(t, media.StatusStored, got.Status)
	assert.Equal(t, "image/jpeg", got.ContentType)
	assert.Equal(t, int64(4321), got.Size)
}

func TestMediaAssetsRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewMediaAssetsRepository(db)
	_, err := repo.Get(context.Background(), sampleHash)
	require.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestMediaAssetsRepository_GetByUpstreamURL(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewMediaAssetsRepository(db)

	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	require.NoError(t, repo.Upsert(ctx, media.Asset{
		Hash: sampleHash, UpstreamURL: url, Kind: "poster_w342",
		Status: media.StatusStored, ContentType: "image/jpeg", Size: 17,
	}))

	got, err := repo.GetByUpstreamURL(ctx, url)
	require.NoError(t, err)
	assert.Equal(t, sampleHash, got.Hash)
	assert.Equal(t, "poster_w342", got.Kind)

	_, err = repo.GetByUpstreamURL(ctx, "https://nope.example/x.jpg")
	require.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestMediaAssetsRepository_TouchLastAccess(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewMediaAssetsRepository(db)

	// Empty hash short-circuits silently — must not error.
	require.NoError(t, repo.TouchLastAccess(ctx, ""))

	require.NoError(t, repo.Upsert(ctx, media.Asset{
		Hash: sampleHash, UpstreamURL: "https://x.example/a.jpg", Kind: "k", Status: media.StatusStored,
	}))
	require.NoError(t, repo.TouchLastAccess(ctx, sampleHash))
	// Row stays intact.
	got, err := repo.Get(ctx, sampleHash)
	require.NoError(t, err)
	assert.Equal(t, media.StatusStored, got.Status)
}

func TestMediaAssetsRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewMediaAssetsRepository(db)

	a := media.Asset{
		Hash: sampleHash, UpstreamURL: "https://image.tmdb.org/t/p/w342/abc.jpg",
		Kind: "poster_w342", Status: media.StatusStored,
		ContentType: "image/jpeg", Size: 100,
	}
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Upsert(ctx, a))
	}
	got, err := repo.Get(ctx, sampleHash)
	require.NoError(t, err)
	assert.Equal(t, "poster_w342", got.Kind)
}

func TestMediaAssetsRepository_HashForSourceURL_Stored(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewMediaAssetsRepository(db)

	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	require.NoError(t, repo.Upsert(ctx, media.Asset{
		Hash: sampleHash, UpstreamURL: url, Kind: "poster_w342",
		Status: media.StatusStored, ContentType: "image/jpeg", Size: 17,
	}))

	got, err := repo.HashForSourceURL(ctx, url)
	require.NoError(t, err)
	assert.Equal(t, sampleHash, got)
}

func TestMediaAssetsRepository_HashForSourceURL_PendingMisses(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewMediaAssetsRepository(db)

	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	require.NoError(t, repo.Upsert(ctx, media.Asset{
		Hash: sampleHash, UpstreamURL: url, Kind: "poster_w342",
		Status: media.StatusPending,
	}))

	_, err := repo.HashForSourceURL(ctx, url)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestMediaAssetsRepository_HashForSourceURL_FailedMisses(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewMediaAssetsRepository(db)

	url := "https://image.tmdb.org/t/p/w342/abc.jpg"
	require.NoError(t, repo.Upsert(ctx, media.Asset{
		Hash: sampleHash, UpstreamURL: url, Kind: "poster_w342",
		Status: media.StatusFailed,
	}))

	_, err := repo.HashForSourceURL(ctx, url)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestMediaAssetsRepository_HashForSourceURL_Empty(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewMediaAssetsRepository(db)
	_, err := repo.HashForSourceURL(context.Background(), "")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestMediaAssetsRepository_HashForSourceURL_Unknown(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewMediaAssetsRepository(db)
	_, err := repo.HashForSourceURL(context.Background(), "https://nope.example/x.jpg")
	require.ErrorIs(t, err, ports.ErrNotFound)
}
