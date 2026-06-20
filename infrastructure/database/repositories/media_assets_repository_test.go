package repositories

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/media"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

const sampleHash = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

func TestMediaAssetsRepository_Upsert_EmptyHash(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMediaAssetsRepository(db)
			err := repo.Upsert(context.Background(), media.Asset{Hash: "", UpstreamURL: "https://x", Status: media.StatusPending})
			require.Error(t, err)
		})
	}
}

func TestMediaAssetsRepository_PendingThenStored(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
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
		})
	}
}

func TestMediaAssetsRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMediaAssetsRepository(db)
			_, err := repo.Get(context.Background(), sampleHash)
			require.True(t, errors.Is(err, ports.ErrNotFound))

			var typed *sharedErrors.MediaAssetNotFoundError
			require.True(t, errors.As(err, &typed), "Get NotFound must expose typed MediaAssetNotFoundError via errors.As")
			require.Equal(t, "hash", typed.Kind)
			require.Equal(t, sampleHash, typed.Key)
		})
	}
}

func TestMediaAssetsRepository_GetByUpstreamURL(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
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

			var typed *sharedErrors.MediaAssetNotFoundError
			require.True(t, errors.As(err, &typed), "GetByUpstreamURL NotFound must expose typed MediaAssetNotFoundError via errors.As")
			require.Equal(t, "source_url", typed.Kind)
			require.Equal(t, "https://nope.example/x.jpg", typed.Key)
		})
	}
}

func TestMediaAssetsRepository_TouchLastAccess(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
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
		})
	}
}

func TestMediaAssetsRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewMediaAssetsRepository(db)

			a := media.Asset{
				Hash: sampleHash, UpstreamURL: "https://image.tmdb.org/t/p/w342/abc.jpg",
				Kind: "poster_w342", Status: media.StatusStored,
				ContentType: "image/jpeg", Size: 100,
			}
			for range 3 {
				require.NoError(t, repo.Upsert(ctx, a))
			}
			got, err := repo.Get(ctx, sampleHash)
			require.NoError(t, err)
			assert.Equal(t, "poster_w342", got.Kind)
		})
	}
}

func TestMediaAssetsRepository_HashForSourceURL_Stored(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
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
		})
	}
}

func TestMediaAssetsRepository_HashForSourceURL_PendingMisses(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewMediaAssetsRepository(db)

			url := "https://image.tmdb.org/t/p/w342/abc.jpg"
			require.NoError(t, repo.Upsert(ctx, media.Asset{
				Hash: sampleHash, UpstreamURL: url, Kind: "poster_w342",
				Status: media.StatusPending,
			}))

			_, err := repo.HashForSourceURL(ctx, url)
			require.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}

func TestMediaAssetsRepository_HashForSourceURL_FailedMisses(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewMediaAssetsRepository(db)

			url := "https://image.tmdb.org/t/p/w342/abc.jpg"
			require.NoError(t, repo.Upsert(ctx, media.Asset{
				Hash: sampleHash, UpstreamURL: url, Kind: "poster_w342",
				Status: media.StatusFailed,
			}))

			_, err := repo.HashForSourceURL(ctx, url)
			require.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}

func TestMediaAssetsRepository_HashForSourceURL_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMediaAssetsRepository(db)
			_, err := repo.HashForSourceURL(context.Background(), "")
			require.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}

func TestMediaAssetsRepository_HashForSourceURL_Unknown(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMediaAssetsRepository(db)
			_, err := repo.HashForSourceURL(context.Background(), "https://nope.example/x.jpg")
			require.ErrorIs(t, err, ports.ErrNotFound)

			var typed *sharedErrors.MediaAssetNotFoundError
			require.True(t, errors.As(err, &typed), "HashForSourceURL Unknown must expose typed MediaAssetNotFoundError via errors.As")
			require.Equal(t, "source_url", typed.Kind)
			require.Equal(t, "https://nope.example/x.jpg", typed.Key)
		})
	}
}

func TestMediaAssetsRepository_EnsurePending_InsertsNewRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewMediaAssetsRepository(db)
			hash := strings.Repeat("a", 64)
			url := "https://image.tmdb.org/t/p/w342/abc.jpg"
			require.NoError(t, repo.EnsurePending(ctx, hash, url, "poster_w342"))

			got, err := repo.Get(ctx, hash)
			require.NoError(t, err)
			assert.Equal(t, hash, got.Hash)
			assert.Equal(t, url, got.UpstreamURL)
			assert.Equal(t, "poster_w342", got.Kind)
			assert.Equal(t, media.StatusPending, got.Status)
		})
	}
}

func TestMediaAssetsRepository_EnsurePending_IsIdempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewMediaAssetsRepository(db)
			hash := strings.Repeat("b", 64)
			url := "https://example.com/x.jpg"
			// First call inserts pending.
			require.NoError(t, repo.EnsurePending(ctx, hash, url, "poster_w342"))
			// Now Upsert lifts it to stored.
			require.NoError(t, repo.Upsert(ctx, media.Asset{
				Hash: hash, UpstreamURL: url,
				Kind: "poster_w342", ContentType: "image/jpeg", Size: 100,
				Status: media.StatusStored,
			}))
			// Second EnsurePending must NOT downgrade to pending.
			require.NoError(t, repo.EnsurePending(ctx, hash, url, "poster_w342"))
			got, err := repo.Get(ctx, hash)
			require.NoError(t, err)
			assert.Equal(t, media.StatusStored, got.Status, "EnsurePending must NOT downgrade a stored row")
			assert.Equal(t, "image/jpeg", got.ContentType, "EnsurePending must preserve content_type from the stored row")
			assert.Equal(t, int64(100), got.Size, "EnsurePending must preserve size_bytes from the stored row")
		})
	}
}

func TestMediaAssetsRepository_EnsurePending_PreservesFailedStatus(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewMediaAssetsRepository(db)
			hash := strings.Repeat("d", 64)
			url := "https://example.com/dead.jpg"
			require.NoError(t, repo.Upsert(ctx, media.Asset{
				Hash: hash, UpstreamURL: url, Kind: "poster_w342", Status: media.StatusFailed,
			}))
			require.NoError(t, repo.EnsurePending(ctx, hash, url, "poster_w342"))
			got, err := repo.Get(ctx, hash)
			require.NoError(t, err)
			assert.Equal(t, media.StatusFailed, got.Status, "EnsurePending must NOT overwrite a failed row")
		})
	}
}

func TestMediaAssetsRepository_EnsurePending_RejectsEmptyArgs(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewMediaAssetsRepository(db)
			require.Error(t, repo.EnsurePending(ctx, "", "https://x", "poster_w342"))
			require.Error(t, repo.EnsurePending(ctx, strings.Repeat("a", 64), "", "poster_w342"))
		})
	}
}

func TestMediaAssetsRepository_GetSourceURLByHash(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewMediaAssetsRepository(db)
			hash := strings.Repeat("c", 64)
			url := "https://example.com/y.jpg"
			require.NoError(t, repo.EnsurePending(ctx, hash, url, "backdrop_w1280"))

			gotURL, kind, status, err := repo.GetSourceURLByHash(ctx, hash)
			require.NoError(t, err)
			assert.Equal(t, url, gotURL)
			assert.Equal(t, "backdrop_w1280", kind)
			assert.Equal(t, media.StatusPending, status)

			unknownHash := strings.Repeat("z", 64)
			_, _, _, err = repo.GetSourceURLByHash(ctx, unknownHash)
			require.ErrorIs(t, err, ports.ErrNotFound)

			var typed *sharedErrors.MediaAssetNotFoundError
			require.True(t, errors.As(err, &typed), "GetSourceURLByHash NotFound must expose typed MediaAssetNotFoundError via errors.As")
			require.Equal(t, "hash", typed.Kind)
			require.Equal(t, unknownHash, typed.Key)

			_, _, _, err = repo.GetSourceURLByHash(ctx, "")
			require.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}
