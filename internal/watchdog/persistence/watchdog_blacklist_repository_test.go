package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

func sampleBlacklistEntry(t *testing.T, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int) regrab.BlacklistEntry {
	t.Helper()
	e, err := regrab.NewBlacklistEntry(
		instance, seriesID, season, 3,
		regrab.ReasonConsecutiveNoBetter,
		time.Now().UTC(),
	)
	require.NoError(t, err)
	return e
}

// seedBlacklistInstance creates a sonarr_instance row so the FK CASCADE
// constraint on watchdog_blacklist passes on Postgres (SQLite has no
// FK enforcement by default but the column shape is the same).
func seedBlacklistInstance(t *testing.T, ctx context.Context, repo *WatchdogBlacklistRepository, name domain.InstanceName) {
	t.Helper()
	db := repo.db
	now := time.Now().UTC()
	require.NoError(t, db.WithContext(ctx).Save(&database.SonarrInstanceModel{
		Name:      string(name),
		URL:       "http://" + string(name),
		Mode:      "managed",
		CreatedAt: now,
		UpdatedAt: now,
	}).Error)
}

func TestWatchdogBlacklistRepository_Upsert_Insert(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			ctx := context.Background()
			seedBlacklistInstance(t, ctx, repo, "homelab")

			in := sampleBlacklistEntry(t, "homelab", 122, 2)
			require.NoError(t, repo.Upsert(ctx, in))

			got, err := repo.Find(ctx, "homelab", 122, 2)
			require.NoError(t, err)
			assert.Equal(t, in.InstanceName, got.InstanceName)
			assert.Equal(t, in.SeriesID, got.SeriesID)
			assert.Equal(t, in.SeasonNumber, got.SeasonNumber)
			assert.Equal(t, in.Reason, got.Reason)
			assert.Equal(t, in.Consecutive, got.Consecutive)
			assert.False(t, got.CreatedAt.IsZero())
			assert.Nil(t, got.TTLUntil, "v1 always writes NULL TTLUntil")
		})
	}
}

func TestWatchdogBlacklistRepository_Upsert_UpdatesOnConflict(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			ctx := context.Background()
			seedBlacklistInstance(t, ctx, repo, "homelab")

			first := sampleBlacklistEntry(t, "homelab", 122, 2)
			require.NoError(t, repo.Upsert(ctx, first))

			second := sampleBlacklistEntry(t, "homelab", 122, 2)
			second.Consecutive = 5
			second.Reason = regrab.ReasonQbitUnreachablePersistent
			second.CreatedAt = first.CreatedAt.Add(time.Hour)
			require.NoError(t, repo.Upsert(ctx, second))

			got, err := repo.Find(ctx, "homelab", 122, 2)
			require.NoError(t, err)
			assert.Equal(t, 5, got.Consecutive, "newer consecutive wins")
			assert.Equal(t, regrab.ReasonQbitUnreachablePersistent, got.Reason)
		})
	}
}

func TestWatchdogBlacklistRepository_Find_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			_, err := repo.Find(context.Background(), "ghost", 1, 1)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestWatchdogBlacklistRepository_DeleteByTriple(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			ctx := context.Background()
			seedBlacklistInstance(t, ctx, repo, "homelab")

			in := sampleBlacklistEntry(t, "homelab", 122, 2)
			require.NoError(t, repo.Upsert(ctx, in))
			require.NoError(t, repo.DeleteByTriple(ctx, "homelab", 122, 2))

			_, err := repo.Find(ctx, "homelab", 122, 2)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestWatchdogBlacklistRepository_DeleteByTriple_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			err := repo.DeleteByTriple(context.Background(), "ghost", 1, 1)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestWatchdogBlacklistRepository_ListByInstance(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			ctx := context.Background()
			seedBlacklistInstance(t, ctx, repo, "homelab")
			seedBlacklistInstance(t, ctx, repo, "4k")

			now := time.Now().UTC().Truncate(time.Second)
			older, err := regrab.NewBlacklistEntry("homelab", 100, 1, 3, regrab.ReasonConsecutiveNoBetter, now.Add(-time.Hour))
			require.NoError(t, err)
			newer, err := regrab.NewBlacklistEntry("homelab", 200, 2, 3, regrab.ReasonConsecutiveNoBetter, now)
			require.NoError(t, err)
			other, err := regrab.NewBlacklistEntry("4k", 300, 1, 3, regrab.ReasonConsecutiveNoBetter, now)
			require.NoError(t, err)
			require.NoError(t, repo.Upsert(ctx, older))
			require.NoError(t, repo.Upsert(ctx, newer))
			require.NoError(t, repo.Upsert(ctx, other))

			rows, err := repo.ListByInstance(ctx, "homelab")
			require.NoError(t, err)
			require.Len(t, rows, 2, "must include only homelab rows")
			assert.Equal(t, domain.SonarrSeriesID(200), rows[0].SeriesID, "newest first")
			assert.Equal(t, domain.SonarrSeriesID(100), rows[1].SeriesID)
		})
	}
}

func TestWatchdogBlacklistRepository_ListByInstance_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			rows, err := repo.ListByInstance(context.Background(), "ghost")
			require.NoError(t, err)
			assert.Empty(t, rows)
		})
	}
}

func TestWatchdogBlacklistRepository_TripleUniqueness(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			ctx := context.Background()
			seedBlacklistInstance(t, ctx, repo, "homelab")
			seedBlacklistInstance(t, ctx, repo, "4k")

			// Same series id on a different instance is a separate row.
			require.NoError(t, repo.Upsert(ctx, sampleBlacklistEntry(t, "homelab", 122, 2)))
			require.NoError(t, repo.Upsert(ctx, sampleBlacklistEntry(t, "4k", 122, 2)))

			a, err := repo.Find(ctx, "homelab", 122, 2)
			require.NoError(t, err)
			b, err := repo.Find(ctx, "4k", 122, 2)
			require.NoError(t, err)
			assert.Equal(t, domain.InstanceName("homelab"), a.InstanceName)
			assert.Equal(t, domain.InstanceName("4k"), b.InstanceName)
		})
	}
}

func TestWatchdogBlacklistRepository_ClosedDB(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWatchdogBlacklistRepository(db)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())
			err = repo.Upsert(context.Background(), sampleBlacklistEntry(t, "homelab", 1, 1))
			require.Error(t, err)
		})
	}
}
