package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/torrentsync"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestTorrentMovieMapRepository_UpsertNew(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewTorrentMovieMapRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance:      "alpha",
				Hash:          "aaaa",
				RadarrMovieID: 42,
				Source:        torrentsync.MovieMapSourceWebhook,
				Provenance:    torrentsync.MovieProvenanceRadarrSearch,
				CreatedAt:     now,
			}))

			var m database.TorrentMovieMapModel
			require.NoError(t, db.First(&m, "instance_name = ? AND torrent_hash = ?", "alpha", "aaaa").Error)
			assert.Equal(t, domain.RadarrMovieID(42), m.RadarrMovieID)
			assert.Equal(t, string(torrentsync.MovieMapSourceWebhook), m.Source)
			assert.Equal(t, string(torrentsync.MovieProvenanceRadarrSearch), m.Provenance)
			assert.True(t, m.CreatedAt.Equal(now))
		})
	}
}

func TestTorrentMovieMapRepository_UpsertExisting_FirstSourceWins(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewTorrentMovieMapRepository(db)
			ctx := context.Background()

			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance:      "alpha",
				Hash:          "bbbb",
				RadarrMovieID: 7,
				Source:        torrentsync.MovieMapSourceWebhook,
				Provenance:    torrentsync.MovieProvenanceRadarrSearch,
				CreatedAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			}))

			// Second insert with a different (lower-priority) source and a
			// different provenance. Repo MUST keep radarr_movie_id / source /
			// provenance from the first row and touch only created_at.
			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance:      "alpha",
				Hash:          "bbbb",
				RadarrMovieID: 999,
				Source:        torrentsync.MovieMapSourceRadarrHistory,
				Provenance:    torrentsync.MovieProvenanceManualImport,
				CreatedAt:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			}))

			var m database.TorrentMovieMapModel
			require.NoError(t, db.First(&m, "instance_name = ? AND torrent_hash = ?", "alpha", "bbbb").Error)
			assert.Equal(t, domain.RadarrMovieID(7), m.RadarrMovieID, "radarr_movie_id must not change")
			assert.Equal(t, string(torrentsync.MovieMapSourceWebhook), m.Source, "source must not change")
			assert.Equal(t, string(torrentsync.MovieProvenanceRadarrSearch), m.Provenance, "provenance must not change")
		})
	}
}

func TestTorrentMovieMapRepository_UpsertValidation(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewTorrentMovieMapRepository(db)
			ctx := context.Background()

			ok := torrentsync.MovieMapRow{
				Instance:      "alpha",
				Hash:          "cccc",
				RadarrMovieID: 1,
				Source:        torrentsync.MovieMapSourceRadarrQueue,
				Provenance:    torrentsync.MovieProvenanceRadarrSearch,
			}

			missingInstance := ok
			missingInstance.Instance = ""
			require.Error(t, r.Upsert(ctx, missingInstance))

			missingHash := ok
			missingHash.Hash = ""
			require.Error(t, r.Upsert(ctx, missingHash))

			missingMovie := ok
			missingMovie.RadarrMovieID = 0
			require.Error(t, r.Upsert(ctx, missingMovie))

			missingSource := ok
			missingSource.Source = ""
			require.Error(t, r.Upsert(ctx, missingSource))

			missingProvenance := ok
			missingProvenance.Provenance = ""
			require.Error(t, r.Upsert(ctx, missingProvenance))

			// The valid row still lands, and created_at is defaulted from
			// the repo clock when the caller leaves it zero.
			require.NoError(t, r.Upsert(ctx, ok))
			var m database.TorrentMovieMapModel
			require.NoError(t, db.First(&m, "instance_name = ? AND torrent_hash = ?", "alpha", "cccc").Error)
			assert.False(t, m.CreatedAt.IsZero())
		})
	}
}

func TestTorrentMovieMapRepository_CrossInstanceIsolation(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewTorrentMovieMapRepository(db)
			ctx := context.Background()

			// Same hash on two instances → two rows with different movie ids.
			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance: "alpha", Hash: "ffff", RadarrMovieID: 1,
				Source:     torrentsync.MovieMapSourceWebhook,
				Provenance: torrentsync.MovieProvenanceRadarrSearch,
			}))
			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance: "beta", Hash: "ffff", RadarrMovieID: 2,
				Source:     torrentsync.MovieMapSourceWebhook,
				Provenance: torrentsync.MovieProvenanceRadarrSearch,
			}))

			var count int64
			require.NoError(t, db.Model(&database.TorrentMovieMapModel{}).
				Where("torrent_hash = ?", "ffff").Count(&count).Error)
			assert.Equal(t, int64(2), count)
		})
	}
}

func TestTorrentMovieMapRepository_HashesForMovie(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewTorrentMovieMapRepository(db)
			ctx := context.Background()

			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance: "alpha", Hash: "aaaa", RadarrMovieID: 42,
				Source:     torrentsync.MovieMapSourceWebhook,
				Provenance: torrentsync.MovieProvenanceRadarrSearch,
			}))
			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance: "alpha", Hash: "bbbb", RadarrMovieID: 42,
				Source:     torrentsync.MovieMapSourceRadarrQueue,
				Provenance: torrentsync.MovieProvenanceManualImport,
			}))
			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance: "alpha", Hash: "cccc", RadarrMovieID: 99,
				Source:     torrentsync.MovieMapSourceRadarrHistory,
				Provenance: torrentsync.MovieProvenanceRadarrSearch,
			}))
			// Same movie id on another instance must NOT leak in.
			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance: "beta", Hash: "dddd", RadarrMovieID: 42,
				Source:     torrentsync.MovieMapSourceWebhook,
				Provenance: torrentsync.MovieProvenanceRadarrSearch,
			}))

			got, err := r.HashesForMovie(ctx, "alpha", 42)
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"aaaa", "bbbb"}, got)
		})
	}
}

func TestTorrentMovieMapRepository_HashesForMovie_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewTorrentMovieMapRepository(db)
			ctx := context.Background()

			got, err := r.HashesForMovie(ctx, "alpha", 1234)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

// UpsertTx shares the Upsert body but is the entrypoint the webhook path
// (B1.2) will call inside a tx scope. Covered separately so the port
// method is exercised, not just compile-time asserted.
func TestTorrentMovieMapRepository_UpsertTx_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewTorrentMovieMapRepository(db)
			ctx := context.Background()

			require.NoError(t, r.UpsertTx(ctx, torrentsync.MovieMapRow{
				Instance: "alpha", Hash: "eeee", RadarrMovieID: 5,
				Source:     torrentsync.MovieMapSourceWebhook,
				Provenance: torrentsync.MovieProvenanceManualImport,
			}))
			require.Error(t, r.UpsertTx(ctx, torrentsync.MovieMapRow{
				Instance: "alpha", Hash: "eeee", RadarrMovieID: 5,
				Source:     torrentsync.MovieMapSourceWebhook,
				Provenance: "",
			}), "UpsertTx must apply the same validation as Upsert")

			got, err := r.HashesForMovie(ctx, "alpha", 5)
			require.NoError(t, err)
			assert.Equal(t, []string{"eeee"}, got)
		})
	}
}

// EntriesForMovie is the B1.4 read port: HashesForMovie plus the
// source/provenance columns, ordered torrent_hash ASC. Mirrors the
// HashesForMovie cases (populated / empty / wrong instance).
func TestTorrentMovieMapRepository_EntriesForMovie(t *testing.T) {
	t.Parallel()
	for _, backend := range qbitSettingsBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewTorrentMovieMapRepository(db)
			ctx := context.Background()

			// Inserted out of hash order to prove the ORDER BY.
			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance: "alpha", Hash: "bbbb", RadarrMovieID: 42,
				Source:     torrentsync.MovieMapSourceRadarrQueue,
				Provenance: torrentsync.MovieProvenanceManualImport,
			}))
			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance: "alpha", Hash: "aaaa", RadarrMovieID: 42,
				Source:     torrentsync.MovieMapSourceWebhook,
				Provenance: torrentsync.MovieProvenanceRadarrSearch,
			}))
			// Different movie on the same instance must not leak in.
			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance: "alpha", Hash: "cccc", RadarrMovieID: 99,
				Source:     torrentsync.MovieMapSourceRadarrHistory,
				Provenance: torrentsync.MovieProvenanceRadarrSearch,
			}))
			// Same movie id on another instance must not leak in either.
			require.NoError(t, r.Upsert(ctx, torrentsync.MovieMapRow{
				Instance: "beta", Hash: "dddd", RadarrMovieID: 42,
				Source:     torrentsync.MovieMapSourceWebhook,
				Provenance: torrentsync.MovieProvenanceRadarrSearch,
			}))

			got, err := r.EntriesForMovie(ctx, "alpha", 42)
			require.NoError(t, err)
			require.Len(t, got, 2)
			assert.Equal(t, []torrentsync.MovieMapEntry{
				{
					Hash:       "aaaa",
					Source:     torrentsync.MovieMapSourceWebhook,
					Provenance: torrentsync.MovieProvenanceRadarrSearch,
				},
				{
					Hash:       "bbbb",
					Source:     torrentsync.MovieMapSourceRadarrQueue,
					Provenance: torrentsync.MovieProvenanceManualImport,
				},
			}, got)

			// Unknown movie id → empty, no error.
			empty, err := r.EntriesForMovie(ctx, "alpha", 1234)
			require.NoError(t, err)
			assert.Empty(t, empty)

			// Wrong instance → empty, no error.
			wrongInstance, err := r.EntriesForMovie(ctx, "gamma", 42)
			require.NoError(t, err)
			assert.Empty(t, wrongInstance)
		})
	}
}
