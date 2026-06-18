package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/torrentsync"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestTorrentSeriesMapRepository_UpsertNew(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	r := NewTorrentSeriesMapRepository(db)
	ctx := context.Background()

	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	row := torrentsync.MapRow{
		Instance:     "alpha",
		Hash:         "aaaa",
		SeriesID:     42,
		SeasonNumber: 3,
		Source:       torrentsync.MapSourceWebhook,
		CreatedAt:    now,
	}
	require.NoError(t, r.Upsert(ctx, row))

	var m database.TorrentSeriesMapModel
	require.NoError(t, db.First(&m, "instance_name = ? AND torrent_hash = ?", "alpha", "aaaa").Error)
	assert.Equal(t, domain.SonarrSeriesID(42), m.SeriesID)
	require.NotNil(t, m.SeasonNumber)
	assert.Equal(t, 3, *m.SeasonNumber)
	assert.Equal(t, string(torrentsync.MapSourceWebhook), m.Source)
	assert.True(t, m.CreatedAt.Equal(now))
}

func TestTorrentSeriesMapRepository_UpsertExisting_FirstSourceWins(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	r := NewTorrentSeriesMapRepository(db)
	ctx := context.Background()

	first := torrentsync.MapRow{
		Instance:     "alpha",
		Hash:         "bbbb",
		SeriesID:     7,
		SeasonNumber: 1,
		Source:       torrentsync.MapSourceWebhook,
		CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, r.Upsert(ctx, first))

	// Second insert with a different (lower-priority) source. Repo
	// MUST keep series_id/season/source from the first row and touch
	// only created_at.
	second := torrentsync.MapRow{
		Instance:     "alpha",
		Hash:         "bbbb",
		SeriesID:     999,
		SeasonNumber: 99,
		Source:       torrentsync.MapSourceHistory,
		CreatedAt:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, r.Upsert(ctx, second))

	var m database.TorrentSeriesMapModel
	require.NoError(t, db.First(&m, "instance_name = ? AND torrent_hash = ?", "alpha", "bbbb").Error)
	assert.Equal(t, domain.SonarrSeriesID(7), m.SeriesID, "series_id must not change")
	require.NotNil(t, m.SeasonNumber)
	assert.Equal(t, 1, *m.SeasonNumber, "season_number must not change")
	assert.Equal(t, string(torrentsync.MapSourceWebhook), m.Source, "source must not change")
}

func TestTorrentSeriesMapRepository_UpsertMissingSeriesID(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	r := NewTorrentSeriesMapRepository(db)
	ctx := context.Background()

	err := r.Upsert(ctx, torrentsync.MapRow{
		Instance: "alpha",
		Hash:     "cccc",
		SeriesID: 0,
		Source:   torrentsync.MapSourceGrabRecord,
	})
	require.Error(t, err)
}

func TestTorrentSeriesMapRepository_UpsertMissingInstanceOrHash(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	r := NewTorrentSeriesMapRepository(db)
	ctx := context.Background()

	require.Error(t, r.Upsert(ctx, torrentsync.MapRow{Hash: "h", SeriesID: 1}))
	require.Error(t, r.Upsert(ctx, torrentsync.MapRow{Instance: "i", SeriesID: 1}))
}

func TestTorrentSeriesMapRepository_NullableSeasonNumber(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	r := NewTorrentSeriesMapRepository(db)
	ctx := context.Background()

	// SeasonNumber = 0 → NULL in DB.
	require.NoError(t, r.Upsert(ctx, torrentsync.MapRow{
		Instance: "alpha", Hash: "dddd", SeriesID: 5,
		Source: torrentsync.MapSourceQueue,
	}))
	var m database.TorrentSeriesMapModel
	require.NoError(t, db.First(&m, "instance_name = ? AND torrent_hash = ?", "alpha", "dddd").Error)
	assert.Nil(t, m.SeasonNumber)

	// Non-zero season persists.
	require.NoError(t, r.Upsert(ctx, torrentsync.MapRow{
		Instance: "alpha", Hash: "eeee", SeriesID: 5, SeasonNumber: 2,
		Source: torrentsync.MapSourceQueue,
	}))
	var m2 database.TorrentSeriesMapModel
	require.NoError(t, db.First(&m2, "instance_name = ? AND torrent_hash = ?", "alpha", "eeee").Error)
	require.NotNil(t, m2.SeasonNumber)
	assert.Equal(t, 2, *m2.SeasonNumber)
}

func TestTorrentSeriesMapRepository_CrossInstanceIsolation(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	r := NewTorrentSeriesMapRepository(db)
	ctx := context.Background()

	// Same hash on two instances → two rows with potentially different
	// series_id.
	require.NoError(t, r.Upsert(ctx, torrentsync.MapRow{
		Instance: "alpha", Hash: "ffff", SeriesID: 1,
		Source: torrentsync.MapSourceWebhook,
	}))
	require.NoError(t, r.Upsert(ctx, torrentsync.MapRow{
		Instance: "beta", Hash: "ffff", SeriesID: 2,
		Source: torrentsync.MapSourceWebhook,
	}))

	var count int64
	require.NoError(t, db.Model(&database.TorrentSeriesMapModel{}).Where("torrent_hash = ?", "ffff").Count(&count).Error)
	assert.Equal(t, int64(2), count)
}

func TestTorrentSeriesMapRepository_HashesForSeries(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	r := NewTorrentSeriesMapRepository(db)
	ctx := context.Background()

	require.NoError(t, r.Upsert(ctx, torrentsync.MapRow{
		Instance: "alpha", Hash: "aaaa", SeriesID: 42,
		Source: torrentsync.MapSourceWebhook,
	}))
	require.NoError(t, r.Upsert(ctx, torrentsync.MapRow{
		Instance: "alpha", Hash: "bbbb", SeriesID: 42,
		Source: torrentsync.MapSourceQueue,
	}))
	require.NoError(t, r.Upsert(ctx, torrentsync.MapRow{
		Instance: "alpha", Hash: "cccc", SeriesID: 99,
		Source: torrentsync.MapSourceHistory,
	}))

	got, err := r.HashesForSeries(ctx, "alpha", 42)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"aaaa", "bbbb"}, got)
}

func TestTorrentSeriesMapRepository_HashesForSeries_Empty(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	r := NewTorrentSeriesMapRepository(db)
	ctx := context.Background()

	got, err := r.HashesForSeries(ctx, "alpha", 1234)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestGrabRepository_FindSeriesByTorrentHashes(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	grabRepo := NewGrabRepository(db)
	ctx := context.Background()

	// Empty input is a no-op.
	rows, err := grabRepo.FindSeriesByTorrentHashes(ctx, "alpha", nil)
	require.NoError(t, err)
	assert.Empty(t, rows)
}
